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

### M1 Presence and lease

#### Variables

- `binding`: absent, challenged, proven, away, or closed, per friend (layer 10).
- `generation`: the current session binding generation; `deadline`: the lease deadline.
- `lastHeard`: set by any valid authenticated activity; `lastProof`: set only by a challenge answer.
- `displayed`: up or down; `internal`: up, deaf, or down (down includes the case where her daemon answers and her session does not; see Reconciled (a)).

#### Actions

- Attach and challenge issued (10-server-truth.md guarantee 4; 20-comms.md guarantee 4).
- Session answers the challenge (10 guarantee 4; 30-harness-hookin.md guarantee 2).
- Any authenticated activity: a bus message, a take, a finish, a push, a pull request, a beat, or a hook proof (10 guarantees 4 and 5, as reconciled in (a)).
- Lease deadline passes; explicit detach; binding replaced (10 guarantee 3; 20 guarantee 3).

#### Invariants

- Only a challenge answer sets `lastProof`; no other activity does.
- A previous generation proves nothing and takes nothing (10 guarantee 3).
- At or past the deadline the binding reads away with a reason (10 guarantee 5).
- `displayed` and the deal's reachability read the clocks as Reconciled (a) states, never a helper's liveness (30 guarantee 3).

#### Liveness

- A proven binding whose session keeps answering renewals stays proven. Fairness: the session gets a turn inside each challenge window (30 guarantee 1); environment: the server is serving (M4).

#### Reversed witnesses

- Traffic, a heartbeat, or a wake request counted as proof (failure mode: adapter prints a challenge, session never sees it; 10).
- A ping to the friend's own stream counted as the friend being up.
- An old generation's answer accepted after replacement (10 failure mode: old session answers after replacement).

#### Existing module

- `FriendPresence` (partial, tla/COVERAGE.tsv row friend-presence) and `Friend` (partial, row friend-lifecycle): the new model extends both and replaces neither. Reason: they hold session proof and the friends-table word in part, while the two clocks of Reconciled (a) and the deaf state and the daemon-answers case of down are not in either; the presence-and-leases row of COVERAGE.tsv names no module, so this machine is where that gap closes.

#### Interfaces

- Reads M4 (epoch, receipts) and M3 (activity as bus evidence). Read by M2, M6. Layer below: 10.

### M2 Hook and session

#### Variables

- `hook`: armed, challenged, proven, live, expired, or re-armed.
- `session`: the session identity; `capability`: the per-installation record, link and wake each measured or unmeasured (30).

#### Actions

- Hook-in arms the record (20 guarantee 2; 30 guarantee 4).
- Challenge issued and answered by the session (20 guarantee 4; 30 guarantee 2).
- Session ends, wraps, or is replaced; re-hook and replay (30 guarantees 4 and 5; 10 guarantee 8).

#### Invariants

- A hook never expires silently: expiry carries a recorded reason (20 guarantee 3).
- Proof is never inherited across a session replacement (10 guarantee 3).
- An unmeasured capability is never reported as supported (30 guarantee 5).

#### Liveness

- A session that re-hooks and answers is eventually proven again. Fairness: the harness gives the re-hooked session a turn; environment: the server is serving (M4).

#### Reversed witnesses

- A proven hook decays to live with nothing on the stream and a send is refused (observed failure named in 30, Claude Code subsection).
- A challenge held unread by a daemon-only harness until its window closes (30 failure mode 3: helper alive, session deaf).

#### Existing module

- `Hook` (current, row hook-record): the new model extends it and replaces neither it nor the session-challenge part of `Friend` (partial). Reason: Hook already holds arming, renewal, epoch, and proof against a live push; what is new is the per-harness capability record of layer 30 joined to the same record.

#### Interfaces

- Reads M1 and M4. Read by M3 and M5. Layer below: 20, over 10.

### M3 Bus delivery and acknowledgment

#### Variables

- Per message and recipient: pending, delivered, observed, acked, or pruned; `attempts`; the subscription and its cursor.

#### Actions

