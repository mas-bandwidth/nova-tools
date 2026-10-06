# nova-doctor — specification

`nova-doctor` says what is missing from this machine's nova setup and the one line that
fixes each. It runs every registered check, one per dependency, and prints one line per
check. SPEC.md's **Conventions** govern. The frame is `internal/doctor/doctor.go`; each check is
its own file, `internal/doctor/check_<dependency>.go`.

```
nova-doctor [--check <name>]... [--strict] [--local] [--json]
```

1. **A check is a name, the dependency it covers, a Run and a result.** Run takes an `Env`:
   exec, files, network dial and clock, all fakeable, so a check is tested with fakes and
   never against a real service. The result is `ok`, `warn` or `fail`, with evidence and,
   when not ok, one fix line: a nova verb when one exists, never a hand script. A check
   that returns no fix gets `run nova-doctor --check <name> --json for the full evidence`.
2. **Checks register themselves.** A check file calls `doctor.Register` from `init`. A new
   dependency adds one file and edits no other; a duplicate or incomplete registration
   panics at start.
3. **The line.** `DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]`, on one line
   (whitespace in evidence is folded), in name order. Every check runs; a fail does not stop
   the rest.
4. **The exit is the worst result.** 0 when every check is ok, or warns without `--strict`;
   1 on a warn with `--strict`; 2 on a fail, and 2 on a refused command line.
5. **`--check <name>`** runs only that check; repeatable. An unknown name is refused at exit 2
   naming every check there is.
6. **`--local`** skips the checks only a fleet needs (`Check.Fleet`) and says so with
   `DOCTOR <check> skip <why>` for each; a skipped check never changes the exit.
   `nova-up --local` ends by running `nova-doctor --local`.
7. **`--json`** prints the same report as one object: `{"checks":[{check, covers, status,
   evidence, fix, skipped}], "exit", "strict"}`.
8. **Read-only.** The doctor runs no fix and starts no service; it reads, and says what to run.

## check self

Covers the nova tools on PATH. For each tool a full install holds, it looks the tool up on
PATH and runs `<tool> version`, reading the line with `buildinfo.Parse`.

- **ok**: every tool found prints a version line and all carry one build identity (the
  release); the evidence counts them and names the release.
- **warn**: all found tools agree, but some are not on PATH (named): one tool is a fine number.
- **fail**: a tool on PATH prints no version line (named); or the identities differ, in
  which case the identity most tools carry is the release and each tool that differs is named
  with its own (`skew: nova-bus=v1.1.0 differ from release v1.2.0 carried by the rest`); or
  no nova tool is on PATH.
