# nova-fuse AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: 4540d7fd37ca
Score: 9/10 (50/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-fuse help` -> `example:` block; the six lines ran in order from init through lift quarantine, exactly as printed |
| 2 | refusals | 4 | `nova-fuse init` -> `REFUSED: --box is required; refusing to guess; run: nova-fuse help`; but `nova-fuse check --bogus` names only `--bogus`, not the missing `--box` |
| 3 | output | 4 | `nova-fuse status --box ./fresh.json --json` -> `{"result":{"verb":"status","status":"ok","exit":0},"facts":{"lockdown":"clear","quarantines":0}}`; but a blown `check` prints only to stderr, so stdout capture sees empty at exit 1 |
| 4 | dry-run | 5 | `nova-fuse lockdown --box ./fresh.json --dry-run "would blow"` -> `LOCKDOWN OK dry_run=true`, and `cat ./fresh.json` unchanged |
| 5 | idempotent | 3 | `nova-fuse lockdown --box ./fuse-box.json --op a "r"` -> `REFUSED: unknown flag --op`; no write verb takes `--op`, though re-writing the same state keeps the standing record |
| 6 | no prompts | 5 | every verb, including the example sitting, ran to completion with no input, `n/a: no interactive path` |
| 7 | exit codes | 5 | `nova-fuse check --box ./fuse-box.json x` -> exit 1; `nova-fuse status --box ./fuse-box.json` -> exit 0; `nova-fuse init` (no `--box`) -> exit 2, as the help's exit table states |
| 8 | one word | 5 | verbs are single words (version, init, status, check, lockdown, quarantine, lift, path, help) with no duplicates |
| 9 | push | 5 | a state file tool with no waited-on state, `n/a: no store to poll or push` |
| 10 | no leaks | 5 | no output line carried a secret; paths ( reasons echo only caller-supplied data, escaped |
| 11 | true help | 4 | help promises a dry-run OK line "says dry_run=true"; on an already-blown box `nova-fuse lockdown --box ./fuse-box.json --dry-run "x"` prints `LOCKDOWN OK already=blown` with no dry_run=true |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse check --box ./fuse-box.json x 2>/dev/null` | prints nothing on stdout at exit 1; the blown `FUSE FAILED` line and its remedy go only to stderr, so a caller capturing stdout sees an empty result on a permission denial | print the blown result on stdout too, keeping exit 1, and state the stream contract on `help check` | S |
| 2 | `nova-fuse check --bogus` | `REFUSED: unknown flag --bogus; the flags of check are --box, --json` names the unknown flag but not the also-missing required `--box`, so one run does not report every problem at once | after a parse failure, report every missing required flag and every unknown flag in one run | S |
| 3 | `nova-fuse lockdown --box ./fuse-box.json --op abc "r"` | `REFUSED: unknown flag --op`; no write verb takes the standard `--op` idempotency id, so a retry is safe only by luck of the standing record, not by caller-chosen id | implement `--op` on init, lockdown, quarantine and lift quarantine per the house standard | M |
| 4 | `nova-fuse lockdown --box ./fuse-box.json --dry-run "x"` on a blown box | prints `LOCKDOWN OK already=blown since=...` with no `dry_run=true`, so a dry run and a write print the same line, contradicting the help's promise | carry `dry_run=true` and "nothing written" on the standing lines when `--dry-run` is set | S |
| 5 | `nova-fuse help help` | `REFUSED: unknown verb "help"`; the verb lists disagree between the bare refusal (`lift`) and the help refusal (`lift quarantine, lift lockdown`), so an AI is told two different verb sets | one verb list feeding the dispatcher, the banner, every refusal and `help <verb>` | S |
| 6 | `ls ./*.lock` after any write | a 0-byte `fuse-box.json.lock` is left beside the box, named nowhere in help; a new AI sees a stray sibling it cannot explain | name `<box>.lock` in the help box paragraph, or unlink it after unlock where the lock design allows | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | README.md:83 | `These are the Nova Tools 1.0.0 commands.` and the install example pin the trial at 1.0.0 while the tree ships later | move the sentence and install example to the shipped release | S |
| 2 | internal/fuse/fuse.go:1 | the package doc opens on shouted numbered headings (`WHAT IS ACTUALLY DECIDED HERE`) | state each rule once, present tense, in ordinary prose | M |
| 3 | `nova-fuse help` | the banner is 74 lines, with the exit-code prose repeated in every per-verb help | keep the three answers, the verb table and the example in the banner; move the rest to `help <verb>` and docs/CLI.md | M |
| 4 | `nova-fuse init` (missing `--box`) | the hint is a second indented line, not the grammar's `NOTE` token | prefix the hint with the `NOTE` token or fold it into the refusal line | S |
| 5 | `nova-fuse status --box ./fuse-box.json --max 0` | the `STATUS MORE` remedy is the hint `--max <n> raises the ceiling`, not a command that runs | print `nova-fuse status --box <path> --max <n>` as the remedy | S |

## Top 3 fixes
1. Print the blown result on stdout as well (keeping exit 1), so an AI capturing stdout never reads empty on a permission denial.
2. Report the missing `--box` beside an unknown flag in one run, so a refusal names every problem at once.
3. Implement the standard `--op` idempotency id on the write verbs.

## Good, keep
The refusal grammar names the cause and the next runnable command in one line, quotes the box path for the shell, and `--` keeps a leading-dash surface data. That one-turn recovery is the strongest property.
Every write is verified by re-reading the box, and a re-blow or re-quarantine keeps the first record (`already=blown`, `already=quarantined`), so retries are safe even before `--op` exists.
The six-line example runs as printed from init through lift quarantine, and `--json` (new since the 2026-10-02 rating, which reported no `--json`) renders the same value as the typed lines; `--dry-run` writes nothing and creates no lock file.

## What could not be tried
No verb was left untried: version, init, status, check, lockdown, quarantine, lift quarantine, lift lockdown, path and help all ran locally in the scratch directory, with no service or live store required, so there is no verb judged from help alone.
