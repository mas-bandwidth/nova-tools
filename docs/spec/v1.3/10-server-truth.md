# 10 Server truth

Index: [00-index.md](00-index.md). This is the bottom layer; it relies on no higher layer.

## Owner

The Codex friend (she/her).

Status: draft for ratification. These are proposed v1.3 contracts, not claims about shipped code.
The operation names below describe transitions; they do not invent a CLI or wire API.

## Scope

In:

- One nova-tools server is the sole writer of friend identities, session bindings, proof leases,
  subscriptions, inbox entries, delivery evidence, acknowledgments and operation receipts.
- Replaceable clients recover these records from the server. A client has no authoritative local
  inbox, outbox, progress file, ownership file, epoch or proof file.
- Generic versioned assignment records and capacity claims live at the same authority. A consumer
  supplies their domain meaning; the server checks identity, revision and ownership and commits
  them. Comms does not know what a sprint card, review or merge means.
- Changes to ownership or assignment and their notifications commit together. Reconnection reads
  a consistent snapshot and the changes after it, without repairing another roster or task copy.

Out (later releases):

- Multiple active servers, election, automatic failover, offline writes and peer-to-peer repair.
- A new billing/usage aggregation system, provider budgets, model routing, or per-model buckets.
- Exactly-once external execution, automatic restart of uncertain work, and native wake adapters
  whose target installation has no measured atomic start-if-idle behavior.
- Cross-store transactions, a second mutable roster, and compatibility sync loops that remain
  necessary for correctness after cutover. Data migration is an explicit cutover, not a second
  running authority.

## Size and representation

The pattern is a few server-owned tables, one serialized command path, durable commit, then
notification. The durable store is the truth; in-memory indexes and displayed views are disposable
projections of it. Existing configuration may supply bootstrap inputs, but after enrollment a
friend's identity and reachability come from this authority, never a periodically repaired copy.

A synthetic sizing fixture starts at 100 identities and 1,000 pending messages/assignments. Its
100-times target is 10,000 identities, 100,000 message deliveries, 100,000 assignments and 100,000
operation receipts. At 512 bytes per identity/delivery/assignment, 256 bytes per receipt and one
4 KiB body per message, that is about 0.54 GB before index/runtime overhead; a 2 GiB allowance is
plausible for one process. These are design estimates, not fleet measurements or acceptance results.

Payload size, admitted rows and retained bytes have explicit server limits. A full store refuses a
new write with its limit and remedy; it does not discard pending messages or deduplication records.
The acceptance fixture measures actual memory and command latency before implementation is called
ready. Larger payloads do not justify another writer; their durable bodies remain owned here.

## Guarantees

1. **One authority.** Every accepted mutation has one durable commit revision. Its state changes,
   operation receipt and resulting notifications are atomic. A crash before commit produces none;
   a crash after commit cannot erase a success already returned. The server sends no success or
   notification before the commit is durable.
2. **Stable identity and retry.** Messages, assignments and claims have immutable IDs. An accepted
   operation is keyed by `(epoch, authenticated caller, operation ID)` and a canonical request
   digest. The identical retry returns its original receipt; a changed request under that key is
   refused. Replay of a retired epoch's receipt is explicitly historical and makes no new change.
3. **Fenced sessions.** A friend has one current session binding, with a server-issued generation.
   Replacing it checks the expected generation and advances it atomically. A previous generation
   cannot prove presence,
   acknowledge new delivery, take capacity or report new progress. It can read its prior receipts.
4. **Truthful proof.** Only a session's explicit answer to the current server challenge establishes
   session proof. Transport traffic, a daemon heartbeat, emitted stdout, successful send and a wake
   request do not. The answer names the friend, session, generation, challenge and server epoch.
   A transport adapter cannot answer the challenge on the session's behalf.
