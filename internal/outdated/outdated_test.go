package outdated

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moveeeax/tf-upgrade-advisor/internal/config"
)

const fixture = "../../testdata/outdated"

func check(t *testing.T, limit Distance) (Result, *fakeRegistry) {
	t.Helper()
	cfg, err := config.LoadTree(fixture)
	if err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	ignores, err := LoadIgnores(filepath.Join(fixture, DefaultIgnoreFile), true)
	if err != nil {
		t.Fatalf("LoadIgnores: %v", err)
	}
	reg := newFakeRegistry(t)
	res, err := Check(context.Background(), cfg, NewRegistry(reg.URL), Options{MaxBehind: limit, Ignores: ignores})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return res, reg
}

// entry returns the entry for a local name in one stack.
func entry(t *testing.T, res Result, stack, name string) Entry {
	t.Helper()
	for _, e := range res.Entries {
		if e.Name == name && strings.Contains(e.File, filepath.Join("stacks", stack)) {
			return e
		}
	}
	t.Fatalf("no entry %s in stack %s; have %+v", name, stack, res.Entries)
	return Entry{}
}

func TestCheckClassifiesEveryPin(t *testing.T) {
	res, _ := check(t, DistanceMinor)

	cases := []struct {
		stack, name       string
		resolved, latest  string
		behind            Distance
		over, missing     bool
		deprecated, ignor bool
	}{
		// Exact pin, a major behind; the 6.1.0 prerelease is not "latest".
		{"network", "vpc", "5.8.1", "6.0.0", DistanceMajor, true, false, false, false},
		// `~> 5.1.0` holds the //subdir module at 5.1.x: minor behind, allowed.
		{"network", "http_sg", "5.1.2", "5.2.0", DistanceMinor, false, false, false, false},
		{"network", "aws", "5.100.0", "6.2.0", DistanceMajor, true, false, false, false},
		{"network", "random", "3.6.3", "3.6.3", DistanceNone, false, false, false, false},
		{"data", "aws", "6.2.0", "6.2.0", DistanceNone, false, false, false, false},
		// 1.2.3 is not in the registry list any more.
		{"data", "pinned_to_yanked", "", "1.2.4", DistanceNone, true, true, false, false},
		// Behind, but held back by the ignore file.
		{"data", "bucket", "4.1.0", "5.0.0", DistanceMajor, false, false, false, true},
		// Unpinned resolves to latest, which the registry marks deprecated.
		{"data", "iam", "5.0.0", "5.0.0", DistanceNone, true, false, true, false},
		// Provider-level registry warning.
		{"data", "template", "2.2.0", "2.2.0", DistanceNone, true, false, true, false},
	}
	for _, c := range cases {
		e := entry(t, res, c.stack, c.name)
		if e.Resolved != c.resolved || e.Latest != c.latest || e.Behind != c.behind {
			t.Errorf("%s/%s: resolved %q latest %q behind %s, want %q %q %s",
				c.stack, c.name, e.Resolved, e.Latest, e.Behind, c.resolved, c.latest, c.behind)
		}
		if e.OverLimit != c.over || e.Missing != c.missing ||
			(e.Deprecation != "") != c.deprecated || e.Ignored != c.ignor {
			t.Errorf("%s/%s: over=%t missing=%t deprecated=%q ignored=%t", c.stack, c.name,
				e.OverLimit, e.Missing, e.Deprecation, e.Ignored)
		}
	}

	iam := entry(t, res, "data", "iam")
	if iam.Deprecation != "Use terraform-aws-modules/iam-role/aws. (https://example.com/iam-deprecation)" {
		t.Errorf("deprecation note = %q", iam.Deprecation)
	}
	if !iam.Unpinned() {
		t.Error("iam has no version argument and should read as unpinned")
	}
	if got := entry(t, res, "data", "bucket").IgnoreReason; !strings.Contains(got, "ACL") {
		t.Errorf("ignore reason not carried through: %q", got)
	}
	if n := len(res.OverLimit()); n != 5 {
		t.Errorf("over-limit = %d, want 5", n)
	}
}

