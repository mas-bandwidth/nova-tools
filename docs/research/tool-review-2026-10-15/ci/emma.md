# nova-ci AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: b4f484c84916
Score: 9.0/10 (49/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-ci slowtests --example --budget 120 --load 4 --cpus 16` -> `CI-SLOW OK packages=2 slowest=github.com/mas-bandwidth/nova-tools/internal/example:65.1s` |
| 2 | refusals | 4 | `nova-ci bench run` -> `BENCH-RUN REFUSED: --host is required; it wants the bench the command runs on, a host ssh reaches; refusing to guess; run: nova-ci help` |
| 3 | output | 3 | `nova-ci version --json` -> `nova-ci version REFUSED: takes no flags and no arguments, got 1; run: nova-ci version -h` |
| 4 | dry-run | 4 | `nova-ci github receipt --from-runner --repo owner/name --sha 0123456789abcdef0123456789abcdef01234567 --run-id 42 --workflow ci --conclusion success --dry-run` -> `CI RECEIPT owner/name sha=0123456789abcdef0123456789abcdef01234567 run=42 workflow=ci conclusion=success pr=- ev=-` |
| 5 | idempotent | 4 | `nova-ci github receipt --op op1 --dry-run` -> `nova-ci github receipt REFUSED: unknown flag --op; the flags of github receipt are --at, --conclusion, --dry-run, --from-runner, --pr, --redis, --repo, --run-id, --sha, --workflow; run: nova-ci github receipt -h` |
| 6 | no prompts | 5 | `nova-ci slowtests --budget -5` -> `nova-ci slowtests REFUSED: --budget must be a whole number of seconds greater than zero (got -5); run: nova-ci slowtests -h` |
| 7 | exit codes | 5 | `nova-ci bogusverb` -> `nova-ci REFUSED: unknown verb "bogusverb"; the verbs are slowtests, functional, local, new-rule, new-verb, bench run, github receipt, version, help; run: nova-ci help` |
| 8 | one word | 5 | `nova-ci version` -> `nova-ci v1.2.25-0.20261015164621-b4f484c84916 linux/amd64 go1.26.6` |
| 9 | push | 5 | `n/a: CI runner and test stream analyzer with no polling loops or state watches` |
| 10 | no leaks | 5 | `nova-ci github receipt -h` -> `--redis <string>  the store's host:port (default NOVA_REDIS_ADDR); dialled as the seat in NOVA_SPRINT_REDIS_USER` |
| 11 | true help | 4 | `nova-ci bench run -h` -> `exit codes: 0 done, 1 the verb said no (slowtests, local, github receipt), 2 usage or could not run; by verb:` (followed by the entire 130-line top-level banner) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci version --json`, `nova-ci local --json`, `nova-ci new-rule --json`, `nova-ci new-verb --json`, `nova-ci github receipt --json` | Only `slowtests` and `functional` accept `--json`; every other verb refuses `--json` with exit code 2 (`nova-ci version REFUSED: takes no flags and no arguments, got 1`) | Add `--json` support across `local`, `new-rule`, `new-verb`, `github receipt`, and `version` using the standard output envelope | M |
| 2 | `nova-ci bench run -h` and `nova-ci bench -h` | Both help pages print the entire 130-line top-level usage banner inside their `exit codes:` block (`133` lines total for `bench run -h`) | Pass only `bench run`'s single exit-code summary to `ExitTable` instead of the full multi-line tool banner | S |
| 3 | `nova-ci bench run` | Missing-flag refusals emit multiple `BENCH-RUN REFUSED:` lines pointing to `run: nova-ci help` instead of the tool's single-line `nova-ci bench run REFUSED: ...; run: nova-ci bench run -h` grammar | Route `bench run` and `bench` group refusals through `nova-ci`'s single-line refusal formatter pointing to `nova-ci bench run -h` | S |
| 4 | `nova-ci bench run --help` | `bench run` copies a directory tree and runs a remote command over SSH but offers no `--dry-run` flag to inspect the target directory and command without connecting | Add `--dry-run` to `nova-ci bench run` so an AI can verify the rsync and remote execution plan offline | S |
| 5 | `nova-ci slowtests --example --budget 60 --load 4 --cpus 16 --json` | Text output prints `CI-LOAD load=4.00 cpus=16 per-cpu=0.25: measured, not a verdict`, but `--json` `facts` includes `load` and `cpus` while omitting `per_cpu` | Include `per_cpu` in `slowtests --json` `facts` so JSON and line output carry identical fields | S |
| 6 | `nova-ci --help` | The banner says `slowtests reads the events of the packages you name`, but `slowtests` takes no package arguments on its command line and reads `go test -json` events from stdin | Reword the banner line to state that `slowtests` reads the `go test -json` event stream on stdin for the packages passed to `go test` | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci functional ./...` | Across the whole repository, line 2 is a 27,306-character `-run` alternation regex (`689` test names) with no `--max` cap | Add an optional `--max` or summary count when inspecting large package trees | S |
| 2 | `nova-ci github receipt` | `github receipt` keys writes by `--repo`, `--sha`, `--run-id`, and `--workflow` but does not accept an explicit `--op` token | Accept `--op` on `github receipt` for uniform idempotence flag coverage across write verbs | S |

## Top 3 fixes
1. Add `--json` output envelope support to `local`, `new-rule`, `new-verb`, `github receipt`, and `version`.
2. Fix `nova-ci bench run -h` and `nova-ci bench -h` so `ExitTable` prints only the `bench run` exit-code summary instead of repeating the 130-line top-level banner, and unify `bench run` refusals under `nova-ci bench run REFUSED: ...; run: nova-ci bench run -h`.
3. Add `--dry-run` to `nova-ci bench run` and include `per_cpu` in `nova-ci slowtests --json` `facts`.

## Good, keep
- `nova-ci slowtests --example` runs cold with zero setup and prints an exact copy-pasteable `--enforce` command in both line and `--json` output when a budget is exceeded.
- `nova-ci functional` now supports `--json` (`{"packages":[...],"pattern":"..."}`), closing the earlier 1.2.0 gap on `functional --json`, and prints `./`-prefixed package paths ready for `go test`.
- `nova-ci github receipt` names all seven missing required flags and the store requirement in a single refusal line, and its `--dry-run` validates every field offline without dialing Redis.

## What could not be tried
- `nova-ci github receipt` without `--dry-run`: writes a CI receipt to a live Redis store (`ev:github`), which is forbidden by card rules; judged from `--dry-run` and `-h`.
- `nova-ci bench run`: copies a tree over SSH and runs a command on a remote bench; judged from `bench run -h` and its refusal output.
