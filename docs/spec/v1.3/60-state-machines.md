# 60 State machines to model in TLA+

Index: [00-index.md](00-index.md). Filled from 10 to 50 once they are written.

## Owner

The coordinator (she/her), on Claude Code.

## Scope

In:

- Every state machine that layers 10 to 50 define, modelled before it is built.

Out (later releases):

- Machines whose layer moves out of v1.3.

## Guarantees

1. Every state machine named in 10 to 50 has a TLA+ module, with its invariants and liveness taken
   from that layer's guarantees.
2. Every module is checked with TLC on a small instance, on Linux benches only.
3. Every module has reversed witnesses: configurations that must fail, one for each failure the
   layer names.
4. Every counterexample is checked against the spec by hand before it is written down.

## State and transitions

For each machine: the variables (the state it owns), the actions (operations and outside events),
the invariants, and the liveness properties. Taken from the layer that owns it.

## Failure modes

1. A model that differs from the spec. Detected by the hand check of each counterexample, and
   recovered by fixing whichever one is wrong, through its owner.

## Interfaces

- Below: the state and transitions sections of 10 to 50.
- Above: the work. Each change cites the model it implements.

## Open questions

1. Which existing modules are extended, and which are replaced. Answered per machine under
   its Existing module heading below; the summary is the closing table.

## The machines

Seven machines, fixed by the coordinator and extended only with a reason from layers 10 to 50.
Models are written bottom up in the order M4, M3, M1, M2, M5, M6, M7.

### The shape every model shares: one server, N clients

Round 2, from the project owner's rulings of 2026-10-14 (layer 10 guarantees 14 and 15, layer
20 guarantee 18). Every module has exactly one server process, Redis, which holds every
variable that is truth, and a set `Clients` of N clients (each friend, the coordinator, the
sprint) that act only by asking it. Clients have no actions among themselves: no message
passes client to client, and nothing in a module is decided by agreement between clients.

- Variables: the server's state; per client, a `cache` and its `cacheRev`, and the client's
  process facts (running, crashed). Constants: `T_refresh` (1 second, as a tick count), the
  lease and challenge lengths, and $T_{deaf}$.
- Actions: `ClientAsk(c, op)` (the server commits or refuses), `ServerPush(c)` over the link,
  `Refresh(c)` at most every `T_refresh` and at once on a push, `ClientCrash(c)` and
  `ClientRestart(c)` (the restart empties the cache), `Tick` of the server clock.
- Invariants, in every module: **NoClientTruth**: no server action reads a client's cache or
  clock; every guard of a server action reads server variables only. **CacheDerived**: every
  cached value equals the server's value at some revision at or before the current one, and a
  mutation proposed from a cache older than the current revision is refused. **ServerClock**:
  only `Tick` advances the time that decides lease, challenge and claim expiry.
- Liveness: **FreshView**: while the server is reachable and a client is running, its
  `cacheRev` reaches the server's revision as of `T_refresh` ago (weak fairness on `Refresh`).
- Reversed witnesses: a client extending its own lease on its own clock; a restarted client
  reading a stale local file as its lease or inbox; two clients agreeing a take between them
  without the server; a cache written back as a server record.

### M1 Presence and lease

#### Variables

- `binding`: absent, challenged, proven, away, or closed, per friend (layer 10).
- `generation`: the current session binding generation; `deadline`: the lease deadline.
- `lastHeard`: set by any valid authenticated activity; `lastProof`: set only by a challenge answer.
- `displayed`: up or down; `internal`: up, deaf, or down, defined in layer 20 (guarantee 17 and its Displayed presence states, round 2): up is hooked; deaf is unhooked but heard within $T_{deaf}$; down includes the case where her daemon answers and her session does not.
- `hooked`: the binding is proven, unexpired on the server clock, and its last proof answered a challenge delivered through the link (layer 20 guarantees 12 and 14).

#### Actions

