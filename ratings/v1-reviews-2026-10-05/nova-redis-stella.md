# nova-redis review, 2026-10-05

Rater: gpt-5.6-terra (Stella)
Build: 3baf154bb6ef
Verdict: GOOD WITH FIXES for an AI to use
Score: 6/10

## Reasons
The source gives a deliberately narrow Redis owner: explicit addresses, no password arguments, bounded connection setup, safe owner-plus-TTL scratch keys, and specific remediation. It is not yet a tool I could use cold without care because its first safe state-changing trial requires a Redis instance; this source build has no dry-run path. The first confusion is that the `nova-redis` command reference opens directly with a command synopsis, despite the repository onboarding standard requiring a `### First run` section with runnable first commands and common mistakes (docs/CLI.md:1540; docs/ONBOARDING.md:30-34). The first claim I doubted is that every command's verb help is available: the command reference explicitly says `-h` and `--help` after a verb are refused (docs/CLI.md:1590), while the code defers `verbflag.Recover` for verb help and the test requires exit-zero no-effect help (cmd/nova-redis/main.go:151-154; cmd/nova-redis/verbhelp_test.go:10-22).

I read source build `3baf154bb6ef96ed62037ecde8a8a8b93ea39149`. The locally installed executable was a different build, `v1.2.0-dev.0d56536c`, so its safe dry-run output is experience evidence only and is not attributed to this source build. No Go build, test, vet, server, shared Redis, secrets, function load, cold independent read, install, or dev-ancestry check was run on this Studio.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-redis/main.go:211-255 | Source `spill` has no `--dry-run`; its only successful path opens a Redis store and writes. A cold AI cannot prove its intended key, TTL, and output safely without first arranging a scratch server, which this review environment must not start. The source command reference also offers no safe trial. | Add a `spill --dry-run` path that validates all supplied inputs and prints the planned key, TTL, bytes, and store without reading login state, dialing, or writing; document and test it. | M |
| 2 | docs/CLI.md:1590 | The command reference tells an AI that per-verb `-h` and `--help` are refused, but source code and its help test say they print help and exit 0 before effects. This makes a correct discovery action look unsafe or broken. | Replace the stale sentence with the actual exit-zero per-verb help contract and include `fn load` and `fn check` in the existing help-test cases. | S |
| 3 | docs/CLI.md:1540; docs/ONBOARDING.md:30-34 | The tool's CLI section has no required `### First run` block. A newcomer sees five dense commands but no tested first safe command, expected transcript, or common-error remedy in the place the repository says to look. | Add a `### First run` section with one safe refusal and, once finding 1 exists, one dry-run transcript; keep its commands pinned to the existing transcript test. | S |
| 4 | `nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner stella-review --name note --ttl 10m --value hello` | The installed newer binary accepts the old `--addr` spelling only with a `NOTE` in JSON, while its primary help spelling is `--redis`; source build documentation and code use only `--addr`. An AI moving between the released binary and this source cannot tell which spelling is canonical. | When the source adopts the newer spelling, document one canonical flag and a bounded deprecation note for its alias; otherwise do not advertise a different spelling in shipped help. | S |

## Good, keep
The login contract keeps passwords out of arguments and names the environment-variable precedence (docs/CLI.md:1552-1560).
The source makes a missing owner refuse before connect, with a direct remedy; the installed binary demonstrated this at exit 2 on the deliberate refusal above.
The serve specification constrains bind addresses, persistence, and secret handling rather than silently choosing unsafe defaults (docs/SPEC-REDIS.md:64-86).
