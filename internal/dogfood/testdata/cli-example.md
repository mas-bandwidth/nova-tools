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
