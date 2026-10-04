# nova-ci READ rating, nova-tools 1.1.0

Rater: deepseek-v4.1-flash
Build: c28448a54d56
Score: 7.5/10
README: 7/10

## Reasons

Read cold, running nothing. The README line is README.md:40: the tool checks
test runs and their cost, "test-time budgets over go test -json output, and this
repository's own CI steps", and that sentence is the banner's line 1 word for
word (cmd/nova-ci/main.go:38), the family rule kept. Reading the README before
any code: first confused at README.md:40, where the row's "test-time budgets"
fights the prose's "reports their timings" so a stranger cannot tell whether a
budget gates a run or only measures one; first bored at README.md:63-71, four
commands of git ceremony for a bus trial I had not chosen; first doubted at
README.md:50-52, where the trial block is pinned to the 1.0.0 release inside a
1.1.0 tree, so a cold reader cannot tell whether the row's command matches the
head.

The best writing here is slowtests. It is store-free and its first run comes
from the binary alone: --example embeds a two-package stream
(cmd/nova-ci/main.go:31-36, 275), the README row's command and the banner's
example block are that sitting, and the docs say plainly that the times are a
measurement unless --enforce (docs/CLI.md:1936-1938). The refusal is the house
one, one line naming every problem at once with a remedy that runs
(cmd/nova-ci/main.go:385-390), and it is pinned by execution
(cmd/nova-ci/firstrun_test.go:52-63). The status grammar OK, REFUSED, FAILED
was unified across the verbs from one value and is pinned by TestStatusGrammar,
which is exactly the kind of test that teaches a contract.

The cost is that the tool is not yet one family. Only slowtests builds
internal/tool's one value and renders it as lines or --json; the other five
verbs print a shape of their own, so an AI reading the tool cold meets five
output dialects and no JSON to consume, against the standard's one output
structure with two renderings (docs/STANDARD.md:56). Its own spec names verbs
the binary does not have (docs/SPEC-CI.md:16 and its siblings), a second,
undispatched main sits in the tool's directory (cmd/nova-ci/timing.go:1), one
document prints the wrong exit for --enforce (docs/TESTS.md:908), and the CLI
section opens without the First run the standard requires
(docs/CLI.md:1920; docs/STANDARD.md:72). A 10 needs every verb on the one
value with --json, a spec and a help that name only the verbs that exist, the
hand-run script gone or wired into the banner and the reference, and the
numbers and the doors right wherever a stranger meets them.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-ci/main.go:53 | only slowtests takes `--json` and builds internal/tool's one value; functional, local, new-rule, new-verb and github receipt each print a shape of their own (main.go:528 CI FUNCTIONAL, local.go:380 PKG, newrule.go:59 wrote, receipt.go:108 CI RECEIPT), so an AI meets five output dialects and no data form to consume, against docs/STANDARD.md:56 | render every verb through internal/tool's one value and add `--json`, so the line and the object come from one value | L |
| 2 | docs/SPEC-CI.md:16 | the tool's own spec says the class test is entered "in the CI check roster and in help as the verb `waits`", and says the same of `testbins`, `templates`, `net` and `goenv`, but nova-ci dispatches none of them (cmd/nova-ci/main.go:145) and no roster or help names them; a cold reader cannot tell which verbs exist | say these run as `go test ./internal/ci -run Test...`, not as verbs, or add the roster and the help they promise | M |
| 3 | cmd/nova-ci/timing.go:1 | a second `package main` behind `//go:build ignore`, 128 lines plus the 490-line internal/ci/timing, run by hand and reached by no shipped verb; the banner (cmd/nova-ci/main.go:48-141) and docs/CLI.md:1920 never name it, and its default `--repos` names the organisation's other repository (internal/ci/timing/timing.go:40), carried in a generality debt row; it is the leftover script the last read named | delete the script and internal/ci/timing, or wire a `timing` verb into the banner, docs/CLI.md and the dispatch | M |
| 4 | docs/TESTS.md:908 | the nova-ci first-run transcript says "only `--enforce` makes it exit 2"; slowtests exits 1 when --enforce finds a package over budget (cmd/nova-ci/main.go:121-123, docs/CLI.md:1938, docs/SPEC-CI.md:232), so the one document a stranger copies names the wrong exit | change "exit 2" to "exit 1" in docs/TESTS.md:907-908 | S |
| 5 | docs/CLI.md:1920 | the nova-ci section opens with a summary and three commands, not the `### First run` block docs/STANDARD.md:72 says every tool's section opens with; the tool's first run lives only in docs/TESTS.md:891 | add the `### First run` block with the two slowtests commands and how to read CI-SLOW and CI-LOAD, as the section's siblings in the same file do | S |

## Good, keep

slowtests is store-free and runnable from the binary alone: --example embeds a
two-package stream so a stranger sees a CI-SLOW finding and a green with nothing
installed (cmd/nova-ci/main.go:31-36, 275; cmd/nova-ci/testdata/example-events.jsonl).

A refusal is one line that names every problem at once and ends in a command
that runs (cmd/nova-ci/main.go:385-390), and the first-run test executes the
banner's examples and those refusals rather than reading them
(cmd/nova-ci/firstrun_test.go:32-63).

The one status grammar was brought to every verb from one value and pinned by
TestStatusGrammar, which asserts the status word and the exit together
(cmd/nova-ci/status_grammar_test.go).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| this repository's own CI in five output dialects | STILL THERE | cmd/nova-ci/main.go:38-141 groups the verbs, but five shapes remain: main.go:539 CI-SLOW, main.go:528 CI FUNCTIONAL, local.go:380 PKG, newrule.go:59 wrote, receipt.go:108 CI RECEIPT; only slowtests takes --json (main.go:53) |
| a leftover script | STILL THERE | cmd/nova-ci/timing.go:1 is `//go:build ignore`, a second main (timing.go:30) run by hand; the banner (main.go:48-141) and docs/CLI.md:1920 never name it |
| a spec of verbs the tool does not have | STILL THERE | docs/SPEC-CI.md:16 names the verb `waits` "in the CI check roster and in help"; main.go:145 lists the verbs and `waits` is not one |
| the banner's pipeline example needs the test-exit caveat where it is used | STILL THERE | main.go:46 pipes `go test -json` into slowtests with no caveat; the caveat is only in docs/CLI.md:1934-1936 |
