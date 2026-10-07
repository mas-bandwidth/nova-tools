# nova-update dogfood — opencode-2, 2026-10-06

Cold read as a stranger: only `nova-update -h`, `nova-update help`, every verb's
`-h`, and the tool's own page under `docs/` (`docs/CLI.md`, the `## nova-update`
section), nothing else. The binary was built in the staged checkout at commit
7acb90e18a764f0e728cd5ed701196a34405a824 on a Linux bench, with
`go build -o $J/bin/nova-update ./cmd/nova-update`, never the installed binary; it
answered `nova-update v1.0.1-0.20261007211151-7acb90e18a76 linux/amd64 go1.26.6`.
Every verb was run with its real flags against a scratch store under
`$J/scratch/store` and a temp release root, the refusals too: `example`, `check`,
`status`, `apply` (a plan and a real install), `report` (plain, `--draft`,
`--send`, `--snapshot`, `--store`), `watch --adopt`, `adoption`, `release
cut|build|install|adopt|pull|cycle`, `help` and `version`. Commands are quoted as
typed from the checkout root (`$J/repo`, the binary `../bin/nova-update`); the job
root is written `$J` and a home path inside a quoted line is elided with `…`. The
bench stands as "a Linux bench", and every actor and recipient below is made up.
No code changed, and a finding is recorded here, never fixed.

## Findings

1. `../bin/nova-update status --file ../scratch/store/vtest.tsv`
   Printed:
   ```
   STATUS FAILED checked=2 current=1 stale=0 newer=0 ahead=0 differ=1 unknown=0 pins=1 took=11ms file=../scratch/store/vtest.tsv entries=2 kinds=pin,tool at=2026-10-07T21:17:33Z timeout=5s budget=1m0s max=20
   STATUS DIFFERENT name=pinV kind=pin installed=9.9.9 latest=v9.9.9 path=- source=local:echo\x20mypin\x20v9.9.9 owner=boss
   STATUS EQUAL name=toolV kind=tool installed=9.9.9 latest=9.9.9 path=- source=local:echo\x20mypin\x20v9.9.9 owner=boss
   ```
   I expected one `local:` source to be read the same way whatever the entry's
   kind. As printed the exact same source `local:echo mypin v9.9.9` folds the
   leading `v` for a `tool` (toolV is EQUAL) but keeps it for a `pin` (pinV is
   DIFFERENT against the same installed `9.9.9`); under the same reader
   `local:go version` yields `latest=version` for a pin while a tool reads
   `1.26.6`. The installed side is normalized and the pin's latest side is not.
   Grade: URGENT (wrong result)

2. `../bin/nova-update status --file ../scratch/store/pins3.tsv`
   Printed:
   ```
   STATUS FAILED checked=1 current=0 stale=0 newer=0 ahead=0 differ=0 unknown=1 pins=0 took=6ms file=../scratch/store/pins3.tsv entries=1 kinds=pin at=2026-10-07T21:16:12Z timeout=5s budget=1m0s max=20
   STATUS UNKNOWN name=pinok kind=pin installed=9.9.9 path=- source=local:echo\x20v9.9.9: version line has fewer than two tokens (wrap it in a script that prints the version alone)
   ```
   I expected the source contract `help` describes for `local:<argv>` — a command
   whose output carries the version — to work for a pin. As printed a pin takes
   the second blank-separated token of the source's first line and needs at least
   two of them, which neither `help` nor any verb's `-h` says, and the refusal's
   own remedy ("wrap it in a script that prints the version alone") is a script
   that still prints one token and is refused the same way, so following it does
   not end the refusal.
   Grade: NEXT (unclear help)

3. `../bin/nova-update check --file ../scratch/store/main.tsv 1>/dev/null`
   Printed:
   ```
   CHECK FAILED checked=5 current=1 stale=1 newer=1 ahead=0 differ=1 unknown=1 pins=1 took=340ms file=../scratch/store/main.tsv entries=5 kinds=model,pin,tool at=2026-10-07T21:19:04Z timeout=5s budget=1m0s max=20
   CHECK DIFFERENT name=pinA kind=pin installed=1.26.6 latest=version path=- source=local:go\x20version owner=boss
   CHECK STALE name=stale kind=tool installed=1.0.0 latest=1.2.3 path=- source=local:echo\x20v1.2.3 owner=boss
   ```
   I expected the run's result lines where the same verb writes them when every
   entry is current (`check --file ../scratch/store/versions.tsv` prints
   `CHECK OK ...` on stdout). As printed a run with any finding moves its whole
   human result to stderr and leaves stdout empty — every line above survives
   `1>/dev/null` — so `result=$(../bin/nova-update check --file ...)` is empty for
   exactly the runs a caller captures; only `--json` stays on stdout. A caller
   that greps stderr for `CHECK` also has to know these lines are not stdout.
   Grade: NEXT (friction)

