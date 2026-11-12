package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const outdatedFixture = "../testdata/outdated"

// registry serves every module and provider of the fixture with a 1.0.0 and a
// 2.0.0 release, plus the versions the fixture pins exactly, so the pins land
// a major behind; the stack pinned to 1.2.3 of example/gone/aws is yanked.
func registry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		versions := []map[string]any{{"version": "1.0.0"}, {"version": "2.0.0"}}
		for _, v := range []string{"5.8.1", "5.1.0", "4.1.0", "2.2.0", "3.6.0", "5.40.0", "6.0.0", "9.0.0"} {
			versions = append(versions, map[string]any{"version": v})
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/modules/example/gone/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"modules": []any{map[string]any{"versions": versions[:2]}}})
		case strings.HasPrefix(r.URL.Path, "/v1/modules/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"modules": []any{map[string]any{"versions": versions}}})
		case strings.HasPrefix(r.URL.Path, "/v1/providers/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"versions": versions})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestOutdatedExitsOneWhenSomethingNeedsAnUpgrade(t *testing.T) {
	code, out, _, err := run(t, "outdated", "--registry", registry(t), outdatedFixture)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != exitFindings {
		t.Errorf("exit code = %d, want %d", code, exitFindings)
	}
	for _, want := range []string{"## Module and provider versions", "- [ ] **`terraform-aws-modules/vpc/aws`**", "yanked"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestOutdatedFailFalse(t *testing.T) {
	code, _, _, err := run(t, "outdated", "--registry", registry(t), "--fail=false", "--format", "text", outdatedFixture)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != exitOK {
		t.Errorf("exit code = %d, want 0 with --fail=false", code)
	}
}

func TestOutdatedRejectsBadFlags(t *testing.T) {
	for _, args := range [][]string{
		{"outdated", "--max-behind", "huge", outdatedFixture},
		{"outdated", "--format", "yaml", outdatedFixture},
		{"outdated", "--ignore-file", filepath.Join(t.TempDir(), "missing.yaml"), outdatedFixture},
	} {
		code, _, _, err := run(t, args...)
		if err == nil || code != exitError {
			t.Errorf("%v: code %d err %v, want a usage error", args, code, err)
		}
	}
}

func TestOutdatedIgnoreFileNeedsReasons(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "ignore.yaml")
	if err := os.WriteFile(bad, []byte("ignore:\n  - source: hashicorp/aws\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := run(t, "outdated", "--registry", registry(t), "--ignore-file", bad, outdatedFixture)
	if err == nil || !strings.Contains(err.Error(), "reason is required") {
		t.Fatalf("expected a missing-reason error, got %v", err)
	}
}

func TestOutdatedOpenIssueNeedsRepoAndToken(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	_, _, _, err := run(t, "outdated", "--open-issue", outdatedFixture)
	if err == nil || !strings.Contains(err.Error(), "GITHUB_REPOSITORY") {
		t.Fatalf("expected a missing-repo error, got %v", err)
	}
	_, _, _, err = run(t, "outdated", "--open-issue", "--github-repo", "acme/infra", outdatedFixture)
	if err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Fatalf("expected a missing-token error, got %v", err)
	}
}

func TestOutdatedOpenIssue(t *testing.T) {
	var created map[string]any
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte("[]"))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/infra/issues":
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":7,"html_url":"https://github.com/acme/infra/issues/7","state":"open"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()
	t.Setenv("GITHUB_API_URL", gh.URL)
	t.Setenv("GITHUB_TOKEN", "t0ken")
	t.Setenv("GITHUB_REPOSITORY", "acme/infra")
	outputs := filepath.Join(t.TempDir(), "output")
	t.Setenv("GITHUB_OUTPUT", outputs)

	code, out, stderr, err := run(t, "outdated", "--registry", registry(t), "--format", "json",
		"--open-issue", "--label", "tech-debt", "--label", "terraform", outdatedFixture)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != exitFindings {
		t.Errorf("exit code = %d", code)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("stdout should stay JSON:\n%s", out)
	}
	// The issue body is the Markdown report whatever --format says.
	body, _ := created["body"].(string)
	if !strings.Contains(body, "tf-upgrade-advisor:outdated") || !strings.Contains(body, "## Module and provider versions") {
		t.Errorf("issue body = %q", body)
	}
	if labels, _ := created["labels"].([]any); len(labels) != 2 {
		t.Errorf("labels = %v", created["labels"])
	}
	if !strings.Contains(stderr, "issue #7 created") {
		t.Errorf("stderr = %q", stderr)
	}
	raw, err := os.ReadFile(outputs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"needs-upgrade=", "issue-number=7", "issue-url=https://github.com/acme/infra/issues/7"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("step outputs missing %q:\n%s", want, raw)
		}
	}
}
