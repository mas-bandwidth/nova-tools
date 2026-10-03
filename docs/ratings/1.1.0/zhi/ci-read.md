# nova-ci READ rating, nova-tools 1.1.0

Rater: DeepSeek
Build: 2c02b2aa2042
Score: 8.5/10
README: 8/10

## Reasons
The README's one sentence for the tool is "test-time budgets over go test -json output, and this repository's own CI steps" (README.md:38); the code does exactly that. The first place I was confused was README.md:38, whose second clause ("and this repository's own CI steps") does not name the steps, so the tool's scope stayed unclear until docs/CLI.md:1830-1833 laid out the seven verbs. The first place I was bored was AGENTS.md:19-37, "Doing it right the first time": a page of maxims before any tool-specific prose, which I had to skim to reach the rules that bind nova-ci. The first claim I doubted was README.md:48, which calls these "the Nova Tools 1.0.0 commands" and installs @v1.0.0 (README.md:53), while the tree is at 1.1.0 (docs/RELEASE-NOTES-1.1.0.md).

The writing is close to a 10. docs/CLI.md:1830-1833 says what the tool is for in three lines and the verbs deliver it. The entry point, verbs and data are findable in under a minute: cmd/nova-ci/main.go:150 names every verb, and the banner (main.go:41-146) states each verb's effect, the exit codes per verb, and a runnable example: block. Each file is one thing (local.go, receipt.go, newrule.go, load.go, timing.go, version.go) and each comment says why, in the present tense. Names a stranger understands (slowtests, local, functional, new-rule, new-verb, github receipt). It is one family with the other tools: the same refusal grammar (nova-ci <verb> REFUSED: <every problem>; run: <next>), the one-line output grammar, --json rendered from the same value as the lines, --max with a MORE line, and exit codes stated per verb. The tests teach the contract: firstrun_test.go walks the onboarding points and executes the banner's examples and the docs/TESTS.md transcript, and slowtests_test.go feeds canned events.

What keeps it from a 10: a spec/code disagreement on an exit code (SPEC-CI.md:282 says a CI-SLEEPS line exits 2, while the code and the banner exit 1); a stale README that installs 1.0.0; and a committed side script (cmd/nova-ci/timing.go) that carries a second entry point but is not wired as a verb. Each is small, and each is named below.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-CI.md:282 | the spec says an unledgered SLEEPS skip exits 2, but slowtests.Verdict (internal/ci/slowtests/slowtests.go:610-611) and the banner (cmd/nova-ci/main.go:127) exit 1; an AI that scripts against the spec's 2 would miss the failure | change exit 2 to exit 1 in SPEC-CI.md:282 so the spec, the code and CLI.md:1881 agree | S |
| 2 | README.md:48 | the table says "Nova Tools 1.0.0 commands" and the install line uses @v1.0.0 (README.md:53), while the tree is 1.1.0; a cold reader installs the previous release | bump the README's version and the install example to 1.1.0, or point at the latest release | S |
| 3 | cmd/nova-ci/timing.go:1 | a committed //go:build ignore script with its own main and usage sits inside cmd/nova-ci but is not a verb, so a reader sees a second entry point the binary never exposes | wire a timing verb (one dispatch case in main.go) or move the script to a scripts/ directory | S |

## Good, keep
The refusal grammar that names every problem at once with a remedy is the best part of the tool and must not be lost. The banner's per-verb exit-code table, quoted by each verb's -h, is a rare and valuable promise. The tests that execute the banner's examples and the docs/TESTS.md transcript make the docs trustworthy.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| this repository's own CI in five output dialects | STILL THERE | slowtests prints CI-SLOW, CI-SLEEPS and CI-LOAD (cmd/nova-ci/main.go:358, 362-364); local prints PKG and RED (cmd/nova-ci/local.go:375, 415); github receipt prints CI RECEIPT (cmd/nova-ci/receipt.go:108) |
| a leftover script | CHANGED | timing.go is now an explicitly documented //go:build ignore script with a stated purpose and run line (cmd/nova-ci/timing.go:1, 18-27) |
| a spec of verbs the tool does not have | CHANGED | docs/CLI.md:1830-1833 names exactly the seven verbs the binary has; the class-test verbs (waits, net, tiers and the rest) live in SPEC-CI.md and run as go test rules, not as nova-ci verbs |
| the banner's pipeline example needs the test-exit caveat where it is used | FIXED | docs/CLI.md:1843-1844 now says to check the test run's exit status separately because slowtests checks timing, not pass or fail |
| README then 6.5 to 8.4 | CHANGED | the nova-ci table row (README.md:38) and its first command stay clear; the remaining ding is the 1.0.0 version string at README.md:48 |
