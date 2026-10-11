# nova-sandbox READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 (Claude Code), a Claude worker given the Grok friend card; this is not Grok's own read
Build: ba3d867c0e31
READ: 7/10
USE: 6/10

Question: is this a good tool for an AI to use?

## Reasons

READ. `nova-sandbox help` opens with one sentence of purpose, a how-it-works paragraph that says what is readable, what is writable and what 125 means, and a first-run block in order (check, make a scratch, probe, run). Every flag carries its reason, not just its type: `--write` is required because "a command with no writable directory is a misconfiguration", `--read` "CARRIES EXECUTE", `--read-noexec` is "the flag for a cache or a data tree". The exit table separates the wrapped command's own status from the tool's. A cold reader can wrap a command correctly from help alone. What keeps it from 10: help is long (the run, reap and egress sections are all in the one page), and the short texts disagree with the binary. The `run` usage line (cmd/nova-sandbox/main.go:63) leaves out `--out`, `--artifact`, `--max-procs` and `--max-mem`, and `run --help` calls `--out` the flag that keeps a commit. `--gpu` is accepted but is missing from the bare usage line (cmd/nova-sandbox/main.go:58). The `egress plan -h` example (cmd/nova-sandbox/verbhelp.go:82) leaves out the required `--resolver`, and the `policy -h` example (cmd/nova-sandbox/verbhelp.go:78) names `/usr/local/go`, so both exit 2 as printed. `--max` appears in the bare form's list of flags but in no help text. Help never says that the directory of the resolved command becomes a read root, and that is the one fact that changes what the wall allows (see USE).

USE. On a scratch directory on a Linux bench (landlock, kernel abi 8 clamped to 6), the first run worked as printed: `check` exit 0, `probe --write` exit 0 with four named steps, five with `--secret`, and `--json` on both. A wrapped `/bin/sh -c 'echo inside > out'` wrote inside and printed SANDBOX OK and SANDBOX DONE exit=0. The wall held for every escape I tried from inside a shell: a write beside the --write, a read of a file outside every grant, a write to /dev/shm, a listing of the real home, and with `--net-deny` a curl to port 443 (connect refused, exit 7 passed through). Exit codes pass through as documented (7, 126, 127, 137). Refusals are mostly very good: HOME outside the wall prints the exact `mkdir -p ... && HOME=... nova-sandbox ...` to run next, and a relative `--write` prints the absolute path it would be. `egress plan` against the real allowlist wrote a 26-line nftables table, and `egress check` caught each tampering I made (metadata deny removed, the final drop removed, a port opened, a rule unscoped), naming the line, exit 1.

The score is held down by one containment gap and a mixed refusal grammar. The directory of the resolved command is granted read AND execute (pkg/sandbox/policy.go:809, the roots table in docs/SPEC-SANDBOX.md), and the SANDBOX OK line still says `read=0`. So `nova-sandbox --write $JOB -- $DATA/leak.sh`, with no grant on `$DATA`, printed the contents of `$DATA/secret.txt`. Under `--read-noexec $DATA`, a binary in `$DATA` named as the command still ran, while the same binary started from a shell inside the wall was denied with 126. `policy` printed `read=$DATA` and `read-noexec=$DATA` together and exit 0, while the typed form `--read $DATA --read-noexec $DATA` is refused (pkg/sandbox/policy.go:700). A `--read-noexec` nested inside a `--write` is accepted, and a program under it ran: only equal paths are refused (pkg/sandbox/policy.go:704). The refusal reason codes are not reliable keys. An unknown flag to egress and `--json` on the bare form both say `reason=no_command`. Probe refusals say `reason=check` with the real code in parentheses. Several refusals (a relative path, both-lists collisions, `--net-allow`, `--gpu`) end with no `run:` next step. `--json` exists on check, policy and probe, and is refused on version, worktree and egress.

