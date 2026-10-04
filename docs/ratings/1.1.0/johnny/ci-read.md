# nova-ci READ rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 6.5/10
README: 6.5/10

## Reasons

The README row is findable, and its first command needs nothing but the binary. The verb behind that command is a real piece of writing: one engine, a banner that states each verb's exits, refusals that name every problem and the next command, and tests that pin the lines. It is not a 10. The sentence that says what the tool is, and the spec that sentence's section points at, do not tell one story. A 10 would name only the verbs this binary has, put one success grammar on every verb, make every exit number match the banner, and put the test-exit caveat on the pipeline line the banner itself shows.

Confused at README.md:38. One cell says the tool is test-time budgets over go test -json output and also this repository's own CI steps, then names local, new-rule, new-verb and a receipt verb without saying which half they belong to.

Bored at README.md:64. After the table already gave a first command, the try-one section spends a git-init ritual on a different tool and never comes back to this one.

Doubted README.md:48. The page says these are the 1.0.0 commands, and the install line pins that tag, while the CI row already talks like later machinery. Before any code, that pin is not a claim to trust.

Read on, the banner's first line repeats the README sentence (cmd/nova-ci/main.go:41) and the code does the budget half. The CI-steps half is a second program, which the workflow builds and calls. The command reference ends by pointing at a class-test index written as if those tests were this binary's verbs. Inside the one section that is the budget verb, that spec says the over-budget exit is 1 and, in its numbered list, 2.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-CI.md:309 | The numbered red test says an over-budget package exits 2 under enforce. docs/SPEC-CI.md:232 and the banner at cmd/nova-ci/main.go:126 say 1. The engine returns 1 at internal/ci/slowtests/slowtests.go:610. docs/TESTS.md:811 says enforce exits 2. The comment at cmd/nova-ci/main.go:281 says exit 2. Makefile:294 then rewrites the verb's non-zero into 2 when the tests were green. An AI that branches on the exit cannot tell a budget no from a usage refusal. | Make the numbered list, the transcript prose and the function comment say 1, and put one sentence on the banner that the make target turns that 1 into 2 when the test status was 0. | M |
| 2 | docs/SPEC-CI.md:13 | The spec calls a class test the verb waits, and repeats that shape down the index. This binary's verb list at cmd/nova-ci/main.go:150 does not include them. docs/CLI.md:1909 points here, so a cold reader runs commands the tool refuses as unknown. The steps the first sentence claims are a second program at tools/ci/main.go:1. | Say these are class tests, not verbs of this binary, and name the second program in the first sentence so it is not this tool. | L |
| 3 | cmd/nova-ci/main.go:49 | The banner's own-module line is a pipe and does not say that a failing test's exit is not this verb's exit. docs/CLI.md:1843 says it beside the same command. Success lines are five dialects: a CI-SLOW line, bare package paths at cmd/nova-ci/main.go:569, PKG prose at cmd/nova-ci/local.go:213, a wrote line, and a receipt line at internal/cireceipt/cireceipt.go:143. A reader who learned the refusal grammar cannot read a success. | Add the test-exit caveat to that banner line, and lead functional, local and the scaffold with one status word, the way the receipt line already leads with its name. | M |
| 4 | docs/CLI.md:1828 | This tool's section does not open with a first-run heading. Neighbouring sections do, and the onboarding rule says the command reference opens with that heading. The transcript that should sit here is in another file, and its exit words disagree with the banner. | Open this section with that heading, filled from a real run, and keep its exit words equal to the banner. | S |

## Good, keep

The built-in event stream lets a first run print a real over-budget line with no module and no checkout (cmd/nova-ci/main.go:34).

A refusal is one line, every problem at once, and the next command (cmd/nova-ci/main.go:167). With the JSON flag the same verdict is one object, not a second story (cmd/nova-ci/main.go:469).

local runs the Makefile target instead of copying its flags, so the verb cannot drift from the leg it claims to be (cmd/nova-ci/local.go:11).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| five output dialects in one tool | STILL THERE | cmd/nova-ci/main.go:569 prints bare paths, cmd/nova-ci/local.go:213 prints PKG prose, internal/cireceipt/cireceipt.go:143 prints a receipt line, and slowtests prints CI-SLOW |
| a leftover script beside the verb | CHANGED | no stray slowtests shell remains; the CI steps are a second program at tools/ci/main.go:1 that the workflow still builds |
| a spec of verbs the tool does not have | STILL THERE | docs/SPEC-CI.md:13 calls the class test the verb waits, and cmd/nova-ci/main.go:150 does not list it |
| the banner pipeline line omits the test-exit caveat | STILL THERE | cmd/nova-ci/main.go:49 shows the pipe with no caveat, while docs/CLI.md:1843 states it beside the same command |
