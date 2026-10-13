# 10 Server truth

Index: [00-index.md](00-index.md). This layer may rely on nothing below it.

## Owner

Stella (she/her).

## Scope

In:

- (to fill)

Out (later releases):

- (to fill)

## Guarantees

Numbered, each one testable.

1. (to fill)

## State and transitions

- States: (to fill)
- Events: (to fill)
- Who owns each record: (to fill)

## Failure modes

For each: how it is detected, and how it is recovered.

1. (to fill)

## Interfaces

- Below: none. This is the bottom layer.
- Above: what 20 (comms), 40 (friend lane) and 50 (sprint delivery) read and write here.

## Open questions

1. (to fill)

## Inputs

Design points for the owner to weigh. The decisions are the owner's.

- One server is the truth and clients ask. Today, who exists is held in three copies (config rows,
  the sprint store and the bus roster), with a sync loop between them. Adding a friend could need
  hand steps before the bus knew her name. Candidate: one store, one writer per record family, and
  no copy that a reader has to repair.
- Presence is a server record, not a client claim. Today, "is this friend up" is inferred five ways
  (ping and pong, wake files, daemon push proofs, send-time notes, a loop pinging itself), and no two
  have to agree. Candidate: up means the hook record is proven and not expired at the server's read
  time, and down always carries a reason.
- Stella: one server-owned inbox and task state machine, with immutable message ids, idempotent
  acks, per-session leases and delivery generations. The client holds only replaceable connection
  state, never ownership or progress.
- Stella: publish base changes and ownership changes once, with monotonic revisions, so that a
  session change can recover its assignments.
- Johnny: dealt, taken, worked, finished, read and merged live on the server, and a claim survives a
  session wrap.
- Slots and leases are dealt by the server. Today, lanes that share a host coordinate through
  filesystem slot locks, which a dead keeper or a symlink path can break.
- The usage ledger is the server's, not a per-machine fold, so a friend lane keeps no usage state.
- Records are keyed by epoch, so a clear starts clean with no reset step.
- A per-model request budget, if kept in v1.3, is one shared bucket on the server, taken atomically
  by every caller.