- Attach and challenge issued (10-server-truth.md guarantee 4; 20-comms.md guarantee 4).
- Session answers the challenge (10 guarantee 4; 30-harness-hookin.md guarantee 2).
- Any authenticated activity: a bus message, a take, a finish, a push, a pull request, a beat, or a hook proof (10 guarantee 13).
- Lapse, Silence and ReProve over the internal states (20 guarantee 17).
- Lease deadline passes; explicit detach; binding replaced (10 guarantee 3; 20 guarantee 3).

#### Invariants

- Only a challenge answer sets `lastProof`; no other activity does.
- A previous generation proves nothing and takes nothing (10 guarantee 3).
- At or past the deadline the binding reads away with a reason (10 guarantee 5).
- `displayed` and the deal's reachability read the clocks as Reconciled (a) states, never a helper's liveness (30 guarantee 3).
- Deaf displays up and is never hooked: no push and no new send reaches a deaf or down friend (20 guarantees 16 and 17).
- Activity never sets `hooked` or extends `deadline` (10 guarantee 13).

#### Liveness

- A proven binding whose session keeps answering renewals stays proven. Fairness: the session gets a turn inside each challenge window (30 guarantee 1); environment: the server is serving (M4).

#### Reversed witnesses

- Traffic, a heartbeat, or a wake request counted as proof (failure mode: adapter prints a challenge, session never sees it; 10).
- A ping to the friend's own stream counted as the friend being up.
- An old generation's answer accepted after replacement (10 failure mode: old session answers after replacement).
- A deaf friend treated as hooked: a push offered or a new send admitted to her (20 guarantee 16).
- Daemon activity alone keeping a friend up past $T_{deaf}$ (20 guarantee 17, the daemon-answers case of down).

#### Existing module

- `FriendPresence` (partial, tla/COVERAGE.tsv row friend-presence) and `Friend` (partial, row friend-lifecycle): the new model extends both and replaces neither. Reason: they hold session proof and the friends-table word in part, while the two clocks of Reconciled (a) and the deaf state and the daemon-answers case of down are not in either; the presence-and-leases row of COVERAGE.tsv names no module, so this machine is where that gap closes.

#### Interfaces

- Reads M4 (epoch, receipts) and M3 (activity as bus evidence). Read by M2, M6. Layer below: 10.

### M2 Psychic link, hook and session

Round 2: this machine is the psychic link (layer 20 guarantees 12 to 18; layer 30 guarantees 6
to 9), the `BusLink` states of the v1.3 card for the gate joined to the hook record.

#### Variables

- `link`: unhooked, armed, challenged, hooked, or lapsed, per name (lapsed reads as unhooked to every verb).
- `reader`: running, expired, or absent, per name; its bound (the monitor expiry, or the wait chain).
- `delivered`: the set of nonces the link has written to the session; `answered`: the nonces the session answered.
- `selfTest`: passed or not, for the current generation (30 guarantee 6).
- `session`: the session identity; `capability`: the per-installation record, link and wake each measured or unmeasured (30).

#### Actions

- `Arm`: hook-in arms the record and starts the reader (20 guarantee 2 and interface 1; 30 guarantee 7).
- `Deliver(challenge)`: the server writes a nonce onto the link only (20 guarantee 14).
- `Prove(nonce)`: the session answers a nonce from `delivered` (20 guarantees 4 and 14; 30 guarantee 2).
- `Renew`, `Expire` (server clock; 20 guarantee 15), `ReaderExpire`, `Rearm` (30 guarantee 8).
- `Send`, `Recv`, `Peek`, `Ack`, `Take` for a name; `SendTo(name)` by any client (20 guarantees 12 and 16; 40 guarantee 10).
- `Status(name)`: a read that runs hooked or unhooked and reports the away state and its reason, carrying no message and no nonce (20 guarantees 12 and 14).
- `SelfTest`: after proof, a send-to-self lands in-turn and is answered, recording a passed self-test for the current generation (30 guarantee 6).
- `SendToOneShot(name)`: any send naming a one-shot friend is refused `one_shot` with the brief remedy, admitting nothing (20 guarantee 19).
- Session ends, wraps, or is replaced; re-hook and replay (30 guarantees 4 and 5; 10 guarantee 8).

