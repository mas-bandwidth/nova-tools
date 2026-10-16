# nova-decide AI-usability review, 2026-10-15

Reviewer: nemotron-3-ultra
Build: 4540d7fd37ca
Score: 9.5/10 (52/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-decide help` -> its `example:` block references `./cmd/nova-decide/testdata/...` paths that exist only in a source checkout; a cold user who ran `nova-decide example --dir ./ex` cannot run the rest from the generated directory |
| 2 | refusals | 5 | `nova-decide ask --state ./state.txt --backend fixed --answers ./ans.json --record ./d.jsonl` -> `ASK REFUSED: --schema is required; it wants the decision's schema, a JSON file; refusing to guess; run: nova-decide help` (all four missing flags named at once) |
| 3 | output | 5 | `nova-decide read --card ... --diff ... --backend fixed --answers ... --record ./d.jsonl --op t --json` -> `{"result":{"verb":"read","status":"ok","exit":0},"facts":{...},"items":[...]}` (one value, line and JSON renderings consistent) |
| 4 | dry-run | 5 | `nova-decide example --dir ./ex2 --dry-run` -> `EXAMPLE OK files=4 dry_run=true` and no directory written; `nova-decide outcome --record ./d.jsonl --id card-1 --label test --dry-run` -> `OUTCOME OK id=card-1 label=test recorded=no dry_run=true` |
| 5 | idempotent | 5 | `nova-decide read --card ... --diff ... --backend fixed --answers ... --record ./d.jsonl --op card-1` run twice -> second prints `READ OK id=card-1 ... recorded=existing` and changes nothing |
| 6 | no prompts | 5 | `nova-decide ask --backend fixed --record ./d.jsonl` (no --answers) -> `ASK REFUSED: --backend fixed answers from --answers <file>` without waiting |
| 7 | exit codes | 5 | `nova-decide outcome --record ./d.jsonl --id card-1 --label wrong` after `ok` -> exit 1 `OUTCOME FAILED ...`; refusals exit 2; successes exit 0, as the help's table states |
| 8 | one word | 5 | 14 verbs (`example, ask, read, score, attempt, grade, gate, brief, outcome, calibrate, import, score-grades, findings, version`), all single words, no duplicates, each distinct from every other tool verb |
| 9 | push | 5 | n/a: the tool records decisions to a file and waits on no external state, so there is nothing to poll |
| 10 | no leaks | 5 | no command or error output in the probes printed an environment secret; the key is read only from the environment (`--backend jev` with no key refuses with `reason=key_absent`) |
| 11 | true help | 3 | `nova-decide help --json` -> exit 2 `unknown verb "--json"` while the banner's appendix claims "Every verb takes --json"; `nova-decide findings --max 1` -> `FINDINGS REFUSED: unknown flag --max` while the banner says "A verb that lists takes --max"; `help ask --json` silently ignores the flag |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide help --json` | exit 2 `unknown verb "--json"` while the banner's appendix claims "Every verb takes --json"; `nova-decide help ask --json` silently prints the help and ignores the flag | accept `--json` on help (or refuse it after the verb consistently) | S |
| 2 | `nova-decide findings --max 1` | `FINDINGS REFUSED: unknown flag --max` while the banner says "A verb that lists takes --max"; findings lists FINDING lines | give findings `--max` and a `MORE shown= total=` line | S |
| 3 | `nova-decide help` example block | the ask/read/score/attempt/grade/gate example lines reference `./cmd/nova-decide/testdata/...`, which exist only inside a source checkout; a cold user who ran `nova-decide example --dir` cannot run the rest from the generated directory | make the example lines reference the directory `example` creates, so the whole block runs from the binary alone | S |
| 4 | `nova-decide ask` refusal | missing required flags answer `run: nova-decide help`, the whole banner, not the verb's own help, so a reader scrolls to find the flag list | make the remedy `nova-decide ask -h` | S |
| 5 | `nova-decide findings --record missing.jsonl` | a named but absent record file is read as empty: `FINDINGS OK scored=0 classes=0` exit 0, silently masking a typo | refuse a named record that does not exist in the reading verbs | S |
| 6 | `nova-decide read --card ... --diff empty.diff` | an empty diff is `READ OK verdict=LAND` and recorded, where ask refuses an empty state | refuse an empty diff in read, score and gate as ask refuses an empty state | S |
| 7 | `nova-decide brief --help` | the help names the minutes question but none of its options; a fixed-backend caller cannot author the answers from the help alone | name minutes' options in `nova-decide help brief`, and name them again in the BRIEF CARD error | M |
| 8 | `nova-decide ask --dry-run` | prints only its plan line `ASK OK ... recorded=no dry_run=true`, not the ANSWER lines a fixed run would record, so the answers cannot be previewed without recording | print the answers a fixed run would record | S |
| 9 | `nova-decide calibrate` / `findings` | `--bars` (0.5,0.7,0.9) and `--bar` (0.5) defaults are printed by no help text | print each flag's default in the `-h` flag list | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide import --verdicts '*.md'` | a source glob that matches no file is `IMPORT OK` with every count 0 and exit 0, so a mistyped `--verdicts` imports nothing silently | refuse a glob that matches nothing, or print matched=0 per source | S |
| 2 | `nova-decide calibrate` | the help never says a noul is scored by its p of yes with higher counting as flagged; with a positively named noul (safe) the labels work inverted and the AUC still prints | state in `nova-decide help calibrate` that the question must be phrased so yes is the thing flagged, defect-style | S |
| 3 | `nova-decide score-grades` | requires a `nova-sprint log --json` export as `--log`; without one it refuses with a clear message, but building a real sprint log is outside a cold user's scope | document the expected log shape in `nova-decide help score-grades` | S |

## Top 3 fixes
1. Accept `--json` on `help` (and stop silently ignoring it) so the banner's "every verb takes --json" is true.
2. Give `findings` `--max` with a `MORE` line so the banner's "a verb that lists takes --max" holds.
3. Point the help example block at the files `example --dir` generates so a cold run is self-contained.

## Good, keep
Every refusal names the cause, what the input wants, and a runnable next command, and reports all missing flags at once; the `--json` and line renderings come from one value, so they cannot drift; re-running the same `--op` returns the recorded result and changes nothing, making retries after a timeout safe.

## What could not be tried
`--backend jev` was judged from its `-h`, each verb's `--dry-run`, and the CLI doc, and was not run against a live backend: it sends to a model endpoint, which this card must not reach or spend a key on. `score-grades` and `import --judgments/--reports` need a real nova-sprint log and judgment directory not present in this checkout. The default-`--op` clock-collision behaviour and the empty-`--diff`/empty-record edge cases described in the 2026-10-02 ratings were re-measured here: empty diff is still accepted (blocker 6), and the clock-folded id without `--op` is GONE (two identical no-`--op` asks now share an id and the second returns `recorded=existing`).
