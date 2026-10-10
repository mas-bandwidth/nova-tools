# nova-update READ rating, nova-tools 1.1.0

Rater: deepseek-v4.1-flash
Build: bd7949b97aec
Score: 6.5/10
README: 7/10

## Reasons
The README line for this tool is README.md:42: "Inspect versions and apply one chosen update." The tool's own banner opens with the same claim (internal/update/cli.go:134), so the two agree, and that agreement is the best thing here. The first place I was confused is README.md:50: at a 1.1.0 head the reader is told "These are the Nova Tools 1.0.0 commands" and sent to that release. The first place I was bored is README.md:21: the catalogue is one HTML table of nineteen rows, so finding the row for the tool in hand is a scan, not a lookup. The first place I doubted a claim is README.md:42 again: the row's job is to compare installed tools with their latest releases and update one, yet its first command is `nova-update report`, which reads no latest and installs nothing.

The tool itself is disciplined: every refusal names a remedy, reads are bounded, UNKNOWN is never OK, and the manifest is a clear six-column file. But the head is still the tool the earlier ratings met. First, nova-update hand-builds its dispatch, flags and output (internal/update/cli.go:288) while nova-version, the other binary over the same reader, is an pkg/tool.Tool (internal/update/versiontool.go:14): a reader meets two shapes for one shared reader. Second, more than half the spec is the release pipeline (docs/SPEC-UPDATE.md:441), and the code carries a second large package for it; updating one's own tools is a few verbs and this is not. Third, the spec's own first sentence, "One tool, three verbs" (docs/SPEC-UPDATE.md:6), is false: the binary answers nine verbs (internal/update/cli.go:71). A 10/10 would state the real surface once, put the release pipeline behind its own name, put both binaries on the one skeleton, and cut the repeated first-run prose.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/update/cli.go:288 | `Run` hand-parses flags with `flag.NewFlagSet`, dispatches by hand and renders through this package's own `emit`, while nova-version, the other binary over the same reader, is built on pkg/tool (internal/update/versiontool.go:14); the standard says a tool starts from the skeleton | Move nova-update's verbs onto pkg/tool, or say in one place why the shared reader keeps its own dispatch, so both binaries have one shape | L |
| 2 | docs/SPEC-UPDATE.md:441 | The release section runs from here to the end and rules 27 and 28 add watch and adoption, so a reader who wants to check a version meets a build-publish-adopt pipeline for this repository's own releases in the same binary | Put the release pipeline under its own name and spec, leaving this page about checking and updating a caller's tools | L |
| 3 | docs/SPEC-UPDATE.md:6 | The first sentence says "One tool, three verbs", then the page documents check, status, apply and report, then watch, adoption and a six-verb release; the binary answers nine verbs (internal/update/cli.go:71) | State the real surface in the opening paragraph | M |
| 4 | docs/CLI.md:1544 | The first-run paragraph stands three times, near-identical: docs/CLI.md:1544 for nova-update, docs/CLI.md:1720 for nova-version, and docs/SPEC-UPDATE.md:703; six sentences on UNKNOWN, the six-column manifest and the version ladder are maintained in three places | Say it once and link the other sites to it | M |
| 5 | internal/update/cli.go:153 | `help` prints the opening, the usage, a defaults paragraph, a delivery note, a locals paragraph, a paragraph on the two binaries, a six-line manifest lecture, an exit-code paragraph and an example; the three answers are already in the opening | Keep the opening, the usage and one next command, and move the manifest, locals and exit table to the verb help the reader asks for | M |
| 6 | docs/SPEC-UPDATE.md:459 | The release gate refuses until a named person's read is recorded (the name is repeated at docs/CLI.md:1601) and a ticket number stands in the user-facing text (docs/CLI.md:1554 and docs/CLI.md:1730); a stranger can resolve neither | Name the role, not the person, and describe the behaviour instead of the ticket | S |
| 7 | README.md:50 | At a 1.1.0 head the README says "These are the Nova Tools 1.0.0 commands" and sends the reader to that release, while the tree carries 1.1.0 release notes and 1.1.0 ratings | Make the version line current or version-neutral | S |
| 8 | docs/SPEC-UPDATE.md:23 | "this estate's first tool that reads somebody else's server on a clock" and "The estate runs it nightly" describe an estate and a schedule the tool does not carry; the tool brings no timer and the reader has no estate | Say what the tool does now without the estate or the nightly claim | S |

## Good, keep
Every refusal names what the input wants and the command to run next (internal/update/cli.go:65), and one run reports every missing flag at once (internal/update/cli.go:411).
A dead, empty or malformed source is UNKNOWN and never OK (internal/update/latest.go:99), and the example manifest runs from the binary alone (internal/update/example.go:18).
The tests teach the contract: one named test per rule, with a fixture for each latest source (docs/SPEC-UPDATE.md:721).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a hand-built CLI beside the skeleton | STILL THERE | internal/update/cli.go:288 builds its own flags, dispatch and output; internal/update/versiontool.go:14 is the skeleton |
| banner walls | CHANGED | the opening answers three questions at internal/update/cli.go:134, though help stays long at internal/update/cli.go:153 |
| a release pipeline larger than the tool | STILL THERE | docs/SPEC-UPDATE.md:441 and the pkg/release package |
| manifest arguments and oversized shared CLI code | STILL THERE | docs/SPEC-UPDATE.md:2 and internal/update/cli.go:288 (a 1064-line CLI) |
| README rating then: 6.5 to 7 from one rater, 8.4 from another | CHANGED | README.md:42 |
