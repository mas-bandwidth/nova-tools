# nova-fuse AI-usability review, 2026-10-15

Reviewer: x-ai/grok-build-0.1
Build: 15302296f439
Score: 8.5 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-fuse init --box ./fuse-box.json` -> `INIT OK box=./fuse-box.json: an empty box, no fuse blown (verified by re-reading the box)` |
| 2 | refusals | 3 | `nova-fuse status --box ./no.json --max abc --foo bar` -> `nova-fuse status REFUSED: unknown flag --foo; the flags of status are --box, --json, --max; run: nova-fuse help status` (stops at first) |
| 3 | output | 5 | `nova-fuse status --box ./b.json --json` -> `{"result":{"verb":"status","status":"ok","exit":0},"facts":{"lockdown":"clear","quarantines":1},...` |
| 4 | dry-run | 4 | `nova-fuse lockdown --dry-run --box ./already.json "x"` (after prior lockdown) -> `LOCKDOWN OK already=blown ...` (no dry_run=true, unlike help claim) |
| 5 | idempotent | 5 | `n/a: no --op flag on any write verb (help lists only --box --dry-run --json --max)` |
| 6 | no prompts | 5 | `n/a: no interactive surface or prompt in help or any run` |
| 7 | exit codes | 5 | `nova-fuse check --box ./real1.json` -> exit 1 `FUSE FAILED lockdown...` ; unknown flag -> 2 with remedy |
| 8 | one word | 3 | `nova-fuse lift quarantine --box b s` (usage has two-word verbs); unknown lists "lift" |
| 9 | push | 5 | `n/a: no waiting on state; all file ops are sync with re-read verify` |
| 10 | no leaks | 5 | `nova-fuse status --box ./no.json` -> `no box at ./no.json ...` (only user path) |
| 11 | true help | 3 | `nova-fuse help` says dry-run line "says dry_run=true"; `lockdown --dry-run` on already prints without it; also help claims --json support but SPEC (linked) says "There is no --json"; surface display escapes cause copy-paste wrong clear |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse status --box ./no.json --max abc --foo bar` | `nova-fuse status REFUSED: unknown flag --foo; the flags of status are --box, --json, --max; run: nova-fuse help status` (only first; --max and --box not reported) | report every flag/value problem in one refusal | S |
| 2 | `nova-fuse check --box real1.json > out.txt` (real1 has lockdown) | stdout empty, `FUSE FAILED` only on stderr | emit the result line to stdout too (keep exit 1); document streams in help check | S |
| 3 | `nova-fuse lockdown --dry-run --box ./already.json "x"` (box already blown) | `LOCKDOWN OK already=blown since=... (standing record kept...)` (no dry_run=true) | put dry_run=true and "nothing written" on already= lines | S |
| 4 | `ls` after any write | `box.json.lock` 0-byte sidecar appears; never named in `help`, CLI or banner | document `<box>.lock` in help for writes; say it is safe to ignore | S |
| 5 | `nova-fuse check -h` and `nova-fuse status -h` | no mention of normalization/folding or recommended spelling | state folding and "use spelling from FUSE FAILED/LIFT remedy" in check/status help | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | all write verbs | no --op for cross-tool idempotency and retry safety | add --op (no-op today) | S |
| 2 | `nova-fuse path` | prints bare path; no grammar token | document "payload, not event" | S |

## Top 3 fixes
1. report every problem at once in refusals (flag parse stops early)
2. emit blown FUSE line to stdout (or --json) so capture gets the result
3. document or clean the .lock sidecar; carry dry_run marker on already= lines

## Good, keep
- every refusal names cause + next command (run: ...); absent/unreadable box refused at 2, treated BLOWN never CLEAR
- dry-run on writes that can, writes nothing; every write re-verifies by re-read and announces "verified"
- exit 0 only permission; check gate says out loud when no surface checked; lift lockdown refused by design with live-convo remedy; re-quarantine on same keeps first record and says "already=... standing record kept"
- status count never capped under --max, MORE line, --max 0 lists all; folding works both directions (case/ws); -- keeps untrusted dash as data

## What could not be tried
n/a: all verbs tried from help (version, init, status, check, lockdown, quarantine, lift quarantine, lift lockdown, path); no external service required
