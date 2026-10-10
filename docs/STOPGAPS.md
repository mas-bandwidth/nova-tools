# The stopgap register

Every hand script the fleet runs beside the product. Each was written as a stopgap, and
nova-sprint v1.0.0 (lens simplicity, the release's rule of 2026-10-04: no bash or zsh in anything that
ships, every loop or helper is a Go verb) retires them one by one. This file is the list:
one section per stopgap, its path, its behaviours (one observable behaviour per line, read
from the script itself), the verb or card that replaces it, and a status line.

The class test `internal/ci/stopgaps_class_test.go` parses this file. A `STATUS: live` row
needs nothing more. A `STATUS: retired <date>` row needs a `Tool:` line naming the tool
whose verb table (in `docs/CLI.md`) lists every verb of its `Replacement:` line, and every
behaviour of it must end in `(test: TestX)` naming a test that exists in the tree. The
later cards of this stream retire the rows; the card that wrote this file retired none.

A row's fields: `Path:` (the script, or the launchd label), `Replacement:` (a verb or card,
comma separated, or `none yet`), `Tool:` (only a retired row), `STATUS:`, then `Behaviours:`
and a numbered list. The replacement names are cards of this stream: `claude-oneshot-lanesb`
(headless claude one-shot lanes for the buds; the script headers call the earlier card
claude-oneshot-lanes), `coordinator-wake-verb`, `coordinator-ping-verb`, `friend-token-cap`,
`sprint-dashboard-verb`.

## coordinator-wake

Path: <coordinator-dir>/tmp/buswatch/watch.sh
Replacement: watch
Tool: nova-sprint
STATUS: retired 2026-10-10

Behaviours:
1. At start it records the line count of the wake file, the file count of <coordinator-home>/inbox/sprint-judgments and the time. (test: TestWatchWakeWatchesTheWakeFile)
2. It exits 0 printing `WAKE FILE <time>: <up to 3 new lines>` when a line is appended to the wake file. (test: TestWatchWakeWatchesTheWakeFile)
3. It exits 0 printing `JUDGMENT WAKE <time>: <n> new: <3 newest names>` when the judgments directory holds more files than at start, at most once per 1200 s (judgment-wake.stamp). (test: TestWatchWakeFiresOncePerEventAndNeverLapses)
4. It exits 0 printing `TEN-MINUTE CHECK <time>` once it has run 600 s. (test: TestWatchWakeFiresOncePerEventAndNeverLapses)
5. Every 60 s it reads the sprint with `ns.sh where --json`; when that read is empty it checks nothing else that round. (test: TestWatchWakeFiresOncePerEventAndNeverLapses)
6. It exits 0 printing `MACHINE WAKE <time>` when `ns.sh log --since 2m` holds a "the machine stopped" line not by the coordinator and the stop-asked file is absent (the first sighting wakes it). (test: TestWatchWakeFiresOncePerEventAndNeverLapses)
7. It exits 0 printing `FRIEND WAKE <time>: down: <names>` when a friend other than the two opencode friends, the coordinator and its personal seat has not read up on two checks in a row. (test: TestWatchWakeFiresOncePerEventAndNeverLapses)
8. It exits 0 printing `MERGE WAKE <time>` when cards are merging, the newest `LAND OK|DONE` line of the sprint server's loop log is older than 900 s, stop-asked is absent, and the last merge wake is 600 s or more old (merge-wake.stamp). (test: TestWatchWakeFiresOncePerEventAndNeverLapses)
9. It exits 0 printing `BACKLOG WAKE <time>: <alarms> (<summary>)` when the fleet works under half of the up width, review holds over 40, merging holds over 60, or ready is 0 with cards waiting; only after 120 s alive, with stop-asked absent, at most once per 1800 s (backlog-wake.stamp). (test: TestWatchWakeFiresOncePerEventAndNeverLapses)
10. It blocks up to 5 s per round on the bus stream bus2:to:<coordinator> (redis port 6381) from the end of the stream. (test: TestWatchWakeFiresOncePerEventAndNeverLapses)
11. It exits 0 printing `BUS WAKE <time>: <up to 5 subjects>` for a message to the coordinator from anyone but the coordinator, except pong or PING, `card <id> dealt`, `card <id> finished`, LAND or landed, DONE, `width <n>`, e2e-ok, awake, `RESULT:`, a subject ending ` HOLD`, rule applied, ACK and acknowledged. (test: TestWatchWakeFiresOncePerEventAndNeverLapses)

