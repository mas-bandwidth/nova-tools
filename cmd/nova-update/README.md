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

### Release cut

`nova-update release cut` reads every check run on the commit it is tagging and refuses the cut when any is not `success`, `skipped` or `neutral`. It sets aside a cancelled `release.yml` run of an older tag on the same commit (the publish recipe cancels that run when the cut publishes its own assets), so a second cut from the same commit is not blocked by a previous tag's cancelled run; the `RELEASE CUT` line names what was set aside as `ci-set-aside=<run id>@<tag>,...`. It also sets aside a whole cancelled suite when a later green suite of the same workflow path at the same commit covers the same job names: every run of the cancelled suite is completed and its workflow run concluded `cancelled` (an aggregate job such as `certification-ok` that failed only because its needs were cancelled is part of the cancelled suite), the green suite completed later with every run `success`, `skipped` or `neutral`, and it has a run for every job name the cancelled suite has, matrix legs included. Those runs are named `ci-set-aside=<run id>:superseded-by=<run id>`, and the set-aside is written into the tag annotation and the CHANGELOG section as `CI set aside: ...`. A cancelled suite with no later green suite of its workflow, a later suite that is itself red, pending, or missing a job name, and a failed (not cancelled) suite all still refuse. A cancelled run whose head branch is the release branch or the version being cut is still a stop, and `--waive-ci` still refuses when every remaining check is green — including when the only non-green runs were the set-aside ones ("the previous tag's release run is set aside; no waiver is needed").

It also sets aside a completed failed or cancelled `revert-on-red.yml` check as a stale verdict when a `ci.yml` run at the same commit has a latest attempt that completed green after the revert check was created (2026-10-20, ci run 39346166100; the workflow's own fix is the v1.7 item `revert-on-red-reads-the-latest-attempt`), and the checks of an earlier attempt of a workflow run whose latest attempt completed green with every job name of the earlier one. These are named `ci-set-aside=<check id>:stale-verdict-of=<ci run id>@<attempt>` and `<check id>:superseded-attempt=<run id>@<attempt>`, and travel into the tag annotation and the CHANGELOG. A revert-on-red failure with ci red, pending, or older still refuses, as does any other workflow's failure.

## Spec

The contract is [docs/SPEC-UPDATE.md](../../docs/SPEC-UPDATE.md).
