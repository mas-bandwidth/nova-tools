# Push, not poll

The inventory of every loop in nova-tools that waits on a clock, and how each one is woken. It was a section of
SPEC-SPRINT.md (section 8) until v1.2.3, when the sprint's docs moved to nova-sprint; the table covers loops in every
nova-tools command, so it stays here. `internal/ci/push_not_poll_class_test.go` holds the tree to it, through
`internal/ci/testdata/push-loops.txt`.

### Push, not poll

(the owner, 2026-10-05: "Take a look across all communications tools between friends, and all tools for coordination, and make sure that they are all PUSH NOTIFICATION not polling!"; "nova-bus is useless if the friend using it is deaf and is not listening to messages sent back.") Every loop that moves a message or a card between the sprint, the bus, a friend's session and a fleet member is a row below, and so is every other loop in the tree that waits on a clock. The audit of 2026-10-06 (card comms-push-audit-b.w1) walked the tree and the coordinator machine's launchd rows; the class test `TestEveryTimerLoopIsNamedInThePushTable` (internal/ci/push_not_poll_class_test.go) holds the tree to the table: each timer loop the scanner finds (a function with a for loop that reaches time.Sleep, time.After, time.Tick, time.NewTicker or time.NewTimer, a clock seam given a duration, or a ticker handed in) is a line of internal/ci/testdata/push-loops.txt naming its row here, a loop with no line fails, a line whose loop is gone fails, a line naming no row fails, and a timer poll with neither a card nor a reason fails. A loop that becomes a push leaves the ledger in the change that makes it one.

The mechanisms: a **blocking read** waits in the store until the thing arrives (XREADGROUP BLOCK, the notes wait); a **delivery into a session** is the harness's adapter putting the text into the AI's session as a turn; a **beat** is a timed send whose absence is the signal (liveness), never a read; a **timer poll** reads on a clock whether or not anything came; **not a channel** is a wait on one machine's own process, lock, file or server, moving nothing between parties; **removed** is a loop that no longer runs.

