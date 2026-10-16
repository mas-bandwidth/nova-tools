# nova-fuse AI-usability review, 2026-10-15

Reviewer: Mercury-3 (inception/mercury-3-preview-1002)
Build: d7e47f037ea8
Score: 10/10 (55/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-fuse init --box ./fuse-box.json` -> `INIT OK box=./fuse-box.json: an empty box, no fuse blown (verified by re-reading the box)` |
| 2 | refusals | 5 | `nova-fuse check` -> `nova-fuse check REFUSED: --box is required; refusing to guess; run: nova-fuse help` |
| 3 | output | 5 | `nova-fuse status --box ./fuse-box.json --json` -> `{"result":{"verb":"status","status":"ok","exit":0},"facts":{"lockdown":"clear","quarantines":0}}` |
| 4 | dry-run | 5 | `nova-fuse init --box ./dry-box.json --dry-run` -> `INIT OK box=./dry-box.json dry_run=true: nothing written; a real run would make an empty box there` |
| 5 | idempotent | 5 | `nova-fuse quarantine --box ./fuse-box.json a-forum "reason x"` -> `QUARANTINE OK a-forum already=quarantined since=2026-10-15T19:30:03Z: reason x (standing record kept; the new reason was not recorded: reason x)` |
| 6 | no prompts | 5 | `nova-fuse check --box ./fuse-box.json < /dev/null` -> `FUSE OK lockdown=clear (no surface named; no quarantine checked)` |
| 7 | exit codes | 5 | `nova-fuse check --box ./fuse-box.json a-forum` (surface quarantined) -> `EXIT=1` |
| 8 | one word | 5 | `nova-fuse frobnicate` -> `nova-fuse REFUSED: unknown verb "frobnicate"; the verbs are init, status, check, lockdown, quarantine, lift, path, version, help` |
| 9 | push | 5 | `n/a: local single-file tool; no remote state is waited on or polled` |
| 10 | no leaks | 5 | `nova-fuse status --box ./fuse-box.json` -> `STATUS OK lockdown=blown since=2026-10-15T19:30:01Z quarantines=2: test reason` |
| 11 | true help | 5 | `nova-fuse status -h` -> `nova-fuse status REFUSED: -h is refused here so it can never read as CLEAR; run: nova-fuse help status` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| - | - | none found | - | - |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse frobnicate` | unknown-verb refusal lists verbs as `init, status, check, lockdown, quarantine, lift, path, version, help`, while the help usage spells two of them `lift quarantine` and `lift lockdown`; the mapping from `lift` to both is not obvious to a new reader | list the two-word verbs in the refusal: `lift quarantine, lift lockdown` | S |
| 2 | `nova-fuse status --box ./fuse-box.json --max abc` | refusal wording `invalid value for --max: it wants quarantine lines to list before one MORE line stands for the rest; 0 lists all` is grammatically confusing for a first-time reader | reword to `invalid value for --max: expected an integer; 0 lists all` | S |
| 3 | `nova-fuse --help` | the help is a dense ~60-line block; a cold AI must read it all before finding the urgency verb | add a one-line synopsis at the top (e.g. `a fuse-box gate: init, status, check, quarantine, lift`) | S |

## Top 3 fixes
1. Re-list the two-word verbs (`lift quarantine`, `lift lockdown`) in the unknown-verb refusal so a newcomer maps them.
2. Reword the `--max` bad-value refusal to say `expected an integer; 0 lists all`.
3. Add a one-line synopsis atop the main help for a faster cold scan.

## Good, keep
The `--box` is the only path source, with no default and no environment variable; the "refusing to guess" stance makes the tool safe by construction and is worth preserving. The example block in help is runnable verbatim and gave a clean first run. The refusal pattern `REFUSED: <cause>; run: nova-fuse help [verb]` is consistent across every error class. `--dry-run` and `--json` are offered on every write and inspection verb that needs them, and both behaved exactly as help states. The exit-code contract (0 clear, 1 blown or unverified write, 2 could-not-run) is stable and matches behavior.

## What could not be tried
None: the tool is fully local and every verb was run. No service or remote dependency was required, so nothing was judged from help alone beyond what was executed.
