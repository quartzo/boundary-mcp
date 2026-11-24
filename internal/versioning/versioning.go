// Package versioning implements content-addressed protocol versions.
//
// A version is the SHA-256 hash of a canonical bundle (purpose.md,
// protocol.fbs and state_machine.json). The hash is immutable identity; dates,
// authors, intents and aliases are annotations stored alongside it.
package versioning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	bundleSchema = "boundary-mcp/contract/v1"
	wireSchema   = "boundary-mcp/wire/v1"
)

// Bundle is the three-file contract that a version identifies.
type Bundle struct {
	Purpose      string
	Protocol     string
	StateMachine string
}

// FileDigest records one file in the canonical bundle manifest.
type FileDigest struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
}

// Version is the immutable metadata of a contract version.
type Version struct {
	Version   string       `json:"version"`
	WireHash  string       `json:"wire_hash"`
	CreatedAt time.Time    `json:"created_at"`
	Actor     string       `json:"actor,omitempty"`
	Intent    string       `json:"intent,omitempty"`
	Base      string       `json:"base,omitempty"`
	Files     []FileDigest `json:"files"`
}

// Meta is the annotation supplied when a version is snapshotted.
type Meta struct {
	Actor  string
	Intent string
	Base   string
}

// ActorPin records that an actor adopted a version.
type ActorPin struct {
	Actor     string    `json:"actor"`
	Role      string    `json:"role,omitempty"`
	Version   string    `json:"version"`
	Alias     string    `json:"alias,omitempty"`
	AdoptedAt time.Time `json:"adopted_at"`
	Status    string    `json:"status,omitempty"`
}

// Store persists versions, aliases and actor pins below a governance directory.
type Store struct {
	root string
}

// NewStore returns a Store rooted at governanceDir.
func NewStore(governanceDir string) *Store {
	return &Store{root: governanceDir}
}

// Root returns the governance directory.
func (s *Store) Root() string { return s.root }

// VersionDir returns the directory for a version.
func (s *Store) VersionDir(version string) string {
	return filepath.Join(s.root, "versions", strings.TrimPrefix(version, "sha256:"))
}

// CompatDir returns the compatibility-manifest directory.
func (s *Store) CompatDir() string { return filepath.Join(s.root, "compat") }

// ActorsDir returns the actor-pin directory.
func (s *Store) ActorsDir() string { return filepath.Join(s.root, "actors") }

// AliasesPath returns the alias registry path.
func (s *Store) AliasesPath() string { return filepath.Join(s.root, "aliases.json") }

// LoadBundle reads a bundle from dir. A missing state_machine.json defaults to
// {} to match the compiler's view of an existing governance directory.
func (s *Store) LoadBundle(dir string) (Bundle, error) {
	purpose, err := os.ReadFile(filepath.Join(dir, "purpose.md"))
	if err != nil {
		return Bundle{}, fmt.Errorf("versioning: read purpose.md: %w", err)
	}
	protocol, err := os.ReadFile(filepath.Join(dir, "protocol.fbs"))
	if err != nil {
		return Bundle{}, fmt.Errorf("versioning: read protocol.fbs: %w", err)
	}
	state, err := os.ReadFile(filepath.Join(dir, "state_machine.json"))
	if errors.Is(err, fs.ErrNotExist) {
		state = []byte("{}")
	} else if err != nil {
		return Bundle{}, fmt.Errorf("versioning: read state_machine.json: %w", err)
	}
	return Bundle{
		Purpose:      string(purpose),
		Protocol:     string(protocol),
		StateMachine: string(state),
	}, nil
}

// VersionHash returns the content-addressed version identity.
func (b Bundle) VersionHash() string {
	return hashManifest(bundleSchema, b.digests())
}

// WireHash returns the identity of the wire contract alone.
func (b Bundle) WireHash() string {
	return hashManifest(wireSchema, []FileDigest{digest("protocol.fbs", b.Protocol)})
}