#### Invariants

- Send, Recv, Peek, Ack and Take by a name happen only while it is hooked (20 guarantee 12; 40 guarantee 10); before proof the link reader carries control events only, never a message (20 guarantee 12).
- A one-shot name never has a link, lease or presence record, and nothing is admitted for it (20 guarantee 19).
- A message to a name is admitted only while that name is hooked; otherwise the sender holds a refusal and the inbox is unchanged (20 guarantee 16).
- `link` = hooked only if the last proof answered a nonce in `delivered` (20 guarantee 14).
- A take, or a deal to the name, only with `selfTest` passed (30 guarantee 6).
- A hook never expires silently: expiry carries a recorded reason (20 guarantee 3).
- Proof is never inherited across a session replacement (10 guarantee 3).
- An unmeasured capability is never reported as supported (30 guarantee 5).
- Lapse is decided by the server clock alone (10 guarantee 14).

#### Liveness

- A hooked name that keeps re-arming its reader inside the bound and answering renewals stays hooked. Fairness: the session gets a turn inside each challenge window (30 guarantee 1); environment: the server is serving (M4).
- A reader lost without re-arm leads to unhooked within $T_{lease}$ (30 guarantee 8).
- A session that re-hooks and answers is eventually hooked again.

#### Reversed witnesses

- A send to an unhooked name queued silently and the sender told success (20 guarantee 16; the deaf-friend failures of 20, Rationale).
- A recv or ack by an unhooked name succeeding (20 guarantee 12).
- A prove with a nonce taken from `nova-hook hook` output or a refusal, never delivered through the link, accepted (20 guarantee 14).
- A reader alive with no session behind it keeping the name hooked (30 guarantee 8; 30 failure mode 3).
- A take by a friend that never passed the self-test (30 guarantee 6).
- A monitor-harness link reader refused `not_hooked` before the first prove, so the first challenge is never delivered and the name can never hook (20 guarantee 12).
- A renewal challenge demoting a proven lease to challenged, so a healthy friend is unhooked at renewal (20 guarantee 4).
- A send to a one-shot name queued or admitted instead of refused with the brief remedy (20 guarantee 19).
- A proven hook decays to live with nothing on the stream and a send is refused (observed failure named in 30, Claude Code subsection; now no "live" state exists).
- A challenge held unread by a daemon-only harness until its window closes (30 failure mode 3: helper alive, session deaf).

#### Existing module

- `Hook` (current, row hook-record): the new model extends it and replaces neither it nor the session-challenge part of `Friend` (partial). Reason: Hook already holds arming, renewal, epoch, and proof against a live push; what is new is the mandatory link gate on every bus verb, the challenge delivered only through the link, the send refusal to unhooked names, and the per-harness capability record of layer 30, joined to the same record.

#### Interfaces

- Reads M1 and M4. Read by M3 and M5. Layer below: 20, over 10.

### M3 Bus delivery and acknowledgment

#### Variables

- Per message and recipient: pending, delivered, observed, acked, or pruned; `attempts`; the subscription and its cursor.

#### Actions

- Send admits every recipient entry in one commit, only when every recipient is hooked; otherwise refuses with nothing admitted (10 guarantees 1 and 2; 20 guarantees 6 and 16).
- Offer, observe, acknowledge (10 guarantees 6 and 7; 20 guarantees 8 and 9).
- Redelivery until acked; replay on attach (10 guarantee 7; 20 guarantee 9); prune after retention (20 guarantee 11).

#### Invariants

- An offer or an observation never removes a pending entry; only that recipient's ack does (10 guarantee 7).
- No message is acted on twice from one ack (10 guarantees 6 and 7).
- Replay on attach presents every pending entry before new ingress (20 guarantee 9).
- No entry is admitted for an unhooked recipient (20 guarantee 16).

#### Liveness

