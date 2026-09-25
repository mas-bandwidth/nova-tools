# Command reference

A fixture the size of a first run: every shape docs/CLI.md actually holds, and
nothing else. The tests read it line by line.

## nova-example

```
nova-example quickstart --dir <dir> [--fail-max <n>]   # the first run
nova-example links  --dir <dir> [--file <path>]        # every link resolves
nova-example links  --dir <dir> --exclude <prefix>     # the same verb, declared twice
nova-example help
nova-example version
```

A verb named in prose, `nova-example ghost`, is talked about and not declared,
so it is not a verb.

### First run

```
$ nova-example quickstart --dir ./self
QUICKSTART OK dir=./self checks=2
$ nova-example transcript-only --dir ./self
```

## nova-fixture

```
nova-fixture lift quarantine --box <path> <surface>  rescind your own quarantine
nova-fixture lift lockdown                           REFUSED forever, by design
nova-fixture path --box <path>                       echo the box path
nova-fixture --questions <json file> [--floor 0.9]
```

### A second tool's verb, inside this section

```
nova-example status --dir <dir>
```

## nova-indented

Documented by pasting the tool's own help, which indents every verb under a
`usage:` line. Nothing here starts at column zero.

```
nova-indented: the thin client (see docs/SPEC-INDENTED.md)

usage:
  nova-indented session start --session <path> --as <name>
                              --max-bytes <n> [--repair]
  nova-indented session stop  --session <path>
  nova-indented query         --ask <kind>
  nova-indented version       print this build identity
  nova-indented help

wire:
  one line in, one line out. The reply is the session's own answer line.

flags:
  --session <path>  the socket. Required, always: there is no discovery.
```

## nova-transcript

Documented as prose and one worked transcript, with no synopsis block at all.

### First run

```
$ nova-transcript check
CHECK OK backend=sandbox-exec

$ HOME=/Users/me/jobs/j1/home \
  nova-transcript probe --write /Users/me/jobs/j1
PROBE OK steps=5 passed=5
```

## nova-prose

Documented with one heading per verb, the shape a spec uses.

### cut

Cuts the cards from the pool.

### native and batch

Two ways to run the same work; this heading names neither verb on its own.

### serve: the process outside a session

The long-running half.

### The seven verbs

A heading about the verbs, which is not one of them.
