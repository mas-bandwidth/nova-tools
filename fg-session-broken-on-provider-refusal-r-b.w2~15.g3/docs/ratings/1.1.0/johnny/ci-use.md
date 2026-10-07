# nova-ci USE rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 6.5/10

## Reasons

The first command in the banner runs, and the refusals are the best part of the tool. A missing receipt field, an unknown verb and a bad number each name the problem and the next command, and a dry run of the receipt prints the line it would write and dials nothing. It is not a 10. The two jobs the tool is for both have a trap that the help does not warn about: a red test still prints an OK line, and the functional selection's `./...` form prints a path the test runner rejects. A 10 would keep the test's own exit in that OK line, print a path the runner accepts, and refuse a scaffold whose root is not this repository.

First success, exit 0: `nova-ci slowtests --example --budget 60 --load 4 --cpus 16` printed one CI-SLOW line (seconds=65.1s, budget=60s) and one CI-LOAD line. The same command with `--budget 120` printed CI-SLOW OK packages=2. Nothing was set up.

Job one was a real module under a scratch directory. `go test -json -count=1 -timeout 600s .` exited 0, and slowtests with `--budget 60` read that stream. A skip whose text contained the marker produced a CI-SLEEPS line and exit 1 until a ledger named it; with the ledger the line was CI-SLOW OK. A package budget of 0.0001 with `--enforce` printed CI-SLOW for example.com/trial at 0.4s and exited 1. Job two was functional selection. Given `. ./store` it printed `./store` and `^(TestStoreRound)$`, and `go test -tags functional -run` on those two lines exited 0. Given `./...` it printed `store` without the dot-slash, and the same go test exited 1.

Four refusals, each exit 2. Missing flags: `nova-ci github receipt --dry-run` named `--from-runner`, repo, sha, run id, workflow and conclusion in one line. Unknown flag: `nova-ci slowtests --not-a-flag` named that flag and listed the flags. Unknown verb: `nova-ci nosuch` named the verbs and said to run help. Bad value: `--budget -3` said it wants a whole number of seconds greater than zero, and `--load Inf` said it wants a finite number. `--json` on the example printed one object whose items repeat the CI-SLOW line; `--json` on the unknown flag printed status refused on stdout. The receipt dry run, with good fields, printed CI RECEIPT with ev=- and a NOTE that nothing was dialled.

Guessed: the skip text, because help says "the SLEEPS marker" and not the string, and a tagged test file alone was reported as no functional tag until the directory also had a non-test file. Not tried, from the help: local's real test run (it needs this repository's checkout; elsewhere `--dry-run` refuses), a scaffold write (dry run only), and a receipt dial (dry run only, no store).

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci slowtests --budget 60 --sleeps sleeps.txt --load 1 --cpus 8` | The events were a go test that exited 1, one test calling Fatal. This verb printed CI-SLOW OK packages=1 and exited 0, and so did the connection the banner's first-run line tells you to make. Help calls the times a measurement and never says the test status is a different exit. An AI that trusts the OK line reports a red suite as green. | Put the test status on the OK line, or say in that first-run line that the test's exit has to be kept apart from this verb's. | M |
| 2 | `nova-ci functional ./...` | Printed `store` and `^(TestStoreRound)$`. go test with `-tags functional -run` on that directory name exited 1: package store is not in std. The same verb given `. ./store` printed `./store`, and that go test exited 0. The form a stranger types from the functional refusal's own example is the one that does not run. | Print the directory with the dot-slash prefix the test runner accepts, including when the argument was `./...`. | S |
| 3 | `nova-ci new-rule --dry-run demo-rule` | In a module that is not this repository, exit 0, and it listed three files it would write (a class test, a fixture, a make fragment) plus a NOTE that the dry run wrote nothing. In a directory with no go.mod, exit 2, and the line says it is not a checkout. Help says the verb needs this repository. The check is only that a go.mod exists, so dropping the dry-run flag would write into the wrong tree. | Refuse a root whose module path is not this repository, the same way an empty directory is refused, before listing files. | M |
| 4 | `nova-ci slowtests -h` | Help names a SLEEPS marker and the allowlist columns, and does not give the skip string or the rule that a row's budget sits between the measurement and 3 times it. A bad row is refused with that 3-times sentence, so the second run learns it; the first skip without a ledger prints the remedy with spaces turned into backslash-x20, which hides the ledger phrase. A flag the verb does not have also stops the run before a bad budget and an extra argument are named. | State the skip string and the 3-times rule in the flag text, print the ledger phrase as words, and when the flag parse fails still name the other problems. | M |

## Good, keep

A bare `nova-ci` exits 2 with one line naming every verb and `run: nova-ci help`, and the banner's example command prints a real CI-SLOW line with no module.

`nova-ci github receipt --dry-run` with empty fields names every missing field in one line. With good fields it prints CI RECEIPT ev=- and dials nothing, which is what the help says.

`nova-ci slowtests --not-a-flag` names that flag and lists the flags the verb has, then tells you to run the verb's `-h`.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| functional ./... prints a path the test runner rejects | STILL THERE | `nova-ci functional ./...` printed `store` and `^(TestStoreRound)$`; go test -tags functional -run on that name exited 1, package store is not in std. `. ./store` printed `./store` and that go test exited 0 |
| the allowlist bound and the SLEEPS marker are not in the help | CHANGED | `nova-ci slowtests -h` names the SLEEPS marker and the allowlist columns; a row whose budget is 9s on a 0.4s measurement is refused with "not between its measurement 0.4s and 3 times it". The bound and the skip string are still absent from -h |
| the unknown-option refusal omits the offending flag | FIXED | `nova-ci slowtests --not-a-flag` printed `unknown flag --not-a-flag` and the flag list, exit 2 |
