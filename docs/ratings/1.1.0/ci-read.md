# nova-ci READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: bc60d1f260ea
Score: 7.5/10
README: 7/10

## Reasons

Read cold, running nothing. The README line is README.md:40: the tool checks
test runs and their cost, "test-time budgets over go test -json output, and this
repository's own CI steps", and that sentence is the banner's line 1 word for
word (cmd/nova-ci/main.go:38), the family rule kept. Reading the README before
any code: first confused at README.md:40, where the row's "test-time budgets"
fights the prose's "reports their timings" so a stranger cannot tell whether a
budget gates a run or only measures one; first bored at README.md:24-34, where
extensive setup paragraphs and shell snippets are packed into table cells,
slowing down comprehension; first doubted at README.md:50-52, where the trial
block is pinned to the 1.0.0 release inside a 1.1.0 tree, so a cold reader cannot
tell whether the row's command matches the head.

The best writing here is slowtests. It is store-free and its first run comes
from the binary alone: --example embeds a two-package stream
(cmd/nova-ci/main.go:31-36, 280), the README row's command and the banner's
example block are that sitting, and the docs say plainly that the times are a
measurement unless --enforce (docs/CLI.md:1936-1938). The refusal is the house
one, one line naming every problem at once with a remedy that runs
(cmd/nova-ci/main.go:179-182), and it is pinned by execution
(cmd/nova-ci/firstrun_test.go:52-63). The status grammar OK, REFUSED, FAILED
was unified across the verbs from one value and is pinned by TestStatusGrammar,
which is exactly the kind of test that teaches a contract.

The help changes of this wave fixed two of the four defects the last read
named. The banner's exit summary now names the three verbs that say no at exit
1 (cmd/nova-ci/main.go:125-128), and its example block runs --example, not a
pipeline, so the test-exit caveat is no longer owed where a stranger pastes.
The spec now names each class test as a go test, not a nova-ci verb
(docs/SPEC-CI.md:12-16), and a test pins that the spec's verbs are the
tool's verbs.

The cost is that the tool is still not one family. Only slowtests builds
pkg/tool's one value and renders it as lines or --json; the other five
verbs print a shape of their own, so an AI reading the tool cold meets five
output dialects and no data form to consume, against the standard's one output
structure with two renderings (docs/STANDARD.md:56). A second, undispatched
main sits in the tool's directory (cmd/nova-ci/timing.go:1), the dispatch and
refusal printer are hand-rolled beside pkg/tool's copies
(cmd/nova-ci/main.go:169-206, 236-259), one document prints the wrong exit for
--enforce (docs/TESTS.md:908), and the CLI section opens without the First run
the standard requires (docs/CLI.md:1920). A 10 needs every verb on the one
value with --json, the hand-run script gone or wired into the banner, the
dispatch on the shared skeleton or its reason written where a reader stands,
and the numbers and the doors right wherever a stranger meets them.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-ci/main.go:54 | only slowtests takes `--json` and builds pkg/tool's one value; functional, local, new-rule, new-verb and github receipt each print a shape of their own (main.go:533 CI FUNCTIONAL, local.go:380 PKG, newrule.go:69 wrote, receipt.go:108 CI RECEIPT), so an AI meets five output dialects and no data form to consume, against docs/STANDARD.md:56 | render every verb through pkg/tool's one value and add `--json`, so the line and the object come from one value | L |
| 2 | cmd/nova-ci/timing.go:1 | a second `package main` behind `//go:build ignore`, 128 lines plus the 490-line internal/ci/timing, run by hand and reached by no shipped verb; the banner (cmd/nova-ci/main.go:38-146) and docs/CLI.md:1920 never name it; its default `--repos` names another repository, carried in a generality debt row; it is the leftover script the last read named | delete the script and internal/ci/timing, or wire a `timing` verb into the banner, docs/CLI.md and the dispatch | M |
| 3 | cmd/nova-ci/main.go:236 | the tool that enforces the house standard stands off the shared skeleton: dispatch, refusal printer and exit-table extraction are hand-rolled beside pkg/tool's copies (cmd/nova-ci/main.go:169-206), and no line at the switch says why, so the tree carries two of each | write the reason at the dispatch in one line, or move the tool onto pkg/tool and delete the copies | M |
| 4 | docs/TESTS.md:908 | the nova-ci first-run transcript says "only `--enforce` makes it exit 2"; slowtests exits 1 when --enforce finds a package over budget (cmd/nova-ci/main.go:127, docs/CLI.md:1937, docs/SPEC-CI.md:220), so the document a stranger copies names the wrong exit | change "exit 2" to "exit 1" in docs/TESTS.md:908-909 | S |
| 5 | docs/CLI.md:1920 | the nova-ci section opens with a summary and three commands, not the `### First run` block docs/STANDARD.md:72 says every tool section opens with; the tool first run lives only in docs/TESTS.md:892 | add the `### First run` block with the two slowtests commands and how to read CI-SLOW and CI-LOAD, as sibling sections in the same file do | S |

## Good, keep

slowtests is store-free and runnable from the binary alone: --example embeds a
two-package stream so a stranger sees a CI-SLOW finding and a green with nothing
installed (cmd/nova-ci/main.go:31-36, 280; cmd/nova-ci/testdata/example-events.jsonl).

A refusal is one line that names every problem at once and ends in a command
that runs (cmd/nova-ci/main.go:179-182), and the first-run test executes the
banner's examples and those refusals rather than reading them
(cmd/nova-ci/firstrun_test.go:32-63).

The one status grammar was brought to every verb from one value and pinned by
TestStatusGrammar, which asserts the status word and the exit together
(cmd/nova-ci/status_grammar_test.go).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| this repository's own CI in five output dialects | STILL THERE | cmd/nova-ci/main.go:38-146 groups the verbs, but five shapes remain: main.go:533 CI FUNCTIONAL, local.go:380 PKG, newrule.go:69 wrote, receipt.go:108 CI RECEIPT; only slowtests takes --json (cmd/nova-ci/main.go:54) |
| a leftover script | STILL THERE | cmd/nova-ci/timing.go:1 is //go:build ignore, a second main run by hand; the banner (cmd/nova-ci/main.go:38) and docs/CLI.md:1920 never name it |
| a spec of verbs the tool does not have | FIXED | docs/SPEC-CI.md:12 now names TestNoFixedWaitsOnTheCIPath as a go test in internal/ci, not a verb; TestSpecCIVerbsAreTheToolsVerbs pins the spec verbs to cmd/nova-ci/main.go:150 |
| the banner's pipeline example needs the test-exit caveat where it is used | FIXED | cmd/nova-ci/main.go:142 runs --example rather than a pipeline, and exit summary cmd/nova-ci/main.go:125 names the three verbs that exit 1 |
