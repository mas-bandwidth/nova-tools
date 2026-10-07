# nova-update dogfood — opencode-2 (zhi), 2026-10-06

Read as a stranger: only `nova-update -h`, `nova-update help`,
`nova-update <verb> -h` and its page under `docs/` (`docs/CLI.md`, the
`## nova-update` section), nothing else. Built from the staged checkout at
174eac229d10f84680b90dea2976b375dbd2c7ad and used as
`nova-update v1.0.1-0.20261007155708-174eac229d10 linux/amd64 go1.26.6`.
Every verb was used with its real flags against a scratch store under
`$J/scratch/store` and a temp dir; the refusals too. Commands are quoted as typed
from the checkout root (`$J/repo`, the binary `../bin/nova-update`), with the job
root shortened to `$J` in the quoted output; every `$J` below stands for that one
directory. A home path inside a quoted line is elided with `…`, and the bench
stands as `<bench>`, so this record names no host or person. A `release build`
for linux-amd64, an `install`, an `adopt --dry-run`, a `pull` and a `cycle
--dry-run` were run for real; no code changed, and a finding is recorded here,
never fixed.

## Findings

1. `../bin/nova-update status --file ../scratch/store/pins.tsv`
   and `../bin/nova-update status --file ../scratch/store/pins2.tsv`
   and `../bin/nova-update status --file ../scratch/store/pins3.tsv`

   ```
   STATUS FAILED checked=3 current=0 stale=0 newer=0 ahead=0 differ=2 unknown=1 pins=2 took=13ms file=../scratch/store/pins.tsv entries=3 kinds=pin at=2026-10-07T16:02:33Z timeout=5s budget=1m0s max=20
   STATUS DIFFERENT name=pinA kind=pin installed=1.26.6 latest=version path=- source=local:go\x20version owner=alice
   STATUS UNKNOWN name=pinB kind=pin installed=1.26.6 path=- source=local:echo\x20go1.26.6: version line has fewer than two tokens (wrap it in a script that prints the version alone)
   ```
   and
   ```
   STATUS FAILED checked=3 current=1 stale=0 newer=0 ahead=0 differ=2 unknown=0 pins=2 took=18ms file=../scratch/store/pins3.tsv entries=3 kinds=pin at=2026-10-07T16:07:35Z timeout=5s budget=1m0s max=20
   STATUS EQUAL name=pinG kind=pin installed=9.9.9 latest=9.9.9 path=- source=local:echo\x20mypin\x209.9.9 owner=alice
   STATUS DIFFERENT name=pinH kind=pin installed=9.9.9 latest=v9.9.9 path=- source=local:echo\x20mypin\x20v9.9.9 owner=alice
   ```

   Expected a `pin` and a `tool` to read the same `local:` source the same way,
   and a `pin` whose installed version is the version its source prints to be
   EQUAL. As printed the same `local:go version` that makes a `tool` current makes
   a `pin` DIFFERENT with `latest=version` — the second whitespace token, not the
   version — while `local:echo mypin v9.9.9` gives `latest=v9.9.9` against
   `installed=9.9.9`, so the source that prints the same `v`-prefixed version the
   entry holds is read as a different version. The leading `v` is folded out of
   the installed side but not out of the entry's latest side.
   Grade: URGENT.

2. `../bin/nova-update status --file ../scratch/store/main2.tsv`
   (`pinok` holds `v9.9.9`; its latest is `local:echo v9.9.9`)

   ```
   STATUS FAILED checked=6 current=2 stale=1 newer=1 ahead=0 differ=0 unknown=2 pins=0 took=22ms file=../scratch/store/main2.tsv entries=6 kinds=pin,tool at=2026-10-07T16:02:26Z timeout=3s budget=1m0s max=20
   STATUS UNKNOWN name=pinok kind=pin installed=9.9.9 path=- source=local:echo\x20v9.9.9: version line has fewer than two tokens (wrap it in a script that prints the version alone)
   STATUS EQUAL name=go kind=tool installed=1.26.6 latest=1.26.6 path=…/go/bin/go source=local:go\x20version owner=alice
   ```

   Expected the same `local:` source a `tool` accepts — a command whose first
   line carries the version, `local:echo v1.2.3` — to work for a `pin`, or the
   help to say a pin's source must print two tokens. As printed a `pin`'s source
   must print at least two tokens and its version is the second, which neither
   `pin -h` nor the manifest section of `help` says; and the refusal's own remedy,
   "wrap it in a script that prints the version alone", still prints one token and
   so still fails. A reader is told to do the thing that does not work.
   Grade: NEXT.

