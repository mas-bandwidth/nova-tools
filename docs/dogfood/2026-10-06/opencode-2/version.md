# nova-version dogfood — opencode-2, 2026-10-06

Read as a stranger: only `nova-version -h`, `nova-version help`, every
`nova-version <verb> -h`, and the tool's page under `docs/`
(`docs/SPEC-VERSION.md`). Run by Zhi (deepseek/deepseek-v4) under dsh, the
DeepSeek Harness headless runner; built and used on a Linux bench as
`nova-version v1.0.1-0.20261007155708-174eac229d10 linux/amd64 go1.26.6` from
the tip of `sprint/mechanical-2026-10-02` at
`174eac229d10f84680b90dea2976b375dbd2c7ad`. Every verb (`example`, `moved`,
`snapshot`, `diff`, `report`, `send`, `version`) ran with its real flags against
a scratch directory, a scratch `--bin` of stubs and the real checkout for
`moved`, and the refusals ran too. Nothing was written outside the scratch
directory; no code changed.

## Findings

### 1. `send -h` lists `--draft`, and `send --draft` always refuses it — URGENT

Command: `nova-version send --file m.tsv --draft --as caller --to reader`

Printed (exit 2):
```
SEND REFUSED: --draft and --send are exclusive (choose one); run: nova-version send -h
```
`nova-version send -h` lists `--draft  print the note only` among send's flags,
and the unknown-flag refusal repeats them (`the flags of send are --as, --budget,
--draft, --file, --host, --kind, --max, --snapshot, --timeout, --to`) — `--send`
is not one of send's flags. Expected: the listed flag prints the note, or it is
not listed on this verb; and the reason must not name an exclusive `--send` that
send does not have. A stranger following the help is refused for using a flag the
help gave them, and `send` has no preview path at all.
Grade: URGENT.

### 2. `report --draft --snapshot` writes the state file though the help says it writes nothing — URGENT

Command: `nova-version report --file m.tsv --draft --snapshot fresh.tsv --as caller --to reader`

Printed (exit 1), and `fresh.tsv` is created (`-rw------- 324`):
```
From: caller
To: reader
Subject: versions on - at 2026-10-07T16:04:00Z
```
`report -h`'s effect line reads "without --send, report reads and writes nothing
(--draft prints the note)", and `--snapshot` is "state file of what was observed
and confirmed sent". Expected: a draft writes no file; the state file is written
only by `--send`. The file records the observation, so a preview moves state, and
a later real `report --send --snapshot fresh.tsv` reads `changed=no` and prints no
CHANGED lines though nothing was ever sent.
Grade: URGENT.

### 3. `moved` names its missing flags in one refusal line each — NEXT

Command: `nova-version moved`

Printed (exit 2):
```
MOVED REFUSED: --from is required; it wants the revision to compare from; refusing to guess; run: nova-version help
MOVED REFUSED: --to is required; it wants the revision to compare to; refusing to guess; run: nova-version help
MOVED REFUSED: --repo is required; it wants the checkout holding both revisions; refusing to guess; run: nova-version help
MOVED REFUSED: --out is required; it wants the path of the note to write; refusing to guess; run: nova-version help
```
Expected: one line naming every missing flag at once, the way `snapshot` with no
flags prints `SNAPSHOT REFUSED: missing --bin, --out; ...`. Four lines read as
four separate faults, and `help moved extra` buries its one positional refusal
(`takes no positional arguments, got "extra"`) after these four.
Grade: NEXT.

### 4. A spent bound is reported as a broken git, with the wrong remedy — NEXT

Command: `nova-version moved --from 174eac229 --to 174eac229 --repo . --out x.md --timeout 1ns`

Printed (exit 2):
```
MOVED REFUSED: cannot run git against . (budget) (supply a --repo and an environment where git answers); run: nova-version help
```
Expected: the spent deadline named, and the `--timeout`/`--budget` that answers
it, as `snapshot` names its bounds. `git` answered the repo; the 1ns bound
stopped it, and the remedy sends the reader to repair an environment that is
fine. `--budget 1ns` prints the identical line, so a spent per-child `--timeout`
is also called a budget.
Grade: NEXT.

### 5. Prose fields are escaped instead of plain — NEXT

Commands: `nova-version report --file m.tsv` and `nova-version snapshot --file m.tsv`

Printed (exit 1):
```
REPORT TOOL name=go kind=tool version=1.26.6 raw=go\x20version\x20go1.26.6\x20linux/amd64 path=<home>/go/bin/go
```
```
SNAPSHOT UNKNOWN name=ghost reason=not_found remedy=install\x20definitely-not-a-cmd-xyz\x20or\x20supply\x20its\x20executable\x20path;\x20searched\x20PATH\x3d<home>/...
```
Expected: plain prose in the line (`raw=go version go1.26.6 linux/amd64`,
`remedy=install ... PATH=<home>/...`), as the standard's "a row's reason or
command is prose: plain in the line, `text` in the JSON". The command really
printed `go version go1.26.6 linux/amd64`; a person reads `\x20` and `\x3d`.
Grade: NEXT.

