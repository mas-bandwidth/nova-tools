# nova-sprint dogfood — dsh (zhi), 2026-10-06

Read as a stranger: only `nova-sprint -h`, `nova-sprint help`, `nova-sprint
<verb> -h` (and the group forms) and the page under `docs/`
(docs/SPEC-SPRINT.md — the verbs section, the seat and the push proof — and
docs/CLI.md's nova-sprint section). Built from the staged checkout at
6ec8bb02edc83283630f2e10e21bd035b3336f65 as `nova-sprint
v1.0.1-0.20261006195723-6ec8bb02edc8 darwin/arm64 go1.26.6` (built on a bench,
run on the working machine; the two selftest forms re-run on the bench's Linux
build too). Every verb ran at least once with its real flags against the
in-memory twin `mem:sprint.twin` in a scratch dir of the job directory, the
refusals too, about 25 minutes of use: the card flow end to end with a real
git origin beside it (init, add with and without a brief, start, tick, take,
progress, finish with --head and --usage, read --begin/--ok, the tick's own
accept, land --repo-dir --base, where through DONE and the automatic
archive), then queue, card (--brief, --fields), needs, held, sentinels,
sentinel set, release, resolve, bases, log (--card, --stream, --since),
check, repair, inbox (--read, --open, --wait), wait, ack, answer (--dry-run,
fixed), ci, funded, promoted, merge-window open (twice under one --op),
set, stream set/archive/unarchive/remove, reader
add/set/away/up/retire/remove, rank, relink, recut, brief, move, drop,
rework, return, redo, unpin, ask, hold, unhold, fleet
beat/up/down/level/sync, friend sync/beat/down/up/cards/take/level/health/
clean/reconcile, collect, lane take/give/list, play, snapshot and
--restore-drill, backup --file and --out, demo stop and a refused demo load,
promote --dry-run, install/uninstall --dry-run, units --check, server switch
--dry-run, coordinator, cost reconcile --dry-run, stats, stats tidy --dry-run,
stats --routes, quack, preflight, release check, view
coordinator/cards/worker, where --json/--release, watch --wake, dashboard,
seat (push, --sent, pong, install, uninstall), fsck seat, machinery,
handover, goal set/show/drop, selftest and selftest land, clear, teardown,
version — plus the unknown verb and flag, the wrong actor, the stale
generation, and the confirmation refusals of clear and teardown.

Not run: the dashboard's and `demo load`'s success paths (both start a
server on this machine, which the card forbids), `run`, `where --watch` and
`inbox --wait` (refused on a twin by design, exercised as refusals), and the
nova-config-backed verbs (fleet sync, friend sync, friend clean, collect,
fsck seat) beyond their config refusals, no Postgres being here; the friends
table was empty, so every friend verb ran as its refusal.

## Findings

1. The documented first run cannot be pasted and run: the push proof refuses
   its second command, and the remedy it names is itself refused on a twin.
   - `nova-sprint add --stream s1 --count 1 --one`
     ```
     nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
     ```
     exit 2; the next line of the block (`nova-sprint start`) prints the same
     refusal. This is the second command of the `### First run` block in
     docs/CLI.md and of the banner's twin flow, pasted exactly as printed with
     only the export line each names.
   - What I expected: onboarding point 1 — an example block whose lines run
     as printed, and a first run that needs no more than the page says. The
     remedy the refusal names is a dead end on the twin the same block tells me
     to use: `seat install` answers `REFUSED: the push loop waits on the
     sprint's machine, and the in-memory twin mem:sprint.twin has none`. The
     escape that works — `seat push --harness <h> --target <dir>`, then
     `seat push --sent <nonce>`, then `seat pong <nonce>` — is documented only
     in SPEC-SPRINT.md's push-proof section, where `seat push --sent` is
     described as the push loop's own report, so a stranger must invent a
     nonce to satisfy it. The repo's `TestTheFirstRunTranscriptRunsOverATwin`
     runs the same transcript with the proof disarmed (`pushArmedDefault =
     false` in TestMain), so the shipped binary refuses what the docs say
     runs.
   - Grade: URGENT.

2. `selftest`, the install gate, is red on a good binary: both of its forms
   die at their own `add`.
   - `nova-sprint selftest`
     ```
     SELFTEST FAILED step=add why=nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir> dir=/tmp/nova-sprint-selftest-4262236488
     ```
     exit 1 (the same line on the working machine and on the bench's Linux
     build); and `nova-sprint selftest land --binary <path>`:
     ```
     nova-sprint selftest land FAILED: selftest land: command [add --stream selftest --count 1 --brief-file /tmp/nova-sprint-selftest-3006828184/canned-brief.md --redis mem:/tmp/nova-sprint-selftest-3006828184/selftest.twin --actor boss] failed: nova-sprint add REFUSED: one card at a time is the mistake; put the briefs in a directory and run: nova-sprint add --stream selftest --brief-dir <dir>; or say --one for a single card; run: nova-sprint add -h: exit status 2
     ```
     exit 1.
   - What I expected: the spec's `SELFTEST OK landed=1 land=... gate=...` —
     the gate a build is installed by must pass on a good binary. The plain
     form dies on the same push proof as finding 1 (the walkthrough's `add`
     runs against the selftest's own fresh twin, where no proof can exist);
     the land form dies one check earlier, its own canned `add` naming a
     single `--brief-file` without `--one`, the very refusal a stranger gets
     for the same shape. A build gated by this selftest cannot land.
   - Grade: URGENT.

3. `add` on a RUNNING twin prints its MOVED line before any read can see the
   card, and the reads point at each other.
   - `nova-sprint add --stream s3 --brief-file brief-card.md --one` (machine
     RUNNING)
     ```
     MOVED brief-card -> ready stream=s3 score=4
     ADD OK stream=s3 cards=1 before=- moved=1 refused=0 notes=0 op=add-t52-1
     ```
     then, before the next tick, `nova-sprint card brief-card`:
     ```
     nova-sprint card: no primary brief-card; run: nova-sprint where
     ```
     and `view cards`, `where` and `needs` show nothing of it until a tick
     prints `MOVED drain: brief-card -> ready ... (add by boss)`.
   - What I expected: the twin section says a card's move is queued until the
     next tick, so I expected the ADD line to say queued, or the reads to name
     `tick` as the remedy; `card`'s remedy names `where`, and `where` shows
     nothing. In the same window `stream archive s5` archived a stream whose
     just-added sentinel was still queued (it read an empty row, said
     `0 cards landed`), and the drain then placed the sentinel after the
     unarchive; nothing was lost — the next tick placed it — but every read in
     that window answers a card that `ADD OK` says moved.
   - Grade: NEXT.

4. A twin snapshot is written as a `.rdb` that its own `--restore-drill`
   refuses.
   - `nova-sprint snapshot --restore-drill snaps/snapshot-20261006T204550Z.rdb`
     ```
     nova-sprint snapshot REFUSED: snaps/snapshot-20261006T204550Z.rdb does not load into a twin: not an RDB (no REDIS header); run: nova-sprint snapshot -h
     ```
     exit 2 — the file is the one `snapshot --dir snaps` had just written and
     verified: `SNAPSHOT OK file=snaps/snapshot-20261006T204550Z.rdb
     sha256=... bytes=229252 keys=4 cards=5 verified=checksum+twin`.
   - What I expected: the drill is the documented check of a snapshot file
     ("checks the file against its checksum, loads it into a twin and prints
     its counts"); on the twin store the snapshot verb writes the twin's
     document under a `.rdb` name and prints verified, so I expected its own
     drill to read that file back. It does load — `NOVA_SPRINT_REDIS=mem:`
     that file answers `where` — but only the drill's RDB header check is
     offered, and the refusal's remedy is `-h`.
   - Grade: NEXT.

5. `promote --dry-run` outside a git repository surfaces git's own fatal and
   a remedy that repeats the command just run.
   - `nova-sprint promote --dry-run`
     ```
     nova-sprint promote: git symbolic-ref: fatal: not a git repository (or any parent up to mount point /Volumes) | Stopping at filesystem boundary (GIT_DISCOVERY_ACROSS_FILESYSTEM not set).; run: nova-sprint promote --dry-run
     ```
     exit 1.
   - What I expected: a refusal in the tool's own grammar naming what it
     wants (a `--branch`, or a `--repo-dir`), as every other verb gave me;
     instead the raw `git symbolic-ref` fatal is the line, and the remedy is
     the failing command itself, so the next turn is not a paste.
   - Grade: NEXT.

6. The `machine:silent` judgment is shown open with no alias, and `ack`
   refuses the id the inbox itself prints.
   - `nova-sprint ack machine:silent --reason 'twin ticks by hand'`
     ```
     REFUSED machine:silent: no open judgment machine:silent; run: nova-sprint inbox
     ACK FAILED moved=0 refused=1 notes=0
     ```
     exit 1, while the inbox prints `JUDGMENT machine:silent ! the machine is
     not ticking` and `inbox --open machine:silent` answers it.
   - What I expected: a judgment the inbox shows open to be nameable by the id
     or the alias it prints. This one carries no alias (the ci judgment beside
     it shows `alias=j2`), and a stale alias behaves differently across verbs:
     `wait j1` ran OK on a judgment the machine had answered while `ack j1` was
     refused `no open judgment`, where the spec promises a stale `--answers`
     id a NOTE line saying the machine answered it already.
   - Grade: NEXT.

7. `backup --file`'s secret-scan failure names line numbers of a file it has
   already removed.
   - `nova-sprint backup --file sprint-backup.txt`
     ```
     nova-sprint backup FAILED: the backup sprint-backup.txt holds secret-shaped text on line 224,335,2260,2366,2853,2854,3337,3403,5658,5679,5732,5853,5875,5897 (the value is not shown); it was removed; find the card or key that holds it and remove it; run: nova-sprint backup --file sprint-backup.txt
     ```
     exit 1 — in this run the trip was my own test briefs (`sha=deadbeef00`
     twice in a RESULT line), so the scan was right to be suspicious.
   - What I expected: the remedy to be actionable in one turn: the failure
     deletes the file, so "line 224" names nothing I can open; naming the key
     or the card whose text tripped it (the dump holds one RESTORE line a key,
     sorted, and the scan already knows the lines) would make the fix one
     paste instead of a hunt through a file that no longer exists.
   - Grade: NEXT.

READ 6/10 — the banner answers what, how and how-to, every verb's `-h`
exits 0 with its flags, effect class and exit codes, and the refusals and
unknown-name answers are one line with a remedy throughout; minus two for the
first run that exits 2 where the page says paste it (finding 1) and the push
proof's escape living only in the spec's seat sections.

USE 6/10 — past the push proof the whole card flow ran first try from the help
alone, landed real commits through a real git origin to DONE, and op replay,
the twin, the tick and every refusal held; minus two for the selftest install
gate red (finding 2), the snapshot that its own drill refuses (finding 4) and
the invisible-add window (finding 3).

urgent=2 next=5
