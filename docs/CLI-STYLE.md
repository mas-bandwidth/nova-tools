# CLI style

DRAFT, 2026-09-27, for Stella's cold read before it binds anything. This page is the whole command-line contract, written so a cold AI can predict any nova tool. SPEC.md still governs the exit table and the output grammar. Each rule lists the tools that break it today, so the page is also the repair list.

## Grammar and help

**(a) Grammar.** `<tool> <verb> [flags] [positionals]`. Flags come before positionals. `--` ends the flags, so a flag-shaped literal can follow it. A late flag is refused by name, never read as a file or as query text. Breaks today: nova-self-talk, nova-memory search, nova-sandbox (an unknown verb falls into the wrap), nova-update (intersperses).

**(b) Help.** `<tool> help` prints the banner. `<tool> help <verb>` and `<tool> <verb> -h|--help` print that verb's help and exit 0, with no read, write or dial. Breaks today: 14 tools; only nova-self-talk passes, and nova-table was not probed.

## Flags

**(c) One spelling for each flag, and each flag wants one thing.**

| flag | wants | spelled today → rename |
|---|---|---|
| `--addr` | a Redis host:port | nova-table, nova-config and nova-tokens `--redis`; nova-update report and nova-wake `--store` |
| `--user`, `--password-env` | an ACL user, and an env variable's NAME | nova-redis, nova-update and nova-wake read env silently; nova-table and nova-config use `NOVA_SPRINT_*`; nova-tokens has three fallbacks |
| `--pg` | a Postgres DSN with no password | nova-config (already) |
| `--root` | a root the tool reads | nova-check `--dir`, `--home`, `--repo`, `--repo-dir`; nova-tokens `--swarm-root`; nova-sandbox worktree `--repo` |
| `--dir` | a working directory the tool owns | nova-tokens `--out` (5 verbs); nova-sandbox run and nova-update release `--out` |
| `--store` | a store directory the tool owns | nova-redis serve `--dir`; nova-cairn and nova-secrets already comply |
| `--out` | one output file | (complies) |
| `--as` | the acting identity | nova-check `--by`, `--identity`; nova-secrets `--as` is a seat, so `--seat` |
| `--remote`, `--branch` | git | nova-bus and nova-wake already comply |
| `--max` | any cap; 0 means all | nova-memory and nova-check `--fail-max`; nova-bus `--open-max`, `--max-notes`; nova-wake watch `--max` (a duration) |
| `--timeout` | a Go duration | nova-check seconds; `--gh-timeout`, `--git-timeout`, `--tools-timeout` |

A storage-shape noun (`--bus`, `--box`) stays, and its help names the shape. Breaks today: 13 tools.

## Exits and output

**(d) Exits.** 0 passed or done. 1 ran and said NO. 2 could not run. nova-fuse's write verbs are the one deliberate deviation, and SPEC.md's table governs them. No tool invents another code (125, 3, 124) unless it wraps a program whose own code it passes through. Breaks today (10 tools):
- nova-sandbox: 125, 3 and 124.
- 1 or 2 swapped: nova-ci, nova-bus, nova-table, nova-secrets, nova-tokens, nova-wake and nova-check.
- 0 on a NO: nova-config `--check` and nova-version diff.

**(e) Output.**
- A result is exactly one line on stdout: `<TOOL> <VERB> OK key=value ...`. FAIL and REFUSED lines go to stderr.
- No OK line is printed when the exit is not zero. The closing line of a verb that runs several checks is FAIL when any check failed.
- A field is quoted with Go `%q`. That keeps an event on one line; it is not shell quoting.
- A remedy is a command, printed shell-quoted so that it pastes.

Breaks today (14 tools):
- The closing line: nova-check quickstart (`QUICKSTART OK worst-exit=2`); nova-config apply (APPLY printed before a refused write).
- The token: nova-table, nova-config, nova-tokens, nova-secrets, nova-wake, nova-check, nova-memory, nova-update, nova-ci and nova-sandbox.
- Quoting: nova-redis, nova-bus, nova-fuse and nova-cairn.

