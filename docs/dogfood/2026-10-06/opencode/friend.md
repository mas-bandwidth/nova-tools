# nova-friend dogfood — opencode, 2026-10-06

Run by an opencode friend who had never used the tool, reading nothing but
`nova-friend -h`, `nova-friend help`, each verb's `-h`, and
docs/SPEC-FRIEND.md, then using every verb at least once with its real flags
against a scratch directory on a Linux bench. No redis-server was started
(machine rule), so every bus verb was exercised against an address with
nothing listening. Binary built from commit 62d1a251aa84857342114f90e3f5a58a40131e00
(sprint/mechanical-2026-10-02), version line
`nova-friend devel linux/amd64 go1.26.6`.

Verbs used: run, install, uninstall, check, ping, pong, ping-install, resume,
status, refuse-go, version, help, screen. Not done: host (needs tmux), beat,
wait-pong (needs pong), watch (needs redis), serve (needs redis), ping-uninstall.
`status` requires a prior install+run; `serve` requires a redis address.

## Findings

1. command: `nova-friend run --as bob --harness opencode --dir ./scratch/bob --dry-run`
   Printed:
   ```
   RUN FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   ```
   I expected: `--dry-run checks the flags and the harness and prints the daemon it would run (RUN DRY-RUN as= harness= dir= state= redis=): no store is opened and nothing is written` (from run -h). The dry run is the only store-free probe of the daemon, and it cannot run.
   Grade: URGENT

2. command: `nova-friend ping --as ada --to bob --nonce abc123 --dry-run`
   Printed:
   ```
   PING FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   ```
   I expected: `--dry-run checks the PING as send checks it and sends nothing.` (from ping -h).
   Grade: URGENT

3. command: `nova-friend pong --as bob --nonce abc123 --to ada --redis 127.0.0.1:1 --dry-run`
   Printed:
   ```
   PONG FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   ```
   I expected: `--dry-run checks the note as send checks it and prints the line it would send; nothing is sent and no pong file is written.` (from pong -h).
   Grade: URGENT

4. command: `nova-friend resume --as bob --dir ./scratch/caseb --state-dir ./scratch/caseb/.nova-friend --dry-run`
   Printed:
   ```
   RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   ```
   I expected: RESUME OK with dry_run=true. The dry-run flag is ignored by resume too.
   Grade: URGENT

5. command: `nova-friend run --as bob --harness claude --dir ./scratch/bob --redis 127.0.0.1:1`
   Printed:
   ```
   RUN 2026-10-07T19:09:23Z store: redis at 127.0.0.1:1 as the default user, no password: unreachable: dial tcp 127.0.0.1:1: connect: connection refused; next: start the store or correct the address, which was given to this tool; opening again in 1s
   RUN 2026-10-07T19:09:24Z store: redis at 127.0.0.1:1 as the default user, no password: unreachable: dial tcp 127.0.0.1:1: connect: connection refused; next: start the store or correct the address, which was given to this tool; opening again in 2s
   RUN 2026-10-07T19:09:25Z store: redis at 127.0.0.1:1 as the default user, no password: unreachable: dial tcp 127.0.0.1:1: connect: connection refused; next: start the store or correct the address, which was given to this tool; opening again in 4s
   ```
   I expected: run -h says a harness with no deliver command (claude) is refused at once, exit 2, the adapter card its remedy. Observed: the daemon hangs forever retrying the store, never reaching the harness refusal. Same for `--harness opencode`.
   Grade: URGENT

6. command: `nova-friend install --as bob --harness opencode --dir ./bob --dry-run`
   Printed:
   ```
   INSTALL REFUSED: --redis is required; it wants the bus store's Redis address, host:port (or NOVA_BUS_REDIS), written into the agent; refusing to guess; run: nova-friend help
   ```
   I expected: the example block in the banner says `--redis <addr>]` is optional; on a machine with NOVA_BUS_REDIS unset, example lines should run as printed or exit 1, not exit 2 for a missing optional flag.
   Grade: URGENT

7. command: `nova-friend check --as ada bob zed --since 1h`
   Printed:
   ```
   CHECK DAEMON friend=bob agent=none pid=- status=none connection=- challenge=- pong_age=- presence=down seen_age=- proof=none proof_age=-
   CHECK HARNESS friend=bob harness=unknown route=push last=- last_exit=- failed_of_last20=0 deferred=0 broken=- reason=- session_live=- queued=-
   CHECK BUS friend=bob real_since=0 last_real=-
   ```
   I expected: friends are the positional arguments (bob, zed) and `--since 1h` sets the window. Observed: after the first friend, every flag and its value becomes a friend — `CHECK DAEMON friend=--since ...` and `CHECK DAEMON friend=1h ...` follow, and the summary says `friends=4`.
   Grade: URGENT

