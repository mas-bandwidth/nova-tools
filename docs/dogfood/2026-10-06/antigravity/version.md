# nova-version dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-version -h`, `nova-version help`, `nova-version <verb> -h` and the page under `docs/`. Built from the staged checkout at
1c05149f60a74e73707c4eb6325b84194648ffcf and used as
`nova-version v1.0.1-0.20261007140251-1c05149f60a7 linux/amd64 go1.26.6`: the
`example:` lines run as printed, then snapshot (manifest and `--bin`), diff,
report (lines and draft), moved `--dry-run`, send, version, the refusals too.
Commands ran in one scratch directory; `nova-version example --out versions.tsv`
wrote `versions.tsv` and `nova-version snapshot --bin ../bin --out ./snap.tsv`
wrote `snap.tsv` first.

## Findings

1. An escaped blank leaks into the human-facing lines. The command as typed:

   `nova-version report --file versions.tsv`

   first lines of its output:
   ```
   REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=36ms file=versions.tsv host=- as=- entries=1 kinds=tool at=2026-10-07T14:11:01Z timeout=5s budget=1m0s max=20 snapshot=-
   REPORT TOOL name=go kind=tool version=1.26.6 raw=go\x20version\x20go1.26.6\x20linux/amd64 path=/tmp/nvzhi/bin/go
   ```
   `nova-version send --file versions.tsv --as zhi --to reader` prints the same
   `raw=` field. The manifest's third field is the command `go version` (six
   tab-separated fields, a blank inside one), so I expected the plain text there
   (`raw=go version go1.26.6 linux/amd64`), as `path=` and `version=` beside it
   are plain; a person reads `go\x20version`.
   Grade: NEXT.

2. A report without `--host` writes the placeholder into the prose subject. The
   command as typed:

   `nova-version report --file versions.tsv --draft --as zhi --to reader`

   first three lines of its output:
   ```
   From: zhi
   To: reader
   Subject: versions on - at 2026-10-07T14:11:01Z
   ```
   I expected the subject to drop the host when none was given (`Subject: versions at ...`), not to print `on -`.
   Grade: NEXT.

3. The status word names a short form, not the tool. The commands as typed and
   the first line each printed:

   `nova-version`
   ```
   VERSION REFUSED: no verb given; the verbs are example, moved, snapshot, diff, report, send, version; run: nova-version help
   ```
   `nova-version bogus`
   ```
   VERSION REFUSED: unknown verb "bogus"; the verbs are example, moved, snapshot, diff, report, send, version; run: nova-version help
   ```
   `nova-version snapshot`
   ```
   SNAPSHOT REFUSED: missing --bin, --out; refusing to guess (--bin wants the directory holding the binaries, for example ./bin, and --out the TSV snapshot to write, for example ./before.tsv; or give --file <manifest> alone to count which adopted tools answer); run: nova-version help
   ```
   `nova-version diff --from ./snap.tsv --to ./snap.tsv`
   ```
   DIFF OK from=./snap.tsv to=./snap.tsv tools=1 changed=0
   ```
   `nova-version send --file versions.tsv --as zhi --to reader`
   ```
   SEND FAILED checked=1 known=1 unknown=0 changed=- sent=uncertain took=29ms file=versions.tsv host=- as=zhi entries=1 kinds=tool at=2026-10-07T14:11:01Z timeout=5s budget=1m0s max=20 snapshot=-
   SEND TOOL name=go kind=tool version=1.26.6 raw=go\x20version\x20go1.26.6\x20linux/amd64 path=/tmp/nvzhi/bin/go
   SEND NOTE send not confirmed: exit 2; the bus said: SEND REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to g... (retry --send with the same --snapshot)
   ```
   So the bare and unknown-verb refusals printed `VERSION REFUSED:`, `snapshot`
   printed `SNAPSHOT REFUSED:`, `diff` printed `DIFF OK` and `send` printed
   `SEND FAILED`, while the banner opens `nova-version:`. docs/STANDARD.md:57
   ("After the verb's name the first word is `OK`, `REFUSED` or `FAILED`")
   judges `DIFF OK` and `SEND FAILED` and gives the refusals their status word;
   docs/STANDARD.md:70 gives the refusal form `<tool>[ <verb>] REFUSED: <what was wrong>; run: <tool> help`, which supports the refusals. Read together,
   the leading word is the short verb name plus the status, not the tool name;
   other tools lead with their own name (`nova-ci REFUSED:`), so a reader
   matching a line to the tool must translate. I expected the leading word to
   be `nova-version` (`nova-version DIFF OK ...`, `nova-version REFUSED: ...`).
   Grade: NEXT.

4. `moved` prints `verbs=` with no statement of what it counts. The command as
   typed:

   `nova-version moved --from 1c05149f60a74e73707c4eb6325b84194648ffcf --to 1c05149f60a74e73707c4eb6325b84194648ffcf --repo ../repo --out ./moved.md --dry-run`

   first lines of its output:
   ```
   MOVED OK from=1c05149f60a74e73707c4eb6325b84194648ffcf to=1c05149f60a74e73707c4eb6325b84194648ffcf added=0 deleted=0 renamed=0 verbs=310 file=./moved.md dry_run=true
   MOVED from=1c05149f60a74e73707c4eb6325b84194648ffcf to=1c05149f60a74e73707c4eb6325b84194648ffcf at=2026-10-07T14:09:14Z
   ```
   With the same revision on both sides `added=0 deleted=0 renamed=0`, yet
   `verbs=310`: `moved -h` never says whether the number counts the verbs at
   `--from`, at `--to`, or both, and the note under the line does not name it
   either. I expected the help (or the note) to say what `verbs=` counts, so a
   reader can tell the number from a count of the note's own entries.
   Grade: NEXT.

## What the tool got right

- The first run is two commands and reads local `go version`:
  `nova-version example --out versions.tsv` writes a one-entry manifest, and
  `nova-version report --file versions.tsv` (and `snapshot --file versions.tsv`) answers it in tens of milliseconds.
- `nova-version snapshot --bin ../bin --out ./snap.tsv` records each binary's
  stamp and revision (`SNAPSHOT OK ... tools=1`), and `--dry-run` prints the row
  and writes nothing.
- `nova-version diff --from ./snap.tsv --to ./snap.tsv` is `changed=0` on
  identical snapshots.
- Refusals name every want at once and how to satisfy it: `snapshot` with no
  flags lists `--bin` and `--out` with examples; `report --file ./missing.tsv`
  says what the manifest is and that `example --out` writes one.
- `send` with no bus is honest: `SEND FAILED ... sent=uncertain` with the bus's
  own refusal (`--redis is required`) in the note and a retry offered with the
  same `--snapshot`.

READ 8/10 — the banner answers what it does, how it works and where its state
lives, and the manifest rules live in `report -h`; the escaped field and the
subject keep it off a 9.

USE 8/10 — every verb tried ran with no store and no network from one manifest;
the short status prefix and the unexplained `verbs=` are the stumbles.

urgent=0 next=4
