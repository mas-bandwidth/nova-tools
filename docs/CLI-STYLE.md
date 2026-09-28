# CLI style

DRAFT, 2026-09-27, revised after Stella's cold read. The command-line contract, written so a cold AI can predict any nova tool. SPEC.md governs the exit table, the escaping and the token shape. Each rule lists what breaks it today, so the page is the repair list.

## Grammar and help

**(a) Grammar.** `<tool> <verb path> [flags] [positionals]`. A verb path may be nested (`nova-fuse lift quarantine`, `nova-ci github receipt`). Flags come before positionals. A late flag is refused by name, except a literal after `--`. Breaks today: nova-self-talk, nova-memory search, nova-sandbox (an unknown verb falls into the wrap), nova-update (intersperses).

**(b) Help.** `<tool> help` prints the banner. `<tool> help <verb>` and `<tool> <verb> -h|--help` print that verb's help and exit 0, with no side effects. Breaks today: 14 tools; only nova-self-talk passes, and nova-table was not probed.

## Flags

**(c) One spelling for each flag, and each flag wants one thing.**

| flag | wants | spelled today → rename |
|---|---|---|
| `--addr` | a Redis host:port | nova-table, nova-config and nova-tokens `--redis`; nova-update report and nova-wake `--store` |
| `--user`, `--password-env` | an ACL user, and an env variable's NAME | nova-redis, nova-update and nova-wake read env silently; nova-table and nova-config use `NOVA_SPRINT_*`; nova-tokens has three fallbacks |
| `--pg` | a Postgres DSN with no password | nova-config (already) |
| `--root` | a root the tool reads | nova-check `--dir`, `--home`, `--repo`, `--repo-dir`; nova-tokens `--swarm-root`; nova-sandbox worktree `--repo` |
| `--dir` | a working directory the tool owns | nova-tokens `--out` (5 verbs); nova-sandbox run and nova-update release `--out` |
| `--store` | a store directory the tool owns | nova-redis serve `--dir`; nova-cairn and nova-secrets comply |
| `--out` | one output file | (complies) |
| `--as` | the acting identity | nova-check `--by`, `--identity`; nova-secrets `--as` is a seat, so `--seat` |
| `--remote`, `--branch` | git | nova-bus and nova-wake comply |
| `--max` | displayed items; default 20, 0 means all | nova-memory and nova-check `--fail-max`; nova-wake watch `--max` is a duration, so it needs a unit name |
| `--timeout` | a Go duration | nova-check seconds; `--gh-timeout`, `--git-timeout`, `--tools-timeout` |

A resource limit keeps its unit or scope in its name; these spellings stand: nova-check kernel `--max-bytes`, `--max-tokens`; nova-bus `--max-notes`, `--max-bytes`, `--max-body-bytes`, `--open-max`. A resource budget of 0 is a refusal, not "all"; help states each zero. A storage-shape noun (`--bus`, `--box`) stays; help names the shape. Breaks today: 13 tools.

## Exits and output

**(d) Exits.** 0 passed or done. 1 ran and said NO. 2 could not run. A wrapper may pass through its program's code; no tool invents another. The protocol exceptions today:
- nova-fuse's write verbs (SPEC.md's table).
- nova-bus wait `--idle-exit <n>`, a caller-selected timeout code: keep or replace, with its harness callers.

Breaks today (10 tools):
- nova-sandbox: 125, 3 and 124.
- 1 or 2 swapped: nova-ci, nova-bus, nova-table, nova-secrets, nova-tokens, nova-wake and nova-check.
- 0 on a NO: nova-config `--check` and nova-version diff.

**(e) Output: events and payloads.**
- EVENT output is one line per event, `<TOKEN> OK|FAIL key=value ...`; OK to stdout, FAIL and REFUSED to stderr.
- Item OK events may precede a later failure; the closing status is then FAIL, exit not 0.
- PAYLOAD output: a verb documented to emit a payload names its format in help and emits exactly that payload on stdout and nothing else; its events go to stderr. The documented cases today: nova-bus prepare (a JSON artifact), nova-bus draft (a drafted note) and nova-fuse path (a bare value). A payload is never capped like a listing.
- A remedy is a command, printed shell-quoted so it pastes.

Breaks today: nova-check quickstart (`QUICKSTART OK worst-exit=2`). Payload verbs are not breaks.

**(e2) Token and escaping grammar: not decided here.** SPEC.md's oneline escaping, whitespace-safe `key=value` fields and `VERB STATUS` token shape govern today. Two candidate changes wait on Glenn; every other rule holds either way.

## Refusals and silence

**(f) Refusals.**
- A refusal is one line, plus at most one hint line for what the input WANTS (SPEC.md): `<tool> <verb>: <what was wrong>; run: <tool> <verb> -h`.
- It names every missing required flag at once, never in rounds.
- An unknown verb or flag is named; an unknown verb lists the verbs.