3. `../bin/nova-update check --file ../scratch/store/main2.tsv >$J/scratch/run/chk2.out 2>$J/scratch/run/chk2.err`

   ```
   $ wc -l <$J/scratch/run/chk2.out
   0
   $ cat $J/scratch/run/chk2.err
   CHECK FAILED checked=6 current=2 stale=1 newer=1 ahead=0 differ=0 unknown=2 pins=0 took=20ms file=../scratch/store/main2.tsv entries=6 kinds=pin,tool at=2026-10-07T16:06:04Z timeout=5s budget=1m0s max=20
   CHECK UNKNOWN name=pinok kind=pin installed=9.9.9 path=- source=local:echo\x20v9.9.9: version line has fewer than two tokens (wrap it in a script that prints the version alone)
   CHECK STALE name=stale kind=tool installed=1.0.0 latest=1.2.3 path=- source=local:echo\x20v1.2.3 owner=alice
   ```

   Expected the result lines the help calls "the result" — `A result's first line
   is the verb, its status word (OK, FAIL, REFUSED) and the run's counts` — to sit
   on stdout, where the same run's lines sit when every entry is current
   (`check --file ../scratch/store/versions.tsv` writes `CHECK OK ...` to stdout).
   As printed a run with any finding moves its whole human result to stderr and
   leaves stdout empty, so `findings=$(../bin/nova-update check --file ...)` gets
   nothing for exactly the runs a caller captures; only `--json` stays on stdout.
   Grade: NEXT.

4. `../bin/nova-update check --file ../scratch/store/maxgroups.tsv --max 1`
   (three `tool` entries — two stale, one newer — and two `model` entries, both
   unknown)

   ```
   CHECK FAILED checked=5 current=0 stale=2 newer=1 ahead=0 differ=0 unknown=2 pins=0 took=10ms file=../scratch/store/maxgroups.tsv entries=5 kinds=model,tool at=2026-10-07T16:09:14Z timeout=5s budget=1m0s max=1
   CHECK STALE name=t1 kind=tool installed=1.0.0 latest=1.2.3 path=- source=local:echo\x20v1.2.3 owner=alice
   CHECK NEWER name=t3 kind=tool installed=2.0.0 latest=1.0.0 path=- source=local:echo\x20v1.0.0 owner=alice
   ```
   and then
   ```
   CHECK MORE kind=stale shown=1 total=2 --max <n> raises the ceiling, --max 0 lists all
   CHECK MORE kind=unknown shown=1 total=2 --max <n> raises the ceiling, --max 0 lists all
   ```

   Expected the ceiling its help promises, `--max <int> lines listed per kind
   before one MORE line stands for the rest`, to group by the manifest's `kind`
   (`model`, `tool`) — the word `--kind` selects by — so under `--max 1` the three
   tools are one group with one line and one `MORE kind=tool total=3`. As printed
   the ceiling groups by the finding's status word: two tools of different
   statuses each get a line and the third tool is hidden behind `kind=stale`,
   while the two unknown models collapse into one `kind=unknown` group, so
   `--max` caps lines per status, not per kind.
   Grade: NEXT.

5. `../bin/nova-update -h`

   ```
   nova-update -h
     nova-update release <cut|build|install|adopt|pull> ...
       nova-tools' own release pipeline: nova-update help release prints its usage lines
   ```

   Expected the release synopsis to name every verb the tool has. As printed the
   top-level help lists five and omits `cycle`, while `release -h`, `help release`,
   the `release` refusal (`the release verbs are cut, build, install, adopt, pull,
   cycle`) and the card's own `docs/CLI.md` page all say six. A stranger who reads
   only `-h` never learns the one release verb that installs a whole bench.
   Grade: NEXT.

6. `../bin/nova-update report --file ../scratch/store/versions.tsv --draft --as zhi --to alice,bob`

   ```
   From: zhi
   To: alice,bob
   Subject: versions on - at 2026-10-07T16:03:03Z
   ```

   Expected a subject that names the machine or leaves the clause out. As printed
   a report with no `--host` carries a bare `-` where the host goes, so the
   recipient reads `versions on -` and cannot tell whether the label was omitted
   or is the literal `-`; `--host bench-x` prints `versions on bench-x`.
   Grade: NEXT.

7. `../bin/nova-update report --file ../scratch/store/versions.tsv --send --as zhi --to alice`

   ```
   REPORT FAILED checked=1 known=1 unknown=0 changed=- sent=uncertain took=42ms file=../scratch/store/versions.tsv host=- as=zhi entries=1 kinds=tool at=2026-10-07T16:03:03Z timeout=5s budget=1m0s max=20 snapshot=-
   REPORT TOOL name=go kind=tool version=1.26.6 raw=go\x20version\x20go1.26.6\x20linux/amd64 path=…/go/bin/go
   REPORT NOTE send not confirmed: exit 2; the bus said: SEND REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to g... (retry --send with the same --snapshot)
   ```

   Expected the bus's whole refusal or a pointer to a file that holds it. As
   printed the note cuts the sentence mid-word at `refusing to g...`, so the one
   part that says what the bus will not do — and whether there is a second remedy
   beyond setting `NOVA_BUS_REDIS` — is unreadable, and `--send` has no `--json`
   path that keeps the full text.
   Grade: NEXT.

