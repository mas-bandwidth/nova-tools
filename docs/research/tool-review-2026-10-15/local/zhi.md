# nova-local AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: c0b5300f55cd
Score: 8.5/10 (46/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-local` -> `LOCAL REFUSED: no verb given; the verbs are status, serve, worker, version; run: nova-local help` |
| 2 | refusals | 5 | `nova-local serve --engine ollama` -> `SERVE REFUSED: --model is required; ... refusing to guess; run: nova-local help`; `nova-local status --bogus x` -> `STATUS REFUSED: unknown flag --bogus; the flags of status are --base, --engine, --json, --list, --max, --timeout; run: nova-local status -h`; `nova-local frobnicate` -> `LOCAL REFUSED: unknown verb "frobnicate"; the verbs are status, serve, worker, version; run: nova-local help`; `nova-local status --timeout mustard` -> `STATUS REFUSED: invalid value for --timeout: it wants a duration such as 30s or 5m ...; run: nova-local status -h`; all four name cause and next command |
| 3 | output | 5 | `nova-local status --json` -> `{"result":{"verb":"status","status":"failed","exit":1,"remedy":"ollama serve","why":["no engine answers (1 asked); start one, such as the ollama daemon"]},"facts":{}}` |
| 4 | dry-run | 2 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7 --dry-run` -> `SERVE FAILED: no engine answers; ...` then `SERVE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written`; `nova-local worker ... --dry-run` -> `WORKER FAILED: the engine's models could not be read: ...` then the same NOTE; only `serve --stop --dry-run` works (`SERVE OK stopped=planned engine=ollama model=gemma4-32k dry_run=true`) |
| 5 | idempotent | 1 | `nova-local help` -> no `--op` flag on any write verb (serve, worker); the same op cannot be retried safely |
| 6 | no prompts | 5 | `nova-local status` -> `STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve` (immediate exit, no interactive wait) |
| 7 | exit codes | 5 | `nova-local status` -> exit 1; `nova-local serve --engine ollama` -> exit 2; `nova-local version` -> exit 0; all match the banner's 0/1/2 table |
| 8 | one word | 5 | verbs status, serve, worker, version are distinct; flags (`--engine`, `--model`, `--base`, `--json`, `--dry-run`, `--max`) mean the same across verbs |
| 9 | push | 5 | n/a: status is a one-shot read; serve delivers a single model load/unload request, and nothing is polled |
| 10 | no leaks | 5 | `--key-file` is stat'ed, never opened (help states it); no output line leaks a secret across all runs including `--json` |
| 11 | true help | 3 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7 --dry-run` -> `SERVE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` contradicts help's `--dry-run print what the verb would write and write nothing` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7 --dry-run` | `SERVE FAILED: no engine answers; ...` then `SERVE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` — the dry-run cannot print a plan without a live engine, and the note admits the verb never honored `--dry-run`, so the `writes nothing` guarantee is unbacked | have serve and worker consult `Call.DryRun` and print the plan from flags alone, demanding the engine only for the steps that truly need it; drop the `it may have written` note | M |
| 2 | `nova-local worker --engine ollama ... --dry-run` | `WORKER FAILED: the engine's models could not be read: Get "http://127.0.0.1:11434/api/tags": ... connection refused` then `WORKER NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` — a new user cannot preview the JSON description it would write without first running an engine | under `--dry-run` with an unreachable engine, still print the JSON that would be written from flags | S |
| 3 | `nova-local serve` / `nova-local worker` | no `--op` flag on any write verb (help lists none), so the same op has no idempotency guard and a timeout retry can overwrite or duplicate | add `--op` to serve and worker per the shared skeleton, returning the recorded result on replay | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-local help` | the banner shows line output but no `--json` example; a cold reader cannot see the JSON shape | add one `--json` example line to the banner | S |
| 2 | `nova-local serve -h` | `--num-ctx` is described as `required to serve` but lacks the `(required)` marker the other required flags carry, so a cold reader may not notice it is mandatory | print it as `(required to serve)` consistently with the other `(required)` flags | S |

## Top 3 fixes
1. Make `--dry-run` on serve and worker print the real plan from flags (honoring `Call.DryRun`) and never warn `it may have written`; drop the live-engine requirement for the plan.
2. Add `--op <id>` to serve and worker so a write is idempotent and a timeout retry is safe.
3. Let worker `--dry-run` show the JSON it would write even when the engine is down, so a first run can preview without infrastructure.

## Good, keep
- Every refusal names the cause, what the flag wants, and the exact next command in one line (`run: nova-local help`, `run: nova-local status -h`); all four refusal classes behaved this way, including `--json`.
- The banner's exit-code table is truthful and matched by behavior (0 done / 1 ran-and-said-no / 2 could-not-run).
- `--json` is universal and consistent with the line output, both from the same `result{verb,status,exit,remedy,why}` value.
- The `--key-file` is stat'ed and never opened, so a path name never leaks a key's contents.

## What could not be tried
- `status` success path and `status --list` MODEL lines: need a live ollama engine answering on the health path (none here; NOVA_TEST_NO_HOST).
- `serve` real run and `worker` real write (local write): need a live engine with pulled weights; judged from help and `--dry-run`.
- No pre-existing 2026-10-02 rating for nova-local exists under `docs/ratings/`, so no old defect could be compared.
