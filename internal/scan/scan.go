// Package scan intersects a parsed configuration with a rule corpus.
//
// The matcher is a flat lookup on (block kind, block type), not an expression
// engine. Every finding points at a line that exists in the checkout, and the
// same inputs always produce the same output — no heuristics, no network, no
// model in the path.
package scan

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/moveeeax/tf-upgrade-advisor/internal/config"
	"github.com/moveeeax/tf-upgrade-advisor/internal/rules"
)

// PinRuleID is the synthetic rule reported for a `required_providers` entry
// that still pins the old major.
const PinRuleID = "required-providers-pin"

// Finding is one rule hit at one place in the configuration.
type Finding struct {
	RuleID        string         `json:"rule_id"`
	Kind          rules.Kind     `json:"kind"`
	Severity      rules.Severity `json:"severity"`
	Title         string         `json:"title"`
	Detail        string         `json:"detail,omitempty"`
	Guide         string         `json:"guide,omitempty"`
	Address       string         `json:"address"`
	Attribute     string         `json:"attribute,omitempty"`
	File          string         `json:"file"`
	Line          int            `json:"line"`
	SuggestedDiff string         `json:"suggested_diff,omitempty"`
}

// Result is one scan.
type Result struct {
	Provider       string    `json:"provider"`
	From           int       `json:"from"`
	To             int       `json:"to"`
	GuideURL       string    `json:"guide_url"`
	Root           string    `json:"root"`
	DirsScanned    int       `json:"dirs_scanned"`
	BlocksScanned  int       `json:"blocks_scanned"`
	RulesEvaluated int       `json:"rules_evaluated"`
	SkippedModules []string  `json:"skipped_modules,omitempty"`
	Findings       []Finding `json:"findings"`
}

// Counts returns the number of findings per severity.
func (r Result) Counts() map[rules.Severity]int {
	out := map[rules.Severity]int{}
	for _, f := range r.Findings {
		out[f.Severity]++
	}
	return out
}

// HasBreaking reports whether anything found blocks the upgrade. Callers use it
// as the CI exit code.
func (r Result) HasBreaking() bool { return r.Counts()[rules.SeverityBreaking] > 0 }

// Run matches every rule in the corpus against every block in cfg.
func Run(cfg *config.Config, corpus rules.Corpus) Result {
	res := Result{
		Provider:       corpus.Provider,
		From:           corpus.From,
		To:             corpus.To,
		GuideURL:       corpus.GuideURL,
		Root:           cfg.Root,
		DirsScanned:    len(cfg.Dirs),
		BlocksScanned:  len(cfg.Usages),
		RulesEvaluated: len(corpus.Rules),
		SkippedModules: cfg.SkippedModules,
	}

	index := map[string][]rules.Rule{}
	for _, r := range corpus.Rules {
		key := r.Match.BlockKind + "|" + r.Match.Type
		index[key] = append(index[key], r)
	}

	for _, u := range cfg.Usages {
		for _, r := range index[string(u.Kind)+"|"+u.Type] {
			res.Findings = append(res.Findings, apply(r, u, corpus)...)
		}
	}
	res.Findings = append(res.Findings, pinFindings(cfg, corpus)...)

	sort.SliceStable(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.Severity != b.Severity {
			return a.Severity.Rank() < b.Severity.Rank()
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.RuleID < b.RuleID
	})
	return res
}

// apply evaluates a single rule against a single block.
func apply(r rules.Rule, u config.Usage, corpus rules.Corpus) []Finding {
	base := Finding{
		RuleID:        r.ID,
		Kind:          r.Kind,
		Severity:      r.Severity,
		Title:         r.Title,
		Detail:        r.Detail,
		Guide:         corpus.GuideLink(r),
		Address:       u.Address,
		File:          u.File,
		Line:          u.Line,
		SuggestedDiff: r.SuggestedDiff,
	}

	// Block-level rule: the whole resource type is affected.
	if len(r.Match.Attributes) == 0 {
		return []Finding{base}
	}

	// "Now required": fire once, at the block, when none of the paths are set.
	if r.Match.Absent {
		for _, a := range u.Attrs {
			if !a.Block && matchesAny(a.Path, r.Match.Attributes) {
				return nil
			}
		}
		return []Finding{base}
	}

	var out []Finding
	for _, a := range u.Attrs {
		if !matchesAny(a.Path, r.Match.Attributes) {
			continue
		}
		f := base
		f.Attribute = a.Path
		f.File = a.File
		f.Line = a.Line
		out = append(out, f)
	}
	return out
}