Breaks today: all 16 tools. The worst:
- Rounds: nova-secrets, nova-check hygiene, nova-bus and nova-sandbox worktree.
- Unnamed input: `bad flags` (nova-check); `unknown verb` (nova-update, nova-version).
- Remedies that loop or point nowhere: nova-table, nova-secrets and nova-version.

**(g) No silent success.** Three cases:
- A missing INPUT (a required path, root, store, session, machine, friend or seat that must exist and be readable) is a refusal, exit 2, never `count=0 OK`.
- A CREATE target: a verb documented to create a destination may create exactly the explicitly selected path, never a parent tree and never a default.
- A VALID EMPTY RESULT (an idle poll, an empty search, a clean checker, an idempotent retry) is OK with its count and, where it helps, `note=`; only when the input was resolved and read.

An ignored flag is a refusal; a stale checker (`last=` past its bound) is FAIL. Breaks today: 12 tools, 26 cases; HIGH in nova-fuse, nova-tokens, nova-cairn, nova-ci, nova-sandbox, nova-secrets, nova-version and nova-wake. Now valid empties: nova-check links on an empty directory, nova-ci slowtests on empty stdin, nova-self-talk all skipped.

## Paths and helpers

**(h) No guessed inputs.**
- No default paths outside the tool's own `--dir` or `--store`; no directory but a CREATE target.
- An external helper is found on PATH, with an override flag, and the path found is echoed.
- An explicitly selected seat or named config is not guessed: help states it once, the summary line echoes it once (`seat=`, `config=`); payload output stays pure.

Breaks today (10 tools):
- Guessed paths: nova-secrets (and no PATH lookup for sops), nova-update and nova-ci.
- Unechoed config files: nova-bus `.nova-bus/defaults`, nova-wake `.nova-wake/config`.
- The seat's Redis: nova-table and nova-config, pending the seatredis decision. The nova-sprint environment: nova-tokens.
- Unasked directories: nova-fuse and nova-redis.

**(i) Bounded listings.** At most `--max` items per kind, then `<TOKEN> MORE kind=<kind> shown=<n> total=<t> <remedy>` (SPEC.md). A cap never changes the work checked or the total counted. Breaks today: nova-sandbox egress plan (3,419 lines) and nova-update (the whole `$PATH`). nova-check nocode on a code repo is disputed.

## First run and use

**(j) First run.**
- Every verb has a `### First run` in docs/CLI.md, produced by running the tool; its first line states the local prerequisites (a throwaway store, a scratch dir).
- The class test (`internal/ci/onboarding_test.go`) reads TESTS.md, so tools pass without one. It must run each CLI.md First run in fixtures it owns.

Breaks today (11 tools):
- None: nova-redis, nova-secrets, nova-cairn and nova-ci.
- Broken: nova-config, nova-bus and nova-sandbox.
- Other: nova-memory (hand-typed), nova-table (stalls at `create`), nova-update (needs a checkout), nova-fuse (a prepopulated box).

**(k) A remedy preserves the effective inputs (Stella's rule).** It keeps the failed operation's inputs (target, identity, login, selections, explicit empty and default overrides) unless it names an intended change. It carries only flags the target verb accepts, and names password VARIABLES, never a value. A test runs the printed remedy through the real CLI. Breaks today: nova-fuse check and nova-cairn append (measured; others not probed).

**(l) An uncertain write is never reported as absent (Stella's rule).** A write is done, refused or unconfirmed; a retry reports the stored receipt with its original stamp. Breaks today: nova-config apply (it reports a write that did not land) and nova-cairn append (a duplicate reports the retry's clock).

**(m) Library noise never reaches stderr.** go-redis pool logs are discarded; the refusal says what failed. Breaks today: nova-redis, nova-config, nova-update and nova-wake.

**(n) Used means delivered.** A delivery loop reports `delivered=` and `refused=` truthfully; an idle poll is a valid empty result. Dated delivery evidence is a release criterion. Breaks today (6 tools):
- nova-tokens: the nightly chain is green over a red fold.
- nova-wake: serve was refused on every poll for five days.
- nova-redis, nova-fuse, nova-config and nova-table: no dated delivery.

## Decisions for Glenn

1. **Token shape:** keep `VERB STATUS`, or move to `TOOL VERB STATUS`.
2. **Field quoting:** keep oneline escaping, or move fields to Go `%q`, which needs a quoted-string parser.

   Either costs every parser and class test that reads lines, in one cut.
3. **seatredis default:** default to the seat's Redis, as the class test requires, or refuse without `--addr`.
4. **`--idle-exit`:** keep nova-bus wait's code, or replace it with its callers.

## Enforce in this order

1. Verb help at exit 0, with no side effects.
2. A truthful final status and exit, and uncertainty and receipt semantics.
3. Missing-input and ignored-flag refusal.
4. Runnable, login-preserving remedies, and quiet library failures.
5. Executable First runs and bounded listings.
6. The flag and token migration, one separate coherent change, before the docs rewrite.