func TestCheckListsWhatItDidNotCheck(t *testing.T) {
	res, _ := check(t, DistanceMinor)
	reasons := map[string]string{}
	for _, u := range res.Unchecked {
		reasons[u.Name] = u.Reason
	}
	if !strings.Contains(reasons["legacy"], "not a registry source") {
		t.Errorf("git module: %q", reasons["legacy"])
	}
	if !strings.Contains(reasons["corp"], "tf.example.com") {
		t.Errorf("private provider: %q", reasons["corp"])
	}
	if _, ok := reasons["tags"]; ok {
		t.Error("a local module is part of the repository and should not be listed")
	}
	for _, e := range res.Entries {
		if strings.Contains(e.File, ".terraform") {
			t.Errorf("entry from a .terraform directory: %+v", e)
		}
	}
	if len(res.StaleIgnores) != 1 || res.StaleIgnores[0].Source != "hashicorp/null" {
		t.Errorf("stale ignores = %+v, want hashicorp/null", res.StaleIgnores)
	}
}

func TestCheckAsksTheRegistryOncePerSource(t *testing.T) {
	_, reg := check(t, DistanceMinor)
	if n := reg.requests("/v1/providers/hashicorp/aws/versions"); n != 1 {
		t.Errorf("hashicorp/aws fetched %d times, want 1", n)
	}
}

func TestMaxBehindMovesTheLine(t *testing.T) {
	strict, _ := check(t, DistanceNone)
	if !entry(t, strict, "network", "http_sg").OverLimit {
		t.Error("with --max-behind none a minor-behind pin is over the limit")
	}
	loose, _ := check(t, DistanceMajor)
	if entry(t, loose, "network", "vpc").OverLimit {
		t.Error("with --max-behind major a major-behind pin is within the limit")
	}
	// Yanked and deprecated pins are over the limit whatever the distance.
	if !entry(t, loose, "data", "pinned_to_yanked").OverLimit || !entry(t, loose, "data", "template").OverLimit {
		t.Error("yanked and deprecated pins must stay over the limit")
	}
}

func TestRegistryOutageIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down for maintenance", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	cfg, err := config.LoadTree(fixture)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Check(context.Background(), cfg, NewRegistry(srv.URL), Options{MaxBehind: DistanceMinor})
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("expected a 503 error, got %v", err)
	}
}

func TestDistance(t *testing.T) {
	cases := []struct {
		constraint string
		versions   []string
		resolved   string
		behind     Distance
	}{
		{"1.2.3", []string{"1.2.3", "1.2.4"}, "1.2.3", DistancePatch},
		{"~> 1.2", []string{"1.2.0", "1.9.0", "2.0.0"}, "1.9.0", DistanceMajor},
		{">= 1.0, < 2.0", []string{"1.0.0", "1.4.0"}, "1.4.0", DistanceNone},
		{"= 2.0.0-rc1", []string{"2.0.0-rc1", "2.0.0"}, "2.0.0-rc1", DistancePatch},
		{"v1.0.0", []string{"v1.0.0", "1.1.0"}, "v1.0.0", DistanceMinor},
	}
	for _, c := range cases {
		e := Entry{Constraint: c.constraint}
		var rels []Release
		for _, v := range c.versions {
			rels = append(rels, Release{Version: v})
		}
		if reason := resolve(&e, rels); reason != "" {
			t.Errorf("%s: %s", c.constraint, reason)
			continue
		}
		if e.Resolved != c.resolved || e.Behind != c.behind {
			t.Errorf("%s over %v: resolved %q behind %s, want %q %s",
				c.constraint, c.versions, e.Resolved, e.Behind, c.resolved, c.behind)
		}
	}

	e := Entry{Constraint: "not a version"}
	if reason := resolve(&e, []Release{{Version: "1.0.0"}}); reason == "" {
		t.Error("an unparseable constraint should be reported, not guessed at")
	}
}

func TestParseDistance(t *testing.T) {
	for _, s := range []string{"none", "patch", "minor", "major"} {
		d, err := ParseDistance(s)
		if err != nil || d.String() != s {
			t.Errorf("ParseDistance(%q) = %v, %v", s, d, err)
		}
	}
	if _, err := ParseDistance("huge"); err == nil {
		t.Error("expected an error for an unknown distance")
	}
}
