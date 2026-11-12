package outdated

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeGitHub keeps issues in memory and implements list, create and edit.
type fakeGitHub struct {
	*httptest.Server
	mu     sync.Mutex
	issues []map[string]any
	auth   []string
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		const base = "/repos/acme/infra/issues"
		switch {
		case r.Method == http.MethodGet && r.URL.Path == base:
			if r.URL.Query().Get("state") != "open" {
				http.Error(w, "test expects state=open", http.StatusBadRequest)
				return
			}
			open := []map[string]any{}
			for _, is := range f.issues {
				if is["state"] == "open" {
					open = append(open, is)
				}
			}
			_ = json.NewEncoder(w).Encode(open)
		case r.Method == http.MethodPost && r.URL.Path == base:
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			n := len(f.issues) + 1
			in["number"] = n
			in["state"] = "open"
			in["html_url"] = fmt.Sprintf("https://github.com/acme/infra/issues/%d", n)
			f.issues = append(f.issues, in)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(in)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, base+"/"):
			n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, base+"/"))
			if n < 1 || n > len(f.issues) {
				http.NotFound(w, r)
				return
			}
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			for k, v := range in {
				f.issues[n-1][k] = v
			}
			_ = json.NewEncoder(w).Encode(f.issues[n-1])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func TestSyncIssueCreatesThenEditsThenCloses(t *testing.T) {
	fake := newFakeGitHub(t)
	// An unrelated pull request mentioning the tool must not be picked up.
	fake.issues = append(fake.issues, map[string]any{
		"number": 1, "state": "open", "body": IssueMarker, "pull_request": map[string]any{"url": "x"},
	})
	gh := NewGitHub(fake.URL, "t0ken", "acme/infra")
	ctx := context.Background()

	action, issue, err := gh.SyncIssue(ctx, "Upgrades", "first list", []string{"tech-debt"}, true)
	if err != nil || action != IssueCreated || issue.Number != 2 {
		t.Fatalf("first run: %s %+v %v", action, issue, err)
	}
	if labels, _ := fake.issues[1]["labels"].([]any); len(labels) != 1 || labels[0] != "tech-debt" {
		t.Errorf("labels = %v", fake.issues[1]["labels"])
	}

	action, issue, err = gh.SyncIssue(ctx, "Upgrades", "second list", []string{"tech-debt"}, true)
	if err != nil || action != IssueUpdated || issue.Number != 2 {
		t.Fatalf("second run should edit #2 in place: %s %+v %v", action, issue, err)
	}
	if body := fake.issues[1]["body"].(string); !strings.Contains(body, "second list") || !strings.HasPrefix(body, IssueMarker) {
		t.Errorf("body = %q", body)
	}
	if len(fake.issues) != 2 {
		t.Errorf("a second issue was opened: %d", len(fake.issues))
	}

	action, _, err = gh.SyncIssue(ctx, "Upgrades", "all clear", nil, false)
	if err != nil || action != IssueClosed {
		t.Fatalf("third run should close the issue: %s %v", action, err)
	}
	if fake.issues[1]["state"] != "closed" || fake.issues[1]["state_reason"] != "completed" {
		t.Errorf("issue not closed as completed: %v", fake.issues[1])
	}

	action, issue, err = gh.SyncIssue(ctx, "Upgrades", "all clear", nil, false)
	if err != nil || action != IssueNone || issue != nil {
		t.Fatalf("nothing pending and no open issue should do nothing: %s %+v %v", action, issue, err)
	}
	for _, a := range fake.auth {
		if a != "Bearer t0ken" {
			t.Fatalf("Authorization = %q", a)
		}
	}
}

func TestSyncIssueReportsAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	_, _, err := NewGitHub(srv.URL, "t", "acme/infra").SyncIssue(context.Background(), "t", "b", nil, true)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected the 403 to surface, got %v", err)
	}
}

func TestWithMarkerTruncatesLongBodies(t *testing.T) {
	long := strings.Repeat("- line\n", maxIssueBody)
	body := withMarker(long)
	if len(body) > maxIssueBody+200 {
		t.Errorf("body is %d bytes", len(body))
	}
	if !strings.Contains(body, "truncated") {
		t.Error("truncation should be stated in the body")
	}
}

func TestDeprecationNote(t *testing.T) {
	cases := map[string]string{
		`null`:                           "",
		`false`:                          "",
		`{}`:                             "",
		`{"reason":"moved"}`:             "moved",
		`{"reason":"moved","link":"u"}`:  "moved (u)",
		`{"link":"u"}`:                   "u",
		`true`:                           "deprecated in the registry",
		`{"deprecated_at":"2026-01-01"}`: "deprecated in the registry",
	}
	for raw, want := range cases {
		if got := deprecationNote(json.RawMessage(raw)); got != want {
			t.Errorf("deprecationNote(%s) = %q, want %q", raw, got, want)
		}
	}
	if got := deprecationNote(nil); got != "" {
		t.Errorf("absent field = %q", got)
	}
}
