# Dogfood: nova-version — 2026-10-06, dsh (zhi)

One friend, one tool, cold. I read only `nova-version -h`, `nova-version help`, every
verb's `-h`, and the pages under `docs/` the banner names (SPEC-VERSION.md, and
SPEC-UPDATE.md for the manifest verbs `snapshot --file`, `report` and `send`), then used
every verb at least once with its real flags. Built from the staged checkout at
cb5fb8d4c329 and used as `nova-version v1.0.1-0.20261006204015-cb5fb8d4c329 linux/amd64
go1.26.6` on the Linux bench (no go command runs on the working machine): the `example:` lines run as
printed; `snapshot` in both shapes; `diff`; `report` lines and `--draft`; `moved`
`--dry-run` and a real write over the checkout and over scratch repos; `send` with no
bus; `version`; the refusals too. 15–40 minutes of use; no code changed, a finding is
recorded here and never fixed here.

## Findings

1. `report -h` says a run without `--send` writes nothing, but `report --snapshot <path>`
   writes the state file.

       $ nova-version report --file m.tsv --snapshot stateA.json
       REPORT FAILED checked=2 known=1 unknown=1 changed=yes sent=- took=36ms file=m.tsv host=- as=- entries=2 kinds=tool at=2026-10-06T20:59:03Z timeout=5s budget=1m0s max=20 snapshot=stateA.json
       REPORT TOOL name=go kind=tool version=1.26.6 raw=go\x20version\x20go1.26.6\x20linux/amd64 path=<home>/go/bin/go
       REPORT UNKNOWN name=ghost kind=tool path=- raw=-: not_found (install /nonexistent/ghost-cmd or supply its executable path; searched PATH=<home>/sdk/bin:...)
       (stateA.json was written, 188 bytes; the same with `--draft --snapshot`)

   I expected no file, because `report -h`'s effect line is "delivery: sends beyond this
   machine; only with --send, which also writes the --snapshot state file; without
   --send, report reads and writes nothing (--draft prints the note)". The write is what
   SPEC-UPDATE.md rule 25 demands ("every run with `--snapshot` writes `observed`"), so
   the code is right and the help line is the part that lies: a reader who trusts it
   cannot tell why a state file appeared, and a `--draft` that claims to write nothing
   does. Grade: URGENT.

2. `moved`'s status line and its note both spell the fields `added=`/`deleted=`, but the
   status line counts tools while the note uses them for added and removed verbs and
   flags; one run prints `added=0` above its own `added=` rows.

       $ nova-version moved --from a8ef557e2 --to cb5fb8d4c --repo <checkout> --out note.md --dry-run
       MOVED OK from=a8ef557e22b48506835fbdae7e517e18a5129d36 to=cb5fb8d4c3290f710d22a21a86aa8d229e4db905 added=0 deleted=0 renamed=0 verbs=300 file=note.md dry_run=true
       MOVED from=a8ef557e22b48506835fbdae7e517e18a5129d36 to=cb5fb8d4c3290f710d22a21a86aa8d229e4db905 at=2026-10-06T20:55:51Z
       added=--check tool=nova-sprint verb=friend

   A scratch pair shows the scope exactly: adding only the verb `beta` to a tool prints
   `MOVED OK ... added=0 deleted=0 renamed=0 verbs=3 ...` (the total verbs rose 2->3)
   above `added=beta tool=toola`; adding only a tool prints `added=1`; adding only a flag
   prints `added=0` above `added=--loud ...`. I expected the count to describe the rows
   the same run prints, and SPEC-VERSION.md moved item 2 says the inventory is "the tools
   and verbs present" whose "added and deleted entries" it reports. As printed, a script
   that reads the one-line result concludes nothing moved while the note lists the
   change; the field is defined nowhere in `moved -h` or the docs page. Grade: NEXT.

