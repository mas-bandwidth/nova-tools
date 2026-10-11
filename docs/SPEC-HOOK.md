# SPEC-HOOK: an identity is hooked in, or nothing runs

Every tool that sends, reads or takes work as an identity runs only while that identity is hooked in:
the pushes its role needs are live and proven, in one record in the bus Redis. A tool that finds the
identity not hooked in refuses, and its refusal is a breadcrumb: the role, which push is not live, who
holds it, and the exact command that hooks the identity in, runnable as printed.

The record and its rules are the hook module, `github.com/mas-bandwidth/nova-tools/hook` (the `hook/`
directory, its own `go.mod`, the Redis client its one dependency), so every repository that gates
reads the same record the same way. The machine is `tla/Hook.tla`. The command line is `nova-hook`.

## Roles and pushes

| role | pushes | who hooks in as it |
|---|---|---|
| `coordinator` | `bus`, `sprint` | the seat: its session's bus stream and the sprint server's pushes |
| `friend` | `bus`, `work` | a friend working the sprint: her bus stream and the work dealt to her |
| `comms` | `bus` | a friend on the bus who takes no sprint work |
| `member` | `work` | a machine's member loop |
| `reader` | `work` | a machine's reader loop |

A push is held by the process that receives it:

| push | holder | its proof |
|---|---|---|
| `bus` of a session | `nova-hook hook --as <name> --role <role>`, under the session's Monitor | the session answers the hook's challenge: `nova-hook prove --as <name> --nonce <n>` |
| `bus` of a friend | her daemon, `nova-friend run` (or its notifications receiver) | her session answered the daemon's session check (the push proof) |
| `sprint` | `nova-sprint hook --as <name>` | its own subscription's challenge, answered |
| `work` of a friend | her daemon's lanes | the sprint server answered her daemon's beat inside a lease |
| `work` of a member or reader | its loop, `nova-swarm member` | the loop's own round trip to the sprint server |

## The key layout

All keys live in the bus Redis (the store `nova-bus` opens: `--redis`, else `NOVA_BUS_REDIS`, else the
fleet row's `fleet:bus`).

| key | type | holds |
|---|---|---|
| `hook:rec:<name>` | hash | `role`, `state` (`hooked` or `unhooked`), `epoch`, `armed` and `unhooked` (ms of the store's clock), `by` |
| `hook:live:<name>:<push>` | string, TTL `LeaseTTL` | the lease: `{"epoch":<n>,"at":<ms>,"by":"<holder>"}` |
| `hook:proof:<name>:<push>` | string, TTL `ProofTTL` | the proof, the same shape |
| `hook:chal:<name>` | hash, TTL `AnswerBound` | `epoch`, `sum` (the SHA-256 of the nonce, never the nonce), `pushes` |
| `hook:names` | set | every name that ever hooked in |

Every stamp is the store's clock (`TIME`), never a holder's. Every write is one script that checks the
record's state and epoch in the same step: a holder of an epoch since unhooked writes nothing.

## The clocks

| name | value | what it bounds |
|---|---|---|
| `LeaseTTL` | 90 s | a push stays live this long after its holder last renewed it |
| `RenewEvery` | 30 s | a holder renews three times inside one lease |
| `ChallengeEvery` | 30 min | a session's hook asks again |
| `AnswerBound` | 5 min | a challenge waits this long for its answer |
| `ProofTTL` | 35 min | a proof stands for one cadence and its answer's bound |

A holder that dies stops renewing, and its lease expires on its own: the gate refuses within `LeaseTTL`.

## The states

A push is `down` (no lease of the record's epoch), `armed` (a lease and no proof) or `proven` (both).
The record is `none` (never hooked), `unhooked`, `expired` (hooked, no push of its role live), `partial`
(some live), `live` (every push live, not every one proven) or `proven` (every push live and proven).
The gate's one yes is `proven`, with a role that carries every push the act needs (`hook.Status.Gate`;
`tla/Hook.tla`, `GateMeansHooked`).

## The epoch and the clear rule

The record carries its own epoch. `nova-hook hook` as the role the record is hooked as joins it at its
epoch; as another role (the identity's own word: it takes the record over, and says so), or after an
unhook, it starts a new epoch and clears every lease, proof and challenge of the old one. A holder (a
daemon, a loop) joins a record hooked as its role and never takes over one hooked as another role: it
holds nothing and says why, so a coordinator's own friend daemon cannot unhook the coordinator. `nova-hook unhook` moves the epoch on and clears them too, so no holder's
later write counts (`tla/Hook.tla`, `LiveIsJoined`, `UnhookStands`). A running holder whose renewal is
refused holds nothing until someone hooks the identity in again as its role, then rejoins that epoch.
A sprint's clear does not touch the hook keys: a hook is the bus's, not the sprint's.

## The gate in every tool

- `nova-bus`: every verb (`wait`, `send`, `peek`, `recv`, `ack`, `receipts`, `overdue`, `log`, `names`)
  gates the identity acting (the login user, else `--as`) on the `bus` push, before it reads or writes.
- `nova-friend run`: the daemon is its friend's hook as `friend` (`comms` under `--notifications-only`).
  While the gate says no, her lanes take nothing and report nothing (take, progress, finish), her reads
  begin nothing, and her beat says her down with the reason `unhooked: ...`, so the server deals her
  nothing. The coordinator's verbs (`ping`, `ping --to-friends`, `serve`, `watch`, `reach`) gate `--as`
  on the `bus` push.
- `pkg/hookgate` is the one place a nova-tools verb meets the gate: `Gate` over the bus store it opened,
  and `Out`, the gate's no as the verb's result.

A gate's no is `FAILED` at exit 1, `reason=unhooked`, the hook command its remedy and every other line
of the breadcrumb a `NOTE`. A record that cannot be read is `REFUSED` at exit 2: the gate fails closed,
and only its yes lets a verb act.

What is not gated, and why: `nova-hook` itself (it is the hook); `nova-friend pong` and the delivery
check `nova-friend check --harness` (they are how a session proves its bus push: gating them would
leave the push unprovable); the local verbs that open no bus store (`install`, `uninstall`, `host`,
`status`, `resume`, `refuse-go`, `acceptance`, the Claude `hook` adapter); `wait-pong`, which names no
identity; and `nova-friend beat`, the canary, which opens no bus store (the daemon's own beat is gated).

## The verbs

- `nova-hook hook --as <name> --role <role> [--once]`: arms (or joins) the record, holds the `bus` push
  when the role carries it (renewing its lease, printing each message on the name's stream as
  `HOOK PUSH`), prints `HOOK CHALLENGE nonce=<n>` at start and every `ChallengeEvery`, and a
  `HOOK STATE` line on each change; it stops on a signal (exit 0) or when the record is unhooked under
  it (exit 1). `--once` arms, renews once and prints the state (0 proven, 1 not).
- `nova-hook prove --as <name> --nonce <n>`: answers the challenge.
- `nova-hook unhook --as <name> [--dry-run]`: unhooks.
- `nova-hook status [--as <name>]`: one record as a check (0 proven, 1 not, with the breadcrumb), or
  every record.

## Using the module from another repository

```sh
go get github.com/mas-bandwidth/nova-tools/hook@v0.1.0
```

`hook.Store{C: <redis client>}` is the record; `Gate(ctx, hook.Want{Name, Need, Role})` is the gate;
`hook.Holder` holds pushes for a long-running process (`Start`, then `Beat` every `RenewEvery` with the
pushes its own round trip proved); `hooktest` is the test rig, a miniredis reached through `net.Pipe`.
The module is tagged `hook/vX.Y.Z` in this repository.
