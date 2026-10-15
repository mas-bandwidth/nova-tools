# nova-ci AI-usability review, 2026-10-15

Reviewer: Mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.0/10 (50/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-ci slowtests --example --budget 60 --load 4 --cpus 16` -> `CI-SLOW package=github.com/mas-bandwidth/nova-tools/internal/example seconds=65.1s budget=60s` |
| 2 | refusals | 5 | `nova-ci slowtests --bogus` -> `nova-ci slowtests REFUSED: unknown flag --bogus; ... run: nova-ci slowtests -h`; the missing-flag, unknown-verb and bad-value refusals also name cause and next command |
| 3 | output | 5 | `nova-ci slowtests --example --json` -> `{"result":{...},"facts":{...},"items":[...]}`; lines are a stable `KEY=value` grammar |
| 4 | dry-run | 4 | `nova-ci new-rule --dry-run mycoolrule` -> `nova-ci new-rule NOTE --dry-run wrote nothing; run it again without --dry-run to write these 3 files`; bench run has no --dry-run |
| 5 | idempotent | 1 | `nova-ci github receipt -h` -> flags list has no `--op`; no write verb takes --op, so a retry after a timeout is not provably safe |
| 6 | no prompts | 5 | every command in this review ran to completion with no interactive input; `nova-ci help` exits 0 |
| 7 | exit codes | 5 | `printf '' | nova-ci slowtests` -> exit 1 on `CI-SLOW FAILED`; `nova-ci bogusverb` -> exit 2, as help states |
| 8 | one word | 5 | `nova-ci help` lists slowtests, functional, local, new-rule, new-verb, bench run, github receipt, version, help; no duplicate verbs |
| 9 | push | 5 | `n/a: no verb waits on or polls state; github receipt pushes one row and nothing is polled` |
| 10 | no leaks | 5 | `nova-ci github receipt --dry-run ...` -> `CI RECEIPT ... pr=- ev=-`; the password comes from an env var, never the line |
| 11 | true help | 5 | `nova-ci functional ./pkg/oneline` -> `CI FUNCTIONAL OK packages=0 reason=no-functional-tag-in-1-dirs`, matching the help's stated contract |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci github receipt -h` | no write verb takes `--op`; a timed-out delivery retried will double-write a receipt or re-run a scaffold, so a retry is not provably safe (rubric 5) | add `--op <id>` idempotency to the write verbs (github receipt, bench run, new-rule, new-verb): the same id, verb and args return the recorded result and change nothing | M |
| 2 | `nova-ci bench run -h` | bench run is a delivery and has no `--dry-run`, so a caller cannot preview what will run on the bench before it happens (rubric 4) | add `--dry-run` that prints the copy, the command and the run directory and writes nothing | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci functional --json ./pkg/oneline` | prints `{"packages":[],"pattern":""}` with no result/facts envelope, unlike slowtests' `{result,facts,items}` JSON | emit the same result/facts/items envelope from the shared value | S |
| 2 | `printf '' | nova-ci slowtests` | `CI-LOAD load=unknown ...` prints the sysctl exec error inline when load cannot be read; graceful but noisy on a first run | downgrade the reason to a NOTE, or silently fall back to `load=-` | S |
| 3 | `cmd/nova-ci/timing.go` | the second, unbuilt `package main` (the leftover-script defect earlier ratings named) is still in the command's directory, reached by no verb and named in no help | move it under `tools/` with a header saying what runs it, or wire it in as a verb | M |

## Top 3 fixes
1. Add `--op` idempotency to the write verbs; moves rubric 5 from 1 to 5 (the biggest single point gain).
2. Add `--dry-run` to bench run; moves rubric 4 to 5.
3. Unify the `--json` shape across verbs (functional envelope); removes the one parse-shape surprise.

## Good, keep
The refusal grammar is exactly the standard: one line naming the cause, every problem at once, and the next command to run (`run: nova-ci slowtests -h`), always with the matching exit code. `slowtests --example` gives a zero-setup first run, and the pipeline over real `go test -json` output works end to end as the help describes. The write verbs guard against duplicate scaffolds (exit 2 when a file is already there) and all but bench run carry a working `--dry-run` that writes nothing.

Old defects checked against the earlier ratings: the five-output-dialects gap is fixed (every verb now speaks `key=value` and `--json`); the leftover `timing.go` script is still present (nice-to-have 3).

## What could not be tried
- bench run: a delivery that needs a reachable Linux bench host; judged from help and `-h` only.
- github receipt (real write): would post to a live store; only `--dry-run` was run.
- local (full run): would run the whole repository unit tier over ~145 packages; only `--dry-run` was run within the wall.