8. `../bin/nova-update report --store localhost:1 --timeout 2s 2>$J/scratch/run/store.err`

   ```
   redis: 2026/10/07 16:05:59 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   redis: 2026/10/07 16:05:59 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   redis: 2026/10/07 16:05:59 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   ```

   Expected one result line from the tool, and its refusal below it. As printed
   four retry lines from the Redis client land on stderr above the tool's own
   `REPORT REFUSED: fleet beats at localhost:1: ...` line, in the client library's
   format, with no way to quiet them; a caller that greps stderr for `REPORT` must
   first skip the library's noise.
   Grade: NEXT.

9. `../bin/nova-update release pull --version v0.0.0-dogfood --out ../scratch/rel --changelog ../scratch/store/CHANGELOG.copy.md --reason "dogfood probe withdraw"`

   ```
   release: deleting $J/scratch/rel/v0.0.0-dogfood/linux-amd64/nova-bus
   release: deleting $J/scratch/rel/v0.0.0-dogfood/linux-amd64/nova-cairn
   release: deleting $J/scratch/rel/v0.0.0-dogfood/linux-amd64/nova-card
   ```
   and, after all 26 files, the run ends
   ```
   RELEASE PULLED LOCAL version=v0.0.0-dogfood files=26 out=../scratch/rel/v0.0.0-dogfood/linux-amd64
   PULL FAILED version=v0.0.0-dogfood changelog=../scratch/store/CHANGELOG.copy.md: the changelog has no `## v0.0.0-dogfood` section to mark as pulled (name the changelog that carries this release's section, or add the section first) (the artifacts are deleted; mark the section by hand)
   ```

   Expected the changelog to be read (and its section found or written) before
   anything is deleted, so a pull either does both halves or refuses first. As
   printed a pull whose changelog has no section deletes all 26 artifacts and then
   fails the marking, leaving the release gone and the history unmarked; the note's
   remedy, `mark the section by hand`, is the only recovery, because a retry cannot
   read the release's `SHA256SUMS` to learn the file names again.
   Grade: NEXT.

10. `../bin/nova-update adoption --file ../scratch/store/ledger2.tsv --as nobody`

    ```
    ADOPTION OK entries=0 friends=0 file=../scratch/store/ledger2.tsv max=20
    ```

    Expected a note that `nobody` is not a friend the ledger names, or a non-zero
    exit, as the tool does for a name absent from a manifest (`APPLY REFUSED: name
    nosuch absent from ...; its 3 entries are ...`). As printed a misspelled or
    unknown friend yields a success with no rows, indistinguishable from a friend
    who has adopted nothing.
    Grade: NEXT.

11. `../bin/nova-update adoption --file ../scratch/store/ledger.tsv`
    (line 4's state is `pending`)

    ```
    ADOPTION REFUSED: ../scratch/store/ledger.tsv: line 4: unknown state pending (use evaluated,useful-now,tried,adopted,declined,deferred,unknown,equivalent); run: nova-update adoption -h
    ```

    Expected `adoption -h` to list the state vocabulary it enforces — it documents
    the five columns but none of the states — so the remedy it prints (`run:
    nova-update adoption -h`) answers the question. As printed the only place the
    eight states appear is the refusal itself; the help the refusal points at does
    not name them.
    Grade: NEXT.

Right: `example` writes and never overwrites (`EXAMPLE REFUSED: ... exists and
is not the example manifest`), and its repeated run is a no-op
(`unchanged=true`); `check`/`status` read `github:`, `npm:`, `brew:`, `ollama:`
and `local:` sources and report EQUAL, STALE, NEWER, AHEAD, DIFFERENT and
UNKNOWN, with `--kind`, `--max`, `--timeout` and `--budget` enforced (`--max -1`,
`--budget 5`, `--timeout nope`, `--kind bogus` each refused); `apply` prints a
plan and starts nothing on `--dry-run`, substitutes `{version}` and refuses
`--version` on an argv that does not hold it; `report` reads only installed
identities, writes a snapshot that quiets an unchanged repeat, refuses `--draft`
without `--as`/`--to` and truncates a send that was not confirmed; `watch` runs a
checks file, escalates a refused check to its owner and takes no `--json`;
`adoption` filters by friend and caps its rows; and the release verbs each refused
missing flags in one line naming every one, refused an unsupported platform before
the first compile, built 24 tools and verified 24, installed them, adopted none
where `ssh <bench>` was refused, and reported a pull it could not mark. The
manifest reader is the best part: `broken1.tsv` (bad kind, empty field, bad
source, duplicate name) is refused with all four problems, each with its line.
No code changed.

READ 6/10: the manifest format and every verb's flags, effect and exit codes are
documented in depth and the refusals mostly name a remedy, but the top-level
release synopsis omits `cycle`, a `pin`'s `local:` source contract and the
`adoption` state vocabulary are missing, `--max` names the wrong unit, and the
`on -` subject and the `\x20` escaping of values with blanks are left for the
reader to work out.

USE 6/10: every verb ran for real and did what its line said — the example never
overwrote, a stale manifest named every problem at once, a build/install/pull
cycle worked end to end, and refusals were clear and non-destructive — but a
`pin` reads its latest source wrongly, a failing run's result lands on stderr with
stdout empty, and a pull deletes the release before it finds out it cannot mark
the changelog.

urgent=1 next=10
