# nova-decide AI-usability review, 2026-10-15

Reviewer: Mercury
Build: d7e47f037ea8
Score: 9.5/10 (52/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-decide example --dir ./decide-example` -> `EXAMPLE OK files=4`; then `nova-decide ask --schema ./decide-example/schema.json --state ./decide-example/state.txt --backend fixed --answers ./decide-example/answers.json --record ./decisions.jsonl --op first` -> `ASK OK id=first ... recorded=new` |
| 2 | refusals | 5 | `nova-decide ask --state ./decide-example/state.txt --backend fixed --record ./decisions.jsonl` -> `ASK REFUSED: --schema is required; ... run: nova-decide help`; unknown flag -> `ASK REFUSED: unknown flag --bogus; ... run: nova-decide ask -h`; unknown verb -> `DECIDE REFUSED: unknown verb "frobnicate"; ... run: nova-decide help`; bad value -> `ASK REFUSED: --backend "banana" is no backend; ... run: nova-decide help` |
| 3 | output | 5 | `nova-decide read ... --json` -> `{"result":{"verb":"read","status":"ok","exit":0},"facts":{...},"items":[...]}`; the same value as the typed `READ OK`/`READ ANSWER` lines |
| 4 | dry-run | 5 | `nova-decide ask ... --dry-run` -> `ASK OK id=dry1 ... recorded=no dry_run=true`; `ls ./drytst.jsonl` -> no such file, so nothing written; `gate --dry-run` writes nothing |
| 5 | idempotent | 4 | with `--op`, the same op again -> `ASK OK id=first ... recorded=existing`; but without `--op` the same ask run twice records two decisions (`id=example-a25130544eaa` then `id=example-8e95898c7ee4`, two lines) |
| 6 | no prompts | 5 | every probe returned immediately with no input waited on; `nova-decide read ...` -> `READ OK` with no interactive exchange |
| 7 | exit codes | 5 | help states 0 done / 1 conflict / 2 could not run; observed 0 on success (`ASK OK`, exit 0), 1 on `OUTCOME FAILED ... an outcome is attached once` (exit 1), 2 on every bad invocation (exit 2) |
| 8 | one word | 5 | help lists a distinct verb per noun (example, ask, read, score, attempt, grade, gate, brief, outcome, calibrate, import, score-grades, findings, version); no duplicate verb or tool |
| 9 | push | 5 | n/a: the tool reads and writes local record files synchronously; no state is waited on, so nothing to push or poll |
| 10 | no leaks | 5 | none of the outputs I ran leaked a secret; the key path is refused by design with `reason=key_absent`, and the key is only passed via `nova-secrets exec` |
| 11 | true help | 3 | the banner claims `Every verb takes --json` and `A verb that lists takes --max`; `nova-decide help --json` is `unknown verb "--json"` exit 2, `nova-decide help ask --json` silently ignores the flag, and `findings --max 1` -> `FINDINGS REFUSED: unknown flag --max; ... run: nova-decide findings -h` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide help --json`, `nova-decide help ask --json`, `nova-decide findings --max 1` | the banner promises every verb takes `--json` and every listing verb takes `--max`; `help --json` is refused as `unknown verb "--json"` exit 2, `help ask --json` silently ignores the flag, and `findings --max 1` -> `FINDINGS REFUSED: unknown flag --max` | accept `--json` on help (or refuse an unknown flag after the verb), and give findings `--max` with a MORE line | S |
| 2 | `nova-decide ask ...` twice with no `--op` | the same ask a second apart records two decisions (`id=example-a25130544eaa` then `id=example-8e95898c7ee4`, two lines in the record), so a plain retry after a timeout is not safe | hash the schema and the state only for the default id, as gate's default op already does, and say in help that a repeat returns the recorded decision | S |
| 3 | `nova-decide import --record ./imp.jsonl --verdicts 'nomatch/*'` | `IMPORT OK verdict_new=0 ... unanswered=0`, exit 0, for a glob that matches nothing: a mistyped source imports nothing silently | refuse a source glob that matches no file (exit 2), or print `matched=0` per source | S |
| 4 | `nova-decide outcome --record ./decisions.jsonl --id card-1 --label bad` | `OUTCOME FAILED id=card-1: decision card-1 is labelled ok already ..., an outcome is attached once`, exit 1, with no `run:` next command | add a remedy line: label another decision, or keep the recorded label | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | missing-flag and bad-value refusals | they end `run: nova-decide help`, the whole banner, where the verb's flags live in `nova-decide <verb> -h` | point the default remedy at `nova-decide <verb> -h` | S |
| 2 | `nova-decide help brief` | the minutes question names no options (`under-10`, `10-20`, `20-45`, `45-90`, `90-180`, `over-180`) | list the options in help and in every `not one of its options` refusal | S |
| 3 | `nova-decide help` "how it works" | the top help still uses a shorthand pseudo-run (`state R?`, `print ASK OK id=f ...`) instead of a line the tool prints | quote a real run's OK and ANSWER lines | S |
| 4 | `nova-decide help` "jev or fixed answers" | jev is never glossed in the top help; a cold reader learns it is a hosted model needing `JEV_API_KEY` only from the key refusal | one clause: jev is the hosted model, called with `JEV_API_KEY` under `nova-secrets exec`; fixed is a local answers file | S |
| 5 | `nova-decide example --dir` | it writes only `schema.json`, `state.txt`, `answers.json`, `sample.diff`; the remaining CLI.md first-run lines hard-code `./cmd/nova-decide/testdata/` paths, so a user outside a checkout cannot paste the whole walkthrough from the generated directory | emit the remaining fixtures (`card.md`, `card.diff`, `result.md`, `greet.md`, `gate-output.txt`, a small `record.jsonl`) from `example` | M |

## Top 3 fixes
1. Make the help true: accept `--json` on help and give findings `--max` (blocker 1) — the largest single cost, criterion 11.
2. Make the default id a function of schema and state only (blocker 2), so a plain retry is idempotent.
3. Refuse an import source glob that matches nothing (blocker 3) so a mistyped source never imports silently.

## Good, keep
- The refusal grammar is exemplary: every refusal names the cause and the exact next command in the same line, and a run reports every independent problem at once (missing `--schema` and the fixed-backend `--answers` note together); the unknown-flag refusal lists the verb's own flags and its `-h`.
- One result value with two renderings: the typed `... OK`/`... ANSWER` lines and `--json` are the same value, so they cannot drift; `--dry-run` shares the real code path (`recorded=no dry_run=true`), and `--op` idempotency is exact (`recorded=existing`).
- The fixed backend makes the whole lifecycle runnable with no network and no key, and the jev key refusal names the one command that supplies it.

## What could not be tried
- `--backend jev`: needs `JEV_API_KEY` and a reachable provider; judged from help and the documented `reason=key_absent` refusal, not run.
- `import` with real sources and `score-grades`: judged from help and `import --dry-run` (which refuses with a named door when no source is given), not run end to end.
