# nova-ci USE rating, nova-tools 1.1.0

Rater: qwen3.8-flash
Build: 402eab2ac09b
Score: 8.5/10

## Reasons

Cold use from the binary alone. The first run needs nothing set up: `nova-ci slowtests --example --budget 60` prints one CI-SLOW line for the built-in over-budget package and one CI-LOAD line, exit 0. Two real jobs ran end to end in a Go module made under scratch: `go test -json -count=1 . | nova-ci slowtests --budget 1 --enforce` caught a two-second test as `CI-SLOW package=example.com/mymod seconds=2.0s budget=1s slowest=TestSlow:2.0s` and exited 1; the second job, `nova-ci functional ./...`, named `store` on one line and the run pattern `^(TestFunctionalThing)$` on the next, but pasting those into `go test` failed — `go test -tags functional -count=1 -run '^(TestFunctionalThing)$' store` exits 1 with `package store is not in std` — so the job took one wrong guess before `./store` ran. Four refusals each named the problem, every problem at once, and the next command: `nova-ci github receipt` with no flags listed every missing field with what each wants, the store requirement and its alternative, and the help command; `nova-ci slowtests --example --bogus` named the flag and listed every flag; `nova-ci frobnicate` named the verb and listed the verbs; `nova-ci slowtests --example --budget 0` named the value and the unit it wants. The near-miss hint that makes recovery one step for a close typo (`--budg` and `--budgt` each yield `did you mean --budget?`) stays silent when the guess is far, and `--bogus` gets the list alone. `--json` is one object with the same verdict as the lines, and a refusal renders as one object carrying its reason and remedy. `--dry-run` is offered by every write verb the help lists and its output is the plan, not a guess: `nova-ci github receipt ... --dry-run` printed the receipt line with `ev=-` and a note that nothing was dialled, and `nova-ci local --dry-run` printed the base, the merge base, the packages and the exact make line.

What a 10 needs: print functional's selected directories as arguments go test accepts, each with a leading `./` — the defect an earlier rating caught and this head still has; state the allowlist row's bound in `slowtests -h`; carry the per-cpu figure into the JSON facts; bound or explain the single run pattern `functional` prints, 829 names deep in this checkout; give an unknown verb the nearest name, as an unknown flag already does for a close typo, and fire that hint for far guesses too. The verbs that need this repository's own checkout (`local`, `new-rule`, `new-verb`) were judged from their help, `local` also from its `--dry-run`; the store write (`github receipt`) was judged from its help and its `--dry-run`, since no store was dialled and no key was read.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci functional ./...` in a scratch module | the first line names each selected package bare (`store`), and pasting it into `go test` fails: `go test -tags functional -count=1 -run '^(TestFunctionalThing)$' store` exits 1 with `package store is not in std`; only `./store` runs, so what the inspection prints is not an argument the runner accepts | print each selected directory with a leading `./` so the line pastes into go test unchanged | S |
| 2 | `nova-ci functional ./...` in this checkout | the second line is one run pattern naming every functional test in the tree: 829 names in one argument of 34,765 characters an AI must pass to go test without reading it, and the first line's 39 bare directories hit the same paste failure as finding 1 | print a note that the pattern is the whole set, or split it under a stated bound | S |
| 3 | `nova-ci slowtests --example --budget 60 --allowlist allow.tsv` | the help gives the row shape but not the bound; a row over it is refused with `budget 999s is not between its measurement 65.1s and 3 times it`, so an AI writing a row learns the ceiling only from a refusal | add one line to `slowtests -h` naming the bound between a row's measurement and three times it | S |
| 4 | `nova-ci slowtests --example --budget 60 --json` | the facts carry load and cpus but not the per-cpu figure the CI-LOAD line prints (`per-cpu=0.73` in the line, absent from the JSON), so the two renderings of one run drift and a JSON consumer must recompute it | add per-cpu to the JSON facts beside load and cpus | S |
| 5 | `nova-ci helpp` | an unknown verb lists the verbs and the remedy but names no nearest, though a close unknown flag does (`--budg` yields `did you mean --budget?`) | name the nearest verb the same way an unknown flag does | S |

## Good, keep
The refusal grammar that names every problem at once with a remedy, lists the flags or verbs, and adds a near-miss hint for a close typo in a flag name, is the best thing about using this tool and must not be lost. `--dry-run` printing the exact plan for every write verb, and `--json` rendering the same verdict as the lines, make the write verbs safe to try. A first run with no setup (`slowtests --example`) and one command per real job are what make the tool land in a minute.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| `functional ./...` prints a path the test runner rejects | STILL THERE | from a scratch module `nova-ci functional ./...` printed `store` and `^(TestFunctionalThing)$`; `go test -tags functional -count=1 -run '^(TestFunctionalThing)$' store` exits 1 with `package store is not in std`, while `./store` exits 0 |
| the allowlist's bound and the SLEEPS marker are not in the help | CHANGED | `nova-ci help slowtests` now says a test skipped with the SLEEPS marker and not on `--sleeps` is a CI-SLEEPS line, but the bound still appears only in the refusal `budget 999s is not between its measurement 65.1s and 3 times it` |
| the unknown-option refusal omits the offending flag | FIXED | `nova-ci slowtests --example --budg 5` prints `unknown flag --budg; ... did you mean --budget?`, exit 2 |
