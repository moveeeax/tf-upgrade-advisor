package outdated

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// IssueMarker is embedded in the issue body so later runs find and edit the
// same issue instead of opening another one. Title and labels are left for
// people to change.
const IssueMarker = "<!-- tf-upgrade-advisor:outdated -->"

// maxIssueBody keeps the body under GitHub's issue body size limit, which the
// API reports as 65536 characters; the margin leaves room for the marker and
// the truncation note.
const maxIssueBody = 60000

// maxIssuePages bounds the search for an existing issue: 1,000 open issues.
const maxIssuePages = 10

// GitHub is the slice of the REST API needed to keep one issue up to date.
type GitHub struct {
	BaseURL string // https://api.github.com, or $GITHUB_API_URL on GHES
	Token   string
	Repo    string // owner/name
	HTTP    *http.Client
}

// NewGitHub returns a client for repo.
func NewGitHub(baseURL, token, repo string) *GitHub {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &GitHub{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Repo:    repo,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Issue is the part of a GitHub issue this tool reads.
type Issue struct {
	Number      int             `json:"number"`
	HTMLURL     string          `json:"html_url"`
	Body        string          `json:"body"`
	State       string          `json:"state"`
	PullRequest json.RawMessage `json:"pull_request,omitempty"`
}

// IssueAction says what SyncIssue did.
type IssueAction string

const (
	IssueNone    IssueAction = "none"
	IssueCreated IssueAction = "created"
	IssueUpdated IssueAction = "updated"
	IssueClosed  IssueAction = "closed"
)

// SyncIssue keeps a single open issue in step with the report. With work to
// do it edits the open issue carrying IssueMarker, or opens one. With nothing
// left it closes that issue with the all-clear body, and does nothing when
// there is none. Labels are applied only when an issue is created.
func (g *GitHub) SyncIssue(ctx context.Context, title, body string, labels []string, pending bool) (IssueAction, *Issue, error) {
	body = withMarker(body)
	existing, err := g.findIssue(ctx)
	if err != nil {
		return IssueNone, nil, err
	}

	switch {
	case existing == nil && !pending:
		return IssueNone, nil, nil
	case existing == nil:
		payload := map[string]any{"title": title, "body": body}
		if len(labels) > 0 {
			payload["labels"] = labels
		}
		var created Issue
		if err := g.do(ctx, http.MethodPost, "/repos/"+g.Repo+"/issues", payload, &created); err != nil {
			return IssueNone, nil, fmt.Errorf("create issue: %w", err)
		}
		return IssueCreated, &created, nil
	case pending:
		var updated Issue
		path := fmt.Sprintf("/repos/%s/issues/%d", g.Repo, existing.Number)
		if err := g.do(ctx, http.MethodPatch, path, map[string]any{"body": body}, &updated); err != nil {
			return IssueNone, nil, fmt.Errorf("update issue #%d: %w", existing.Number, err)
		}
		return IssueUpdated, &updated, nil
	default:
		var closed Issue
		path := fmt.Sprintf("/repos/%s/issues/%d", g.Repo, existing.Number)
		payload := map[string]any{"body": body, "state": "closed", "state_reason": "completed"}
		if err := g.do(ctx, http.MethodPatch, path, payload, &closed); err != nil {
			return IssueNone, nil, fmt.Errorf("close issue #%d: %w", existing.Number, err)
		}
		return IssueClosed, &closed, nil
	}
}

// findIssue returns the oldest open issue carrying the marker. Pull requests
// share the issues endpoint and are skipped.
func (g *GitHub) findIssue(ctx context.Context) (*Issue, error) {
	for page := 1; page <= maxIssuePages; page++ {
		var batch []Issue
		path := fmt.Sprintf("/repos/%s/issues?state=open&sort=created&direction=asc&per_page=100&page=%d", g.Repo, page)
		if err := g.do(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, fmt.Errorf("list issues: %w", err)
		}
		for i := range batch {
			if len(batch[i].PullRequest) == 0 && strings.Contains(batch[i].Body, IssueMarker) {
				return &batch[i], nil
			}
		}
		if len(batch) < 100 {
			break
		}
	}
	return nil, nil
}

func (g *GitHub) do(ctx context.Context, method, path string, payload, into any) error {
	var reader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "tf-upgrade-advisor")
	req.Header.Set("Authorization", "Bearer "+g.Token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GitHub returned %s: %s", resp.Status, bytes.TrimSpace(snippet))
	}
	if into == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

// withMarker prepends the marker and trims an oversized body at a line break.
func withMarker(body string) string {
	if len(body) > maxIssueBody {
		cut := strings.LastIndex(body[:maxIssueBody], "\n")
		if cut < 0 {
			cut = maxIssueBody
		}
		body = body[:cut] + "\n\n_Report truncated to fit the issue. Run with `--format markdown --output` for the full list._\n"
	}
	return IssueMarker + "\n" + body
}
