// Package report renders a scan result as the checklist a human works through,
// or as JSON for another tool to consume.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/moveeeax/tf-upgrade-advisor/internal/rules"
	"github.com/moveeeax/tf-upgrade-advisor/internal/scan"
)

// severityOrder is the order sections appear in the report.
var severityOrder = []rules.Severity{rules.SeverityBreaking, rules.SeverityWarning, rules.SeverityInfo}

var severityLabel = map[rules.Severity]string{
	rules.SeverityBreaking: "Breaking",
	rules.SeverityWarning:  "Warning",
	rules.SeverityInfo:     "Info",
}

// JSON writes the result as a stable machine-readable document.
func JSON(w io.Writer, res scan.Result) error {
	if res.Findings == nil {
		res.Findings = []scan.Finding{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

// Markdown renders the upgrade checklist: a headline count, then one checkbox
// per finding grouped by severity, each with the file:line it was found at.
//
// Findings are numbered continuously across sections so the report can be
// referred to as "item 7" in a review.
func Markdown(res scan.Result) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## Provider upgrade: %s %d.x → %d.x\n\n", res.Provider, res.From, res.To)

	if len(res.Findings) == 0 {
		fmt.Fprintf(&b, "No known breaking changes apply. Scanned %s across %s against %d rules.\n",
			plural(res.BlocksScanned, "block"), plural(res.DirsScanned, "directory", "directories"),
			res.RulesEvaluated)
		writeSkipped(&b, res)
		return b.String()
	}

	counts := res.Counts()
	var parts []string
	for _, s := range severityOrder {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("**%d** %s", n, strings.ToLower(severityLabel[s])))
		}
	}
	fmt.Fprintf(&b, "%s across %s in %s. Checked against %d rules from the official upgrade guide.\n\n",
		strings.Join(parts, ", "), plural(res.BlocksScanned, "block"),
		plural(res.DirsScanned, "directory", "directories"), res.RulesEvaluated)

	if counts[rules.SeverityBreaking] > 0 {
		fmt.Fprintf(&b, "> ⚠️ %s must be fixed before `terraform plan` succeeds on %s %d.x.\n\n",
			plural(counts[rules.SeverityBreaking], "item"), res.Provider, res.To)
	}

	item := 0
	for _, sev := range severityOrder {
		group := findingsBySeverity(res.Findings, sev)
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s (%d)\n\n", severityLabel[sev], len(group))
		for _, f := range group {
			item++
			writeFinding(&b, res, item, f)
		}
	}

	writeSkipped(&b, res)
	if res.GuideURL != "" {
		fmt.Fprintf(&b, "\nRules transcribed from the [official %s provider v%d upgrade guide](%s).\n",
			res.Provider, res.To, res.GuideURL)
	}
	return b.String()
}

func writeFinding(b *strings.Builder, res scan.Result, item int, f scan.Finding) {
	loc := fmt.Sprintf("%s:%d", scan.Rel(res.Root, f.File), f.Line)
	what := "`" + f.Address + "`"
	if f.Attribute != "" {
		what = fmt.Sprintf("`%s` → `%s`", f.Address, f.Attribute)
	}

	fmt.Fprintf(b, "- [ ] **%d. %s** — %s\n", item, f.Title, what)
	fmt.Fprintf(b, "  - `%s` · %s · rule `%s`", loc, f.Kind, f.RuleID)
	if f.Guide != "" {
		fmt.Fprintf(b, " · [guide](%s)", f.Guide)
	}
	b.WriteString("\n")
	if f.Detail != "" {
		fmt.Fprintf(b, "  - %s\n", strings.TrimSpace(f.Detail))
	}
	if f.SuggestedDiff != "" {
		// Four spaces keeps the fence inside the list item (its content indent
		// is two) without crossing the four-extra-space threshold that would
		// turn it into a literal indented code block.
		b.WriteString("\n")
		for _, line := range strings.Split("```diff\n"+strings.TrimRight(f.SuggestedDiff, "\n")+"\n```", "\n") {
			if line == "" {
				b.WriteString("\n")
				continue
			}
			fmt.Fprintf(b, "    %s\n", line)
		}
		b.WriteString("\n")
	}
}

func writeSkipped(b *strings.Builder, res scan.Result) {
	if len(res.SkippedModules) == 0 {
		return
	}
	fmt.Fprintf(b, "\n<details>\n<summary>%s not followed (v1 resolves local paths only)</summary>\n\n",
		plural(len(res.SkippedModules), "remote module"))
	for _, m := range res.SkippedModules {
		fmt.Fprintf(b, "- `%s`\n", m)
	}
	b.WriteString("\n</details>\n")
}

// Text renders a compact one-line-per-finding view for a terminal.
func Text(res scan.Result) string {
	var b strings.Builder
	if len(res.Findings) == 0 {
		fmt.Fprintf(&b, "✓ %s %d -> %d: no known breaking changes in %s\n",
			res.Provider, res.From, res.To, plural(res.BlocksScanned, "block"))
		return b.String()
	}
	counts := res.Counts()
	fmt.Fprintf(&b, "%s %d -> %d: %d breaking, %d warning, %d info\n\n",
		res.Provider, res.From, res.To,
		counts[rules.SeverityBreaking], counts[rules.SeverityWarning], counts[rules.SeverityInfo])
	for _, f := range res.Findings {
		what := f.Address
		if f.Attribute != "" {
			what += "." + f.Attribute
		}
		fmt.Fprintf(&b, "%s:%d: %s: %s [%s]\n",
			scan.Rel(res.Root, f.File), f.Line, f.Severity, what, f.RuleID)
		fmt.Fprintf(&b, "  %s\n", f.Title)
	}
	return b.String()
}

// Corpora renders the embedded rule corpora, so `rules` can answer "what do you
// actually cover" without reading the repo.
func Corpora(w io.Writer, list []rules.Corpus) {
	for _, c := range list {
		byKind := map[rules.Kind]int{}
		bySeverity := map[rules.Severity]int{}
		for _, r := range c.Rules {
			byKind[r.Kind]++
			bySeverity[r.Severity]++
		}
		fmt.Fprintf(w, "%s  %d rules (%d breaking, %d warning, %d info)\n",
			c.Step(), len(c.Rules),
			bySeverity[rules.SeverityBreaking], bySeverity[rules.SeverityWarning], bySeverity[rules.SeverityInfo])
		kinds := make([]string, 0, len(byKind))
		for k, n := range byKind {
			kinds = append(kinds, fmt.Sprintf("%s=%d", k, n))
		}
		sort.Strings(kinds)
		fmt.Fprintf(w, "  kinds: %s\n  guide: %s\n", strings.Join(kinds, " "), c.GuideURL)
	}
}

func findingsBySeverity(findings []scan.Finding, sev rules.Severity) []scan.Finding {
	var out []scan.Finding
	for _, f := range findings {
		if f.Severity == sev {
			out = append(out, f)
		}
	}
	return out
}

func plural(n int, singular string, plural ...string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	if len(plural) > 0 {
		return fmt.Sprintf("%d %s", n, plural[0])
	}
	return fmt.Sprintf("%d %ss", n, singular)
}
