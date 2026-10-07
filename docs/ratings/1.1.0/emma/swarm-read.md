# nova-swarm READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 264f9c0135c7
Score: 7/10
README: 8/10

## Reasons
The tool enforces sound architectural boundaries for running AI subtasks: OS-level sandbox containment, external machinery deadlines, token budgets, slot isolation, and publishing results through atomic file renames. Task templates and card linting provide clear guidance for constructing valid child briefs.

A score of 10 would require decomposing the 1,197-line nativeRun function, adopting the internal/tool skeleton for consistent dispatch and --json support, unifying flag refusal output grammar with actionable remedies, pruning pervasive historical issue numbers from source comments, and removing extreme line-length narrative bloat from the specification.

The first place of confusion was docs/CLI.md:741, where lint mixes markdown task card validation with coordinator launcher bash 3.2 script syntax checks.
The first place of boredom was docs/SPEC-SWARM.md:76, where a 16,783-character paragraph recounts specific benchmark incidents, CPU sampling details, and hardware frequencies.
The first place of doubting a claim was cmd/nova-swarm/main.go:166, which asserts that unusable invocations follow the standard refusal grammar with a runnable remedy, while flag validation at line 337 emits ad-hoc messages omitting both REFUSED and remedy text.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-swarm/native.go:262 | nativeRun is a monolithic 1197-line function combining staging execution proxying idle checks and cleanup | decompose into dedicated lifecycle stage run and proxy packages | L |
| 2 | cmd/nova-swarm/main.go:337 | flags.refused formats required-input errors without REFUSED token or remediation command | align flag refusal output to standard REFUSED grammar with remedy hint | M |
| 3 | cmd/nova-swarm/main.go:27 | cmd/nova-swarm bypasses internal/tool skeleton hand-rolling usage help flag parsing and dispatch | adopt internal/tool skeleton to provide standard json dry-run and help infrastructure | L |
| 4 | docs/SPEC-SWARM.md:76 | spec includes a 16783-character single-line paragraph mixing load test history into functional requirements | break narrative into structured subsections and move run history to test docs | M |
| 5 | cmd/nova-swarm/native.go:549 | legacy --auth option and auth copy handling are maintained alongside modern worker secret definitions | deprecate and remove legacy auth copying in favor of worker secrets | S |
| 6 | cmd/nova-swarm/main.go:653 | non-test source files contain 117 issue and ticket references instead of self-contained rationale | rewrite comments to state current requirements directly without historical tickets | M |
| 7 | docs/CLI.md:741 | lint verb combines markdown task card validation with launcher bash 3.2 syntax linting | separate fleet script linting into dedicated check or distinct subverb | S |

## Good, keep
OS-level sandboxing with external deadline and token budgeting ensures runaway child processes cannot compromise host resources.
Task templates and mechanical card linting establish unambiguous, verifiable constraints before tasks execute.
Verification checks output contracts and failure signatures objectively without human intervention.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1,195-line function | STILL THERE | cmd/nova-swarm/native.go:262 nativeRun spans 1197 lines |
| 142 ticket numbers in non-test code | CHANGED | cmd/nova-swarm/main.go:653 and other files count 117 references |
| a 1,900-character usage line | CHANGED | docs/SPEC-SWARM.md:56 is 1762 chars and line 76 is 16783 chars |
| legacy paths still named | STILL THERE | cmd/nova-swarm/main.go:96 and cmd/nova-swarm/native.go:549 |
| an intimidating, historically narrated interface | STILL THERE | docs/SPEC-SWARM.md:9 and docs/SPEC-SWARM.md:76 |
| the README then: 6.5 to 7 from one rater, 8.4 from another | CHANGED | README.md:29 presents clear summary and first command |
