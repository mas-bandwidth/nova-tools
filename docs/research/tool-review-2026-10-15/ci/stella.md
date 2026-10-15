# nova-ci AI-usability review, 2026-10-15

Reviewer: gpt-6-luna
Build: b4f484c84916
Score: 8.5/10 (47/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-ci help` -> `first run: nothing to set up: slowtests --example reads a built-in event stream.` |
| 2 | refusals | 5 | `nova-ci github receipt --dry-run` -> `nova-ci github receipt REFUSED: --from-runner is required: the ci-ok job reports its own run, the one source; --repo wants owner/name, got ""; --sha wants the 40-hex head the run tested, got ""; --run-id wants the decimal github.run_id, got ""; --workflow wants the workflow's name (github.workflow); --conclusion wants success, failure or cancelled (job.status), got ""; run: nova-ci github receipt -h` (exit 2; all missing fields are named together) |
| 3 | output | 4 | `nova-ci slowtests --example --budget 60 --load 4 --cpus 16 --json` -> `{"result":{"verb":"slowtests","status":"ok","exit":0,"remedy":"nova-ci slowtests '--example' '--budget' '60' '--load' '4' '--cpus' '16' '--json' --enforce"},"facts":{"packages":2,"slowest":"github.com/mas-bandwidth/nova-tools/internal/example","slowest_seconds":65.1,"enforce":false,"load":4,"cpus":16,"within_budget":false,"warning":"budget exceeded without enforcement; reuse the same event stream"},"items":[{"kind":"slow-package","fields":{"package":"github.com/mas-bandwidth/nova-tools/internal/example","seconds":65.1,"budget":60,"slowest":"TestSlowThing:63.4s,TestAlsoSlow:1.5s"}}]}`; the paired text line reports `per-cpu=0.25` |
| 4 | dry-run | 4 | `nova-ci github receipt --from-runner --redis 127.0.0.1:1 --repo example/tool --sha 0000000000000000000000000000000000000000 --run-id 123 --workflow smoke --conclusion success --at 2026-10-15T16:50:00Z --dry-run` -> `CI RECEIPT NOTE --dry-run: the fields are good; nothing was dialled or written; drop --dry-run to write it`; `bench run` writes a temporary run directory and offers no dry-run |
| 5 | idempotent | 0 | `nova-ci help github receipt` -> `--dry-run  check the fields and print the line with ev=-; dial and write nothing`; the listed write flags have no `--op` |
| 6 | no prompts | 5 | `nova-ci new-rule --dry-run reviewprobe` -> `nova-ci new-rule NOTE --dry-run wrote nothing; run it again without --dry-run to write these 3 files`; all exercised commands were noninteractive |
| 7 | exit codes | 5 | `nova-ci slowtests --example --budget nope` -> `nova-ci slowtests REFUSED: invalid value for --budget: it wants a whole number (whole seconds a package's tests may take before it is over budget); run: nova-ci slowtests -h` (exit 2) |
| 8 | one word | 5 | `nova-ci mystery` -> `nova-ci REFUSED: unknown verb "mystery"; the verbs are slowtests, functional, local, new-rule, new-verb, bench run, github receipt, version, help; run: nova-ci help` |
| 9 | push | 5 | `n/a: no state-waiting command exists; bench run streams the invoked command's output rather than polling` |
| 10 | no leaks | 5 | `nova-ci github receipt --from-runner --redis 127.0.0.1:1 --repo example/tool --sha 0000000000000000000000000000000000000000 --run-id 123 --workflow smoke --conclusion success --at 2026-10-15T16:50:00Z --dry-run` -> `CI RECEIPT example/tool sha=0000000000000000000000000000000000000000 run=123 workflow=smoke conclusion=success pr=- ev=-`; no credential or secret value is emitted |
| 11 | true help | 4 | `nova-ci new-rule --dry-run reviewprobe` -> `nova-ci new-rule NOTE --dry-run wrote nothing; run it again without --dry-run to write these 3 files`; actual writes and delivery were not run |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci slowtests --example --budget 60 --load 4 --cpus 16 --json` | The `CI-LOAD` line reports `per-cpu=0.25`, but the JSON `facts` carry only `load` and `cpus`, so a machine reader loses a value the text result provides. This omission was also reported in the 2026-10-02 ratings and remains. | Include `per_cpu` in the JSON facts using the same calculation as the text line. | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci github receipt` | The receipt write has no operation key; a source comment accepts duplicate retry receipts as wake hints. | Consider an optional `--op` with duplicate suppression if consumers need exactly-once receipt rows. | M |
| 2 | `nova-ci bench run --host <h> --dir . -- go vet ./cmd/nova-ci/` | This command creates and removes a remote temporary run directory; unlike the other write surfaces it has no dry-run plan mode. | Consider a plan-only mode that shows copy/run/cleanup steps without contacting a bench. | S |
| 3 | `nova-ci mystery` | The refusal lists valid verbs and help but does not suggest a close verb, unlike near-miss flag refusals. | Consider a nearest-verb hint for close typos while retaining the full verb list. | S |

## Top 3 fixes
1. Add `per_cpu` to `slowtests --json` facts so the JSON result preserves the load measurement already printed in text.
2. Consider operation-key deduplication for receipt writes where consumers need exactly-once rows.
3. Consider a plan-only `bench run` mode that makes its remote steps inspectable before execution.

## Good, keep
`slowtests --example` is a self-contained first run from the binary's help. All four tested refusals named their cause and a runnable help command; `slowtests` and `functional` JSON probes returned parseable results, and the dry-run paths for `local`, `new-rule`, `new-verb`, and `github receipt` left the checkout and store untouched. The exact-source build for `b4f484c84916` passed on Linux, as did `go test -count=1 -timeout 600s ./internal/ci/ ./internal/docs/`; a real `go test -json -count=1 -timeout 600s ./cmd/nova-ci` stream through `nova-ci slowtests` exited 0. The functional test selection emitted by `nova-ci functional ./cmd/nova-ci` also ran and exited 0. The Darwin binary built from the same source matched the installed binary's help and safe-probe output in all 34 comparisons. The old functional package-path defect is fixed: `functional ./cmd/nova-ci` now prints `./cmd/nova-ci/`, and the `docs/CLI.md` nova-ci section now has a `### First run` section. After recording the cold score, I inspected `cmd/nova-ci/receipt.go:51-52`: its comment says repeat receipts are acceptable wake hints; the help itself only says a refused write is tried once.

## What could not be tried
`nova-ci github receipt` without `--dry-run` would write to a store; `nova-ci bench run` would write a temporary directory on a bench; and non-dry-run `local`, `new-rule`, and `new-verb` can write or execute. Those write paths were not run. The required GitHub receipt and bench service paths were judged from help and safe dry-run/refusal probes only.