- Every pending entry is eventually offered, and an accepted ack eventually visible. Fairness: the declared delivery mode gets a session turn (30 guarantee 1); environment: capacity exists and the server is serving (10, conditional liveness). With no proven idle wake, an entry may wait for the next turn and stays visible as pending.

#### Reversed witnesses

- An ack that clears another recipient's entry (10 guarantee 7).
- A recv that hands a redelivered id so it is acted on twice (row bus-receipts witness).
- A failed ack stamp, a lost ack, and a crash together acting a message twice (row bus-receipts known gap; kept as a named gap, not hidden).
- A send admitted for a recipient whose lease lapsed before the commit (20 guarantee 16).

#### Existing module

- `Bus2` (partial, row bus-delivery) extended by `Bus2Receipts` (current, row bus-receipts), with `FriendEnvelope` (current) and `BusSendOnce` (current) at its edges: the new model extends Bus2 and Bus2Receipts and replaces neither. Reason: pending-until-acked, attempts, and receipts are already held there; the v1.3 addition is the layer-10 receipt vocabulary (committed, offered, observed, acked) stated as one machine.

#### Interfaces

- Reads M4 and M1. Read by M2 and M5. Layer below: 10.

### M4 Server commit, receipts and epoch

#### Variables

- `server`: recovering, serving, or unavailable; `epoch`; increasing `revision`.
- `receipts`: keyed by (epoch, caller, operation id) with a request digest.
- Per friend: enrolled or disabled; `generation`: the fenced session generation.
- Admission counts against the named capacity limits.

#### Actions

- Commit a mutation with its receipt and notifications in one step (10 guarantee 1).
- Identical retry returns the original receipt; a changed request under the key is refused (10 guarantee 2).
- Administrative epoch advance retires the old epoch and fences its bindings and claims (10, state table).
- Refusal at a capacity limit, with the limit and remedy named (10, size and representation).
- Restart recovers committed rows, invalidates live proofs and execution leases, and serves again (10 guarantee 5).

#### Invariants

- No success or notification before the commit is durable; a crash before commit produces none (10 guarantee 1).
- One authority: no second writer and no caller-maintained truth (10 guarantee 1; 40 guarantee 1).
- Unavailable is never read as current up (10 guarantee 12).
- The shared one-server, N-client invariants above (10 guarantees 14 and 15).

#### Liveness

- While the server is available, an admitted operation eventually commits or is refused with a remedy. Fairness: the serialized command path is weakly fair to queued operations; environment: persistence is available (10 guarantee 5 states the failure case).

#### Reversed witnesses

- A reply lost after commit, retried, manufacturing a second message (10 failure mode: reply lost after send commit).
- A claim change committed without its notification, or the reverse (10 failure mode: crash between claim change and notification).
- Admission past a capacity limit discarding pending entries (10 failure mode: inbox at its limit).

#### Existing module

- None holds this machine whole: COVERAGE.tsv rows presence-and-leases and server-install name no module for the server record, and no row holds (epoch, caller, operation id) receipts with capacity refusal together. The new model is new; it takes its idempotency keying from `BusSendOnce` (current) as a pattern and replaces nothing. Reason: layer 10 is new in v1.3 and has no predecessor module to extend.

#### Interfaces

- Reads nothing above it; it is the bottom machine. Read by M1, M2, M3, M5, M6, M7. Layer below: none (layer 10).

### M5 Friend lane

#### Variables

- Per lane: idle, taken, running, needs-reconciliation, finished, failed, or released; `width`; the one server deadline; the claim it holds; late-finish records (40, round 2).

#### Actions

