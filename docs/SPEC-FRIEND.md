# SPEC-FRIEND: what a friend runs to be part of the team

The tool is `nova-friend` (`cmd/nova-friend`); the rules are `pkg/friend`; the
models are `tla/Friend.tla` (its lanes), `tla/FriendLane.tla`,
`tla/FriendStage.tla`, and `tla/Reach.tla`.

## Presence

A friend's presence and liveness live in the hook record in the bus Redis
(`hook:rec:<name>`, [SPEC-HOOK.md](SPEC-HOOK.md); `tla/Hook.tla`,
`tla/HookBeat.tla`), never in a daemon beat, process table check, or ping/pong
file:

- `nova-hook hook --as <friend> --role friend --harness <h>` in the friend's
  session holds her `bus` push; `nova-friend run --as <friend>`, while it runs
  her lanes, holds her `work` lease (`hook.Holder`).
- `nova-hook serve` challenges every hooked friend once a minute with a bus
  message (`HOOK CHALLENGE nonce=<n>`); the session's `nova-hook hook` delivers
  it into the session (pkg/harness/notice, one shim per harness, the one
  delivery path), and the model answers inside its turn with
  `nova-hook prove --as <friend> --nonce <n>`, proving both `bus` and `work`.
- The daemon reads nothing off the friend's bus stream and delivers nothing
  into her session: a message to her reaches her through `nova-hook hook`.
- No answer for more than `nova-hook serve --down-after` (5m) unhooks the friend at its next missed
  challenge (`no answer in 5m0s (<n> challenges missed)`) and
  send one coalesced `judgment` (`FRIEND DOWN <friend>: <reason>`) to the
  coordinator.
- While the hook gate says no (`hook.Status.Gate`), her lanes take nothing and
  report nothing, her reads begin nothing, and `nova-bus send` refuses
  deliveries to her with the reason and one deduplicated judgment to the
  coordinator.
- `nova-hook friends [--json]` and `nova-hook status --as <friend>` read
  presence directly from the hook record. Retired and removed (v1.3.5; the
  class rule `internal/ci/retired_presence_class_test.go` refuses their
  return, its table `internal/ci/testdata/retired-presence.txt` naming each
  identifier, verb and flag): the coordinator's one-second keepalive loop, the
  coordinator's ping, the wake ping loop and its launchd agent, the pong wait,
  the hand beat, the daemon's session-check presence and its `presence.json`,
  `pong.json` and `bus2:push`; and (v1.3.6) the daemon's own session delivery
  (the batch turn and its envelope, the present, the dealt-brief and idle-wake
  turns, the notification receiver, messages riding with a card), its answer
  to a `PING` and the wake turn, the limit's wake after a reset (a limit now
  lapses at its reset), and the harness process check. `nova-friend pong` stays
  as the answer to a `nova-friend reach` step and to the delivery check
  (`check --harness`). `nova-friend status` and `check` read the hook record:
  its state, since when, the model's last answer, and the challenges missed.

## The local child load gate

A `nova-swarm member` may configure the raw one-minute host-load thresholds
`--max-load` and `--warn-load` (`pkg/hostload`). Immediately before creating a
local child, it reads the load through `hostload.Source`: above `--max-load` it
refuses the launch naming the measured load and bound; at or above `--warn-load`
through `--max-load` it warns with both values and starts. Zero disables the
corresponding check (both default to zero), and an unavailable load reading
leaves admission unchanged.

## The data

- **The bus**: the friend's stream `bus2:to:<friend>` ([SPEC-BUS.md](SPEC-BUS.md))
  and hook record `hook:rec:<friend>` ([SPEC-HOOK.md](SPEC-HOOK.md)). A message
  remains pending on the stream until the session's turn exits 0 (`ClaimAfter`
  redelivers after 15 minutes if a daemon dies mid-turn).
