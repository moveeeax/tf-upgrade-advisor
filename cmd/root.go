// Package cmd wires the tf-upgrade-advisor CLI.
package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/moveeeax/tf-upgrade-advisor/internal/config"
	"github.com/moveeeax/tf-upgrade-advisor/internal/outdated"
	"github.com/moveeeax/tf-upgrade-advisor/internal/report"
	"github.com/moveeeax/tf-upgrade-advisor/internal/rules"
	"github.com/moveeeax/tf-upgrade-advisor/internal/scan"
)

// Exit codes: 0 = nothing to do, 1 = findings at or above --fail-on,
// 2 = usage or internal error.
const (
	exitOK       = 0
	exitFindings = 1
	exitError    = 2
)

// Version is stamped at build time by goreleaser; "dev" for local builds.
var Version = "dev"

// Run executes the CLI with the given args and streams and returns the process
// exit code. Exit-code logic lives here rather than in cobra's Execute so CI
// gating stays explicit and testable.
func Run(args []string, stdout, stderr io.Writer) (int, error) {
	code := exitOK

	root := &cobra.Command{
		Use:   "tf-upgrade-advisor",
		Short: "Turn a Terraform provider upgrade guide into a checklist for your code",
		Long: "tf-upgrade-advisor intersects a provider's published breaking-change list with\n" +
			"the HCL you actually wrote, so a major upgrade becomes a short checklist\n" +
			"instead of a long changelog. scan parses only: no state, no credentials, no\n" +
			"network, no model in the analysis path. outdated reads the Terraform\n" +
			"Registry's version lists to find pins that have fallen behind.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)
	root.AddCommand(scanCmd(&code), rulesCmd(), outdatedCmd(&code))

	if err := root.Execute(); err != nil {
		return exitError, err
	}
	return code, nil
}

func scanCmd(code *int) *cobra.Command {
	var (
		provider string
		from     int
		to       int
		format   string
		output   string
		failOn   string
	)

	cmd := &cobra.Command{
		Use:   "scan [dir]",
		Short: "Scan a Terraform configuration against a provider major-version step",
		Example: "  tf-upgrade-advisor scan --provider aws --from 5 --to 6 ./\n" +
			"  tf-upgrade-advisor scan --from 5 --to 6 --format json ./infra",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			threshold, err := parseFailOn(failOn)
			if err != nil {
				return err
			}

			corpus, err := rules.Load(provider, from, to)
			if err != nil {
				return err
			}
			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}
			res := scan.Run(cfg, corpus)

			rendered, err := render(format, res)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(cmd.OutOrStdout(), rendered); err != nil {
				return err
			}
			if output != "" {
				if err := os.WriteFile(output, []byte(rendered), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", output, err)
				}
			}
			// A GitHub Actions run gets the checklist on the job summary page
			// without the workflow having to plumb anything.
			if format == "markdown" {
				if err := appendStepSummary(rendered); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "tf-upgrade-advisor: %v\n", err)
				}
			}
			if err := appendStepOutputs(res); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "tf-upgrade-advisor: %v\n", err)
			}

			if failing(res, threshold) {
				*code = exitFindings
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "aws", "provider to check")
	cmd.Flags().IntVar(&from, "from", 0, "major version you are on (required)")
	cmd.Flags().IntVar(&to, "to", 0, "major version you want to reach (required)")
	cmd.Flags().StringVar(&format, "format", "markdown", "output format: markdown, text or json")
	cmd.Flags().StringVar(&output, "output", "", "also write the report to this file")
	cmd.Flags().StringVar(&failOn, "fail-on", "breaking",
		"lowest severity that exits 1: breaking, warning, info or never")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func outdatedCmd(code *int) *cobra.Command {
	var (
		maxBehind  string
		format     string
		output     string
		ignoreFile string
		registry   string
		fail       bool
		openIssue  bool
		labels     []string
		issueTitle string
		repo       string
	)

	cmd := &cobra.Command{
		Use:   "outdated [dir]",
		Short: "List module and provider pins that are behind, deprecated or yanked in the registry",
		Long: "outdated walks every directory under [dir] that holds *.tf files, collects each\n" +
			"registry module `version` and `required_providers` constraint, and compares the\n" +
			"version it resolves to with the newest release on the Terraform Registry.\n" +
			"Unlike scan, it needs network access to the registry.",
		Example: "  tf-upgrade-advisor outdated --max-behind minor .\n" +
			"  tf-upgrade-advisor outdated --open-issue --label tech-debt .",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			limit, err := outdated.ParseDistance(maxBehind)
			if err != nil {
				return fmt.Errorf("--max-behind: %w", err)
			}
			switch format {
			case "markdown", "text", "json":
			default:
				return fmt.Errorf("unknown --format %q (want markdown, text or json)", format)
			}
			var gh *outdated.GitHub
			if openIssue {
				if repo == "" {
					repo = os.Getenv("GITHUB_REPOSITORY")
				}
				if repo == "" {
					return fmt.Errorf("--open-issue needs --github-repo or $GITHUB_REPOSITORY")
				}
				token := os.Getenv("GITHUB_TOKEN")
				if token == "" {
					token = os.Getenv("GH_TOKEN")
				}
				if token == "" {
					return fmt.Errorf("--open-issue needs $GITHUB_TOKEN or $GH_TOKEN")
				}
				gh = outdated.NewGitHub(os.Getenv("GITHUB_API_URL"), token, repo)
			}

			cfg, err := config.LoadTree(dir)
			if err != nil {
				return err
			}
			explicit := ignoreFile != ""
			if !explicit {
				ignoreFile = filepath.Join(cfg.Root, outdated.DefaultIgnoreFile)
			}
			ignores, err := outdated.LoadIgnores(ignoreFile, explicit)
			if err != nil {
				return err
			}
			res, err := outdated.Check(cmd.Context(), cfg, outdated.NewRegistry(registry),
				outdated.Options{MaxBehind: limit, Ignores: ignores})
			if err != nil {
				return err
			}

			markdown := report.OutdatedMarkdown(res)
			var rendered string
			switch format {
			case "markdown":
				rendered = markdown
			case "text":
				rendered = report.OutdatedText(res)
			case "json":
				var b strings.Builder
				if err := report.OutdatedJSON(&b, res); err != nil {
					return err
				}
				rendered = b.String()
			}
			if _, err := io.WriteString(cmd.OutOrStdout(), rendered); err != nil {
				return err
			}
			if output != "" {
				if err := os.WriteFile(output, []byte(rendered), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", output, err)
				}
			}
			if format == "markdown" {
				if err := appendStepSummary(rendered); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "tf-upgrade-advisor: %v\n", err)
				}
			}

			pending := len(res.OverLimit())
			outputs := fmt.Sprintf("needs-upgrade=%d\n", pending)
			if gh != nil {
				action, issue, err := gh.SyncIssue(cmd.Context(), issueTitle, markdown, labels, pending > 0)
				if err != nil {
					return err
				}
				if issue != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "tf-upgrade-advisor: issue #%d %s: %s\n",
						issue.Number, action, issue.HTMLURL)
					outputs += fmt.Sprintf("issue-number=%d\nissue-url=%s\n", issue.Number, issue.HTMLURL)
				}
			}
			if err := appendEnvFile("GITHUB_OUTPUT", outputs); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "tf-upgrade-advisor: %v\n", err)
			}

			if fail && pending > 0 {
				*code = exitFindings
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&maxBehind, "max-behind", "minor",
		"largest distance behind latest that is accepted: none, patch, minor or major")
	cmd.Flags().StringVar(&format, "format", "markdown", "output format: markdown, text or json")
	cmd.Flags().StringVar(&output, "output", "", "also write the report to this file")
	cmd.Flags().StringVar(&ignoreFile, "ignore-file", "",
		"ignore rules (default: "+outdated.DefaultIgnoreFile+" in [dir], when present)")
	cmd.Flags().StringVar(&registry, "registry", outdated.DefaultRegistry, "Terraform Registry base URL")
	cmd.Flags().BoolVar(&fail, "fail", true, "exit 1 when anything needs an upgrade")
	cmd.Flags().BoolVar(&openIssue, "open-issue", false,
		"open one GitHub issue with the list, or edit the one opened earlier; close it when nothing is left")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "label for a newly opened issue (repeatable)")
	cmd.Flags().StringVar(&issueTitle, "issue-title", "Terraform module and provider upgrades",
		"title for a newly opened issue")
	cmd.Flags().StringVar(&repo, "github-repo", "", "owner/name for --open-issue (default: $GITHUB_REPOSITORY)")
	return cmd
}

func rulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rules",
		Short: "List the embedded rule corpora and their coverage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := rules.Available()
			if err != nil {
				return err
			}
			report.Corpora(cmd.OutOrStdout(), list)
			return nil
		},
	}
}

func render(format string, res scan.Result) (string, error) {
	switch format {
	case "markdown":
		return report.Markdown(res), nil
	case "text":
		return report.Text(res), nil
	case "json":
		var b strings.Builder
		if err := report.JSON(&b, res); err != nil {
			return "", err
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("unknown --format %q (want markdown, text or json)", format)
	}
}

// parseFailOn maps the flag to a severity rank; anything at or below that rank
// (i.e. at least as severe) fails the run. "never" is rank -1: nothing fails.
func parseFailOn(v string) (int, error) {
	switch v {
	case "breaking":
		return rules.SeverityBreaking.Rank(), nil
	case "warning":
		return rules.SeverityWarning.Rank(), nil
	case "info":
		return rules.SeverityInfo.Rank(), nil
	case "never":
		return -1, nil
	default:
		return 0, fmt.Errorf("unknown --fail-on %q (want breaking, warning, info or never)", v)
	}
}

func failing(res scan.Result, threshold int) bool {
	if threshold < 0 {
		return false
	}
	for _, f := range res.Findings {
		if f.Severity.Rank() <= threshold {
			return true
		}
	}
	return false
}

// appendStepSummary adds the report to the GitHub Actions job summary when
// running inside Actions. Outside Actions it is a no-op.
func appendStepSummary(body string) error {
	return appendEnvFile("GITHUB_STEP_SUMMARY", body+"\n")
}

// appendStepOutputs publishes the counts as Action step outputs so a workflow
// can branch on them (label the PR, fail only above a threshold, and so on).
// Keep the names in step with action.yml; CI asserts they match.
func appendStepOutputs(res scan.Result) error {
	counts := res.Counts()
	breaking := counts[rules.SeverityBreaking]
	body := fmt.Sprintf("breaking=%d\nwarning=%d\ninfo=%d\ntotal=%d\nhas-breaking=%t\n",
		breaking, counts[rules.SeverityWarning], counts[rules.SeverityInfo],
		len(res.Findings), breaking > 0)
	return appendEnvFile("GITHUB_OUTPUT", body)
}

func appendEnvFile(env, body string) error {
	path := os.Getenv(env)
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", env, err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		return fmt.Errorf("write %s: %w", env, err)
	}
	return nil
}