4. `../bin/nova-update check --file ../scratch/store/maxgroups.tsv --max 1`
   Printed:
   ```
   CHECK FAILED checked=5 current=0 stale=2 newer=1 ahead=0 differ=0 unknown=2 pins=0 took=219ms file=../scratch/store/maxgroups.tsv entries=5 kinds=model,tool at=2026-10-07T21:16:19Z timeout=5s budget=1m0s max=1
   CHECK STALE name=t1 kind=tool installed=1.0.0 latest=1.2.3 path=- source=local:echo\x20v1.2.3 owner=boss
   CHECK NEWER name=t3 kind=tool installed=2.0.0 latest=1.0.0 path=- source=local:echo\x20v1.0.0 owner=boss
   ```
   I expected the ceiling its flag help promises, `--max <int> lines listed per
   kind before one MORE line stands for the rest`, to group by the manifest's
   `kind` — the word `--kind` selects by. As printed the ceiling groups by the
   finding's status word: with three tools (two stale, one newer) the two
   different statuses each get a line and the third tool is hidden behind
   `MORE kind=stale shown=1 total=2`, while the two unknown models collapse into
   `MORE kind=unknown shown=1 total=2`.
   Grade: NEXT (unclear help)

5. `../bin/nova-update -h | grep -n 'release <'`
   Printed:
   ```
   23:  nova-update release <cut|build|install|adopt|pull> ...
   (one line printed)
   ```
   I expected the release synopsis to name every release verb. As printed the
   top-level help lists five and omits `cycle`, while `nova-update help release`
   and the tool's page under `docs/` each print six verbs including `cycle`. A
   stranger who reads only `-h` never learns the one release verb that installs a
   whole bench.
   Grade: NEXT (unclear help)

6. `../bin/nova-update report --file ../scratch/store/versions.tsv --draft --as boss --to carol,dave`
   Printed:
   ```
   From: boss
   To: carol,dave
   Subject: versions on - at 2026-10-07T21:16:23Z
   ```
   I expected a subject that names the machine or leaves the clause out. As
   printed a report with no `--host` carries a bare `-` where the host goes, so
   the recipient reads `versions on -` and cannot tell whether the label was
   omitted or is the literal `-`; the same run with `--host bench-x` prints
   `versions on bench-x`.
   Grade: NEXT (friction)

7. `../bin/nova-update report --file ../scratch/store/versions.tsv --send --as boss --to carol`
   Printed:
   ```
   REPORT FAILED checked=1 known=1 unknown=0 changed=- sent=uncertain took=22ms file=../scratch/store/versions.tsv host=- as=boss entries=1 kinds=tool at=2026-10-07T21:16:23Z timeout=5s budget=1m0s max=20 snapshot=-
   REPORT TOOL name=go kind=tool version=1.26.6 raw=go\x20version\x20go1.26.6\x20linux/amd64 path=…/go/bin/go
   REPORT NOTE send not confirmed: exit 2; the bus said: SEND REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to g... (retry --send with the same --snapshot)
   ```
   I expected the bus's whole refusal or a pointer to a file that holds it. As
   printed the note cuts the sentence mid-word at `refusing to g...`, so the one
   part that says what the bus will not do — and whether a second remedy exists
   beyond setting `NOVA_BUS_REDIS` — is unreadable; `--json` holds the same
   truncated note, so no rendering keeps the full text. The remedy also names
   `--snapshot`, which this run never used.
   Grade: NEXT (friction)

8. `../bin/nova-update report --store localhost:1 --timeout 2s`
   Printed:
   ```
   redis: 2026/10/07 21:16:26 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   redis: 2026/10/07 21:16:27 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   redis: 2026/10/07 21:16:27 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   ```
   I expected one result line from the tool, and its refusal below it. As printed
   four retry lines from the Redis client land on stderr above the tool's own
   `REPORT REFUSED: fleet beats at localhost:1 ...` line, in the client library's
   format and with no flag to quiet them; a caller that greps stderr for `REPORT`
   must first skip the library's noise.
   Grade: NEXT (friction)

