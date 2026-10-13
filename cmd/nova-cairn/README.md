# nova-cairn

## What it is

nova-cairn: a session's words, kept durably as plain files you can come back to

## Why use it

Keep session notes you can return to.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-cairn@latest
nova-cairn version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-cairn).

No fixture: the store is created by the run itself. Every line below is local
— plain files under the named store, no Redis, no remote, no network — and
`cmd/nova-cairn/firstrun_test.go` runs each `$` line in `t.TempDir()`, so the
`./cairns` below is a fresh directory per run. The stamps come from `--now`
because a transcript must read the same twice; a stranger's first run omits
it and the real clock answers instead.

```text
$ nova-cairn open --store ./cairns --session s1 --source bench-a/session-7 --publish manual --now 2026-09-17T12:00:00Z
OPEN OK session=s1 store=./cairns source=bench-a/session-7 publish=manual stamp=2026-09-17T12:00:00Z

$ nova-cairn append --store ./cairns --session s1 --entry e1 --text "the words to keep" --source bench-a/session-7#L3 --publish manual --now 2026-09-17T12:05:00Z
APPEND OK session=s1 entry=e1 source=bench-a/session-7#L3 persisted=true published=false publish=manual duplicate=false stamp=2026-09-17T12:05:00Z

$ nova-cairn index --store ./cairns
INDEX OK sessions=1 entries=1
INDEX SESSION session=s1 publish=manual opened=2026-09-17T12:00:00Z entries=1
INDEX ENTRY session=s1 entry=e1 stamp=2026-09-17T12:05:00Z bytes=17 source=bench-a/session-7#L3

$ nova-cairn receipt --store ./cairns --session s1 --entry e1
RECEIPT OK session=s1 entry=e1 stamp=2026-09-17T12:05:00Z bytes=17 source=bench-a/session-7#L3 persisted=true published=false publish=manual
```

## Verbs

The [nova-cairn section of the command reference](../../docs/CLI.md#nova-cairn) documents every verb's flags, effect and exit codes.

- `open`
- `append`
- `index`
- `receipt`
- `version`
- `help`

## Spec

The contract is [docs/SPEC-CAIRN.md](../../docs/SPEC-CAIRN.md).

`index --since <entry-id|RFC3339>` returns entries strictly after the cursor,
then applies `--max`. An entry ID uses the existing ascending order of stamp,
session, and entry ID, so later entries at the same stamp remain reachable.
IDs shared across sessions refuse; select the cursor with `--session`.
An RFC3339 timestamp (including fractional seconds and offsets) excludes all
entries at or before that instant. Missing IDs, invalid cursors, and an explicit
empty cursor refuse at exit 2. Entry totals and per-session counts describe
filtered matches; session context remains visible even when no entries match.
Without `--since`, the index is unchanged. Filtering still reads the existing
store index: the cursor bounds returned records, not reads. Physical metadata
walking remains; indexed storage and bounded I/O are deferred to v1.3.
It does not create a cache or change stored records.

Receipt JSON includes the complete stored `text` by default. `--text=false`
explicitly omits it. Plaintext continues to omit text unless `--text` is given;
byte counts and all other existing receipt facts remain unchanged.
