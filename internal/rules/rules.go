// Package rules holds the breaking-change corpus: one YAML file per provider
// major-version step, embedded in the binary so a scan needs no network.
//
// A rule is data, not code. Adding coverage for a new provider major means
// adding a YAML file and a test fixture, never touching the matcher.
package rules

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed corpus/*.yaml
var corpusFS embed.FS

// Kind classifies what changed, so a reader can triage a report at a glance.
type Kind string

const (
	KindRemoved        Kind = "removed"
	KindRenamed        Kind = "renamed"
	KindRetyped        Kind = "retyped"
	KindDefaultChanged Kind = "default-changed"
	KindRequired       Kind = "required"
	KindDeprecated     Kind = "deprecated"
	KindStateMove      Kind = "state-move"
)

// Severity drives report ordering and the process exit code.
type Severity string

const (
	// SeverityBreaking means `terraform plan` fails, or applies something you
	// did not ask for, until the configuration is changed. Exits non-zero.
	SeverityBreaking Severity = "breaking"
	// SeverityWarning means it still plans today but is deprecated or changes
	// behaviour subtly.
	SeverityWarning Severity = "warning"
	// SeverityInfo is context: worth reading once, no edit required.
	SeverityInfo Severity = "info"
)

var severityRank = map[Severity]int{SeverityBreaking: 0, SeverityWarning: 1, SeverityInfo: 2}

// Rank orders severities most-urgent first.
func (s Severity) Rank() int {
	if r, ok := severityRank[s]; ok {
		return r
	}
	return len(severityRank)
}

// Match is the intersection test against a scanned configuration.
//
// BlockKind and Type select the block. Attributes are dotted paths relative to
// that block; the rule fires once per path actually present in the
// configuration. With no attributes the rule fires on the block itself (a whole
// resource type that was removed). With Absent set the sense is inverted: the
// rule fires when none of the paths are present, which is how "this argument is
// now required" is expressed.
type Match struct {
	BlockKind  string   `yaml:"block_kind"` // resource | data | provider
	Type       string   `yaml:"type"`
	Attributes []string `yaml:"attributes,omitempty"`
	Absent     bool     `yaml:"absent,omitempty"`
}

// Rule is one entry from a provider upgrade guide.
type Rule struct {
	ID            string   `yaml:"id" json:"id"`
	Kind          Kind     `yaml:"kind" json:"kind"`
	Severity      Severity `yaml:"severity" json:"severity"`
	Match         Match    `yaml:"match" json:"-"`
	Title         string   `yaml:"title" json:"title"`
	Detail        string   `yaml:"detail" json:"detail"`
	GuideAnchor   string   `yaml:"guide_anchor" json:"guide_anchor"`
	SuggestedDiff string   `yaml:"suggested_diff,omitempty" json:"suggested_diff,omitempty"`
}

// Corpus is one provider major-version step.
type Corpus struct {
	Provider string `yaml:"provider"`
	From     int    `yaml:"from"`
	To       int    `yaml:"to"`
	GuideURL string `yaml:"guide_url"`
	Rules    []Rule `yaml:"rules"`
}

// Step names the corpus, e.g. "aws 5 -> 6".
func (c Corpus) Step() string { return fmt.Sprintf("%s %d -> %d", c.Provider, c.From, c.To) }

// GuideLink returns the deep link for a rule's anchor.
func (c Corpus) GuideLink(r Rule) string {
	if r.GuideAnchor == "" {
		return c.GuideURL
	}
	return c.GuideURL + r.GuideAnchor
}

// Load returns the embedded corpus for one provider major-version step.
func Load(provider string, from, to int) (Corpus, error) {
	name := fmt.Sprintf("corpus/%s-%d-to-%d.yaml", strings.ToLower(provider), from, to)
	data, err := corpusFS.ReadFile(name)
	if err != nil {
		avail, _ := Available()
		steps := make([]string, 0, len(avail))
		for _, c := range avail {
			steps = append(steps, c.Step())
		}
		return Corpus{}, fmt.Errorf("no rules for %s %d -> %d (available: %s)",
			provider, from, to, strings.Join(steps, ", "))
	}
	return parse(name, data)
}

// Available lists every embedded corpus, ordered by provider then version.
func Available() ([]Corpus, error) {
	entries, err := fs.Glob(corpusFS, "corpus/*.yaml")
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	out := make([]Corpus, 0, len(entries))
	for _, e := range entries {
		data, err := corpusFS.ReadFile(e)
		if err != nil {
			return nil, err
		}
		c, err := parse(e, data)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].From < out[j].From
	})
	return out, nil
}

// parse decodes and validates a corpus file. A malformed corpus is a build-time
// bug in this repo, so validation is strict and the errors name the rule.
func parse(name string, data []byte) (Corpus, error) {
	var c Corpus
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return Corpus{}, fmt.Errorf("%s: %w", name, err)
	}
	if c.Provider == "" || c.From == 0 || c.To == 0 {
		return Corpus{}, fmt.Errorf("%s: provider, from and to are required", name)
	}
	if want := fmt.Sprintf("%s-%d-to-%d.yaml", c.Provider, c.From, c.To); path.Base(name) != want {
		return Corpus{}, fmt.Errorf("%s: header says %s, expected filename %s", name, c.Step(), want)
	}
	seen := map[string]bool{}
	for i, r := range c.Rules {
		where := fmt.Sprintf("%s: rule %d", name, i)
		if r.ID == "" {
			return Corpus{}, fmt.Errorf("%s: missing id", where)
		}
		if seen[r.ID] {
			return Corpus{}, fmt.Errorf("%s: duplicate id %q", where, r.ID)
		}
		seen[r.ID] = true
		if _, ok := severityRank[r.Severity]; !ok {
			return Corpus{}, fmt.Errorf("%s (%s): invalid severity %q", where, r.ID, r.Severity)
		}
		if !validKind(r.Kind) {
			return Corpus{}, fmt.Errorf("%s (%s): invalid kind %q", where, r.ID, r.Kind)
		}
		switch r.Match.BlockKind {
		case "resource", "data", "provider":
		default:
			return Corpus{}, fmt.Errorf("%s (%s): invalid match.block_kind %q", where, r.ID, r.Match.BlockKind)
		}
		if r.Match.Type == "" {
			return Corpus{}, fmt.Errorf("%s (%s): missing match.type", where, r.ID)
		}
		if r.Title == "" {
			return Corpus{}, fmt.Errorf("%s (%s): missing title", where, r.ID)
		}
		if r.Match.Absent && len(r.Match.Attributes) == 0 {
			return Corpus{}, fmt.Errorf("%s (%s): match.absent needs at least one attribute", where, r.ID)
		}
	}
	return c, nil
}

func validKind(k Kind) bool {
	switch k {
	case KindRemoved, KindRenamed, KindRetyped, KindDefaultChanged,
		KindRequired, KindDeprecated, KindStateMove:
		return true
	}
	return false
}