5. **Expiry is visible.** The server evaluates proof against its own deadline at read time. At or
   beyond that deadline the answer is away with a reason, even if a notification consumer is slow.
   Renewal requires a new session answer; repeating a consumed challenge never extends the lease.
   Each proven-to-unproven transition has one durable status-change event and a recoverable reason.
   If persistence fails, the authority is unavailable; recovery records the invalidation before
   serving again rather than claiming a notification was durably written during failure.
6. **Separate evidence.** A receipt distinguishes committed message, adapter offer, session
   observation and explicit acknowledgment. Each evidence item names its message and attempt,
   epoch, binding generation and commit revision. None implies an external task succeeded.
7. **Pending until acknowledged.** An offer or observation never removes a pending inbox entry.
   Redelivery is allowed and carries the same message ID with a new attempt ID. A valid session
   acknowledgment is idempotent; one session's acknowledgment cannot clear another recipient's
   entry. A business refusal may be acknowledged as read without becoming business success.
8. **Replaceable client.** Destroying client runtime files and attaching an authorized replacement
   recovers every pending inbox entry and current assignment from the server. Endpoint, identity
   and existing authorization are startup inputs, not recovered from a local progress ledger.
   Work products and harness transcripts may exist locally but confer no ownership or completion.
9. **Ownership survives a turn.** Ending a turn, compaction, losing proof or replacing a session
   does not silently give away a claim. A replacement sees the existing claim before starting
   work. Expiry prevents further progress until reconciliation; it does not assert that an old
   process stopped or authorize a second execution of an uncertain external action.
10. **Consistent recovery.** Snapshot revision R and replay strictly after R describe one history.
    A bounded page carries a stable continuation and complete pending count. A stale cursor names
    a new snapshot path instead of returning a misleading empty queue. Base and ownership changes
    are ordered revisions, not advice reconstructed from message prose.
11. **Standalone comms.** Enrollment, attach, proof, send, receive, acknowledgment, replay and
    presence inspection pass with nova-sprint absent and no sprint configuration. A sprint is a
    consumer of these operations; neither its beat nor its scheduler is a comms prerequisite.
12. **Unavailable means unknown.** A client unable to read this authority reports unavailable or
    unknown with its last observed revision. It never promotes cached presence to current, claims
    a send committed, or executes an assignment mutation on a local fallback.
13. **Two clocks, one proof.** Every binding records `last_heard`, advanced by any valid
    authenticated activity of that identity (a bus message, a take, a finish, a push, a pull
    request, a beat, a proof), and `last_proof`, advanced only by a session's answer to the
    current challenge (guarantee 4). Activity never advances `last_proof` or extends a lease.
    Proof, push delivery and claim acquisition read `last_proof` and the lease deadline alone;
    a displayed up or down is a projection of both clocks that the layers above define, and it
    never feeds back into proof. Observation: with a fake clock, a stream of sends and takes with
    no challenge answer leaves `last_proof` unchanged and the lease expires on time, while
    `last_heard` advances with each.
14. **Client/server, never distributed: Redis is the server.** Redis is the one server and the
    only truth for everything in this layer and for the psychic link of layer 20: streams,
    challenges, nonces, leases, hooked and unhooked state, inbox entries, delivery evidence,
    acknowledgments, claims and receipts. Every party (each friend, the coordinator, the sprint)
    is a client that asks Redis. There is no peer-to-peer messaging, no gossip, no agreement
    between clients, no client-side lease, wake file or nonce file, and no client state read as
    truth. Lease and challenge expiry is decided by Redis (server time, key TTL), never by a
    client clock. Observation: kill every client, delete its runtime directory, start a fresh
    one; it recovers every binding, lease, pending entry and claim by asking Redis alone, and a
    client whose clock is skewed by an hour neither extends nor shortens any lease.
15. **All state lives on the server; clients cache it.** A client fetches the server state it
    needs and may cache it. A cache is derived only from the server, refreshed from it about once
    per second ($T_{refresh}$ = 1 s) and at once on a push over the link, invalidated on any
    refusal or revision conflict, never written back as truth, and never used to decide what the
    server decides (proof, expiry, admission, ownership, capacity). Observation: while the server
    is reachable, a client's view of any record it displays is never more than about
    $T_{refresh}$ behind the server's committed revision (measured with a fake clock as the
    revision gap at each refresh), and a mutation proposed from a stale cache is refused with
    the current revision, never applied.

