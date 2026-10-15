# nova-sandbox AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: ee201d6267cc
Score: 8.5/10 (47/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-sandbox check` -> `CHECK OK backend=landlock abi=4 net=enforceable hosts=none` |
| 2 | refusals | 5 | `nova-sandbox --write <dir> --bogus -- /bin/true` -> `SANDBOX REFUSED reason=bad_flag: unknown flag --bogus; ... run: nova-sandbox help` |
| 3 | output | 5 | `nova-sandbox probe --write <dir> --json` -> `{"result":{"verb":"probe","status":"ok","exit":0},...}` |
| 4 | dry-run | 2 | `nova-sandbox worktree -h` -> no `--dry-run` flag listed for a write verb |
| 5 | idempotent | 1 | `nova-sandbox help` -> no `--op` flag on any verb |
| 6 | no prompts | 5 | `nova-sandbox --write <dir> -- /bin/true` -> ran to completion, never prompted |
| 7 | exit codes | 5 | `nova-sandbox frobnicate` -> `exit=2`, matching help's `2 could not run` |
| 8 | one word | 5 | `nova-sandbox help` -> verbs check probe policy run reap worktree egress version help, one action each, no duplicate |
| 9 | push | 5 | n/a: the wrap waits only on its own child process, no external state polled |
| 10 | no leaks | 5 | `nova-sandbox probe --write <dir> --secret <path>` -> prints the path, never the file contents |
| 11 | true help | 4 | `nova-sandbox --write <dir> --json -- /bin/true` -> `--json is not a flag of the bare form`, though the banner lists `--json` among the tool's flags |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox worktree -h` / `nova-sandbox egress plan -h` | write verbs with no `--dry-run`: a worktree or a generated ruleset is written with no way to preview it, so a caller cannot see what a write will do before it does it | add `--dry-run` to worktree and egress plan, printing the write plan and writing nothing | M |
| 2 | `nova-sandbox help` banner flag list vs `nova-sandbox --write <dir> --json -- /bin/true` | the banner lists `--json` among the tool's flags, but the bare form refuses it with `--json is not a flag of the bare form; it is check, policy and probe's`, so a reader who trusts the banner is sent to a refusal | constrain the banner's flag list to the verbs that take each flag (say `--json` is check, policy and probe's) | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox help` | no verb takes `--op`, so a retried write (egress apply twice, a repeated worktree) has no recorded-id idempotency | add `--op` idempotency to the write verbs, or document why the disposable model makes it unnecessary | L |
| 2 | `nova-sandbox egress plan` | plan resolves every allowlist name over the network each run, so an offline or slow forge turns a read into a 40s i/o timeout refusal; a pinned cache would make it reproducible | cache resolved names so plan can run offline against the last pin | M |
| 3 | `nova-sandbox --write <dir> -- /bin/sh -c 'echo x > relative/path'` | a command using a relative path fails with `Directory nonexistent` because cwd defaults to the first `--write`, which a cold reader may not expect | on a relative write that misses, hint the cwd default in the DONE line | S |

## Top 3 fixes
1. add `--dry-run` to worktree and egress plan (blocker 1) — moves criterion 4 up the most.
2. fix the banner flag list so `--json` is not claimed for the bare form (blocker 2) — removes the one help lie.
3. add `--op` idempotency or document its absence (nice-to-have 1) — recovers criterion 5.

## Good, keep
The wall is genuinely enforced and honest: `check` reports the real backend and abi, `probe` runs real allow/deny steps, and refusals every time name the cause and the exact next command in one line. Nothing is guessed: every path must exist, be absolute, and sit in the named set, and HOME must resolve inside a --write. `--json` on check, policy and probe is parseable and matches the typed line output, and probe's `--secret` design (prove unreadability, never read contents) keeps secrets out of every output line.
The six 1.2.0 nova-sandbox ratings under docs/ratings/1.2.0/ (2026-10-02) flag the read-noexec override of command dir, computed root uncounted on OK, -h examples that do not run, exit text vs reality (125/126), bare form listing other verbs' flags then refusing them, ABI NOTE before every run, probe control file in unnamed parent, reap 125 undocumented on linux; none of those are among the findings or blockers in this review (so either addressed on the path to this head, or not exercised by the probe set here).

## What could not be tried
run, reap and the darwin APFS path are darwin-only and refuse on this linux host (exit 125); egress plan could not complete because it must resolve allowlist names over a network this job does not have (i/o timeout); worktree needs a forge pull request and was judged from its help only; egress apply/drop need nft and a live bench table and were not run.
