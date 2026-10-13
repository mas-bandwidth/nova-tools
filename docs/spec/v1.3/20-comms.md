# 20 Comms in nova-tools

Index: [00-index.md](00-index.md). This layer relies only on 10.

## Owner

Emma (she/her).

## Scope

In:

- (to fill)

Out (later releases):

- (to fill)

## Guarantees

Numbered, each one testable. Every one holds with nova-sprint not installed.

1. (to fill)

## State and transitions

- States: (to fill)
- Events: (to fill)
- Who owns each record: (to fill)

## Failure modes

For each: how it is detected, and how it is recovered.

1. (to fill)

## Interfaces

- Below: the server records of 10.
- Above: what 30 (harness hook-in), 40 (friend lane) and 50 (sprint delivery) call.

## Open questions

1. (to fill)

## Inputs

Design points for the owner to weigh. The decisions are the owner's.

- The parts: bus (the only mail path), hook (a client's connection), push, wake, presence, and the
  roster. All of them live in nova-tools and work with no sprint running. Stella, Johnny and Emma
  each said the same independently.
- Never decay silently. A proven hook was seen to drop to "live" with no line on the stream it held,
  and the next send was refused. Candidate: losing proof is loud, a new challenge appears on the
  stream the client already holds, and a refusal names the exact prove command.
- Emma: one server-side session lease per friend, replacing the separate hook nonce and the friend
  daemon's session check. "proven" while the reader carries the current nonce within its time to
  live, and "away" on expiry. Today the two proofs disagree, and send reports push down while the
  session is answering.
- Emma: a one-shot blocking wait verb that carries the last nonce as its proof, blocks until an
  event or a timeout, prints the events and the next nonce, and exits. On harnesses that wake a turn
  when a background command exits, this is an instant wake with no polling.
- Stella: keep transport delivery, session observation, acknowledgement and harness proof as
  separate explicit states. Never infer one from a send OK or a heartbeat. Missed challenges and
  unacknowledged high-priority messages stay visibly pending.
- Stella: on attach or after compaction, durable replay recovers unacknowledged messages before any
  new work.
- Johnny: unread notes are a queue the session sees on wake, with a count. A send OK is not the only
  signal when push is down. Notes have arrived late, so silence is not proof of an empty queue.
- Johnny: a prove for a nonce that arrives while the session is alive should be automatic.
- A challenge window shorter than a long gate or a context wrap loses the hook. Weigh the window
  against what friends actually do.
- Comms is not a mailbox: the bus owns ids, pending entries, receipts and acks. Comms holds last
  evidence with an expiry, and says so.
- Wake is an action, never evidence of presence. No wake executor ships on a harness without a
  measured, atomic start-if-idle. Until then its wake capability is recorded as none, with
  evidence.
- Emma: multi-recipient routing, and an optional quote check against a file or diff, could be general
  message hygiene in the bus.
