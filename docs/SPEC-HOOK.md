# SPEC-HOOK: an identity is hooked in, or nothing runs

Each tool that gates an acting identity permits its operation only while the required pushes for that
identity's role are live and proven, in one record in the bus Redis. A tool that finds its acting
identity not hooked in refuses, and its refusal is a breadcrumb: the role, which push is not live, who
holds it, and the exact command that hooks the identity in, runnable as printed. A mailbox push check
on `nova-bus send` or `recv` is separate: it is advisory by default and becomes a refusal only when
`--require-push` or `NOVA_BUS_REQUIRE_PUSH=1` is set.

## What this checks: liveness, not identity

The hook checks that the pushes of a name are live and answered: a holder renews them, and the
session or the daemon that receives them proves it reads them. A proof is a protocol response to a
challenge; it does not establish foreground session presence or native idle-wake behavior. It does
not check who the caller is.
Any process that can reach the bus Redis can run `nova-hook hook --as <anyone>`, and every tool's
`--as` is the caller's own word. The bus Redis has only its default user (checked read-only with
`ACL USERS` on the keeper, 127.0.0.1:6381), so every client may write every `hook:*` key. Protection
against a caller acting as another name needs a Redis ACL user per identity, each allowed only its
own `hook:*:<name>*` and `bus2:*` keys, and the tools logging in as that user (SPEC-BUS's per-friend
ACL line, not built). That is the owner's decision; until it is made, the hook is a liveness gate and
nothing more.

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
| `bus` of a friend | her daemon, `nova-friend run` (or its notifications receiver) | a session harness: her session answered the daemon's session check this run, and is present now or was within `BusGrace` (10 min, one check cadence); a per-card harness (claude, whose cards run as processes of their own, so no interactive session holds the push): the daemon's own round trip on her bus store; the notifications receiver: its own round trip |
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
| `ProofTTL` | 40 min | a proof stands for one cadence and twice its answer's bound: a proof answered at the end of one challenge's bound outlives the whole bound of the next |

A holder that dies stops renewing, and its lease expires on its own: the gate refuses within `LeaseTTL`.

## The states

A push is `down` (no lease of the record's epoch), `armed` (a lease and no proof) or `proven` (both).
The record is `none` (never hooked), `unhooked`, `expired` (hooked, no push of its role live), `partial`
(some live), `live` (every push live, not every one proven) or `proven` (every push live and proven).
The gate judges the act's own need (`hook.Status.Gate`; `tla/Hook.tla`, `GateOK`, `GateOKMeansHooked`,
`NeedIsEnough`): a hooked record, a role that carries every push the act needs, and those pushes each
`proven`. An act that needs the bus push opens when the bus push is live and proven, whatever the role's
other pushes do: a friend working by hand, with her bus push held by her session's hook and no daemon
holding her work push, is `partial` in `nova-hook status` and still sends on the bus and beats. An act
that names no push (`nova-hook status`) is judged on every push of the role, so `proven` is still the
record's one all-clear, and a daemon's own gate names every push it holds (`hook.Holder.Gate`). An act
that takes work needs the `work` push and is not opened by the bus push. A refusal names only the
pushes the act needs, and the breadcrumb only their holders.

## The epoch and the clear rule

The record carries its own epoch and role. `nova-hook hook` is the identity's own word
(`hook.Store.HookAs`): as the role the record is hooked as, it joins it at its epoch; otherwise (no
record, an unhooked one, or one hooked as another role, which it takes over and says so) it starts a
new epoch and clears every lease, proof and challenge of the old one.

A holder (a daemon, a loop; `hook.Holder.Start`) is not the identity's word. It joins a record hooked
as its role and arms a record only where there is none at all. It never takes over a record hooked as
another role, so a coordinator's own friend daemon cannot unhook the coordinator; and it never arms an
unhooked record, so an unhook stands across the holder's restart, not only while it runs. In either
case it holds nothing and says why (`hook.ErrOtherRole`, `hook.ErrNotHooked`), until someone hooks the
identity in again as its role; then it joins that epoch (`tla/Hook.tla`, `Arm`, `HookedByItsWord`).

`nova-hook unhook` moves the epoch on and clears the keys, so no holder's later write counts
(`LiveIsJoined`, `UnhookStands`). Every write of a holder (a renewal, a proof, an unproof, a challenge)
is refused unless the record is hooked at the holder's epoch as the holder's role.

A record that is lost (no record: its key deleted, or the store flushed) is not an unhook: a running
holder's next beat arms it again, as its start would, and the session's `nova-hook hook` loop arms it
again at its next renewal (`ReArm`). Its epoch then counts from 1 again; the role in every holder's
write is what keeps a holder of another role that joined an old epoch of the same number from writing
under the new one (`Flush`, the `norole` witness).

A sprint's clear does not touch the hook keys: a hook is the bus's, not the sprint's.

## The gate in every tool

- `nova-bus`: every verb (`wait`, `send`, `peek`, `recv`, `ack`, `receipts`, `overdue`, `log`, `names`)
  strictly gates the acting identity (the login user, else `--as`) on its own `bus` push before it reads or writes.
  Separately, `send` notes unproven `from`/`to`/`cc` mailbox pushes and continues; `recv` notes an
  unproven mailbox push and continues. `--require-push` or `NOVA_BUS_REQUIRE_PUSH=1` makes those
  mailbox checks strict. A `send` caller still needs its own proven `bus` push either way.
- `nova-friend run`: the daemon is its friend's hook as `friend` (`comms` under `--notifications-only`).
  While the gate says no, her lanes take nothing and report nothing (take, progress, finish), her reads
  begin nothing, and her beat says her down with the reason `unhooked: <why>`, so the server deals her
  nothing. Each round trip of the daemon's hook step is bounded on its own (`hook.Store.Timeout`).
