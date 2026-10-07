# Dogfood: nova-version — 2026-10-06, grok (Zhi)

Read as a stranger: only `nova-version -h`, `nova-version help`, every verb's
`-h`, and the tool's page under `docs/` (`docs/SPEC-VERSION.md`). Run by Zhi
(deepseek/deepseek-v4) under dsh, the DeepSeek Harness headless runner; built and
used on a Linux bench as `nova-version v1.0.1-0.20261007153756-abb9bfecc729
linux/amd64 go1.26.6` from the tip of `sprint/mechanical-2026-10-02` at
`abb9bfecc729` (`land fp-sec77-f5-b`). Every verb (`example`, `moved`,
`snapshot`, `diff`, `report`, `send`, `version`) ran with its real flags against a
scratch directory, a scratch `--bin` and a scratch repository, `snapshot` in both
shapes, `moved` with `--dry-run` and a real write, `send` with and without a
fleet bus; the refusals ran too, and nothing was written outside the scratch
directory. No code changed; a finding is recorded here and never fixed here.

## Findings

### 1. `send -h` lists `--draft`, and `send --draft` refuses it naming a `--send` send does not have — URGENT

Command: `nova-version send --file v.tsv --draft --as caller --to reader`

Printed (exit 2):
```
SEND REFUSED: --draft and --send are exclusive (choose one); run: nova-version send -h
```
`send -h` lists `--draft  print the note only` among send's flags (with `--kind`
and `--max`, none of which appears in send's synopsis line). Expected: the listed
flag prints the note, as `report --draft` does, or it is not listed on this verb;
and the reason must not name an exclusive `--send`, a flag `send` does not have.
A stranger following the help is refused for using a flag the help gave them.
Grade: URGENT.

### 2. `report --draft --snapshot` writes the state file though the help says a report without `--send` writes nothing — URGENT

Command: `nova-version report --file v.tsv --draft --snapshot draft.tsv --as caller --to reader`

Printed (exit 0):
```
From: caller
To: reader
Subject: versions on - at 2026-10-07T15:54:56Z
```
`draft.tsv` (122 bytes) is created. `report -h`'s effect line reads "only with
`--send`, which also writes the `--snapshot` state file; without `--send`, report
reads and writes nothing (`--draft` prints the note)". Expected: a draft writes no
file; the `--snapshot` state file is written only with `--send`. The file records
`observed`, so a second draft run reports `changed=no`: a preview silently moves
state a reader was told would not move.
Grade: URGENT.

### 3. `diff` announces a changed binary and prints the same stamp on both sides — URGENT

Command: `nova-version diff --from pA.tsv --to pB.tsv` (`pB` holds the same
binary at the same stamp on `linux/arm64` instead of `linux/amd64`; every other
field is equal)

Printed (exit 0):
```
DIFF OK from=pA.tsv to=pB.tsv tools=1 changed=1
DIFF CHANGED name=nova-alpha from=v1.0.0-0.20260101000000-aaaaaaaaaaaa to=v1.0.0-0.20260101000000-aaaaaaaaaaaa
```
Expected: the changed line tells the reader what changed. A platform change is
counted but both sides print the identical stamp, so a reader or a script
comparing `from` and `to` sees no difference on a line that says there is one.
Grade: URGENT.

### 4. `report --max` is documented as a per-kind cap and is a whole-listing cap, and its `MORE` line names totals for one kind that are the totals of all — URGENT

