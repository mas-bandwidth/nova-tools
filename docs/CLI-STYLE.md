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
- nova-bus wait `--idle-exit <n>`, a caller-selected timeout code (an open decision below).

**(e) Output: events and payloads.**
- EVENT output is one line per event, `<TOKEN> OK|FAIL key=value ...`; OK to stdout, FAIL and REFUSED to stderr.
- Item OK events may precede a later failure; the closing status is then FAIL, exit not 0.
- PAYLOAD output: a verb documented to emit a payload names its format in help and emits exactly that payload on stdout and nothing else; its events go to stderr. The documented cases: nova-bus prepare (a JSON artifact), nova-bus draft (a drafted note) and nova-fuse path (a bare value). A payload is never capped like a listing.
- A remedy is a command, printed shell-quoted so it pastes.

**(e2) Token and escaping grammar: not decided here.** SPEC.md's oneline escaping, whitespace-safe `key=value` fields and `VERB STATUS` token shape govern. Two candidate changes are open decisions below; every other rule holds either way.

## Refusals and silence

**(f) Refusals.**
- A refusal is one line, plus at most one hint line for what the input WANTS (SPEC.md): `<tool> <verb>: <what is wrong>; run: <tool> <verb> -h`.
- It names every missing required flag at once, never in rounds.
- An unknown verb or flag is named; an unknown verb lists the verbs.

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

## Decisions for Glenn

These are open. Every other rule holds whichever way each goes.

1. **Token shape:** `VERB STATUS`, or `TOOL VERB STATUS`.
2. **Field quoting:** oneline escaping, or fields in Go `%q`, which needs a quoted-string parser.

   Either change costs every parser and class test that reads lines, in one cut.
3. **The seat's Redis:** whether a tool may default to the seat's Redis, as the class test requires, or refuses without `--addr`.
4. **`--idle-exit`:** whether nova-bus wait keeps its caller-selected code, or its callers move off it.

## Enforce in this order

1. Verb help at exit 0, with no side effects.
2. A truthful final status and exit, and uncertainty and receipt semantics.
3. Missing-input and ignored-flag refusal.
4. Runnable, login-preserving remedies, and quiet library failures.
5. Executable First runs and bounded listings.
6. The flag and token migration, one separate coherent change, before the docs rewrite.
