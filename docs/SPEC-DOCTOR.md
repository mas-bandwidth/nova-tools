# nova-doctor — specification

nova-doctor says what is missing for the nova tools to work on this machine, and for each
thing the one line that fixes it. It changes nothing and runs no fix.

## The frame

A **check** is a name, the dependency it covers, a `Run` over an `Env`, and a result.
The result is `ok`, `warn` or `fail`, with evidence and, when it is not `ok`, the one fix
line: a nova verb when one exists, never a hand script. A result that is not `ok` with no
fix line is turned into a `fail` that says so; a panic is a `fail`; a check never stops
the others.

`Env` is everything a check reaches outside itself: the environment, files, directories,
a child process, a network dial and the clock. The real `Env` is the process's; a test
passes fakes (an exec that answers from a table, files under `t.TempDir()`, a fixed
clock), so no test starts a service or opens a socket.

Checks live one per file, `internal/doctor/check_<dependency>.go`, and register
themselves from an `init` into the default registry. A new dependency adds one file and
edits no other. A duplicate or unnamed registration panics at start-up.

## The command

```
nova-doctor [run] [--check <name>]... [--local] [--strict] [--json]
```

`run` is the one verb and the default: `nova-doctor --local` is `nova-doctor run --local`.
A bare `nova-doctor`, with no verb and no flag, runs no check: it is refused at exit 2 on
stderr, naming the verbs and `run: nova-doctor help`, as every tool's bare command is
(docs/ONBOARDING.md point 1).

- `--check <name>` runs only that check; repeatable. A name that is no check is refused
  at exit 2, naming the checks there are.
- `--local` skips the checks only a fleet needs and says which, in one line:
  `DOCTOR local skipped=<name,name> (...)`.
- `--strict` makes a `warn` exit 1.
- `--json` prints `{"exit":N,"results":[{check,dependency,status,evidence,fix}],"skipped":[...]}`,
  the same results as the lines.

Checks run in name order. One line each:

```
DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]
```

## The exit

| exit | when |
| --- | --- |
| 0 | every result is `ok`, or a `warn` without `--strict` |
| 1 | a `warn` with `--strict`, and no `fail` |
| 2 | a `fail`, or a usage refusal |

## The checks

### self

Dependency: the nova tools on PATH. Finds every executable `nova-*` on PATH (the first of
a name wins, as the shell resolves it), runs `<tool> version`, and reads the version line
(`pkg/buildinfo`). `ok` when every tool reports the same version. `fail` when a
tool does not answer with a version line (named), when none is on PATH, or when the
versions differ: the evidence names every tool that differs from the version most of the
tools report, and the fix is `nova-update apply --file <manifest> <tool> --version <release>`.

`nova-up --local` ends by running `nova-doctor --local`.