func (b Bundle) digests() []FileDigest {
	files := []FileDigest{
		digest("purpose.md", b.Purpose),
		digest("protocol.fbs", b.Protocol),
		digest("state_machine.json", b.StateMachine),
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files
}

func digest(name, content string) FileDigest {
	sum := sha256.Sum256([]byte(content))
	return FileDigest{
		Name:   name,
		SHA256: "sha256:" + hex.EncodeToString(sum[:]),
		Size:   len(content),
	}
}

func hashManifest(schema string, files []FileDigest) string {
	manifest := struct {
		Schema string       `json:"schema"`
		Files  []FileDigest `json:"files"`
	}{Schema: schema, Files: files}
	data, err := json.Marshal(manifest)
	if err != nil {
		// The manifest contains only strings and integers; this cannot fail.
		panic(err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Snapshot writes an immutable version and optionally binds an alias.
//
// Re-snapshotting identical content returns the existing version; it never
// overwrites a version directory. A missing state_machine.json is materialized
// as {}.
func (s *Store) Snapshot(bundle Bundle, meta Meta, alias string) (Version, error) {
	if strings.TrimSpace(bundle.Purpose) == "" {
		return Version{}, errors.New("versioning: purpose.md is empty")
	}
	if strings.TrimSpace(bundle.Protocol) == "" {
		return Version{}, errors.New("versioning: protocol.fbs is empty")
	}
	if bundle.StateMachine == "" {
		bundle.StateMachine = "{}"
	}

	version := bundle.VersionHash()
	if alias != "" {
		if err := validateAlias(alias); err != nil {
			return Version{}, err
		}
		aliases, err := s.Aliases()
		if err != nil {
			return Version{}, err
		}
		if current, ok := aliases[alias]; ok && current != version {
			return Version{}, fmt.Errorf("versioning: alias %q already points to %s (use versionctl alias -force to move it)", alias, current)
		}
	}

	dir := s.VersionDir(version)
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err == nil {
		existing, _, err := s.loadVersionDir(version)
		if err != nil {
			return Version{}, err
		}
		if alias != "" {
			if err := s.SetAlias(alias, version, false); err != nil {
				return Version{}, err
			}
		}
		return existing, nil
	}

	files := map[string]string{
		"purpose.md":         bundle.Purpose,
		"protocol.fbs":       bundle.Protocol,
		"state_machine.json": bundle.StateMachine,
	}
	for name, content := range files {
		if err := writeFileAtomic(filepath.Join(dir, name), []byte(content)); err != nil {
			return Version{}, fmt.Errorf("versioning: write %s: %w", name, err)
		}
	}

	record := Version{
		Version:   version,
		WireHash:  bundle.WireHash(),
		CreatedAt: time.Now().UTC(),
		Actor:     strings.TrimSpace(meta.Actor),
		Intent:    strings.TrimSpace(meta.Intent),
		Base:      strings.TrimSpace(meta.Base),
		Files:     bundle.digests(),
	}
	if err := writeJSON(filepath.Join(dir, "meta.json"), record); err != nil {
		return Version{}, err
	}
	if alias != "" {
		if err := s.SetAlias(alias, version, false); err != nil {
			return Version{}, err
		}
	}
	return record, nil
}

// Load resolves ref (alias, full hash or unique hash prefix) and returns its
// metadata and artifacts.
func (s *Store) Load(ref string) (Version, Bundle, error) {
	version, err := s.Resolve(ref)
	if err != nil {
		return Version{}, Bundle{}, err
	}
	return s.loadVersionDir(version)
}

func (s *Store) loadVersionDir(version string) (Version, Bundle, error) {
	dir := s.VersionDir(version)
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return Version{}, Bundle{}, fmt.Errorf("versioning: read version %s: %w", version, err)
	}
	var record Version
	if err := json.Unmarshal(data, &record); err != nil {
		return Version{}, Bundle{}, fmt.Errorf("versioning: decode version %s: %w", version, err)
	}
	bundle, err := s.LoadBundle(dir)
	if err != nil {
		return Version{}, Bundle{}, err
	}
	return record, bundle, nil
}

// List returns all snapshotted versions, newest first.
func (s *Store) List() ([]Version, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "versions"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("versioning: read versions: %w", err)
	}

	var versions []Version
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.root, "versions", entry.Name(), "meta.json"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("versioning: read version %s: %w", entry.Name(), err)
		}
		var record Version
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("versioning: decode version %s: %w", entry.Name(), err)
		}
		versions = append(versions, record)
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].CreatedAt.Equal(versions[j].CreatedAt) {
			return versions[i].Version < versions[j].Version
		}
		return versions[i].CreatedAt.After(versions[j].CreatedAt)
	})
	return versions, nil
}

// Resolve turns an alias, full hash or unique hash prefix into a full hash.
func (s *Store) Resolve(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("versioning: version reference is required")
	}

	aliases, err := s.Aliases()
	if err != nil {
		return "", err
	}
	if version, ok := aliases[ref]; ok {
		if _, err := os.Stat(filepath.Join(s.VersionDir(version), "meta.json")); err != nil {
			return "", fmt.Errorf("versioning: alias %q points to missing version %s", ref, version)
		}
		return version, nil
	}

	raw := strings.TrimPrefix(ref, "sha256:")
	raw = strings.ToLower(raw)
	if !isHex(raw) {
		return "", fmt.Errorf("versioning: unknown version or alias %q", ref)
	}
	if len(raw) == 64 {
		version := "sha256:" + raw
		if _, err := os.Stat(filepath.Join(s.VersionDir(version), "meta.json")); err != nil {
			return "", fmt.Errorf("versioning: version %s not found", ref)
		}
		return version, nil
	}
	if len(raw) < 8 {
		return "", fmt.Errorf("versioning: version prefix %q is too short", ref)
	}

	entries, err := os.ReadDir(filepath.Join(s.root, "versions"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("versioning: version %s not found", ref)
	}
	if err != nil {
		return "", fmt.Errorf("versioning: read versions: %w", err)
	}
	var matches []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), raw) {
			matches = append(matches, entry.Name())
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("versioning: version %s not found", ref)
	case 1:
		return "sha256:" + matches[0], nil
	default:
		return "", fmt.Errorf("versioning: version prefix %q is ambiguous (%d matches)", ref, len(matches))
	}
}