- Deal (an offered server assignment, no lane transition), take (atomically acquire the claim for a hooked, self-tested friend), start, finish, fail, release (40, state and transitions; guarantees 3 to 5 and 10).
- Deadline and Down move taken or running to needs-reconciliation; Reconcile ends it (40 guarantees 4 and 9).
- LateFinish: a current authenticated, hooked session of the historical holder (the friend who held the claim when it was released or reassigned) submits evidence for its immutable claim ID and execution fence; the server records it idempotently under that historical holder and holds it without advancing the current claim (40 guarantee 11).
- OneShotMember: a one-shot friend is a member lane with an identity label; its deal and take are a member's, with no link, presence or self-test gate, and its identity prompt is prepended to each brief (40 guarantee 12; 20 guarantee 19).
- Session end and session attach change no lane state (40 guarantee 4).
- Client crash changes no stored state (40, failure mode 1).
- A claim survives a session wrap (40 guarantee 4).

#### Invariants

- At most `width` lanes hold taken, running, or needs-reconciliation cards (40 guarantees 3 and 9).
- The server's deadline is the only deadline; no client clock expires a claim (40 guarantee 7).
- A crash is neither a fail nor a release (40, state and transitions).
- A lane in needs-reconciliation counts against `width` and its card is not dealt again (40 guarantees 4 and 9).
- Every valid finish is attributed to its historical claim holder; released, reassigned or superseded execution evidence is held, while unhooked or stale-session submissions cannot advance work (40 guarantee 11).
- A one-shot friend's work is credited to its identity from the server claim, and no presence, lease or link state exists for it (40 guarantee 12).

#### Liveness

- A taken card eventually finishes, fails, or enters needs-reconciliation at its deadline, and a needs-reconciliation card eventually reconciles once its holder re-hooks or the coordinator judges. Fairness: a running lane gets its run; environment: the server is serving (M4) and the lane hears the bus without nova-sprint (40 guarantee 6).

#### Reversed witnesses

- A lane that starts a second client on session attach (40 failure mode 5).
- A card returned by a session wrap (40 guarantee 4).
- A provider refusal existing only in client memory, with no judgment on the server (40 guarantee 5).
- A card released at the deadline while its run is uncertain and dealt again (40 guarantee 4; round 2, change 4).
- A finish refused as "no longer the friend's" with no record (40 guarantee 11; the 92, 21 and 8 refused reports of 20, Rationale).
- A late finish recorded under the replacement holder instead of the historical holder (40 guarantee 11).
- A one-shot friend treated as needing a link: it is never dealt, and liveness breaks (40 guarantee 12).

#### Existing module

- `FriendLane` (current, row friend-lane-take): the new model extends it and replaces nothing. Reason: it already holds take, run, report by server ids, epoch and lane, and the gone bound; v1.3 distinguishes an offered deal from taken after claim acquisition, adds needs-reconciliation and held late finishes, and separates finished from failed in the vocabulary of layer 40. A one-shot friend folds into this member lane model as a member with an identity label (40 guarantee 12), not as a separate machine.

#### Interfaces

- Reads M2, M3, M4. Read by M6. Layer below: 40, over 30.

### M6 Deal and claim

#### Variables

- The assignment and capacity claim: owner, revision, claim id, execution lease; active, needs-reconciliation, or released (10, state table).
- Dealt versus taken: a dealt card is not a taken card (50, Inputs; 40, state and transitions).
- `claimsOf(card)`: the active or needs-reconciliation claims on a card, at most one (50 guarantee 6).
- `releases`: every release with its reason, replayed to its holder on attach (50, Inputs; round 2, change 9).

#### Actions

- Deal only to a friend who is hooked, self-tested, proven and internally up (50 guarantee 7; Reconciled (a) for which clock the deal reads). Displayed up includes deaf, and deaf is not dealt a card. A one-shot friend is dealt as a member lane, outside this gate (50 guarantee 7; 40 guarantee 12).
- Down moves the friend's taken or running claims to needs-reconciliation and stops new deals to her; deaf changes no claim (40 guarantee 9; round 2, change 7).
- Tick: deals ready cards only; it never moves a taken card back to ready (50 guarantee 6).
- Take acquires the claim atomically after checking the offered assignment, current binding, one claim per card and available capacity (10, state table; 40 guarantee 10; 50 guarantee 6).
- Execution-lease expiry makes the claim needs-reconciliation and retains it (10 guarantee 9).
- Release or reassign advances the claim fence (10, state table).

