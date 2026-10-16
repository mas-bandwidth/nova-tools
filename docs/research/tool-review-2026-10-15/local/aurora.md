# nova-local AI-usability review, 2026-10-15

Reviewer: x-ai/grok-build-0.1
Build: 4540d7fd37ca
Score: 8.5/10 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-local help` -> `nova-local: run local models: what an engine has, one model served at a chosen context, and a worker description nova-swarm accepts` |
| 2 | refusals | 5 | `nova-local serve --engine ollama` -> `SERVE REFUSED: --model is required; it wants the model: the reference to serve (gemma4:12b), or with --stop the served tag; refusing to guess; run: nova-local help` |
| 3 | output | 5 | `nova-local status --json` -> `{"result":{"verb":"status","status":"failed","exit":1,"remedy":"ollama serve","why":["no engine answers (1 asked); start one, such as the ollama daemon"]}}` |
| 4 | dry-run | 4 | `nova-local serve --stop --engine ollama --model gemma4-32k --dry-run` -> `SERVE OK stopped=planned engine=ollama model=gemma4-32k dry_run=true` |
| 5 | idempotent | 1 | `nova-local worker -h` -> no --op flag listed for write verbs |
| 6 | no prompts | 5 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 1024` -> `SERVE REFUSED: --model is required; it wants the model: the reference to serve (gemma4:12b), or with --stop the served tag; refusing to guess; run: nova-local help` (exits, no stdin) |
| 7 | exit codes | 5 | `nova-local status` -> exit 1; `nova-local serve` (missing required) -> exit 2; `nova-local version` -> exit 0 (match banner table) |
| 8 | one word | 5 | `nova-local help` -> `the verbs are status, serve, worker, version` |
| 9 | push | 5 | `n/a: no verb waits on or polls changing state; status/engines are one-shot inspect, serve instructs engine` |
| 10 | no leaks | 5 | `nova-local worker --key-file /abs/path --dry-run` (failed run) -> key path shown in command; no content or secret value in any output line |
| 11 | true help | 3 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --dry-run` -> `SERVE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` contradicts `--dry-run print what the verb would write and write nothing` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --dry-run` | `SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve` then `SERVE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` | make serve/worker honor --dry-run from Call.DryRun before dialing engine; emit plan/result from flags | M |
| 2 | `nova-local serve -h` | no `--op` on serve or worker | add --op per pkg/tool skeleton so same op on write is no-op replay | M |
| 3 | `nova-local help` | example: `nova-local worker ... --worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key ...` | replace internal testdata paths with a minimal self-contained setup a checkout-less user can follow (or add note) | S |
| 4 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --dry-run` | NOTE claims "it may have written" | remove the may-have-written note; dry-run must never write | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-local help` | banner shows example but no --json line | add `nova-local version --json` (or status) to the example block | S |
| 2 | status output | STATUS lines stable and rich | keep | - |

## Top 3 fixes
1. Honor --dry-run before engine dials for serve and worker (lifts dry-run and true-help)
2. Add --op to serve and worker (lifts idempotent 1->5)
3. Make the worker example in help use paths or instructions a new user can satisfy without source checkout

## Good, keep
Every refusal names the cause, what the flag wants, and the next command; all problems reported together.
--json is universal and consistent with the line output.
Remedies are actionable commands; exit codes match the documented table.
(The same defects as noted in the sibling review for this round were still present; no 2026-10-02 local rating file existed to compare.)

## What could not be tried
real (non-dry) `nova-local serve` (except --stop) and `nova-local worker`: both read the engine's model list first
successful `nova-local status` (with ENGINE/MODEL lines) and `--list`: requires an answering engine
any path that exercises tag creation, warm-up, digest check, load thresholds, or --max truncation on lists
