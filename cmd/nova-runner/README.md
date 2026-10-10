# nova-runner

## What it is

nova-runner: keeps one friend at her row's width, running her cards mechanically every second, with no coordinator in the loop

## Why use it

You do not have to remember to keep a friend running at her width: the runner reads her row from `nova-config`, takes her cards from `nova-sprint`, and starts and finishes lanes until the row says otherwise.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-runner@latest
nova-runner --help
```

## First run

`nova-runner --help` prints the loop and needs no store, seat or key, so a reader can see the whole contract before running anything:

```text
$ nova-runner --help
nova-runner keeps one friend running at her row's width, every second, with no coordinator in the loop.
```

The real run is one command:

```text
$ nova-runner run --as ada --dir ~/ada-working --harness opencode --seat <seat>
```

`--dir` is the friend's working directory, the one `nova-friend` stages jobs under; the runner reads her row from `nova-config friend show <friend>` and her queue from `nova-sprint queue --as friend.<friend>`, then stages each lane the way `nova-friend` does and runs the harness one-shot.

## Verbs

The verbs and the loop they drive are the contract in [docs/SPEC-RUNNER.md](../../docs/SPEC-RUNNER.md).

- `run`
- `help`

## Spec

The contract is [docs/SPEC-RUNNER.md](../../docs/SPEC-RUNNER.md).
