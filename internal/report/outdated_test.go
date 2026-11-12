package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/moveeeax/tf-upgrade-advisor/internal/outdated"
)

// sample is a hand-built result so the renderers are tested without a
// registry. Root is a fake absolute path; files sit under it.
func sample() outdated.Result {
	return outdated.Result{
		Root:        "/repo",
		Registry:    "https://registry.terraform.io",
		MaxBehind:   outdated.DistanceMinor,
		DirsScanned: 2,
		Entries: []outdated.Entry{
			{Kind: outdated.KindModule, Name: "vpc", Source: "terraform-aws-modules/vpc/aws", Constraint: "5.8.1",
				File: "/repo/a/main.tf", Line: 3, Resolved: "5.8.1", Latest: "6.0.0", Behind: outdated.DistanceMajor, OverLimit: true},
			{Kind: outdated.KindModule, Name: "vpc", Source: "terraform-aws-modules/vpc/aws", Constraint: "~> 5.0",
				File: "/repo/b/main.tf", Line: 9, Resolved: "5.9.0", Latest: "6.0.0", Behind: outdated.DistanceMajor, OverLimit: true},
			{Kind: outdated.KindModule, Name: "gone", Source: "example/gone/aws", Constraint: "1.2.3",
				File: "/repo/b/main.tf", Line: 20, Latest: "1.2.4", Missing: true, OverLimit: true},
			{Kind: outdated.KindModule, Name: "bucket", Source: "terraform-aws-modules/s3-bucket/aws", Constraint: "4.1.0",
				File: "/repo/b/main.tf", Line: 30, Resolved: "4.1.0", Latest: "5.0.0", Behind: outdated.DistanceMajor,
				Ignored: true, IgnoreReason: "waiting on ACL removal"},
			{Kind: outdated.KindProvider, Name: "random", Source: "hashicorp/random", Constraint: "~> 3.5.0",
				File: "/repo/a/main.tf", Line: 12, Resolved: "3.5.1", Latest: "3.6.3", Behind: outdated.DistanceMinor},
			{Kind: outdated.KindProvider, Name: "template", Source: "hashicorp/template", Constraint: "2.2.0",
				File: "/repo/a/main.tf", Line: 15, Resolved: "2.2.0", Latest: "2.2.0",
				Deprecation: "This provider is archived.", OverLimit: true},
		},
		Unchecked: []outdated.Unchecked{
			{Kind: outdated.KindModule, Name: "legacy", Source: "git::https://example.com/x.git", File: "/repo/a/main.tf", Line: 40, Reason: "not a registry source"},
		},
		StaleIgnores: []outdated.IgnoreRule{{Source: "hashicorp/null", Reason: "old"}},
	}
}

func TestOutdatedMarkdownGroupsBySource(t *testing.T) {
	md := OutdatedMarkdown(sample())
	for _, want := range []string{
		"## Module and provider versions",
		"**4** of 6 pinned versions need an upgrade (more than a minor release behind",
		"### Needs an upgrade (3)",
		"- [ ] **`terraform-aws-modules/vpc/aws`** module, latest `6.0.0`",
		"  - `a/main.tf:3` · `vpc` `5.8.1` · resolves to `5.8.1`, major behind",
		"  - `b/main.tf:9` · `vpc` `~> 5.0` · resolves to `5.9.0`, major behind",
		"matches no published version (yanked or never released)",
		"    - Deprecated: This provider is archived.",
		"### Behind, within the limit (1)",
		"`hashicorp/random` provider",
		"1 pin held back on purpose",
		"waiting on ACL removal",
		"1 source not checked",
		"not a registry source",
		"- `hashicorp/null`",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	// One checkbox per source, not per location.
	if n := strings.Count(md, "- [ ] **`terraform-aws-modules/vpc/aws`**"); n != 1 {
		t.Errorf("vpc listed %d times, want once", n)
	}
}

func TestOutdatedMarkdownAllClear(t *testing.T) {
	res := sample()
	res.Entries = res.Entries[4:5] // only the within-limit provider
	res.Entries[0].OverLimit = false
	md := OutdatedMarkdown(res)
	if !strings.Contains(md, "Nothing needs an upgrade") {
		t.Errorf("expected the all-clear line:\n%s", md)
	}
	if strings.Contains(md, "- [ ]") {
		t.Errorf("no checkboxes expected when nothing is over the limit:\n%s", md)
	}
}

func TestOutdatedText(t *testing.T) {
	txt := OutdatedText(sample())
	for _, want := range []string{
		"4 of 6 pinned versions need an upgrade (max behind: minor)",
		"a/main.tf:3: upgrade: module terraform-aws-modules/vpc/aws 5.8.1 (latest 6.0.0) resolves to 5.8.1, major behind",
		"b/main.tf:30: ignored: module terraform-aws-modules/s3-bucket/aws",
		"a/main.tf:12: behind: provider hashicorp/random",
		"a/main.tf:40: not checked: module git::https://example.com/x.git: not a registry source",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("text missing %q:\n%s", want, txt)
		}
	}
}

func TestOutdatedJSON(t *testing.T) {
	var b strings.Builder
	if err := OutdatedJSON(&b, sample()); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MaxBehind string `json:"max_behind"`
		Entries   []struct {
			Behind    string `json:"behind"`
			OverLimit bool   `json:"over_limit"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(b.String()), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, b.String())
	}
	if doc.MaxBehind != "minor" || doc.Entries[0].Behind != "major" || !doc.Entries[0].OverLimit {
		t.Errorf("unexpected JSON: %+v", doc)
	}

	b.Reset()
	if err := OutdatedJSON(&b, outdated.Result{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"entries": []`) {
		t.Errorf("empty result should carry an empty list, got %s", b.String())
	}
}
