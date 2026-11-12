package outdated

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

// DefaultIgnoreFile is read from the scanned root when --ignore-file is not set.
const DefaultIgnoreFile = ".tf-upgrade-advisor-ignore.yaml"

// IgnoreRule keeps a deliberately held-back module or provider out of the
// over-limit list. Reason is mandatory: a pin nobody can explain is exactly
// what this report exists to surface.
type IgnoreRule struct {
	// Source is the registry address, `namespace/name/system` for a module or
	// `namespace/type` for a provider. A `registry.terraform.io/` prefix is
	// accepted and dropped.
	Source string `yaml:"source" json:"source"`
	// Constraint, when set, limits the rule to entries whose version argument
	// is exactly this text, so the rule stops applying once someone edits it.
	Constraint string `yaml:"constraint,omitempty" json:"constraint,omitempty"`
	Reason     string `yaml:"reason" json:"reason"`
}

type ignoreFile struct {
	Ignore []IgnoreRule `yaml:"ignore"`
}

// LoadIgnores reads an ignore file. A missing file at the default location is
// not an error; a missing file the user named explicitly is.
func LoadIgnores(path string, explicit bool) ([]IgnoreRule, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseIgnores(raw, path)
}

// ParseIgnores validates the ignore file. Unknown fields are rejected so a
// typo such as `resaon` fails loudly instead of silently ignoring nothing.
func ParseIgnores(raw []byte, name string) ([]IgnoreRule, error) {
	var f ignoreFile
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	for i := range f.Ignore {
		r := &f.Ignore[i]
		r.Source = normalizeSource(r.Source)
		r.Reason = strings.TrimSpace(r.Reason)
		if r.Source == "" {
			return nil, fmt.Errorf("%s: ignore[%d]: source is required", name, i)
		}
		if r.Reason == "" {
			return nil, fmt.Errorf("%s: ignore[%d] (%s): reason is required", name, i, r.Source)
		}
	}
	return f.Ignore, nil
}

func normalizeSource(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.TrimPrefix(s, publicHost+"/")
}

func (r IgnoreRule) matches(e Entry) bool {
	if r.Source != strings.ToLower(e.Source) {
		return false
	}
	return r.Constraint == "" || r.Constraint == e.Constraint
}
