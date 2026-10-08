# nova-ci

## What it is

nova-ci: test-time budgets over go test -json output, and this repository's own CI steps

## Why use it

Check test runs and their cost.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-ci@latest
nova-ci version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-ci).

Fixture: `cmd/nova-ci/testdata/example-events.jsonl`, built into the binary:
`--example` reads it in place of stdin, so the lines below run from the binary
alone, and they are the usage banner's `example:` block line for line. The
transcript was produced by running the built binary, not written by hand.

```text
$ nova-ci slowtests --example --budget 60 --load 4 --cpus 16
CI-SLOW package=github.com/mas-bandwidth/nova-tools/internal/example seconds=65.1s budget=60s slowest=TestSlowThing:63.4s,TestAlsoSlow:1.5s
CI-LOAD load=4.00 cpus=16 per-cpu=0.25: measured, not a verdict

$ nova-ci slowtests --example --budget 120 --load 4 --cpus 16
CI-SLOW OK packages=2 slowest=github.com/mas-bandwidth/nova-tools/internal/example:65.1s
CI-LOAD load=4.00 cpus=16 per-cpu=0.25: measured, not a verdict
```

## Verbs

The [nova-ci section of the command reference](../../docs/CLI.md#nova-ci) documents every verb's flags, effect and exit codes.

- `help`
- `version`
- `slowtests`
- `functional`
- `local`
- `new-rule`
- `new-verb`
- `bench`
- `github`

## Spec

The contract is [docs/SPEC-CI.md](../../docs/SPEC-CI.md).