| loop | direction | mechanism | cadence | card or reason |
|---|---|---|---|---|
| friend bus read | bus to the friend's daemon | timer poll | XREADGROUP BLOCK BeatEvery (1 s) while the session is free; while a turn runs, in one-shot mode or for a passive harness a 0-block read or a peek, then a 1 s Pause | card friend-bus-read-blocks |
| notification delivery recovery | bus to the existing Codex queue | timer poll | one bounded XREADGROUP BLOCK pass of at most 32 entries, then BeatEvery (1 s); enqueue failures back off ten seconds to one minute; ready courtesies share a 30 s configurable coalescing window | the journal owes recovery and queue capacity is observed only when enqueue is due; the Codex queue offers no capacity-change event, so bounded retries preserve full payloads without an unread-input flood (SPEC-FRIEND.md, Notifications) |
| friend delivery | the friend's daemon into her session | delivery into a session | each batch as one turn; the tmux adapter looks at the pane every TmuxPoll (500 ms) until its prompt is free | the pane has no idle event; the wait is for the pane, never for a message |
| friend card reconcile | sprint to the friend's daemon (the cards she holds) | timer poll | Held asked once an InboxEvery (1 s), on the daemon's step | card friend-cards-pushed-on-the-bus |
| friend reader ask | sprint to the friend's reader row (reader-<friend>; a bud's reader is this row) | timer poll | queue --as reader-<friend> once a ReadAskEvery (10 s) | card friend-reads-pushed-on-the-bus |
| friend beat | the friend's daemon to the sprint | beat | BeatEvery (1 s) | liveness is the beat's absence (FriendBeatEvery; down after fifteen without one) |
| member pass | sprint server to a fleet member and a reader (queue, take, report) | timer poll | --every 3 s (at most 5 s); woken at once by its own push's end, a child's exit or SIGTERM | card member-waits-on-the-servers-bus-message |
| member beat | the member to the sprint | beat | --every 3 s, apart from the pass | liveness is the beat's absence; BeatStall stops it when the pass stalls |
| coordinator inbox push, through the server | sprint to the coordinator's inbox (inbox --wait --push) | timer poll | log --json every tickEndPoll (1 s), the inbox read on each new tick-end, at most waitLook (15 s) between reads | card inbox-wait-blocks-on-the-server |
| coordinator inbox push, on the store | sprint to the coordinator's inbox, the store opened in process | blocking read | WaitNotes on the notes stream until a tick-end, at most waitLook (15 s); a backend that cannot block (the test store) is read every 100 ms | - |
| coordinator push into the session | the inbox into the coordinator's session | delivery into a session | each new judgment, one turn; a push check every 10 minutes (the push proof, section 11) | - |
| watch --wake | sprint and bus to the coordinator | timer poll | a look every 20 s (--every) | card watch-wake-retires-into-the-push-loop |
| answer pass | sprint to the coordinator seat's rule answers | timer poll | --every | card answer-runs-on-the-tick-end |
| lane take wait | sprint to the AI asking for a lane | timer poll | the take asked every LaneAskEvery (5 s) | card lane-grant-is-a-bus-message |
| friend sync | the friends table to this machine's friend daemons | timer poll | --every (the unit's) | card friend-sync-on-row-change |
| coordinator ping | the coordinator to every friend's stream, pongs back on its own | beat | PingEvery (1 s); the rows read again every RowsEvery (1 min) | the ping is the transport's liveness probe: its unanswered absence is the signal |
| wake ping | the coordinator into each friend's session | beat | a pass every --every (10 min) | the probe that the session, not the daemon, hears: a deaf session is found by it |
| wake ping pong wait | a friend's session to the coordinator | timer poll | the pong looked for every WaitPongEvery (1 s), up to --within | card pong-wait-blocks-on-the-stream |
| sprint server tick | the store's log to the sprint server | blocking read | WaitLog on the log until a line comes, at most one tick per TickEvery (1 s); a failed tick backs off | - |
| land loop | the merge queue to the lander | timer poll | LandEvery (2 s) | card land-loop-wakes-on-the-queue-note |
| decide loop | sprint to nova-decide's lane | timer poll | DecideEvery (5 s) | card decide-loop-wakes-on-the-tick-end |
| provider balance | each provider's balance API to the sprint | timer poll | BalancePollEvery (10 min) | measured: the providers' balance endpoints offer no push, and one read per ten minutes is what the rests are judged on |
| machinery gc | the sprint server to every machine's scratch (nova-sprint gc, here and through the fleet runner) | timer poll | what is due read every gcLoopEvery (1 min): each machine once an hour (GCEvery), its volume read every GCProbeEvery (5 min), a volume at 80% at most once every GCVolumeRetry (10 min) | measured: a volume's use and a scratch directory's age have no event to push; the hourly pass and the 80% alarm are the rule (section 1, gc) |
| promote | the sprint's landed count to a promotion | timer poll | --every (1 h) | a promotion is a schedule, not an event a party waits on |
| dashboard pull | sprint to the Television and the dashboard page | timer poll | one read per --every (1 s) whoever is looking, pushed on to each page as server-sent events; a keepalive every 15 s | a display pull: one read serves every viewer, and the page itself is pushed to |
| table and where displays | sprint to a terminal (nova-table watch, where --watch) | timer poll | --every (1 s) | a display pull: a person's terminal, no party waits on it |
| samplers and watchdogs | one machine to itself (host load, a card's live sample, the slot cleaner, the wall's process count, a job lease's heartbeat, the idle watch, the store round trip) | not a channel | 1 s to 30 s each | a measurement or a lease of one machine; no message or card moves |
| waits and retries | one machine to itself (a process gone, a lock, a file steady, a server up, a push or open retried, a tick given up stopping) | not a channel | bounded by its caller | a wait on the machine's own state, or a backoff; no message or card moves |
| nova-wake | bus to the coordinator (nova-wake serve) | removed | - | retired with the tool (fleet/retired-tools.txt); its binary is gone, so a unit that still names it restarts for ever: the loops play retires the unit (nova_retire_units) |

The cards this audit cut, each one row of the table turned from a timer poll into a push; each one's DONE-WHEN is its row's mechanism changed here and its ledger line gone:

- **friend-bus-read-blocks**: the daemon's read blocks on XREADGROUP with no timer between reads in every mode: the beat goes on its own ticker (as the member's does), a running turn no longer turns the read into a peek and a Pause (the turn's end is a channel the read's select takes), and a passive harness's daemon blocks the same way. Paths: internal/friend/daemon.go, internal/bus.
- **friend-cards-pushed-on-the-bus** and **friend-reads-pushed-on-the-bus**: the server's deal and ask write one bus message of kind `card` or `read` to the friend's stream, in the step that deals or asks, and the daemon reconciles her inbox and her reader row when it reads one; InboxEvery and ReadAskEvery become a backstop of minutes, not the path.
- **member-waits-on-the-servers-bus-message**: the server writes one bus message to `member:<name>` when it deals to the member or the reader, and the member's pass blocks on that stream (sprintwire carries the wait); --every becomes a backstop of a minute, the beat stays on its own clock.
- **inbox-wait-blocks-on-the-server**: the sprint server serves a wait verb (a long poll of the notes stream, WaitNotes on the server side), so inbox --wait through the server blocks there; tickEndPoll goes.
- **watch-wake-retires-into-the-push-loop**: every kind watch --wake looks for every 20 s is a judgment the push loop already delivers or one the tick writes; the verb becomes a blocking read of the coordinator's bus stream and the tick-end, or is retired.
- **answer-runs-on-the-tick-end**, **decide-loop-wakes-on-the-tick-end** and **land-loop-wakes-on-the-queue-note**: each blocks on the note its work follows (WaitTickEnd, a merge-queue note) in place of its clock.
- **lane-grant-is-a-bus-message**: a grant is a bus message to the asker; lane take --wait blocks on it.
- **friend-sync-on-row-change**: a change of a friend row writes a note friend sync blocks on.
- **pong-wait-blocks-on-the-stream**: waitPong reads the coordinator's stream with XREAD BLOCK for the pong in place of a read every second.

The nova-wake unit: the tool left cmd/ (fleet/retired-tools.txt) and its binary with it, but on 2026-10-06 the coordinator's machine still held its unit, `com.nova.loop.wake-serve-<seat>.plist` in ~/Library/LaunchAgents (`nova-loop wake-serve-<seat> -- ~/.local/bin/nova-wake serve ...`, KeepAlive), not loaded that day; its log's last start, 2026-10-04T03:46Z, says `restarts=4045` and `nova-wake: No such file or directory`. The unit predates the play's mark, so the play does not retire it as its own: it goes by `nova_retire_units: [wake-serve-<seat>]` in that machine's host_vars and one run of fleet/loops.yml, with its loop record, if the store still holds one, deleted first.