9. `../bin/nova-update release pull --version v0.0.0-dogfood --out ../scratch/rel --changelog ../scratch/store/CHANGELOG.pull.md --reason "dogfood probe withdraw"`
   Printed:
   ```
   release: deleting $J/scratch/rel/v0.0.0-dogfood/linux-amd64/nova-bus
   release: deleting $J/scratch/rel/v0.0.0-dogfood/linux-amd64/nova-cairn
   release: deleting $J/scratch/rel/v0.0.0-dogfood/linux-amd64/nova-card
   ```
   I expected the changelog to be read, and its section found or refused, before
   anything is deleted, so a pull either does both halves or refuses first. As
   printed a pull whose changelog has no `## v0.0.0-dogfood` section deletes all
   26 artifacts (the release directory is gone afterwards) and only then fails
   the marking — `PULL FAILED ... (the artifacts are deleted; mark the section by
   hand)`. The remedy is the only recovery, because a retry can no longer read
   the release's `SHA256SUMS` to learn the file names.
   Grade: NEXT (friction)

10. `../bin/nova-update adoption --file ../scratch/store/ledger.tsv --as nobody`
   Printed:
   ```
   ADOPTION OK entries=0 friends=0 file=../scratch/store/ledger.tsv max=20
   (one line printed)
   ```
   I expected a note that `nobody` is not a friend the ledger names, or a
   non-zero exit, as the tool does for a name absent from a manifest
   (`APPLY REFUSED: name nosuch absent from ...`). As printed a misspelled or
   unknown friend yields a success with no rows, indistinguishable from a friend
   who has adopted nothing.
   Grade: NEXT (friction)

11. `../bin/nova-update adoption --file ../scratch/store/ledgerbad.tsv`
   Printed:
   ```
   ADOPTION REFUSED: ../scratch/store/ledgerbad.tsv: line 2: unknown state pending (use evaluated,useful-now,tried,adopted,declined,deferred,unknown,equivalent); run: nova-update adoption -h
   (one line printed)
   ```
   I expected `adoption -h` to list the state vocabulary the verb enforces — it
   documents the five columns but none of the states — so the remedy it prints
   answers the question. As printed the only place the eight states appear is the
   refusal itself; the help the refusal points at does not name them.
   Grade: NEXT (unclear help)

## What held

`example` writes the one-tool manifest and never overwrites a file that is not
the example (`EXAMPLE REFUSED: ... exists and is not the example manifest`), and a
repeat of its own write is `unchanged=true`; `check` and `status` read `local:`
and `ollama:` sources and reported EQUAL, STALE, NEWER, DIFFERENT and UNKNOWN
with `--kind`, `--max`, `--timeout` and `--budget` enforced (`--max -1`,
`--budget 5`, `--timeout nope` and `--kind bogus` each refused with what the flag
wants); `apply --dry-run` printed the plan and started nothing, and a real
`apply` ran the entry's argv and read the version again to the target
(`APPLY OK ... from=1.0.0 to=2.0.0`), while `--version` on an argv without
`{version}` was refused and an unknown name named every entry the file holds;
`report` read only installed identities, its `--snapshot` wrote a state file and
a repeat answered `changed=no`, `--kind` filtered the rows, and its `--store`
refusal named the dial failure; `watch --adopt` ran both a passing and a refused
check and escalated the refused check to its owner; `adoption` listed and
filtered choices and refused an unknown state while naming all eight; `release
build` compiled 24 tools and verified all 24 (`RELEASE BUILT ... tools=24
verified=24`), `release install` verified and installed 24, `release adopt
--dry-run` probed a machine and refused it with the reason and the remedy,
`release pull --dry-run` printed what it would delete and deleted nothing, and
`release cut` refused at the forge read with the remedy to check `--repo` and
`--from`; `release cycle` could not be run past its argument check — no
`ansible-playbook` is installed on the bench, so `--ansible` was missing and it
refused rather than guess; every verb answered `-h` at exit 0, `help` printed the
whole banner, and `version` printed the one version line; the manifest reader was
the best part, refusing a file with a bad kind, a duplicate name, an empty field
and a bad source all at once, each named with its line.

READ 6/10 — the manifest, every verb's flags and the refusal grammar are
documented in depth and a refusal usually names a remedy, but the release
synopsis in `-h` omits `cycle`, the `local:` source contract for a `pin` and the
`adoption` state vocabulary are missing, `--max` names the wrong unit, and the
`on -` subject and the `\x20` escaping of values with blanks are left for the
reader to work out.

USE 6/10 — every verb ran for real and did what its line said, the refusals were
clear and non-destructive, and a build/install/plan cycle worked end to end, but
a `pin` reads its latest source differently from a `tool`, a failing `check`
writes its human result to stderr and leaves stdout empty, and a `pull` deletes
the release before it finds out it cannot mark the changelog.

urgent=1 next=10
