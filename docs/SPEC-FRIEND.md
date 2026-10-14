# SPEC-FRIEND: what a friend runs to be part of the team

The tool is `nova-friend` (`cmd/nova-friend`); the rules are `pkg/friend`; the
models are `tla/Friend.tla`, `tla/FriendEnvelope.tla`, `tla/Delivery.tla`,
`tla/FriendNotifications.tla`, `tla/FriendNotificationsCapacity.tla`,
`tla/FriendStage.tla`, and `tla/Reach.tla`.

## Presence

A friend's presence and liveness live in the hook record in the bus Redis
(`hook:rec:<name>`, [SPEC-HOOK.md](SPEC-HOOK.md); `tla/Hook.tla`,
`tla/HookBeat.tla`), never in a daemon beat, process table check, or ping/pong
file:

- `nova-friend run --as <friend>` holds the friend's `bus` and `work` leases
  (`hook.Holder`) while hooked in as `friend` (`comms` under
  `--notifications-only`).
- `nova-hook serve` challenges every hooked friend once a minute with a bus
  message (`HOOK CHALLENGE nonce=<n>`); the daemon delivers it into the session
  through the harness adapter, and the model answers inside its turn with
  `nova-hook prove --as <friend> --nonce <n>`, proving both `bus` and `work`.
- Two missed challenges in a row unhook the friend (`missed 2 challenges`) and
  send one coalesced `judgment` (`FRIEND DOWN <friend>: <reason>`) to the
  coordinator.
- While the hook gate says no (`hook.Status.Gate`), her lanes take nothing and
  report nothing, her reads begin nothing, and `nova-bus send` refuses
  deliveries to her with the reason and one deduplicated judgment to the
  coordinator.
- `nova-hook friends [--json]` and `nova-hook status --as <friend>` read
  presence directly from the hook record. The legacy `PING`/`pong`/`daemon-pong`
  protocol, `SESSION CHECK`, `presence.json`, `pong.json`, `bus2:push`,
  `nova-friend ping`/`pong`/`wait-pong`/`serve`/`beat`, and `WatchHarness` are
  retired and removed.

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
  `status.json`, `deliver.log`, `lanes.json`, `watch.json`, and `host.json`.
  `inbox/QUEUE.json` under `<dir>` holds queued, working, and done tasks.
- **The launchd agent**: `com.nova.friend-<friend>` (`RunAtLoad`, `KeepAlive`,
  5 s throttle), with its log under the home directory. When the source binary
  sits under `/Volumes`, `install` copies it to `~/.nova-friend/bin/nova-friend`
  before writing the plist. After `bootout`, `install` polls
  `launchctl print gui/<uid>/<label>` every 250 ms (`ReleasePoll`, up to
  `ExitTimeout`) until launchd releases the label before bootstrapping.

## The loop (`pkg/friend/daemon.go`)

- **Envelope delivery** (`tla/FriendEnvelope.tla`): when the session is free,
  pending messages are read off the stream and delivered in one turn (`Envelope`)
  oldest first, capped at `TextLimit` (256 KiB, `BatchBytes`; remaining messages
  are listed under `and <n> more: nova-bus recv --as <me> --all` for the next
  turn). Exit 0 acknowledges every message in the envelope; a third failed
  delivery acknowledges with `given_up=true`; a `Deferred` turn stays in hand and
  retries every `RecheckEvery` (10 s) without counting as a failure.
- **The present comes first** (`pkg/friend/present.go`, `tla/Delivery.tla`): on
  daemon start, a new `--session` id, a delivery after `StaleAfter` (30 m), or a
  self-message with subject `present`, `PlanPresent` drains the backlog into one
  `PRESENT` turn carrying her live queue, the skipped counts (`deals`, `notes`),
  and the newest coordinator note, acknowledging older entries as
  `superseded by the present at <time>`.
