// Package outdated inventories the module and provider versions a repository
// pins and compares them with what the Terraform Registry publishes.
//
// Unlike `scan`, this needs the network: the point is to learn about releases
// made after the configuration was written. Only the registry's version-list
// endpoints are read. The HCL is parsed the same syntactic way, so no state, no
// credentials and no `terraform init` are involved.
package outdated

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	goversion "github.com/hashicorp/go-version"

	"github.com/moveeeax/tf-upgrade-advisor/internal/config"
)

// Distance is how far the version a constraint resolves to sits behind the
// latest release.
type Distance int

const (
	DistanceNone Distance = iota
	DistancePatch
	DistanceMinor
	DistanceMajor
)

var distanceNames = []string{"none", "patch", "minor", "major"}

func (d Distance) String() string {
	if d < 0 || int(d) >= len(distanceNames) {
		return "unknown"
	}
	return distanceNames[d]
}

// MarshalText makes the JSON report carry the name, not the number.
func (d Distance) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// ParseDistance reads a --max-behind value.
func ParseDistance(s string) (Distance, error) {
	for i, n := range distanceNames {
		if s == n {
			return Distance(i), nil
		}
	}
	return 0, fmt.Errorf("unknown distance %q (want none, patch, minor or major)", s)
}

// Kind says whether an entry is a module call or a provider requirement.
type Kind string

const (
	KindModule   Kind = "module"
	KindProvider Kind = "provider"
)

// Entry is one pinned module or provider at one place in the repository.
type Entry struct {
	Kind       Kind   `json:"kind"`
	Name       string `json:"name"`
	Source     string `json:"source"` // registry address without the host
	Constraint string `json:"constraint,omitempty"`
	File       string `json:"file"`
	Line       int    `json:"line"`

	// Resolved is the newest published version the constraint admits, i.e.
	// what `terraform init -upgrade` would pick today.
	Resolved string   `json:"resolved,omitempty"`
	Latest   string   `json:"latest,omitempty"`
	Behind   Distance `json:"behind"`
	// Missing means the constraint admits no published version: the pinned
	// release was yanked or never existed.
	Missing bool `json:"missing,omitempty"`
	// Deprecation is the registry's notice for the resolved module version, or
	// the provider-level warnings.
	Deprecation string `json:"deprecation,omitempty"`

	OverLimit    bool   `json:"over_limit"`
	Ignored      bool   `json:"ignored,omitempty"`
	IgnoreReason string `json:"ignore_reason,omitempty"`
}

// Unpinned reports whether the entry has no version constraint at all, so it
// floats to whatever is latest on every init.
func (e Entry) Unpinned() bool { return e.Constraint == "" }