3. `moved`'s rename statement is an exact phrase no help or docs page gives; the natural
   wording is silently read as an add plus a delete.

       $ nova-version moved --from 8f15162e8d3d --to 3c3abdca26a7 --repo <scratch> --out note.md --dry-run
       (commit message: "rename toolb to toold")
       MOVED OK from=8f15162e8d3d8256f30d088886f685fb471ad3f5 to=3c3abdca26a7aa24c8634d20e54d5af09b548b4d added=1 deleted=1 renamed=0 verbs=1 file=note.md dry_run=true
       MOVED from=8f15162e8d3d8256f30d088886f685fb471ad3f5 to=3c3abdca26a7aa24c8634d20e54d5af09b548b4d at=2026-10-06T21:00:23Z
       added=toold

   The same rename with "renamed toolb to toold" prints `added=0 deleted=0 renamed=1`
   and `renamed=toolb->toold`, in the commit message and in a `MOVED` file alike. I
   expected SPEC-VERSION.md moved item 2 ("a rename is stated only when the commit
   message or a `MOVED` file says so") to be enough to reach `renamed=`, but the literal
   `renamed <old> to <new>` is the trigger and neither `moved -h` nor the docs page
   states it, so `renamed=` is effectively unreachable for a reader who does not guess
   the phrase. Grade: NEXT.

4. `send --draft` refuses by naming `--send`, which the `send` verb itself refuses as an
   unknown flag, so the stated choice cannot be made on that verb.

       $ nova-version send --file m.tsv --as a --to b --draft
       SEND REFUSED: --draft and --send are exclusive (choose one); run: nova-version send -h
       [exit=2]
       $ nova-version send --file m.tsv --as a --to b --send
       SEND REFUSED: unknown flag --send; the flags of send are --as, --budget, --draft, --file, --host, --kind, --max, --snapshot, --timeout, --to; did you mean --kind?; run: nova-version send -h

   I expected the refusal to name the conflict that exists on this verb (`--draft` on a
   verb that already sends, per SPEC-UPDATE.md line 423, "`send` implying `--send`") and
   the draft path that works, `nova-version report --draft`. Grade: NEXT.

5. Human lines carry escaped spaces and `=` in an item's prose while the JSON of the
   same run is plain.

       $ nova-version snapshot --file m.tsv
       SNAPSHOT FAILED checked=2 known=1 unknown=1 file=m.tsv
       SNAPSHOT UNKNOWN name=ghost reason=not_found remedy=install\x20/nonexistent/ghost-cmd\x20or\x20supply\x20its\x20executable\x20path;\x20searched\x20PATH\x3d<home>/...
       [exit=1]
       $ nova-version report --file m.tsv
       REPORT TOOL name=go kind=tool version=1.26.6 raw=go\x20version\x20go1.26.6\x20linux/amd64 path=<home>/go/bin/go

   The JSON of the same unknown row carries the plain text
   (`"remedy":"install /nonexistent/ghost-cmd or supply its executable path; searched PATH=..."`).
   I expected the line to be plain too: the standard says a row's reason or command is
   prose, "plain in the line, `text` in the JSON". Grade: NEXT.

6. `send`'s note truncates nova-bus's refusal mid-word and drops the nested remedy.

       $ nova-version send --file m.tsv --as alice --to bob
       SEND FAILED checked=3 known=1 unknown=2 changed=- sent=uncertain took=43ms file=m.tsv host=- as=alice entries=3 kinds=model,tool at=2026-10-06T20:55:11Z timeout=5s budget=1m0s max=20 snapshot=-
       SEND TOOL name=go kind=tool version=1.26.6 raw=go\x20version\x20go1.26.6\x20linux/amd64 path=<home>/go/bin/go
       SEND UNKNOWN name=ghost kind=tool path=- raw=-: not_found (install /nonexistent/ghost-cmd or supply its executable path; searched PATH=<home>/sdk/bin:...)
       SEND NOTE send not confirmed: exit 2; the bus said: SEND REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to g... (retry --send with the same --snapshot)

   I expected the bus's own remedy (its `run:` half) to survive the cap whole; "refusing
   to g..." cuts the one sentence that would tell a reader what to do next, and the
   parenthetical retry is the only half left. Grade: NEXT.

## What worked

Every verb answers `-h` at exit 0 before reading anything; a missing flag is refused
with its want and a remedy in one turn (`snapshot` names `--bin`/`--out`/`--file` at
once, `moved` names all four at once); `example --out` never overwrites another file
(`mine.tsv` kept its bytes) and its second run says `unchanged=true`; `snapshot --bin`
notes a skipped symlink by name and records a forged stub under the executable's name,
not the name it prints; two stamps and two sources are each refused naming both binaries;
`--timeout`/`--budget` are distinguished ("the run's 1s budget was spent before this
binary was read") and non-positive bounds are refused; `--dry-run` on `example`,
`snapshot` and `moved` writes nothing (verified with `ls`); `diff` names exactly the
changed, added and removed rows and reads both files before refusing either; `send` with
no bus is honest (`sent=uncertain` plus the bus's refusal), and `moved` builds both
revisions and reads their own help (a scratch tool that printed no help was refused
naming the tool, the revision and the build to repair).

READ 7/10 — the banner and every verb help answer a cold reader fast and truly, but
`report -h`'s effect line is false on `--snapshot`, `moved`'s count fields are defined
nowhere, and the rename trigger is undocumented.

USE 8/10 — every verb ran for real against scratch dirs and manifests with no store, the
refusals carry the next command, and `--dry-run` is trustworthy; the report `--snapshot`
surprise and the `send --draft` message are the stumbles.

urgent=1 next=5