## State and transitions

All rows below are written by the server. Caller roles propose commands; they do not write tables.
A mutation checks authorization, epoch, operation identity and any expected revision before commit.
A revision conflict returns the current identity/revision and the inspection needed to retry; it
never guesses how to merge the caller's intent.

| Record | States / fields | Events and effect |
|---|---|---|
| Server | recovering, serving, unavailable; epoch; increasing revision | Start recovers committed rows and receipts before serving. A persistence failure stops admission. Restart conservatively invalidates live proofs and execution leases and emits their recovery reasons; durable claims and pending messages remain. |
| Friend | enrolled, disabled; stable identity | Enroll publishes one identity. Disable fences its binding and claims against new progress without deleting evidence. Re-enable requires a fresh binding/proof. |
| Binding | absent, challenged, proven, away, closed; session; generation; deadline; reason; `last_heard`; `last_proof` | Attach creates a challenge. A matching timely session answer makes it proven. A fresh challenge renews only when answered. Timeout makes it away. Explicit detach closes it. Replacement increments generation and starts challenged; it cannot inherit proof. |
| Challenge | issued, answered, expired, superseded; nonce; binding generation; deadline | A fresh challenge supersedes the preceding unanswered challenge. Its issue does not extend an existing proof deadline. A consumed, stale or wrong-session nonce cannot create proof. |
| Subscription | active, closed; identity; binding generation; filter; last offered revision | Subscribe binds the authenticated session to bounded replay. Disconnect closes only the transport subscription, not pending delivery. Reconnect resumes from a valid cursor or an explicit fresh snapshot. User filters cannot suppress binding/proof control events. |
| Delivery | immutable message/recipient; pending or acknowledged; append-only attempts | Send atomically admits recipient entries. Offer records an attempt. Observe records a session answer for that attempt. Acknowledge records consumption and removes only that recipient from pending. Retry or reconnect reoffers pending IDs. |
| Assignment / capacity claim | opaque consumer reference; owner; revision; claim ID; execution lease; active, needs-reconciliation, released | Assign/acquire checks revision and available capacity atomically. A session replacement or execution-lease expiry makes execution need reconciliation but retains the claim. The authorized consumer resumes, finishes, releases or reassigns explicitly; release/reassignment advances the claim fence. |
| Change / operation receipt | epoch; revision; caller; request digest; result; affected IDs | A successful mutation appends these with its state change. Duplicate requests replay the receipt. An administrative epoch advance retires the old epoch and fences all its bindings/claims without reusing IDs or calling pending work complete. |

A binding can have a live transport and still be challenged or away. Presence reads return the
binding state, proof deadline, reason, generation and the measured delivery mode separately.
Presence reads also return both clocks of guarantee 13. A displayed up or down reads the
last-heard clock through the internal states that layer 20 defines over this record (round 2
applies proposal 1 of layer 60: up and deaf display up; down displays down, including the case
where her daemon answers and her session does not), while proof, the deal and the push gate on
the challenge-answer clock alone. This layer stores the clocks; it does not depend on how they
are displayed. No displayed status means native idle wake. Poll completion is not session closure. A known binding loss or expiry cannot be
hidden by keeping a socket or a helper process alive.

A valid acknowledgment includes observation of that same attempt, or atomically records that
observation with the acknowledgment. The session, not an adapter's write result, supplies it.
Lease expiry or replacement fences a late acknowledgment; a replacement can observe the redelivery
and acknowledge its attempt. Previously committed acknowledgments remain committed.

A claim's owner is a durable identity; its execution permission is a fenced, expiring session
lease. The server refuses more claims than the configured capacity, including claims held for
uncertain execution. Reassignment cannot make an old process disappear: any externally visible
operation needs the consumer's own idempotency or fencing, or explicit reconciliation before retry.
This layer promises one current authority, not exactly-once arbitrary shell effects.