// Aliases returns the alias registry.
func (s *Store) Aliases() (map[string]string, error) {
	data, err := os.ReadFile(s.AliasesPath())
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("versioning: read aliases: %w", err)
	}
	var aliases map[string]string
	if err := json.Unmarshal(data, &aliases); err != nil {
		return nil, fmt.Errorf("versioning: decode aliases: %w", err)
	}
	if aliases == nil {
		aliases = map[string]string{}
	}
	return aliases, nil
}

var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func validateAlias(alias string) error {
	if !aliasPattern.MatchString(alias) {
		return fmt.Errorf("versioning: invalid alias %q (want letters, digits, dot, underscore or dash)", alias)
	}
	return nil
}

// SetAlias points alias at ref. Existing aliases are immutable unless force is
// true.
func (s *Store) SetAlias(alias, ref string, force bool) error {
	alias = strings.TrimSpace(alias)
	if err := validateAlias(alias); err != nil {
		return err
	}
	version, err := s.Resolve(ref)
	if err != nil {
		return err
	}

	aliases, err := s.Aliases()
	if err != nil {
		return err
	}
	if current, ok := aliases[alias]; ok && current != version && !force {
		return fmt.Errorf("versioning: alias %q already points to %s (use force to move it)", alias, current)
	}
	aliases[alias] = version
	return s.writeAliases(aliases)
}

func (s *Store) writeAliases(aliases map[string]string) error {
	if aliases == nil {
		aliases = map[string]string{}
	}
	return writeJSON(s.AliasesPath(), aliases)
}

// PinActor records that actor adopted ref. Re-pinning to a different version
// updates the metadata; an identical pin is idempotent. It never changes the
// contract.
func (s *Store) PinActor(actor, role, ref, status string) (ActorPin, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return ActorPin{}, errors.New("versioning: actor is required")
	}
	version, err := s.Resolve(ref)
	if err != nil {
		return ActorPin{}, err
	}
	if strings.TrimSpace(status) == "" {
		status = "active"
	}
	alias := s.aliasFor(version, ref)
	pin := ActorPin{
		Actor:     actor,
		Role:      strings.TrimSpace(role),
		Version:   version,
		Alias:     alias,
		AdoptedAt: time.Now().UTC(),
		Status:    strings.TrimSpace(status),
	}
	path := filepath.Join(s.ActorsDir(), sanitizeName(actor)+".json")
	if data, err := os.ReadFile(path); err == nil {
		var existing ActorPin
		if json.Unmarshal(data, &existing) == nil &&
			existing.Actor == pin.Actor &&
			existing.Role == pin.Role &&
			existing.Version == pin.Version &&
			existing.Alias == pin.Alias &&
			existing.Status == pin.Status {
			return existing, nil
		}
	}
	if err := writeJSON(path, pin); err != nil {
		return ActorPin{}, err
	}
	return pin, nil
}

func (s *Store) aliasFor(version, ref string) string {
	aliases, err := s.Aliases()
	if err != nil {
		return ""
	}
	if aliases[ref] == version {
		return ref
	}
	var names []string
	for name, hash := range aliases {
		if hash == version {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

// Actors returns all actor pins sorted by actor name.
func (s *Store) Actors() ([]ActorPin, error) {
	entries, err := os.ReadDir(s.ActorsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("versioning: read actors: %w", err)
	}
	var pins []ActorPin
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.ActorsDir(), entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("versioning: read actor %s: %w", entry.Name(), err)
		}
		var pin ActorPin
		if err := json.Unmarshal(data, &pin); err != nil {
			return nil, fmt.Errorf("versioning: decode actor %s: %w", entry.Name(), err)
		}
		pins = append(pins, pin)
	}
	sort.Slice(pins, func(i, j int) bool { return pins[i].Actor < pins[j].Actor })
	return pins, nil
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
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

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("versioning: encode %s: %w", filepath.Base(path), err)
	}
	return writeFileAtomic(path, append(data, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("versioning: create %s: %w", dir, err)
	}
	file, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("versioning: create temp file: %w", err)
	}
	tmp := file.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("versioning: write %s: %w", tmp, err)
	}
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return fmt.Errorf("versioning: chmod %s: %w", tmp, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("versioning: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("versioning: rename %s: %w", path, err)
	}
	return nil
}
