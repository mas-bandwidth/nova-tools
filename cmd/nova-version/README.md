# nova-version

## What it is

nova-version: which version of each tool is installed, recorded and compared

## Why use it

See what is installed and at which version.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-version@latest
nova-version version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-version).

```text
$ nova-version example --out versions.tsv
EXAMPLE OK wrote=versions.tsv entries=1 unchanged=false
EXAMPLE NOTE next: nova-version report --file versions.tsv
$ nova-version report --file versions.tsv
REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=24ms file=versions.tsv host=- as=- entries=1 kinds=tool at=2026-10-02T02:55:03Z timeout=5s budget=1m0s max=20 snapshot=-
REPORT TOOL name=go kind=tool version=1.27.1 raw=go\x20version\x20go1.27.1\x20darwin/arm64 path=/opt/homebrew/bin/go
```

## Verbs

The [nova-version section of the command reference](../../docs/CLI.md#nova-version) documents every verb's flags, effect and exit codes.

- `example`
- `moved`
- `snapshot`
- `diff`
- `report`
- `send`
- `version`
- `help`

## Spec

The contract is [docs/SPEC-VERSION.md](../../docs/SPEC-VERSION.md).
