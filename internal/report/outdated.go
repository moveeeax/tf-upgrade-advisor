package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/moveeeax/tf-upgrade-advisor/internal/outdated"
	"github.com/moveeeax/tf-upgrade-advisor/internal/scan"
)

// OutdatedJSON writes the inventory as a stable machine-readable document.
func OutdatedJSON(w io.Writer, res outdated.Result) error {
	if res.Entries == nil {
		res.Entries = []outdated.Entry{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

// group is every place one module or provider is pinned.
type group struct {
	kind    outdated.Kind
	source  string
	latest  string
	entries []outdated.Entry
}

// groupEntries folds entries that share a kind and source, keeping the order
// Check sorted them in.
func groupEntries(entries []outdated.Entry) []group {
	var out []group
	for _, e := range entries {
		if n := len(out); n > 0 && out[n-1].kind == e.Kind && out[n-1].source == e.Source {
			out[n-1].entries = append(out[n-1].entries, e)
			continue
		}
		out = append(out, group{kind: e.Kind, source: e.Source, latest: e.Latest, entries: []outdated.Entry{e}})
	}
	return out
}

// OutdatedMarkdown renders the grouped upgrade list. It is also the body of
// the issue that --open-issue keeps up to date, so one module pinned in forty
// stacks is one checkbox, not forty.
func OutdatedMarkdown(res outdated.Result) string {
	var b strings.Builder
	b.WriteString("## Module and provider versions\n\n")

	var over, behind, ignored []outdated.Entry
	for _, e := range res.Entries {
		switch {
		case e.Ignored:
			ignored = append(ignored, e)
		case e.OverLimit:
			over = append(over, e)
		case e.Behind > outdated.DistanceNone:
			behind = append(behind, e)
		}
	}

	limit := "any release behind latest"
	if res.MaxBehind > outdated.DistanceNone {
		limit = "more than a " + res.MaxBehind.String() + " release behind"
	}
	if len(over) == 0 {
		fmt.Fprintf(&b, "Nothing needs an upgrade: no pin is %s, deprecated or yanked. Checked %s across %s.\n",
			limit, plural(len(res.Entries), "pinned version"),
			plural(res.DirsScanned, "directory", "directories"))
	} else {
		fmt.Fprintf(&b, "**%d** of %s need an upgrade (%s, deprecated or yanked) across %s.\n",
			len(over), plural(len(res.Entries), "pinned version"), limit,
			plural(res.DirsScanned, "directory", "directories"))
	}

	if groups := groupEntries(over); len(groups) > 0 {
		fmt.Fprintf(&b, "\n### Needs an upgrade (%d)\n\n", len(groups))
		for _, g := range groups {
			fmt.Fprintf(&b, "- [ ] **`%s`** %s, latest `%s`\n", g.source, g.kind, g.latest)
			for _, e := range g.entries {
				writeEntry(&b, res, e)
			}
		}
	}
	if groups := groupEntries(behind); len(groups) > 0 {
		fmt.Fprintf(&b, "\n### Behind, within the limit (%d)\n\n", len(groups))
		for _, g := range groups {
			fmt.Fprintf(&b, "- `%s` %s, latest `%s`\n", g.source, g.kind, g.latest)
			for _, e := range g.entries {
				writeEntry(&b, res, e)
			}
		}
	}
	if len(ignored) > 0 {
		fmt.Fprintf(&b, "\n<details>\n<summary>%s held back on purpose</summary>\n\n", plural(len(ignored), "pin"))
		for _, e := range ignored {
			fmt.Fprintf(&b, "- `%s` at `%s:%d`, %s → latest `%s`: %s\n", e.Source,
				scan.Rel(res.Root, e.File), e.Line, describeConstraint(e), e.Latest, e.IgnoreReason)
		}
		b.WriteString("\n</details>\n")
	}
	if len(res.Unchecked) > 0 {
		fmt.Fprintf(&b, "\n<details>\n<summary>%s not checked</summary>\n\n", plural(len(res.Unchecked), "source"))
		for _, u := range res.Unchecked {
			fmt.Fprintf(&b, "- `%s` (%s `%s`, `%s:%d`): %s\n", u.Source, u.Kind, u.Name,
				scan.Rel(res.Root, u.File), u.Line, u.Reason)
		}
		b.WriteString("\n</details>\n")
	}
	if len(res.StaleIgnores) > 0 {
		b.WriteString("\nIgnore rules that matched nothing and can be removed:\n\n")
		for _, r := range res.StaleIgnores {
			if r.Constraint != "" {
				fmt.Fprintf(&b, "- `%s` at `%s`\n", r.Source, r.Constraint)
			} else {
				fmt.Fprintf(&b, "- `%s`\n", r.Source)
			}
		}
	}
	fmt.Fprintf(&b, "\nVersions read from %s.\n", res.Registry)
	return b.String()
}

func writeEntry(b *strings.Builder, res outdated.Result, e outdated.Entry) {
	fmt.Fprintf(b, "  - `%s:%d` · `%s` %s · %s\n",
		scan.Rel(res.Root, e.File), e.Line, e.Name, describeConstraint(e), describeState(e))
	if e.Deprecation != "" {
		fmt.Fprintf(b, "    - Deprecated: %s\n", e.Deprecation)
	}
}

func describeConstraint(e outdated.Entry) string {
	if e.Unpinned() {
		return "unpinned"
	}
	return "`" + e.Constraint + "`"
}

func describeState(e outdated.Entry) string {
	switch {
	case e.Missing:
		return "matches no published version (yanked or never released)"
	case e.Behind > outdated.DistanceNone:
		return fmt.Sprintf("resolves to `%s`, %s behind", e.Resolved, e.Behind)
	case e.Deprecation != "":
		return fmt.Sprintf("resolves to `%s`, deprecated", e.Resolved)
	default:
		return fmt.Sprintf("resolves to `%s`, current", e.Resolved)
	}
}

// OutdatedText renders one line per pin that is behind, deprecated or yanked.
func OutdatedText(res outdated.Result) string {
	var b strings.Builder
	over := len(res.OverLimit())
	fmt.Fprintf(&b, "%d of %s need an upgrade (max behind: %s)\n",
		over, plural(len(res.Entries), "pinned version"), res.MaxBehind)
	first := true
	for _, e := range res.Entries {
		if e.Behind == outdated.DistanceNone && !e.Missing && e.Deprecation == "" {
			continue
		}
		if first {
			b.WriteString("\n")
			first = false
		}
		mark := "behind"
		switch {
		case e.Ignored:
			mark = "ignored"
		case e.OverLimit:
			mark = "upgrade"
		}
		fmt.Fprintf(&b, "%s:%d: %s: %s %s %s (latest %s) %s\n",
			scan.Rel(res.Root, e.File), e.Line, mark, e.Kind, e.Source,
			strings.Trim(describeConstraint(e), "`"), e.Latest,
			strings.ReplaceAll(describeState(e), "`", ""))
	}
	for _, u := range res.Unchecked {
		fmt.Fprintf(&b, "%s:%d: not checked: %s %s: %s\n",
			scan.Rel(res.Root, u.File), u.Line, u.Kind, u.Source, u.Reason)
	}
	return b.String()
}
