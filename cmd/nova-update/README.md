# nova-update

## What it is

nova-update: compare installed tools with their latest releases, and update one when asked

## Why use it

Inspect versions and apply one chosen update.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-update@latest
nova-update version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-update).

```text
$ nova-update example --out versions.tsv
EXAMPLE OK wrote=versions.tsv entries=1 unchanged=false
EXAMPLE NOTE next: nova-update report --file versions.tsv
$ nova-update report --file versions.tsv
REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=23ms file=versions.tsv host=- as=- entries=1 kinds=tool at=2026-10-02T02:55:03Z timeout=5s budget=1m0s max=20 snapshot=-
REPORT TOOL name=go kind=tool version=1.27.1 raw=go\x20version\x20go1.27.1\x20darwin/arm64 path=/opt/homebrew/bin/go
```

## Verbs

The [nova-update section of the command reference](../../docs/CLI.md#nova-update) documents every verb's flags, effect and exit codes.

- `example`
- `check`
- `status`
- `apply`
- `report`
- `watch`
- `adoption`
- `release`
- `help`
- `version`

## Spec

The contract is [docs/SPEC-UPDATE.md](../../docs/SPEC-UPDATE.md).