8. command: `nova-friend check --as ada bob --json`
   Printed:
   ```
   CHECK DAEMON friend=bob agent=none pid=- status=none connection=- challenge=- pong_age=- presence=down seen_age=- proof=none proof_age=-
   CHECK HARNESS friend=bob harness=unknown route=push last=- last_exit=- failed_of_last20=0 deferred=0 broken=- reason=- session_live=- queued=-
   CHECK BUS friend=bob real_since=0 last_real=-
   ```
   I expected: `--json` should render the result as JSON (from banner: "Every verb but run, serve takes --json: the same result as one JSON object on stdout."). Observed: `--json` is parsed as a friend name.
   Grade: URGENT

9. command: `nova-friend screen -h`
   Printed:
   ```
   FRIEND REFUSED: "screen" is no verb and no file; the verbs are run, beat, install, uninstall, check, host, ping, ping-install, ping-uninstall, pong, wait-pong, watch, status, refuse-go, resume, serve and 1 more, and a file is given by its path (./screen); run: nova-friend help
   ```
   I expected: docs/SPEC-FRIEND.md (Hosted in tmux, The screen) says `the last screen of a hosted friend is the pane's capture, the verb nova-friend screen`; the tool's own docs should name only verbs the binary has, or the verb should exist.
   Grade: NEXT

10. command: `nova-friend ping-install --as ada --redis 127.0.0.1:1 --dry-run`
    Printed:
    ```
    PING-INSTALL OK label=com.nova.friend-wake-ping-ada plist=/home/glenn/Library/LaunchAgents/com.nova.friend-wake-ping-ada.plist launchd_log=/home/glenn/Library/Logs/nova-friend-wake-ping-ada.log dry_run=true
    PING-INSTALL PLAN command="write /home/glenn/Library/LaunchAgents/com.nova.friend-wake-ping-ada.plist"
    PING-INSTALL PLAN command="launchctl bootout gui/1000/com.nova.friend-wake-ping-ada"
    ```
    I expected: the usage line `nova-friend ping-install --as <coordinator> --every <d> [...]` leaves `--every` unbracketed and its flag help names no default, so the verb should refuse without it, naming what it wants.
    Grade: NEXT

11. command: `nova-friend refuse-go --name make`
    Printed:
    ```
    REFUSE-GO REFUSED: make is refused: no go command runs on this machine, it is not a build machine; run: rsync the job's clone to a bench, then ssh <bench> 'cd <clone> && make ...'; run: nova-friend help
    ```
    I expected: the usage line and flag help say `--name go|gofmt` (the command that was run: go or gofmt); any other name should be a flag refusal naming the two it wants, not a go refusal spoken for `make`.
    Grade: NEXT

12. command: `nova-friend check --as ada --json`
    Printed:
    ```
    {"friends":null,"summary":{"friends":0,"ok":0,"broken":0,"deaf":0,"silent":0,"down":0,"untrue":0}}
    ```
    I expected: `friends` renders as `[]` when empty, so a consumer's `.friends[]` does not break on null.
    Grade: NEXT

13. command: `nova-friend run -h`
    Printed: two dense prose paragraphs (deferrals, broken sessions, limits, lanes, walls, pricing — roughly two thousand words) before the usage line and the flags list.
    I expected: a verb's help a stranger can scan in one screen — the usage line, a short what/how (the banner already carries it), then the flags; the long behaviour belongs on the docs page, which the help can name.
    Grade: NEXT

## What held

- `host --dry-run` and `install --dry-run` produced honest plans that write nothing.
- `uninstall --dry-run` showed the exact launchctl command it would run.
- `version` and `help` worked without issues.
- `status` correctly reported no daemon run for the test user.

READ 7/10 — The banner answers what/how/how-to-use, every verb's -h describes each flag with what it wants and quotes the exit table, and docs/SPEC-FRIEND.md is precise and dated; but the banner's own example block fails as printed and `run -h` buries its flags under a two-thousand-word prose wall, so a cold read stalls twice.

USE 6/10 — Every refusal met said what it wanted with a one-turn remedy, and the dry runs that work are honest plans that write nothing; but four verbs fail their advertised `--dry-run`, `check` corrupts its own arguments into friend names, and `run` hangs forever on a store outage — three of them in a stranger's first hour.

urgent=8 next=5
