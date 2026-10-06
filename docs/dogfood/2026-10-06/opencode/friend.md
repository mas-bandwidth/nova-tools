# nova-friend, a cold dogfood run (2026-10-06)

Run by an opencode friend who had never used the tool, reading nothing but
`nova-friend -h`, `nova-friend help`, each verb's `-h`, and
docs/SPEC-FRIEND.md, then using every verb at least once with its real flags
against a scratch directory in the job's temp folder (friends `bob` and
coordinator `ada`, the help's own example names). No redis-server was started
(machine rule), so every bus verb was exercised against an address with
nothing listening, which is itself one of the refusals a stranger meets.
Binary built at 0760eac79c771622c8d16e807b89373f0dcd7a10
(sprint/dogfood-opencode-friend-b.w1.g1.e15), version line
`nova-friend v1.0.1-0.20261006184847-0760eac79c77 darwin/amd64 go1.26.6`.

Verbs used: run, install, uninstall, check, host, ping, ping-install,
ping-uninstall, pong, wait-pong, status, refuse-go, resume, serve, version,
help. Not done: a real `install` (writes the launchd agent under
~/Library/LaunchAgents and loads it — outside the job directory and it starts
a daemon), a real `host` (starts a tmux server), real bus sends against a
store, the one-shot lanes and any real delivery into a harness session, and
`wait-pong`'s success path. `ps`/`pgrep` are refused inside this sandbox, so
no process-table observation was possible; the one `run` left running past its
probe was SIGTERM'd (its designed stop). `$J` below is the job directory.

## Findings

1. command: `nova-friend run --as bob --harness opencode --dir $J/scratch/bob --dry-run`
   printed (1 line): `RUN FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` (exit 1)
   expected: what run -h promises: `--dry-run checks the flags and the harness and prints the daemon it would run (RUN DRY-RUN as= harness= dir= state= redis=): no store is opened and nothing is written` — the dry run is the only store-free probe of the daemon, and it cannot run.
   grade: URGENT

2. command: `nova-friend ping --as ada --to bob --nonce abc123 --dry-run`
   printed (1 line): `PING FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` (exit 1, before any store dial)
   expected: ping -h: `--dry-run checks the PING as send checks it and sends nothing.` `ping --wake --to-friends --dry-run` fails the same way.
   grade: URGENT

3. command: `nova-friend pong --as bob --nonce abc123 --to ada --redis 127.0.0.1:1 --dry-run`
   printed (1 line): `PONG FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` (exit 1)
   expected: pong -h: `--dry-run checks the note as send checks it and prints the line it would send; nothing is sent and no pong file is written.`
   grade: URGENT

4. command: `nova-friend resume --as bob --dir $J/scratch/caseb --state-dir $J/scratch/caseb/.nova-friend --dry-run`
   printed (1 line): `RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` (exit 1)
   expected: `RESUME OK cleared=none dry_run=true` — with a pause marker present the same flag works (`RESUME OK cleared=would pause=... dry_run=true`, writes nothing), so only the no-pause path, the first one a stranger tries, fails; nothing was written, so `it may have written` is false, and the line carries no remedy and an internal name.
   grade: URGENT

5. command: `nova-friend run --as bob --harness claude --dir $J/scratch/bob --redis 127.0.0.1:1`
   printed (first 3 lines): `RUN 2026-10-06T19:00:04Z store: redis at 127.0.0.1:1 as the default user, no password: unreachable: dial tcp 127.0.0.1:1: connect: connection refused; next: start the store or correct the address, which was given to this tool; opening again in 1s` / same with `in 2s` / same with `in 4s`
   expected: run -h says a harness with no deliver command `(claude, the surveyed ones) is refused at once, exit 2, the adapter card its remedy`, and every verb's help quotes exit 2 = `could not run (a flag, an input, a store or a server that did not answer)`. Observed: still running after 10 s (stopped only by my SIGTERM), the retry loop never exits, and the claude refusal is never reached behind the store retry — same for `--harness opencode`.
   grade: URGENT

