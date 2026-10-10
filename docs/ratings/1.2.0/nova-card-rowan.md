# nova-card READ and USE rating, nova-tools 1.2.0

Rater: a cold rater, mercurys-2.5 under opencode
Build: 17ec8d256a04943b921987ebd9c19458917b19e9
READ: 7/10
USE: 6.5/10

nova-card is pre-alpha. The README says "Turn a ledger, a findings file or a tool's help into briefs the sprint admits." The verb set is small: generate, lint, template, version, help. The first-run transcript in the README is runnable from a checkout. The spec is docs/SPEC-CARD-CONTRACT.md.

## Reasons

READ. The banner is short and answers what it does. The README first run shows a complete example that works against fixtures in the tree. lint and generate verbs are documented. What holds the score: the README has no exit table, and the generate verb's flags are long with no short flags or examples of typical inputs. The lint verb's output is not documented.

USE. Running generate against the sample findings.tsv produces cards. Lint validates them. The tool works end to end on the provided fixtures. What holds the score: generate --help lists 15 flags with no grouping, and there is no dry-run mode for generate to preview the briefs before writing them.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-card/main.go:help | generate lists 15 flags in one long list without grouping by purpose | group flags as input, output, repo metadata, and options | S |
| 2 | cmd/nova-card/main.go:help | lint --help does not show what a lint error output looks like | add an example lint error line in lint help | S |
| 3 | cmd/nova-card/main.go:generate | generate has no --dry-run flag | add --dry-run that writes briefs to stdout instead of files | M |
| 4 | docs/CLI.md#nova-card | the CLI section has no exit table for generate or lint | add exit codes section matching the code | S |
| 5 | cmd/nova-card/main.go:generate | generate --file path has no validation that the file exists before processing | validate file existence early and refuse with a clear message | S |

## Good, keep

- First run example is complete and runnable from the checkout.
- Lint provides validation feedback.
- Spec is present and describes the card contract.

## Compared with earlier ratings

This tool was not rated in 1.1.0 as it did not exist. The first rating shows a functional tool with pre-alpha status and room for usability improvements.
