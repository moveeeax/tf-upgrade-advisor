# tf-upgrade-advisor

Static analysis that intersects a Terraform provider's breaking-change list with your actual HCL, so a major upgrade becomes a short checklist instead of a 400-line changelog.

The AWS provider v6 upgrade guide is roughly 90 sections long. Almost none of them apply to you. `tf-upgrade-advisor` reads your configuration, matches it against a hand-transcribed rule corpus, and prints the handful of items that do — each with the file and line to open, the guide section it came from, and a suggested diff.

No state file. No credentials. No network. No model anywhere in the analysis path — the same inputs always produce the same report, which is the point.

A second command, `outdated`, answers the neighbouring question: which module and provider pins across the whole repository have fallen behind, been deprecated or been yanked on the Terraform Registry. It needs network access to the registry and can keep one grouped GitHub issue up to date instead of a stream of bump PRs. See [Outdated pins](#outdated-pins).

```console
$ tf-upgrade-advisor scan --provider aws --from 5 --to 6 --format text ./infra
aws 5 -> 6: 8 breaking, 5 warning, 1 info

main.tf:16: breaking: provider.aws.endpoints.opsworks [aws6-provider-endpoints-opsworks]
  provider: `endpoints.opsworks` removed
main.tf:21: breaking: aws_eip.nat.vpc [aws6-eip-vpc]
  `aws_eip`: `vpc` removed
main.tf:25: breaking: aws_flow_log.vpc.log_group_name [aws6-flow-log-log-group-name]
  `aws_flow_log`: `log_group_name` removed
...
```

## Install

```console
go install github.com/moveeeax/tf-upgrade-advisor@latest
```

Or build from a clone:

```console
git clone https://github.com/moveeeax/tf-upgrade-advisor
cd tf-upgrade-advisor
make build
```

Go 1.26 or newer. The rule corpus is embedded in the binary.

## Usage

This example runs against the fixture checked into this repository, so you can copy it verbatim after cloning:

```console
$ go run . scan --provider aws --from 5 --to 6 --format text testdata/aws5
aws 5 -> 6: 8 breaking, 5 warning, 1 info

main.tf:21: breaking: aws_eip.nat.vpc [aws6-eip-vpc]
  `aws_eip`: `vpc` removed
modules/network/main.tf:3: breaking: aws_ssm_association.patch.instance_id [aws6-ssm-association-instance-id]
  `aws_ssm_association`: `instance_id` removed
...
$ echo $?
1
```

Drop `--format text` for the default Markdown checklist — that is what you paste into a ticket:

````markdown
### Breaking (8)

- [ ] **2. `aws_eip`: `vpc` removed** — `aws_eip.nat` → `vpc`
  - `main.tf:21` · removed · rule `aws6-eip-vpc` · [guide](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/guides/version-6-upgrade#resource-aws_eip)
  - Deprecated since the EC2-Classic retirement. Use `domain` instead.

    ```diff
    -  vpc    = true
    +  domain = "vpc"
    ```
````

The same run against an already-upgraded configuration exits 0:

```console
$ go run . scan --provider aws --from 5 --to 6 testdata/clean
## Provider upgrade: aws 5.x → 6.x

No known breaking changes apply. Scanned 4 blocks across 1 directory against 119 rules.
$ echo $?
0
```

### Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `--provider` | `aws` | Provider to check. |
| `--from`, `--to` | — | Major versions, e.g. `--from 5 --to 6`. Required. |
| `--format` | `markdown` | `markdown`, `text` or `json`. |
| `--output` | — | Also write the report to a file. |
| `--fail-on` | `breaking` | Lowest severity that exits 1: `breaking`, `warning`, `info` or `never`. |

Exit codes: `0` nothing at or above the threshold, `1` findings, `2` usage or parse error.

### What is covered

```console
$ tf-upgrade-advisor rules
aws 4 -> 5  60 rules (51 breaking, 9 warning, 0 info)
  kinds: default-changed=2 deprecated=6 removed=39 renamed=10 required=2 state-move=1
  guide: https://registry.terraform.io/providers/hashicorp/aws/latest/docs/guides/version-5-upgrade
aws 5 -> 6  119 rules (54 breaking, 57 warning, 8 info)
  kinds: default-changed=4 deprecated=17 removed=50 renamed=13 required=3 retyped=27 state-move=5
  guide: https://registry.terraform.io/providers/hashicorp/aws/latest/docs/guides/version-6-upgrade
```

Every rule carries the anchor of the guide section it was transcribed from, and the report links straight to it. If you disagree with a finding you can check it at source in one click.

## In CI

```yaml
- uses: moveeeax/tf-upgrade-advisor@v0.1.0
  with:
    provider: aws
    from: 5
    to: 6
    path: ./infra
    fail-on: breaking
```

The checklist is appended to the job summary page. The step also sets `breaking`, `warning`, `info`, `total`, `has-breaking` and `report-file` as outputs. See [`examples/workflow.yml`](examples/workflow.yml).

## Outdated pins

```console
$ tf-upgrade-advisor outdated --max-behind minor --format text .
3 of 8 pinned versions need an upgrade (max behind: minor)

stacks/data/main.tf:25: ignored: module terraform-aws-modules/s3-bucket/aws 4.1.0 (latest 5.17.0) resolves to 4.1.0, major behind
stacks/network/main.tf:16: upgrade: module terraform-aws-modules/vpc/aws 5.8.1 (latest 6.7.3) resolves to 5.8.1, major behind
stacks/network/main.tf:3: upgrade: provider hashicorp/aws ~> 5.40 (latest 6.73.0) resolves to 5.100.0, major behind
stacks/network/main.tf:29: not checked: module git::https://example.com/modules/legacy.git?ref=v1.0.0: not a registry source
...
```

`outdated` walks every directory under the target that holds `*.tf` files (hidden directories such as `.terraform` are skipped), so several stacks side by side are inventoried in one run. For each registry `module` with a `version` argument and each `required_providers` entry it asks the registry for the published versions, works out what the constraint resolves to today (what `terraform init -upgrade` would pick), and compares that with the newest stable release.

A pin needs an upgrade when any of these holds:

1. It resolves to a version further behind latest than `--max-behind` allows (`none`, `patch`, `minor` or `major`; default `minor`).
2. The registry marks the resolved module version deprecated, or attaches a warning to the provider.
3. The constraint matches no published version: the pinned release was yanked or never existed.

Distance is the first release segment that differs: `5.8.1` against `6.0.0` is a major behind. Prereleases never count as latest unless a project has published nothing else. A module without a `version` argument resolves to latest and is reported as unpinned.

Markdown output groups locations by source, so one module pinned in forty stacks is one checkbox. Local modules belong to the repository and are not listed; git, HTTP and S3 sources, private registry hosts and sources the registry does not know are listed under "not checked" with the reason. A registry error other than 404 stops the run with exit code 2: an outage is not a clean report.

### Ignore rules

Versions held back on purpose go in `.tf-upgrade-advisor-ignore.yaml` at the scanned root (or the file passed with `--ignore-file`). Every rule needs a reason; a rule without one, or with an unknown field, is an error.

```yaml
ignore:
  - source: terraform-aws-modules/s3-bucket/aws   # module: namespace/name/system
    constraint: "4.1.0"                           # optional: only while the pin reads exactly this
    reason: 5.x drops the bucket ACL inputs the data stack still sets.
  - source: hashicorp/aws                         # provider: namespace/type
    reason: Blocked on the 6.x upgrade checklist, see INFRA-412.
```

Ignored pins stay in the report with their reason. Rules that matched nothing are listed so they can be deleted.

### One grouped issue

```console
$ GITHUB_TOKEN=... tf-upgrade-advisor outdated --open-issue --label tech-debt --github-repo acme/infra .
```

With `--open-issue` the Markdown report becomes the body of a single issue. The issue carries a hidden marker; later runs find the open issue with that marker and edit its body in place, leaving title and labels to whoever triages it. When nothing needs an upgrade any more the issue is updated with the all-clear report and closed. `--label` and `--issue-title` apply when the issue is first opened. The token comes from `GITHUB_TOKEN` or `GH_TOKEN` and needs `issues: write`; `GITHUB_API_URL` is honoured for GitHub Enterprise Server. See [`examples/outdated.yml`](examples/outdated.yml) for a weekly schedule.

| Flag | Default | Meaning |
|------|---------|---------|
| `--max-behind` | `minor` | Largest accepted distance behind latest: `none`, `patch`, `minor` or `major`. |
| `--format` | `markdown` | `markdown`, `text` or `json`. |
| `--output` | — | Also write the report to a file. |
| `--ignore-file` | `.tf-upgrade-advisor-ignore.yaml` in the target | Ignore rules. An explicitly named file must exist. |
| `--registry` | `https://registry.terraform.io` | Registry base URL, for a mirror that serves the same API. |
| `--fail` | `true` | Exit 1 when anything needs an upgrade. `--fail=false` for report-only runs. |
| `--open-issue` | `false` | Create or edit the grouped issue; close it when the list is empty. |
| `--label` | — | Label for a newly opened issue. Repeatable. |
| `--issue-title` | `Terraform module and provider upgrades` | Title for a newly opened issue. |
| `--github-repo` | `$GITHUB_REPOSITORY` | `owner/name` for `--open-issue`. |

Inside GitHub Actions the Markdown report is appended to the job summary and `needs-upgrade`, `issue-number` and `issue-url` are written to `$GITHUB_OUTPUT`.

Not covered: end-of-life dates (the registry publishes none, so "far behind" is the signal), private registry hosts, git tag sources and OpenTofu's registry.

## How it works

1. **Parse.** Every `*.tf` file in the target directory is parsed with `hclsyntax`. `resource`, `data` and `provider` blocks are recorded along with the flattened set of attribute paths actually set inside them — `logs.audit`, `block_device_mappings.ebs.encrypted`, and so on. A `dynamic` block is flattened to the block type it generates, so `dynamic "ebs_block_device"` matches the same rules as the static form.
2. **Follow local modules.** `module` blocks whose `source` is a relative path are walked too. Registry, git and other remote sources are listed in the report as not followed, rather than silently skipped.
3. **Match.** Each rule is a flat lookup on (block kind, block type) plus an optional list of attribute paths. `absent: true` inverts the sense, which is how "this argument is now required" and "this default changed" are expressed.
4. **Report.** Findings are grouped by severity, numbered continuously, and each one names a file and line that exists in your checkout.

### Adding a rule

Rules are data. A new entry in `internal/rules/corpus/aws-5-to-6.yaml` needs no code change:

```yaml
- id: aws6-eip-vpc
  kind: removed          # removed | renamed | retyped | default-changed | required | deprecated | state-move
  severity: breaking     # breaking | warning | info
  match:
    block_kind: resource
    type: aws_eip
    attributes: [vpc]
  title: "`aws_eip`: `vpc` removed"
  detail: Deprecated since the EC2-Classic retirement. Use `domain` instead.
  guide_anchor: "#resource-aws_eip"
  suggested_diff: |2
    -  vpc    = true
    +  domain = "vpc"
```

The corpus is validated at load time — unknown fields, duplicate IDs, bad severities and missing guide anchors all fail the test suite rather than shipping.

## Limits

Worth knowing before you rely on it:

- **AWS only**, majors 4 → 5 and 5 → 6. Other providers are not covered.
- **Arguments, not references.** The matcher looks at what your configuration *sets*. Changes that only affect what you *read* — an output attribute renamed, a single-nested attribute becoming a list of blocks — are reported at the block, with the reference to check spelled out in the detail. It cannot yet tell you which `locals` block reads `kibana_endpoint`.
- **No value inspection.** A rule fires on the presence of an argument, not on its value. "Uppercase `engine` is deprecated" is therefore an `info` item on every `engine`, not a precise hit.
- **No `--fix`.** Suggested diffs are printed, never applied. Rewriting HCL safely is a separate problem and this tool does not pretend to have solved it.
- **No `.tf.json`**, no Terragrunt, no CDKTF, no private registry modules.
- **The corpus is hand-maintained.** It reflects the published guides at the time it was written; a provider release does not update it automatically.

Terraform core upgrades are out of scope. `scan` is about provider breaking changes; module and provider version drift is what `outdated` reports.

## Development

```console
make test     # go test -race ./...
make lint     # gofmt -l . && go vet ./...
make demo     # runs the README example against testdata/aws5
```

## License

MIT. See [LICENSE](LICENSE).
