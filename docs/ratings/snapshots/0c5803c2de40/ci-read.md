# nova-ci READ rating, current baseline 0c5803c2de40

Rater: GLM (Z.ai), the model behind this opencode worker
Build: 0c5803c2de40
Score: 7.5/10
README: 8.5/10

## Reasons
The source under review is exactly 0c5803c2de406c1b0b2b0841f579c9bf73406b1c; the reading and every file:line below describe that snapshot.
Reading order was README.md top to bottom, then AGENTS.md, then the nova-ci
section of docs/CLI.md and its spec docs/SPEC-CI.md, then cmd/nova-ci from
main.go into the internal packages it leans on (internal/ci/slowtests,
internal/ci/functional, pkg/tool, pkg/oneline, pkg/bounded,
internal/yield). No binary ran and no test ran for this score; the reading
alone decides it.

The README line that sells the tool is README.md:38: test-time budgets over
go test -json output, plus this repository's own CI steps, first command
nova-ci slowtests --example --budget 60.

First confused: README.md:38, the setup cell — "github receipt writes to a
Redis store" names a two-word verb and a store before the tool's grammar is
known, and the why (the ci-ok job's run receipt) is explained only three
documents deep at docs/CLI.md:1913-1919.
First bored: README.md:44 — "Pick the row that is your actual problem today.
One tool is a fine number" is the third telling of the same adoption advice,
after README.md:12-14 and again at README.md:71-72 and README.md:88-89.
First doubted a claim: README.md:38 — one sentence selling two tools (budgets
over events, and "this repository's own CI steps", which names no verb); the
second half is borne out only at docs/CLI.md:1860-1870 and
cmd/nova-ci/local.go:11-30, where local is pinned to CI's own selection and
the Makefile's one test target.

The tool earns trust where it counts. The banner answers what, how and first
run in one screen (cmd/nova-ci/main.go:41-49), and its example lines are
executed by the package's own test (cmd/nova-ci/firstrun_test.go:32).
Refusals name every problem at once and word a flag mistake for a reader
holding only the help (cmd/nova-ci/main.go:176-197). The allowlist refuses a
budget that is not between its own measurement and three times it, so no
number on the list is a guess (internal/ci/slowtests/slowtests.go:223-225).
The tests are the contract made executable: the documented transcript is run
line for line (cmd/nova-ci/firstrun_test.go:87), and the JSON rendering is
held to the same verdict as the lines (cmd/nova-ci/main_test.go:226).

What holds the score at 7.5 is drift between the tool's written surface and
its real one, met as text. Its spec presents help lines for verbs the banner
does not print (docs/SPEC-CI.md:16 against cmd/nova-ci/main.go:150). The
transcript document teaches the wrong exit under --enforce: docs/TESTS.md:812
says 2, while the exit table cmd/nova-ci/main.go:126-127 and the verdict code
internal/ci/slowtests/slowtests.go:610-612 say 1. And a measurement script
unreachable from the binary still lives in the command's directory, its
header arguing from a sprint card's file list (cmd/nova-ci/timing.go:21).
The tool that enforces the house standard is also the one tool off the house
skeleton, its dispatch, refusal printer and exit-table extraction hand-rolled
beside pkg/tool's copies, with no reason written where the reader stands
(cmd/nova-ci/main.go:232).

A 10 needs: the spec's help lines to name verbs that exist (or the verbs to
exist); the transcript prose to agree with the exit table it sits under; the
script gone, wired in, or explained in words that read at this snapshot; the
skeleton question answered in one line at the dispatch; and local's stream
printed in the one key=value grammar the rest of the tool already speaks.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-CI.md:16 | the spec's help line names a verb, waits, that no help prints; the same block form at docs/SPEC-CI.md:80 and docs/SPEC-CI.md:148 names two more, and the banner's verb list, cmd/nova-ci/main.go:150, holds none of them, so a reader auditing the tool against its spec finds the surface missing | either land the roster as a real help section of nova-ci, or rewrite the spec's help-line blocks to name the class test's Go entry point it actually runs | M |
| 2 | cmd/nova-ci/timing.go:21 | a second, unbuilt program lives in the command's directory and its header argues from a sprint card's file list, words that mean nothing to a reader at this snapshot; the measurement it exists for is reachable from no verb and named in no README row | move it under tools/ with a header that states what keeps it unbuilt and what runs it, or wire the verb in and say why it is a file | M |
| 3 | cmd/nova-ci/main.go:232 | the tool that enforces the house standard stands off the shared skeleton: dispatch, refusal printer and exit-table extraction are hand-rolled here beside pkg/tool's copies, and no line at the switch says why, so the tree carries two of each | write the reason at the dispatch in one line, or move the tool onto pkg/tool and delete the copies | M |
| 4 | docs/TESTS.md:812 | the prose under the executed transcript says --enforce makes slowtests exit 2; the exit table, cmd/nova-ci/main.go:126-127, and the verdict, internal/ci/slowtests/slowtests.go:610-612, say 1, the check ran and said no, so the document a stranger copies from teaches the wrong exit | write exit 1 there | S |
| 5 | docs/CLI.md:1828 | the standard's onboarding point 3 says the tool's section here opens with a First run heading; nova-ci's section has none, and its transcript lives under docs/TESTS.md:795, while most sibling tools carry the heading in this file | add the heading over the existing three-command block at docs/CLI.md:1835, or point the reader to the transcript by name | S |
| 6 | cmd/nova-ci/local.go:375 | local prints its package lines as fixed-width columns while every other finding line of the tool is key=value, so one run speaks two line grammars, the residue of the earlier dialect critique | print PKG as key=value through the same helper that renders RED | S |
| 7 | cmd/nova-ci/local.go:56 | the compile-time pin on the niceness constant carries a comment naming localNice, but the const named localNice is declared nine lines down, so two comments claim one name and the pin reads as a typo until unpacked | one comment that states the pin's intent in words, or drop the duplicated name | S |
| 8 | docs/CLI.md:1854 | the sentence sends CI exceptions to "the dated project policy" and names no file; the policy it means is the allowlist row format described two paragraphs down at docs/CLI.md:1872-1875 | name the allowlist file in that sentence | S |

## Good, keep
The banner answers what, how and first run in one screen, and its example lines are executed, not trusted (cmd/nova-ci/main.go:41-49, cmd/nova-ci/firstrun_test.go:32).
One refusal grammar, every problem named at once, and a flag mistake worded for a reader holding only the help (cmd/nova-ci/main.go:176-197).
The allowlist refuses a budget not between its measurement and three times it, so no number on the list is a guess (internal/ci/slowtests/slowtests.go:223-225).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| five output dialects | CHANGED | the CI line families now share one key=value grammar, internal/ci/slowtests/slowtests.go:531 and cmd/nova-ci/main.go:562; the residue is the PKG column line, cmd/nova-ci/local.go:375 |
| a leftover script | STILL THERE | cmd/nova-ci/timing.go:1 is an ignore-tagged file run by hand, absent from the verb list at cmd/nova-ci/main.go:150 |
| a spec of verbs the tool does not have | STILL THERE | docs/SPEC-CI.md:16 presents a waits help line; cmd/nova-ci/main.go:150 lists no such verb |
| the banner's pipeline example needs the test-exit caveat where it is used | STILL THERE | the first-run pipeline line at cmd/nova-ci/main.go:49 still carries no exit-status caveat; the caveat lives only in docs/CLI.md:1842-1844 |
