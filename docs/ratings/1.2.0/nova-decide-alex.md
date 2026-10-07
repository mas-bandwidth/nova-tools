# nova-decide READ and USE rating, nova-tools 1.2.0

Rater: openai/gpt-5.6-luna in OpenCode
Build: a65b5cba44ab
READ: 7/10
USE: 7/10

This is a cold review of the staged v1.2.0 head. I read `nova-decide help`, every verb's `-h`, and `docs/SPEC-NOVA-DECIDE.md`, then built the binary and ran the fixed-backend examples in a throwaway directory. No live store, server, network call, or JEV key was used; the fixed backend made the complete local lifecycle runnable.

## Reasons

READ. The banner explains typed noul and choice decisions, the help enumerates the verbs, and every verb's help gives flags, exit codes, effect, and a runnable example where one exists. The spec clearly explains the two backends, append-only record, operation replay, outcomes, calibration, and the decision-specific schemas. The release is readable enough to operate cold, but the banner still shows a pseudo-run whose output does not match the binary, and several help promises are incomplete: `brief` does not list the `minutes` choices, `grade`'s usage omits documented flags, and the train-side defaults are not stated.

USE. The fixed backend worked end to end over a throwaway record: ask, read, score, attempt, grade, gate, brief, outcome, replay, dry-run, and findings all produced typed output and exited as documented. Operation replay returned the recorded answers without adding a record line, and the dry-run did not write. Required-flag refusals named each missing input and the fixed backend avoids infrastructure. USE is limited by green results over absent evidence: an empty diff is accepted, a missing record is treated as empty, and an unmatched import glob succeeds with zero items. `help --json` also contradicts the banner's claim that every verb accepts JSON.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide help --json` | The banner says every verb takes `--json`, but help treats `--json` as an unknown verb and exits 2. | Accept `--json` for help or state that help is excluded from the promise. | S |
| 2 | `cmd/nova-decide/main.go:56` | The banner's how-it-works pseudo-run uses a schema and output line that are not the shipped fixture or actual output, and does not name the record file. | Quote the real fixture transcript and name schema, state, backend, and record. | S |
| 3 | `nova-decide brief -h` | The help names `minutes` but omits its valid options, so a fixed-backend caller cannot construct that answer from help alone. | List `under-10`, `10-20`, `20-45`, `45-90`, `90-180`, and `over-180`. | S |
| 4 | `nova-decide calibrate -h`, `nova-decide findings -h` | The documented defaults are not stated: calibration uses bars 0.5, 0.7, 0.9 and findings uses bar 0.5. | Put each default in the corresponding flag description. | S |
| 5 | `nova-decide grade -h` | The usage synopsis omits `--examples`, `--held-out`, and `--seed` even though the flags are accepted and documented below it. | Include the optional grading flags in the synopsis. | S |
| 6 | `nova-decide read --diff /dev/null ...` | An empty diff is accepted and recorded as a successful LAND decision. | Refuse an empty diff for read, score, and gate. | S |
| 7 | `nova-decide findings --record ./missing.jsonl` | A named record that does not exist produces `FINDINGS OK scored=0` instead of identifying the missing input. | Refuse missing records in inspection verbs. | S |
| 8 | `nova-decide import --record r.jsonl --verdicts './no-match/*.md'` | A supplied glob matching nothing exits 0 with zero counts, which can look like a successful import. | Refuse an unmatched supplied glob, or emit an explicit absent-input note. | S |
| 9 | `nova-decide ask ... --dry-run` | Dry-run prints only the OK line and omits the answer lines a real fixed-backend run would record. | Render the fixed answers in dry-run using the same output path as a real run. | S |
| 10 | `nova-decide outcome ... --label wrong --dry-run` on an `ok` outcome | Dry-run reports OK even when the requested label conflicts; the real run exits 1 for the same conflict. | Validate the existing label during dry-run and return the real conflict status. | S |

## Good, keep

The fixed backend makes a meaningful full trial possible without a service or key. Every deciding verb prints its answers and probabilities, and the record keeps the state and inputs needed to understand what was decided. `--op` replay is real idempotence, not just a message: the same operation returns `recorded=existing` and leaves the record unchanged. Required-input refusals collect independent missing flags, every help page names the effect on the outside world, and the spec is unusually explicit about schema validation, record locking, and calibration semantics.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| 1.1.0's split READ/USE cards | CHANGED | v1.2.0 uses the combined READ and USE form required by `TestRatingFileIsInForm`. |
| brief help omitted its `minutes` options | STILL THERE | `nova-decide brief -h` still names the question without enumerating its choices. |
| calibration defaults were unstated | STILL THERE | `calibrate -h` still omits 0.5, 0.7, 0.9, and findings still omits bar 0.5. |
| dry-run omitted fixed answers | STILL THERE | `ask ... --dry-run` prints no ANSWER lines. |
| help accepted no `--json` | STILL THERE | `nova-decide help --json` exits 2 with unknown verb. |
| fixed backend was usable for the lifecycle | IMPROVED | This review ran ask through findings on one throwaway record, including replay and dry-run. |
