# nova-fuse AI-usability review, 2026-10-15

Reviewer: grok-4.7
Build: 453ce3a00131
Score: 7.5/10 (42/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-fuse init --box ./fuse-box.json` -> `INIT OK box=./fuse-box.json: an empty box, no fuse blown (verified by re-reading the box)` |
| 2 | refusals | 3 | `nova-fuse status` -> `nova-fuse status REFUSED: --box is required; refusing to guess; run: nova-fuse help` |
| 3 | output | 3 | `nova-fuse path --box 'FUSE OK lockdown=clear'` -> `FUSE OK lockdown=clear` |
| 4 | dry-run | 5 | `nova-fuse init --dry-run --box ./never-written.json` -> `INIT OK box=./never-written.json dry_run=true: nothing written; a real run would make an empty box there` |
| 5 | idempotent | 1 | `nova-fuse quarantine --box ./fuse-box.json --op op1 c-surface "op probe"` -> `nova-fuse quarantine REFUSED: unknown flag --op; the flags of quarantine are --box, --dry-run, --json; run: nova-fuse help quarantine` |
| 6 | no prompts | 5 | `nova-fuse init --box ./stdin-box.json` -> `INIT OK box=./stdin-box.json: an empty box, no fuse blown (verified by re-reading the box)` |
| 7 | exit codes | 4 | `nova-fuse check --box ./job2-box.json other-surface` -> `FUSE FAILED lockdown since=2026-10-15T16:21:00Z: source asked for a credential (hard: all untrusted reads and surface-driven acts stop, authored outbound continues; replaced only in a live conversation with the person you work with)` |
| 8 | one word | 4 | `nova-fuse help nosuch` -> `nova-fuse help REFUSED: unknown verb "nosuch"; the verbs are init, status, check, lockdown, quarantine, lift quarantine, lift lockdown, path, version; run: nova-fuse help` |
| 9 | push | 5 | `n/a: check reads a local file and returns; no verb waits on state` |
| 10 | no leaks | 5 | `nova-fuse check --box ./unreadable.json s` -> `nova-fuse check REFUSED: cannot read ./unreadable.json: open ./unreadable.json: permission denied -- cannot prove no fuse is blown, so treating every fuse as BLOWN, never as clear; repair the box by hand with the person you work with, live; run: nova-fuse help` |
| 11 | true help | 3 | `nova-fuse lockdown --dry-run --box ./job2-box.json "another reason"` -> `LOCKDOWN OK already=blown since=2026-10-15T16:21:00Z: source asked for a credential (standing record kept; the new reason was not recorded: another reason)` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse lockdown --dry-run --box ./job2-box.json "another reason"` | `LOCKDOWN OK already=blown ... (standing record kept; the new reason was not recorded: another reason)` with no `dry_run=true`, the same text as a real re-blow. Help says the dry-run line says `dry_run=true`. | Print `dry_run=true` on every dry-run line, including the already-standing path. | S |
| 2 | `nova-fuse help` (grammar in SPEC.md) | A blown check prints `FUSE FAILED`, and `--json` works. The cited grammar still says `FUSE FAIL` and that there is no `--json`. | Make the cited grammar the bytes the tool prints, including `FAILED` and `--json`. | M |
| 3 | `nova-fuse status` | The missing `--box` refusal is printed twice; the second line has no hint. | Print that refusal once. | S |
| 4 | `nova-fuse check --json --box ./fuse-box.json s1` | `{"result":{"verb":"check","status":"failed","exit":1,"why":["r1"],"reasons":["quarantine_blown"]},"facts":{"decision":"blown",...}}` has no remedy. The text line names the lift command. | Put that same next command in `result.remedy`. | S |
| 5 | `nova-fuse status --bogus --max notanumber` | `unknown flag --bogus` only. The bad `--max` and the missing `--box` are not named. | Name every problem in one refusal. | S |
| 6 | `nova-fuse lift lockdown` | `Nothing this tool is told changes that. Stop, and go talk with them now.` No `run:` command. The banner's remedy is init on a new path. | Add `run: nova-fuse init --box <a new path>`. | S |
| 7 | `nova-fuse path --box 'FUSE OK lockdown=clear'` | Prints `FUSE OK lockdown=clear` at exit 0, which is the gate's clear line. | Prefix the echo with `PATH OK` so it cannot read as the gate. | S |
| 8 | `nova-fuse init --box ./broken-box.json` | `INIT FAILED box=./broken-box.json: ... read it with nova-fuse status --box './broken-box.json'`. That status exits 2. | Say the file is not a box and to repair it by hand, not to run status. | S |
| 9 | `nova-fuse status --box ./missing-box.json` | Exit 2. The banner says `status, path: 0 only`. | Drop `0 only`, or say status exits 2 when the box cannot be read. | S |
| 10 | `nova-fuse quarantine --box ./missing-box.json a-forum reason` | The refusal is right, then a 0-byte `missing-box.json.lock` is left beside a box that was never made. | Do not leave a lock when the verb refuses. | S |
| 11 | `nova-fuse nosuchverb` | Lists `lift` and `help`. `nova-fuse help nosuch` lists `lift quarantine` and `lift lockdown` and omits `help`. | Keep one verb list. | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse quarantine --op` | Writes refuse `--op`. A same-fact retry does not clobber, but there is no op id. | Take `--op` and return the recorded result. | M |
| 2 | `nova-fuse init` | The missing-flag hint is a second indented line, not a `NOTE` line. | Prefix that hint with `NOTE`. | S |
| 3 | `nova-fuse check --box ./fuse-box.json a-forum` | Success and failure open with `FUSE`, not `CHECK`. Help never names that token. | Name `FUSE` in `help check`. | S |

## Top 3 fixes
1. Take `--op` on the four writes so a retry is the recorded result (idempotent is 1; this is the largest gap, and it is a new flag, not a broken one).
2. Print `dry_run=true` on every dry-run line, including an already-standing fuse, so a preview cannot be read as the write.
3. One refusal: print it once, name every problem, and give `lift lockdown` a `run:` command. With that, put the lift command in the check `--json` receipt and make the cited grammar match `FAILED` and `--json`.

## Good, keep
Exit 0 is the only clear. `-h` after a verb exits 2, and `nova-fuse check --box ./fuse-box.json -- -h` treats `-h` as a surface. A missing, unreadable, or broken box exits 2 and says BLOWN, never clear, and names init.
The 2026-10-02 silent re-blow is gone: a second quarantine or lockdown keeps the standing record and says the new reason was not recorded. An unknown flag now names itself. `--json` is on status, check, and quarantine; the cited grammar still says it is not.
A dry-run of a path that does not exist writes nothing and says `dry_run=true`. init never replaces a box. status keeps `quarantines=` uncapped above a `STATUS MORE` line.

## What could not be tried
Every verb ran on a local box: init, status, check, lockdown, quarantine, lift quarantine, lift lockdown, path, version, help. No verb needs a service. A symlink box and a hand-edited folded key were not tried.
Guessed, before the cited grammar was compared: that check's line would open with CHECK (it opens with FUSE), and that a dry-run of an already-blown box would still print `dry_run=true` (it does not).
