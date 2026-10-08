# nova-runner

## What it is

nova-runner keeps one friend running at the width in her nova-config row. It reads her ready queue, starts one-shot harness lanes, reports their results, and beats the true lane count each second.

## Why use it

The runner fills free capacity and responds to width changes without a coordinator restarting a shell script. A running lane finishes when the width falls; the runner does not kill it to meet the new width.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-runner@latest
```

## First run

Read the loop and required inputs before starting a friend's runner:

```text
$ nova-runner help
nova-runner keeps one friend running at her row's width, every second, with no coordinator in the loop.
```

Then use `nova-runner run --as <friend> --dir <working-dir> --harness <one-shot-harness> --seat <seat>`. The friend must have a row in nova-config, and the working directory must exist. `--model-frontier`, `--model-heavy`, `--model-pro`, and `--model-flash` supply defaults when a card names no model.

## Verbs

`help` explains the loop and `run` keeps it active. The [runner contract](../../docs/SPEC-RUNNER.md) documents the commands it calls, its files, and each transition.

## Spec

The contract is [docs/SPEC-RUNNER.md](../../docs/SPEC-RUNNER.md).
