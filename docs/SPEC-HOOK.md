# SPEC-HOOK: an identity is hooked in, or nothing runs

Each tool that gates an acting identity permits its operation only while the required pushes for that
identity's role are live and proven, in one record in the bus Redis. A tool that finds its acting
identity not hooked in refuses, and its refusal is a breadcrumb: the role, which push is not live, who
holds it, and the exact command that hooks the identity in, runnable as printed. `nova-bus send`
also refuses any recipient the hook record shows down, at once, with the reason and one deduplicated
judgment to the coordinator.

## What this checks: liveness, not identity

The hook checks that the pushes of a name are live and answered: a holder renews them, and the
session that receives them proves it reads them by answering the server's challenge from inside its own
turn. It does not check who the caller is until per-identity Redis ACL users are enabled on the bus
store; until then, `--as` is the caller's word and the hook is a liveness and presence gate.

The record and its rules live in `github.com/mas-bandwidth/nova-tools/hook` (`hook/`), modeled by
`tla/Hook.tla` and `tla/HookBeat.tla`, with CLI `nova-hook` (`cmd/nova-hook`).

## Roles and pushes

| role | pushes | who hooks in as it |
|---|---|---|
| `coordinator` | `bus`, `sprint` | the seat: its session's bus stream and the sprint server's pushes |
| `friend` | `bus` | a live friend: the session proves its bus stream; its hook service renews sprint lane leases while proven |
| `comms` | `bus` | a friend on the bus who takes no sprint work |
| `member` | `work` | a machine's member loop |
| `reader` | `work` | a machine's reader loop |

A push is held by the process that receives it:

| push | holder | its proof |
|---|---|---|
| `bus` of a session | `nova-hook hook --as <name> --role <role>`, under the session's Monitor | the session answers `HOOK CHALLENGE nonce=<n>` with `nova-hook prove --as <name> --nonce <n>` |
| optional `work` lease of a friend | the lane daemon, `nova-friend run`, when used | the daemon renews only `work`, never proves or renews `bus`; this optional lease does not decide the friend role's presence |
| `sprint` | the sprint server, `nova-sprint run` | the server holds it for the coordinator while it runs and reaches the bus |
| `work` of a member or reader | its loop, `nova-swarm member` | the loop's own round trip to the sprint server |

The hook service's live friend lane client renews the sprint server's lane leases
by asking what each lane holds. Those server leases are distinct from `hook:live:<name>:work`:
the client holds only the session's `bus` push. A one-shot friend uses the member
loop and identity prompt instead (`docs/SPEC-SWARM.md`); it has no live session presence.

## The key layout