#### Invariants

- A dealt card belongs to the machine until a lane takes it; dealing creates no take (50, Inputs).
- Expiry never asserts an old process stopped and never authorizes a second execution (10 guarantee 9).
- More claims than capacity are refused, including claims held for uncertain execution (10, state and transitions).
- At most one active or needs-reconciliation claim per card; a card returns to the deal only on a finish, a release, or a completed reconciliation (50 guarantee 6).
- No claim is acquired for an unhooked friend (40 guarantee 10; 20 guarantee 12).

#### Liveness

- An active claim eventually finishes, releases, or enters needs-reconciliation at its lease expiry. Fairness: the holder gets turns before the deadline; environment: the server is serving (M4).

#### Reversed witnesses

- A deal sent to a friend the server does not show reachable, changing ownership while she is deaf (50, Inputs: friends were deaf while an ownership change was sent).
- A deal gated on displayed up, reaching a deaf friend (50 guarantee 7).
- A deal that creates a take or consumes width before the lane takes it (40, state and transitions).
- Two clients acquiring the last capacity slot at once, both winning (10 failure mode: two clients attach or acquire concurrently).
- Reassignment silently giving away a claim on a turn end (10 guarantee 9).
- The tick moving a taken card back to ready and dealing it again while its run is uncertain (50 guarantee 6; 12,709 such moves in three hours on 2026-10-14, 20 Rationale).
- Down returning a running card to the deal (round 2, change 7: refused).
- A second deal of a card under an active or needs-reconciliation claim (50 guarantee 6).

#### Existing module

- No module holds the server assignment and capacity claim: the COVERAGE.tsv row presence-and-leases names none, and `FriendLane` (current) holds the client's view of a deal, not the server's claim. The new model is new on the server side and extends FriendLane's deal view on the client side; it replaces nothing.

#### Interfaces

- Reads M4, M1, M5. Read by M7. Layer below: 10, presented through 40 and 50.

### M7 Verdict and merge gate

#### Variables

- A verdict record bound to repo, head, base, and diff digest; MERGE, FIX, or HOLD.
- The fence state: gated on the exact head, read on that head, race-checked against the base, merged only by the coordinator.

#### Actions

- Bench gate on the exact head; cold read records a verdict on that head (50 guarantees 1 and 3).
- Head moves; the gate always runs again on the new full tree, and the verdict is invalidated unless the clean base-merge carry of layer 50 guarantee 1 is verified and recorded.
- Clean base merge with the card's own files and diff digest unchanged: record the structured source-review carry of layer 50 guarantee 1, but require a new bench gate on the exact resulting head.
- Coordinator merges (50 guarantee 5).

#### Invariants

- Nothing merges unless the exact final head is gated and its source review is either a direct cold read or the verified structured carry of layer 50 guarantee 1.
- A verdict quoting source outside the diff at the gated sha merges nothing (50 guarantee 2).
- No later note restates a recorded verdict as a different word; a moved tip is one note naming the new sha (50 guarantee 5).

#### Liveness

- A candidate whose gate is green, whose verdict is MERGE on the current head, and which is not behind its base eventually merges or is returned with the reason named at the fence. Fairness: the coordinator eventually runs the fence; environment: the bench is available (50, Scope: the merge gate, in order).

#### Reversed witnesses

- A stale verdict merging a moved head (50 failure mode 1).
- A verdict naming files not in the diff reaching merge (50 failure mode 2; observed failure named in the Inputs of 50).
- A carry across a base merge that changed the card's own files (50 failure mode 3).

#### Existing module

- `CardContract` (current, row card-contract): the new model extends its Judge part and replaces nothing. Reason: CardContract already holds the member's push, the judgment on a finish, and AcceptedIsNeverReaped; the v1.3 additions are the diff-digest binding and the clean-merge carry, which no existing module holds.

#### Interfaces

