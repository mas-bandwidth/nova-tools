# nova-sandbox AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: d7e47f037ea8
Score: 9.5/10 (51/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-sandbox check` -> `CHECK OK backend=landlock abi=8 net=enforceable` |
| 2 | refusals | 5 | `nova-sandbox -- /bin/true` -> `SANDBOX REFUSED reason=bad_write: --write is required... run: nova-sandbox help` |
| 3 | output | 5 | `nova-sandbox probe --json --write <trial>` -> `{"result":{"verb":"probe","status":"ok","exit":0},...}` |
| 4 | dry-run | 3 | `nova-sandbox reap --dry-run` -> `SANDBOX REFUSED reason=no_sandbox` (reap is darwin-only; worktree and egress apply/drop have no --dry-run) |
| 5 | idempotent | 4 | `nova-sandbox probe --write <trial>` twice -> same `PROBE OK ... steps=4 passed=4` (no stateful tool writes; re-running the same command is deterministic) |
| 6 | no prompts | 5 | `nova-sandbox --write <trial> -- /bin/true` -> immediate `SANDBOX OK` / `SANDBOX DONE exit=0`, no prompt |
| 7 | exit codes | 5 | `nova-sandbox --write <trial> -- /bin/sh -c 'exit 7'` -> `SANDBOX DONE exit=7`, shell `$?` = 7; refusals exit 125, unknown verb exit 2 |
| 8 | one word | 5 | `nova-sandbox help` -> verbs `check, egress, policy, probe, reap, run, version, worktree`, each one word, no duplicates |
| 9 | push | 5 | `n/a: the tool runs one synchronous command and waits on no external state, so there is nothing to poll` |
| 10 | no leaks | 5 | `nova-sandbox --write <trial> -- /bin/sh -c 'echo hi'` -> no secret in any line; denial lines name only paths |
| 11 | true help | 4 | `nova-sandbox --write <trial> -- <noexec>` -> `SANDBOX REFUSED reason=not_executable` exit 125, but help says ``126 the command could not be executed`` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox --frobnicate` | the unknown-flag refusal lists bare-form flags including `--json` and `--max`, then `nova-sandbox --json --write <trial> -- true` refuses them as "not a flag of the bare form" - the suggested flags lead to a second refusal | name only the real bare-form flags in the unknown-flag list (leave `--json`/`--max`/`--secret` to the subverbs) | S |
| 2 | `nova-sandbox --write <trial> -- <noexec.sh>` | help says ``126 the command could not be executed``, but a non-executable or nonexistent command path exits 125 with `reason=not_executable` - two different documented exit meanings for the same failure | either make non-executable commands exit 126 or amend the help to class them as a 125 refusal | S |
| 3 | `nova-sandbox --write <trial> -- /bin/true` | every successful run prints an undocumented `SANDBOX NOTE landlock abi 8 ...` line before `SANDBOX OK`; a new AI parsing the output grammar must silently skip it | gate the abi note behind a verbose flag, or fold it into the `SANDBOX OK` line | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox worktree` | the only non-darwin write verb has no `--dry-run`, so there is no preview before it creates a git worktree | add `--dry-run` to worktree that prints what it would create and touches nothing | S |
| 2 | `nova-sandbox --write <trial> -- /bin/true` | `SANDBOX OK` prints `read=0` even though the resolved command's own directory is implicitly granted as an executable root, so the help line "Every path is yours and none is guessed" is not quite literal | count the command's resolved read root in `read=` or note it in the help | S |
| 3 | `nova-sandbox help` | `run`, `reap` and `egress` are platform/bench-bound; their refusal texts are excellent but a cold reader must reach them to learn they are unavailable here | add one line in the top help marking darwin/bench-only verbs | S |

## Top 3 fixes
1. Fix the unknown-flag list to name only real bare-form flags, so a suggested flag never itself refuses.
2. Align the 126 claim with behaviour: non-executable commands exit 125, not 126.
3. Gate or fold the abi NOTE so a normal run emits only the documented SANDBOX OK/DONE lines.

## Good, keep
- Every refusal names its cause and the exact next command, and several errors are reported at once; this is the strongest part of the tool.
- The first-run example in the help works exactly as written - `check`, `probe`, then a bare run - so a cold AI is productive in under a minute.
- `--json` on check/probe/policy is valid and stable, exit codes pass through faithfully, and the wall's read/write/net denials all hold.

## What could not be tried
- `run` (darwin APFS volume), `reap` (darwin), and `egress apply/drop/check` (need the bench and nftables) - judged from their help and their refusals, both of which are clear and name a viable follow-on.
- `worktree` - it creates a git worktree in the named repo, which would write live state, so it was not run.
- Real outbound network was not attempted (no network in this job); only `--net-deny` and the probe's network checks were exercised.
