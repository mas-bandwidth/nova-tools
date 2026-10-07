# nova-decide READ and USE rating, nova-tools 1.2.0

Rater: Alex, openai/gpt-6-luna in OpenCode
Build: 7acb90e18a76
READ: 6.5/10
USE: 7/10

## Reasons

READ. I built the staged release candidate and read `nova-decide help`, every listed verb's `-h` (including `version` and `help`), and docs/SPEC-NOVA-DECIDE.md without using Jev. I had already read older rating cards to learn the required form, so this was not blind to earlier findings. The verb help is broad: it explains the question each decision answers, gives required flags and effects, and most deciding verbs have executable fixture examples. The spec gives a newcomer a useful map of schemas, the append-only record, replay, calibration and how the sprint consumes decisions. What keeps READ at 6.5 is the first page's invented pseudo-transcript (`state R?`, `id=f`) rather than the binary's output, several hidden defaults and option sets, two verbs with no examples, and visible mismatches between the normative spec and the program. The header's claim that it is trained from its record also overstates the built train side.

USE. I used the fixed backend and fixture inputs with a disposable record under the job's scratch cache, never a live store or server. Ask, read, score, attempt, grade, gate, brief, outcome, calibrate, import, score-grades and findings all ran. The record held decisions and labels, calibration handled one positive and one negative, import recorded two HOLD reports, and the gate emitted one decision per failure. Explicit `--op` replay is a clear recovery path. USE stays at 7 because an unmatched import glob and a nonexistent findings record both return OK with zero work, outcome dry-run accepts a conflicting label that the real call rejects, and a repeated ask without `--op` records a second decision after two seconds. Ask dry-run omits the answer lines, and a failed empty-state ask gives an unusable `make --answers ...` remedy. I did not call Jev: the fixed backend exercised the complete local path without a key or network.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `cmd/nova-decide/main.go:59` | The banner's how paragraph presents an invented schema, state and `ASK OK` line as a run; `R?` and `id=f` do not appear in real output, and the paragraph does not name the record file. | Replace it with the fixture's real output and name where the schema, state and record live. | S |
| 2 | `cmd/nova-decide/main.go:67` | The deciding verbs classify the effect as delivery even with `--backend fixed`, which sends nothing beyond the machine and only writes the local record. | Make the effect conditional: local write for fixed, delivery plus local record write for Jev. | S |
| 3 | `cmd/nova-decide/main.go:137` | `grade`'s top usage omits `--examples`, `--held-out` and `--seed`, although its flags accept them. | Include all three options in the usage line. | S |
| 4 | `cmd/nova-decide/main.go:191` | `brief -h` names `minutes` but not its six answer values, so a fixed-backend caller cannot construct that answer from help alone. | List `under-10`, `10-20`, `20-45`, `45-90`, `90-180` and `over-180`. | S |
| 5 | `cmd/nova-decide/main.go:240,317` | `calibrate -h` omits default bars `0.5,0.7,0.9`; `findings -h` omits default bar `0.5` and default seven-day window. | Print these defaults in their flag descriptions. | S |
| 6 | `nova-decide import -h`; `nova-decide score-grades -h` | Both built verbs have no runnable example, unlike the other verbs' help. | Add fixture-backed examples for each verb. | S |
| 7 | `cmd/nova-decide/main.go:257` | Import help says "one nothing answers is counted as unanswered", which is not parseable cold. | Say "a judgment the log does not answer is counted as unanswered". | S |
| 8 | `nova-decide findings --max 1`; `internal/tool/tool.go:512` | The banner says every listing verb takes `--max`, but findings refuses it as an unknown flag. | Add bounded findings output with a `MORE` count, or narrow the banner's promise. | S |
| 9 | `nova-decide help --json` | The banner says every verb takes `--json`, but help treats `--json` as an unknown verb and exits 2. | Support JSON for help or explicitly exempt help from the banner's promise. | S |
| 10 | `cmd/nova-decide/main.go:778` | An empty-state ask fails with `run: make --answers answer every question of the schema`, which is not a command and does not fix the empty state. | Report the state problem and a remedy that changes the state input; keep answers-file advice for answer errors only. | S |
| 11 | `nova-decide ask` without `--op`; `cmd/nova-decide/main.go:751` | Repeating the same ask two seconds later creates a different id and records another decision because the default id includes the timestamp; a retry is not safe unless the caller supplied an op. | Derive the default id from stable inputs, or require and explain `--op` wherever replay safety is needed. | S |
| 12 | `nova-decide ask --dry-run` | The dry run prints only the ASK summary, not the fixed answers that the real run would record. | Print the known fixed ANSWER items during dry-run. | S |
| 13 | `nova-decide outcome --dry-run` | For a decision already labelled `ok`, `--label wrong --dry-run` prints `OUTCOME OK ... recorded=no`, exit 0, although the real run rejects the conflicting label with exit 1. | Validate the existing label in dry-run and return the same conflict status as a real run. | S |
| 14 | `nova-decide findings --record ../.cache/no-such-record.jsonl` | A named record that does not exist is treated as empty and returns `FINDINGS OK scored=0`, hiding a path mistake. | Refuse a missing input record in read-only verbs. | S |
| 15 | `nova-decide import --verdicts '../.cache/no-matches/*'` | A source glob matching nothing returns `IMPORT OK` with all counts zero. | Refuse an unmatched source or report an explicit no-files result that is not success. | S |
| 16 | `docs/CLI.md:2752` | The first-run example writes `./decisions.jsonl` in the checkout root, contrary to README.md's `./trial-` naming rule for created files. | Use `./trial-decisions.jsonl` in the first-run commands. | S |
| 17 | `docs/SPEC-NOVA-DECIDE.md:181-195` | The spec says backend failures print `<VERB> FAIL`, while an empty-state backend failure prints `ASK FAILED`; its dry-run list also omits score and import, which accept `--dry-run`. | Make the output grammar and dry-run verb list match the binary. | S |
| 18 | `docs/SPEC-NOVA-DECIDE.md:1,17-19` | The title says the system is trained from its own record while the same spec says export and backend training are not built. | Title the built behavior as calibrated from its record until training exists. | S |
| 19 | `docs/SPEC-NOVA-DECIDE.md:106-111` | The record's one-writer and attach-once claims say their TLA+ model is owed, so the stated state invariants have no TLC check. | Land and check the promised record model. | M |

