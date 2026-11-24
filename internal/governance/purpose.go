// Package governance loads and filters the verbal source of truth
// (governance/purpose.md) for a single actor.
package governance

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrActorNotFound is returned when an actor is not mentioned in purpose.md.
var ErrActorNotFound = errors.New("governance: actor not found in purpose")

// Purpose is a parsed purpose.md document.
type Purpose struct {
	preamble []string
	sections []section
	raw      string
}

type section struct {
	title string
	lines []string
}

// LoadPurpose reads and parses the purpose document at path.
func LoadPurpose(path string) (*Purpose, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("governance: read purpose: %w", err)
	}
	p := ParsePurpose(data)
	if strings.TrimSpace(p.raw) == "" {
		return nil, errors.New("governance: purpose document is empty")
	}
	return p, nil
}

// ParsePurpose parses a purpose document.
//
// A section starts at a line beginning with "## "; everything before the
// first section is kept as the preamble.
func ParsePurpose(data []byte) *Purpose {
	raw := strings.ReplaceAll(string(data), "\r\n", "\n")
	p := &Purpose{raw: raw}

	var current *section
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "## ") {
			if current != nil {
				p.sections = append(p.sections, *current)
			}
			current = &section{title: strings.TrimSpace(strings.TrimPrefix(line, "## "))}
			continue
		}
		if current == nil {
			p.preamble = append(p.preamble, line)
		} else {
			current.lines = append(current.lines, line)
		}
	}
	if current != nil {
		p.sections = append(p.sections, *current)
	}
	return p
}

// ViewFor returns the subset of purpose.md relevant to actor.
//
// The "Actors" section is filtered to entries mentioning the actor, since
// that is the part describing responsibilities the agent should not see for
// other actors. Protocol flow and boundary invariants are contract-wide and
// are returned in full: they are already implied by protocol.fbs.
func (p *Purpose) ViewFor(actor string) (string, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "", errors.New("governance: actor name is required")
	}
	if !mentionsActor(p.raw, actor) {
		return "", fmt.Errorf("%w: %q", ErrActorNotFound, actor)
	}

	var b strings.Builder
	for _, line := range p.preamble {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, sec := range p.sections {
		lines := sec.lines
		if isActorSection(sec.title) {
			lines = filterActorLines(lines, actor)
			if len(lines) == 0 {
				continue
			}
		}
		b.WriteString("## ")
		b.WriteString(sec.title)
		b.WriteByte('\n')
		for _, line := range lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

func isActorSection(title string) bool {
	return strings.Contains(strings.ToLower(title), "actor")
}

func filterActorLines(lines []string, actor string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && isListItem(trimmed) && !mentionsActor(line, actor) {
			continue
		}
		out = append(out, line)
	}
	return trimBlankEdges(out)
}

func isListItem(line string) bool {
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") {
		return true
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(line) && (line[i] == '.' || line[i] == ')') && line[i+1] == ' '
}

func trimBlankEdges(lines []string) []string {
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}

func mentionsActor(text, actor string) bool {
	haystack := strings.ToLower(text)
	needle := strings.ToLower(strings.TrimSpace(actor))
	if needle == "" {
		return false
	}

	for offset := 0; offset <= len(haystack)-len(needle); {
		idx := strings.Index(haystack[offset:], needle)
		if idx < 0 {
			return false
		}
		idx += offset
		if !isIdent(haystack, idx-1) && !isIdent(haystack, idx+len(needle)) {
			return true
		}
		offset = idx + 1
	}
	return false
}

func isIdent(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	b := s[i]
	return b == '_' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}
