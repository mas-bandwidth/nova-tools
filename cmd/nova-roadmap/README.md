# nova-roadmap

## What it is

nova-roadmap: renders ROADMAP.md from docs/roadmap.sexp and checks the committed page against the data

## Why use it

Keep a roadmap as feature tables generated from data, with a drift check for CI.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-roadmap@latest
nova-roadmap version
```

Or take the `nova-roadmap` binary from a [release](https://github.com/mas-bandwidth/nova-tools/releases):
a repository in any language needs only that binary for `make roadmap` and `make roadmap-check`.

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-roadmap).

```text
$ nova-roadmap example --out ./docs/roadmap.sexp
$ nova-roadmap render --file ./docs/roadmap.sexp --out ./ROADMAP.md
$ nova-roadmap check --file ./docs/roadmap.sexp --out ./ROADMAP.md
```

## The form

`nova-roadmap help form` prints the feature-table form with a worked example:
one `##` section per area, a line saying what it is, one row per feature, ✅/❌
cells under a `done` column or an axis such as languages, a complete row and a
link to the data. A cell is ✅ only when every task under it is done with its
evidence (a test or a commit) in the data. The older card list and
`docs/fixes.sexp` (`--kind fixes`) render too; [pkg/roadmap](../../pkg/roadmap)
documents every form.

## Verbs

Every flag of every verb is in the [command reference](../../docs/CLI.md#nova-roadmap).

- `example`
- `render`
- `check`
- `version`
- `help`

## Spec

The contract is [docs/SPEC-ROADMAP.md](../../docs/SPEC-ROADMAP.md).

## In a repository

```make
roadmap:
	nova-roadmap render

roadmap-check:
	nova-roadmap check
```
