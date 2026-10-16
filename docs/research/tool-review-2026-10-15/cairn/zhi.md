# nova-cairn AI-usability review, 2026-10-15

Reviewer: Mercury
Build: cc2f2f8c8148
Score: 9.5/10 (52/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-cairn open --store ./cairns1 --session s1 --publish manual` -> `OPEN OK session=s1 store=./cairns1 source=- publish=manual` |
| 2 | refusals | 5 | `nova-cairn open` -> `OPEN REFUSED: --publish is required; it wants the publication policy: never, manual, deferred, immediate; refusing to guess; run: nova-cairn help` |
| 3 | output | 5 | `nova-cairn index --store ./cairns1 --json` -> `{"result":{"verb":"index","status":"ok","exit":0},"facts":{"sessions":1,"entries":3},"items":[...]}` |
| 4 | dry-run | 5 | `nova-cairn open --store ./cairns_dry --session s1 --publish manual --dry-run` -> `OPEN OK ... dry_run=true`; `./cairns_dry` not created |
| 5 | idempotent | 2 | `nova-cairn append --store ./cairns1 --session s1 --entry e9 --text "op words" --op myop` -> `APPEND REFUSED: unknown flag --op; the flags of append are ...` |
| 6 | no prompts | 5 | `n/a: every verb runs to completion on argv alone; nothing reads a TTY` |
| 7 | exit codes | 5 | `nova-cairn append --store ./cairns1 --session s1 --entry e1 --text "other words"` -> `APPEND FAILED` entry `EXIT=1`, matching verb's stated exit table |
| 8 | one word | 5 | `nova-cairn help` -> `the verbs are open, append, index, receipt, version` (each one word, no duplicates) |
| 9 | push | 5 | `n/a: state is a local file store; nothing is polled or waited on` |
| 10 | no leaks | 5 | `nova-cairn receipt --store ./cairns1 --session s1 --entry e1 --text` -> clean `RECEIPT OK ... text="the words to keep"`, no secret leaked |
| 11 | true help | 5 | the example block (open, append, index, receipt) ran exactly as `help` printed; every exit code matched the stated table |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn append --store ./cairns1 --session s1 --entry e9 --text "op words" --op myop` | `APPEND REFUSED: unknown flag --op; the flags of append are --dry-run, --entry, --file, --json, --now, --publish, --session, --source, --store, --text` | add `--op <id>` to `open` and `append` (a retry id, per the family standard); the same id again returns the recorded result and changes nothing | S |
| 2 | `nova-cairn note` | `CAIRN REFUSED: unknown verb "NOTE"; the verbs are open, append, index, receipt, version` — the help's usage block carries a line `nova-cairn NOTE: --publish is a recorded word ...` that reads as a verb (old 1.2.0 defect, still here) | move the note out of the usage block without the `nova-cairn ` prefix, or onto `open -h` and `append -h` | S |
| 3 | `nova-cairn open --store ./cairns1 --session s1 --publish manual --bogus 1 --json` | `OPEN REFUSED: unknown flag --bogus; the flags of open are ...` printed as lines — `--json` after the unknown flag is never seen, so the refusal is not JSON (old 1.2.0 defect, still here) | scan argv for `--json` before parsing, and render every refusal in the mode asked for | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn index --store ./cairns1 --max 1` | `INDEX MORE kind=entry shown=1 total=3 --max <n> raises the ceiling, --max 0 lists all` | the `MORE` line names only the flag, not the whole runnable command; print the full quoted re-run (store and session included), as `nova-check` does | S |
| 2 | `nova-cairn index --store ./nosuchstore` | `INDEX REFUSED: cannot read store "./nosuchstore": stat ./nosuchstore: no such file or directory; run: nova-cairn help` | a missing store leaks Go's `stat` error; name `nova-cairn open --store ./nosuchstore --session <id> --publish <policy>` as the remedy | S |

## Top 3 fixes
1. Add `--op <id>` to `open` and `append` so a write is idempotent by an op id, as the family standard requires; this is the one gap that costs the score.
2. Remove the `nova-cairn NOTE:` line from the usage block so the listed verbs are all real verbs.
3. Honor `--json` in every refusal by scanning argv before parsing, so a program always gets the format it asked for.

## Good, keep
- Fixed since the 1.2.0 ratings: a matching re-open now prints `reopened=true` with the recorded open stamp (it used to look like a fresh first open); the same-id write race is gone; and the four example lines run verbatim from the help.
- Refusals name every missing flag at once, each with what it wants and the next command; an unknown flag lists the verb's whole flag set and a nearest guess.
- The store is plain files, `--dry-run` writes nothing (verified by a store that never appears), the line grammar is stable and parseable, and a matching `--json` exists on every verb.

## What could not be tried
- Nothing: every verb (`open`, `append`, `index`, `receipt`, `version`) runs against a local store under the job's scratch directory; no verb needs a service the card may not touch.
- `index --since <RFC3339>` and `--since <entry-id>` both exercised; `append --file <path>` and `--source` exercised; `--publish` conflict and duplicate paths exercised.
