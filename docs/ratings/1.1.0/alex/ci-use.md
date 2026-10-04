# nova-ci USE rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 044b5dfe9c1b
Score: 8/10

## Reasons

Cold use: every command from `nova-ci help`, `nova-ci help <verb>`, `-h` and
the banner's example block, run in a scratch directory, nothing real touched (no
store, no remote, no gh, no PostgreSQL, no model, no key). What the READ
rating read was no evidence here.

(a) The first run works from the binary alone: `nova-ci slowtests --example
--budget 60` prints its CI-SLOW and CI-LOAD lines at exit 0, the same sitting
docs/TESTS.md documents. (b) Two real jobs, end to end, on a Go module made
for the purpose: piping the module's own `go test -json -timeout 600s ./slow ./fast`
stream into `nova-ci slowtests --budget 1` printed the slow package with its
slowest test, the CI-SLEEPS line for an unledgered skip and the load line at
exit 1; the same stream with the sleeps ledger was the OK line at exit 0, and
under --enforce exit 1 again. The second job: `nova-ci functional ./slow`
selected the package holding a functional-tagged test and printed the -run
pattern, and the runner accepted both; `nova-ci functional ./fast` answered
the packages=0 reason line at exit 0. Beyond the two: `nova-ci new-rule`
wrote its three files, refused a rerun naming the file already there, and
`nova-ci new-verb --dry-run` listed its four; `nova-ci local --base dev
--dry-run` on a scratch look-alike checkout printed the selected package and
the exact make line, and without --dry-run ran it, folding the stream to PKG
lines and an honest exit 2 when make failed with no red test. (c) Four
refusals provoked: the bare `nova-ci github receipt` names all seven missing
fields at once, each with what it wants; `nova-ci slowtests --bogus` names the
offending flag and every flag the verb takes; `nova-ci frobnicate` names the
unknown verb and the verbs there are; `--budget zero`, `--load NaN` and
`--conclusion skipped` each say what the flag wants. Every refusal is one line
at exit 2 with the next command at its end. (d) `--json` prints the verdict
or the refusal as the one object the help describes, with the same exit;
`--dry-run` on github receipt prints the line with ev=- and a NOTE and dials
nothing, on new-rule checks the tree and writes nothing, on local prints the
make line and runs nothing. The --max cap prints a MORE line with shown,
total and the flag that widens it. (e) Where I had to guess: the ./ prefix
the functional output needs (below); where the allowlist's where-labels are
read from; the 3x bound, which the help does not state but the refusal names
exactly ("budget 10s is not between its measurement 1s and 3 times it").

Not tried: `local` against this repository's own checkout (judged from its
help; its --dry-run and a full run were exercised on a scratch look-alike),
`github receipt` without --dry-run (a store write; judged from its help and
its --dry-run), and the committed timing script (run as a file, not a verb).

What costs the 2: the natural `./...` spelling of functional's argument prints
package paths the runner rejects, in any module; the CI-SLEEPS remedy line
ships its ledger phrase with escaped blanks; the allowlist label refusal gives
an empty list outside a checkout; the bound lives in a refusal, not the
help. A 10 needs the four findings fixed; with them every verb's first
natural spelling runs, and every rule the tool enforces is in the help it
prints.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci functional ./...` | prints package dirs with no ./ prefix ("slow" in my module, "cmd/nova-bus ..." in this repository), and the runner rejects them: `go list slow` answers "package slow is not in std", while `go list ./slow` resolves; the consumer appends the words to go test as-is | prefix ./ on the paths Expand walks from a "." root, as it already does for a "./x" root (internal/ci/functional/functional.go:138-141) | S |
| 2 | `nova-ci slowtests --budget 1` | the CI-SLEEPS line ships its ledger phrase as a typed value, so the default reads "not on the\x20SLEEPS\x20ledger\x20(no\x20--sleeps\x20given)": the remedy a first run most often meets carries escaped blanks a reader must unescape (cmd/nova-ci/main.go:354-357, internal/ci/slowtests/slowtests.go:547) | word the default ledger phrase without blanks, or render it as free text | S |
| 3 | `nova-ci slowtests --allowlist bad-allowlist.tsv --sleeps /dev/null` | the where-label refusal says "a runner label .github/workflows/ci.yml names ()" with an empty list when run outside a checkout, so the reader cannot act on it without guessing where the file is read from | name the directory the file was looked up in, or one valid label, in the refusal | S |
| 4 | `nova-ci help slowtests` | the allowlist's bound (a row's budget may be at most three times its measurement) is not in the help; I learned it by provoking the refusal, which names it exactly | add the bound clause to the --allowlist usage line | S |

## Good, keep

The refusal grammar names every problem at once: the bare github receipt names
all seven missing fields, each with what it wants, in one exit-2 line ending
in the verb's own -h.

--dry-run is honest everywhere it is offered: github receipt prints the line
with ev=- and dials nothing, new-rule checks the tree and writes nothing,
local prints the make line and runs nothing.

The first run needs nothing: --example reads a built-in stream, --load and
--cpus let the transcript reproduce on any machine, and the sleeps ledger,
the allowlist and --enforce all behaved exactly as the help says.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| functional ./... prints a path the test runner rejects | STILL THERE | `nova-ci functional ./...` printed "slow" in my module and "cmd/nova-bus cmd/nova-cairn ..." in this repository; `go list slow` answers "package slow is not in std", `go list ./slow` resolves |
| the allowlist's bound and the SLEEPS marker are not in the help | CHANGED | the help's unit-tier slowtests entry names the SLEEPS marker and the --sleeps ledger; the 3x bound is still not in the help but the refusal names it: a row of budget 10 over a 1s measurement is refused "budget 10s is not between its measurement 1s and 3 times it" |
| the unknown-option refusal omits the offending flag | FIXED | `nova-ci slowtests --bogus` answers "unknown flag --bogus; the flags are --allowlist, --budget, --cpus, --enforce, --example, --json, --load, --max, --package-budget, --sleeps, --test-budget" |
