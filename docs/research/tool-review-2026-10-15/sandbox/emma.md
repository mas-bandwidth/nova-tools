# nova-sandbox AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: b930c415a495
Score: 8.0/10 (44/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-sandbox check` -> `CHECK OK backend=landlock abi=8 net=enforceable hosts=none ...` (exit 0) and `HOME=/home/nova/scratch/trial/home nova-sandbox --write /home/nova/scratch/trial -- /bin/echo inside-sandbox` -> `SANDBOX DONE exit=0 cmd=echo` (exit 0), though `run -h` and `reap -h` on Linux print `(darwin)` APFS help |
| 2 | refusals | 4 | `nova-sandbox --write /home/nova/scratch/trial -- /bin/true` -> `SANDBOX REFUSED reason=home_outside: ... run: mkdir -p /home/nova/scratch/trial/home && HOME=/home/nova/scratch/trial/home nova-sandbox --write /home/nova/scratch/trial -- /bin/true` (exit 125); `egress apply` and `egress drop` refusals point to `egress plan` |
| 3 | output | 3 | `nova-sandbox check --json` -> `{"result":{"verb":"check","status":"ok","exit":0},"facts":{...}}` (exit 0), but `version`, `worktree`, `reap`, `run`, `egress`, and the bare wrap refuse `--json`, and every wrapped run on kernel ABI 8 emits a `SANDBOX NOTE landlock abi 8 is above this tool's table...` preamble line |
| 4 | dry-run | 3 | `nova-sandbox policy` previews the bare wrap's ruleset without running a command and `reap` takes `--dry-run`, but `run`, `worktree`, `egress plan`, `egress apply`, and `egress drop` refuse `--dry-run` |
| 5 | idempotent | 4 | `check`, `policy`, `probe`, `reap`, and `egress check` are idempotent; `worktree` and `run` take no `--op` |
| 6 | no prompts | 5 | `nova-sandbox -- /bin/true` -> exits 125 immediately without reading stdin |
| 7 | exit codes | 4 | `check` -> 0, `version --bogus` -> 2, bare wrap `/bin/false` -> 1, bare wrap refusal -> 125; however on Linux `nova-sandbox reap --dry-run` exits 125 (`reason=no_sandbox`) while `help` says `reap: 0 clean, 3 something remained` |
| 8 | one word | 4 | Unknown-flag refusals use three different `reason=` tokens across verbs: `reason=unknown_flag` (`version --bogus`), `reason=bad_flag` (`--bogus -- /bin/true`, `worktree --dry-run`), and `reason=no_command` (`reap --json`, `run --dry-run`, `egress apply --dry-run`) |
| 9 | push | 5 | `n/a: nova-sandbox wraps a child process synchronously and does not poll remote state` |
| 10 | no leaks | 5 | `HOME=/home/nova/scratch/trial/home nova-sandbox probe --write /home/nova/scratch/trial` -> `PROBE OK backend=landlock abi=8 steps=4 passed=4 net=nopromise gpu=none` (proves read/write boundaries without reading secret contents) |
| 11 | true help | 3 | `nova-sandbox --bogus -- /bin/true` says `the flags are --read, --read-noexec, --write, --cwd, --tmp, --name, --acl, --secret, --gpu, --net-deny, --net-listen, --json, --net-allow, --max`, and then `nova-sandbox --json --write /home/nova/scratch/trial -- /bin/echo hi` refuses with `--json is not a flag of the bare form` (exit 125) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox --bogus -- /bin/true` then `nova-sandbox --json --write /home/nova/scratch/trial -- /bin/echo hi` | Unknown-flag refusal on the bare wrap lists `--secret`, `--json`, and `--max` among `the flags are ...`, and passing `--json` is then refused with `SANDBOX REFUSED reason=no_command: --json is not a flag of the bare form; it is check, policy and probe's` (carried from the 2026-10-02 ratings) | List only the bare form's valid flags in its unknown-flag refusal, and use `reason=bad_flag` instead of `reason=no_command` when a verb-only flag is given to the bare wrap | S |
| 2 | `nova-sandbox reap --json`, `nova-sandbox run --dry-run`, `nova-sandbox egress apply --dry-run`, `nova-sandbox version --bogus` | Unknown flags are tagged `reason=no_command: unknown flag ...` on `reap`, `run`, and `egress`, `reason=unknown_flag` on `version`, and `reason=bad_flag` on `worktree` and the bare wrap; `egress apply` and `egress drop` refusals also end with `run: nova-sandbox egress plan ...` instead of their own verb's usage (carried from the 2026-10-02 ratings) | Use `reason=bad_flag` for unknown flags across every verb, and end `egress apply`, `egress check`, and `egress drop` refusals with each subverb's own `run:` usage line | S |
| 3 | `nova-sandbox reap --dry-run` (on Linux) | Prints `SANDBOX REFUSED reason=no_sandbox: the disposable place is darwin's APFS volume...` at exit 125, contradicting `nova-sandbox help` (`reap: 0 clean, 3 something remained`) so `nova-sandbox reap --dry-run` cannot be used cross-platform as the end-of-card gate promised in `reap -h` (carried from the 2026-10-02 ratings) | Exit 0 (`SANDBOX REAP OK volumes=0`) on Linux or document exit 125 in `reap`'s exit-code help, and note on `run -h`/`reap -h` when running on Linux that disposable volumes are darwin/windows only | S |
| 4 | `nova-sandbox worktree --dry-run`, `nova-sandbox egress plan --dry-run` | `worktree`, `egress plan`, `egress apply`, and `egress drop` mutate worktrees, ruleset files, or kernel nftables and refuse `--dry-run` (`unknown flag --dry-run`) | Add `--dry-run` to `worktree` and `egress` (`plan`, `apply`, `drop`) so callers can preview worktree creation/pruning and nftables changes without writing | M |
| 5 | `nova-sandbox egress -h` and `nova-sandbox egress plan -h` | The example `nova-sandbox egress plan --run r1 --policy infra/image/egress.txt --model-host api.example.com --uid 1001 --out /tmp/r1.nft` omits the required `--resolver <ip>` flag, failing when pasted with `EGRESS REFUSED reason=bad_resolver` (carried from the 2026-10-02 ratings) | Include `--resolver 10.9.0.53` in the `egress -h` and `egress plan -h` example line | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox version --json`, `nova-sandbox worktree --json`, `nova-sandbox egress check --json`, `nova-sandbox reap --json` | Only `check`, `policy`, and `probe` accept `--json`; the remaining verbs refuse `--json` | Add `--json` support to `version`, `reap`, `worktree`, and `egress` | M |
| 2 | `HOME=/home/nova/scratch/trial/home nova-sandbox --write /home/nova/scratch/trial -- /bin/true` | Emits `SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped)...` on stderr before every single wrapped command on an ABI 8 kernel (carried from the 2026-10-02 ratings) | Keep `abi=8 used=6` on `SANDBOX OK` and emit the explanatory `NOTE` only on `nova-sandbox check` and `nova-sandbox policy` | S |

## Top 3 fixes
1. Fix the bare wrap's flag list and `reason=` codes across all verbs so unknown flags consistently report `reason=bad_flag` and only list flags that the invoked form actually accepts.
2. Give `egress apply`, `egress check`, and `egress drop` their own `run:` remedies on refusal, and add the missing `--resolver` flag to the `egress plan -h` example.
3. Make `nova-sandbox reap --dry-run` exit codes and `run -h` / `reap -h` platform descriptions truthful on Linux, and add `--dry-run` to `worktree` and `egress`.

## Good, keep
- `SANDBOX REFUSED reason=home_outside` gives the exact copy-pasteable `mkdir -p <write>/home && HOME=<write>/home nova-sandbox ...` command needed to fix an inherited `HOME`.
- `SANDBOX OK` before the child starts (with `cwdb64=` and `deletes=`) and `SANDBOX DONE exit=<n>` after it finishes cleanly separate a sandbox pre-flight refusal (exit 125) from the child's own exit code.
- `nova-sandbox probe` runs a positive control (`write_outside_control expect=allow got=allow`) before proving `write_outside expect=deny got=deny`, ensuring a denial comes from the wall rather than an unwritable parent directory.

## What could not be tried
- `nova-sandbox run` (disposable APFS volume / Windows Job Object): darwin/windows only; refuses with `reason=no_sandbox` on Linux.
- `nova-sandbox egress apply` and `nova-sandbox egress drop`: mutate live host `nftables` rules and require root/net-admin privileges.
- `nova-sandbox worktree --pr <id>`: requires network calls to GitHub to resolve a pull request head.