Not tried: `run` and `reap` (darwin only; on Linux both refuse with `no_sandbox`, exit 125), `egress apply` (it would change the bench's firewall), and `worktree --pr` against a forge. `worktree --prune` ran on an empty scratch: `WORKTREE OK removed=0 kept=0`.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | pkg/sandbox/policy.go:809 | The directory of the resolved command is a read+exec root that overrides a `--read-noexec` on the same directory. The command in it ran, `policy` printed `read=` and `read-noexec=` for one path, exit 0, and a script there read a sibling secret with no grant named. | Refuse when the command's directory is inside a `--read-noexec`, as the typed `--read`/`--read-noexec` collision is refused. | M |
| 2 | cmd/nova-sandbox/main.go:535 | SANDBOX OK says `read=0` while the command's directory is readable, and help never says the command's directory is granted. | Print that root on the OK line (for example `cmdroot=<dir>`) and say it in help beside `--read`. | S |
| 3 | pkg/sandbox/policy.go:704 | A `--read-noexec` nested inside a `--write` is accepted and buys nothing: a program under it ran. Only equal paths are refused. | Refuse a `--read-noexec` that is inside any `--write` (Inside, not ==). | S |
| 4 | cmd/nova-sandbox/verbhelp.go:82 | The `egress plan` example leaves out the required `--resolver` and names a host that is not in the allowlist, so it exits 2. Same at verbhelp.go:81. | Show `--resolver <ip>` and a host the allowlist carries. | S |
| 5 | cmd/nova-sandbox/verbhelp.go:78 | The `policy` example's `--read /usr/local/go` is refused, exit 2, wherever that path is absent. | Use a placeholder the reader replaces, or a path the example itself creates. | S |
| 6 | cmd/nova-sandbox/main.go:63 | Help's `run` usage leaves out `--out`, `--artifact`, `--out-max-bytes`, `--max-procs` and `--max-mem`, which `run --help` documents; without `--out` a run loses its commit. | List them, or end the line with "run --help for the rest". | S |
| 7 | cmd/nova-sandbox/main.go:405 | The bare form's unknown-flag refusal lists `--secret`, `--json` and `--max` as its flags, then refuses each one as "not a flag of the bare form". `--max` is in no help text. | List only the bare form's flags; document `--max` in `probe -h` or remove it. | S |
| 8 | cmd/nova-sandbox/main.go:58 | `--gpu` is accepted by the bare form, probe and policy, but appears in none of their usage lines. | Add `[--gpu none|metal]` to the three usage lines. | S |
| 9 | cmd/nova-sandbox/main.go:385 | Reason codes do not match the problem: `no_command` for a bad `--max`, for `--json` on the bare form and for an unknown egress flag; probe prints `reason=check` with the real code in parentheses (main.go:688). | `reason=` carries the real code (bad_flag, bad_write, ...) on every verb. | S |
| 10 | pkg/sandbox/policy.go:509 | Some refusals end with no next step: a relative path, the both-lists collisions (policy.go:700-706), a bad `--net-allow` or `--gpu`, probe with no `--write`. | End every refusal with `run: <command>`, as home_outside does. | S |
| 11 | cmd/nova-sandbox/main.go:193 | Help offers `reap --dry-run` as a gate with codes 0 and 3. Off darwin it exits 125 `no_sandbox`, a code reap does not document. | Document the off-darwin code in reap's exit line. | S |
| 12 | cmd/nova-sandbox/egress.go:347 | `egress drop` of a table that does not exist exits 1 "nft said NO", with nft's text escaped as `\x0a`. A cleanup verb fails on a clean machine. | Treat an absent table as done (exit 0, `absent=true`), and print nft's error on one readable line. | S |
| 13 | cmd/nova-sandbox/egress.go:274 | `egress check` accepts `--run`, which `check -h` does not list, and ignores it: `--run zz` on the r1 plan exits 0. | Refuse `--run` on check, or compare it with the plan's table. | S |
| 14 | cmd/nova-sandbox/main.go:249 | `--json` is refused on version, worktree and egress, while check, policy and probe take it. | `--json` on every verb, with the same result shape. | M |
| 15 | cmd/nova-sandbox/main.go:523 | A 250-character abi-clamp NOTE prints before every wrapped run on a newer kernel; `check` already says the same thing and the OK line carries `used=6`. | Say it in check only, and keep `used=` on the OK line. | S |
| 16 | `nova-sandbox egress plan` | Refusals do not arrive all at once: a missing `--resolver` hides `bad_model_host`, which appears only on the next run. | Validate every flag before returning, as the bare form does. | S |

## Good, keep

The wall held for every escape tried from inside a wrapped shell: a write outside, a read of an ungranted file, /dev/shm, the real home, and the network under `--net-deny`.
The home_outside refusal prints the exact command to run next, with the paths filled in, and a relative path is refused with the absolute path it would have been.
`probe` proves the wall in named steps with expect and got, and `--json` carries the same facts.
`egress check` names the line and the rule it broke for each tampering, and exits 1 (the verb said NO) as distinct from 2 (could not run).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| none | first rating | docs/ratings/1.1.0 and docs/ratings/snapshots hold no nova-sandbox rating; this is the baseline for the next one |