- The coordinator's verbs (`ping`, `ping --to-friends`, `serve`, `watch`, `reach`) gate `--as` on the
  `bus` push and on the role: `coordinator`, or `comms` until nova-sprint hook ships with nova-sprint
  PR #93 (until then a coordinator cannot be proven). A role that merely carries the bus push is not
  enough: a name hooked in as `friend` (the seat's own friend daemon arming it) opens none of them
  (`hook.Want.Roles`).
- `nova-friend beat`, the hand canary, gates `--as` as a `friend` or as `comms` on the `bus` push: it
  sends no beat for a name not hooked in as one of them with its bus push live and proven. A beat is a
  friend's own act on the bus (`--as` is her own name) and takes no work, so a friend working by hand
  proves her presence herself; a name hooked in as the coordinator, a member or a reader opens none of
  it (`hook.Want.Roles`).
- `pkg/hookgate` is the one place a nova-tools verb meets the gate: `Gate` over the bus store it opened,
  and `Out`, the gate's no as the verb's result.

A strict acting-identity gate's no is `FAILED` at exit 1, `reason=unhooked`, the remedy and every other
line of the breadcrumb a `NOTE`. A record that cannot be read is `REFUSED` at exit 2: the gate fails
closed, and only its yes lets that identity act. An advisory mailbox check instead adds a `NOTE` and
lets the operation continue; the explicit `--require-push` flag or environment setting makes that
check refuse before reading or writing.

## The breadcrumb

Every line of it runs as printed: no elision for the reader to fill in.

- With a role known (the record's, or the role the verb is for), the remedy is
  `nova-hook hook --as <name> --role <role>`, and a line per push that is not proven names its holder:
  the friend daemon's service (`com.nova.friend-<name>`, or `com.nova.friend-notifications-<name>` for
  comms) with the line that restarts it on this machine (`launchctl kickstart -k gui/$(id -u)/<label>` on macOS, `systemctl --user restart <label>` on Linux); the session's
  `nova-hook hook` under its Monitor; for the sprint push, `nova-sprint hook --as <name>`, which ships
  with nova-sprint PR #93, and until then `--role comms`.
- With no role known (a `nova-bus` verb run as a name never hooked in), the breadcrumb never guesses
  one: the remedy is `nova-hook status --as <name>`, and the lines offer each role that carries the
  push, comms first, then friend, then coordinator.

What is not gated, and why: `nova-hook` itself (it is the hook); the default `nova-bus` mailbox
proof check on `send`/`recv` (it is advisory, unless `--require-push` or `NOVA_BUS_REQUIRE_PUSH=1`);
`nova-friend pong` and the delivery
check `nova-friend check --harness` (they are how a session proves its bus push: gating them would
leave the push unprovable); the local verbs that open no bus store (`install`, `uninstall`, `host`,
`status`, `resume`, `refuse-go`, `acceptance`, the Claude `hook` adapter); `wait-pong`, which names no
identity.

## The verbs

- `nova-hook hook --as <name> --role <role> [--once] [--dry-run]`: arms (or joins) the record, holds the `bus` push
  when the role carries it (renewing its lease, printing each message on the name's stream as
  `HOOK PUSH`), prints `HOOK CHALLENGE nonce=<n>` at start and every `ChallengeEvery`, and a
  `HOOK STATE` line on each change; it stops on a signal (exit 0) or when the record is unhooked under
  it (exit 1), and arms a record lost under it again. A stream that does not answer is one
  `HOOK STATE state=unread` line while the fault stays the same, a tick's wait between reads, and a
  `HOOK STATE state=read` line when it answers again: a store that is down never floods the Monitor.
  `--once` arms, renews once and prints the state (0 proven, 1 not); `--dry-run` reads the record and prints
  the plan it would take (role, epoch, the pushes held and each other push's holder), writing nothing.
- `nova-hook prove --as <name> --nonce <n> [--dry-run]`: answers the challenge; `--dry-run` reads the record
  and prints what it would prove (the state, the role and the live pushes a challenge would name), writing nothing.
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

## Adoption: the callers that need hooking

Every caller below acts on the bus as a name, so from this change it is refused until that name is
hooked in. The refusal is the breadcrumb at exit 1; nothing is silenced.

- `com.nova.loop.friend-ping-<seat>`, the hand loop on the keeper: `nova-friend ping --as <seat> --to <each>`. It needs the seat's name hooked in as `coordinator`, or as `comms` until nova-sprint PR #93
  ships: `nova-hook hook --as <seat> --role comms` under the seat's Monitor, its challenges answered.
- `nova-update`'s receipts, sent as the machine: `internal/update/report.go:184` (`redisSend`'s
  `nova-bus send --as <machine>`, which the snapshot report sends through) and
  `internal/update/adopt.go:199` (the adoption receipt, through the same send). Decision: these sends are to be exempt, by the identity's nova-config row kind (`machine`),
  for `nova-bus send` only, never by a flag a caller could pass, each exempt send saying so on a
  `NOTE` line. Why not hook the machines in as comms: a comms hook needs its bus push proven, and
  nothing reads a machine's bus stream, so no session or daemon could ever answer its challenge; a
  machine hooked as comms would sit `live` and never `proven`, and its receipts would be refused all
  the same. A machine's receipt is a one-way report into the seat's stream, and the gate's question
  (does the sender hear its own stream) has no answer for a name that has no reader. **Not built in
  this change:** the exemption is owed as its own card, because `nova-bus` reads no nova-config row
  today. Until it lands these receipts are refused (`FAILED reason=unhooked`), the receipt watch
  reports them overdue, and that alarm stays on.
