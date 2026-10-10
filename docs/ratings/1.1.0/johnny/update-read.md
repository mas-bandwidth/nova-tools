# nova-update READ rating, nova-tools 1.1.0

Rater: Grok
Build: 264f9c0135c7
Score: 6.5/10
README: 6.5/10

## Reasons

README line: compare installed tools with their latest releases, and update one when asked (README.md:40).

Before any code, the first confusion is README.md:40, where the trial cell says the report compares Go with itself and applies nothing, which is not a check against latest releases. The first boredom is README.md:24, a long table of near-identical setup cells before this tool's row. The first doubt is README.md:48, which calls these the 1.0.0 commands. The install section that page points to first still names that tag (docs/USAGE.md:92), and the decision list that follows never names this tool (docs/USAGE.md:140).

The first-run path is what earns the score. docs/CLI.md:1447 and docs/SPEC-UPDATE.md:697 are two commands, the binary alone, a one-tool manifest, and a report that reads installed identities and installs nothing. example leaves a different file alone and names the next command (internal/update/example.go:36). One refusal lists every missing flag and a bad count together (internal/update/cli.go:434). Help's verb block is the spec's verb block, and a test compares the two (internal/update/cli.go:103, internal/update/rules_test.go:250). A source that does not answer is UNKNOWN and the run exits 1 (docs/SPEC-UPDATE.md:141). apply takes one exact name, and --dry-run prints the plan and writes nothing (docs/SPEC-UPDATE.md:163, docs/SPEC-UPDATE.md:184). Comments in the dispatch often say why, in the present tense.

A 10 needs one true sentence in the banner, the spec, and the report path. The banner says check and report compare the two sides (internal/update/cli.go:137). The spec's first-run paragraph says the report does no latest lookup (docs/SPEC-UPDATE.md:706). The report value prints only the installed read (internal/update/report.go:46). On that same path a latest field that starts with local: is still executed and then dropped (internal/update/cli.go:555). The spec opens with three verbs, the next bullets name four, and the verb block names example, check, status, apply, report, watch, adoption and release (docs/SPEC-UPDATE.md:5, docs/SPEC-UPDATE.md:408). The release manual begins inside this tool's command section (docs/CLI.md:1490), and the release package's non-test Go is longer than this package's. Dispatch is a hand-built flag set and a hand-built flag interleaver (internal/update/cli.go:347, internal/update/cli.go:232) beside the shared skeleton (pkg/tool/tool.go:39), and the same file holds the other binary's moved verb (internal/update/cli.go:715).

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/update/cli.go:137 | The how-it-works lines say check and report compare installed with latest. The report value prints only the installed read, while a local: latest command on that path still runs and is dropped. An AI that trusts the banner waits for a comparison the report never prints. | Say that report reads installed identities only, and do not start a latest command on the report path. | M |
| 2 | docs/SPEC-UPDATE.md:5 | The spec says one tool and three verbs. The verb block is eight surfaces, and the command reference then becomes a release manual (docs/CLI.md:1490) for a package larger than the updater. The shape is not findable in a minute. | Open with the verbs that exist, and keep the release manual behind help release. | L |
| 3 | internal/update/cli.go:232 | Flags are parsed with a private flag set and a hand-rolled interleaver, in a 1063-line file shared with the other binary, including that binary's moved verb (internal/update/cli.go:715). The skeleton already owns banner, help, refusal and dry-run (pkg/tool/tool.go:39). | Dispatch through the skeleton and move the other binary's verbs out of this file. | L |
| 4 | docs/USAGE.md:140 | The decision list the README points to first never offers this tool, and the install block above it is still the 1.0.0 tag (docs/USAGE.md:92). | Add one decision entry whose trial is the two first-run commands, and name the current tag. | S |

## Good, keep

example refuses to overwrite a file that is not the example and names the next command (internal/update/example.go:36).
One refusal names every missing flag and the bad count in the same line (internal/update/cli.go:434).
The verb block in help is the spec's verb block, held by a test (internal/update/rules_test.go:250), and a dead source stays UNKNOWN rather than current (docs/SPEC-UPDATE.md:141).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| hand-built CLI beside the skeleton | STILL THERE | internal/update/cli.go:347 builds its own flag set and internal/update/cli.go:232 interleaves flags, while pkg/tool/tool.go:39 is the skeleton |
| banner walls | STILL THERE | internal/update/cli.go:159 adds a recovery paragraph, internal/update/cli.go:175 a two-binary lecture, and internal/update/cli.go:186 six manifest lines after the short how-it-works |
| release pipeline larger than the tool | STILL THERE | docs/CLI.md:1490 opens the release manual in this tool's section, and the release package's non-test Go is longer than internal/update's |
| manifest arguments | STILL THERE | docs/SPEC-UPDATE.md:48 still refuses an argument that holds a blank and tells the caller to name a script, and docs/CLI.md:1485 repeats that |
| oversized shared CLI code | STILL THERE | internal/update/cli.go:171 says both binaries share this file, which also holds the other binary's moved verb at internal/update/cli.go:715 |
| README scored 6.5 to 7, and 8.4 | STILL THERE | README.md:40 still says the report compares Go with itself, while docs/SPEC-UPDATE.md:706 says the report does no latest lookup |
