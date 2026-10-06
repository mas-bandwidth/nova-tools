# nova-doctor — specification

`nova-doctor` says what is missing from this machine's nova setup and the one line that
fixes each. It reads and changes nothing. SPEC.md's **Conventions** govern; the frame is
`internal/doctor/doctor.go`, each check is one file beside it.

```
nova-doctor [run] [--check <name>]... [--json] [--strict] [--local]
```

`run` is the one verb and the default for a flag: `nova-doctor --local` is `nova-doctor run --local`.
A bare `nova-doctor` is refused (exit 2), naming `run`, as every nova tool refuses a bare command.

## The line

Every check that runs prints one line:

```
DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]
```

The evidence is one line and is what the check saw. `fix:` is absent on `ok` and present on
`warn` and `fail`: a nova verb when one exists, never a hand script.

## Exit

1. **0** when every check is `ok` or a `warn`; **1** on a `warn` only under `--strict`;
   **2** when any check is `fail`, or the invocation is refused (an unknown `--check`).
2. The exit is the worst result of the checks that ran, and a check that outlasts its
   30-second bound is a `fail` that says so.

## Flags

- `--check <name>` runs only that check; it repeats. A name that is no check is refused,
  naming the checks there are.
- `--json` prints one object, `{"checks":[{name,covers,status,evidence,fix}],"skipped":[...],"exit":n}`:
  the same results as the lines.
- `--strict` makes a `warn` exit 1.
- `--local` skips the checks only a fleet needs and prints
  `NOTE --local skipped <n> fleet check(s): <names>`.

## A check

A check is a name, the dependency it covers, whether only a fleet needs it, and a `Run` over
`Env`: environment, directory listing, program execution, file reads, network dial and the
clock, all of them fakeable. A check lives in `internal/doctor/check_<dependency>.go` and
registers itself from an `init`, so a new dependency adds a file and edits no other.
A test of a check runs it over a fake `Env`; none starts a service or opens a socket.

## Checks

### self

The nova tools on PATH, each one's version, all from one release. The first `nova-*`
executable of a name along PATH is the one that runs; each answers `version` with the
version line of `internal/buildinfo`.

- `ok` when every tool reports one version.
- `fail` when the versions differ, naming each tool off the version most tools hold (when
  none holds a majority, every tool and version); when a tool answers no version line,
  naming it; when no `nova-*` tool is on PATH.
- The fix is `nova-update release install --from <release dir> --version <v> --bin <dir>`.
