# nova-up AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9/10 (50/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-up` -> `UP REFUSED: no verb and no file given; the verbs are up, version; run: nova-up help`; `nova-up up --seat --dry-run --sprint-redis 127.0.0.1:6380 --server 127.0.0.1:6390 --bus-redis 127.0.0.1:6380` -> `UP OK fields=3 dry_run=true` |
| 2 | refusals | 5 | `nova-up up --local --root` -> `UP REFUSED: --root needs a value: ... run: nova-up up -h`; `nova-up up --bogus` -> `UP REFUSED: unknown flag --bogus; the flags of up are ...; run: nova-up up -h`; `nova-up bogus` -> `UP REFUSED: "bogus" is no verb ...; run: nova-up help`; `nova-up up --seat --sprint-redis notanaddr ...` -> `UP REFUSED: invalid password-free host:port endpoints ...; run: nova-up help` |
| 3 | output | 5 | `nova-up up --local --dry-run --root ./try` -> `UP binaries missing redis-server,sops,age-keygen,nova-sprint not on PATH or not answering its version; install: ...`; `nova-up up --local --dry-run --root ./try --json` -> `{"result":{"verb":"up","status":"failed","exit":1,"why":["missing: binaries; ..."]},"facts":{"root":"...","steps":9,"changes":8,"applied":0},"payload":"..."}` |
| 4 | dry-run | 5 | `nova-up up --seat --dry-run --sprint-redis 127.0.0.1:6380 --server 127.0.0.1:6390 --bus-redis 127.0.0.1:6380` -> `UP NOTE dry run: seat environment validated; nothing written`; after `nova-up up --local --dry-run --root ./try`, `ls try` -> `ls: cannot access 'try': No such file or directory` |
| 5 | idempotent | 3 | `nova-up help` -> `A second run over an applied machine prints ok on every line and UP UNCHANGED`; no `--op` flag exists on `up`; a second apply could not be run (applying needs the missing binaries and writes live state, forbidden here), so same-op-again-changes-nothing is claimed but unverified |
| 6 | no prompts | 5 | every run above returned immediately, nothing waited on stdin |
| 7 | exit codes | 5 | `nova-up version` -> `exit=0`; `nova-up up --local --dry-run --root ./try` -> `exit=1`; `nova-up bogus` -> `exit=2`; match help's `0 done or nothing to change, 1 a step is missing or failed, 2 could not run (a flag)` |
| 8 | one word | 5 | verbs are single words `up`, `version`, `help`; no duplicate verbs or tools |
| 9 | push | 5 | n/a: the plan it scans local state and writes; nothing external is polled or waited on |
| 10 | no leaks | 5 | `nova-up up --local --dry-run --root ./try --json` -> `UP redis create 127.0.0.1:6390 loop=redis-local ...: unit create, passwords to seal=4, acl apply, fn load` (counts only, no secret); no output line printed a secret |
| 11 | true help | 3 | help says `Every verb takes --json`, but `nova-up help --json` -> usage text (not JSON) - `exit=0`; help says `A verb that lists takes --max ... and says MORE`, but no verb of nova-up lists and `nova-up up --max abc` -> `UP REFUSED: unknown flag --max` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-up help --json` | help claims `Every verb takes --json`, but `help --json` prints the usage text, not one JSON object; an AI that pipes help into `--json` gets unparseable text | either make the help verb honor `--json` or narrow the claim to the work verbs `up` and `version` | S |
| 2 | `nova-up help` | the synopsis says `A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest`, but nova-up has no list verb; `nova-up up --max abc` -> `UP REFUSED: unknown flag --max`, so the sentence promises a surface that is not there | drop the `--max`/list-verb sentence from nova-up's help (template leftover) | S |
| 3 | `nova-up up --seat --dry-run --sprint-redis notanaddr --server 127.0.0.1:1 --bus-redis 127.0.0.1:2` | on a refused run the tool prints `UP NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written`, which tells an AI a dry-run may have written when a refused plan writes nothing | print this note only when the verb actually writes (or drop the "may have written" half) | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-up up --local --dry-run --root ./try` | the failing plan prints its `UP FAILED ... changes=8 ...` summary before the per-step lines, and lists create/change steps that never ran; the `changes=8` count includes steps that were not applied | print the summary after the step lines, or count only steps that would actually change on the next real run | S |

## Top 3 fixes
1. Make `help --json` honor `--json` (or correct the `Every verb takes --json` claim) so an AI can parse help as JSON.
2. Remove the `--max`/list-verb boilerplate from help, which describes a surface nova-up does not have.
3. Stop claiming a refused dry-run "may have written"; keep the no-write guarantee unambiguous.

## Good, keep
The bare invocation and every refusal name the cause and the exact next command, and the `up -h` flags list is complete. The `--json` output is stable, single-line, and parseable, and the `--dry-run` plan genuinely writes nothing. Exit codes match the help under all conditions tried.

## What could not be tried
Applying `up --local` (real run) was not run: it needs redis-server, sops, age and nova-sprint on PATH (missing here) and would start systemd user units and write under a live root, which this review may not touch; it is judged from `--dry-run` and help only. Applying `up --seat` (real write of the per-user seat profile) was not run because it writes live per-user state; judged from `--dry-run` only.
