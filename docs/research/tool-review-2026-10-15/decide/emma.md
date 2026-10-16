# nova-decide AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: b4f484c84916
Score: 9.0/10 (50/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-decide example --dir ./ex` -> `EXAMPLE OK files=4` |
| 2 | refusals | 4 | `nova-decide ask` -> `ASK REFUSED: --schema is required; it wants the decision's schema, a JSON file; refusing to guess; run: nova-decide help` |
| 3 | output | 4 | `nova-decide ask --schema ./ex/schema.json --state ./ex/state.txt --backend fixed --answers ./ex/answers.json --record ./decisions.jsonl --op first --json` -> `{"result":{"verb":"ask","status":"ok","exit":0},"facts":{"id":"first","decision":"example","backend":"fixed","tokens_in":0,"tokens_out":0,"recorded":"existing"},"items":[{"kind":"answer","fields":{"question":"clear","type":"noul","value":"yes","p":"yes:0.9"}}]}` |
| 4 | dry-run | 5 | `nova-decide ask --schema ./ex/schema.json --state ./ex/state.txt --backend fixed --answers ./ex/answers.json --record ./decisions.jsonl --op second --dry-run` -> `ASK OK id=second decision=example backend=fixed questions=1 state_bytes=70 recorded=no dry_run=true` |
| 5 | idempotent | 4 | `nova-decide ask --schema ./ex/schema.json --state ./ex/state.txt --backend fixed --answers ./ex/answers.json --record ./decisions.jsonl --op first` -> `ASK OK id=first decision=example backend=fixed tokens_in=0 tokens_out=0 recorded=existing` |
| 6 | no prompts | 5 | `nova-decide findings --record ./decisions.jsonl --bar 2.5` -> `FINDINGS REFUSED: --bar "2.5" wants one probability from 0 to 1; run: nova-decide help` |
| 7 | exit codes | 5 | `nova-decide outcome --record ./decisions.jsonl --id first --label wrong` -> `OUTCOME FAILED id=first: decision first is labelled ok already (at 2026-10-15T18:00:45Z), not wrong; an outcome is attached once` |
| 8 | one word | 5 | `nova-decide calibrate --record ./cmd/nova-decide/testdata/record.jsonl --decision read --question defect --positive wrong --negative ok` -> `CALIBRATE OK decision=read schema=505bd379c3753631 question=defect option=yes positives=3 negatives=5 skipped=0 auc=0.933` |
| 9 | push | 5 | `n/a: synchronous decision recorder and calibration reader with no polling loops or state watches` |
| 10 | no leaks | 5 | `nova-decide ask --bogus` -> `ASK REFUSED: unknown flag --bogus; the flags of ask are --answers, --backend, --dry-run, --json, --op, --record, --schema, --state, --timeout; run: nova-decide ask -h` |
| 11 | true help | 4 | `nova-decide example --dir ./ex3 --dry-run` -> `EXAMPLE OK files=4 dry_run=true` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide --help` (`example:` section) | Line 1 runs `nova-decide example --dir ./decide-example`, but the very next example line (`nova-decide ask --schema ./cmd/nova-decide/testdata/schema.json ...`) and all subsequent example lines hardcode checkout-relative `./cmd/nova-decide/testdata/...` paths that fail outside the source checkout | Point the `ask` and `read` example lines in `--help` at the `./decide-example/` directory written by `nova-decide example --dir ./decide-example` | S |
| 2 | `nova-decide ask`, `nova-decide findings --record ./decisions.jsonl --bar 2.5`, `nova-decide outcome --record ./decisions.jsonl --id first --label wrong` | Missing-flag and bad-value refusals end with `run: nova-decide help` instead of the verb-specific `run: nova-decide <verb> -h` (which unknown-flag refusals use), and `OUTCOME FAILED` omits a `; run: ...` remedy | Point every verb-level refusal to `run: nova-decide <verb> -h` and append `; run: nova-decide outcome -h` (or a read command on `--record`) to `OUTCOME FAILED` | S |
| 3 | `nova-decide brief` and `nova-decide import` | Both verbs append decision records to `--record <file>`, and `ask`, `read`, `score`, `attempt`, `grade`, and `gate` all accept `--op <id>`, but `brief` and `import` refuse `--op` (`BRIEF REFUSED: unknown flag --op`) | Accept `--op <id>` on `brief` and `import` so every record-appending verb has uniform idempotence flag support | S |
| 4 | `nova-decide --help` (`usage:` line for `example`) | `nova-decide example -h` and `nova-decide example --dir ./ex3 --dry-run` support `--dry-run` (`EXAMPLE OK files=4 dry_run=true`), but the top-level `usage:` synopsis `nova-decide example --dir <new-directory>` omits `[--dry-run]` | Add `[--dry-run]` to `nova-decide example --dir <new-directory> [--dry-run]` in `nova-decide --help` | S |
| 5 | `nova-decide ask ... --json` | In `--json` output, `items[].fields.p` packs the option and probability into a colon-delimited string (`"p":"yes:0.9"`) instead of a structured map or numeric field, requiring string splitting after JSON parsing | Add a structured numeric probability map (such as `"probabilities":{"yes":0.9}`) alongside `"p"` in `--json` answer items | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide example --dir ./decide-example` | Writes `schema.json`, `state.txt`, `answers.json`, and `sample.diff`, but does not write a sample `record.jsonl` with labelled outcomes for trying `calibrate` and `findings` outside the repository | Include a small `record.jsonl` in `nova-decide example` so `calibrate` and `findings` can also be run from `./decide-example` alone | S |

## Top 3 fixes
1. Update the `example:` commands in `nova-decide --help` to use the `./decide-example/` files generated by `nova-decide example --dir ./decide-example` instead of checkout-only `./cmd/nova-decide/testdata/...` paths, and add `[--dry-run]` to the `example` usage line.
2. Point missing-flag and bad-value refusals to `run: nova-decide <verb> -h` instead of `run: nova-decide help`, and include a `run:` remedy on `OUTCOME FAILED`.
3. Add `--op <id>` support to `nova-decide brief` and `nova-decide import` to match all other record-writing verbs.

## Good, keep
- When multiple required flags are missing (`nova-decide ask`), the tool prints every missing required flag (`--schema`, `--state`, `--backend`, `--record`) in one invocation instead of failing one flag at a time.
- Every verb supports `--json`, and repeating a decision with the same `--op <id>` is idempotent (`recorded=existing`, exit `0`) while conflicting outcomes exit `1` without overwriting the first outcome.
- `nova-decide example --dir ./ex` embeds sample inputs directly in the binary so `ask` and `--dry-run` work offline with zero network or model calls.

## What could not be tried
- `--backend jev` on `ask`, `read`, `score`, `attempt`, `grade`, `gate`, and `brief`: reaches a live model backend and spends a key, which the card rules forbid (`no call that reaches a model or spends a key`); exercised all decision and record verbs using `--backend fixed`, `--dry-run`, and scratch record files.
