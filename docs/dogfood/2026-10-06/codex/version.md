# nova-version dogfood — codex, 2026-10-06

Read as a stranger: only `nova-version -h`, `nova-version help`, every
`nova-version <verb> -h`, and the tool's page under `docs/`
(`docs/SPEC-VERSION.md`). Run by Zhi (deepseek/deepseek-v4) under dsh, the
DeepSeek Harness headless runner; built and used on a Linux bench as
`nova-version v1.0.1-0.20261007144630-1dc3dd9cdf2d linux/amd64 go1.26.6` from
the tip of `sprint/mechanical-2026-10-02` at
`1dc3dd9cdf2d380c1b24ac6601e9e8be7bd39eb7`. Every verb (`example`, `moved`,
`snapshot`, `diff`, `report`, `send`, `version`) ran with its real flags against
a scratch directory and a scratch `--bin`, and the refusals ran too. Nothing was
written outside the scratch directory.

## Findings

### 1. `send -h` lists `--draft`, and `send --draft` always refuses it — URGENT

Command: `nova-version send --file m.tsv --draft --as caller --to reader`

Printed (exit 2):
```
SEND REFUSED: --draft and --send are exclusive (choose one); run: nova-version send -h
```
`nova-version send -h` lists `--draft  print the note only` among send's flags,
and the unknown-flag refusal repeats them (`the flags of send are --as, --budget, --draft, --file, --host, --kind, --max, --snapshot, --timeout, --to`). The same
refusal is printed with `--as`/`--to` present and absent. Expected: the listed
flag prints the note, or it is not listed on this verb; and the reason must not
name an exclusive `--send`, a flag `send` does not have. A stranger following the
help is refused for using a flag the help gave them.
Grade: URGENT.

### 2. `report --draft` writes the `--snapshot` state though the help says it writes nothing — URGENT

Command: `nova-version report --file m.tsv --draft --snapshot draft.tsv --as caller --to reader`

Printed (exit 0), and `draft.tsv` is created (`-rw------- 100`):
```
From: caller
To: reader
Subject: versions on - at 2026-10-07T14:55:04Z
```
`report -h`'s effect line reads "without --send, report reads and writes nothing
(--draft prints the note)", and the `--draft` flag is "print the note only".
Expected: a draft writes no file; the `--snapshot` state file is written only by
`--send`. The file records `observed` for the report, so a preview silently moves
state a reader was told would not move.
Grade: URGENT.

### 3. `diff` announces a change and prints the same value on both sides — URGENT

Command: `nova-version diff --from A.tsv --to B.tsv` (`binA` differs only in
`revision`, `binB` is removed, `binC` added)

Printed (exit 0):
```
DIFF OK from=A.tsv to=B.tsv tools=3 changed=3
DIFF CHANGED name=binA from=v1.0.0-0.20260101000000-aaaaaaaaaaaa to=v1.0.0-0.20260101000000-aaaaaaaaaaaa
DIFF CHANGED name=binB from=v1.0.0-0.20260101000000-aaaaaaaaaaaa to=-
```
Expected: `from` and `to` differ whenever the rows differ. A row that differs
only in `revision` (or only in `platform`) prints the identical stamp on both
sides — the line says a binary changed and shows nothing that changed, and the
revision the snapshot exists to record is exactly what is hidden. `changed=1`
cannot be read. (SPEC-VERSION.md item 6 prescribes stamp-only, so the spec and
the code share the bug.)
Grade: URGENT.

### 4. Prose fields are escaped instead of plain — NEXT

Commands: `nova-version report --file m.tsv` and
`env PATH=/usr/bin:/bin nova-version snapshot --file unknown.tsv`

Printed (exit 0 and exit 1):
```
REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=7ms file=m.tsv host=- as=- entries=1 kinds=tool at=2026-10-07T14:55:09Z timeout=5s budget=1m0s max=20 snapshot=-
REPORT TOOL name=nvtool kind=tool version=1.2.3 raw=nvtool\x201.2.3\x20linux/amd64 path=/tmp/nvcodex/nvtool
```
```
SNAPSHOT FAILED checked=1 known=0 unknown=1 file=unknown.tsv
SNAPSHOT UNKNOWN name=gone reason=not_found remedy=install\x20definitely-not-a-cmd-xyz\x20or\x20supply\x20its\x20executable\x20path;\x20searched\x20PATH\x3d/usr/bin:/bin
```
Expected: plain prose in the line (`raw=nvtool 1.2.3 linux/amd64`,
`remedy=install ... PATH=/usr/bin:/bin`), as the standard's "a row's reason or
command is prose: plain in the line, `text` in the JSON". The command printed
`nvtool 1.2.3 linux/amd64`; a person reads `\x20` and `\x3d`.
Grade: NEXT.

### 5. A draft's subject prints the host placeholder — NEXT

Command: `nova-version report --file m.tsv --draft --as caller --to reader`

