# nova-tla

## What it is

nova-tla: the TLA+ runner of every repository: TLC over a checkout's declared cases, their run records, and which records are stale. It runs the declared cases of a checkout's `tla/CASES.tsv` with TLC, each held to the result its row declares, writes their run records (`tla/RUNS.tsv`), names the groups an edit staled by fingerprint, and checks nova-tools' table model (its suites, the replay of its findings and of `table.lua` receipts).

## Why use it

Every state machine has a TLA+ model beside it, checked with TLC. nova-tla is how a model's records are measured and kept current: `groups --stale` says which groups an edit staled, `run` measures them on a Linux bench (or `run --bench any` from a working machine), and `merge --keep` joins them onto the committed records. One binary serves every repository, with or without a Go module; the tree decides which runner its records are measured with (`nova-tla help runner`).

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-tla@latest
nova-tla version
```

The release ships it with every other command. A tree that requires nova-tools in its go.mod runs the one its go.mod pins, from the tree: `go tool nova-tla <verb> ...` with a go.mod tool line.

## First run

The lines below are the tool's first run as [docs/TESTS.md](../../docs/TESTS.md) records it; `cmd/nova-tla/firstrun_test.go` executes them against the example checkout in `cmd/nova-tla/testdata/example`, from the nova-tools checkout root.

```text
$ nova-tla groups --root ./cmd/nova-tla/testdata/example
["door","lock"]
$ nova-tla groups --root ./cmd/nova-tla/testdata/example --stale
```

## Verbs

The reference for its verbs is `nova-tla help` and [tla/README.md](../../tla/README.md); the CI job that runs it is in [docs/SPEC-CI.md](../../docs/SPEC-CI.md).

- `run`
- `groups`
- `merge`
- `inputs`
- `table`
- `member`
- `replay`
- `witnesses`
- `version`

## Spec

The contract is [docs/SPEC-CI.md](../../docs/SPEC-CI.md) (the tlc job and the run records).