func matchesAny(path string, patterns []string) bool {
	for _, p := range patterns {
		if path == p {
			return true
		}
	}
	return false
}

// pinFindings reports `required_providers` entries that still constrain the
// provider to the major being upgraded from. These are the lines someone has to
// edit to even start the upgrade, so the report names the files.
func pinFindings(cfg *config.Config, corpus rules.Corpus) []Finding {
	var out []Finding
	for _, req := range cfg.Requirements {
		if !isProvider(req, corpus.Provider) {
			continue
		}
		if req.Constraint == "" || allows(req.Constraint, corpus.To) {
			continue
		}
		out = append(out, Finding{
			RuleID:   PinRuleID,
			Kind:     rules.KindRequired,
			Severity: rules.SeverityInfo,
			Title: fmt.Sprintf("`required_providers.%s` does not allow %d.x (%s)",
				req.Name, corpus.To, req.Constraint),
			Detail: fmt.Sprintf("Widen the constraint to the %d.x series and run `terraform init -upgrade` "+
				"once the breaking items below are resolved.", corpus.To),
			Guide:   corpus.GuideURL,
			Address: "required_providers." + req.Name,
			File:    req.File,
			Line:    req.Line,
			SuggestedDiff: fmt.Sprintf("-      version = %q\n+      version = \"~> %d.0\"",
				req.Constraint, corpus.To),
		})
	}
	return out
}

// isProvider matches a required_providers entry to a provider. The source
// address wins when present; only a sourceless legacy entry falls back to the
// local name, which callers are free to alias.
func isProvider(req config.ProviderRequirement, provider string) bool {
	if req.Source == "" {
		return req.Name == provider
	}
	source := strings.ToLower(req.Source)
	source = strings.TrimPrefix(source, "registry.terraform.io/")
	return source == "hashicorp/"+provider
}

// allows reports whether a Terraform version constraint admits the first
// release of the given major, i.e. `<major>.0.0`. That is exactly the question
// the report needs to answer: can `terraform init -upgrade` reach the target
// series without editing this line?
//
// Anything the parser does not understand is treated as permissive, so an
// exotic constraint produces no advice rather than wrong advice.
func allows(constraint string, major int) bool {
	target := version{major, 0, 0}
	for _, part := range strings.Split(constraint, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		op := ""
		for _, candidate := range []string{"~>", ">=", "<=", "!=", ">", "<", "="} {
			if strings.HasPrefix(part, candidate) {
				op = candidate
				part = strings.TrimSpace(strings.TrimPrefix(part, candidate))
				break
			}
		}
		v, parts, ok := parseVersion(part)
		if !ok {
			continue
		}
		switch op {
		case "~>":
			// Pessimistic constraint: the last specified component may rise,
			// everything to its left is fixed. Either way the major is fixed.
			if target.major != v.major {
				return false
			}
			if parts >= 2 && target.minor < v.minor {
				return false
			}
		case "", "=":
			if target != v {
				return false
			}
		case ">":
			if !target.after(v) {
				return false
			}
		case ">=":
			if target.before(v) {
				return false
			}
		case "<":
			if !target.before(v) {
				return false
			}
		case "<=":
			if target.after(v) {
				return false
			}
		}
	}
	return true
}

type version struct{ major, minor, patch int }

func (a version) before(b version) bool {
	if a.major != b.major {
		return a.major < b.major
	}
	if a.minor != b.minor {
		return a.minor < b.minor
	}
	return a.patch < b.patch
}

func (a version) after(b version) bool { return b.before(a) }

// parseVersion reads up to three dotted numeric components and reports how many
// were present, which `~>` needs in order to know what is pinned.
func parseVersion(s string) (version, int, bool) {
	fields := strings.Split(s, ".")
	if len(fields) > 3 {
		return version{}, 0, false
	}
	var v version
	slots := []*int{&v.major, &v.minor, &v.patch}
	for i, f := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return version{}, 0, false
		}
		*slots[i] = n
	}
	return v, len(fields), true
}

// Rel shortens a path for display, relative to root when possible.
func Rel(root, file string) string {
	if rel, err := filepath.Rel(root, file); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return file
}