Command: `nova-version report --file mixed.tsv --max 1` (`mixed.tsv` holds one
`engine` row and four `tool` rows; `report -h` calls `--max` a "per-kind output
cap")

Printed (exit 0):
```
REPORT OK checked=5 known=5 unknown=0 changed=- sent=- took=13ms file=mixed.tsv host=- as=- entries=5 kinds=engine,tool at=2026-10-07T15:54:56Z timeout=5s budget=1m0s max=1 snapshot=-
REPORT TOOL name=node kind=engine version=26.7.0 raw=v26.7.0 path=…/bin/node
REPORT MORE kind=tool shown=1 total=5 --max <n> raises the ceiling, --max 0 lists all
```
Expected: a per-kind cap of one shows one row of each kind (engine's row and
tool's first row) and says what is hidden per kind. Instead the cap is one row for
the whole listing, the engine row is dropped once the cap is hit, and `MORE` names
`kind=tool` with `total=5`, the count of every entry, where tool holds four. The
same line with `big.tsv` (three `tool`, one `engine`) and `--max 2` reads
`MORE kind=tool shown=2 total=4`.
Grade: URGENT.

### 5. `report`'s exit table says exit 1 means an UNKNOWN entry, but a refused delivery also exits 1 — NEXT

Command: `nova-version report --file v.tsv --send --as caller --to reader`

Printed (exit 1):
```
REPORT FAILED checked=1 known=1 unknown=0 changed=- sent=uncertain took=16ms file=v.tsv host=- as=caller entries=1 kinds=tool at=2026-10-07T15:54:56Z timeout=5s budget=1m0s max=20 snapshot=-
REPORT TOOL name=go kind=tool version=1.26.6 raw=… path=…/go/bin/go
REPORT NOTE send not confirmed: exit 2; the bus said: SEND REFUSED: --redis is required: …
```
`report -h`'s exit table reads "report: 0 every entry answered; 1 an entry is
UNKNOWN; 2 usage, or a manifest that did not read". Here every entry answered
(`unknown=0`) and the exit is 1. Expected: the table names the refused or
unconfirmed delivery, as `send`'s row does.
Grade: NEXT.

### 6. `report`'s effect line says it "reads nothing" when its whole job is to read each installed version — NEXT

Command: `nova-version report --file v.tsv`

Printed (exit 0):
```
REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=11ms file=v.tsv host=- as=- entries=1 kinds=tool at=2026-10-07T15:54:56Z timeout=5s budget=1m0s max=20 snapshot=-
REPORT TOOL name=go kind=tool version=1.26.6 raw=… path=…/go/bin/go
```
`report -h`'s effect line says "without `--send`, report reads and writes
nothing". Expected: the sentence names what is read (every installed version) and
what is not (no note, and item 2 aside no state), so the one line that states the
verb's effect is not contradicted by the verb's own first result line.
Grade: NEXT.

### 7. `moved`'s missing-revision remedy says "run git fetch" and names no fetch that would bring it — NEXT

Command: `nova-version moved --repo . --from deadbeefdeadbeefdeadbeefdeadbeefdeadbeef --to HEAD --out note.txt --dry-run`

Printed (exit 2):
```
MOVED REFUSED: revision deadbeefdeadbeefdeadbeefdeadbeefdeadbeef is not a commit in <repo> (run git fetch to bring it, or name a revision this checkout holds); run: nova-version help
```
`docs/SPEC-VERSION.md` item 4 asks that a revision not in `--repo` name "the
`git fetch` that would bring it". Expected: a fetch command a stranger can paste
(the remote and the ref), not the words "run git fetch"; `moved` fetches nothing
itself, so the reader is the one who runs it and needs the whole command.
Grade: NEXT.

### 8. `snapshot --file`'s UNKNOWN remedy is `\x20`-escaped, and `report --file` prints the same remedy plainly — NEXT

Command: `nova-version snapshot --file unk.tsv` (line 3 names a command not on
`PATH`)

Printed (exit 1):
```
SNAPSHOT FAILED checked=2 known=1 unknown=1 file=unk.tsv
SNAPSHOT UNKNOWN name=ghost reason=not_found remedy=install\x20no-such-command-xyz\x20or\x20supply\x20its\x20executable\x20path;\x20searched\x20PATH\3d…:/usr/bin:…
```
The same unknown in `report --file unk.tsv` prints the remedy as plain prose:
```
REPORT UNKNOWN name=ghost kind=tool path=- raw=-: not_found (install no-such-command-xyz or supply its executable path; searched PATH=…:/usr/bin:…)
```
Expected: a remedy a reader can read and act on. The escaped spaces and `\3d`
make snapshot's remedy one unreadable token, and the two verbs print the same fact
two ways.
Grade: NEXT.

### 9. The example manifest's own `apply` carries a `{version}` placeholder no run replaces — NEXT

Command: `nova-version example`

Printed (exit 0):
```
name	kind	installed	latest	apply	owner
# The example manifest: Go, read with go version on both sides. Replace it with your own tools.
go	tool	go version	local:go version	go install golang.org/dl/go{version}@latest	caller
```
The manifest rules say `apply` is an argv split on single spaces with no shell,
so `{version}` is one literal argument. Expected: the first-run manifest a
stranger keeps and edits holds an `apply` that runs as written, or says in the
file that `{version}` is a placeholder to replace; as written it installs nothing.
Grade: NEXT.

### 10. A bare `nova-version` leads its refusal with `VERSION`, a verb that was not run — NEXT

Command: `nova-version`

Printed (exit 2):
```
VERSION REFUSED: no verb given; the verbs are example, moved, snapshot, diff, report, send, version; run: nova-version help
```
Expected: the refusal's first word is the tool's name or the bare-door word, as
the other refusals lead with their verb (`SEND`, `REPORT`); `VERSION` reads as the
verb `version`, so the first line says a verb ran, and then says no verb was given.
Grade: NEXT.

READ 6/10 — the banner and every verb's help answer a cold reader fast and mostly
truly, and the manifest's six rules in `report -h` are complete, but the flag
tables list `--draft` on `send`, the report effect line is false about reading and
about `--snapshot`, and `--max` is described as per-kind when it is not.

USE 6/10 — every verb ran for real with no store at all, the refusals name what
the input wants, and `moved` reads both revisions' own help; but `diff` hides a
platform-only change, `report --max` cuts by a rule its help does not state, and a
draft report moves the state it says it will not.

urgent=4 next=6
