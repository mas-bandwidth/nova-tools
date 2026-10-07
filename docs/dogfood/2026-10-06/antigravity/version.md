# nova-version dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-version -h`, `nova-version help`, `nova-version
<verb> -h` and the page under `docs/`. Built from the staged checkout at
1c05149f60a74e73707c4eb6325b84194648ffcf on a Linux bench and used as
`nova-version v1.0.1-0.20261007140251-1c05149f60a7 linux/amd64 go1.26.6`. The
commands below ran from a scratch directory holding `versions.tsv` (written by
`nova-version example --out versions.tsv`), with `nova-version` on `PATH` from
`../../bin` and the checkout at `../../repo`; `py.tsv` is a hand-written one-row
manifest whose `installed` field is `python3`. Every verb ran with its real flags
(`example`, `snapshot --file`, `snapshot --bin`, `--dry-run`, `--json`, `diff`,
`report`, `report --draft`, `report --host`, `send`, `moved`, `moved --dry-run`,
`version`, `--version`), the refusals too; a finding is recorded, never fixed
here.

## Findings

1. `nova-version report --file py.tsv` prints the manifest's `installed` command
   escaped:
   ```
   REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=21ms file=py.tsv host=- as=- entries=1 kinds=tool at=2026-10-07T14:14:26Z timeout=5s budget=1m0s max=20 snapshot=-
   REPORT TOOL name=py kind=tool version=3.14.4 raw=Python\x203.14.4 path=/usr/bin/python3
   ```
   (the whole output; two lines). I expected the raw line the command printed to
   read as plain text, the way `version=` and `path=` do in the same line —
   `raw=Python 3.14.4` — because `docs/STANDARD.md:56` says a row's command is
   prose and plain in the line; here each blank becomes `\x20`, which a person
   reads as noise and cannot paste back. Grade: NEXT.

2. `nova-version report --file py.tsv --draft --as zhi --to caller` writes the
   absent host into the note's subject:
   ```
   From: zhi
   To: caller
   Subject: versions on - at 2026-10-07T14:14:26Z
   ```
   I expected the subject to drop the host when none was given (`Subject:
   versions at 2026-10-07T14:14:26Z`) rather than print `on -`. The same run's
   `REPORT OK` line spells the absent host `host=-`, so the `-` is a placeholder,
   not a value a reader can act on. Grade: NEXT.

3. `nova-version` (bare), `nova-version bogus` and `nova-version help bogus`
   print:
   ```
   VERSION REFUSED: no verb given; the verbs are example, moved, snapshot, diff, report, send, version; run: nova-version help
   VERSION REFUSED: unknown verb "bogus"; the verbs are example, moved, snapshot, diff, report, send, version; run: nova-version help
   ```
   `docs/STANDARD.md:70` gives the grammar of a call the tool cannot run as
   `<tool>[ <verb>] REFUSED: <what was wrong>; run: <tool> help`, so the bare
   call wants `nova-version REFUSED: ...` and the unknown verb wants
   `nova-version bogus REFUSED: ...`; `VERSION` names neither the tool
   (`nova-version`) nor a verb that ran. The verb-level lines are right under
   `docs/STANDARD.md:57` ("After the verb's name the first word is `OK`,
   `REFUSED` or `FAILED`"): `nova-version snapshot` printed `SNAPSHOT REFUSED:
   missing --bin, --out; ...`, `nova-version diff --from snap.tsv --to snap.tsv`
   printed `DIFF OK from=snap.tsv to=snap.tsv tools=5 changed=0`, and
   `nova-version send --file versions.tsv --as zhi --to caller` printed `SEND
   FAILED checked=1 ... sent=uncertain`. Only the no-verb and unknown-verb
   refusals use the short form. Grade: NEXT.

4. `nova-version moved --from 08d5d63f4 --to 1c05149f6 --repo ../../repo --out
   moved-note.md` prints:
   ```
   MOVED OK from=08d5d63f4651b134ad1e28184ad352e79a99b71d to=1c05149f60a74e73707c4eb6325b84194648ffcf added=0 deleted=0 renamed=0 verbs=310 file=moved-note.md
   ```
   The note it writes holds only `MOVED from=... to=... at=...`; no verb list is
   in it, so `verbs=310` cannot be derived from the note, and `nova-version moved
   -h` lists `--from`, `--to`, `--repo`, `--out`, `--timeout`, `--budget` and
   `--dry-run` and never says what `verbs` counts. `docs/SPEC-VERSION.md` rule 3
   names the field but not its unit. I expected the verb's help to say it counts
   the (tool, verb) pairs the `--to` build answers. Grade: NEXT.

## What the tool got right

- The first run is two commands and reads local `go version`: `nova-version
  example --out versions.tsv` writes a one-entry manifest and names the next
  command; the same call again is `EXAMPLE OK wrote=versions.tsv entries=1
  unchanged=true`; a file holding anything else is refused (`EXAMPLE REFUSED:
  other.txt exists and is not the example manifest; ...`), never overwritten.
- `nova-version snapshot --bin ../../bin --out snap.tsv` records five binaries as
  `SNAPSHOT ROW` lines carrying `name`, `stamp`, `revision` and `platform`, and
  `--dry-run` prints the same rows plus `dry_run=true` with `SNAPSHOT NOTE dry
  run: snap2.tsv not written`; `--json` renders the same value as the lines.
- `nova-version diff --from snap.tsv --to snap.tsv` is `DIFF OK from=snap.tsv
  to=snap.tsv tools=5 changed=0` on identical snapshots.
- Refusals name every want at once and how to satisfy it: `nova-version snapshot`
  lists `--bin` and `--out` with examples; a mixed-stamp `--bin` names both
  binaries and both stamps and points at `nova-update release build`;
  `nova-version moved` with no flags names all four missing flags.
- `nova-version send` with no bus is honest: `SEND FAILED ... sent=uncertain`,
  exit 1, the bus's own `--redis is required` refusal in the note, and a retry
  offered with the same `--snapshot`.

READ 8/10 — the banner answers what it does, how it works and where its state
lives (`THE MANIFEST is the file --file names`), `report -h` carries the six
manifest rules, and every verb's `-h` quotes the exit table; the escaped `raw=`
field and the `-` subject keep it off a 9.

USE 8/10 — every verb ran with no store and no network from one manifest, the
first run is two pasteable commands, and the refusals carry a remedy; the
escaped field, the placeholder subject and the undefined `verbs=` are the
stumbles.

urgent=0 next=4
