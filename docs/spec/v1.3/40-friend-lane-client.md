# 40 Friend lane as client

Index: [00-index.md](00-index.md). This layer relies only on 10, 20 and 30.

## Owner

The Grok friend (he/him).

## Scope

In:

- A friend lane is a client of the one server. It takes work the server deals, runs it, and
  reports the finish. It keeps no durable record of its own.
- The harness owns the client process (layer 30). A session attaches to that process. Ending
  the session does not stop it, and starting a session does not start a second one.
- At most `width` lanes run for one friend. Each lane holds one card, in a fresh run. One
  lane failing does not stop the others.
- A missing provider credential, or a provider refusal, is one judgment on the server at
  once, and that lane is finished failed.
- The lane hears the bus with nova-sprint not installed. nova-sprint only deals cards and
  reads the finish records back.

Out (later releases):

- Dealing slots on a shared host, lock files, and a keeper process. v1.3 has no second
  scheduler beside the server.
- A local nudge-debt ledger. The client keeps no debt.
- The migration that deletes today's lane files, inbox directory, outbox directory, and
  presence file. The end state is that those files are not read. The steps that retire them
  are not this layer.
- Packaging of service units beyond the one install command below.

## Guarantees

Numbered, each one testable. Each one holds with nova-sprint not installed, except 4 and 5,
which are the card records nova-sprint deals and this client only reports.

1. **No local truth.** Stop the client and start another. The server's records of what is
   taken, running, and finished are unchanged, and the new client shows those records. The
   client directory holds no inbox, outbox, presence file, or epoch flag that the new
   client reads as authority.
2. **The harness owns the client.** End the session while the client is running. A note
   that arrives after that is still delivered to the client. The next session attaches to
   the same client and receives the note through layer 20's replay. It does not start a
   second client.
3. **One-shot lanes, up to width.** With width 2, deal three cards. Two runs start. The
   third stays with the server. Fail one run. The other run finishes. Both results are on
   the server. The third card is dealt only after one lane is free.
4. **A claim survives a wrap.** Take a card, then end the session without finishing. The
   card stays taken by that friend. It is not returned by the wrap. It leaves the friend
   when the run finishes, the server releases it, or the server's deadline passes.
5. **A provider refusal is a judgment.** A run whose provider refuses writes one judgment
   the server can show, and that lane is finished failed. The other lanes are still
   running. Nothing about the refusal exists only in the client's memory.
6. **The lane does not need nova-sprint to hear the bus.** With nova-sprint not installed,
   a note sent to the friend is received by the client.
7. **One deadline.** The claim's deadline is the one the server stored. No file the client
   writes changes it, and the client does not expire the claim on its own clock.
8. **Install is one command, and a second run changes nothing.** On an empty host,
   `nova-friend install` leaves one client that answers a session check. Running it again
   leaves that same client and does not start another.

## State and transitions

A lane is one of these. The server stores the state. The client displays it.

- `idle`: the client is up and holds no card.
- `taken`: the server has recorded the deal to this friend, and the run has not started.
- `running`: the run has started.
- `finished`: the run ended and the result is on the server.
- `failed`: the run ended without a result, and the judgment is on the server.
- `released`: the server took the card back, by its deadline or by an explicit release.

Events:

- `Deal`: `idle` to `taken`. The server does this. A client with `width` lanes already
  `taken` or `running` is not dealt another.
- `Start`: `taken` to `running`. The client does this by beginning the run, and the server
  records it.
- `Finish`: `running` to `finished`. The result is on the server before the run's process
  exits.
- `Fail`: `running` to `failed`. One judgment is on the server before the process exits.
- `Release`: `taken` or `running` to `released`. Only the server does this.
- `ClientCrash`: the process is gone. The stored state does not change. A crash is not a
  `Fail` and not a `Release`.
- `SessionEnd` and `SessionAttach`: no lane changes state.

Who owns each record:

- The server owns the lane state, the deadline, the width, the judgment, and the lease
  (layer 20).
- The client owns the process that is running the card, and the nonce of the current
  wait. Both are gone when the process is gone. Neither is authority.

## Failure modes

1. **The client dies mid-run.** The server sees the process gone only by the lane missing
   its next report before the deadline. Until then the state stays `running`. Recovery:
   the server `Release`s at its deadline. A new client does not take the card while it is
   still `running` or `taken`.
2. **The session wraps.** The session is gone and the client is not. Recovery: the next
   session attaches and reads layer 20's replay. The lane state is whatever the server
   stored.
3. **The provider refuses, or the credential is missing.** The run returns that refusal.
   Recovery: one judgment, the lane `failed`, every other lane untouched.
4. **The bus is unreachable.** A wait or a send fails to reach the server. Recovery is
   layer 20's bounded retry. The client does not store the note locally and deliver it
   later from its own disk.
5. **A second session starts.** It finds the client already running. Recovery: it
   attaches. It does not spawn.

## Interfaces

- Below: server records (10), comms verbs (20), and the harness hook-in (30). The client
  is the process layer 30 keeps alive.
- Above: 50 deals a card by the `Deal` event and reads `finished`, `failed`, and
  `released`. This layer does not choose which card, and it does not schedule.

## Open questions

1. Whether a running lane owes the server a heartbeat before the deadline, or the
   deadline alone is the release. v1.3 uses the deadline alone. A heartbeat is a second
   clock, and this layer has one.
2. Layer 10 does not yet have a slot record for a shared host. Until it does, this layer
   does not deal slots.

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