### 6. A draft's subject prints the host placeholder — NEXT

Command: `nova-version report --file m.tsv --draft --as caller --to reader`

Printed (exit 1):
```
From: caller
To: reader
Subject: versions on - at 2026-10-07T16:03:25Z
```
Expected: with no `--host`, the subject drops the host phrase
(`Subject: versions at ...`) rather than printing `on -`. With `--host bench1`
the same line reads `Subject: versions on bench1 at ...`, so the placeholder is
the absent value, not a word.
Grade: NEXT.

### 7. The status word is the short verb name, not the tool — NEXT

Commands: `nova-version`, `nova-version bogus` and `nova-version --nope`

Printed (exit 2):
```
VERSION REFUSED: no verb given; the verbs are example, moved, snapshot, diff, report, send, version; run: nova-version help
```
`nova-version bogus` prints `VERSION REFUSED: unknown verb "bogus"; ...`, and
`nova-version --nope` prints `VERSION REFUSED: unknown verb "--nope"; ...`.
Expected: the leading word to be `nova-version` (`nova-version REFUSED: ...`), so
a line can be matched to its tool, and a leading `--flag` answered as an unknown
flag rather than an unknown verb. The verb refusals use the short name
(`SNAPSHOT REFUSED:`, `DIFF REFUSED:`), which a reader must translate.
Grade: NEXT.

### 8. A non-executable binary is reported as not found — NEXT

Command: `nova-version snapshot --bin binnonreg --out out/nonexec.tsv` (`binnonreg/nova-nonexec` is a regular file, mode 644)

Printed (exit 2):
```
SNAPSHOT REFUSED: cannot read nova-nonexec version (not_found) (repair the build there: go build ./cmd/nova-nonexec); run: nova-version help
```
Expected: the file is present, only not executable — name that and the
`chmod +x` that fixes it, rather than `not_found` (the file was found) with a
rebuild remedy that changes nothing.
Grade: NEXT.

## What held

- The banner answers what it does, how it works and the first run; `nova-version`,
  `-h`, `--help` and `help` are one text at exit 0, and `help <verb>` and
  `<verb> -h` are one text at exit 0.
- `example` writes the one-tool manifest, refuses to overwrite a different file
  (`EXAMPLE REFUSED: other.tsv exists and is not the example manifest; refusing to
  overwrite it (name another --out)`), prints `unchanged=true` on a second
  identical run, and the printed lines run in order (`example --out`, then
  `snapshot --file`, `report --file`, `version`).
- `snapshot --bin` reads each binary's own `version` (never the file's name),
  sorts, writes the header and one row each, notes a skipped symlink by name
  (`SNAPSHOT NOTE "nova-link": symlink, not a regular file; not snapshotted`),
  and refuses a mixed set naming both names and both stamps, an exit-non-zero or
  unparseable `version`, a `--bin` with no `nova-*` file, each missing flag, both
  shapes together, and a non-positive bound, each with a remedy and no partial
  `--out`. `--max` bounds the rows with `SNAPSHOT MORE kind=row shown=1 total=3`
  and `--json` carries the same items plus `more`.
- `snapshot --file` counts known and unknown and names each unknown with the
  searched `PATH`; `--max` does not change the count.
- `diff` reads both files before refusing, names a missing file and a wrong
  header with the `snapshot` remedy, prints one `DIFF CHANGED` per row that
  differs (a revision-only change shows both differing stamps), prints no line
  for an unchanged row, and renders the same items under `--json`.
- `report` holds the six-rule manifest and names every problem at once (`line 2:
  unknown kind bogus ...; line 3: 5 fields, want 6 ...`), refuses a duplicate name
  and an empty field, filters with `--kind` and bounds per kind with a `REPORT
  MORE kind=tool shown=1 total=3` line, and is honest with no bus: `REPORT FAILED
  ... sent=uncertain` with the bus's own `--redis is required` refusal in the note
  and a retry offered with the same `--snapshot`.
- `moved --dry-run` builds both revisions and prints the note without writing
  `--out` (32s, `added=0 deleted=0 renamed=0 verbs=311`, and the note lists each
  added/deleted flag with its tool and verb); the real run writes the note; an
  empty diff is exit 0; a non-checkout `--repo`, an unknown revision and a
  non-positive bound are each refused by name.
- `version` and `--version` are one spelling, `version --json` renders the same
  value, and a positional after `version` is refused.

READ 8/10 — the banner answers what it does, how it works and where its state
lives, and every verb's `-h` is complete; the `send -h` flag that can never be
used, the draft that writes delivery state, and the escaped prose keep it off a 9.

USE 8/10 — one scratch directory carried every verb with no store and no network,
and the refusals name their remedy; a help flag that always refuses, a preview
that moves state, two misleading `moved` refusals and one top-level naming stumble
are the stumbles.

urgent=2 next=6