All keys live in the bus Redis (`--redis`, else `NOVA_BUS_REDIS`, else the fleet row's `fleet:bus`).

| key | type | holds |
|---|---|---|
| `hook:rec:<name>` | hash | `role`, `state` (`hooked` or `unhooked`), `epoch`, `armed` and `unhooked` (ms of the store's clock), `by`, `misses`, `reason`, `beat_at` |
| `hook:live:<name>:<push>` | string, TTL `LeaseTTL` | the lease: `{"epoch":<n>,"at":<ms>,"by":"<holder>"}` |
| `hook:proof:<name>:<push>` | string, TTL `ProofTTL` | the proof, the same shape |
| `hook:chal:<name>` | hash, TTL `AnswerBound` | `epoch`, `sum` (SHA-256 of the nonce, never the nonce), `pushes`, `until` |
| `hook:names` | set | every name that ever hooked in |
| `hook:serve` | hash | the heartbeat server's row (`at`, `every`, `down_after`, `by`, `told:*`, `coord:*`, `gen:*`) |
| `hook:judgments` | hash | open presence judgments awaiting `nova-bus send --re <id>` |
| `hook:events` | stream | capped audit stream of unhooks, judgments, answers, and `not-connected` episodes |

Every stamp is the store's clock (`TIME`), never a holder's. Every write is one Lua script that checks
the record's state, role, and epoch in the same step.

## The clocks

| name | value | what it bounds |
|---|---|---|
| `LeaseTTL` | 90 s | a push stays live this long after its holder last renewed it |
| `RenewEvery` | 30 s | a holder renews three times inside one lease |
| `BeatEvery` | 1 min | `nova-hook serve` challenges every hooked session-backed name |
| `DownAfter` | 5 min | `nova-hook serve --down-after`: a name silent longer than this is unhooked at its next miss, and reads down |
| `ChallengeEvery` | 30 min | a standalone `nova-hook hook` loop's own challenge cadence |
| `AnswerBound` | 5 min | a standalone challenge's answer window |
| `ProofTTL` | 40 min | a standalone proof's TTL |

A holder that dies stops renewing, and its lease expires on its own: the gate refuses within `LeaseTTL`.

## The states

A push is `down` (no lease of the record's epoch), `armed` (a lease and no proof), or `proven` (both).
The record is `none` (never hooked), `unhooked`, `expired` (hooked, no push of its role live), `partial`
(some live), `live` (every push live, not every one proven), or `proven` (every push live and proven).
Furthermore, `Served.Down` (`Status.Down`) reads any session-backed name `down` when `hook:serve` has
never run, is more than two intervals old by the store's `TIME`, or when the name's last answer is older
than the server's down-after (`hook:serve` `down_after`, written by every pass; two intervals for a row
without one) (`tla/HookBeat.tla`, `DownAfter`). The gate's one yes is `proven` and not `Down`, with a role
that carries every push the act needs (`hook.Status.Gate`; `tla/Hook.tla`, `GateMeansHooked`).

## The epoch and the clear rule

`nova-hook hook` is the identity's own word (`hook.Store.HookAs`): as the role the record is hooked as,
it joins that epoch; otherwise (no record, an unhooked one, or another role) it starts a new epoch and
clears every lease, proof, and challenge of the old one.

A holder (`hook.Holder.Start`) joins a record hooked as its role and arms a record only where there is
none at all (`hook.ErrOtherRole`, `hook.ErrNotHooked`). `nova-hook unhook` moves the epoch on and clears
the keys (`LiveIsJoined`, `UnhookStands`). A record whose key was lost (store flush) is armed again at
the holder's next beat (`ReArm`).

## The gate in every tool

- `nova-bus`: every verb (`wait`, `send`, `peek`, `recv`, `ack`, `receipts`, `overdue`, `log`, `names`)
  strictly gates `--as` on its own `bus` push before reading or writing. `send` refuses any recipient
  the hook record shows down, at once, with the reason and one deduplicated judgment to the coordinator.
  `names` reads every name's state from the hook record.
- `nova-friend run`: acts as its friend's hook holder for `friend`. While the gate says no, her lanes
  take nothing and report nothing, and her reads begin nothing.
- Coordinator verbs (`nova-hook serve`, `nova-friend watch`, `nova-friend reach`) gate `--as` on the
  `bus` push and role `coordinator` or `comms` (`hook.Want.Roles`).
- `pkg/hookgate` is the one place a `nova-tools` verb meets the gate (`Gate` and `Out`).

A gate refusal is `FAILED` at exit 1 with `reason=unhooked` and the runnable breadcrumb lines on `NOTE`.
An unreadable store is `REFUSED` at exit 2 (fail-closed). `nova-hook` itself and the local setup verbs
that open no bus store (`install`, `uninstall`, `host`, `status`, `resume`, `refuse-go`, `acceptance`,
`wall`) are not gated.

## The breadcrumb

Every line runs as printed:

- With a role known, the remedy is `nova-hook hook --as <name> --role <role>`, and a line per unproven
  push names its holder (the session's `nova-hook hook` for the bus push, `com.nova.friend-<name>` for
  a friend's work push, or the sprint server).
- With no role known, the remedy is `nova-hook status --as <name>`, offering `comms`, `friend`, and
  `coordinator`.

## The heartbeat

`nova-hook serve --as <name>`, hooked in as `coordinator` or `comms`, makes one atomic pass every
`--every` (`BeatEvery`, 1 min) by the store's `TIME` (`tla/HookBeat.tla`):

- **One pass an interval.** `hook:serve` is the once-key: a pass less than nine tenths of an interval
  after the last is skipped, so multiple servers make one pass between them.
- **A challenge to each session-backed name.** Each hooked `coordinator`, `friend`, or `comms` name gets
  a fresh challenge in `hook:chal:<name>` (`until` = now + every) and a bus message with subject
  `HOOK CHALLENGE nonce=<n>`. A friend's challenge names both `bus` and `work`; her session's
  `nova-hook prove --as <name> --nonce <n>` proves both and clears `misses`.
- **Down after silence, not after a count.** A challenge still unanswered at the next pass is a miss
  and increments `misses`. The pass unhooks the name only at a miss when its last answer
  (`answered_at`, or `armed` if later) is older than `--down-after` (`DownAfter`, 5 min) by the store's
  `TIME`, with `reason` `"no answer in 5m0s (<n> challenges missed)"`, and logs to `hook:events`.
  Challenges still go out every `--every`: a harness that cannot answer mid-turn (Antigravity) answers
  between turns and stays up (2026-10-14: one minute is too fast unless every harness can be
  injected into mid-turn). The model is `tla/HookBeat.tla` (`DownAfter`, `NoDownBeforeDownAfter`; the reversed
  witness `MCHookBeatBrokenDownEarly` is the old two-miss rule).
- **Coalesced telling, one write per audience.** Changes in the unhooked set or coordinator are sent
  once per audience (`hook:told:<audience>:<gen>`): one `judgment` to the coordinator (`FRIEND DOWN
  <name>: <reason>`, `FRIEND BACK <name>`) and one message (or `COORDINATOR DOWN` judgment) to the
  other live names. Open judgments in `hook:judgments` are re-pushed once as `OVERDUE` past
  `--judge-within` (2 min). Each pass also runs `bus.ExpireAsks`.
- **Bootstrap and client timers.** A client's standalone `Challenge` (`tla/HookBeat.tla`, `Bootstrap`)
  writes only when no challenge stands and `beat_at` is `0` in the current epoch, so a client never
  overwrites a heartbeat challenge or an epoch the heartbeat already owns. A session's `nova-hook hook`
  loop emits `HOOK STATE state=not-connected` when `hook:serve` has not moved for two intervals on the
  client's clock, and `state=connected` when it resumes. `nova-hook status --as <name>` fails with
  `NO CHALLENGE FOR <age>` (exit 1) when no challenge arrived for two intervals.

## The friend lane client

A friend's supervised hook service (`nova-hook hook --role friend` under `nova-hook.client.<name>`) is also her lane client (`internal/friend`, `cmd/nova-hook/lanes.go`; the card the-friend-lane-is-a-client-with-no-local-state). The sprint server owns every lane's record: the card it holds, its generation, its lease and the finish. Every `LaneEvery` (a third of the server's 10m lane lease, `ServerLaneLease`, never the challenge cadence), and at once on start, the client asks for each lane 1..`--lanes` (default 8; the server's width for her row bounds what is taken; `--lanes 0` asks nothing) with the fleet's lane take, `take --as friend.<name> --lane <n> --json`, at `--sprint-server` (`NOVA_SPRINT_SERVER`, else `127.0.0.1:6390`). An ask renews that lane's lease; one round is bounded to half the hook's own renewal (`hook.RenewEvery`) so a hung sprint server never stalls the hook. The client asks only while the friend's record reads proven: a model that stops answering its challenges stops the asks, and the server returns its cards when the lease runs out. New holds make one bus message to her own name, subject `SPRINT CARDS n=<N> as=friend.<name>`, which the loop folds into the one pending notice like any other: "N cards taken; briefs at <paths>; work each in a child agent to width", with the server's finish and progress verbs for every card she holds. The briefs are cached under `~/.local/state/nova-hook/<name>/briefs/<job>/BRIEF.md`, a cache rebuilt from the server's answers: a client started in an empty directory resumes from the server alone and tells the model again what she holds. The model is `tla/FriendLaneClient.tla`.

## The verbs

- `nova-hook hook --as <name> --role <role> [--once] [--dry-run]`: arms or joins the record, renews the
  `bus` lease, prints stream arrivals as `HOOK PUSH`, challenges as `HOOK CHALLENGE nonce=<n>`, and
  state transitions as `HOOK STATE`.
- `nova-hook prove --as <name> --nonce <n> [--dry-run]`: answers the current challenge from inside the
  model's turn.
- `nova-hook unhook --as <name> [--dry-run]`: advances the epoch and clears leases, proofs, and
  challenges.
- `nova-hook status [--as <name>]`: checks one record (0 proven, 1 not with breadcrumb) or lists all.
- `nova-hook friends [--json]`: lists every name's presence from the hook record (`up` or `down`,
  `since`, `reason`, last challenge and answer, `misses`, coordinator, and server age).
- `nova-hook serve --as <name> [--every <d>] [--down-after <d>] [--judge-within <d>]`: runs the heartbeat
  pass loop; `--down-after` (default 5m, at least `--every`) is the silence before a name goes down.

## Using the package from another repository

`hook.Store{C: <redis client>}` is the record; `Gate(ctx, hook.Want{Name, Need, Role})` is the gate;
`hook.Holder` holds pushes for a daemon (`Start`, then `Beat` every `RenewEvery`); `hooktest` is the
in-memory test rig over `net.Pipe`.
