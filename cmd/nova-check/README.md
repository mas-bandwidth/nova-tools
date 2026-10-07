# nova-check

## What it is

nova-check: checks over markdown records and repositories, each finding named by file and line

## Why use it

Catch broken links and other problems in your records.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-check@latest
nova-check version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-check).

Fixture: `cmd/nova-check/testdata/example-self`.

```text
$ nova-check quickstart --dir ./self
QUICKSTART RUN dir=./self checks=2: links, then nocode
LINKS OK files=4 links=3 excluded=0
NOCODE OK files=5 clean deny-list=floor-list
QUICKSTART OK done=2 worst-exit=0 next=kernel,attest,floors,corpus (kernel wants a size budget, attest a manifest of what a full boot reads, floors a derived copy and its source, corpus a ledger of protected lines: nova-check help)

$ nova-check kernel --file ./self/docs/SEED-CORE.md --max-bytes 4000
KERNEL OK bytes=771 budget=4000
```

## Verbs

The [nova-check section of the command reference](../../docs/CLI.md#nova-check) documents every verb's flags, effect and exit codes.

- `version`
- `quickstart`
- `attest`
- `links`
- `kernel`
- `nocode`
- `floors`
- `corpus`
- `hygiene`
- `dogfood`
- `convergence`
- `spelling`

## Spec

The contract is [docs/SPEC-CHECK.md](../../docs/SPEC-CHECK.md).