- **Sender authority** (`pkg/friend`): only a message from the
  coordinator seat holder (`Daemon.Seat`, cached for `SeatCacheFor` = 10 s) is
  delivered as a plain instruction; every other sender's metadata and body are
  quoted behind `> ` (`Quoted`, `BatchFor`) as data, never instructions.
- **Silent-turn stop and refusals** (`pkg/friend/limit.go`,
  `pkg/friend/limits.go`, `cmd/nova-friend/limit.go`, `tla/Friend.tla`):
  - A batch turn with no stdout/stderr for `--silent-stop` (20 m) has its
    process group signalled (`SIGTERM` then `SIGKILL`).
  - A terminal provider refusal repeated `--broken-after` (3) turns in a row
    marks `session=broken` and notifies the coordinator once; two identical
    provider refusals on a lane mark `lane-fault` (`LaneFaultWatch`, cleared by
    `nova-friend lanes --as <friend> --clear-faults`).
  - `DSHRefusal` (an agent preset refusal or `MISSING_CREDENTIAL`, even on exit
    0) marks `session=broken` immediately and defers messages until a turn
    succeeds.
  - Usage and credit limits (`ParseLimit` over `pkg/friend/testdata/limits.tsv`,
    `ReadLimit` on Claude `rate_limit_event`, and `resetOfText` in the stated
    timezone) hold deliveries in `Limits.Gate` until the reset time (`--limit-rest`
    1 h default when no reset time is stated, raising one `LimitUnreadText`
    judgment), then wake the session.
- **Harness adapters**:
  - `opencode`: runs `opencode run --session <id> <text>` in `<dir>` without a
    `--dir` flag, after verifying `--session` and `--model` in `opencode run --help`.
  - `codex`: requires an explicit watched-folder route
    (`--harness codex --session <id> --adapter folder --delivery-dir <dir>`),
    writing `<payload>.meta.json` and `FRIEND-*.md`. Implicit queue or
    `exec resume` routes and `--notifications-only` are refused before any side
    effect.
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

## Notifications

The transport-independent notification receiver
(`com.nova.friend-notifications-<name>`, state in `<state-dir>/notifications/`,
modeled in `tla/FriendNotifications.tla` and
`tla/FriendNotificationsCapacity.tla`) owns a single recipient group, coalesces
deal notices per friend across 32-entry passes (`--notify-window` 30 s,
withdrawing notices whose cards have all left her row), preserves requests,
blockers, and reports without payload loss in an atomic file-synced journal, and
refuses Codex notification-only deployment.

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
- **Claude and OpenCode lanes** (`pkg/friend/adapter_claude.go`,
  `pkg/friend/adapter_opencode_lanes.go`, `pkg/friend/lane_parity.go`): run in
  the job directory with all paths absolute (`LaneJobOf`, `WithLaneDir`) and the
  brief inline (`CardText`), rescuing stray relative reports (`RescueStray`). A
  zero-exit run that writes neither `REPORT.md` nor `RESULT.md` is a
  `harness-fault: no report` (retried without advancing `CardTurns`; three alike
  in 10 m pause the lanes for 15 m).
- **Caps, pacing, and pause** (`pkg/friend/lane_cap.go`,
  `pkg/friend/tokencap.go`, `pkg/friend/spendcap.go`, `pkg/friend/pacing.go`,
  `pkg/friend/tla/LanePause.tla`):
  - Wall-time cap per tier (`DefaultLaneCaps`: flash 15 m, pro 45 m, heavy 90 m,
    frontier 150 m), writing a `Verdict: HOLD` report with `CappedWords` and the
    last 40 output lines.
  - Per-card token cap (`row_token_cap`, default `6000000`, `0` uncapped) summed
    across input, cache, output, and reasoning tokens.
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
climbs the escalation ladder (`bus` message, `push` turn via the daemon, `window`
keystrokes via tmux or macOS Accessibility `AXIsProcessTrusted`) and stops at the
first proof from the friend (`REACH PROOF` and `REACH OK`, exit 0; `REACH FAILED`
after all steps, exit 1).
