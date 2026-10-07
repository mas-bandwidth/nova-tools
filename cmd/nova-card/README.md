# nova-card

## What it is

nova-card: writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help

## Why use it

Turn a ledger, a findings file or a tool's help into briefs the sprint admits. nova-card is pre-alpha: not ready for production use.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-card@latest
nova-card version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-card).

Fixture: `cmd/nova-card/testdata/findings.tsv`, a reader's findings on two
files, typed as `./cmd/nova-card/testdata/findings.tsv` from the root of a
checkout; `./cards` is a directory the first line creates.

```text
$ nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo example/repo --base dev --sha 0123456789abcdef0123456789abcdef01234567 --out ./cards
CARDS OK dir=./cards cards=2 waves=1 tier=pro

$ nova-card lint --card ./cards/finding-internal-bus-send.md
LINT OK file=./cards/finding-internal-bus-send.md

$ nova-card lint --card ./cards/finding-cmd-nova-bus-main.md
LINT OK file=./cards/finding-cmd-nova-bus-main.md
```

## Verbs

The [nova-card section of the command reference](../../docs/CLI.md#nova-card) documents every verb's flags, effect and exit codes.

- `generate`
- `lint`
- `template`
- `version`
- `help`

## Spec

The contract is [docs/SPEC-CARD-CONTRACT.md](../../docs/SPEC-CARD-CONTRACT.md).
