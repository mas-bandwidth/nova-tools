# nova-update dogfood — opencode-2, 2026-10-06

Read cold as a stranger on a Linux bench: only `nova-update -h`, `nova-update help`,
`nova-update help release`, every verb's `-h`, and the tool's page under `docs/`
(`docs/SPEC-UPDATE.md`), nothing else. The binary was built in the staged checkout at
`d746414234c081ea41b65ddde0aeadcca6c5ffe4` (the checkout's staged tip) with
`go build -o $J/bin/nova-update ./cmd/nova-update` (never the installed binary), where
`$J` is the job root and the commands below are typed with the binary as `nova-update`
from `$J/repo`, files under `$J/scratch`. `nova-update version` printed:

```
nova-update v1.0.1-0.20261010033023-d746414234c0 linux/amd64 go1.26.6
```

and `go version -m $J/bin/nova-update | head -5` printed:

```
$J/bin/nova-update: go1.26.6
	path	github.com/mas-bandwidth/nova-tools/cmd/nova-update
	mod	github.com/mas-bandwidth/nova-tools	v1.0.1-0.20261010033023-d746414234c0	
	dep	github.com/cespare/xxhash/v2	v2.3.0	h1:UL815xU9SqsFlibzuggzjXhog7bL6oX9BbNZnL2UFvs=
	dep	github.com/jackc/pgpassfile	v1.0.0	h1:/6Hmqy13Ss2zCq62VdNG8tM1wchn8zjSGOBJ6icpsIM=
```

The `mod` line's pseudo-version names `d746414234c0`, the staged commit, so this is the
staged checkout's own build and no other binary's output is written here. Every verb ran
with its real flags against a scratch store under `$J/scratch/store` and a temp release
root, the refusals too, over about twenty minutes. Every actor and recipient below is a
made-up placeholder (`boss`, `carol`, `nobody`), and no real identity is written. No code
changed, and a finding is recorded here, never fixed.

## Findings

1. `nova-update status --file ../scratch/store/pins.tsv 2>&1`
   Printed:
   ```
   STATUS FAILED checked=2 current=0 stale=0 newer=0 ahead=0 differ=1 unknown=1 pins=1 took=20ms file=../scratch/store/pins.tsv entries=2 kinds=pin at=2026-10-10T03:37:50Z timeout=5s budget=1m0s max=20
   STATUS UNKNOWN name=pinok kind=pin installed=v9.9.9 path=/usr/bin/echo source=local:echo\x20v9.9.9: version line has fewer than two tokens (wrap it in a script that prints the version alone)
   STATUS DIFFERENT name=pinbad kind=pin installed=v9.9.9 latest=v8.8.8 path=/usr/bin/echo source=local:echo\x20mypin\x20v8.8.8 owner=boss
   ```
   I expected the remedy to end the refusal. SPEC-UPDATE rule 15 reads both sides of a
   `pin` as the second token of the first line, so `local:echo v9.9.9` cannot satisfy the
   read, and wrapping the command in a script that prints the version alone still prints
   one token and is refused the same way; nothing the refusal says gets a `pin` past it.
   Grade: URGENT (a refusal with no remedy)

2. `nova-update watch --adopt ../scratch/store/checks.tsv 2>&1`
   Printed:
   ```
   ADOPT OK check=passing detail=ok
   ADOPT REFUSED check=failing detail=not_found (install nosuchcmd or supply its executable path)
   ADOPT ESCALATE check=failing to=boss: duty files an issue and a fix card (not_found)
   ```
   I expected one pass on one stream, whole. SPEC-UPDATE's grammar says a run that is OK
   prints on stdout and one that is not on stderr, whole, and that watch's `ADOPT` lines
   keep their order. As printed the passing check is on stdout while the refusal, the
   escalation and the ending `ADOPT DONE` are on stderr, so `watch ... > log` records a
   pass with no failure and no ending, and `watch ... 2> log` records failures with no
   passing check.
   Grade: URGENT (a wrong result: a captured stream reads as a clean pass)

3. `nova-update check --file ../scratch/store/maxg.tsv 2>&1`
   Printed:
   ```
   CHECK FAILED checked=3 current=0 stale=2 newer=1 ahead=0 differ=0 unknown=0 pins=0 took=22ms file=../scratch/store/maxg.tsv entries=3 kinds=tool at=2026-10-10T03:37:50Z timeout=5s budget=1m0s max=20
   CHECK STALE name=t1 kind=tool installed=1.0.0 latest=1.2.3 path=/usr/bin/echo source=local:echo\x20v1.2.3 owner=boss
   CHECK STALE name=t2 kind=tool installed=1.0.0 latest=1.2.3 path=/usr/bin/echo source=local:echo\x20v1.2.3 owner=boss
   ```
   I expected the same verb to write its human result in the same place whether or not it
   is OK. As printed a run with any finding moves its whole result to stderr and leaves
   stdout empty (`1>/dev/null` lost every line above; the stdout bytes were 0), so
   `result=$(nova-update check ...)` is empty for exactly the runs a caller captures.
   Grade: NEXT (friction)

4. `nova-update status --file ../scratch/store/maxg.tsv 2>&1`
   Printed:
   ```
   STATUS FAILED checked=3 current=0 stale=2 newer=1 ahead=0 differ=0 unknown=0 pins=0 took=54ms file=../scratch/store/maxg.tsv entries=3 kinds=tool at=2026-10-10T03:37:50Z timeout=5s budget=1m0s max=20
   STATUS STALE name=t1 kind=tool installed=1.0.0 latest=1.2.3 path=/usr/bin/echo source=local:echo\x20v1.2.3 owner=boss
   STATUS STALE name=t2 kind=tool installed=1.0.0 latest=1.2.3 path=/usr/bin/echo source=local:echo\x20v1.2.3 owner=boss
   ```
   I expected a value a person reads to stay readable, or a quoted form. As printed the
   human line escapes the blank in `source=local:echo\x20v1.2.3`, while `--json` for the
   same run prints `"source":"local:echo v1.2.3"` plain, so the two renderings of one
   value carry two spellings.
   Grade: NEXT (friction)

5. `nova-update check --file ../scratch/store/maxg.tsv --max 1 2>&1`
   Printed:
   ```
   CHECK FAILED checked=3 current=0 stale=2 newer=1 ahead=0 differ=0 unknown=0 pins=0 took=39ms file=../scratch/store/maxg.tsv entries=3 kinds=tool at=2026-10-10T03:37:50Z timeout=5s budget=1m0s max=1
   CHECK STALE name=t1 kind=tool installed=1.0.0 latest=1.2.3 path=/usr/bin/echo source=local:echo\x20v1.2.3 owner=boss
   CHECK NEWER name=t3 kind=tool installed=2.0.0 latest=1.0.0 path=/usr/bin/echo source=local:echo\x20v1.0.0 owner=boss
   ```
   I expected the ceiling its flag help promises, `--max <int> lines listed per kind
   before one MORE line stands for the rest`, to group by the manifest's `kind`. As
   printed the ceiling groups by the verdict word: with three tools (two stale, one
   newer) the third tool disappears behind `MORE kind=stale shown=1 total=2` while the
   newer one keeps its own line.
   Grade: NEXT (unclear help)

6. `nova-update report --store 127.0.0.1:1 --timeout 2s 2>&1`
   Printed:
   ```
   redis: 2026/10/10 03:37:50 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   redis: 2026/10/10 03:37:51 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   redis: 2026/10/10 03:37:51 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   ```
   I expected one `REPORT REFUSED` line naming the store and the cause, in the tool's own
   voice. As printed four lines from the Redis client, each carrying a source file and
   line, land above the tool's own refusal, so a caller that greps stderr for `REPORT`
   must first skip the library's noise.
   Grade: NEXT (friction)

7. `nova-update report --file ../scratch/store/apply.tsv --draft --as boss --to carol 2>&1`
   Printed:
   ```
   From: boss
   To: carol
   Subject: versions on - at 2026-10-10T03:37:52Z
   ```
   I expected a subject that names the machine or drops the clause. As printed a report
   with no `--host` carries a bare `-` where the host goes, so the recipient reads
   `versions on -`; the same run with `--host bench-x` prints `versions on bench-x`.
   Grade: NEXT (friction)

8. `nova-update -h | grep -n 'release <'`
   Printed:
   ```
   23:  nova-update release <cut|build|install|adopt|pull> ...
   (one line printed)
   ```
   I expected the top-level synopsis to name every release verb. As printed it lists five
   and omits `cycle`, while `nova-update help release` and a bare `nova-update release`
   each print six verbs including `cycle`.
   Grade: NEXT (unclear help)

9. `nova-update adoption --file ../scratch/store/ledger.tsv --as nobody 2>&1`
   Printed:
   ```
   ADOPTION OK entries=0 friends=0 file=../scratch/store/ledger.tsv max=20
   (one line printed)
   ```
   I expected a note that `nobody` is not a friend the ledger names, or a non-zero exit,
   as `apply` does for a name absent from a manifest (`name nosuch absent from ...`). As
   printed a misspelled friend yields a success with no rows, indistinguishable from a
   friend who has adopted nothing.
   Grade: NEXT (friction)

10. `nova-update adoption --file ../scratch/store/ledgerbad.tsv 2>&1`
    Printed:
    ```
    ADOPTION REFUSED: ../scratch/store/ledgerbad.tsv: line 2: unknown state pending (use evaluated,useful-now,tried,adopted,declined,deferred,unknown,equivalent); run: nova-update adoption -h
    (one line printed)
    ```
    I expected `adoption -h`, the remedy the refusal points at, to list the state
    vocabulary the verb enforces. As printed it documents the five columns and none of
    the eight states, so the refusal is the only place the eight appear.
    Grade: NEXT (unclear help)

## What held

`example` wrote the one-tool manifest, a repeat of its own write answered `unchanged=true`,
and it never overwrote another file. `check` and `status` read `local:` sources and
reported EQUAL, STALE, NEWER, DIFFERENT and UNKNOWN with `--kind`, `--max`, `--timeout`
and `--budget` enforced; a manifest with a bad kind, a four-field line and a double space
was refused once with every problem and its line; a missing `--file`, an unreadable file,
`--max -1`, an unknown `--kind` and an unknown flag each refused in one line with a
remedy. `apply --dry-run` printed the plan and started nothing; a real `apply` ran the
entry's argv and read the version again (`APPLY OK name=vv from=1.0.0 to=2.0.0`), a second
apply was idempotent (`from=2.0.0 to=2.0.0`); no name, an absent name, a model (refused
with its owner's own pull) and `none` each refused. `report` read only the installed side;
`--json` is one value with the lines; `--snapshot` wrote a state file, the first run said
`changed=yes` and a repeat said `changed=no`; `--draft` printed the note and sent nothing;
`--send` with no bus failed honestly (`sent=uncertain`); a missing `--to` refused.
`watch --adopt` ran a passing and a refused check and escalated the refused one to its
owner. `adoption` listed its choices, `--json` matched the lines, and an unknown state was
refused with all eight named. `release build` compiled and verified 24 tools; `release
install` verified and installed 24, a repeat skipped 24; `release adopt --dry-run`
inferred the version, verified the artifacts and refused an unreachable machine with the
reason and the remedy; `release pull --dry-run` printed the 26 files it would delete and
deleted nothing; `release cut` refused at the forge read naming `--repo` and `--from`;
`release cycle --dry-run` refused on the missing receipts directory; each release verb's
no-flags refusal named every missing flag in one line, and each `release <verb> -h`
printed its usage at exit 0. `version` printed the one version line, and `help`, `-h` and
`help release` all exited 0.

Could not run: `report --store`'s success path and the `watch --as/--to` and `report
--send` success paths need a reachable bus store, and the rules forbid starting one;
`release adopt` and `release cycle` were run only to their `--dry-run` and their refusals
because a real one needs ssh trust and an inventory this bench does not have.

READ 6/10 — the banner, every verb's flags and the refusal grammar are documented in
depth and a refusal usually names a remedy, but a `pin`'s second-token read is not legible
from the help and its remedy does not work, the eight `adoption` states appear only in the
refusal, `--max` names the wrong unit, and the release synopsis omits `cycle`.

USE 7/10 — every verb ran for real from one manifest and a scratch dir with no store, a
real `apply` and its dry run did exactly what their help says, and every refusal was one
line with a next step; the split `watch` streams, the escaped text values and the client
lines on `report --store` are the places the output stops being one readable value.

urgent=2 next=8
