# nova-version dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-version -h`, `nova-version help`, `nova-version
<verb> -h` and the page under `docs/`. Built from the staged checkout at
927b3a9496f0cf65abea12db73b7971348c48ec1 and used as
`nova-version v1.0.1-0.20261006193659-927b3a9496f0 darwin/arm64 go1.27.1`: the
`example:` lines run as printed, then snapshot (manifest and `--bin`), diff,
report (lines and draft), moved `--dry-run`, send, version, the refusals too.

## Findings

1. An escaped blank leaks into the human-facing lines. `report` and `send` print
   ```
   REPORT TOOL name=go kind=tool version=1.27.1 raw=go\x20version\x20go1.27.1\x20darwin/arm64 path=/opt/homebrew/bin/go
   ```
   where the manifest's field holds the command `go version` (six tab-separated
   fields, a blank inside one). A person reads `go\x20version`; I expected the
   plain text (`path=` and `version=` are plain).
   Grade: NEXT.

2. A report without `--host` writes the placeholder into the prose subject:
   ```
   Subject: versions on - at 2026-10-06T19:44:05Z
   ```
   I expected the subject to drop the host when none was given.
   Grade: NEXT.

3. The status word names a short form, not the tool: `VERSION REFUSED: ...` for
   the bare and unknown-verb refusals, `SNAPSHOT REFUSED: ...`, `DIFF OK ...`,
   `SEND FAILED ...`, while the banner opens `nova-version:`. The one grammar in
   docs/STANDARD.md section 2 wants `nova-version <verb> REFUSED: ...`.
   Grade: NEXT.

4. `moved --from <sha> --to <sha>` with the same revision on both sides prints
   `MOVED OK from=... to=... added=0 deleted=0 renamed=0 verbs=300 file=...`,
   so `verbs=300` is a number a reader cannot explain and cannot tell from a
   count of the note's verbs (a `verb` the binary does not name). `moved -h`
   should say what `verbs` counts.
   Grade: NEXT.

## What the tool got right

- The first run is two commands and reads local `go version`: `example --out
  versions.tsv` writes a one-entry manifest, `snapshot --file` and `report
  --file` answer it in 49ms.
- `snapshot --bin ../bin --out ./snap.tsv` records each binary's stamp and
  revision, and `--dry-run` prints the row and writes nothing.
- `diff --from ./snap.tsv --to ./snap.tsv` is `changed=0` on identical
  snapshots.
- Refusals name every want at once and how to satisfy it: `snapshot` with no
  flags lists `--bin` and `--out` with examples; `report --file ./missing.tsv`
  says what the manifest is and that `example --out` writes one.
- `send` with no bus is honest: `SEND FAILED ... sent=uncertain` with the bus's
  own refusal (`--redis is required`) in the note and a retry offered with the
  same `--snapshot`.

READ 8/10 — the banner answers what it does, how it works and where its state
lives, and the manifest rules live in `report -h`; the escape and the subject
keep it off a 9.

USE 8/10 — every verb tried ran with no store and no network from one manifest;
the escaped field and the unexplained `verbs=` are the stumbles.

urgent=0 next=4
