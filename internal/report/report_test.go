package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/moveeeax/tf-upgrade-advisor/internal/config"
	"github.com/moveeeax/tf-upgrade-advisor/internal/rules"
	"github.com/moveeeax/tf-upgrade-advisor/internal/scan"
)

func fixture(t *testing.T, dir string) scan.Result {
	t.Helper()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	corpus, err := rules.Load("aws", 5, 6)
	if err != nil {
		t.Fatalf("rules.Load: %v", err)
	}
	return scan.Run(cfg, corpus)
}

func TestMarkdownChecklist(t *testing.T) {
	md := Markdown(fixture(t, "../../testdata/aws5"))

	for _, want := range []string{
		"## Provider upgrade: aws 5.x → 6.x",
		"### Breaking (",
		"### Warning (",
		"### Info (",
		"- [ ] **1. ",
		"`main.tf:21`",                  // file:line for every hit
		"aws_eip",                       // the resource that hit
		"```diff",                       // suggested diff, not applied
		"#resource-aws_eip",             // deep link into the guide
		"terraform-aws-modules/vpc/aws", // remote module disclosure
		"remote module not followed",    // ...and why
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report is missing %q", want)
		}
	}

	// Checklist numbering must be continuous across severity sections; a report
	// referred to as "item 7" in review has to have exactly one item 7.
	for i := 1; i <= 3; i++ {
		marker := "- [ ] **" + string(rune('0'+i)) + ". "
		if n := strings.Count(md, marker); n != 1 {
			t.Errorf("found %d occurrences of checklist item %d", n, i)
		}
	}

	// A fenced diff indented four spaces stays inside its list item; more would
	// render as a literal code block showing the backticks.
	for _, line := range strings.Split(md, "\n") {
		if strings.Contains(line, "```diff") && line != "    ```diff" {
			t.Errorf("diff fence indented wrong: %q", line)
		}
	}
}

func TestMarkdownCleanConfiguration(t *testing.T) {
	md := Markdown(fixture(t, "../../testdata/clean"))
	if !strings.Contains(md, "No known breaking changes apply") {
		t.Errorf("clean report should say so plainly:\n%s", md)
	}
	if strings.Contains(md, "- [ ]") {
		t.Errorf("clean report should have no checklist items:\n%s", md)
	}
}

func TestTextOutput(t *testing.T) {
	txt := Text(fixture(t, "../../testdata/aws5"))
	if !strings.Contains(txt, "main.tf:21: breaking: aws_eip.nat.vpc [aws6-eip-vpc]") {
		t.Errorf("expected a grep-friendly line for the aws_eip hit:\n%s", txt)
	}
	clean := Text(fixture(t, "../../testdata/clean"))
	if !strings.HasPrefix(clean, "✓ aws 5 -> 6:") {
		t.Errorf("clean text output = %q", clean)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	var b strings.Builder
	res := fixture(t, "../../testdata/aws5")
	if err := JSON(&b, res); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var decoded scan.Result
	if err := json.Unmarshal([]byte(b.String()), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Findings) != len(res.Findings) {
		t.Fatalf("round trip lost findings: %d -> %d", len(res.Findings), len(decoded.Findings))
	}
	if decoded.Provider != "aws" || decoded.From != 5 || decoded.To != 6 {
		t.Errorf("step metadata lost: %+v", decoded)
	}
	for _, f := range decoded.Findings {
		if f.RuleID == "" || f.File == "" || f.Line == 0 {
			t.Fatalf("finding missing provenance: %+v", f)
		}
	}
}

func TestJSONEmptyFindingsIsAnArray(t *testing.T) {
	var b strings.Builder
	if err := JSON(&b, scan.Result{Provider: "aws", From: 5, To: 6}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"findings": []`) {
		t.Errorf("empty findings must serialise as [], not null:\n%s", b.String())
	}
}

func TestCorpora(t *testing.T) {
	list, err := rules.Available()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	Corpora(&b, list)
	out := b.String()
	for _, want := range []string{"aws 4 -> 5", "aws 5 -> 6", "breaking", "guide:"} {
		if !strings.Contains(out, want) {
			t.Errorf("corpora listing missing %q:\n%s", want, out)
		}
	}
}
