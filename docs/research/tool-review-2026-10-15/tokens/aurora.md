# nova-tokens AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.5/10 (51/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-tokens` -> `TOKENS REFUSED: no verb given; ... run: nova-tokens help`; help then ran its example fold/check/sum/sources verbatim |
| 2 | refusals | 5 | `nova-tokens fold` (no flags) -> four `TOKENS REFUSED` lines, each naming what it wants and `run: nova-tokens help`; `--bogus`, unknown verb, bad `--month` all name cause + next command |
| 3 | output | 5 | `nova-tokens sources ...` -> `SOURCES OK sources=1 ... unattributed=-`; stable `VERB <status> k=v` line grammar; `--json` renders the same value as one JSON object |
| 4 | dry-run | 5 | `nova-tokens fold ... --dry-run` -> `TOKENS DAY ... written=false would_write=true ... dry_run=true`; `session --out --dry-run` left the out dir empty |
| 5 | idempotent | 1 | `nova-tokens fold --op abc ...` -> `TOKENS REFUSED: unknown flag --op; ...`; fold takes no `--op`, though a re-fold writes an identical day file |
| 6 | no prompts | 5 | every command ran to completion non-interactively; no prompt ever appeared |
| 7 | exit codes | 5 | `nova-tokens check --out ./out --through 2026-09-20` -> `CHECK FAILED stale last=2026-09-11 through=2026-09-20` exit 1; ok=0, refusal=2, matching the help's exit table |
| 8 | one word | 5 | verbs fold, report, ledger, sum, check, sources, profiles, session, version: each one word, none duplicated |
| 9 | push | 5 | n/a: no remote state was waited on; the only wait is a local fold.lock that refuses rather than polls |
| 10 | no leaks | 5 | no command output or refusal line printed a secret; the Redis password is never a flag, only `--password-env <NAME>` |
| 11 | true help | 5 | `report` separated its note body on stdout from `TOKENS AVG`/`REPORT OK` on stderr as documented; exit codes and `--json` claims held when run |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens fold --op abc ...` | `TOKENS REFUSED: unknown flag --op;` -- the write verbs take no `--op <id>`, so a timed-out retry cannot be made safe by an op id as the standard promises | register `--op <id>` on fold, ledger, session --out and report --note through pkg/tool, deduplicating on the op id | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens sources --bogus` | `did you mean --bus?` is an unreliable guess (arbitrarily picked from the flag list); a cold AI may chase the wrong flag | compute the nearest flag by edit distance, or drop the suggestion when none is close | S |
| 2 | `nova-tokens fold` then `ls ./out` | a persistent `fold.lock` file appears in the user's out directory that they never created; check ignores it but a cold user may not know why it is there | document `fold.lock` in the first-run transcript, or move it to a dotfile name | S |

## Top 3 fixes
1. Add `--op <id>` idempotency to the write verbs (fold, ledger, session --out, report --note) via pkg/tool.
2. Make the unknown-flag nearest-match hint suggest a genuinely close flag or say none.
3. Reduce the surprise of the persistent `fold.lock` (document it or rename to a dotfile).

## Good, keep
The refusal grammar names the next command on every refusal, and a multi-problem run reports all problems at once, so recovery is one paste.
`report` local mode keeps its note body on stdout and its status lines on stderr, so the note artifact pipes cleanly into a bus note.
The read-only verbs (sources, check, sum, report local, session) run over the shipped fixtures with no service, and exit codes match the help table exactly.
Old 2026-10-02 defects checked against this build: `--json` numbers are real numbers, `--dry-run` is the real plan, and `--max` bounds listings with a `MORE` line -- all still true. The missing-flag and bad-value refusals still end `run: nova-tokens help` (global) rather than the verb's own `-h` -- a known old friction, kept as a nice-to-have. The write verbs still take no `--op`, the gap this review's blocker names.

## What could not be tried
`profiles` (needs a swarm root directory with card-usage files; none shipped in testdata). `report --redis` and `ledger` (need a Redis store; not touched). `--opencode` (needs a sqlite3 OpenCode database). `--provider google|openai|xai` (needs provider usage export files). `--swarm` pool source (no shipped pool fixture); each judged from its help and, for the write verbs, from `--dry-run`.
