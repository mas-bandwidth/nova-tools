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

`nova-update release cut` reads every check run on the commit it is tagging and refuses the cut when any is not `success`, `skipped` or `neutral`. It sets aside a cancelled `release.yml` run of an older tag on the same commit (the publish recipe cancels that run when the cut publishes its own assets), so a second cut from the same commit is not blocked by a previous tag's cancelled run; the `RELEASE CUT` line names what was set aside as `ci-set-aside=<run id>@<tag>,...`. A cancelled run whose head branch is the release branch or the version being cut is still a stop, and `--waive-ci` still refuses when every remaining check is green — including when the only non-green runs were the set-aside ones ("the previous tag's release run is set aside; no waiver is needed").

## Spec

The contract is [docs/SPEC-UPDATE.md](../../docs/SPEC-UPDATE.md).
