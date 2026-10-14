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
- A one-shot friend runs as an ordinary fleet member lane with an identity prompt, not as a
  friend lane (guarantee 12). The project owner, live, 2026-10-14: "just make one shot friends,
  exactly the same code that already works in the fleet, just with an extra prompt at the
  front", which "removes all the complexity around one-shot friends being their own code path."

Out (later releases):

- Dealing slots on a shared host, lock files, and a keeper process. v1.3 has no second
  scheduler beside the server.
- A local nudge-debt ledger. The client keeps no debt.
- The migration that deletes today's lane files, inbox directory, outbox directory, and
  presence file. The end state is that those files are not read. The steps that retire them
  are not this layer.
- Packaging of service units beyond the one install command below.

## Guarantees

Numbered, each one testable. Each one holds with nova-sprint not installed, except 4, 5 and 9
to 12, which are the card records nova-sprint deals and this client only reports.

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
   card stays taken by that friend. It is not returned by the wrap. It leaves the friend when the run finishes or the server releases it; at the execution deadline the claim enters needs-reconciliation and still holds the card until the server releases or reassigns it.
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
9. **Down does not take a card back.** Beside the wrap rule of guarantee 4 (round 2, proposal
   5 of layer 60 as corrected by change 7): when layer 20 reads the friend down (guarantee 17
   there), including the case where her daemon answers and her session does not, the server
   stops new deals to her and moves her taken or running claims to needs-reconciliation. Deaf
   keeps them as they are. A card returns to the deal only when its holder finishes or releases
   it, or reconciliation establishes the run's outcome or stop. Observation: with a fake clock,
   take two cards, let the friend go deaf and then down; both claims read needs-reconciliation,
   neither card is dealt again, and a finish from her then lands as guarantee 11 says.
10. **No take without a proven link.** A lane takes a card only while its friend is hooked
    (layer 20 guarantee 12) and has passed the self-test of layer 30 guarantee 6. A take from an
    unhooked friend is refused with the link remedy and changes no claim. Observation: unit test
    with a fake store: an unhooked friend's take is refused and the card stays dealt; after arm,
    prove and self-test the take succeeds.
11. **A finished report stays its author's.** A finish names its immutable claim ID and
    execution fence; the server attributes it to that historical claim holder, never to a
    replacement holder, branch suffix or client-supplied author name. After the historical
    holder (the friend who held the claim when it was released or reassigned) re-hooks, an
    authenticated current session may submit evidence for a released, reassigned or superseded
    execution fence; the server records it idempotently as a late finish under that historical
    holder and holds it for reconciliation, without advancing the current claim or merging
    anything; unhooked and stale-session submissions still receive the link or session refusal
    and may be retried through the current binding. Observation: release a claim,
    then finish it from the old holder: one late-finish record exists under her name, and no
    "no longer the friend's" refusal leaves the finish unrecorded.
12. **A one-shot friend is a member lane with an identity prompt.** A one-shot friend (no live
    session between cards; layer 10, Friend record) is not a friend lane of this layer. It runs
    as an ordinary fleet member lane: the same server-held take, run and finish loop fleet
    members use, client/server, with the same lane client. Its only per-friend configuration is
    an identity prompt (name, pronouns, voice) prepended to each card brief, the harness, the
    model and the account or key, all server-held rows cached by the client (layer 10
    guarantee 15). It has no friend daemon, presence pings, wake files or friend-specific lane
    parser; those paths are deleted, not kept beside the member path. It is excluded from the
    psychic link by definition (layer 20 guarantee 19), so guarantee 10 does not gate its take;
    work and context for it travel in the card brief. Its work is credited to its identity from
    the server claim, so its history stays its own. Observation: unit test with a fake store and
    time passed in: a one-shot friend row is dealt and finishes through the member path, its
    identity prompt is prepended to the brief, and no presence state exists for it.

The lane carries every deal, take, finish, judgment and read over the psychic link of layer 20
(guarantees 12 to 18), and asks Redis, the one server, for everything it shows (layer 10
guarantees 14 and 15). The take-back by a tick that redeals a taken card, which in the three
hours before 14:24Z on 2026-10-14 moved friend cards from working to ready 12,709 times and left
finished reports refused (the Grok friend 92, the Antigravity friend 21, a one-shot dsh friend
8), is what guarantees 4, 9 and 11 remove.

