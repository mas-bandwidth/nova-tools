# nova-update READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 50c0c29cb5dc
Score: 6.5/10
README: 7.5/10

## Reasons
The README introduces nova-update clearly in its overview table (README.md:40), defining its role in comparing installed dependencies against latest releases and applying one chosen update upon request, supported by a workable example. The tool addresses a necessary operational requirement: evaluating versions and executing controlled, non-automatic updates across heterogeneous dependencies.

However, several design choices degrade its quality as software writing. The first place of confusion is cmd/nova-update/main.go:10 and internal/update/cli.go:71, where nine disparate verbs—including watch (adoption verification), adoption (ledger tracking), and release (a sprawling multi-platform build, SSH distribution, and deployment subsystem in internal/release)—are bundled into what was presented as a focused version manifest evaluator. The second friction is boredom in docs/CLI.md:1492-1616 and internal/update/cli.go:152-196, where pages of multi-platform build matrices, SSH staging topologies, SHA256SUMS retention policies, and a dense 60-line help banner overwhelm the core contract. The third is a doubted claim at docs/SPEC-UPDATE.md:6 ("One tool, three verbs", which immediately enumerates four subcommands, while the implementation registers nine verbs in internal/update/cli.go:71), alongside docs/SPEC-UPDATE.md:28 asserting "the tool has no clock of its own — no daemon, no timer, no --watch", directly contradicted by nova-update watch in internal/update/cli.go:117 and internal/update/adopt.go:239. Furthermore, whereas nova-version adopted the internal/tool skeleton (versiontool.go:14), nova-update retains an oversized 1,000-line hand-rolled CLI dispatcher in internal/update/cli.go:284 with custom flag parsing (interspersed), custom error translations (flagProblem), and non-standard refusal tokens (ADOPT in adopt.go:257). Finally, watch and release refuse structured --json output, violating the repository's universal --json contract (AGENTS.md:27).

A 10/10 would require separating the multi-platform release and adoption-watching subsystems into dedicated tooling outside update, migrating nova-update fully onto internal/tool to eliminate hand-rolled CLI dispatch and flag parsing, trimming the help banner to a concise onboarding structure, reconciling SPEC-UPDATE with actual shipped verbs, and supporting --json across every subcommand without exception.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/update/cli.go:284 | nova-update retains a 1000-line hand-rolled CLI dispatcher with custom flag parsing and banner logic instead of using internal/tool | Migrate nova-update to internal/tool.Tool as done for nova-version in versiontool.go | L |
| 2 | internal/update/cli.go:317 | Embedding a multi-platform compiler, SSH fan-out, and release deployment pipeline into nova-update bloats the tool and dilutes its focus | Move release and watch workflows into dedicated binaries or standalone packages | L |
| 3 | internal/update/cli.go:152 | nova-update help prints a massive 60-line banner with ASCII manifest tutorials, two-binary comparisons, and notes | Trim the banner to the standard onboarding layout and move tutorial details to docs | M |
| 4 | docs/SPEC-UPDATE.md:6 | Spec claims "One tool, three verbs" then lists four verbs, while the implementation provides nine verbs | Reconcile spec verb count with actual shipped subcommands | S |
| 5 | internal/update/adopt.go:257 | watch emits refusal token ADOPT REFUSED to stderr and lacks --json support, breaking uniform CLI conventions | Use UPDATE REFUSED, write to standard error sink via skeleton, and implement --json | M |
| 6 | internal/update/read.go:74 | Parsing kind pin with token splits causes commands like local:go version to extract version as version rather than version number | Use versionKey rather than naive token indexing for pin kinds | S |

## Good, keep
Clean manifest validation in internal/update/manifest.go that aggregates and reports all formatting errors in a single turn.
Conservative update execution with explicit --dry-run planning that verifies target versions before and after process execution.
Explicit handling of model digests in internal/update/read.go acknowledging that neural weights lack semantic versioning.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a hand-built CLI beside the skeleton | STILL THERE | internal/update/cli.go:284 |
| banner walls | STILL THERE | internal/update/cli.go:152 |
| a release pipeline larger than the tool | STILL THERE | internal/release/cli.go:1 |
| manifest arguments and oversized shared CLI code | STILL THERE | internal/update/cli.go:1 |
| README then: 6.5 to 7 from one rater, 8.4 from another | CHANGED | README.md:40 |
