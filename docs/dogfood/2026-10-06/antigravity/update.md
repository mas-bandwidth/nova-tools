# nova-update dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-update -h`, `nova-update help`, `nova-update
<verb> -h` and the page under `docs/`. Built from the staged checkout at
927b3a9496f0cf65abea12db73b7971348c48ec1 and used as
`nova-update v1.0.1-0.20261006193659-927b3a9496f0 darwin/arm64 go1.27.1`: the
`example` verb's manifest written and read, then `check`, `status`, `apply
--dry-run`, `report`, `adoption`, `help release`, `version`, the refusals too.

## Findings

1. Backslash-x escapes leak into the human-facing lines. `status` and `apply`
   print a field built from a command as
   ```
   STATUS EQUAL name=go kind=tool installed=1.27.1 latest=1.27.1 path=/opt/homebrew/bin/go source=local:go\x20version owner=caller
   ```
   and `report` prints `raw=go\x20version\x20go1.27.1\x20darwin/arm64`. A person
   reads `local:go\x20version` where the manifest says `local:go version`
   (tab-separated, space inside the field). I expected the plain text (quoted if
   it must stay one field), the way `path=` and `installed=` are plain.
   Grade: NEXT.

2. A result's continuation lines do not use `MORE`/`NOTE`. `status` prints its
   rows as fresh `STATUS EQUAL ...` lines after the `STATUS OK ...` summary, and
   `apply` prints `APPLY EQUAL ...` / `APPLY PLAN ...` after `APPLY OK ...`
   (only the last is `APPLY NOTE`). A reader can take each row for its own
   verdict.
   Grade: NEXT.

3. `report --file ./versions.tsv --draft --as zhi --to ada` (no `--host`) writes
   `Subject: versions on - at 2026-10-06T19:42:22Z`: the host placeholder `-`
   lands inside the prose subject. I expected the subject to drop the host when
   none was given.
   Grade: NEXT.

4. The status word names a short form, not the tool: `UPDATE REFUSED: ...` for
   the bare and unknown-verb refusals, `CHECK REFUSED: ...`, `APPLY OK ...`,
   while the banner opens `nova-update:`. The one grammar in docs/STANDARD.md
   section 2 wants `nova-update <verb> REFUSED: ...`.
   Grade: NEXT.

## What the tool got right

- The first run is one command and installs nothing: `example --out
  ./versions.tsv` writes a one-entry manifest and names the next command.
- `check`/`status`/`report` work with no network: the example's `latest` is
  `local:go version`, and both sides read 1.27.1 in ~17ms.
- `apply --dry-run` prints the exact argv it would run and writes nothing; a
  wrong name is refused with the manifest's actual entries listed.
- A missing `--file` is refused with what it wants and how to write one
  (`nova-update example --out ...`), and `check --json` is the same value as the
  lines.
- `help release` gives the full release pipeline usage lines.

READ 8/10 — the banner answers what it does, how it works and where its state
lives, and the first run is one command; the escape and the continuation grammar
keep it off a 9.

USE 8/10 — every verb tried (check, status, apply --dry-run, report, adoption)
ran with no store and no network from one manifest; the `\x20` in the fields and
the `on -` subject are the stumbles.

urgent=0 next=4
