# nova-ci READ rating, nova-tools 1.1.0

Rater: gpt-5.6-sol
Build: 2c02b2aa2042
Score: 7.5/10
README: 7/10

## Reasons
The README gives a direct one-line purpose and a runnable first command at README.md:38. My first confusion is the jump from the release-labelled commands to a source checkout requirement at README.md:48, because it is not immediately clear which version the current checkout documents. I first get bored in the long all-tools table at README.md:21 before reaching this tool near its end. I first doubt a claim at README.md:38, where “this repository's own CI steps” is broad enough that I cannot tell which steps are included until reading the command reference.

The command is unusually explicit about inputs, effects, exit codes, and safe trials. Its dispatch is immediate at cmd/nova-ci/main.go:232, each substantial behavior moves into a focused internal package, and the tests describe contracts in concrete cases. Comments usually explain the constraint behind the implementation, especially the isolation and selection rules in cmd/nova-ci/local.go:1. The shared refusal grammar and output value keep it recognizably in the same family as the other commands.

The largest cost is a normative contradiction: docs/SPEC-CI.md:209 and docs/SPEC-CI.md:220 call an over-budget result a refusal, while docs/SPEC-CI.md:229 says that result exits 0 unless enforcement is requested. The implementation follows the latter at internal/ci/slowtests/slowtests.go:599. A cold reader has to decide which contract sentence to trust. The entry file is also heavy: cmd/nova-ci/main.go:41 embeds a banner longer than one hundred lines, then carries dispatch, slow-test parsing and rendering, and functional selection through line 572. The help is complete, but its two separate `slowtests` usage blocks at cmd/nova-ci/main.go:54 and cmd/nova-ci/main.go:75 make the flag combinations slower to understand than one consolidated synopsis.

A 10 would align the normative wording with the implemented measurement behavior, split the entry file by verb, and consolidate the slow-test help without losing its unusually good detail.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-CI.md:209 | The contract says over-budget packages are refused, but the same section and implementation say the default result is a successful measurement. | Reserve “refused” for unusable input and state the enforcement-dependent exit once. | S |
| 2 | cmd/nova-ci/main.go:41 | A long embedded banner plus dispatch and two verb implementations gives one entry file several jobs and makes the primary path visually heavy. | Move each verb and its help fragment into a focused file while retaining one dispatch table. | M |
| 3 | cmd/nova-ci/main.go:54 | The same verb has two synopsis blocks, so a reader must merge basic and unit-tier flags mentally. | Show one synopsis and group the optional budget-policy flags underneath it. | S |

## Good, keep
The first command is safe and self-contained, and the banner tells where state lives.
The local-run comments explain why selection, niceness, cache behavior, and the Makefile remain single sources of truth.
The contract tests teach refusal wording, dry-run behavior, output grammar, and failure paths with concrete cases.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| five output dialects | STILL THERE | cmd/nova-ci/main.go:41 defines distinct slow, local, functional, scaffold, and receipt outputs |
| leftover script | STILL THERE | cmd/nova-ci/timing.go:1 remains an ignored standalone program beside the shipped command |
| a spec of verbs the tool does not have | FIXED | docs/CLI.md:1828 documents the verbs dispatched at cmd/nova-ci/main.go:252 |
| the banner pipeline example lacks the test-exit caveat where it is used | STILL THERE | cmd/nova-ci/main.go:49 gives the pipeline without the caveat that docs/CLI.md:1842 supplies |