### simp-retire-buswatch-bc.w1

The append wake of the retired script is the verb's `--wake-file`, and the launch command in
docs/SPRINT-COORDINATOR.md names the file. The wake-file baseline is initialized apart from the
whole state's seed, so a state seeded before the flag was enabled starts at the file's end
rather than waking on the lines already there (`TestWatchWakeSeededOldState`, cmd/nova-sprint).

## bud-card-runner

Path: <buds-dir>/<bud>/runner.zsh (identical copies, one per bud)
Replacement: claude-oneshot-lanesb
STATUS: live

Behaviours:
1. It scans the bud's working/inbox/ every 10 s for a directory holding BRIEF.md, with no runner/<job>.started file and no non-empty outbox/<job>/REPORT.md.
2. It starts each such card as one headless `claude -p --permission-mode bypassPermissions </dev/null` in working/, with the bud's account in CLAUDE_CONFIG_DIR, and records runner/<job>.started, .pid and .tier.
3. It reads the tier from the brief's `RESULT:` line and picks the model: frontier claude-fable-5-1, heavy claude-opus-5-5, pro claude-sonnet-5-5, flash claude-haiku-4-5-20251001, none or other claude-opus-5-5.
4. It runs at most MAX (default 8) cards at once and at most one frontier card at once.
5. Its prompt names the bud, tells the child to read AGENTS.md, do the card in inbox/<job>/BRIEF.md and write REPORT.md and RESULT.md, and carries the bench rule: no go command on the coordinator's machine, sync to the Linux bench (the second bench when the first does not answer), remove the bench directory after.
6. It starts the bud's `nova-friend run --as <bud> --harness claude` daemon (the beat) when none runs and restarts it when it is gone.
7. A usage-limit message in a run's output with no REPORT.md writes runner/PAUSED with the reset epoch (one hour on when the message has none), removes that card's .started so it runs again, and stops the daemon so the bud reads down.
8. While runner/PAUSED exists it starts no card; at the reset time it removes PAUSED, logs RESUME and starts the daemon.
9. It appends START, END, LIMIT, RESUME and DAEMON lines to runner.log with model, exit, wall time and the report's first line.

## bud-reader-runner

Path: <buds-dir>/<bud>/reader.zsh (identical copies, one per bud)
Replacement: claude-oneshot-lanesb
STATUS: live

Behaviours:
1. Every 10 s it reads `ns.sh queue --as reader-<bud> --json`, which is the reader's beat.
2. It begins every card in the `asked` column that it has not started, up to MAX (default 4) at once, with `read --begin <card> --epoch <epoch>`; a refusal not saying OK is logged and the card is left.
3. It writes the read's BRIEF.md, WORKER-REPORT.txt and READ.md under working/reads/<card>/ from the card's packet.
4. READ.md tells the child to clone the repo, detach at the work's head, judge the merge-base diff only, run go vet and go test on the touched packages on the bench, and write RESULT.md with head, branch, verdict (ok or broken), gate, report and a body.
5. It runs the read as one headless `claude -p --permission-mode bypassPermissions </dev/null` on the bud's account, with the model from the read's tier (the bud-card-runner's tier table).
6. A RESULT.md verdict of ok or broken is recorded with `read --ok` or `read --broken`, the report line and up to 3500 bytes of the body as the finding, and a usage line (model, wall time, harness claude-code, account).
7. A read with no verdict is handed back with `read --return` and the reason.
8. A usage-limit message in a read's output writes runner/PAUSED (the card runner's file), stops the bud's nova-friend daemon and returns the read.
9. While runner/PAUSED exists it begins no read.
10. `reader.zsh --once <card>` begins and runs that one read and exits.
11. It appends BEGIN, END, RETURN, LIMIT and BEGIN REFUSED lines to reader.log.

## flash-friend-runner

Path: <friends-dir>/<flash-friend>/runner.zsh
Replacement: claude-oneshot-lanesb, friend-token-cap
STATUS: live

