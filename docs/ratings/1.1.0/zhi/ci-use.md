# nova-ci USE rating, nova-tools 1.1.0

Rater: DeepSeek
Build: 2c02b2aa2042
Score: 9/10

## Reasons
Cold use, from `nova-ci help` alone. The first run is one command that needs no setup: `nova-ci slowtests --example --budget 60` prints a CI-SLOW line for the built-in over-budget package and a CI-LOAD line, exit 0. Two real jobs then ran end to end in a scratch Go module: `go test -json -count=1 . | nova-ci slowtests --budget 1` caught a two-second test as `CI-SLOW package=example.com/mymod seconds=2.1s budget=1s slowest=TestSlow:2.0s`, and `nova-ci functional ./cmd/nova-ci` named the three functional-tagged tests plus a -run pattern. Four refusals each named the problem and the next command, and named every problem at once: `nova-ci github receipt --repo owner/name --sha <40hex> --run-id 1 --workflow CI --conclusion success` refused in one line with `--from-runner is required`, `--sha wants the 40-hex head`, and `needs --redis ... or --dry-run`; an unknown flag named itself and listed every flag; an unknown verb listed the verbs; `--budget 0` and `--load NaN` were each refused with the value and the unit it wants. `--json` and `--dry-run` do what the help says: the JSON is one object carrying the same verdict as the lines, and `--dry-run` on local, new-rule, new-verb and github receipt printed the plan and wrote nothing.

Where I had to guess: github receipt needs a real store for a write, and a real `local` run would run the unit tier, so those two paths were judged from their help and their `--dry-run`, not run for real. What a 10 needs: state the allowlist row's three-times headroom bound in `slowtests -h`, carry the CI-LOAD per-cpu figure into the JSON facts, and bound or explain the giant `functional` -run pattern.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci slowtests -h` | the help says an allowlist row names a higher budget but never states the ceiling the code enforces (a row's budget may not exceed three times its measured seconds, slowtests.go:223); an AI writing a row learns the bound only from a refusal or the spec | add one line to the slowtests help naming the three-times headroom ceiling | S |
| 2 | `nova-ci slowtests --example --budget 60 --json` | the CI-LOAD line prints a per-cpu figure but the JSON facts carry load and cpus only, so the two renderings of the same run drift and a JSON consumer must recompute per-cpu | add per-cpu to the JSON facts beside load and cpus | S |
| 3 | `nova-ci functional ./...` | one -run pattern names every functional test in the tree with no bound or note, so a large tree yields one unreadable argument that could near shell limits | add a --max or a one-line note that the pattern is the full set for the make target | S |

## Good, keep
The refusal grammar that names every problem at once with a remedy, and lists the flags or verbs after an unknown one, is the best thing about using this tool and must not be lost. The `--dry-run` verbs that print the exact plan and write nothing make the write verbs safe to try. The `--json` that renders the same verdict as the lines, and `slowtests --example` with no setup, are what make a first run land in one minute.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| functional ./... prints a path the test runner rejects | FIXED | `nova-ci functional ./...` prints directory paths (`cmd/nova-bus cmd/nova-cairn cmd/nova-ci ... tools/fardelay`) then a `-run` pattern, exit 0, both valid go test arguments |
| the allowlist's bound and the SLEEPS marker are not in the help | CHANGED | `nova-ci slowtests -h` now names the SLEEPS marker ("A test skipped with the SLEEPS marker and not on --sleeps ... is a CI-SLEEPS line") but still does not state the three-times headroom bound on a row |
| the unknown-option refusal omits the offending flag | FIXED | `nova-ci slowtests --example --bogus` prints `nova-ci slowtests REFUSED: unknown flag --bogus; the flags are --allowlist, --budget, --cpus, --enforce, --example, --json, --load, --max, --package-budget, --sleeps, --test-budget; run: nova-ci slowtests -h`, exit 2 |
