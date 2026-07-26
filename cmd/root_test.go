package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code, err = Run(args, &out, &errBuf)
	return code, out.String(), errBuf.String(), err
}

func TestScanExitsOneOnBreakingFindings(t *testing.T) {
	code, out, _, err := run(t, "scan", "--provider", "aws", "--from", "5", "--to", "6", "../testdata/aws5")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != exitFindings {
		t.Errorf("exit code = %d, want %d so CI can gate on it", code, exitFindings)
	}
	if !strings.Contains(out, "- [ ]") {
		t.Errorf("expected a checklist on stdout:\n%s", out)
	}
}

func TestScanExitsZeroOnACleanConfiguration(t *testing.T) {
	code, out, _, err := run(t, "scan", "--from", "5", "--to", "6", "../testdata/clean")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != exitOK {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out, "No known breaking changes apply") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestFailOnNever(t *testing.T) {
	code, _, _, err := run(t, "scan", "--from", "5", "--to", "6", "--fail-on", "never", "../testdata/aws5")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != exitOK {
		t.Errorf("exit code = %d, want 0 with --fail-on never", code)
	}
}

func TestFailOnWarningIsStricterThanBreaking(t *testing.T) {
	// The clean fixture has no findings at all, so use the one that has
	// warnings but confirm the flag is actually parsed and applied.
	code, _, _, err := run(t, "scan", "--from", "5", "--to", "6", "--fail-on", "info", "../testdata/aws5")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != exitFindings {
		t.Errorf("exit code = %d, want 1", code)
	}

	if _, _, _, err := run(t, "scan", "--from", "5", "--to", "6", "--fail-on", "loud", "../testdata/aws5"); err == nil {
		t.Error("expected an error for an unknown --fail-on value")
	}
}

func TestJSONFormatAndOutputFile(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "report.json")
	code, out, _, err := run(t, "scan", "--from", "5", "--to", "6",
		"--format", "json", "--output", dst, "../testdata/aws5")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != exitFindings {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("expected JSON on stdout, got:\n%s", out)
	}
	written, readErr := os.ReadFile(dst)
	if readErr != nil {
		t.Fatalf("read --output file: %v", readErr)
	}
	if string(written) != out {
		t.Error("--output file differs from stdout")
	}
}

func TestUnknownFormatIsAnError(t *testing.T) {
	code, _, _, err := run(t, "scan", "--from", "5", "--to", "6", "--format", "yaml", "../testdata/clean")
	if err == nil {
		t.Fatal("expected an error for an unknown --format")
	}
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
}

func TestUncoveredStepIsAnError(t *testing.T) {
	code, _, _, err := run(t, "scan", "--provider", "google", "--from", "5", "--to", "6", "../testdata/clean")
	if err == nil {
		t.Fatal("expected an error for an uncovered provider")
	}
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(err.Error(), "aws 5 -> 6") {
		t.Errorf("error should name what is covered: %v", err)
	}
}

func TestMissingRequiredFlags(t *testing.T) {
	if _, _, _, err := run(t, "scan", "../testdata/clean"); err == nil {
		t.Error("expected an error when --from and --to are missing")
	}
}

func TestStepSummaryIsAppendedInsideActions(t *testing.T) {
	summary := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)

	if _, _, _, err := run(t, "scan", "--from", "5", "--to", "6", "../testdata/aws5"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("read step summary: %v", err)
	}
	if !strings.Contains(string(body), "## Provider upgrade: aws 5.x → 6.x") {
		t.Errorf("step summary not written:\n%s", body)
	}
}

func TestStepSummaryNotWrittenForJSON(t *testing.T) {
	summary := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)

	if _, _, _, err := run(t, "scan", "--from", "5", "--to", "6", "--format", "json", "../testdata/aws5"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(summary); !os.IsNotExist(err) {
		t.Error("JSON output should not land on the job summary page")
	}
}

func TestStepOutputs(t *testing.T) {
	out := filepath.Join(t.TempDir(), "output")
	t.Setenv("GITHUB_OUTPUT", out)

	if _, _, _, err := run(t, "scan", "--from", "5", "--to", "6", "--format", "text", "../testdata/aws5"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read step outputs: %v", err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("malformed output line %q", line)
		}
		got[k] = v
	}
	for _, key := range []string{"breaking", "warning", "info", "total", "has-breaking"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing step output %q; have %v", key, got)
		}
	}
	if got["has-breaking"] != "true" {
		t.Errorf("has-breaking = %q, want true", got["has-breaking"])
	}
	if got["breaking"] == "0" {
		t.Errorf("breaking = 0 for a fixture full of breaking changes")
	}
}

func TestRulesCommand(t *testing.T) {
	code, out, _, err := run(t, "rules")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != exitOK {
		t.Errorf("exit code = %d, want 0", code)
	}
	for _, want := range []string{"aws 4 -> 5", "aws 5 -> 6", "rules ("} {
		if !strings.Contains(out, want) {
			t.Errorf("rules output missing %q:\n%s", want, out)
		}
	}
}