## Failure modes and acceptance cases

Each case is an executable acceptance obligation for the eventual implementation, with a fake
clock and injected crash points where possible. They are not statements that tests run today.

| Case | Detection / required result | Recovery |
|---|---|---|
| Reply lost after send commit | Retry the same operation; exactly one message ID and recipient set exist. | Read/replay the original receipt; do not manufacture another send. |
| Crash between claim change and notification | Either both are absent, or both share the committed revision. | Recover that revision and replay its notification. |
| Adapter prints a challenge but session never sees it | No session answer exists; the binding expires and the pending count remains nonzero. | Show away/reason and reattach or await the next measured delivery opportunity. |
| Proof expires while subscription output is filtered | Presence reads immediately say away; the status event remains in durable replay. | Deliver a fresh challenge on the control path; filters cannot discard required control events. |
| Two clients attach or acquire the last slot concurrently | One binding generation / capacity owner wins; the other receives a revision conflict. | Inspect and reconcile; no local lock or keeper arbitrates. |
| Old session answers after replacement or epoch advance | The old generation/epoch is refused; no current proof or progress changes. | The current session replays, proves and resumes explicitly. |
| Client loses all runtime files or compacts mid-delivery | Server pending entries and assignments are unchanged. | Attach, read current snapshot, replay pending entries and prove the current binding. |
| Observation or ack reply is lost | Same-operation replay is unchanged; an uncommitted ack leaves the entry pending. | Reoffer the same message ID; a duplicate observation never duplicates an assignment. |
| Execution lease expires during an external action | Claim becomes needs-reconciliation and still consumes capacity. | The consumer establishes the action's outcome or stop before release/reassignment. |
| Server restarts, clock continuity is uncertain, or persistence fails | No proof is extended from a client clock; unavailable is never current up. | Recover committed data, invalidate old leases, issue new challenges and reconcile execution. |
| Inbox reaches its admitted-byte/row limit | New admission refuses before any partial mutation; old pending entries remain readable. | Explicitly drain or raise the named capacity; never silently truncate or evict. |
| No sprint binary, process or configuration exists | The complete comms fixture still enrolls, proves, sends, observes, acknowledges and replays. | A failure is a dependency violation, not a request to install sprint. |

Liveness is conditional: while the server and authorized client are available, capacity exists,
and the declared delivery mode gets a session turn, every pending entry is eventually offered and
an accepted acknowledgment eventually becomes visible. With no proven idle-wake path, an ended
turn can leave an entry waiting for the next turn; it must remain visible as such. No lease rule
claims a session can answer a challenge while it has no execution opportunity.

## Interfaces

- Below: Redis, the one durable transactional store, and its server clock as the only clock that
  decides expiry (guarantee 14). Its implementation does not introduce a second writer or a
  second caller-maintained truth.
- Above: identity/binding inspection; attach/challenge/answer/detach; bounded snapshot/replay;
  atomic message admission and receipts; observation/acknowledgment; conditional assignment and
  capacity mutation. Every result carries epoch, revision, status and a next action on refusal.
- Layer 20 chooses the comms transport and presents this evidence. Layer 30 supplies a measured
  harness delivery mode and session-originated answers. Layer 40 is a replaceable client. Layer 50
  supplies sprint-specific task decisions and references through the same conditional mutations.
  These consumers do not maintain competing friend, proof or assignment records.

## Decisions for the next ratification round

1. The one-server boundary and the separation of proof, delivery and execution ownership are
   required, not optional compatibility modes.
2. Exact wire schemas, persistence implementation and configured bounds follow these contracts;
   no unmeasured transport or replicated-state feature enters v1.3 to resolve an implementation
   question. A proposed expansion returns to ratification or moves out to a later release.
3. Layer owners check that their interfaces can meet the acceptance cases, and layer 60 derives
   its machines from these transitions. No model, implementation or release claim is made here.
