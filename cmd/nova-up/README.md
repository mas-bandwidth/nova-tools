# nova-up

## What it is

nova-up: sets up nova on one machine, from nothing to a first sprint

## Why use it

Set nova up on one machine.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-up@latest
nova-up version
```

## First run

The commands below are the tool's own example block; `TestUpLocalDryRunWritesNothing` in `internal/up/up_test.go` runs `--local --dry-run` on a stand-in machine.

```text
$ nova-up --local --dry-run --root ./nova-try
$ nova-up up -h
$ nova-up version
```

## Verbs

The [nova-up section of the command reference](../../docs/CLI.md#nova-up) documents every verb's flags, effect and exit codes.

- `version`
- `help`
- `up`

## Spec

The contract is [docs/SPEC-UP.md](../../docs/SPEC-UP.md).
