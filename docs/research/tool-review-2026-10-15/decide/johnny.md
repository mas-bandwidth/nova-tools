# nova-decide AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 10/10 (54/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-decide help` -> `usage: nova-decide example --dir <new-directory>` |
| 2 | refusals | 5 | `nova-decide ask --nope` -> `ASK REFUSED: unknown flag --nope; ...; run: nova-decide ask -h` |
| 3 | output | 5 | `nova-decide ask ... --json` -> `{"result":{"verb":"ask","status":"ok","exit":0},...}` |
| 4 | dry-run | 5 | `nova-decide example --dir ./ex2 --dry-run` -> `EXAMPLE NOTE dry run: no example files written` |
| 5 | idempotent | 5 | `nova-decide ask ... --op twice1` twice -> second prints `recorded=existing` |
| 6 | no prompts | 5 | `nova-decide version` -> `nova-decide v1.2.26-...` with no interaction |
| 7 | exit codes | 5 | `nova-decide outcome --id read1 --label fail` -> exit `1`, `OUTCOME FAILED id=read1: ...` |
| 8 | one word | 5 | `nova-decide help` lists all verbs as single tokens: `example, ask, read, score, ...` |
| 9 | push | 5 | n/a: nova-decide is a decision recorder; it appends to a record file, never polls a service |
| 10 | no leaks | 5 | `nova-decide ask --nope --json` -> refusal JSON carries only flags and remedy, no secrets |
| 11 | true help | 4 | banner says `Every verb takes --json`, but `nova-decide help --json` -> `unknown verb "--json"`, exit 2 |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide help --json` and `nova-decide findings --max 1` | the banner promises `Every verb takes --json` and `A verb that lists takes --max`; `help --json` is `unknown verb "--json"` exit 2, and `findings --max 1` is `unknown flag --max; ... run: nova-decide findings -h` | accept `--json` on help and `--max` on findings, or qualify the banner's promise | S |
| 2 | `nova-decide findings --record missing.jsonl` | a named but absent record file is read as empty: `FINDINGS OK scored=0 classes=0` exit 0, silently masking a typo | refuse a named record that does not exist in the reading verbs | S |
| 3 | `nova-decide read --card ... --diff empty.diff` | an empty diff is `READ OK verdict=LAND` and recorded, where ask refuses an empty state | refuse an empty diff in read, score and gate as ask refuses an empty state | S |
| 4 | `nova-decide help` example lines | every example uses repo-relative paths like `./cmd/nova-decide/testdata/schema.json`, so the commands fail from any other cwd with `ASK REFUSED: open ./cmd/nova-decide/testdata/schema.json: no such file or directory` | state plainly the examples assume the checkout root as cwd, or use absolute/`$REPO` paths | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide findings` default `--bar` | the help does not state the default bar of 0.5 the run applies (`bar=0.5`) | document the default in the help for `--bar` | S |
| 2 | `nova-decide calibrate` default `--bars` | runs with no `--bars` report bars 0.5, 0.7, 0.9 that help never names | state the default bars in the `-h` flag list | S |

## Top 3 fixes
1. Make `help` accept `--json` and give `findings` a `--max` that the banner already promises.
2. Refuse a named but absent record file in the reading verbs (findings, calibrate, score-grades).
3. Refuse an empty diff in read/score/gate, as ask refuses an empty state.

## Good, keep
Refusals are exemplary: every one names the cause and the exact next command, including `did you mean --op?`, and `--json` versions carry a structured `remedy` field. The `--op` idempotency is solid: re-running the same op returns `recorded=existing` and changes nothing. Output is consistently a stable `KEY=value` line grammar with a matching `--json` object, and exit codes match the documented 0/1/2 contract exactly. The older defect where the default id folded the clock in (same ask twice without `--op` recording twice) is GONE: two identical no-`--op` asks now share an id and the second returns `recorded=existing` (rec line count unchanged).

## What could not be tried
- `score-grades`: requires a `nova-sprint log --json` export as `--log`; without one it refuses with a clear message, and building a real sprint log was outside this card's scope.
- `import --judgments`/`--reports`: need a judgment directory plus a sprint log, or REPORT.md globs not present in this checkout.
- `import --verdicts` with matches: ran with an empty glob; both new and existing counts correctly returned 0.
- The `jev` backend (a model-host service) was not exercised; all runs used the `fixed` backend as the card requires no call that hits a model or spends a key.
