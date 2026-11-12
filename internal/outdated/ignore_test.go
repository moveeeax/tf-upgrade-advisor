package outdated

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseIgnoresRequiresAReason(t *testing.T) {
	_, err := ParseIgnores([]byte("ignore:\n  - source: hashicorp/aws\n"), "x.yaml")
	if err == nil || !strings.Contains(err.Error(), "reason is required") {
		t.Fatalf("expected a missing-reason error, got %v", err)
	}
	_, err = ParseIgnores([]byte("ignore:\n  - source: hashicorp/aws\n    reason: \"  \"\n"), "x.yaml")
	if err == nil {
		t.Fatal("a blank reason must not count as a reason")
	}
}

func TestParseIgnoresRejectsUnknownFields(t *testing.T) {
	_, err := ParseIgnores([]byte("ignore:\n  - source: hashicorp/aws\n    resaon: typo\n"), "x.yaml")
	if err == nil {
		t.Fatal("expected an error for an unknown field")
	}
}

func TestParseIgnoresNormalizesSource(t *testing.T) {
	rules, err := ParseIgnores([]byte("ignore:\n  - source: Registry.Terraform.io/HashiCorp/AWS\n    reason: r\n"), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if rules[0].Source != "hashicorp/aws" {
		t.Errorf("source = %q", rules[0].Source)
	}
	if !rules[0].matches(Entry{Source: "hashicorp/aws", Constraint: "~> 5.0"}) {
		t.Error("a rule without a constraint matches every constraint")
	}
}

func TestIgnoreConstraintMustMatchExactly(t *testing.T) {
	r := IgnoreRule{Source: "hashicorp/aws", Constraint: "~> 5.0", Reason: "r"}
	if !r.matches(Entry{Source: "hashicorp/aws", Constraint: "~> 5.0"}) {
		t.Error("same constraint should match")
	}
	if r.matches(Entry{Source: "hashicorp/aws", Constraint: "~> 5.1"}) {
		t.Error("once the pin is edited the rule should stop applying")
	}
}

func TestLoadIgnoresMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), DefaultIgnoreFile)
	if rules, err := LoadIgnores(missing, false); err != nil || rules != nil {
		t.Errorf("a missing default file is fine, got %v, %v", rules, err)
	}
	if _, err := LoadIgnores(missing, true); err == nil {
		t.Error("a missing file named on the command line is an error")
	}
}

func TestEmptyIgnoreFile(t *testing.T) {
	rules, err := ParseIgnores(nil, "x.yaml")
	if err != nil || len(rules) != 0 {
		t.Errorf("empty file: %v, %v", rules, err)
	}
}