Printed (exit 0):
```
From: caller
To: reader
Subject: versions on - at 2026-10-07T14:55:04Z
```
Expected: with no `--host`, the subject drops the host phrase (`Subject: versions at ...`) rather than printing `on -`.
Grade: NEXT.

### 6. `moved` names its missing flags in three refusal lines — NEXT

Command: `nova-version moved --from 1dc3dd9cdf2d380c1b24ac6601e9e8be7bd39eb7`

Printed (exit 2):
```
MOVED REFUSED: --to is required; it wants the revision to compare to; refusing to guess; run: nova-version help
MOVED REFUSED: --repo is required; it wants the checkout holding both revisions; refusing to guess; run: nova-version help
MOVED REFUSED: --out is required; it wants the path of the note to write; refusing to guess; run: nova-version help
```
Expected: one line naming every missing flag at once, the way `snapshot` with no
flags prints `SNAPSHOT REFUSED: missing --bin, --out; ...`. Three refusal lines
triple the remedy and read as three separate faults.
Grade: NEXT.

### 7. A spent bound is reported as a broken git, with the wrong remedy — NEXT

Command: `nova-version moved --from 1dc3dd9cdf2d380c1b24ac6601e9e8be7bd39eb7 --to 1dc3dd9cdf2d380c1b24ac6601e9e8be7bd39eb7 --repo ../../repo --out moved.md --timeout 1ns`

Printed (exit 2):
```
MOVED REFUSED: cannot run git against ../../repo (budget) (supply a --repo and an environment where git answers); run: nova-version help
```
Expected: the spent deadline named, and the `--timeout`/`--budget` that answers
it, as `snapshot` names its bounds. `git` answered; the 1ns bound stopped it, and
the remedy sends the reader to repair an environment that is fine.
Grade: NEXT.

### 8. The status word is the short verb name, not the tool — NEXT

Commands: `nova-version` and `nova-version bogus`

Printed (exit 2):
```
VERSION REFUSED: no verb given; the verbs are example, moved, snapshot, diff, report, send, version; run: nova-version help
```
`nova-version bogus` prints `VERSION REFUSED: unknown verb "bogus"; ...`. Expected:
the leading word to be `nova-version` (`nova-version REFUSED: ...`), so a line can
be matched to its tool; the verb refusals use the short name (`SNAPSHOT REFUSED:`,
`DIFF REFUSED:`), which a reader must translate.
Grade: NEXT.

## What held

- The first run is the banner's four lines exactly: `example --out versions.tsv`
  writes a one-entry manifest (and refuses to overwrite a different file:
  `EXAMPLE REFUSED: versions.tsv exists and is not the example manifest; refusing to overwrite it (name another --out)`), then `snapshot --file`, `report --file`
  and `version` all answer from it. A second identical `example --out` prints
  `unchanged=true`, exit 0.
- `snapshot --bin` reads each binary's own `version`, sorts by name, writes the
  header and one row, notes a skipped symlink by name
  (`SNAPSHOT NOTE "nova-link": symlink, not a regular file; not snapshotted`),
  and refuses a mixed set, a bad version line, an unreadable `--bin` and an empty
  `--bin`, each with a remedy and no partial `--out`.
- `diff` refuses a missing file, a wrong header and a wrong arity, naming the file
  and the `snapshot` that writes one, and reads both files before either refusal.
- `report`'s manifest collects every problem on one line (`line 2: unknown kind bogus (use harness,engine,model,tool,pin); line 3: 7 fields, want 6 ...`),
  `--kind` and `--max` filter and bound (`REPORT MORE kind=tool shown=1 total=2 --max <n> raises the ceiling, --max 0 lists all`), and an unknown installed
  command is `REPORT FAILED ... unknown=1` with the searched `PATH`.
- `moved --dry-run` builds both revisions and prints `MOVED OK ... added=0 deleted=0 renamed=0 verbs=311 file=moved.md dry_run=true` in ~14s, writing no
  `--out`; the real run writes the note. A bad revision, a non-checkout `--repo`
  and a non-positive bound are each refused.
- `send` and `report --send` are honest with no bus: `FAILED ... sent=uncertain`
  with the bus's own `--redis is required` refusal in the note and a retry offered
  with the same `--snapshot`.
- `version` and `--version` are one spelling; `version --json` renders the same
  value; a positional after `version` is refused.

READ 8/10 — the banner answers what it does, how it works and where its state
lives, every verb's `-h` is complete and the manifest rules live in `report -h`;
the escaped prose, the `on -` subject and a `send -h` flag that can never be used
keep it off a 9.

USE 8/10 — one manifest carried every verb from a scratch directory with no store
and no network, and the refusals name their remedy; a help flag that always
refuses, a draft that writes delivery state, a change `diff` cannot show and two
misleading `moved` refusals are the stumbles.

urgent=3 next=5