Behaviours:
1. Every 5 s it reads `ns.sh queue --as friend.<flash-friend> --json` and takes the cards on that friend's row.
2. It runs a card that is in the working column, passes the filter, has inbox/<job>/BRIEF.md, no non-empty outbox/<job>/REPORT.md, no jobs/<job> made outside the runner, and no runner/<job>.started.
3. The filter lets only flash-tier cards run; a card of another tier dealt to him and not started is taken back with `friend take` once (runner/<job>.took), with its reason logged as TAKE.
4. It runs each card as one headless `opencode run --model inception/mercury-2.5 --title "<Friend> one-shot <job> <epoch>" </dev/null`, never --session, with go and gofmt refused on the coordinator's machine (runner/bin first in PATH, GOROOT pointing nowhere).
5. Its prompt names the card, tells the child to read the friend's own AGENTS.md, work under jobs/<job>/, run go on the Linux bench (the second bench as the fallback), put 'By: <Friend>' and the model and harness opencode in every commit body, and write REPORT.draft.md and RESULT.md.
6. It runs at most MAX lanes (6, then 8 once the 1-minute load stays under 80 for 600 s, which also sets the store row's width and slots), and holds new lanes to 3 while the load is above 90, with one bus note on the drop.
7. It kills a lane at the brief's deadline plus 15 minutes.
8. It beats for the friend every 5 s with `friend beat <flash-friend>` (working, width, running jobs), except while PAUSED exists.
9. Each card's tokens are read from opencode's database (the run's session and its children) and priced by the store's route row for inception/mercury-2.5, rounded up to the cent, with opencode's own cost and the invoice-effective rate beside it; with no route row, or one with prices it does not apply, the cost reads `unpriced (<why>)`.
10. At the end it publishes REPORT.draft.md as REPORT.md with a `Cost:` line under `Head:`, adds tokens: and cost: lines to RESULT.md, logs END and sends the bus note `<Friend> card <job>: <verdict>` to the coordinator.
11. A lane over TOKEN_CAP (6,000,000 tokens, all kinds) gets a HOLD draft naming the cap and is stopped.
12. A 402, 429, out-of-funds or rate-limit `Error:` line in a run writes runner/PAUSED with the message, stops every lane, and holds the friend down with `friend down`; nothing resumes until a person removes PAUSED and runs `friend up`.
13. `runner.zsh adopt <job> <pid> <title> <t0>` finishes the card of a lane whose runner died.

## security-friend-runner

Path: <friends-dir>/<security-friend>/runner.zsh
Replacement: claude-oneshot-lanesb
STATUS: live

Behaviours:
1. It does what flash-friend-runner does (queue read every 5 s, one-shot `opencode run`, load hold, deadline kill, route-row cost, REPORT.draft.md published as REPORT.md with Cost:, bus note, provider-failure pause, adopt) for the security friend on abliteration-ai/abliterated-model-large-v2.
2. Its filter lets only security cards run: stream starting `security`, or an id starting fp-sec, sec- or security-; any other card is skipped with a SKIP log line and never taken back.
3. It runs one lane (MAX 1, no raise).
4. It does not beat: the security friend's own nova-friend daemon beats and delivers its bus.
5. It has no per-card token cap.
6. Its cost line is the route-row price (its own route row, pro-abliterated-*) with the effective rate of $0.022 per 1M total tokens beside it, with no intro-rate wording.
7. Its prompt reads the friend's own SELF.md and playbook.md and puts 'By: <Friend>' in commits.

## friend-ping

Path: com.nova.loop.friend-ping-<coordinator> (~/Library/LaunchAgents/com.nova.loop.friend-ping-<coordinator>.plist, `zsh -c` while loop)
Replacement: coordinator-ping-verb
STATUS: live

Behaviours:
1. launchd keeps it alive (RunAtLoad, KeepAlive, 5 s throttle) with NOVA_BUS_REDIS=127.0.0.1:6381.
2. Once per round it runs `nova-friend ping --as <coordinator> --to <friend>` for the six friends in alphabetical order, one after the other.
3. It sleeps 600 s between rounds.
4. It appends each ping's output to ~/Library/Logs/friend-ping-<coordinator>.log and launchd's output to friend-ping-<coordinator>-launchd.log.

## friend-beat-loops

Path: com.nova.loop.friend-beat-<name> for five friends (one per friend; ~/Library/LaunchAgents/com.nova.loop.friend-beat-<name>.plist, written by fleet/loops.yml, `zsh -c` while loops)
Replacement: none yet
STATUS: live

Behaviours:
1. Every second it tests `pgrep -qf` for the friend's app: opencode, Antigravity, grok, ChatGPT and DeepSeek, one app per friend.
2. While that process runs it runs `nova-sprint friend beat <name>` with NOVA_SPRINT_SERVER=127.0.0.1:6390 and NOVA_SPRINT_ACTOR=<name>, output discarded.
3. While it does not run it beats nothing, so the friend reads down.
4. launchd keeps it alive (RunAtLoad, KeepAlive, 10 s throttle) and writes its output to ~/nova-bench/loops/friend-beat-<name>.log.

## sprint-dashboard

Path: <bench-dir>/dashboard/server.py (Python)
Replacement: dashboard
Tool: nova-sprint
STATUS: retired 2026-10-07

Behaviours:
1. It serves one page and its files (app.js, the font, OFL.txt, the logo and favicons) on DASHBOARD_HOST:DASHBOARD_PORT (127.0.0.1:7390 by default), every answer no-store, and `/healthz` answers `ok` (test: TestDashboardServesThePageNoStore).
2. One poller thread runs `$SPRINT_CMD where --json` with NOVA_SPRINT_SERVER set, back to back, no sooner than DASHBOARD_MIN_INTERVAL after the last start, with a timeout of DASHBOARD_POLL_TIMEOUT (60 s) (test: TestDashboardServesWhatServerPyServedFromOnePoller).
3. `/api/sprint` returns the cached snapshot with ok, fetchedAt, attemptAt, error, readSeconds, minInterval and the build id, and never runs the command itself (test: TestDashboardServesWhatServerPyServedFromOnePoller).
4. A failed poll (timeout, bad exit, no JSON, no tables) keeps the last good snapshot, sets ok false with a short reason, and never serves the command's stderr (test: TestDashboardServesWhatServerPyServedFromOnePoller).
5. It computes cards landed per hour from the landed count over the last hour of samples, shown once ten minutes of samples exist; a drop in the count restarts the samples (test: TestDashboardThroughput).
6. With DASHBOARD_UPSTREAM set it reads another server's snapshot instead of the sprint, so a check adds no load to the sprint server (test: TestThePullerServesTheUpstreamsCopy).
7. It puts logo.svg into the page title inline in the text colour and serves it as /favicon.svg, else a raster logo at /logo-icon.png, /favicon.png, /logo-tile-192.png, /logo-tile-384.png, /logo.webp and /logo.png (test: TestDashboardServesWhatServerPyServedFromOnePoller).
8. It stamps the page's script link with a build id that changes with the served files, and the page reloads itself when it changes (test: TestDashboardServesTheLogoAndItsBuild).
9. It logs a read-time summary line once a minute and each new failure once (test: TestDashboardLogsAReadSummaryAMinute).

### sprint-dashboard-verb-r-b.w7

`nova-sprint dashboard` holds every behaviour above; each line cites the test that holds
it in the verb, and `docs/CLI.md` names `dashboard` in the nova-sprint verb table, so the
stopgap class test (`internal/ci/stopgaps_class_test.go`) reads the retired row as true.
Two parts of the script are not carried over, by design: it derived its raster logos by
running ffmpeg (a white-key and crop) and sips (192 and 384 px tiles), and the verb runs no
outside program, so the image `--logo` names is served as given at every logo route and is
prepared once by hand; and it filtered the `SECRETS` line of a wrapper script, where the
verb reads the sprint in its own process and has no wrapper. The doctor's `dashboard` check
holds that the dashboard runs as a loop record and answers on loopback (docs/SETUP.md).

### sprint-dashboard-verb-r-b.w8

The doctor's `dashboard` check fails, not passes, when the loop record's `--listen` names
no loopback address: a unit that serves no page on loopback is a fault the check can see,
and its fix line names the loop record and a loopback address to add. The logo routes type
the image by its magic bytes before its file name, so a WebP image named `logo.png` is
served `image/webp`. Tests: `TestDashboardCheck` (internal/doctor) and
`TestDashboardServesWhatServerPyServedFromOnePoller` (internal/sprintdash).

### sprint-dashboard-verb-r-b.w9

The carried dashboard work now merges with the current sprint base: its doctor check
requires a loopback page to answer, and image routes identify raster content from the
bytes before the file extension. Tests: `TestDashboardCheck` (internal/doctor) and
`TestDashboardServesWhatServerPyServedFromOnePoller` (internal/sprintdash).
