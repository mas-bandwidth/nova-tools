# CLI style

The command-line contract, written so a cold AI can predict any nova tool. SPEC.md governs the exit table and the output grammar.

## Grammar and help

**(a) Grammar.** `<tool> <verb path> [flags] [positionals]`. A verb path may be nested (`nova-fuse lift quarantine`, `nova-ci github receipt`). Flags come before positionals. A late flag is refused by name, except a literal after `--`.

**(b) Help.** `<tool> help` prints the banner. `<tool> help <verb>` and `<tool> <verb> -h|--help` print that verb's help and exit 0, with no side effects.

## Flags

**(c) One spelling for each flag, and each flag wants one thing.**

| flag | wants |
|---|---|
| `--addr` | a Redis host:port |
| `--user`, `--password-env` | an ACL user, and an env variable's NAME |
| `--pg` | a Postgres DSN with no password |
| `--root` | a root the tool reads |
| `--dir` | a working directory the tool owns |
| `--store` | a store directory the tool owns |
| `--out` | one output file |
| `--as` | the acting identity |
| `--seat` | a seat |
| `--remote`, `--branch` | a git remote, and a git branch |
| `--max` | displayed items; default 20, 0 means all |
| `--timeout` | a Go duration |

A resource limit keeps its unit or scope in its name; these spellings stand: nova-check kernel `--max-bytes`, `--max-tokens`; nova-bus `--max-notes`, `--max-bytes`, `--max-body-bytes`, `--open-max`. A resource budget of 0 is a refusal, not "all"; help states each zero. A storage-shape noun (`--bus`, `--box`) stays; help names the shape.

## Exits and output

**(d) Exits.** 0 passed or done. 1 ran and said NO. 2 could not run. A wrapper may pass through its program's code; no tool invents another. The protocol exceptions:
- nova-fuse's write verbs (SPEC.md's table).
- nova-bus wait `--idle-exit <n>`, a caller-selected code for an idle timeout.
- `nova-config machine self` uses 3 when the machine cannot be identified or
  `--check` cannot read its configuration; under `--check`, 2 means its name is
  not a machine row. `nova-sprint fleet sync --check` uses 0 for no drift, 2 for
  drift, and 3 when the inventory or store cannot be read. Sync without
  `--check` also uses 3 for an unreadable inventory. These are the existing
  self/sync read-boundary exits, not a general third refusal code.

**(e) Output: events and payloads.**
- EVENT output is one line per event, `<TOKEN> OK|FAIL key=value ...`; OK to stdout, FAIL and REFUSED to stderr. One exception, per SPEC.md: nova-self-talk's `SELFTALK FAIL files=...` summary count line goes to stdout beside its advisory note.
- Item OK events may precede a later failure; the closing status is then FAIL, exit not 0.
- PAYLOAD output: a verb documented to emit a payload names its format in help and emits exactly that payload on stdout and nothing else; its events go to stderr. Cases include nova-bus prepare (a JSON artifact), nova-bus draft (a drafted note), nova-fuse path (a bare value), `nova-sprint inbox --json` and `fleet sync --json`, and `nova-config machine width --json`. A payload is never capped like a listing.
- A remedy is a command, printed shell-quoted so it pastes.

**(e2) Token and escaping grammar.** SPEC.md's oneline escaping, whitespace-safe `key=value` fields and the `VERB STATUS` token shape govern. The caller knows which tool it ran, so the token does not repeat it, and a field needs no quoted-string parser.

## Refusals and silence

**(f) Refusals.**
- A MALFORMED INVOCATION (a missing or unknown flag, a bad value shape, an unknown verb) is refused in one line, plus at most one hint line for what the input WANTS (SPEC.md): `<tool> <verb>: <what is wrong>; run: <tool> <verb> -h`.
- It names every missing required flag at once, never in rounds.
- An unknown flag is named. An unknown verb is named, lists the verbs, and points at the root `<tool> help`, since it has no verb help.
- A RUNTIME failure (an unreachable store, a refused login, a missing input file, a lost reply) keeps the one-line shape, and its remedy is the concrete next command for that failure, not `-h`.
- nova-check's hints stand: each missing required flag gets its own refusal line and one hint line from a fixed set the binary ships, saying what the flag is and what a first run puts there. Its refusals point at the root `nova-check help`; they move to `nova-check <verb> -h` with the rest.

**(g) No silent success.** Three cases:
- A missing INPUT (a required path, root, store, session, machine, friend or seat that must exist and be readable) is a refusal, exit 2, never `count=0 OK`.
- A CREATE target: a verb documented to create a destination may create exactly the explicitly selected path, never a parent tree and never a default.
- A VALID EMPTY RESULT (an idle poll, an empty search, a clean checker, an idempotent retry) is OK with its count and, where it helps, `note=`; only when the input is resolved and read.

An ignored flag is a refusal; a stale checker (`last=` past its bound) is FAIL.

## Paths and helpers

**(h) No guessed inputs.**
- No default paths outside the tool's own `--dir` or `--store`; no directory but a CREATE target.
- An external helper is found on PATH, with an override flag, and the path found is echoed.
- An explicitly selected seat or named config is not guessed: help states it once, the summary line echoes it once (`seat=`, `config=`); payload output stays pure.

**(i) Bounded listings.** At most `--max` items per kind, then `<TOKEN> MORE kind=<kind> shown=<n> total=<t> <remedy>` (SPEC.md). A cap never changes the work checked or the total counted.

## First run and use

**(j) First run.**
- Every verb has a `### First run` in docs/CLI.md, produced by running the tool; its first line states the local prerequisites (a throwaway store, a scratch dir).
- The class test (`internal/ci/onboarding_test.go`) runs each CLI.md First run in fixtures it owns.

**(k) A remedy preserves the effective inputs.** It keeps the failed operation's inputs (target, identity, login, selections, explicit empty and default overrides) unless it names an intended change. It carries only flags the target verb accepts, and names password VARIABLES, never a value. A test runs the printed remedy through the real CLI.

**(l) An uncertain write is never reported as absent.** A write is done, refused or unconfirmed; a retry reports the stored receipt with its original stamp.

**(m) Library noise never reaches stderr.** go-redis pool logs are discarded; the refusal says what failed.

**(n) Used means delivered.** A delivery loop reports `delivered=` and `refused=` truthfully; an idle poll is a valid empty result. Dated delivery evidence is a release criterion.

## Settled choices

1. **Token shape:** `VERB STATUS`.
2. **Field quoting:** oneline escaping.
3. **The seat's Redis:** a tool may default to the Redis its seat is configured with; that is configuration, not a guess. Every other store is named with `--addr`.
4. **`--idle-exit`:** nova-bus wait keeps its caller-selected code.
5. **Help pointer:** a malformed invocation's remedy is `<tool> <verb> -h`.

## Enforce in this order

1. Verb help at exit 0, with no side effects.
2. A truthful final status and exit, and uncertainty and receipt semantics.
3. Missing-input and ignored-flag refusal.
4. Runnable, login-preserving remedies, and quiet library failures.
5. Executable First runs and bounded listings.
6. The flag and token migration, one coherent change that updates every affected doc with it. The agreed docs proceed now.
