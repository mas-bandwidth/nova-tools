# nova-sprint dogfood — Zhi (deepseek/deepseek-v4), 2026-10-06

Read as a stranger: only `nova-sprint -h`, `nova-sprint help`, `nova-sprint <verb> -h`,
and the tool's pages under `docs/` (`docs/SPEC-SPRINT.md`, the nova-sprint section of
`docs/CLI.md`, and the `### First run` transcript in `docs/TESTS.md`). Built from
`sprint/mechanical-2026-10-02` at `7b132f109d92d8281b85f9c3ded24dddce2073ba` and used as
`nova-sprint v1.0.1-0.20261007013005-7b132f109d92 linux/amd64 go1.26.6` on a Linux bench (the
host's name is written `<bench>` below, a placeholder the tree's generality rule requires).
Every top-level verb in `nova-sprint help` ran at least once against `mem:<file>`, the
in-memory twin the help calls the no-Redis path, with real flags and in the order a
coordinator would use them, and every listed sub-verb ran too except `demo load` and
`selftest land`, which start a throwaway store or a build (only their `-h` ran); the
refusals ran as well. No live store and no server were touched,
and no code was changed. The headline is that the twin cannot get past its second command:
the coordinator push proof is enforced on the twin, while the only verb that could satisfy
it (`seat install`) is refused on a twin.

Commands below are quoted as typed, with `$BENCH` standing for the job's bench directory and
`$HOME` for the scratch home the run set, both placeholders.

## Findings

### 1. The documented no-Redis first run refuses `add`: the push proof is enforced on a twin that cannot install a push loop — URGENT

Command: `nova-sprint add --stream s1 --count 1 --one` (after the documented
`nova-sprint init --readers reader-a,reader-b --members m1`, which succeeds)

Printed (exit 2):

    nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>

Expected: `docs/CLI.md` "First run" says "Try one card's whole flow with no Redis or git",
sets `NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=boss`, and shows this exact line
answering with `MOVED s1-1 -> ready stream=s1 score=1` and `ADD OK
stream=s1 cards=1 before=- moved=1 refused=0 notes=0 op=add-t2-1`; `docs/TESTS.md`'s
`### First run` prints the same, and `nova-sprint help` says the twin "runs every verb".
The refusal names `seat install` as the remedy, but on the same twin
`nova-sprint seat install --harness opencode --target <dir>` is refused too: `nova-sprint
seat install REFUSED: the push loop waits on the sprint's machine, and the in-memory twin
mem:sprint.twin has none`. So a stranger following the only documented first run is stopped
at command two with no remedy on the store the help told them to use. (The transcript is
still reproduced by the in-process unit test, which passes; the shipped binary does not.)
Grade: URGENT.

### 2. `selftest` cannot self-test the twin path either: it fails at its `add` step — URGENT

Command: `nova-sprint selftest`

Printed (exit 1):

    SELFTEST FAILED step=add why=nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>

Expected: `nova-sprint selftest -h` says it "makes a fresh directory, a bare origin and a
clone whose base holds a go module, runs the card's flow of the walkthrough on a twin file
in it and lands one card through the tree gate; writes only in that directory, opens no
store of the caller's and no network". It should end `SELFTEST OK`. This is the same gate as
finding 1, on the tool's own self-check, so the one path that is supposed to prove the twin
flow cannot pass. Grade: URGENT.

### 3. `adopt`'s refusal tells the user to pass `--version`, a flag `adopt` does not have — URGENT

Command: `nova-sprint adopt 1.1.0 --source . --inventory i --reason x --dry-run`

Printed (exit 2):

    nova-sprint adopt REFUSED: <version|path>: the version "1.1.0" is not v-prefixed (pass --version vX.Y.Z); run: nova-sprint adopt -h

Expected: the usage and example take the version positionally (`nova-sprint adopt
<version|path> ...`, `nova-sprint adopt v1.2.0-dev.0123abc ...`), so the remedy should read
`adopt v1.1.0`, or the command should accept `--version`. Following the printed remedy,
`nova-sprint adopt --version v1.1.0 --source . --inventory i --reason x --dry-run` answers
`nova-sprint adopt REFUSED: unknown flag --version; the flags of adopt are --actor,
--ansible, --dry-run, --epoch, --inventory, --json, --limit, --max, --op, --reason,
--receipts, --redis, --source; did you mean --reason?`. A refusal whose one named remedy is
itself refused. Grade: URGENT.

### 4. `fleet sync --check` exits 3 (documented as "the config could not be read") for a push-proof refusal — NEXT

Command: `nova-sprint fleet sync --check`

Printed (exit 3):

    nova-sprint fleet sync: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>

