# SPEC-FRIEND: what a friend runs to be part of the team

A friend is a frontier-level mind that runs her own loop. She takes a ready card from her
fleet row `friend.<name>` with `nova-sprint take --as friend.<name>` (the take marks the
card started), works it, and finishes it with `nova-sprint finish --as friend.<name>`.
`nova-sprint friend stage` clones a card at its carried head into a directory when she
wants one. Her liveness is her nova-hook record, and her working count is the cards in the
working cell of her fleet row, counted as any fleet member's are.

There is no friend daemon and no friend delivery mode. The nova-friend daemon (its lanes,
its inbox and outbox, its one process per card, its beat and its launchd agent), the
friend row's `mode`, `config_dir`, `token_cap` and `session` fields, and the
`session=one-shot` path, where a fleet member ran a friend's row with an identity prompt,
were removed on 2026-11-05 (the owner: "One shot was a workaround for friends who can't
reliably run their own loops and take ready cards, and we don't run those sort of friends
anymore."). A model that cannot run its own loop is a fleet member, not a friend.

`pkg/friend` keeps what the sprint server and the hook share: the `friend cards` answer
(`HeldCard`, `ParseHeld`), the LANE line (`LaneTakeArgv`, `ParseLaneHold`), the pronoun
sets, and the antigravity probes `nova-hook` reads.

## Presence

A friend's presence and liveness live in the hook record in the bus Redis
(`hook:rec:<name>`, [SPEC-HOOK.md](SPEC-HOOK.md); `tla/Hook.tla`,
`tla/HookBeat.tla`), never in a beat, a process table check, or a ping/pong file:

- `nova-hook hook --as <friend> --role friend --harness <h>` in the friend's
  session holds her `bus` push.
- `nova-hook serve` challenges every hooked friend once a minute with a bus
  message (`HOOK CHALLENGE nonce=<n>`); the session's `nova-hook hook` delivers
  it into the session (pkg/harness/notice, one shim per harness, the one
  delivery path), and the model answers inside its turn with
  `nova-hook prove --as <friend> --nonce <n>`.
- No answer for more than `nova-hook serve --down-after` (5m) unhooks the friend at its next missed
  challenge (`no answer in 5m0s (<n> challenges missed)`) and
  sends one coalesced `judgment` (`FRIEND DOWN <friend>: <reason>`) to the
  coordinator.
- While the hook gate says no (`hook.Status.Gate`), `nova-bus send` refuses
  deliveries to her with the reason and one deduplicated judgment to the
  coordinator.
- `nova-hook friends [--json]` and `nova-hook status --as <friend>` read
  presence directly from the hook record. The class rule
  `internal/ci/retired_presence_class_test.go` refuses the return of the retired
  presence paths, its table `internal/ci/testdata/retired-presence.txt` naming each
  identifier, verb and flag.

## The hook's lane client

A friend's hook service (`nova-hook hook --role friend`, `internal/friend`,
`cmd/nova-hook/lanes.go`; `tla/FriendLaneClient.tla`, `tla/FriendAutoTake.tla`) asks the
sprint server what each lane of her row holds with `take --as friend.<name> --lane <n>`,
takes a ready card into a free lane, and tells her model once on the bus. It keeps no
local state that decides: the server's records are the truth, and the model finishes
through the server's own verbs.

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
  and hook record `hook:rec:<friend>` ([SPEC-HOOK.md](SPEC-HOOK.md)).
- **The friend row**: nova-config's `friend` kind (tiers, roles, width, streams, kinds,
  pronouns; [SPEC-CONFIG.md](SPEC-CONFIG.md)), copied into the sprint store by
  `nova-sprint friend sync`.