- Reads M6 and M4. Read by no machine; it is the top. Layer below: 50, over 40.

## Reconciled

### (a) Proof and activity

What the layers say: layer 10 (guarantees 4 and 5) holds that only a session's explicit answer
to the server's challenge is proof, and that traffic, a heartbeat, or a wake request is not;
the lease expires on the proof clock alone. The standing rule for the friends table is wider:
any valid, authenticated activity from a friend (a bus message of any kind, a take, a finish,
a push to a card branch, a pull request opened or updated, a beat, a hook proof) resets her
timeout, as any valid packet from a client resets its timeout accumulator.

The choice for the models: M1 carries two clocks. `lastHeard` is set by any valid authenticated
activity; `lastProof` is set only by a challenge answer. The displayed status reads `lastHeard`
through the internal states: up and deaf (active but not receiving: no recv or ack and no answer
to a wake ping, held up to 10 minutes before down) display as up, and deaf keeps her taken cards;
down displays as down, stops new deals to her, and moves her taken or running claims to needs-reconciliation. Down includes the case where her daemon answers and her session does not. A card returns to the deal only when its holder finishes or releases it, or reconciliation establishes the run's outcome or stop, and proposal 5 below states the same beside the wrap rule of layer 40. The deal reads session proof: a card is dealt only to a friend whose
binding is proven and unexpired and who is internally up (hooked). Deaf displays up and is not dealt a card. Delivery of a note is gated
the same way for the push: the push is offered only to a hooked binding, and a new note to an
unhooked friend is refused to its sender at once and admits nothing (round 2, change 10 and the
project owner's ruling; layer 20 guarantee 16). Notes admitted while she was hooked stay
pending and replay on attach (M3).

Round 2 applied this settlement into the layers: the two clocks in layer 10 (guarantee 13 and
the presence paragraph, proposal 1); the internal and displayed states in layer 20 (guarantee
17 and its Displayed presence states, proposal 2), so deaf and displayed now have a home in a
layer and not only here; and the claim consequences in layer 40 (guarantees 4 and 9, proposal 5
as corrected by change 7).

Whether a change is proposed to an owner: yes, proposal 1 below to the owner of layer 10 (its
"convenience up" paragraph and guarantee 5) and proposal 2 to the owner of layer 20 (its lease
states carry no deaf state and do not name the daemon-answers case of down).

### (b) Vocabulary

What the layers say: layer 10 names a binding absent, challenged, proven, away, closed. Layer
20 names a lease AWAY, CHALLENGED, PROVEN, EXPIRED, and its EXPIRED passes at once to AWAY with
a reason.

The one-to-one map: absent maps to AWAY with reason never-attached; challenged to CHALLENGED;
proven to PROVEN; away to AWAY with the recorded reason (challenge_timeout or lease_expired);
closed to AWAY with reason detached. EXPIRED is not a state in the map: it is the transition
into away, carrying its reason. The models use the layer 10 names (absent, challenged, proven,
away, closed) and carry the layer 20 reason as the away reason, so a lease expiry and a
challenge timeout are told apart by reason, never by state.

Whether a change is proposed to an owner: proposal 3, applied in round 2 (layer 20 lease
states).

### (c) The wait verb

What the layers said in round 1: layer 20 specified `nova-bus wait`, carrying a nonce, blocking on the
server, and exiting on a message (guarantee 5). The comms design carried on this branch family
specifies `nova-hook hook` to arm a link that is settled against a hard deadline and a renewable
proof, with timers and a drain cursor, and a one-shot `nova-hook wait`.

The choice for the models: the models use the one-shot `nova-hook wait` (M2 and M3 model a
one-shot wait that exits on a message or a challenge and is re-armed by its caller). `nova-bus wait` as a second blocking verb is deferred to a later release: one wait verb is modelled, and
the piggybacked proof of layer 20's guarantee 5 is modelled as the wait carrying the current
nonce to the same server operation, under whichever name ratification keeps.

Whether a change is proposed to an owner: proposal 4, applied in round 2 by change 6: layer 20
names `nova-hook wait` as its one wait verb, and layer 30's exit-wake subsections use it.
Round 3: on a monitor harness (Claude Code, and Grok as its owner measured it) the link is the
long-lived `nova-bus recv --as <name> --forever --exec <deliver>` reader of layer 20 guarantee 13,
not a re-armed wait; M2 models both reader kinds under `reader` and its bound.

## Closing table

Models are written bottom up in this order: M4, M3, M1, M2, M5, M6, M7.

| Machine | Module to write | Card |
|---|---|---|
| M4 Server commit, receipts and epoch | ServerTruth | v13-model-m4-server-truth |
| M3 Bus delivery and acknowledgment | BusDelivery (extends Bus2 and Bus2Receipts) | v13-model-m3-bus-delivery |
| M1 Presence and lease | PresenceLease (extends FriendPresence and Friend) | v13-model-m1-presence-lease |
| M2 Psychic link, hook and session | LinkSession (extends Hook; holds the BusLink states) | v13-model-m2-hook-session |
| M5 Friend lane | FriendLaneV13 (extends FriendLane) | v13-model-m5-friend-lane |
| M6 Deal and claim | DealClaim (new; extends the FriendLane deal view) | v13-model-m6-deal-claim |
| M7 Verdict and merge gate | VerdictGate (extends CardContract) | v13-model-m7-verdict-gate |

## Proposed changes for the owners

Each proposal names the file, the sentence, and the reason. All five were applied in round 2
(the ratifying friends confirm each in their own file by ratifying the round 2 sha); proposals 2
and 5 are restated below as applied, after change 7.

1. File: docs/spec/v1.3/10-server-truth.md. Sentence: in the paragraph beginning "If a
   convenience `up` field exists", state that the displayed up or down reads the last-heard
   clock through the internal states of layer 60 Reconciled (a) (up and deaf display up; down
   displays down, including the case where her daemon answers and her session does not), while
   proof, the deal, and the push gate on the challenge-answer
   clock alone. Reason: layer 10's up currently means only a current proven lease, which
   contradicts the standing friends-table rule that any authenticated activity resets the
   timeout; M1 models both clocks and needs layer 10 to name both.
2. File: docs/spec/v1.3/20-comms.md. Sentence: in the lease state machine, add the internal
   states deaf (active, not receiving, held up to 10 minutes, displays up, keeps taken cards)
   and the daemon-answers case of down (her daemon answers, her session does not; displays
   down; her taken or running claims move to needs-reconciliation, never back to the deal).
   Applied as layer 20 guarantee 17 and its Displayed presence states. Reason: the deal and the
   display read them, and layer 20's four lease states could not express them.
3. File: docs/spec/v1.3/20-comms.md. Sentence: name EXPIRED a transition into AWAY carrying its
   reason (challenge_timeout or lease_expired), not a fifth state. Reason: the one-to-one map
   of layer 60 Reconciled (b), which the models use with layer 10's names.
4. File: docs/spec/v1.3/20-comms.md. Sentence: in guarantee 5 and the interface list, the
   one-shot wait verb the models use is `nova-hook wait`; either rename `nova-bus wait` to it or
   record `nova-bus wait` as deferred to a later release. Reason: two blocking wait verbs would
   be modelled and built twice for one operation (layer 60 Reconciled (c)).
5. File: docs/spec/v1.3/40-friend-lane-client.md. Sentence: beside guarantee 4 and in the state
   list, state that down (layer 20 guarantee 17), including the case where her daemon answers
   and her session does not, stops new deals to her and moves her taken or running claims to
   needs-reconciliation, while deaf keeps them; a card returns to the deal only when its holder
   finishes or releases it, or reconciliation establishes the run's outcome or stop. Applied as
   layer 40 guarantee 9 and the needs-reconciliation state (round 2, change 7 replacing the
   round 1 down-return). Reason: M6 deals on that distinction, and returning a taken or running
   card while its run is uncertain authorizes a second execution.
