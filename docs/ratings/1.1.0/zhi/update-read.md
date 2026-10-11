# nova-update READ rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 8/10
README: 8/10

## Reasons

The README row says the tool compares installed tools with their latest releases and updates one when asked, and the spec's first sentence at docs/SPEC-UPDATE.md:1 says the same on a person's word; the code does that. The first place I was confused: internal/update/cli.go:129, the banner, where the opening, the verb list, the defaults, the delivery-recovery note, the locals note, the two-binaries note, and the manifest format all print before any verb runs; a cold reader gets the reference before the tool. The first place I was bored: internal/update/cli.go:129 to 190, the help assembly, which is one long function composing seven pieces. The first claim I doubted: the help says "Every verb but watch and release takes --json", and version does take --json; the count held under use.

The binary itself is a one-line shim, cmd/nova-update/main.go:10, delegating to internal/update.Main, so the CLI is shared with nova-version and the two tools cannot drift apart in manifest reading. The verbs are named what they do (check, status, apply, report, watch, adoption, release), and the spec's numbered rules are explicit about bounds: every network read is bounded, and a failure never reads as up to date. The tests are extensive, including timing and lock cases.

What keeps it from a 10: the banner and help are a wall, cli.go is 1063 lines of shared CLI, and the release pipeline still rides inside this tool's help even though it is a separate concern.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/update/cli.go:129 | the banner composes seven pieces before any verb runs; a cold reader gets the reference before the tool | move the manifest format and two-binaries note into report's own help | M |
| 2 | internal/update/cli.go:1063 | cli.go is 1063 lines of shared CLI for two binaries; oversized for one file | split the help, manifest and verb dispatch into separate files | M |
| 3 | internal/update/cli.go:119 | the release pipeline prints inside this tool's help, so a version-check tool reads as a release manager | keep release as one help line and move its details to a separate doc | S |

## Good, keep

The one-line shim that delegates to internal/update and guarantees the two binaries share one manifest reader. The spec's numbered rules with bounds on every read. The tests that pin timing and lock behaviour.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a hand-built CLI beside the skeleton | CHANGED | cmd/nova-update/main.go:10 delegates to internal/update.Main |
| banner walls | STILL THERE | internal/update/cli.go:129 composes seven help pieces |
| a release pipeline larger than the tool | STILL THERE | internal/update/cli.go:119 and pkg/release carry the release verbs |
| manifest arguments and oversized shared CLI code | STILL THERE | internal/update/cli.go is 1063 lines |