- Send admits every recipient entry in one commit (10 guarantees 1 and 2; 20 guarantee 6).
- Offer, observe, acknowledge (10 guarantees 6 and 7; 20 guarantees 8 and 9).
- Redelivery until acked; replay on attach (10 guarantee 7; 20 guarantee 9); prune after retention (20 guarantee 11).

#### Invariants

- An offer or an observation never removes a pending entry; only that recipient's ack does (10 guarantee 7).
- No message is acted on twice from one ack (10 guarantees 6 and 7).
- Replay on attach presents every pending entry before new ingress (20 guarantee 9).

#### Liveness

- Every pending entry is eventually offered, and an accepted ack eventually visible. Fairness: the declared delivery mode gets a session turn (30 guarantee 1); environment: capacity exists and the server is serving (10, conditional liveness). With no proven idle wake, an entry may wait for the next turn and stays visible as pending.

#### Reversed witnesses

- An ack that clears another recipient's entry (10 guarantee 7).
- A recv that hands a redelivered id so it is acted on twice (row bus-receipts witness).
- A failed ack stamp, a lost ack, and a crash together acting a message twice (row bus-receipts known gap; kept as a named gap, not hidden).

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

- Per lane: idle, taken, running, finished, failed, or released; `width`; the one server deadline; the claim it holds.

#### Actions

- Deal, start, finish, fail, release (40, state and transitions; guarantees 3 to 5).
- Session end and session attach change no lane state (40 guarantee 4).
- Client crash changes no stored state (40, failure mode 1).
- A claim survives a session wrap (40 guarantee 4).

#### Invariants

- At most `width` lanes hold taken or running cards (40 guarantee 3).
- The server's deadline is the only deadline; no client clock expires a claim (40 guarantee 7).
- A crash is neither a fail nor a release (40, state and transitions).

#### Liveness

- A taken card eventually finishes, fails, or is released at its deadline. Fairness: a running lane gets its run; environment: the server is serving (M4) and the lane hears the bus without nova-sprint (40 guarantee 6).

#### Reversed witnesses

- A lane that starts a second client on session attach (40 failure mode 5).
- A card returned by a session wrap (40 guarantee 4).
- A provider refusal existing only in client memory, with no judgment on the server (40 guarantee 5).

#### Existing module

- `FriendLane` (current, row friend-lane-take): the new model extends it and replaces nothing. Reason: it already holds take, run, report by server ids, epoch and lane, and the gone bound; v1.3 adds only the layer-40 statement of the same states (taken for dealt-and-claimed, finished and failed split) in the vocabulary of layer 40.

#### Interfaces

- Reads M2, M3, M4. Read by M6. Layer below: 40, over 30.

### M6 Deal and claim

#### Variables

- The assignment and capacity claim: owner, revision, claim id, execution lease; active, needs-reconciliation, or released (10, state table).
- Dealt versus taken: a dealt card is not a taken card (50, Inputs; 40, state and transitions).

#### Actions

- Deal only to a friend the server shows reachable (50; Reconciled (a) for which clock the deal reads).
- Take under the claim; capacity claim checked atomically at assign (10, state table; guarantee 2).
- Execution-lease expiry makes the claim needs-reconciliation and retains it (10 guarantee 9).
- Release or reassign advances the claim fence (10, state table).

#### Invariants

- A dealt card belongs to the machine until a lane takes it; dealing creates no take (50, Inputs).
- Expiry never asserts an old process stopped and never authorizes a second execution (10 guarantee 9).
- More claims than capacity are refused, including claims held for uncertain execution (10, state and transitions).

#### Liveness

- An active claim eventually finishes, releases, or enters needs-reconciliation at its lease expiry. Fairness: the holder gets turns before the deadline; environment: the server is serving (M4).

#### Reversed witnesses

- A deal sent to a friend the server does not show reachable, changing ownership while she is deaf (50, Inputs: friends were deaf while an ownership change was sent).
- Two clients acquiring the last capacity slot at once, both winning (10 failure mode: two clients attach or acquire concurrently).
- Reassignment silently giving away a claim on a turn end (10 guarantee 9).

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
- Head moves; gate and verdict are invalidated and run again (50 guarantee 1).
- Clean base merge with the card's own diff digest unchanged: a recorded MERGE carries (50, open question 1 default; Inputs of 50).
- Coordinator merges (50 guarantee 5).