- **The state directory**: `<dir>/.nova-friend` under the friend's working
  directory unless `--state-dir` names another (`~/.nova-friend/<friend>` when
  `<dir>` sits on a removable volume that refuses background writes). It holds
  `status.json`, `deliver.log`, `watch.json`, and `host.json`. Lane state remains
  in memory; the sprint server owns the lane records.
  `inbox/QUEUE.json` under `<dir>` holds queued, working, and done tasks.
- **The launchd agent**: `com.nova.friend-<friend>` (`RunAtLoad`, `KeepAlive`,
  5 s throttle), with its log under the home directory. When the source binary
  sits under `/Volumes`, `install` copies it to `~/.nova-friend/bin/nova-friend`
  before writing the plist. After `bootout`, `install` polls
  `launchctl print gui/<uid>/<label>` every 250 ms (`ReleasePoll`, up to
  `ExitTimeout`) until launchd releases the label before bootstrapping.

## The loop (`pkg/friend/daemon.go`)

- **No session delivery**: the daemon reads nothing off the friend's bus stream
  and delivers nothing into her session. A message to her, a dealt card's notice
  (friend sync's bus message) and the hook's challenge all reach her session
  through her session's `nova-hook hook --harness <h>` (pkg/harness/notice).
  The daemon runs her one-shot lanes (`pkg/friend/lanes.go`, `tla/FriendLane.tla`),
  each handed one card a turn with nothing riding along, and serves her reader row.
- **Silent-turn stop and refusals** (`pkg/friend/limit.go`,
  `pkg/friend/limits.go`, `cmd/nova-friend/limit.go`, `tla/Friend.tla`):
  - A lane turn with no stdout/stderr for `--silent-stop` (20 m) has its
    process group signalled (`SIGTERM` then `SIGKILL`).
  - A terminal provider refusal repeated `--broken-after` (3) turns in a row
    marks `session=broken` and notifies the coordinator once; two identical
    provider refusals on a lane mark `lane-fault` (`LaneFaultWatch`, cleared by
    `nova-friend lanes --as <friend> --clear-faults`).
  - Usage and credit limits (`ParseLimit` over `pkg/friend/testdata/limits.tsv`,
    `ReadLimit` on Claude `rate_limit_event`, and `resetOfText` in the stated
    timezone) hold the friend down in `Limits` until the reset time (`--limit-rest`
    1 h default when no reset time is stated, raising one `LimitUnreadText`
    judgment); the limit lapses at the reset, and nothing is pushed into the
    session to wake it.
- **Harness adapters** (the lanes, `nova-friend check --harness`, and `nova-friend reach`'s push step):
  - `opencode`: runs `opencode run --session <id> <text>` in `<dir>` without a
    `--dir` flag, after verifying `--session` and `--model` in `opencode run --help`.
  - `codex`: session delivery still requires an explicit watched-folder route
    (`--harness codex --session <id> --adapter folder --delivery-dir <dir>`),
    writing `<payload>.meta.json` and `FRIEND-*.md`. Implicit queue or
    `exec resume` delivery routes are refused before any side effect. In
    one-shot mode, the separate card runner starts a real `codex exec --json`
    model process for each lane card under the existing `nova-friend wall` and uses the
    account's `config_dir`, `CODEX_HOME` or `~/.codex` as its Codex home. The
    server row's work or read tier selects exactly one enabled Codex model
    from the applied `nova-config` subscription-backed routes exposed by
    `nova-sprint routes --json`, respecting that tier's applied route array
    (an empty array admits every enabled route). A sprint view without the
    applied arrays refuses intake; no adapter model table or account default
    chooses a model. A missing or ambiguous route starts no model or failed
    card attempt; the lane waits for corrected configuration. Before taking a card, the runner
    checks `codex login status` inside the same wall and requires a ChatGPT
    login. Both the proof and model launch clear API-key environment variables
    and force the ChatGPT login method, so an API-key account holds intake.
    A configured model must also be available to the ChatGPT-account Codex
    CLI. The runner refuses to start
    without a lane context and its `nova-friend` wall. Inside that outer OS
    sandbox, it uses Codex's externally sandboxed CLI mode and
    `--ignore-user-config`: a nested Codex Seatbelt cannot inspect the wall's
    denied coordinator root, so it cannot write the card outbox. The outer wall
    still limits checkout, outbox, account directory, network and denied roots;
    a failed wall setup starts no model. This is not a desktop session
    delivery route or an idle-wake claim. `nova-hook` owns bus and challenge notices;
    the lane loop owns refill and report collection. If the same friend is the
    sprint coordinator, the daemon joins her existing coordinator hook epoch
    and holds the optional work lease; it never changes her bus+sprint role or
    starts a second bus reader. A different existing hook role still refuses.
  - `antigravity`: delivers into the conversation mailbox via
    `agentapi send-message --title=nova-friend <conversation> <text>`, tracks
    deliveries and `read.json` receipts in `antigravity-ledger.json`, waits when
    deliveries sit unread past `AntigravityReadBound` (5 m), and follows the
    live reader conversation after `AntigravityStopped` (3 unread) with a 10 m
    switch hold (`AntigravitySwitchHold`).
  - `grok`: finds the open TUI in `~/.grok/active_sessions.json` and an active
    `tail -n 0 -F <file>.wake` child, appending one single-line event per turn.
  - `tmux` (`pkg/friend/adapter_tmux.go`): `nova-friend host` starts detached
    tmux session `friend-<me>` and records `host.json`; delivery captures the
    pane, waits for the idle prompt pattern, sends literal keys (`send-keys -l`),
    and verifies the prompt disappears.
  - `claude`: one-shot card runner (`claude -p`) and session wake file
    (`<state>/<me>.wake`, read by `nova-bus wait --wake-file`).
  - `dsh` and `gemini`: headless session resume; unmeasured harnesses are
    `Stub` (`pkg/friend/adapter_refused.go`) and refused by `install` and `run`.

## The daemon writes and stages every card she holds (`pkg/friend/inbox.go`, `pkg/friend/stage.go`)

- **Inbox reconcile** (`pkg/friend/inbox.go`, `pkg/friend/rework.go`): asks the
  sprint server for her held cards (`friend cards <friend> --json`, falling back
  to `GET /api/view/worker?as=<friend>` every `ViewEvery` = 15 s if refused),
  writes missing `inbox/<job>/BRIEF.md` files (`NoReplace`), opens reworked
  briefs with `THE ONE THING LEFT: <fix>` (`ReworkedBrief`), and moves briefs of
  cards that left her row to `inbox/retired/<job>`.
- **Job staging and pruning** (`pkg/friend/stage.go`, `tla/FriendStage.tla`,
  `pkg/friend/tla/JobWorktrees.tla`): for each held work card with a `BRIEF.md`
  and no `jobs/<job>/JOB.md`, maintains one bare mirror per repository at
  `mirrors/<owner>/<name>.git`, checks out worktree `jobs/<job>/repo` on the
  card's branch at `BASE`, and writes `jobs/<job>/JOB.md`
  ([SPEC-CARD-CONTRACT.md](SPEC-CARD-CONTRACT.md)). Unreachable repositories or
  missing base commits raise one deduplicated judgment to the coordinator.
  Finished worktrees beyond `FinishedJobsKept` (8) are pruned at most
  `PrunePerPass` (4) per loop while preserving their branches in the mirror.

## One-shot lanes (`pkg/friend/lanes.go`)

In `one-shot` mode (`nova-config friend set <f> --mode one-shot --width <n>`),
the daemon runs `width` concurrent lanes (`ParseLaneHold` on `LANE` lines):

- **One live lane per card** (`pkg/friend/one_lane.go`,
  `pkg/friend/tla/OneLane.tla`): claims `jobs/<job>/LANE` by exclusive hard link,
  renews every 30 s (`LaneMarkStale` = 2m30s), refuses a second lane on the same
  card, and cancels a lane whose card finished elsewhere or left her row.
- **Claude, Codex and OpenCode lanes** (`pkg/friend/adapter_claude_lanes.go`,
  `pkg/friend/adapter_codex_lanes.go`, `pkg/friend/adapter_opencode_lanes.go`,
  `pkg/friend/lane_parity.go`): run in
  the staged job directory with all paths absolute (`LaneJobOf`, `WithLaneDir`)
  and the brief inline (a one-shot process receives the brief as its prompt),
  rescuing stray relative reports (`RescueStray`). A
  zero-exit run that writes neither `REPORT.md` nor `RESULT.md` is a
  `harness-fault: no report` (retried without advancing `CardTurns`; three alike
  in 10 m pause the lanes for 15 m).
- **Caps, pacing, and pause** (`pkg/friend/lane_cap.go`,
  `pkg/friend/tokencap.go`, `pkg/friend/spendcap.go`, `pkg/friend/pacing.go`,
  `pkg/friend/tla/LanePause.tla`):
  - Wall-time cap per tier (`DefaultLaneCaps`: flash 15 m, pro 45 m, heavy 90 m,
    frontier 150 m), writing a `Verdict: HOLD` report with `CappedWords` and the
    last 40 output lines.
  - Claude's per-card token cap (`row_token_cap`, default `6000000`, `0`
    uncapped) is summed across input, cache, output, and reasoning tokens.
    The Codex runner enforces the lane's tier wall-time cap, but does not yet
    enforce a stream-based token cap; its Codex usage is not included in the
    Claude spend counter.
  - Daily/provider spend cap (`pkg/friend/spendcap.go`), subscription pacing
    (`pkg/friend/pacing.go`), rate-limit exponential backoff, and provider
    failure pause (`PAUSED` in the state directory, cleared only by a person via
    `nova-friend resume`).
  - `go`/`gofmt` refusal shims (`--refuse-go`, `GoShims`) on the lane child's
    `PATH`.
- **Lane end and outbox collect** (`pkg/friend/lane_end.go`,
  `pkg/friend/outbox.go`, `pkg/friend/tla/LaneEnd.tla`,
  `pkg/friend/tla/OutboxFinish.tla`, `pkg/friend/tla/Collect.tla`): every ended
  lane or `outbox/<job>/REPORT.md` on her row sends `finish --as friend.<name>`
  to the sprint server (`LAND` only when `Head` is `origin`'s tip of the card's
  branch and addresses `THE ONE THING LEFT:` on reworked briefs; dead lanes from
  `runner.log` and orphaned `started` marks on daemon startup are finished
  `--failed`).
- **Reader slots** (`pkg/friend/read_lanes.go`): polls `queue --as reader-<friend> --json`
  every `ReadAskEvery` (10 s) and runs up to `row_read_slots` (`DefaultReadSlots` = 2)
  concurrent reads in `<dir>/reads/<card>/` separate from work card lanes.
- **Wall profile and acceptance** (`pkg/sandbox/profile.go`,
  [SPEC-SANDBOX.md](SPEC-SANDBOX.md), `cmd/nova-friend/acceptance.go`): every
  lane child runs inside `nova-friend wall --profile <p> --dir <dir> --deny <self>...`.
  `nova-friend acceptance` runs the harness binary's `--version` inside the
  exact `laneWall` after a host reinstall and writes `RESULT.md` without calling
  a model.

## Harness settings (`pkg/friend/settings.go`)

`nova-friend install --harness <h>` writes each harness's required settings
(`$CODEX_HOME/config.toml` `writable_roots`, DSH `standard` preset, Grok wake
file, Claude private `--config-dir`, OpenCode `<dir>/opencode.json`
`external_directory` and `model`), refusing any symlink (`ErrNotRealDir`, exit
2). `nova-friend check --settings` verifies the files against the same list and
reports `CHECK OK` or `CHECK DRIFT` (exit 1).

## Watch (`cmd/nova-friend watch`; `pkg/friend/state.go`)

`nova-friend watch --as <coordinator> [--timeout <d>] [--state-dir <d>] [--redis <addr>] [--json]`
blocks on the coordinator's bus stream (`bus.WaitPick`), `event:` bus messages,
and `<state-dir>/<me>.wake` from the atomic cursor file `<state-dir>/watch.json`,
printing up to five `WATCH MESSAGE`, `WATCH EVENT`, or `WATCH WAKE` lines and
`WATCH OK after=<cursor>` (exit 0), or `WATCH NONE` on timeout (exit 1).

## Reach (`cmd/nova-friend/reach.go`, `tla/Reach.tla`)

`nova-friend reach --as <coordinator> --to <friend> [--from bus|push|window] [--step-timeout <d>] [--dry-run] [--json]`
climbs the escalation ladder (`bus` message, `push` turn via the harness, `window`
keystrokes via tmux or macOS Accessibility `AXIsProcessTrusted`) and stops at the
first proof from the friend (`REACH PROOF` and `REACH OK`, exit 0; `REACH FAILED`
after all steps, exit 1).

## The pong (`pkg/friend/machine.go`, `schemas/pong`, `pkg/pongwire`)

A pong is a session's answer to a reach step or a delivery check. It is read
in both forms through the one strict read, `pkg/schemaread`, by
`friend.PongReader`: the legacy line `PongLine` writes, `pong <nonce> queue=<n> working=<n> width=<n>` (the friend and the send time are the bus
message's from and time), or the table pong `schemas/pong` defines (the
generated `pkg/pongwire` Pong) as its binary wire, padded standard base64 of
the wire, or its JSON text. Both read to the same `friend.Pong`. A message is
a pong when its subject is `pong` (matched without case) or its body opens
with `pong ` (`friend.IsPong`), and a pong is never an ordinary message.

The legacy line is read strictly: single spaces, the three counts in that
order, plain decimals, nothing after. A pong of either form that is damaged,
clamped (a count past its schema bound, a nonce past 32 bytes) or incomplete
(no friend, nonce, count or send time) is refused by the shared names
(`malformed`, `clamped`, `kind_mismatch`, `unknown_form`, `wire_damaged`,
`absent_required (<field>)`), as `PONG REFUSED: <reason>; run: nova-friend pong -h`, and is never read as no pong. Each read is counted once under its
form (`binary`, `base64`, `json`, `legacy`) and each refusal under its reason;
`reach` prints a refusal and the counters as notes (`PONG READS ...`).

The writer has switched (C30): readers changed first, fleet-wide, with the
per-form counter as the evidence, and `nova-friend pong` now writes the table
pong as the padded standard base64 of its binary wire (`PongReader.WriteTable`
in `schemaread.FormBase64`; a bus body is a text transport). A pong past the
table's bounds is refused at exit 2 with one `PONG REFUSED` line and nothing
is sent; the writer never falls back to `PongLine`. `PongLine` is kept for the
legacy line an old writer still sends during the switch, which `PongReader`
reads and counts under `legacy`. `ParsePong`, the reader before this one,
reads no table pong as a pong. The delivery
check (`check --harness`, `pkg/friend/conformance.go`) reads its pong with
`PongReader.Message` as `reach` does, and names a refused pong in its
act-stage failure.

A bus body keeps every byte but the one framing newline the writer adds
(`nova-friend pong` sends the table pong and `\n`): a pong with any other
whitespace (a trailing space or tab, a second newline, a carriage return, a
leading space or tab) is `malformed`, never normalised into a good pong.

## Coordinator notice retries

Each daemon fault detector emits one message on its episode transition. The
bounded sender (`pkg/friend/notices.go`, `tla/FriendNotices.tla`) retains up to
256 pending messages and retries with the same bus token until a receipt arrives.
A lost reply cannot duplicate a committed message (`tla/BusSendOnce.tla`).
A missing coordinator delays delivery; each retry reads the current seat directly
with a bounded lookup, independently of lane-loop status. The first selected recipient remains
fixed across retries. A full queue reports the wait and holds the caller's next notice until delivery
frees space. It retries once per second without holding the queue mutex while
waiting; the independent metadata beat continues. At capacity the daemon may
pause lane-result processing during an outage. Cancellation releases the caller
and explicitly reports the unadmitted subject.
The queue is volatile. Shutdown reports its unsent count; a restart cannot
recover those pending messages from the daemon's memory.