## Refusals and silence

**(f) Refusals.**
- A refusal is one line: `<tool> <verb>: <the flag and what it WANTS>; run: <tool> <verb> -h`.
- It names every missing required flag at once, never in rounds.
- An unknown verb or flag is named in the tool's words, and an unknown verb lists the verbs.

Breaks today: all 16 tools. The worst:
- Rounds: nova-secrets, nova-check hygiene, nova-bus and nova-sandbox worktree.
- Unnamed input: `bad flags` (nova-check); `unknown verb` (nova-update, nova-version).
- Remedies that loop or point nowhere: nova-table, nova-secrets and nova-version.

**(g) No silent success.** A path, root, store, session, machine, friend or seat that does not exist is a refusal (exit 2). It is never `count=0 OK`. An ignored flag is a refusal. A checker that can go stale says so: `last=<date>` older than its bound is FAIL. Breaks today: 13 tools, 29 cases; the HIGH cases are in nova-fuse, nova-tokens, nova-cairn, nova-ci, nova-sandbox, nova-secrets, nova-version and nova-wake.

## Paths and helpers

**(h) No guessed paths.**
- There are no default paths outside the tool's own `--dir` or `--store`.
- No directory is created that was not asked for.
- An external helper is found on PATH, with an explicit override flag, and the path found is echoed.
- A config file the tool reads is named in help, and its path is echoed on every output line it affects (`config=<path>`). nova-bus's `.nova-bus/defaults` and nova-wake's `.nova-wake/config` are today's silent examples.

Breaks today (10 tools):
- Guessed paths: nova-secrets (and no PATH lookup for sops), nova-update and nova-ci.
- Silent config files: nova-wake (which also finds nova-bus on PATH with no override) and nova-bus.
- The seat's Redis: nova-table and nova-config. The nova-sprint environment: nova-tokens.
- Unasked directories: nova-fuse and nova-redis.

**(i) Bounded output.** At most N items, then `<TOKEN> MORE shown=N total=M`. N defaults to 20 through `--max`. Breaks today: nova-sandbox egress plan (3,419 lines) and nova-update (prints the whole `$PATH`). A third case, nova-check nocode on a code repo, is disputed.

## First run and use

**(j) First run.**
- Every verb is in a `### First run` in docs/CLI.md, produced by running the tool.
- The gap: the class test (`internal/ci/onboarding_test.go`, `onboarding_functional_test.go`) reads TESTS.md, which is how tools pass without a First run. It must read CLI.md and execute every line, example and remedy.

Breaks today (11 tools):
- None: nova-redis, nova-secrets, nova-cairn and nova-ci (which also contradicts itself).
- Broken: nova-config, nova-bus and nova-sandbox.
- Other: nova-memory (hand-typed), nova-table (stalls at `create`), nova-update (needs a checkout), nova-fuse (a prepopulated box).

**(k) A remedy preserves the caller's inputs (Stella's rule).** It keeps the login and every explicit flag given, including empty and default values, shell-quoted, and a test runs it through the real CLI. Breaks today: nova-fuse check and nova-cairn append (measured; others not probed).

**(l) An uncertain write is never reported as absent (Stella's rule).** A write is reported as done, refused or unconfirmed. Breaks today: nova-config apply (it reports a write that did not land) and nova-cairn append (a duplicate reports the retry's clock as the stamp).

**(m) Library noise never reaches stderr.** go-redis pool logs and the like are discarded; the tool's refusal says what failed. Breaks today: nova-redis, nova-config, nova-update and nova-wake.

**(n) Used means delivered.** A tool ships with the log line that proves a delivery. A loop's OK line carries `delivered=` and `refused=`; delivering nothing is not OK. Breaks today (6 tools):
- nova-tokens: the nightly chain is green over a red fold.
- nova-wake: serve was refused on every poll for five days.
- nova-redis, nova-fuse, nova-config and nova-table: no dated delivery.
