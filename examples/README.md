# Examples

| File | What it shows |
|------|---------------|
| [`workflow.yml`](workflow.yml) | Gating a pull request on the AWS 5 → 6 checklist with the composite action. |
| [`../testdata/aws5`](../testdata/aws5) | A small 5.x configuration with a local module, used by the tests and by the README example. |
| [`../testdata/clean`](../testdata/clean) | The same shapes after the upgrade, so you can see a clean report. |

Run the scanner against either fixture from the repository root:

```console
$ go run . scan --provider aws --from 5 --to 6 --format text testdata/aws5
$ go run . scan --provider aws --from 5 --to 6 testdata/clean
```
