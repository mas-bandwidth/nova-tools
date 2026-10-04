# nova-ci READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 7/10
README: 7/10

## Reasons
The tool provides useful test timing analysis and scaffolding helpers, but suffers from an identity split between a generic test timing inspector and a private repository development harness.

Reading README.md top to bottom:
- Confused: README.md:38 lists nova-ci under "Check test runs and their cost", but the description combines test-event timing with three verbs that work only inside this specific repository checkout and a receipt verb that writes to Redis.
- Bored: README.md:24 opens a seventeen-row table where extensive setup paragraphs and shell snippets are packed into table cells, slowing down comprehension.
- Doubted a claim: README.md:38 suggests nova-ci checks test runs, but slowtests evaluates only elapsed durations without checking whether the tests actually passed.

To reach a 10/10, the tool would need:
1. Separation between generic test inspection (slowtests, functional) and repository development utilities.
2. A unified output line format across all verbs rather than five divergent dialects.
3. Addition of the required First run heading in docs/CLI.md.
4. Relocation or removal of the ignored script in cmd/nova-ci/timing.go.
5. Reconciliation of docs/SPEC-CI.md so it specifies actual tool verbs rather than internal test names.
6. A note in the banner pipeline example warning that piping go test masks test failure exit codes.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/CLI.md:1828 | Section nova-ci omits the mandatory First run heading specified in onboarding rules | Insert the First run subsection heading before introductory walkthrough | S |
| 2 | docs/SPEC-CI.md:13 | Specification describes test rules as CLI verbs that nova-ci does not implement | Clarify in specification that entries represent class tests not CLI verbs | M |
| 3 | cmd/nova-ci/timing.go:1 | Leftover build-ignored script resides in command package directory | Move script out of command tree into a scripts directory | S |
| 4 | cmd/nova-ci/main.go:49 | Banner pipeline example omits caveat that piping go test masks test exit status | Add guidance in banner note that test pass or fail status must be checked separately | S |
| 5 | cmd/nova-ci/main.go:401 | Verbs emit multiple disconnected output dialects rather than standard status lines | Unify verb output formats under standard status line grammar | L |
| 6 | README.md:38 | Table entry mixes general test timing with repository checkout prerequisites | Distinguish generic test timing capabilities from checkout-bound tasks | S |

## Good, keep
Clean parsing and rejection of non-finite float values like NaN and infinity in flag handling.
Clear data modeling in internal/ci/slowtests separating raw test events from evaluated package reports.
Structured refusal messages that cite every detected flaw and specify the exact next help command.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| this repository's own CI in five output dialects | STILL THERE | cmd/nova-ci/main.go:401 |
| a leftover script | STILL THERE | cmd/nova-ci/timing.go:1 |
| a spec of verbs the tool does not have | STILL THERE | docs/SPEC-CI.md:13 |
| the banner's pipeline example needs the test-exit caveat where it is used | STILL THERE | cmd/nova-ci/main.go:49 |