6. command: `nova-friend install --as bob --harness opencode --dir ./bob --dry-run` (verbatim from the banner's `example:` block; likewise `nova-friend ping --as ada --to bob --nonce abc123`, the pong and wait-pong example lines)
   printed (1 line): `INSTALL REFUSED: --redis is required; it wants the bus store's Redis address, host:port (or NOVA_BUS_REDIS), written into the agent; refusing to guess; run: nova-friend help` (exit 2)
   expected: the example block is the first run; its lines run as printed, exit 0 or 1 — a `NAME=value` setup line naming NOVA_BUS_REDIS may stand among them. On a machine with the env unset, 4 of the 7 example lines exit 2, while the usage lines mark `[--redis <addr>]` optional.
   grade: URGENT

7. command: `nova-friend check --as ada bob zed --since 1h`
   printed (first 3 lines): `CHECK DAEMON friend=bob agent=none pid=- status=none connection=- challenge=- pong_age=- presence=down seen_age=-` / `CHECK HARNESS friend=bob harness=unknown route=push last=- last_exit=- failed_of_last20=0 deferred=0 broken=- reason=-` / `CHECK BUS friend=bob real_since=0 last_real=-`
   expected: friends are the positional arguments (bob, zed) and `--since 1h` sets the window, exactly as the usage line `check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--json]` interleaves them. Observed: after the first friend, every flag and its value becomes a friend — `CHECK DAEMON friend=--since ...` and `CHECK DAEMON friend=1h ...` follow, and the summary says `friends=4`; the window passed was silently dropped.
   grade: URGENT

8. command: `nova-friend check --as ada bob --shown $J/scratch/shown.json --since 1h` (shown.json holds `{"bob":{"state":"up","working":2}}`)
   printed (first 3 lines): `CHECK DAEMON friend=bob agent=none pid=- ...` / `CHECK HARNESS friend=bob ...` / `CHECK BUS friend=bob ...`; the verdict line reads `CHECK VERDICT friend=bob verdict=down shown=- why="down by presence"`, and `--shown`, the file path, `--since` and `1h` are checked as friends
   expected: check -h: when the shown file says up or working and the verdict is not ok, the verdict stays and the why leads with `untrue: shown <state>/<working>, ` — bob should read untrue with shown=up/2. The file was never read.
   grade: URGENT

9. command: `nova-friend check --as ada bob --json`
   printed (first 3 lines): the lines rendering, `CHECK DAEMON friend=bob ...` / `CHECK HARNESS friend=bob ...` / `CHECK BUS friend=bob ...`, plus a `CHECK VERDICT friend=--json ...` row; no JSON anywhere
   expected: the banner: `Every verb but run, serve takes --json: the same result as one JSON object on stdout.` `--json` before the friend arguments works (verified); after them it is parsed as a friend — the same parser as findings 7 and 8.
   grade: URGENT

10. command: `nova-friend screen -h`
    printed (1 line): `FRIEND REFUSED: "screen" is no verb and no file; the verbs are run, install, uninstall, check, host, ping, ping-install, ping-uninstall, pong, wait-pong, status, refuse-go, resume, serve, version, and a file is given by its path (./screen); run: nova-friend help` (exit 2)
    expected: docs/SPEC-FRIEND.md (Hosted in tmux, The screen) says `the last screen of a hosted friend is the pane's capture, the verb nova-friend screen`; the tool's own docs page should name only verbs the binary has, or the verb should exist.
    grade: NEXT

11. command: `nova-friend ping-install --as ada --redis 127.0.0.1:1 --dry-run`
    printed (1 line, then PLAN lines): `PING-INSTALL OK label=com.nova.friend-wake-ping-ada plist=... dry_run=true` (exit 0)
    expected: the usage line `nova-friend ping-install --as <coordinator> --every <d> [...]` leaves `--every` unbracketed and its flag help names no default, so the verb should refuse without it, naming what it wants. (The dry-run plan also never shows the command the agent would run, which is the core of what a dry run promises to show.)
    grade: NEXT

12. command: `nova-friend uninstall --as bob` (no agent installed)
    printed (all of it, 2 lines): `UNINSTALL OK label=com.nova.friend-bob plist=.../data/Library/LaunchAgents/com.nova.friend-bob.plist` / `UNINSTALL RAN command="launchctl bootout gui/501/com.nova.friend-bob"` (exit 0)
    expected: nothing fails silently — launchctl's own error for a label that is not loaded is never shown, and the plan's second step (`rm ...plist`) is never said. `ping-uninstall --as ada` behaves the same.
    grade: NEXT

13. command: `nova-friend refuse-go --name make`
    printed (1 line): `REFUSE-GO REFUSED: make is refused: no go command runs on this machine, it is not a build machine; run: rsync the job's clone to a bench, then ssh <bench> 'cd <clone> && make ...'; run: nova-friend help` (exit 2)
    expected: the usage line and flag help say `--name go|gofmt` (`the command that was run: go or gofmt`); any other name should be a flag refusal naming the two it wants, not a go refusal spoken for `make`.
    grade: NEXT

14. command: `nova-friend check --as ada --json`
    printed (1 line): `{"friends":null,"summary":{"friends":0,"ok":0,"broken":0,"deaf":0,"silent":0,"down":0,"untrue":0}}` (exit 0)
    expected: `friends` renders as `[]` when empty, so a consumer's `.friends[]` does not break on null.
    grade: NEXT

15. command: `nova-friend run -h`
    printed: two dense prose paragraphs (deferrals, broken sessions, limits, lanes, walls, pricing — roughly two thousand words) before the usage line and the flags list
    expected: a verb's help a stranger can scan in one screen — the usage line, a short what/how (the banner already carries it), then the flags; the long behaviour belongs on the docs page, which the help can name.
    grade: NEXT

READ 7/10 — The banner answers what/how/how-to-use, every verb's -h describes each flag with what it wants and quotes the exit table, and docs/SPEC-FRIEND.md is precise and dated; but the banner's own example block fails as printed and `run -h` buries its flags under a two-thousand-word prose wall, so a cold read stalls twice.

USE 6/10 — Every refusal met said what it wanted with a one-turn remedy, and the dry runs that work (host, install, check, uninstall) are honest plans that write nothing; but four verbs fail their advertised `--dry-run`, `check` corrupts its own arguments into friend names, and `run` hangs forever on a store outage — three of them in a stranger's first hour.

urgent=9 next=6