Expected: `fleet sync -h`'s exit table is "0 done (--check: no drift), 1 refused, 2 usage,
a store that did not answer, or (--check) there is drift, 3 the config could not be read".
No config was read here; the coordinator gate refused the caller. Exit 3 tells a release
script the inventory is unreadable when it is the seat that is unreachable. Grade: NEXT.

### 5. `seat check` and `machinery` print a macOS `launchctl` remedy on Linux — NEXT

Command: `nova-sprint seat check` (Linux, `machinery` prints the same block)

Printed (exit 1, first three lines):

    MACHINERY server DOWN addr=none why="NOVA_SPRINT_SERVER is not set" remedy="export NOVA_SPRINT_SERVER=127.0.0.1:$(nova-config loop show sprint-server-<bench> | sed -n 's/.*--listen [^:]*:\([0-9]*\).*/\1/p')"
    MACHINERY store OK redis=mem:sprint.twin dbsize=- machine=stopped epoch=0
    MACHINERY loop DOWN tick=never why="no heartbeat: run --listen has not ticked this store" remedy="launchctl kickstart -k gui/$(id -u)/com.nova.loop.sprint-server-<bench>"

Expected: on this Linux host the loop is a systemd user unit (the same run's
`nova-sprint units --check` reads `.../home/.config/systemd/user/nova-sprint-server.service`),
so the remedy should be the `systemctl --user` form; `launchctl` does not exist here. A
coordinator on a Linux bench is told to run a macOS command. Grade: NEXT.

### 6. `backup --file <path> --dry-run` fails when the target exists, so it is not a preview — NEXT

Command: `nova-sprint backup --file $BENCH/work/sweep/backup/b1 --dry-run` (the
named file was written by the same command a moment before, without `--dry-run`)

Printed (exit 1):

    nova-sprint backup FAILED: $BENCH/work/sweep/backup/b1 exists and is never overwritten; run: nova-sprint backup --file <a path that does not exist>

Expected: `backup -h` says `--dry-run  verify the backup in memory without writing --file;
writes nothing`, and the effect line repeats "with --file, writes the store to a new
owner-only file ... `--dry-run` writes nothing". A dry run should verify the store in memory
and report what it would write whether or not the target path already exists; as it stands,
asking "would this backup work at this path?" against an existing path is a failure, not a
preview. Grade: NEXT.

### 7. `live` with no installed binary prints a bare fork/exec error with a dangling `: ` — NEXT

Command: `nova-sprint live` (no `--bin-dir`)

Printed (exit 1):

    nova-sprint live: $HOME/.local/bin/nova-sprint version: fork/exec $HOME/.local/bin/nova-sprint: no such file or directory: ; run: nova-sprint live -h

Expected: `live -h` says the verb "prints the manifest of this host and changes nothing"
and "exit 0 the manifest was read (whatever it says ...), 1 the installed nova-sprint or
the agents directory could not be read". With nothing installed it should say which install
is missing in a `LIVE SERVER ... installed=` line before failing, and the empty `: ` after
"no such file or directory" is a formatting defect (there is no `why=` text). Grade: NEXT.

### 8. `fleet beat` reports `fds-max=9223372036854775807` instead of the machine's open-file limit — NEXT

Command: `nova-sprint fleet beat m1 --load 10`

Printed (exit 0):

    FLEET-BEAT OK m1 at=2026-10-07T01:47:58Z load=10.0% last=10.0% how=given cores=64 fds=3056 fds-max=9223372036854775807 fds-level=ok

Expected: `fds-max` to be the machine's open-file limit, the ceiling the `fds` count is
approaching; on this host `/proc/self/limits` says `Max open files 1024 524288`, and
`ulimit -n` is 1024, so `2^63-1` is not a reading of anything. The warn and alarm levels do
use the `--fd-warn`/`--fd-alarm` bounds (setting `--fd-warn 1000` correctly prints
`fds-level=warn`), so the guard still fires; the displayed maximum is the defect. Grade: NEXT.

READ 6/10 — every verb has a help page with usage, examples, flags and an effect line, and the exit-code tables and the twin warning ("run one command at a time") are unusually good, but the first-run transcript and the "runs every verb" claim are false for the shipped binary, `adopt`'s remedy names a flag it does not have, and the machinery remedies are platform-blind.

USE 3/10 — the read-only and worker verbs I could reach work and read clean (`where`, `card`, `log`, `inbox`, `seat`, `lane`, `backup --file`, `fleet beat`), but the documented learning path cannot admit a single card on a twin, `selftest` fails the same way, and almost every coordinator write is refused before it can be reviewed, so the tool's central flow could not be exercised as a user.

urgent=3 next=5
