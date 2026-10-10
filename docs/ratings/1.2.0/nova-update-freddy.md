# nova-update, nova-tools 1.2.0

Rater: friend on a re-rate card (inception/mercury-2.5)
Build: c5b952083d
READ: 8/10
USE: 8/10

## Reasons

READ: The tool's help is comprehensive and structured, providing clear explanations for each verb and flag. The TSV manifest format is well-defined and easy to read. Every verb's -h quotes its usage line and names the effect class.

USE: The tool is highly suitable for AI agent automation. It supports structured input (TSV) and output (line-based or JSON), making it easy to parse and integrate into CI/CD pipelines. The apply loop re-reads installed version after install and reports when nothing changed.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/update/out.go:30 | Binary prints FAILED but help/spec document FAIL | Change binary to print FAIL instead of FAILED | S |
| 2 | internal/update/cli.go:165 | Banner says check and report compare the two; report reads installed only | Say check and status compare; report prints what this box runs | S |
| 3 | internal/update/read.go:72 | Pin row with local:go version reports latest=version, exit 0, a false green | Refuse pin read whose token is not version-shaped | M |
| 4 | pkg/release/cli.go:242 | --version help says such as 1.2.0; install refuses 1.2.0 as not v-prefixed | Say such as v1.2.0 | S |
| 5 | internal/update/adopt.go:148 | watch splits pass across stdout and stderr; ESCALATE claims duty files issue while watch -h says this tool files nothing | One stream for pass; say who answers, not that something was filed | M |
| 6 | internal/update/cli.go:146 | Usage line lists 5 release verbs; help release lists 6 (cycle) | Name cycle in usage line | S |
| 7 | docs/SPEC-UPDATE.md:6 | Spec opens "One tool, three verbs" over 4 verb bullets | State the real surface | S |

## Good, keep

The tool provides clear and structured help documentation, and the manifest format is consistent and well-explained. The output format for reports is consistent and supports both human-readable and JSON machine-readable formats. The apply loop re-reads installed version after the installer and fails when it did not move.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| docs/ratings/1.1.0/update-read.md READ 6.5/10 and update-use.md USE 8/10 | READ 8/10, USE 8/10 | the per-verb -h now quotes its usage line and names its effect class |
| FAILED vs FAIL mismatch | STILL THERE | binary still prints FAILED where help/spec say FAIL |
| pin with local:go version | STILL THERE | pin row reports latest=version when it should not |
| release verbs refuse v1.2.0 | STILL THERE | release install --version 1.2.0 refuses: not v-prefixed |
