package rules

import (
	"strings"
	"testing"
)

func TestAvailableCorporaAreValid(t *testing.T) {
	list, err := Available()
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	if len(list) < 2 {
		t.Fatalf("expected at least the aws 4->5 and 5->6 corpora, got %d", len(list))
	}

	seen := map[string]string{}
	for _, c := range list {
		if c.GuideURL == "" {
			t.Errorf("%s: missing guide_url", c.Step())
		}
		if len(c.Rules) == 0 {
			t.Errorf("%s: no rules", c.Step())
		}
		for _, r := range c.Rules {
			// IDs are quoted in reports and in customer emails, so they must be
			// unique across the whole corpus, not just within one file.
			if prev, dup := seen[r.ID]; dup {
				t.Errorf("duplicate rule id %q in %s and %s", r.ID, prev, c.Step())
			}
			seen[r.ID] = c.Step()

			if r.GuideAnchor == "" {
				t.Errorf("%s: rule %s has no guide_anchor; every claim must be checkable at source", c.Step(), r.ID)
			}
			if !strings.HasPrefix(r.GuideAnchor, "#") {
				t.Errorf("%s: rule %s anchor %q should start with #", c.Step(), r.ID, r.GuideAnchor)
			}
			if r.Match.BlockKind == "provider" && r.Match.Type != c.Provider {
				t.Errorf("%s: rule %s matches provider %q", c.Step(), r.ID, r.Match.Type)
			}
			if r.Match.BlockKind != "provider" && !strings.HasPrefix(r.Match.Type, c.Provider+"_") {
				t.Errorf("%s: rule %s matches type %q, which is not a %s type", c.Step(), r.ID, r.Match.Type, c.Provider)
			}
		}
	}
}

func TestLoadKnownStep(t *testing.T) {
	c, err := Load("aws", 5, 6)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Step() != "aws 5 -> 6" {
		t.Errorf("Step = %q", c.Step())
	}

	var eip Rule
	for _, r := range c.Rules {
		if r.ID == "aws6-eip-vpc" {
			eip = r
		}
	}
	if eip.ID == "" {
		t.Fatal("aws6-eip-vpc missing from the 5->6 corpus")
	}
	if eip.Severity != SeverityBreaking || eip.Kind != KindRemoved {
		t.Errorf("aws6-eip-vpc = %s/%s, want breaking/removed", eip.Severity, eip.Kind)
	}
	want := "https://registry.terraform.io/providers/hashicorp/aws/latest/docs/guides/version-6-upgrade#resource-aws_eip"
	if got := c.GuideLink(eip); got != want {
		t.Errorf("GuideLink = %q, want %q", got, want)
	}
}

func TestLoadUnknownStepListsWhatExists(t *testing.T) {
	_, err := Load("azurerm", 3, 4)
	if err == nil {
		t.Fatal("expected an error for an uncovered provider")
	}
	if !strings.Contains(err.Error(), "aws 5 -> 6") {
		t.Errorf("error should list the available steps, got: %v", err)
	}
}

func TestParseRejectsMalformedCorpora(t *testing.T) {
	cases := map[string]string{
		"bad severity": `
provider: aws
from: 1
to: 2
rules:
  - id: x
    kind: removed
    severity: catastrophic
    match: {block_kind: resource, type: aws_eip}
    title: t
`,
		"bad kind": `
provider: aws
from: 1
to: 2
rules:
  - id: x
    kind: vanished
    severity: breaking
    match: {block_kind: resource, type: aws_eip}
    title: t
`,
		"bad block kind": `
provider: aws
from: 1
to: 2
rules:
  - id: x
    kind: removed
    severity: breaking
    match: {block_kind: locals, type: aws_eip}
    title: t
`,
		"duplicate id": `
provider: aws
from: 1
to: 2
rules:
  - id: x
    kind: removed
    severity: breaking
    match: {block_kind: resource, type: aws_eip}
    title: t
  - id: x
    kind: removed
    severity: breaking
    match: {block_kind: resource, type: aws_eip}
    title: t
`,
		"absent without attributes": `
provider: aws
from: 1
to: 2
rules:
  - id: x
    kind: required
    severity: breaking
    match: {block_kind: resource, type: aws_eip, absent: true}
    title: t
`,
		"unknown field": `
provider: aws
from: 1
to: 2
rules:
  - id: x
    kind: removed
    severity: breaking
    match: {block_kind: resource, type: aws_eip}
    title: t
    sevrity: breaking
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parse("corpus/aws-1-to-2.yaml", []byte(src)); err == nil {
				t.Fatal("expected a validation error, got nil")
			}
		})
	}
}

func TestParseRejectsFilenameMismatch(t *testing.T) {
	src := "provider: aws\nfrom: 5\nto: 6\nrules: []\n"
	if _, err := parse("corpus/aws-4-to-5.yaml", []byte(src)); err == nil {
		t.Fatal("expected an error when the header contradicts the filename")
	}
}
