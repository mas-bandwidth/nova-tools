# nova-decide AI-usability review summary, 2026-10-15

Reviews: 4 (R1..R4, in byte order of file name)
Build: cc2f2f8c8148, b4f484c84916, 4540d7fd37ca
Score: 9.5/10 (range 9.0-10)

## Rubric
| # | criterion | median | range | the evidence that decided it |
|---|---|---|---|---|
| 1 | cold | 4 | 4-5 | `nova-decide help` -> its `example:` block references `./cmd/nova-decide/testdata/...` paths that exist only in a source checkout, so a cold user who ran `example --dir` cannot run the rest from the generated directory |
| 2 | refusals | 5 | 4-5 | `nova-decide ask` -> `ASK REFUSED: --schema is required; it wants the decision's schema, a JSON file; refusing to guess; run: nova-decide help` (all missing flags named at once) |
| 3 | output | 5 | 4-5 | `nova-decide read ... --json` -> `{"result":{"verb":"read","status":"ok","exit":0},...}` (one value; line and JSON renderings consistent) |
| 4 | dry-run | 5 | 5-5 | `nova-decide example --dir ./ex2 --dry-run` -> `EXAMPLE OK files=4 dry_run=true` and no directory written |
| 5 | idempotent | 5 | 4-5 | same `--op` run twice -> second prints `recorded=existing` and changes nothing |
| 6 | no prompts | 5 | 5-5 | `nova-decide ask --backend fixed --record ./d.jsonl` (no --answers) -> `ASK REFUSED: --backend fixed answers from --answers <file>` without waiting |
| 7 | exit codes | 5 | 5-5 | `nova-decide outcome --id card-1 --label wrong` after `ok` -> exit 1; refusals exit 2; successes exit 0, as the help's table states |
| 8 | one word | 5 | 5-5 | 14 verbs, all single words, no duplicates, each distinct from every other tool verb |
| 9 | push | 5 | 5-5 | n/a: the tool records decisions to a file and waits on no external state, so there is nothing to poll |
| 10 | no leaks | 5 | 5-5 | no output line printed an environment secret; the key is read only from the environment (`--backend jev` with no key refuses with `reason=key_absent`) |
| 11 | true help | 3.5 | 3-4 | `nova-decide help --json` -> exit 2 `unknown verb "--json"` and `nova-decide findings --max 1` -> `unknown flag --max`, while the banner claims every verb takes `--json` and every listing verb `--max`; `help ask --json` silently ignores the flag |

## Consensus 10/10 blockers
| # | where | what an AI meets | fix | size | named by |
|---|---|---|---|---|---|
| 1 | `nova-decide help --json` | exit 2 `unknown verb "--json"` while the banner's appendix claims "Every verb takes --json"; `nova-decide help ask --json` silently prints the help and ignores the flag | accept `--json` on help (or refuse it after the verb consistently) | S | R1,R3,R4 |
| 2 | `nova-decide findings --max 1` | `FINDINGS REFUSED: unknown flag --max` while the banner says "A verb that lists takes --max"; findings lists FINDING lines | give findings `--max` and a `MORE shown= total=` line | S | R1,R3,R4 |
| 3 | `nova-decide help` example block | the ask/read/score/attempt/grade/gate example lines reference `./cmd/nova-decide/testdata/...`, which exist only inside a source checkout; a cold user who ran `nova-decide example --dir` cannot run the rest from the generated directory | make the example lines reference the directory `example` creates, so the whole block runs from the binary alone | S | R1,R2,R3,R4 |
| 4 | `nova-decide ask`/`findings`/`outcome` refusals | missing-flag and bad-value refusals end with `run: nova-decide help`, the whole banner, not the verb's own help, so a reader scrolls to find the flag list; `OUTCOME FAILED` omits a remedy | point every verb-level refusal to `run: nova-decide <verb> -h` and append a `run:` remedy to `OUTCOME FAILED` | S | R1,R2,R4 |
| 5 | `nova-decide findings --record missing.jsonl` | a named but absent record file is read as empty: `FINDINGS OK scored=0 classes=0` exit 0, silently masking a typo | refuse a named record that does not exist in the reading verbs | S | R3,R4 |
| 6 | `nova-decide read --card ... --diff empty.diff` | an empty diff is `READ OK verdict=LAND` and recorded, where ask refuses an empty state | refuse an empty diff in read, score and gate as ask refuses an empty state | S | R3,R4 |
| 7 | `nova-decide ask --dry-run` | prints only its plan line `ASK OK ... recorded=no dry_run=true`, not the ANSWER lines a fixed run would record, so the answers cannot be previewed without recording | print the answers a fixed run would record | S | R1,R4 |
| 8 | `nova-decide brief` / `nova-decide import` | both verbs append to `--record`, but refuse `--op` (`BRIEF REFUSED: unknown flag --op`), unlike the other record-writing verbs | accept `--op <id>` on brief and import so every record-appending verb has uniform idempotence flag support | S | R2 |
| 9 | `nova-decide --help` usage line for `example` | `nova-decide example -h` and `--dry-run` support `--dry-run`, but the top-level usage synopsis `nova-decide example --dir <new-directory>` omits `[--dry-run]` | add `[--dry-run]` to the `example` usage line | S | R2 |
| 10 | `nova-decide ask ... --json` | `items[].fields.p` packs the option and probability into a colon-delimited string (`"p":"yes:0.9"`), requiring string splitting after JSON parsing | add a structured numeric probability map alongside `"p"` in `--json` answer items | S | R2 |
| 11 | `nova-decide brief --help` | the help names the minutes question but none of its options; a fixed-backend caller cannot author the answers from the help alone | name minutes' options in `nova-decide help brief`, and name them again in the BRIEF CARD error | M | R4 |

## Top 3 fixes
1. Accept `--json` on `help` (and stop silently ignoring it) and give `findings` a `--max`, so the banner's "every verb takes --json" and "a verb that lists takes --max" claims hold.
2. Point the help `example:` block at the directory `example --dir` generates so a cold run is self-contained from the binary alone.
3. Point every verb-level refusal's remedy at `run: nova-decide <verb> -h` and append a `run:` remedy to `OUTCOME FAILED`.

## Disagreements
No criterion was scored two or more points apart across reviews; the widest spreads are true help (3–4) and cold (4–5).
Every blocker above was rerun from the checkout build and still reproduces; none were dropped.
One classification diverges: `ask --dry-run` printing no answer preview is kept as a blocker (carried by R4) though R1 lists it as a nice-to-have; it stays a blocker because it hides what a fixed run would record.
cold: R3 scores it 5 (the example block runs from the checkout) while R1/R2/R4 score 4; the 4s hold on a cold run outside the repository.

## Nice-to-haves
- `import`: refuse a source glob that matches no file, or print matched=0 per source (currently `IMPORT OK` with every count 0 and exit 0).
- `fixed` backend: name the answers shape in the missing `--answers` remedy instead of `run: nova-decide help`.
- `example`: also write a sample `record.jsonl` with labelled outcomes so `calibrate` and `findings` run from the generated directory alone.
- `calibrate`: state in help that a noul is scored by its p of yes (defect-style phrasing), so labels are not inverted.
- `score-grades`: document the expected `nova-sprint log --json` export shape in `nova-decide help score-grades`.