## Good, keep

The fixed backend made a real record-backed first run possible without credentials, a store or network. With an explicit op id, replay returns the recorded decision. Outcomes are attached to decisions, calibration prints AUC and bars, and a gate's failures are individually visible with their probabilities and routes. The help for the Jev backend names its key delivery mechanism, and the spec records calibration quality instead of presenting every decision as trustworthy.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| 1.1.0 READ: the banner's how paragraph is a pseudo-run with a line the tool does not print | STILL THERE | `nova-decide help` still shows `state R?` and `id=f`, while the first-run transcript prints token fields and a caller-provided op id. |
| 1.1.0 READ: the record's TLA+ model is owed | STILL THERE | docs/SPEC-NOVA-DECIDE.md:106-111 still says the model is owed. |
| 1.1.0 USE: brief help omits the minutes options | STILL THERE | `brief -h` names minutes but gives none of its six values. |
| 1.1.0 USE: fixed dry-run omits answers | STILL THERE | `ask --dry-run` prints no ANSWER lines. |
| 1.1.0 USE: calibrate and findings defaults are undocumented | STILL THERE | Their `-h` flag descriptions omit the bars, bar and time-window defaults. |
| 1.1.0 USE: help --json fails | STILL THERE | `nova-decide help --json` exits 2 as unknown verb `--json`. |
| 1.1.0 READ: first run writes decisions.jsonl at checkout root | STILL THERE | docs/CLI.md:2752 uses `./decisions.jsonl`. |
| 1.1.0 READ: `noul` is undefined in the reference | FIXED | The current help defines it as a yes-or-no question with a probability of yes. |
| earlier 1.2.0 ratings: imports and score-grades have no help examples; findings has no --max; dry-run outcome does not check label conflicts | CONFIRMED | The cold help walk and disposable-record probes reproduced all three. |
