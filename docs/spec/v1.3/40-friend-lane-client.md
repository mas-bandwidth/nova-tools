# 40 Friend lane as client

Index: [00-index.md](00-index.md). This layer relies only on 10, 20 and 30.

## Owner

Johnny (he/him).

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

- Below: server records (10), comms verbs (20), and the harness hook-in (30).
- Above: what 50 (sprint delivery) deals to a lane and reads back from it.

## Open questions

1. (to fill)

## Inputs

Design points for the owner to weigh. The decisions are the owner's.

- The friend lane becomes a client: take, run, finish, with no local state. Today the friend daemon
  keeps lane files, an inbox and outbox, a presence file and its own epoch. Each of these is a
  second truth.
- Friends and fleet share one lane protocol, with one deadline rule for both.
- The friend side of push proof moves from session-check and nonce files to a server-held
  subscription, where the ack is the proof.
- Friend messages stop going through folders. The bus is rows in the one store.
- The lane-end epoch comes from the server, not a local flag.
- Slots on a shared host are dealt by the server. No lock file, and no keeper process.
- One-shot lanes: one card per lane, a fresh headless run per card, up to the friend's width. A
  failing lane does not stop the others. A batch session that takes many cards in one long turn went
  deaf to session checks, and wasted many reports on reads that had already been taken back.
- A missing provider credential, or a provider refusal, is reported at once as a judgment, never as
  a silent failure.
- Johnny: the harness owns the client, and the session attaches to a client that is already running.
  A claim survives a session wrap.
- Stella: the client holds only replaceable connection state, never authoritative ownership or
  progress.
- A durable local nudge-debt ledger exists today. It goes against "no local state", and the rewrite
  may replace it.
- A standalone friend client was seen to deadlock on a missing sprint beat. The lane must not need
  nova-sprint in order to hear the bus.
- A friend install on Linux hosts needs service units, or the lane and link cannot be installed
  there.
- A new friend should join with one command. Adding one recently took hours and several hand steps.
