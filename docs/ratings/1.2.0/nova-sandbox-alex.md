# nova-sandbox READ and USE rating, nova-tools 1.2.0

Rater: Mercury on a re-rate card (inception/mercury-2.5)
Build: 7acb90e18a76
READ: 7/10
USE: 7/10

The rating follows the form in docs/ratings/1.2.0/README.md: title, Build (12 hex sha), READ/USE scores, Reasons, Findings (TSV), Good keep, Compared with earlier ratings.

## Reasons

READ. The banner answers the three onboarding questions: what it does (run one command in an OS wall), how it works (sandbox-exec on macOS, Landlock on Linux, flags name grants), how to use it (check, make scratch, probe, run). Exit codes are documented. Each verb has `-h` and answers it. The spec (docs/SPEC-SANDBOX.md) is thorough.

What keeps READ at 7. The help makes promises the wall doesn't keep (a --read under a --write stays writable; --net-allow on Linux with --net-deny is ignored). The banner is 159 lines but omits flags (--gpu, probe's --max). Unknown verbs after `help` print the full banner at exit 0 instead of refusing at 2.

USE. Tested by reading cold, then on a throwaway scratch directory. The wall holds: reads outside grants denied, writes outside --write denied, --net-deny blocks HTTPS, --read-noexec prevents execution. Exit status passes through; SANDBOX OK/DONE framing is clear.

What keeps USE at 7. Three wall defects: --read-noexec lets the command's directory (computed, with execute) run programs; --read under --write stays writable; children can outlive SANDBOX DONE. Refusals sometimes drift from the grammar (unknown_flag vs bad_flag, no_command used for missing --out).

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/sandbox/policy.go:689 | A --read nested inside a --write is accepted and stays writable, though help says --read is "NOT writable" | Refuse when a --read or --read-noexec path lies under a --write (reason=bad_read) | S |
| 2 | internal/sandbox/landlock_policy_linux.go:47 | --net-allow on Linux with --net-deny is accepted but ignored; help promises the port opens | Implement with Landlock or refuse (reason=bad_net), or print a NOTE | S |
| 3 | internal/sandbox/policy.go:229,701 | The command's directory is added as an execute root but --read-noexec overlap check ignores it, allowing execution | Refuse --read-noexec when it holds the command directory; report cmdroot on SANDBOX OK | S |
| 4 | cmd/nova-sandbox/main.go:188 | help says 126 is "could not be executed" but not_executable exits 125 | Exit 126 for not_executable or document it | S |
| 5 | cmd/nova-sandbox/main.go:405 | bareFlags lists --json but then refuses it as not a bare-form flag | List only true bare-form flags in that answer | S |
| 6 | cmd/nova-sandbox/main.go:55 | Top help omits --gpu, --json on check/policy/probe, probe's --max | Add to usage lines or reference <verb> -h | S |
| 7 | cmd/nova-sandbox/main.go:242 | help <unknown-verb> prints banner at exit 0; the verb itself is refused at 2 | Refuse unknown verbs after help at exit 2 | S |
| 8 | cmd/nova-sandbox/main.go:59 | --json is not listed in any usage line; probe accepts --max without documenting it | Document in usage or refuse if verb doesn't use it | S |
| 9 | cmd/nova-sandbox/verbhelp.go:81 | egress -h example omits required --resolver; pasted, it is refused | Add --resolver to both egress examples | S |
| 10 | cmd/nova-sandbox/main.go:523 | ABI clamp NOTE printed before every wrapped run | Print from check and policy only, or once on SANDBOX OK | S |

## Good, keep

- Refusals name the reason, the flag, and the corrected command (absolute path, mkdir and HOME= line for HOME outside wall).
- SANDBOX OK before and SANDBOX DONE exit=<n> after the command, making the command's own exit distinguishable from tool refusals.
- probe with its write_outside_control step: a wall is trusted only after a denial is proven.
- policy prints the exact landlock ruleset without running anything.
- cwdb64= beside cwd=, and deletes= with escaped separators: machine-readable receipts are exact.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| no nova-sandbox rating at 1.1.0 | FIRST | docs/ratings/1.1.0 holds no sandbox rating |
| 7/10 and 7/10 from earlier 1.2.0 ratings | SAME | this rating confirms the scores after independent read |
| findings 1-3 from earlier 1.2.0 rating (command dir as execute root, --read-noexec overlap) | STILL HERE | findings 1 and 3 reproduce |
| help promise about --read being NOT writable | NEW | finding 1 |
| --net-allow on Linux ignored | NEW | finding 2 |
| unknown verb after help | NEW | finding 7 |
| help example missing --resolver | NEW | finding 9 |
