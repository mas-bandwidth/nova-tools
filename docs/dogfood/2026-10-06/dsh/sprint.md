# nova-sprint dogfood, 2026-10-06 (dsh)

Tool: nova-sprint. Build: `nova-sprint v1.0.1-0.20261006204015-cb5fb8d4c329 linux/amd64 go1.26.6`,
from the staged checkout at `cb5fb8d4c3290f710d22a21a86aa8d229e4db905` (BASE
`sprint/mechanical-2026-10-02`). Read as a stranger from the tool's own doors only:
`nova-sprint`, `nova-sprint -h`, `nova-sprint help`, `nova-sprint help <verb>`,
`nova-sprint <verb> -h`, and its page under docs/ (`docs/CLI.md`, "nova-sprint", and
`docs/SPEC-SPRINT.md`). Every store call was the in-memory twin, `--redis mem:<file>` /
`NOVA_SPRINT_REDIS=mem:<file>`; no Redis, no server, no live store. Every top-level verb
and every group verb in `nova-sprint help` was run at least once against a scratch twin or
a temp dir, refusals included; the whole card flow ran end to end both without git
(`finish` without `--head`, then `merge`) and for real with git (`finish --head`, then
`land` against a local bare origin). No code was changed. The one setup the pages do not
name, the seat's push proof, was driven by hand where it was needed: `seat push --harness
claude --target <dir>`, `seat push --sent <nonce>`, `seat pong <nonce>`.

## Findings

1. The first-run flow the tool's own page promises is refused at its second command by the
   seat's push proof; nothing is written and the flow cannot be run from the page.

   **Command:**
   ```
   export NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=boss
   nova-sprint init --readers reader-a,reader-b --members m1
   nova-sprint add --stream s1 --count 1 --one
   ```

   **Printed** (the first line is `INIT OK ...`; this is the `add`, on stderr, exit 2):
   ```
   nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
   ```

   **Expected:** `ADD OK stream=s1 cards=1 moved=1 ...`, and the flow to run as printed.
   `docs/CLI.md`'s "First run" opens "Try one card's whole flow with no Redis or git" and
   lists `init`, `add`, `start`, `tick`, `take`, `finish`, `read`, `merge`; the banner's
   "trying it without a Redis" lists the same. Neither names the seat push proof, and
   `start` is refused the same way, so the no-Redis first run stops at its first writing
   verb. The refusal's own remedy needs a harness, a session directory and an installed
   loop, none of which the page provides. Grade: URGENT.

2. `where --json --cards` calls every primary merging, including ready and waiting cards,
   while the same frame's work-table cell says none is merging.

   **Command:**
   ```
   nova-sprint where
   nova-sprint where --json --cards
   ```
   (twin: three ready primaries, `s1-1`..`s1-3`, and one waiting sentinel, `v1`)

   **Printed** (the text frame's work row, then the JSON frame; the JSON is one line, so
   the two fields it carries are shown):
   ```
   s1   |       1 |     3 |       0 |      0 |       0 |      0 |    - | -
   "merging":[{"head":"","id":"s1-1","stream":"s1"},{"head":"","id":"s1-2","stream":"s1"},{"head":"","id":"s1-3","stream":"s1"},{"head":"","id":"v1","stream":"s1"}]
   "tables":{"work":{"s1":{"cost":"-","landed":"0","merging":"0","per_landed":"-","ready":"3","review":"0","waiting":"1","working":"0"}}}
   ```

   **Expected:** `merging` carries "every merging primary with its stream, head and attempt"
   (`docs/SPEC-SPRINT.md`, the `where` row; `where -h`, `--cards` "also every dealt work
   card, judgment and lane"), so an empty array while the work table's merging column is 0.
   As printed the frame reports four merging primaries where none is merging -- a waiting
   sentinel and three ready cards among them -- and the dashboard's merge panel reads
   exactly this frame (`where --json --cards`), so it would draw a merge queue that does
   not exist. Grade: URGENT.

3. `--json` is accepted and silently ignored on several verbs' refusal path, which prints a
   plain `nova-sprint <verb>:` line instead of the one JSON object every verb promises.

   **Command:**
   ```
   nova-sprint card nope --json
   ```
   (a sprint exists; `nope` is no primary)

   **Printed** (stderr, exit 1; nothing on stdout):
   ```
   nova-sprint card: no primary nope; run: nova-sprint where
   ```

   **Expected:** `card -h` lists `--json`, and the banner's contract is one value with two
   renderings ("Each verb prints what moved (MOVED), what did not and why (REFUSED, on
   stderr)"); the refusal should be a JSON object on stdout (as `take --json`,
   `merge --json` and `land --json` give) and its line should lead with `REFUSED` or
   `FAILED`. `nova-sprint wait nope --for 1m --json`, `nova-sprint friend beat nobody
   --json`, `nova-sprint fleet sync --check --json` and `nova-sprint promote --dry-run`
   (outside a repository) print the same plain shape, so a caller that asked for JSON gets
   text on the error path and cannot parse it. Grade: NEXT.

4. `nova-sprint help -h` is a refusal: help is not allowed its own help door.

   **Command:**
   ```
   nova-sprint help -h
   ```

   **Printed** (stderr, exit 2):
   ```
   nova-sprint help REFUSED: unknown verb -h; run: nova-sprint help
   ```

   **Expected:** exit 0 with the help on stdout. The banner says "For one verb's usage,
   examples, flags and exit codes: `nova-sprint help <verb>` (or `<verb> -h`)", and `help`
   stands in its own verb list; `-h` is read as the verb argument. Grade: NEXT.

5. `nova-sprint help seat push` is refused as an unknown verb even though `fleet`, `friend`
   and the other groups answer `help <group> <verb>`.

   **Command:**
   ```
   nova-sprint help seat push
   ```

   **Printed** (stderr, exit 2):
   ```
   nova-sprint help REFUSED: unknown verb seat push; run: nova-sprint help
   ```

   **Expected:** the help for `seat push`, as `nova-sprint help fleet beat` prints the help
   for `fleet beat` (exit 0). `seat`'s own usage lists `push` and `pong` as forms, so a
   stranger reaching for the sub-verb's help through the documented door is turned away and
   must know to type `seat push -h` instead. Grade: NEXT.

6. `machinery`'s remedy for the down loop names `launchctl` on Linux, where the tool's own
   install verbs write systemd units.

   **Command:**
   ```
   nova-sprint machinery
   ```

   **Printed** (first three lines, exit 1):
   ```
   MACHINERY server DOWN addr=none why="NOVA_SPRINT_SERVER is not set" remedy="export NOVA_SPRINT_SERVER=127.0.0.1:$(nova-config loop show sprint-server-<name> | sed -n 's/.*--listen [^:]*:\([0-9]*\).*/\1/p')"
   MACHINERY store OK redis=mem:... dbsize=- machine=stopped epoch=0
   MACHINERY loop DOWN tick=never why="no heartbeat: run --listen has not ticked this store" remedy="launchctl kickstart -k gui/$(id -u)/com.nova.loop.sprint-server-<name>"
   ```

   **Expected:** a remedy for the platform the tool is running on. On this Linux machine
   `seat install --dry-run` and `install member --dry-run` both write
   `$XDG_CONFIG_HOME/systemd/user/<unit>.service`, so the loop's remedy should name the
   systemd user manager and the unit those verbs install, not `launchctl` and a launchd
   label; a Linux reader pastes a command that does not exist. (The tool interpolated this
   machine's own loop name into both remedies; it is shown as `<name>` here.) Grade: NEXT.

7. `seat push -h` answers with `seat`'s effect class, so the two writing forms read as
   inspections.

   **Command:**
   ```
   nova-sprint seat push -h
   ```

   **Printed** (the usage block; the effect line is the third shown line):
   ```
   usage: nova-sprint seat [--repair --reason <text>] | push [--harness <name> --target <dir> [--session <id>]] | pong <nonce>
   from `nova-sprint help`:
     nova-sprint seat check
   ...
   effect: inspection: reads the seat (holder, epoch, generation), writes nothing
   ```

   **Expected:** `push` to state its own effect and flags: the push loop's report records
   the check it delivered (`--sent`), and `pong` records the session's proof
   (`--actor`), both store writes. As printed, both forms claim to write nothing, and the
   flags that belong to them (`--sent`, `--failed`, `--harness`, `--target`) are mixed into
   one `seat` block. Grade: NEXT.

8. `add` refuses a single named id unless `--one` is given, but its synopsis reads as if one
   id is a normal form.

   **Command:**
   ```
   nova-sprint add --stream s2 s2-1 --needs s1-1
   ```

   **Printed** (stderr, exit 2):
   ```
   nova-sprint add REFUSED: one card at a time is the mistake; put the briefs in a directory and run: nova-sprint add --stream s2 --brief-dir <dir>; or say --one for a single card; run: nova-sprint add -h
   ```

   **Expected:** the synopsis `add --stream <s> (<id>... | --count <n> | ...)` reads as "one
   or more ids"; the tool requires `--one` for exactly one and names that only in the
   refusal's prose, not in the usage line. The refusal carries a one-turn remedy, so this
   is friction, not a wrong result. Grade: NEXT.

## What worked (kept short)

The whole card's life can be driven by hand on a twin once the push proof exists: `init`,
`add`, `start`, `tick`, `take` (with its packet), `progress`, `finish`, `read --begin/--ok`,
`accept`, `merge`, and the git form `finish --head` + `land --repo-dir` (a local bare
origin) all moved the tables and printed the exact next command. `--op` replay is real
(`add --op myop1` twice returns `replay=yes` and writes once). Refusals mostly name the
state and the remedy in one line, and the nearest-name answers (`add --bogus`, an unknown
verb) list the alternatives. `backup --file`, `snapshot`, `lane take/give/list`, the
`reader`/`stream`/`set`/`hold`/`unhold` verbs, and the fleet verbs all ran against the
twin.

## Not run

`dashboard`, `watch`, `server switch`, `selftest`, `selftest land`, `demo load`, and a real
(not `--dry-run`) `install`/`uninstall` start a server, a loop or a unit and were left to
their help and `--dry-run` forms; `run`, `inbox --wait` and `where --watch` were run only
as the twin's refusals (the banner says a twin refuses them); `friend sync install`/
`uninstall` and the `install`/`uninstall` kinds were exercised through `-h` and `--dry-run`.
No Redis, no live store and no server was used, as the card requires.

## Gate

Run on the bench, `GOCACHE` private, `GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1`:

```
go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent
ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.013s [no tests to run]

go test -count=1 -timeout 600s ./internal/docs ./internal/ci
ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.524s
ok  	github.com/mas-bandwidth/nova-tools/internal/ci	14.718s
```

The card's named test `TestDocsTreeIsConsistent` does not exist in `./internal/docs` at
this tip (the run is `[no tests to run]`), so it is no gate; the two packages the card's
STEP 4 names are green. The report itself is under `docs/dogfood`, which is already a
catalogued directory (`internal/docs/catalog.go`), so the map guard passes and this record
needs no code or map change.

READ 6/10 -- the banner answers what it does, how it works and where its state lives, every
verb answers `-h` (but `help` itself), and refusals mostly carry a one-turn remedy; it is
held down by the first run that cannot be run as written and by the help doors (`help -h`,
`help seat push`) and effect line (`seat push -h`) that do not hold.

USE 6/10 -- with the seat proof made by hand every verb I could reach moved real state on a
twin and the card flow landed both without git and against a bare origin; a cold stranger
is stopped at `add` by the push gate, and `where --json --cards` hands the dashboard a
`merging` list that contradicts its own table.

urgent=2 next=6
