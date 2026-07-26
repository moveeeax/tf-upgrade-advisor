package scan

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/moveeeax/tf-upgrade-advisor/internal/config"
	"github.com/moveeeax/tf-upgrade-advisor/internal/rules"
)

func runFixture(t *testing.T, dir string, from, to int) Result {
	t.Helper()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load(%s): %v", dir, err)
	}
	corpus, err := rules.Load("aws", from, to)
	if err != nil {
		t.Fatalf("rules.Load: %v", err)
	}
	return Run(cfg, corpus)
}

// find returns the findings for one rule, keyed by "file:line".
func find(res Result, ruleID string) map[string]Finding {
	out := map[string]Finding{}
	for _, f := range res.Findings {
		if f.RuleID == ruleID {
			out[Rel(res.Root, f.File)+":"+itoa(f.Line)] = f
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestScanFindsAttributeRulesWithLineNumbers(t *testing.T) {
	res := runFixture(t, "../../testdata/aws5", 5, 6)

	cases := []struct {
		rule     string
		at       string
		severity rules.Severity
	}{
		{"aws6-eip-vpc", "main.tf:21", rules.SeverityBreaking},
		{"aws6-flow-log-log-group-name", "main.tf:25", rules.SeverityBreaking},
		{"aws6-provider-endpoints-opsworks", "main.tf:16", rules.SeverityBreaking},
		{"aws6-ssm-association-instance-id", filepath.Join("modules", "network", "main.tf") + ":3", rules.SeverityBreaking},
		{"aws6-instance-user-data-plaintext", "main.tf:36", rules.SeverityWarning},
	}
	for _, c := range cases {
		hits := find(res, c.rule)
		f, ok := hits[c.at]
		if !ok {
			t.Errorf("%s: no finding at %s (got %v)", c.rule, c.at, keys(hits))
			continue
		}
		if f.Severity != c.severity {
			t.Errorf("%s: severity %q, want %q", c.rule, f.Severity, c.severity)
		}
		if f.Guide == "" || !strings.Contains(f.Guide, "#") {
			t.Errorf("%s: finding has no deep link (%q)", c.rule, f.Guide)
		}
	}
}

func TestScanReportsEveryOccurrenceOfAMultiAttributeRule(t *testing.T) {
	res := runFixture(t, "../../testdata/aws5", 5, 6)
	hits := find(res, "aws6-instance-cpu-core-count")
	if len(hits) != 2 {
		t.Fatalf("expected one finding per removed argument, got %v", keys(hits))
	}
	attrs := map[string]bool{}
	for _, f := range hits {
		attrs[f.Attribute] = true
	}
	for _, want := range []string{"cpu_core_count", "cpu_threads_per_core"} {
		if !attrs[want] {
			t.Errorf("no finding for %s; got %v", want, attrs)
		}
	}
}

func TestScanMatchesThroughDynamicBlocks(t *testing.T) {
	res := runFixture(t, "../../testdata/aws5", 5, 6)
	hits := find(res, "aws6-nullable-bool-launch-template")
	if len(hits) != 1 {
		t.Fatalf("expected the dynamic block to match once, got %v", keys(hits))
	}
	for _, f := range hits {
		if f.Attribute != "block_device_mappings.ebs.encrypted" {
			t.Errorf("attribute = %q", f.Attribute)
		}
	}
}

func TestAbsentRulesFireOnlyWhenTheArgumentIsMissing(t *testing.T) {
	res := runFixture(t, "../../testdata/aws5", 5, 6)

	// s3_prefix is missing from the fixture, so the rule must fire, anchored at
	// the resource block rather than at some unrelated attribute.
	hits := find(res, "aws6-cur-report-definition-s3-prefix-required")
	if len(hits) != 1 {
		t.Fatalf("expected one finding for the missing s3_prefix, got %v", keys(hits))
	}
	for at, f := range hits {
		if at != "main.tf:57" {
			t.Errorf("finding anchored at %s, want the resource block at main.tf:57", at)
		}
		if f.Attribute != "" {
			t.Errorf("absent-rule finding should not name an attribute, got %q", f.Attribute)
		}
	}

	// The clean fixture sets neither of the redshift defaults nor the resource
	// itself, so nothing may fire there.
	clean := runFixture(t, "../../testdata/clean", 5, 6)
	if len(clean.Findings) != 0 {
		t.Fatalf("clean fixture produced findings: %+v", clean.Findings)
	}
	if clean.HasBreaking() {
		t.Error("clean fixture reported a breaking change")
	}
}

func TestScanFindsWholeResourceRemovals(t *testing.T) {
	res := runFixture(t, "../../testdata/aws5", 5, 6)
	hits := find(res, "aws6-opsworks-stack-removed")
	if len(hits) != 1 {
		t.Fatalf("expected the removed resource type to be reported once, got %v", keys(hits))
	}
	for _, f := range hits {
		if f.Address != "aws_opsworks_stack.legacy" {
			t.Errorf("address = %q", f.Address)
		}
	}
}

func TestFindingsAreSortedBySeverityThenLocation(t *testing.T) {
	res := runFixture(t, "../../testdata/aws5", 5, 6)
	if !res.HasBreaking() {
		t.Fatal("fixture should contain breaking findings")
	}
	prevRank, prevFile, prevLine := -1, "", 0
	for _, f := range res.Findings {
		rank := f.Severity.Rank()
		switch {
		case rank < prevRank:
			t.Fatalf("severity out of order at %s:%d", f.File, f.Line)
		case rank > prevRank:
			prevRank, prevFile, prevLine = rank, f.File, f.Line
		default:
			if f.File < prevFile || (f.File == prevFile && f.Line < prevLine) {
				t.Fatalf("location out of order at %s:%d", f.File, f.Line)
			}
			prevFile, prevLine = f.File, f.Line
		}
	}
}

func TestPinFinding(t *testing.T) {
	res := runFixture(t, "../../testdata/aws5", 5, 6)
	hits := find(res, PinRuleID)
	if len(hits) != 1 {
		t.Fatalf("expected one pin finding, got %v", keys(hits))
	}
	f := hits["main.tf:5"]
	if f.RuleID != PinRuleID {
		t.Fatalf("pin finding not anchored at main.tf:5, got %v", keys(hits))
	}
	if !strings.Contains(f.Title, "~> 5.40") {
		t.Errorf("pin finding should quote the current constraint, got %q", f.Title)
	}
	if !strings.Contains(f.SuggestedDiff, `version = "~> 6.0"`) {
		t.Errorf("pin finding should suggest the target series, got %q", f.SuggestedDiff)
	}

	// A configuration already allowing 6.x must not be told to bump its pin.
	clean := runFixture(t, "../../testdata/clean", 5, 6)
	if len(find(clean, PinRuleID)) != 0 {
		t.Error("a ~> 6.0 pin was reported as still on 5.x")
	}
}

func TestAllows(t *testing.T) {
	cases := []struct {
		constraint string
		major      int
		want       bool
	}{
		{"~> 5.40", 6, false},
		{"~> 5.0", 6, false},
		{"5.40.0", 6, false},
		{">= 5.0, < 6.0", 6, false},
		{">= 4.0, < 6.0", 6, false},
		{"~> 6.0", 6, true},
		{">= 6.0", 6, true},
		{">= 5.0", 6, true}, // already open to 6.x: nothing to advise
		{"> 5.0", 6, true},
		{"<= 6.0", 6, true},
		{"~> 6.2", 6, false}, // 6.0.0 is below the floor, so the pin still blocks init
		{"not a version", 6, true},
		{"1.2.3.4", 6, true},
	}
	for _, c := range cases {
		if got := allows(c.constraint, c.major); got != c.want {
			t.Errorf("allows(%q, %d) = %v, want %v", c.constraint, c.major, got, c.want)
		}
	}
}

func TestScanCountsAndMetadata(t *testing.T) {
	res := runFixture(t, "../../testdata/aws5", 5, 6)
	if res.DirsScanned != 2 {
		t.Errorf("DirsScanned = %d, want 2", res.DirsScanned)
	}
	if res.BlocksScanned == 0 || res.RulesEvaluated == 0 {
		t.Errorf("empty counters: %+v", res)
	}
	if len(res.SkippedModules) != 1 {
		t.Errorf("SkippedModules = %v, want the registry module", res.SkippedModules)
	}
	counts := res.Counts()
	if counts[rules.SeverityBreaking] == 0 || counts[rules.SeverityWarning] == 0 {
		t.Errorf("counts = %v", counts)
	}
}

func TestUnrelatedStepDoesNotMatch(t *testing.T) {
	// The 4->5 corpus must not fire on 6.x-only removals, and vice versa: the
	// aws5 fixture is a 5.x configuration, so `vpc` on aws_eip is a deprecation
	// there, not a removal.
	res := runFixture(t, "../../testdata/aws5", 4, 5)
	if len(find(res, "aws6-eip-vpc")) != 0 {
		t.Error("a 5->6 rule fired while scanning 4->5")
	}
	hits := find(res, "aws5-eip-vpc-deprecated")
	if len(hits) != 1 {
		t.Fatalf("expected the 4->5 deprecation, got %v", keys(hits))
	}
	for _, f := range hits {
		if f.Severity != rules.SeverityWarning {
			t.Errorf("severity = %q, want warning", f.Severity)
		}
	}
}

func keys(m map[string]Finding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
