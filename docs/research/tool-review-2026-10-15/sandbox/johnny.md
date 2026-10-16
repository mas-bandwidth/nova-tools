# nova-sandbox AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9/10 (49/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `HOME=$PWD/trial/home nova-sandbox probe --write $PWD/trial` -> `PROBE OK backend=landlock abi=4 steps=4 passed=4 net=nopromise gpu=none`; the help's example block ran as printed on the first try |
| 2 | refusals | 5 | `nova-sandbox --net-deny --net-listen --write $PWD/trial -- /bin/true` -> `SANDBOX REFUSED reason=bad_net: --net-deny and --net-listen together: ...; run: nova-sandbox help`; every refusal names cause and next command, and policy named two problems at once |
| 3 | output | 5 | `nova-sandbox check --json` -> `{"result":{"verb":"check","status":"ok","exit":0},"facts":{...}}`; lines are stable `KEY=value`, status word leads, and --json exists where help offers it (check/policy/probe) |
| 4 | dry-run | 3 | `nova-sandbox reap --dry-run` -> `SANDBOX REFUSED reason=no_sandbox: ... run: nova-sandbox --write <dir> -- ...`; only reap lists --dry-run; run, worktree and egress plan do not in their -h |
| 5 | idempotent | 2 | `nova-sandbox run -h` / `worktree -h` / `egress -h` list no `--op`; no write verb takes an op id, so a retry cannot be made safe by the tool |
| 6 | no prompts | 5 | n/a: never reads stdin or waits for an answer; every run, including the wrap, ran unattended |
| 7 | exit codes | 5 | `nova-sandbox --write $PWD/trial -- /bin/sh -c 'cat ../secret.txt'` -> `EXIT=1` (child code passed through); a usage error gave `EXIT=125`, verb usage 2, check 0, all as the banner states |
| 8 | one word | 5 | verbs probe, policy, check, run, reap, worktree, egress, version, help are all single words; egress subverbs are scoped under it |
| 9 | push | 5 | n/a: the tool waits synchronously on the child command; there is no polled state interface to push |
| 10 | no leaks | 5 | no output line carries a secret; the `SANDBOX OK` line's `cwdb64=` is base64 of the cwd, not a secret |
| 11 | true help | 4 | help promises one `SANDBOX DENIED` line per path when a contained command exits non-zero; on a real denial none printed: `nova-sandbox --write $PWD/trial --read $PWD/R -- /bin/sh -c 'touch $PWD/R/x'` -> `touch: cannot touch '.../R/x': Permission denied` with `SANDBOX DONE exit=1` and no DENIED line |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox run -h`, `worktree -h`, `egress plan -h` | none of the write verbs takes `--op`, so a retry after a timeout or a crash has no tool-provided idempotency; the AGENTS.md standard says a write verb takes `--op <id>` | give the stateful writes (worktree, egress plan) an `--op <id>` that replays the recorded result on a repeat | M |
| 2 | `nova-sandbox run -h`, `worktree -h`, `egress plan -h` | only reap advertises `--dry-run`; run (writes/deletes a volume), worktree (creates worktrees and records) and egress plan (writes a ruleset) have no way to preview before writing | add `--dry-run` to run, worktree and egress plan, printing the plan from the same code path and writing nothing | M |
| 3 | `nova-sandbox --write $PWD/trial --read $PWD/R -- /bin/sh -c 'touch $PWD/R/x'` | the child is denied with `touch: cannot touch '...': Permission denied`, but the promised `SANDBOX DENIED` line does not print, so an AI debugging a failing job does not learn which path was refused or which flag would allow it | on any non-zero child exit, emit the promised `SANDBOX DENIED path=... flag=...` line so the refusal names the remedy | M |
| 4 | `nova-sandbox probe --write $PWD/trial --max 1` | `--max` is accepted and inert (all four steps still print with no `MORE` line), and `probe -h` omits `--max` entirely; this defect is carried from the 2026-10-02 ratings | bound probe's step list with a `MORE shown=<n> total=<n>` line and print `--max` in `probe -h` | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox check` | the check's `note=` field embeds a long sentence about landlock abi 4 in the facts line, which muddies parsing | move the note to a `NOTE` line on stderr, keeping `CHECK OK` facts parseable | S |
| 2 | `nova-sandbox policy -h` | the verb prints only the ruleset; it does not state on the line whether the net is nopromise or denied when `--net-deny` is passed | include `net=` in the `POLICY OK` line as the wrap's `SANDBOX OK` line does | S |
| 3 | `nova-sandbox help` | the `--gpu` flag appears only in the long banner and in run/probe/policy -h, not in the compact usage synopsis at the top | add `--gpu <n-or-m>` to the synopsis so a cold reader sees it | S |

## Top 3 fixes
1. Add `--op <id>` to the stateful writes (worktree, egress plan) so a retry is idempotent; this moves criterion 5 from 2 to 5.
2. Add `--dry-run` to run, worktree and egress plan so every write can be previewed; this moves criterion 4 from 3 to 5.
3. Emit the promised `SANDBOX DENIED` line on non-zero child exits so a failing job names the refused path and the allowing flag; this restores criterion 11.

## Good, keep
The cold path is excellent: the banner's example block runs as printed, and probe followed by a wrap is a working first run with no other doc.
Refusals always name the cause, list every problem at once, and print a copy-pastable `run:` remedy (including for HOME outside --write).
The one output value renders as both stable `KEY=value` lines and `--json` from the same facts, so the two cannot drift.
The containment gap the 2026-10-02 ratings carried (a command inside a `--read-noexec` root still ran) is gone: naming such a command now refuses with exit 125 and a message naming the union conflict and the remedy.

## What could not be tried
`run`, `reap` (as a real non-dry run) and the APFS/windows disposable place: darwin/windows only, not present on this linux host; judged from their -h and the `reap --dry-run` refusal.
`worktree`: needs a forge pull request and a repository under --scratch; judged from its -h.
`egress plan/apply/check/drop`: apply/check/drop need nft and linux root, and plan resolves policy names over the network, which this card does not touch; judged from the -h and the refusal grammar.
`--net-allow`, `--gpu metal`, `--secret` probe path: no local provider or key file to exercise them against.
