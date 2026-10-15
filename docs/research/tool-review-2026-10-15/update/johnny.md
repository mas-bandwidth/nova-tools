# nova-update AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 8.5/10 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-update example --out versions.tsv` -> `EXAMPLE OK wrote=versions.tsv entries=1 unchanged=false` |
| 2 | refusals | 5 | `nova-update check` -> `CHECK REFUSED: missing --file; refusing to guess; run: nova-update check -h` |
| 3 | output | 5 | `nova-update status --file versions.tsv --json` -> `{"result":{"verb":"status","status":"ok",...}}` |
| 4 | dry-run | 3 | `nova-update apply --file versions.tsv go --dry-run` -> `APPLY NOTE dry run: nothing installed, nothing written`; but `nova-update example --dry-run` -> `EXAMPLE REFUSED: unknown flag --dry-run` |
| 5 | idempotent | 2 | `nova-update apply --file versions.tsv go --dry-run --op xyz` -> `APPLY REFUSED: unknown flag --op` |
| 6 | no prompts | 5 | every run returned to the shell without waiting on input |
| 7 | exit codes | 5 | `nova-update check --file old.tsv` -> `CHECK FAILED ... exit: 1`; `nova-update check` -> `exit: 2`; exits 0/1/2 match help |
| 8 | one word | 5 | verbs example, check, status, apply, report, watch, adoption, release, version are single words, no duplicates |
| 9 | push | 5 | watch and report --send deliver through nova-bus (push), never poll |
| 10 | no leaks | 5 | outputs carried no secret material across every refusal, report and json run |
| 11 | true help | 3 | `nova-update help` says `check and report compare the two`, but `nova-update report -h` says `no latest, no network` and report prints installed only; help says the first status word is `OK, FAIL or REFUSED` but the binary prints `CHECK FAILED` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-update apply --file versions.tsv go --dry-run --op xyz` | `APPLY REFUSED: unknown flag --op; the flags are --dry-run, --file, --json, --timeout, --version` — the one real write verb has no op id, so a retry after a timeout is not safe | add `--op <id>` to apply, per the family write-verb standard, and echo the recorded result on a repeat | S |
| 2 | `nova-update help` | banner says `check and report compare the two`, but `report -h` says `no latest, no network` and report prints only the installed side — an AI reading the banner expects report to compare against latest | make the banner say check and status compare, report prints what this box runs | S |
| 3 | `nova-update check --file old.tsv` | help says the first status word is `OK, FAIL or REFUSED`; the binary prints `CHECK FAILED`, so an AI matching the documented word misses every failure | make help and binary one spelling (held by a test) | S |
| 4 | `nova-update example --dry-run` | `EXAMPLE REFUSED: unknown flag --dry-run` — a verb that writes has no dry-run; `report --snapshot` likewise writes a state file with no `--dry-run` | add `--dry-run` to example and to report's snapshot write | S |
| 5 | `nova-update report --file versions.tsv --draft --as me --to you` | output begins `From: me` then `To: you` before the `REPORT OK` line, so an AI expecting the verb line first must skip the note envelope | emit the verb result line first, or document the draft envelope shape | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-update version` and `nova-update --version` | the same release is reachable two ways, a small surface duplication | keep one canonical spelling and mention the other once | S |
| 2 | `nova-update help` usage line | usage lists `release <cut|build|install|adopt|pull>`; `release -h` shows a sixth verb, `cycle` | name cycle in the usage line | S |
| 3 | `nova-update report --store <host:port>` | fleet-store mode is judged from help only because touching Redis is out of scope | document a store fixture a cold run can try | S |

## Top 3 fixes
1. Add `--op` to `apply` (moves the idempotency criterion, the largest gap).
2. Fix the help claims that disagree with the binary: the banner's report claim and the `OK, FAIL / FAILED` spelling.
3. Add `--dry-run` to `example` and `report --snapshot` (closes the dry-run gap).

## Good, keep
The refusal grammar is exemplary: every refusal names the cause and the exact next command, and file-level problems are all reported at once. The line grammar is stable and `--json` mirrors it from the same value, so an AI can parse either. Exit codes 0/1/2 are meaningful and match the help exactly, and the `example:` block is a real first run that works as printed.

## What could not be tried
- `apply` real run: it installs a binary; a download/install is out of scope for this card. Judged by `--dry-run` and help.
- `release` subcommands (cut, build, install, adopt, pull, cycle): a release pipeline touching network, ssh and a store; judged from help only.
- `report --store`: reads a fleet Redis; touching a live store is out of scope.
- `report --send` and `watch` with delivery: deliver through nova-bus on a live bus; judged from help and `--draft`.
