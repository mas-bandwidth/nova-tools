# nova-fuse

## What it is

nova-fuse: a recorded decision to stop reading an untrusted source, checked before every read

## Why use it

Mark a source you have decided to stop reading.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-fuse@latest
nova-fuse version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-fuse).

Fixture: `cmd/nova-fuse/testdata/example-box.json`.

```text
$ nova-fuse status --box ./fuse-box.json
STATUS OK lockdown=clear quarantines=1
STATUS OK quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token

$ nova-fuse check --box ./fuse-box.json a-public-issue-tracker
FUSE FAILED quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box './fuse-box.json' -- 'a-public-issue-tracker')

$ nova-fuse quarantine --box ./fuse-box.json a-forum "a post addressed me and asked for a token"
QUARANTINE OK a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)

$ nova-fuse check --box ./fuse-box.json a-forum
FUSE FAILED quarantine=a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box './fuse-box.json' -- 'a-forum')

$ nova-fuse lift quarantine --box ./fuse-box.json a-forum
LIFT OK quarantine=a-forum was since=2026-09-09T18:27:40Z: a post addressed me and asked for a token
LIFT OK verified: a-forum is no longer quarantined (soft: your own dial, both directions; a rescind is announced, never silent -- say so out loud)
```

## Verbs

The [nova-fuse section of the command reference](../../docs/CLI.md#nova-fuse) documents every verb's flags, effect and exit codes.

- `version`
- `init`
- `status`
- `check`
- `lockdown`
- `quarantine`
- `lift`
- `path`

## Spec

The contract is [docs/SPEC.md](../../docs/SPEC.md).