// Unchecked is a module or provider the registry was not asked about, with the
// reason, so the report never claims more coverage than it has.
type Unchecked struct {
	Kind   Kind   `json:"kind"`
	Name   string `json:"name"`
	Source string `json:"source"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// Result is one inventory run.
type Result struct {
	Root         string       `json:"root"`
	Registry     string       `json:"registry"`
	MaxBehind    Distance     `json:"max_behind"`
	DirsScanned  int          `json:"dirs_scanned"`
	Entries      []Entry      `json:"entries"`
	Unchecked    []Unchecked  `json:"unchecked,omitempty"`
	StaleIgnores []IgnoreRule `json:"stale_ignores,omitempty"`
}

// OverLimit returns the entries that need action: further behind than allowed,
// deprecated, or pinned to a version the registry no longer lists.
func (r Result) OverLimit() []Entry {
	var out []Entry
	for _, e := range r.Entries {
		if e.OverLimit {
			out = append(out, e)
		}
	}
	return out
}

// Options tune a run.
type Options struct {
	MaxBehind Distance
	Ignores   []IgnoreRule
}

// Check resolves every registry module and provider in cfg against reg.
// A registry error other than "not found" aborts the run: an outage must not
// read as a clean bill of health.
func Check(ctx context.Context, cfg *config.Config, reg *Registry, opts Options) (Result, error) {
	res := Result{
		Root:        cfg.Root,
		Registry:    reg.BaseURL,
		MaxBehind:   opts.MaxBehind,
		DirsScanned: len(cfg.Dirs),
	}

	for _, m := range cfg.Modules {
		if strings.HasPrefix(m.Source, "./") || strings.HasPrefix(m.Source, "../") {
			continue // part of this repository, nothing to upgrade
		}
		skip := Unchecked{Kind: KindModule, Name: m.Name, Source: m.Source, File: m.File, Line: m.Line}
		host, addr, ok := moduleAddress(m.Source)
		switch {
		case !ok:
			skip.Reason = "not a registry source"
		case host != publicHost:
			skip.Reason = "private registry " + host + " is not queried"
		}
		if skip.Reason != "" {
			res.Unchecked = append(res.Unchecked, skip)
			continue
		}
		releases, err := reg.ModuleVersions(ctx, addr)
		if errors.Is(err, errNotFound) {
			skip.Reason = "not found in the registry"
			res.Unchecked = append(res.Unchecked, skip)
			continue
		}
		if err != nil {
			return Result{}, err
		}
		e := Entry{Kind: KindModule, Name: m.Name, Source: addr, Constraint: m.Version, File: m.File, Line: m.Line}
		if reason := resolve(&e, releases); reason != "" {
			skip.Reason = reason
			res.Unchecked = append(res.Unchecked, skip)
			continue
		}
		res.Entries = append(res.Entries, e)
	}

	for _, p := range cfg.Requirements {
		skip := Unchecked{Kind: KindProvider, Name: p.Name, Source: p.Source, File: p.File, Line: p.Line}
		host, addr, ok := providerAddress(p.Name, p.Source)
		switch {
		case !ok:
			skip.Reason = "source address not understood"
		case host == "terraform.io" && strings.HasPrefix(addr, "builtin/"):
			continue // ships inside Terraform itself
		case host != publicHost:
			skip.Reason = "private registry " + host + " is not queried"
		}
		if skip.Reason != "" {
			res.Unchecked = append(res.Unchecked, skip)
			continue
		}
		releases, warnings, err := reg.ProviderVersions(ctx, addr)
		if errors.Is(err, errNotFound) {
			skip.Reason = "not found in the registry"
			res.Unchecked = append(res.Unchecked, skip)
			continue
		}
		if err != nil {
			return Result{}, err
		}
		e := Entry{Kind: KindProvider, Name: p.Name, Source: addr, Constraint: p.Constraint, File: p.File, Line: p.Line}
		if reason := resolve(&e, releases); reason != "" {
			skip.Reason = reason
			res.Unchecked = append(res.Unchecked, skip)
			continue
		}
		if len(warnings) > 0 {
			e.Deprecation = strings.Join(warnings, " ")
		}
		res.Entries = append(res.Entries, e)
	}

	used := make([]bool, len(opts.Ignores))
	for i := range res.Entries {
		e := &res.Entries[i]
		for j, rule := range opts.Ignores {
			if rule.matches(*e) {
				e.Ignored, e.IgnoreReason = true, rule.Reason
				used[j] = true
				break
			}
		}
		e.OverLimit = !e.Ignored && (e.Missing || e.Deprecation != "" || e.Behind > opts.MaxBehind)
	}
	for j, rule := range opts.Ignores {
		if !used[j] {
			res.StaleIgnores = append(res.StaleIgnores, rule)
		}
	}

	sort.SliceStable(res.Entries, func(i, j int) bool {
		a, b := res.Entries[i], res.Entries[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	sort.SliceStable(res.Unchecked, func(i, j int) bool {
		a, b := res.Unchecked[i], res.Unchecked[j]
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return res, nil
}

// resolve fills Resolved, Latest, Behind, Missing and Deprecation from the
// published releases. It returns a reason when the entry cannot be judged.
func resolve(e *Entry, releases []Release) string {
	type parsed struct {
		v   *goversion.Version
		rel Release
	}
	var all []parsed
	for _, r := range releases {
		v, err := goversion.NewSemver(r.Version)
		if err != nil {
			continue
		}
		all = append(all, parsed{v, r})
	}
	if len(all) == 0 {
		return "the registry lists no versions"
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v.LessThan(all[j].v) })

	// Latest is the newest stable release; a project that has only ever
	// published prereleases falls back to the newest of those.
	latest := all[len(all)-1]
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].v.Prerelease() == "" {
			latest = all[i]
			break
		}
	}
	e.Latest = latest.v.Original()

	var resolved *parsed
	if e.Constraint == "" {
		resolved = &latest
	} else {
		cons, err := goversion.NewConstraint(e.Constraint)
		if err != nil {
			return fmt.Sprintf("version constraint %q not understood", e.Constraint)
		}
		for i := len(all) - 1; i >= 0; i-- {
			if cons.Check(all[i].v) {
				resolved = &all[i]
				break
			}
		}
	}
	if resolved == nil {
		e.Missing = true
		return ""
	}
	e.Resolved = resolved.v.Original()
	e.Deprecation = resolved.rel.Deprecation
	e.Behind = distance(resolved.v, latest.v)
	return ""
}

// distance compares the first differing release segment.
func distance(have, latest *goversion.Version) Distance {
	if !have.LessThan(latest) {
		return DistanceNone
	}
	h, l := have.Segments(), latest.Segments()
	switch {
	case h[0] != l[0]:
		return DistanceMajor
	case h[1] != l[1]:
		return DistanceMinor
	case h[2] != l[2]:
		return DistancePatch
	}
	// Same release number, older prerelease of it (6.0.0-beta1 vs 6.0.0).
	return DistancePatch
}
