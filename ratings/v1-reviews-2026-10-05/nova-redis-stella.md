# nova-redis review, 2026-10-05

Rater: gpt-5.6-terra (Stella)
Build: 52046bd9a0eb
Verdict: GOOD WITH FIXES for an AI to use
Score: 6/10

## Reasons
The source gives a deliberately narrow Redis owner: explicit addresses, no password arguments, bounded connection setup, safe owner-plus-TTL scratch keys, and specific remediation. It is not yet a tool I could use cold without care because its first safe state-changing trial requires a caller-maintained scratch Redis instance; this release source has no dry-run path. The first confusion is that the `nova-redis` command reference opens directly with a command synopsis, despite the repository onboarding standard requiring a `### First run` section with runnable first commands and common mistakes (docs/CLI.md:1540; docs/ONBOARDING.md:30-34). The first claim I doubted is that every command's verb help is available: the command reference explicitly says `-h` and `--help` after a verb are refused (docs/CLI.md:1590), while the code defers `verbflag.Recover` for verb help (cmd/nova-redis/main.go:151-154).

The literal release pin, `git describe --tags --abbrev=0 origin/main`, is `v1.0.0`, source `52046bd9a0ebf726b40617a81e678d2855047a60`. I built that exact source on a serialized Linux bench with `/home/glenn/go/bin/go`; the binary identified itself as `nova-redis v1.0.0 linux/amd64 go1.26.6` and had SHA-256 `2efbd5b7cc2e45077edd7965ffe75d395f27ff939bfe83ffbea7fc701e2013c4`. The complete capture, including empty streams, is retained job-locally in `vision-redis-review-attempt3.log`.

The three source-binary commands below used only a fresh private loopback scratch store. `/tmp/nova-redis-stella-w2-g1-e15.bp8ACn/nova-redis spill --addr 127.0.0.1:6391 --owner stella-review --name note --ttl 10m --value hello` exited 0, stdout `SPILL OK key=stella-review:note ttl=10m0s expires=2026-10-08T03:08:46Z bytes=5`, stderr empty. `/tmp/nova-redis-stella-w2-g1-e15.bp8ACn/nova-redis recall --addr 127.0.0.1:6391 --owner stella-review --name note` exited 0, stdout `RECALL OK key=stella-review:note bytes=5 value=hello`, stderr empty. `/tmp/nova-redis-stella-w2-g1-e15.bp8ACn/nova-redis fn load --addr 127.0.0.1:6391` exited 0, stdout `LOADED nova_sprint sha=e547a647a6a8645d store=127.0.0.1:6391`, stderr empty. The deliberate refusal `/tmp/nova-redis-stella-w2-g1-e15.bp8ACn/nova-redis spill --addr 127.0.0.1:6391 --name note --ttl 10m --value hello` exited 2, stdout empty, stderr `nova-redis spill: --owner is required; refusing to guess; run: nova-redis help`.

No Go test or vet was run on this Studio. The source build, private scratch run, and function load were confined to the serialized Linux bench. No shared Redis, secrets, installed-artifact, cold independent read, installation, or dev-ancestry check is claimed.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-redis/main.go:211-255 | Source `spill` has no `--dry-run`; its only successful path opens a Redis store and writes. A cold AI must first arrange a scratch server to prove its intended key, TTL, and output, and the source command reference offers no safe trial. | Add a `spill --dry-run` path that validates all supplied inputs and prints the planned key, TTL, bytes, and store without reading login state, dialing, or writing; document and test it. | M |
| 2 | docs/CLI.md:1590 | The command reference tells an AI that per-verb `-h` and `--help` are refused, but source code and its help test say they print help and exit 0 before effects. This makes a correct discovery action look unsafe or broken. | Replace the stale sentence with the actual exit-zero per-verb help contract and include `fn load` and `fn check` in the existing help-test cases. | S |
| 3 | docs/CLI.md:1540; docs/ONBOARDING.md:30-34 | The tool's CLI section has no required `### First run` block. A newcomer sees five dense commands but no tested first safe command, expected transcript, or common-error remedy in the place the repository says to look. | Add a `### First run` section with one safe refusal and, once finding 1 exists, one dry-run transcript; keep its commands pinned to the existing transcript test. | S |

## Good, keep
The login contract keeps passwords out of arguments and names the environment-variable precedence (docs/CLI.md:1552-1560).
The exact release source makes a missing owner refuse before connect, with a direct remedy; the deliberate source-binary refusal above exited 2.
The serve specification constrains bind addresses, persistence, and secret handling rather than silently choosing unsafe defaults (docs/SPEC-REDIS.md:64-86).