## State and transitions

A lane is one of these. The server stores the state. The client displays it.

- `idle`: the client is up and holds no card.
- `taken`: the server has accepted the lane's take and recorded its claim for this friend, and the run has not started.
- `running`: the run has started.
- `finished`: the run ended and the result is on the server.
- `failed`: the run ended without a result, and the judgment is on the server.
- `needs-reconciliation`: the execution deadline passed, or the friend went down, while the
  card was `taken` or `running`. The claim still holds the card; it counts against `width`.
- `released`: the server took the card back by an explicit release, or after reconciliation
  established the run's outcome or stop.

Events:

- `Deal` records an offered card in the server assignment and leaves the lane `idle`; `Take`
  moves `idle` to `taken` only after the server atomically checks the offered assignment,
  current hooked and self-tested binding, one claim per card and available width, then records
  the claim. A client with `width` lanes already `taken`, `running` or `needs-reconciliation` is
  not dealt another.
- `Start`: `taken` to `running`. The client does this by beginning the run, and the server
  records it.
- `Finish`: `running` to `finished`. The result is on the server before the run's process exits, attributed by its immutable claim ID and execution fence under guarantee 11. After the historical holder (the friend who held the claim when it was released or reassigned) re-hooks, an authenticated current session may submit evidence for a released, reassigned or superseded execution fence; the server records it idempotently as a late finish under that historical holder and holds it for reconciliation, without advancing the current claim or merging anything; unhooked and stale-session submissions still receive the link or session refusal and may be retried through the current binding.
- `Fail`: `running` to `failed`. One judgment is on the server before the process exits.
- `Deadline` and `Down`: `taken` or `running` to `needs-reconciliation`. Only the server does
  this, at the execution deadline on its clock or when layer 20 reads the friend down.
- `Reconcile`: `needs-reconciliation` to `finished`, `failed` or `released`, once the holder
  (on re-hook, through her replay) or the coordinator (as a judgment) establishes the run's
  outcome or stop. A finish from the holder in this state is a `Finish`.
- `Release`: `taken` or `running` to `released`, by the holder or the server explicitly; every
  release and its reason are recorded on the claim and replayed to its holder on attach.
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
   at its deadline the server moves the claim to `needs-reconciliation`; the card is released
   only after reconciliation. A new client does not take the card while it is still `running`,
   `taken` or `needs-reconciliation`.
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

- Below: server records on Redis, the one server (10); comms verbs and the psychic link (20);
  and the harness hook-in (30). The client is the process layer 30 keeps alive. Every deal,
  take, finish, judgment and read reaches the lane through the link and no other path: the
  daemon polls, ping/pong session checks, nonce and wake files, local inbox and outbox
  folders, lane files, presence file, epoch flag and nudge-debt ledger of today's friend
  daemon are retired, and the lane caches server state only as layer 10 guarantee 15 allows.
- Above: 50 deals a card by the `Deal` event and reads `finished`, `failed`, and
  `released`. This layer does not choose which card, and it does not schedule.

## Open questions

1. Whether a running lane owes the server a heartbeat before the deadline, or the
   deadline alone is the release. v1.3 uses the deadline alone, and at the deadline the
   claim enters needs-reconciliation rather than being released (guarantee 4). A heartbeat
   is a second clock, and this layer has one.
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
- The Grok friend: the harness owns the client, and the session attaches to a client that is already running.
  A claim survives a session wrap.
- The Codex friend: the client holds only replaceable connection state, never authoritative ownership or
  progress.
- A durable local nudge-debt ledger exists today. It goes against "no local state", and the rewrite
  may replace it.
- A standalone friend client was seen to deadlock on a missing sprint beat. The lane must not need
  nova-sprint in order to hear the bus.
- A friend install on Linux hosts needs service units, or the lane and link cannot be installed
  there.
- A new friend should join with one command. Adding one recently took hours and several hand steps.