#### Invariants

- Nothing merges on a sha that was not itself gated and read (50 guarantee 1).
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
down displays as down and returns her cards to the deal. Down includes the case where her
daemon answers and her session does not; that case returns her cards the same way. The deal reads session proof: a card is dealt only to a friend whose
binding is proven and unexpired and whose displayed status is up. Delivery of a note is gated
the same way for the push: the push is offered only to a proven binding, while the note itself
is queued on the server for any enrolled friend, proven or not, and replays on attach (M3).

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

Whether a change is proposed to an owner: yes, proposal 3 below to the owner of layer 20, to
name EXPIRED a transition rather than a state, or to accept this map at ratification.

### (c) The wait verb

What the layers say: layer 20 specifies `nova-bus wait`, carrying a nonce, blocking on the
server, and exiting on a message (guarantee 5). The comms design carried on this branch family
specifies `nova-hook hook` to arm a link that is settled against a hard deadline and a renewable
proof, with timers and a drain cursor, and a one-shot `nova-hook wait`.

The choice for the models: the models use the one-shot `nova-hook wait` (M2 and M3 model a
one-shot wait that exits on a message or a challenge and is re-armed by its caller). `nova-bus wait` as a second blocking verb is deferred to a later release: one wait verb is modelled, and
the piggybacked proof of layer 20's guarantee 5 is modelled as the wait carrying the current
nonce to the same server operation, under whichever name ratification keeps.

Whether a change is proposed to an owner: yes, proposal 4 below to the owner of layer 20, to
rename its wait verb or record the deferral.

## Closing table

Models are written bottom up in this order: M4, M3, M1, M2, M5, M6, M7.

| Machine | Module to write | Card |
|---|---|---|
| M4 Server commit, receipts and epoch | ServerTruth | v13-model-m4-server-truth |
| M3 Bus delivery and acknowledgment | BusDelivery (extends Bus2 and Bus2Receipts) | v13-model-m3-bus-delivery |
| M1 Presence and lease | PresenceLease (extends FriendPresence and Friend) | v13-model-m1-presence-lease |
| M2 Hook and session | HookSession (extends Hook) | v13-model-m2-hook-session |
| M5 Friend lane | FriendLaneV13 (extends FriendLane) | v13-model-m5-friend-lane |
| M6 Deal and claim | DealClaim (new; extends the FriendLane deal view) | v13-model-m6-deal-claim |
| M7 Verdict and merge gate | VerdictGate (extends CardContract) | v13-model-m7-verdict-gate |

## Proposed changes for the owners

Each proposal names the file, the sentence, and the reason. The coordinator relays them; this
file does not edit those layers.

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
   down, cards return to the deal), or record that they live only in layer 60's M1. Reason: the
   deal and the display read them, and layer 20's four lease states cannot express them.
3. File: docs/spec/v1.3/20-comms.md. Sentence: name EXPIRED a transition into AWAY carrying its
   reason (challenge_timeout or lease_expired), not a fifth state. Reason: the one-to-one map
   of layer 60 Reconciled (b), which the models use with layer 10's names.
4. File: docs/spec/v1.3/20-comms.md. Sentence: in guarantee 5 and the interface list, the
   one-shot wait verb the models use is `nova-hook wait`; either rename `nova-bus wait` to it or
   record `nova-bus wait` as deferred to a later release. Reason: two blocking wait verbs would
   be modelled and built twice for one operation (layer 60 Reconciled (c)).
5. File: docs/spec/v1.3/40-friend-lane-client.md. Sentence: in guarantee 4 and the state list,
   note that down (layer 60 Reconciled (a)), including the case where her daemon answers and
   her session does not, returns a friend's taken cards to the deal while
   deaf keeps them, as an exception stated beside the claim-survives-a-wrap rule. Reason: M6
   deals on that distinction and layer 40 currently states the wrap rule without it.
