# nova-local AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9/10 (49/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-local` -> `LOCAL REFUSED: no verb given; the verbs are status, serve, worker, version; run: nova-local help` |
| 2 | refusals | 5 | `nova-local serve --engine ollama --num-ctx 1024` -> `SERVE REFUSED: --model is required; it wants the model: the reference to serve (gemma4:12b), or with --stop the served tag; refusing to guess; run: nova-local help` |
| 3 | output | 5 | `nova-local version --json` -> `{"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-local v1.2.26-0.20261015145010-cc2f2f8c8148 linux/amd64 go1.26.6"}` |
| 4 | dry-run | 5 | `nova-local serve --stop --engine ollama --model gemma4-32k --dry-run` -> `SERVE OK stopped=planned engine=ollama model=gemma4-32k dry_run=true` |
| 5 | idempotent | 1 | `nova-local help` -> no `--op` flag on any write verb (serve, worker); the same op cannot be retried safely |
| 6 | no prompts | 5 | `nova-local status` -> `STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve` (no interactive wait) |
| 7 | exit codes | 5 | `nova-local status` -> exit 1; `nova-local serve` (missing flags) -> exit 2; `nova-local version` -> exit 0; all match the banner's table |
| 8 | one word | 5 | verbs status, serve, worker, version are distinct; flags (`--engine`, `--model`, `--json`, `--dry-run`, `--base`, `--max`) mean the same across verbs |
| 9 | push | 5 | n/a: status is a one-shot read; serve delivers a model load/unload (a single request), nothing is polled |
| 10 | no leaks | 5 | `--key-file` is stat'ed never opened; no output line leaks a secret across all runs |
| 11 | true help | 3 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7 --dry-run` -> `SERVE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` contradicts help's `--dry-run print what the verb would write and write nothing` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7 --dry-run` | `SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve` then `SERVE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` — the dry-run cannot print a plan without a live engine, and the note says the verb never honored `--dry-run`, so the `writes nothing` guarantee is unbacked | have serve/worker consult `Verb.DryRun` and print the plan from flags, demanding the engine only for the parts that truly need it | M |
| 2 | `nova-local worker` / `nova-local serve` | no `--op` flag on any write verb (help lists none), so the same op has no idempotency guard and a retry can overwrite or duplicate | add `--op` to serve and worker per the shared skeleton, returning the recorded result on replay | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-local help` | the banner shows line output but no `--json` example; a cold reader cannot see the JSON shape | add one `--json` example line to the banner | S |
| 2 | `nova-local worker --dry-run` | worker dry-run fails at the engine dial before any plan is shown, so a new user cannot preview the description it would write | when `--dry-run` and the engine is unreachable, still print the JSON that would be written from flags | S |

## Top 3 fixes
1. Make `--dry-run` on serve/worker print the real plan from flags (honoring `Call.DryRun`) and never warn `it may have written`; drop the live-engine requirement for the plan.
2. Add `--op <id>` to serve and worker so a write is idempotent and a timeout retry is safe.
3. Let worker `--dry-run` show the JSON it would write even when the engine is down, so a first run can preview without infrastructure.

## Good, keep
- Every refusal names the cause, what the flag wants, and the exact next command in one line (`run: nova-local help`, `run: nova-local status -h`); all four refusal classes behaved this way.
- The banner's exit-code table is truthful and matched by behavior (0 done / 1 ran-and-said-no / 2 could-not-run).
- `--json` is universal and consistent with the line output, both from the same styledvalue (`result{verb,status,exit,remedy}`).
- The `docs/CLI.md` `### First run` transcript and its `what a first run gets wrong` list are accurate and genuinely useful.

## What could not be tried
- `status` success path, `status --list` model lines: need a live ollama engine answering on the health path (none here; NOVA_TEST_NO_HOST).
- `serve` real run and `worker` real write (local write): need a live engine with pulled weights; judged from help and `--dry-run`.
- No pre-existing 2026-10-02 rating for nova-local exists under `docs/ratings/`, so no old defect could be compared.
