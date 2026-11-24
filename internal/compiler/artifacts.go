package compiler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pmezard/go-difflib/difflib"
)

// Artifacts are the three files that make up a contract.
type Artifacts struct {
	Purpose      string
	Protocol     string
	StateMachine string
}

const stateMachineDefault = "{}"

func (c *Compiler) path(name string) string {
	return filepath.Join(c.opts.GovernanceDir, name)
}

func (c *Compiler) loadArtifacts() (Artifacts, error) {
	return readArtifactsFrom(c.opts.GovernanceDir)
}

func readArtifactsFrom(dir string) (Artifacts, error) {
	purpose, err := os.ReadFile(filepath.Join(dir, "purpose.md"))
	if err != nil {
		return Artifacts{}, fmt.Errorf("compiler: read purpose.md: %w", err)
	}
	protocol, err := os.ReadFile(filepath.Join(dir, "protocol.fbs"))
	if err != nil {
		return Artifacts{}, fmt.Errorf("compiler: read protocol.fbs: %w", err)
	}
	state, err := os.ReadFile(filepath.Join(dir, "state_machine.json"))
	if errors.Is(err, fs.ErrNotExist) {
		state = []byte(stateMachineDefault)
	} else if err != nil {
		return Artifacts{}, fmt.Errorf("compiler: read state_machine.json: %w", err)
	}

	return Artifacts{
		Purpose:      string(purpose),
		Protocol:     string(protocol),
		StateMachine: string(state),
	}, nil
}

func (c *Compiler) commitArtifacts(before, after Artifacts) error {
	writes := []struct {
		name   string
		before string
		after  string
	}{
		{"purpose.md", before.Purpose, after.Purpose},
		{"protocol.fbs", before.Protocol, after.Protocol},
		{"state_machine.json", before.StateMachine, after.StateMachine},
	}
	for _, w := range writes {
		if w.before == w.after {
			continue
		}
		if err := writeFileAtomic(c.path(w.name), []byte(w.after)); err != nil {
			return fmt.Errorf("compiler: write %s: %w", w.name, err)
		}
	}
	return nil
}

func changedFiles(before, after Artifacts) []string {
	var files []string
	if before.Purpose != after.Purpose {
		files = append(files, "purpose.md")
	}
	if before.Protocol != after.Protocol {
		files = append(files, "protocol.fbs")
	}
	if before.StateMachine != after.StateMachine {
		files = append(files, "state_machine.json")
	}
	return files
}

func diffArtifacts(before, after Artifacts) (string, error) {
	files := []struct {
		name string
		a    string
		b    string
	}{
		{"purpose.md", before.Purpose, after.Purpose},
		{"protocol.fbs", before.Protocol, after.Protocol},
		{"state_machine.json", before.StateMachine, after.StateMachine},
	}

	var out strings.Builder
	for _, file := range files {
		if file.a == file.b {
			continue
		}
		diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A:        difflib.SplitLines(file.a),
			B:        difflib.SplitLines(file.b),
			FromFile: "a/" + file.name,
			ToFile:   "b/" + file.name,
			Context:  3,
		})
		if err != nil {
			return "", err
		}
		out.WriteString(diff)
	}
	return out.String(), nil
}

type commitMeta struct {
	Timestamp         string     `json:"timestamp"`
	Actor             string     `json:"actor"`
	Intent            string     `json:"intent"`
	Summary           string     `json:"summary,omitempty"`
	Direction         string     `json:"direction"`
	Model             string     `json:"model,omitempty"`
	AffectsInvariants []string   `json:"affects_invariants,omitempty"`
	Files             []string   `json:"files"`
	Patch             string     `json:"patch"`
	Judge             *judgeMeta `json:"judge,omitempty"`
}

func (c *Compiler) writeHistory(in applyInput, changed []string, diff string, now time.Time, judgeRecord *judgeMeta) (string, string, error) {
	stamp := now.UTC().Format("20060102T150405.000000000Z")
	dir := filepath.Join(c.opts.GovernanceDir, "history", stamp+"_"+sanitizeName(in.actor))

	snapshots := []struct {
		name    string
		content string
	}{
		{"purpose.md", in.after.Purpose},
		{"protocol.fbs", in.after.Protocol},
		{"state_machine.json", in.after.StateMachine},
	}
	for _, snapshot := range snapshots {
		if err := writeFileAtomic(filepath.Join(dir, snapshot.name), []byte(snapshot.content)); err != nil {
			return "", "", fmt.Errorf("compiler: write history %s: %w", snapshot.name, err)
		}
	}

	patchPath := filepath.Join(dir, "diff.patch")
	if err := writeFileAtomic(patchPath, []byte(diff)); err != nil {
		return "", "", fmt.Errorf("compiler: write history patch: %w", err)
	}

	meta := commitMeta{
		Timestamp:         now.UTC().Format(time.RFC3339Nano),
		Actor:             in.actor,
		Intent:            in.intent,
		Summary:           in.summary,
		Direction:         in.direction,
		Model:             in.model,
		AffectsInvariants: in.affects,
		Files:             changed,
		Patch:             "diff.patch",
		Judge:             judgeRecord,
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("compiler: encode history meta: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, "meta.json"), append(metaJSON, '\n')); err != nil {
		return "", "", fmt.Errorf("compiler: write history meta: %w", err)
	}

	return dir, patchPath, nil
}

func (c *Compiler) loadLatestSnapshot() (*Artifacts, error) {
	historyDir := filepath.Join(c.opts.GovernanceDir, "history")
	entries, err := os.ReadDir(historyDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("compiler: read history: %w", err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	for _, name := range names {
		dir := filepath.Join(historyDir, name)
		if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
			continue
		}
		artifacts, err := readArtifactsFrom(dir)
		if err != nil {
			return nil, err
		}
		return &artifacts, nil
	}
	return nil, nil
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	file, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer func() { _ = os.Remove(tmp) }()

	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "manual"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			return r
		default:
			return '_'
		}
	}, name)
}
