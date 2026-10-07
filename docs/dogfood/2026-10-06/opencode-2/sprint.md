# nova-sprint dogfood — Zhi (deepseek/deepseek-v4), 2026-10-06

Read as a stranger: only `nova-sprint -h`, `nova-sprint help`, `nova-sprint <verb> -h`,
and the tool's pages under `docs/` (`docs/SPEC-SPRINT.md`, the nova-sprint section of
`docs/CLI.md`, and the `### First run` transcript in `docs/TESTS.md`). Built from
`sprint/mechanical-2026-10-02` at `8804eeba22ea9017d7187cc9c885de761b617ac2` and used as
`nova-sprint v1.0.1-0.20261007155547-8804eeba22ea linux/amd64 go1.26.6` on a Linux bench
(the host is written `<bench>` below, a placeholder the tree's generality rule requires).
Every top-level verb named in `nova-sprint help`'s usage list was run for real or read as
`-h`, against `mem:<file>`, the in-memory twin the help calls the no-Redis path, with real
flags and in the order a coordinator would use them. The server, host-delivery and
destructive verbs (`run`, `dashboard`, `install`, `uninstall`, `demo`, `clear`, `teardown`)
were read as `-h` and never run: this card starts no server and touches no host. A few
further verbs were not run for real — `watch`, `server switch`, `coordinator`, `fsck seat`,
`friend health`, `friend take`, `friend sync`, `fleet sync`, `stream archive`,
`stream unarchive` — their help was read (`watch --wake -h`, `server switch -h`,
`coordinator -h`, `fsck seat -h`, and the `friend`, `fleet` and `stream` group help) and
they are named here so the gap is on the record. `seat install` ran for real only to learn
why its remedy does not work on a twin. No live store and no server were touched, and no code was changed.

The headline is that the documented no-Redis learning path is dead on arrival: the
coordinator push proof is enforced on the twin, while the only verb that could satisfy it
(`seat install`) is refused on a twin. `docs/SPEC-SPRINT.md` (the push proof) says every
coordinator verb is refused without a live proof and that `seat install` is refused for the
in-memory twin, and the shipped binary does exactly that; but `docs/CLI.md`'s "First run",
the `docs/TESTS.md` transcript and `nova-sprint help` all show that same twin flow
succeeding and say the twin "runs every verb". A stranger following the only documented
first run is stopped at command two.

Commands below are quoted as typed, with `$BENCH` standing for the job's bench directory,
`$HOME` for the scratch home the run set and `$TMP` for a temporary directory.

## Findings

### 1. The documented no-Redis first run refuses `add`, and `seat install` — its one named remedy — is refused on the same twin — URGENT

Command: `nova-sprint add --stream s1 --count 1 --one` (after the documented
`export NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=boss` and
`nova-sprint init --readers reader-a,reader-b --members m1`, which succeeds)

Printed (exit 2):

    nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>

Expected: `docs/CLI.md` "First run" says "Try one card's whole flow with no Redis or git",
shows this exact line answering `MOVED s1-1 -> ready stream=s1 ...` and
`ADD OK stream=s1 cards=1 ... before=- moved=1 refused=0 notes=0 op=add-t2-1`, and
`docs/TESTS.md`'s `### First run` prints the same; `nova-sprint help` says the twin "runs
every verb". The refusal names `seat install`, but on the same twin
`nova-sprint seat install --harness dsh --target $BENCH/session` answers

    nova-sprint seat install REFUSED: the push loop waits on the sprint's machine, and the in-memory twin mem:sprint.twin has none: install it for a Redis store or the sprint's server; nothing was written; run: nova-sprint seat install -h

So the one store the help tells a stranger to learn on cannot admit a single card, and the
remedy it prints is refused by that store. Grade: URGENT.

### 2. `selftest` cannot self-test the twin path: it fails at its own `add` step — URGENT

Command: `nova-sprint selftest`

Printed (exit 1):

    SELFTEST FAILED step=add why=nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir> dir=$TMP/nova-sprint-selftest-3900966852

Expected: `selftest -h` says it "makes a fresh directory, a bare origin and a clone whose
base holds a go module, runs the card's flow of the walkthrough on a twin file in it and
lands one card through the tree gate ... `SELFTEST OK`". It is the same push gate as
finding 1, on the tool's own self-check, so the one path that is supposed to prove the twin
flow cannot pass. Grade: URGENT.

### 3. `selftest land` is refused by the tool's own one-card rule: its internal `add` omits `--one` — URGENT

Command: `nova-sprint selftest land`

Printed (exit 1):

    nova-sprint selftest land FAILED: selftest land: command [add --stream selftest --count 1 --brief-file $TMP/nova-sprint-selftest-1150477132/canned-brief.md --redis mem:$TMP/nova-sprint-selftest-1150477132/selftest.twin --actor boss] failed: nova-sprint add REFUSED: one card at a time is the mistake; put the briefs in a directory and run: nova-sprint add --stream selftest --brief-dir <dir>; or say --one for a single card; run: nova-sprint add -h: exit status 2

Expected: `selftest land` runs the walkthrough and lands its card. The command it builds
itself, `add --stream selftest --count 1 --brief-file <path>`, is refused by the same add
lint for the very omission `--one` repairs, so the shipped sub-verb cannot run at all
(and this one is not the push gate — it is the tool contradicting its own rule). Grade:
URGENT.

### 4. `adopt`'s refusal tells the user to pass `--version`, a flag `adopt` does not have — URGENT

Command: `nova-sprint adopt 1.1.0 --source . --inventory i --reason x --dry-run`

Printed (exit 2):

    nova-sprint adopt REFUSED: <version|path>: the version "1.1.0" is not v-prefixed (pass --version vX.Y.Z); run: nova-sprint adopt -h

Expected: `adopt -h`'s usage takes the version positionally
(`nova-sprint adopt <version|path> ...`, example `nova-sprint adopt v1.2.0-dev.0123abc ...`),
so the remedy should read `adopt v1.1.0`. Following the printed remedy,
`nova-sprint adopt --version v1.1.0 --source . --inventory i --reason x --dry-run` answers

    nova-sprint adopt REFUSED: unknown flag --version; the flags of adopt are --actor, --ansible, --dry-run, --epoch, --inventory, --json, --limit, --max, --op, --reason, --receipts, --redis, --source; did you mean --reason?

A refusal whose one named remedy is itself refused. Grade: URGENT.

### 5. `land --dry-run` (and every other write-nothing verb: `rebase --dry-run`, `cost reconcile --dry-run`, `play --simulation`) is refused by the push gate — URGENT

Command: `nova-sprint land --stream s1 --dry-run`

Printed (exit 2):

    nova-sprint land REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>

Expected: `land -h` says `--dry-run` "reads the store only: no git, no push, no report", and
`rebase -h`, `cost reconcile -h` and `play -h` likewise describe a preview or a simulation
that writes nothing. None of them can write a push target, so none can be tried on the
store the help recommends; on the same twin `play --simulation --ticks 1`,
`rebase --from a --to b --dry-run` and `cost reconcile --dry-run` return the same PUSH DOWN
line, and `play` is the tool's own seeded simulation of the world through the verbs. A dry
run a reader cannot run to preview anything is not a dry run. Grade: URGENT.

### 6. `backup --file <path> --dry-run` fails when the target exists, so it is not a preview — NEXT

Command: `nova-sprint backup --file $BENCH/b1 --dry-run` (the named file was written by the
same command a moment before, without `--dry-run`)

Printed (exit 1):

    nova-sprint backup FAILED: $BENCH/b1 exists and is never overwritten; run: nova-sprint backup --file <a path that does not exist>

Expected: `backup -h` says `--dry-run  verify the backup in memory without writing --file;
writes nothing`, and the effect line repeats "with `--file`, writes the store to a new
owner-only file ... `--dry-run` writes nothing". A dry run should verify the store in
memory and report what it would write whether or not the target path already exists; as it
stands, asking "would this backup work at this path?" against an existing path is a
failure, not a preview. Grade: NEXT.

### 7. `seat check` and `machinery` print a macOS `launchctl` remedy on Linux — NEXT

Command: `nova-sprint seat check` (Linux; `machinery` prints the same block)

Printed (exit 1, first three lines):

    MACHINERY server DOWN addr=none why="NOVA_SPRINT_SERVER is not set" remedy="export NOVA_SPRINT_SERVER=127.0.0.1:$(nova-config loop show sprint-server-<bench> | sed -n 's/.*--listen [^:]*:\([0-9]*\).*/\1/p')"
    MACHINERY store OK redis=mem:sprint.twin dbsize=- machine=stopped epoch=0
    MACHINERY loop DOWN tick=never why="no heartbeat: run --listen has not ticked this store" remedy="launchctl kickstart -k gui/$(id -u)/com.nova.loop.sprint-server-<bench>"

Expected: on this Linux host the loop is a systemd user unit (the same run's
`nova-sprint units --check` reads `$HOME/.config/systemd/user/nova-sprint-server.service`),
so the remedy should be the `systemctl --user` form; `launchctl` does not exist here. A
coordinator on a Linux bench is told to run a macOS command. Grade: NEXT.

### 8. `fleet beat` reports `fds-max=9223372036854775807` instead of the machine's open-file limit — NEXT

Command: `nova-sprint fleet beat m1 --load 10`

Printed (exit 0):

    FLEET-BEAT OK m1 at=2026-10-07T16:00:42Z load=10.0% last=10.0% how=given cores=64 fds=3927 fds-max=9223372036854775807 fds-level=ok

Expected: `fds-max` to be the machine's open-file limit, the ceiling the `fds` count is
approaching; on this host `ulimit -n` is 1024, so `2^63-1` is not a reading of anything.
The warn and alarm levels still use their bounds, so the guard fires; the displayed maximum
is the defect. Grade: NEXT.

### 9. `remind --in 0s` is refused with a message that contradicts the command — NEXT

Command: `nova-sprint remind --in 0s --note soon`

Printed (exit 2):

    nova-sprint remind REFUSED: give one of --in <duration> (from now) or --at <time> (an absolute time), not both and not neither; run: nova-sprint remind -h

Expected: exactly one of the two was given (`--in 0s`), and `remind --in 1h --note hello`
succeeds on the same store; the parser reads the zero duration as "neither" and then says
"not both and not neither", naming a fault that is not there. The message should name the
real fault (a duration above 0) or accept 0. Grade: NEXT.

### 10. `promote --dry-run` outside a clone prints a raw git fatal, and its printed remedy is the same failing command — NEXT

Command: `nova-sprint promote --dry-run` (run in a scratch directory, not a clone)

Printed (exit 1):

    nova-sprint promote: git symbolic-ref: fatal: not a git repository (or any of the parent directories): .git; run: nova-sprint promote --dry-run

Expected: `promote -h` documents `--repo-dir <clone>  the clone the branch is cut in
(default: the current directory)`, so with no clone in the current directory the verb
should refuse in the tool's own grammar (`promote REFUSED: --repo-dir <clone> wants ...`)
instead of echoing a raw git error, and the printed remedy must not be the identical
command. Grade: NEXT.

### 11. `help version` refuses a verb that exists — NEXT

Command: `nova-sprint help version`

Printed (exit 2):

    nova-sprint help REFUSED: unknown verb version; run: nova-sprint help

Expected: `nova-sprint version` is a verb (it prints the version line and exits 0) and the
bare command's own refusal lists `help, version` among the available verbs, so
`help version` should print its help. The same holds for `help help` and `help --help`,
which are also answered "unknown verb" against a verb the banner lists. Grade: NEXT.

### 12. `docs/CLI.md`'s nova-sprint reference is stale: it omits 23 verbs the binary's help prints — NEXT

Command: `nova-sprint help` (compared line by line with the `## nova-sprint` section of
`docs/CLI.md`, lines 1080–1560)

Printed (the binary's usage lists, among others, `preflight`, `redo`, `unpin`,
`verify-landed`, `landed`, `snapshot`, `promote`, `hold`, `unhold`, `needs`, `held`, `bases`,
`watch`, `friend clean`, `friend reconcile`, `lane`, `promoted`, `merge-window`, `live`,
`adopt`, `coordinator`, `server`, `fsck`):

    usage:
      nova-sprint init [--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>] [--owner <name>] [--rules <file>]
      nova-sprint add --stream <s> (<id>... | --count <n> | ...) [--held] [--allow-shared-paths] [--one: a single card is meant] ...

The CLI.md section carries 107 top-level usage lines and none of the 23 names above, so a
reader who trusts the command reference cannot discover a fifth of the tool. Grade: NEXT.

READ 6/10 — every verb has a help page with usage, examples, flags, an effect line and an
exit table, and the refusal grammar, the nearest-flag suggestions and `--shadow`/`--dry-run`
notes are unusually good, but the banner is a 670-line wall for one `-h`, `add -h` embeds a
whole card template, the documented first-run transcript is false for the shipped binary,
and CLI.md's reference is 23 verbs behind.

USE 3/10 — the reads and local writes I could reach work and read clean (`init`, `tick` and
`tick --shadow`, `where`, `check`, `stats`, `routes`, `rules`, `bases`, `needs`, `streams`,
`held`, `sentinels`, `log`, `view`, `handover`, `preflight`, `release check`,
`verify-landed`, `lane --dry-run`, `answer --dry-run`, `snapshot`, `backup --file`,
`fleet beat`, `machinery`, `units --check`, `live`), but the documented learning path cannot
admit one card on a twin, `selftest` and `selftest land` both fail, and nearly every
coordinator write — dry runs and the simulation included — is refused before it can be
exercised as a user.

urgent=5 next=7
