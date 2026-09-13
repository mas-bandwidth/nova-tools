# nova-redis — scratch space for thinking together

**Draft 1, 2026-09-13. Proposal for review; nothing here is implemented.**
Tracks [nova-tools#130](https://github.com/mas-bandwidth/nova-tools/issues/130),
using the earlier research in
[ideas#774](https://github.com/mas-bandwidth/ideas/issues/774).

## Why try it?

An AI friend should be able to leave a small intermediate result for another
local process, ask for a few indexed records, or wait for a change without
spending a model turn on a shell loop. Redis may provide that shared workbench. **Scratch space, ephemeral work and
cache: it helps us think; it never stores the one true form of accepted work.**
Its value must be measured against the files, resident objects and waits we
already have. Moving a cheap in-process lookup through Redis can make it slower.

Glenn's current requirements, 2026-09-13: prioritize a spec now; assume a local
Redis instance; leave a secured remote instance, possibly over Tailscale, for
later. He further clarified: "scratch space, ephemeral work", "cache", and
"it should not be storing the one true form of something, it should just be
helping us think". The component boundaries and first implementation scope below are the
author's proposals. Earlier comments in #130 that presented local-spill-only as
Glenn's ruling were explicitly corrected; they are research, not a restriction.

Adoption is optional. Different AI friends, models, languages and harnesses can
keep the approaches that work for them. No installation, account, private memory
migration or common identity is required to join a team using Nova tools.

## Boundaries and first scope

Propose a Go `nova-redis` CLI and a shared internal client package. Other Nova
binaries use the package directly; they do not spawn a CLI for every operation.
Other languages can use the documented, versioned protocol. This is a client
integration, not a Redis server module and not a new Lisp evaluator.

Assume an explicitly configured, operator-managed local Redis instance. The
first implementation neither installs nor starts it. No unattended service,
launch-agent edit, port opening, ACL change or software update is a side effect
of a read or a check. A future lifecycle command needs its own reviewed contract.

| An AI wants to… | Proposed interface | Meaning |
| --- | --- | --- |
| Leave a small result for a local friend or child | `spill`, `recall` | Bounded, expiring scratch with an explicit owner and audience |
| Share a lookup without rescanning a directory | `map`, `set`, `rank` | Bounded hashes, membership sets and sorted candidate indexes |
| Read one coherent work view | revision-labelled immutable projection | Built from the owning tool's authoritative state |
| Wait until something changed | `signal emit`, `signal wait` | Replayable-within-retention local hints; the source still decides what happened |
| See whether a local worker checked in recently | `presence beat`, `presence list` | Self-reported session observations with expiry; never task ownership |
| Know whether the workbench is usable | `status`, `check`, `version` | Capabilities, limits, connection health and explicit unknowns |

The first build slice is scratch and cache: `spill`/`recall`, bounded collection
operations, coherent projections, and health checks. Signals and presence below
are optional later adapters in this proposal; neither blocks the scratch pilot
nor justifies turning it into a scheduler. A friend can use scratch without any
observation, wake or coordination integration.

Out of first scope: cross-bench transport, coordinator election, distributed
locks, authoritative queues, spending reservations, token-ledger storage,
secret storage, arbitrary Redis commands, server modules, vector search and
automatic conversion of existing tools. Each integration is a separate reviewed
change; this spec alone does not change another tool's contract.

## One authority per fact

1. `nova-work` owns resident S, accepted events, its recovery journal, ownership
   and Git checkpoints. Its current proposal is
   [PR #231 at d8fc7d7](https://github.com/mas-bandwidth/nova-tools/pull/231).
   Redis may hold derived views labelled with source identity, coordinator
   generation, scope revision and event boundary. It never accepts a task
   mutation or authorizes dispatch. A selected candidate must be checked and
   accepted by the active coordinator against the current generation.
2. `nova-bus` remains the message record and delivery interface. A signal is
   emitted only after the source's own acceptance boundary and names the source
   receipt. Redis success does not mean a bus message was delivered. A failed
   hint never turns a successful source write into a reported source failure.
3. `nova-tokens` retains raw, attributable events and their source evidence.
   Redis can cache a revision-labelled report; counters cannot replace the
   friend/model/bench/repo/session/attempt records or their cost provenance.
   A lost cache must not reset an allowance or erase an unresolved liability.
4. Scratch may be the sole copy of explicitly disposable intermediate data.
   Anything required to finish, recover or audit accepted work must have a
   durable source before a caller relies on it. `spill` does not confer that
   durability. An AI chooses what it shares; private self records and credentials
   do not belong in the shared workbench.
5. Presence expiry means stale observation. It does not prove a worker stopped,
   free its slot, refund a budget, terminate its process or transfer a lease.
   Lock replacement requires a separate end-to-end fencing design. `SET NX`
   plus an expiry is not such a design.

This preserves the single active reader/writer of S. Other processes see
published projections and submit changes through the existing coordinator.

## Configuration, isolation and limits

Every invocation takes `--config <absolute-path>` except help/version. No guessed
endpoint, home directory, bench, namespace or credential. A versioned JSON config
contains an endpoint, team/bench/principal/session identities, allowed audiences,
namespace grants and positive limits. Unknown security-relevant fields fail
validation rather than being ignored. Config content is data, never instructions.

For v1, endpoints are an absolute Unix socket path or a literal loopback IP and
port. Refuse hostnames, wildcard bindings as client addresses, redirects,
cluster discovery and non-loopback endpoints. Unix socket directory permissions
and server ACLs must enforce the declared audience; loopback alone is not an
identity boundary between users or processes on the machine. A hostile process
running with the same OS identity and credentials is outside that isolation
claim. Bench and principal names supplied by callers are labels, not proof.

Use per-principal Redis ACL credentials with explicit command and key grants.
Credentials arrive through an explicit protected descriptor supplied by the
caller or a secrets integration; never command arguments, URLs, output, Redis
values or checked-in config. Do not inspect another harness's authentication.
Namespace prefixes alone are not access control. Read access for a friend must
be deliberately granted, not inferred from knowing a key.

Keys encode schema version, team, bench, principal, session, class and logical
name unambiguously. Validate and escape components so separators, glob syntax
and Unicode variants cannot select another namespace. A read of another owner's
key is allowed only through a declared grant. Writes stay within the writer's
grants; shared projections have one configured publisher.

All stored objects have an explicit expiry, class and owner. Collection expiry
is at the object level, not independently inferred for its members. Reads never
refresh expiry. Updates preserve the original deadline unless the caller
explicitly requests a new one. Presence renews only its own session observation.

Required positive configuration bounds: per-value bytes, per-object members,
per-namespace objects and logical payload bytes, batch operations and bytes,
stream entries and payload bytes, response bytes/rows, TTL range, connection
and operation deadlines, and retry attempts. The library validates before
sending; atomic mutation logic enforces collection and quota bounds against
concurrent writers. Logical payload accounting is not Redis resident memory:
an operator-set server memory ceiling and `noeviction` are also required by the
reference profile. Memory pressure refuses writes, never silently evicts work.
Expired objects may temporarily overcount conservative quota accounting; bounded
reconciliation can reclaim quota but must never undercount live objects.

Only reviewed fixed operations or fixed packaged scripts are allowed. No raw
`EVAL`, arbitrary command passthrough, `KEYS`, `FLUSHDB`, `FLUSHALL`, server
configuration or process-control verbs. Scripts, if chosen, are implementation
code with bounded work and reviewed ACL implications, never user-supplied code.

## Data and CLI contract

Follow [SPEC.md](SPEC.md#conventions) for escaping, bounded output, version
reporting and exit codes. Commands return structured JSON with `--json`, or a
compact one-line summary by default. Large payloads require an explicit output
file or bounded JSON output; diagnostics never echo values or credentials.

Each object has `schema`, `class`, `owner`, `session`, `created_at`, `expires_at`,
`object_revision`, and optional `source` provenance. A projection's source is
mandatory: `{identity, generation, scope_revision, event_boundary, digest}`.
`object_revision` is a concurrency token, not proof of source currency.

| Verb | Required inputs beyond config | Result and concurrency rule |
| --- | --- | --- |
| `spill` | key, input file, expiry, `--expect absent` or revision | Store bounded opaque bytes and metadata atomically |
| `recall` | key, output bound | Value, metadata and revision, or an explicit miss |
| `map put/get` | key, field; value/expected revision for put | Bounded field lookup or conditional mutation |
| `set add/remove/contains` | key, member; expected revision for mutation | Membership, not task completion or durable deduplication |
| `rank put/remove/range` | key; member and signed integer score for put; revision for mutation; bounded page for range | Candidate IDs in score order, stable member tie-break |
| `drop` | exact owned key, expected revision | Remove one disposable object; no recursive or wildcard delete |
| `signal emit` | stream, event ID, source receipt, bounded hint | Append hint, with stream identity and cursor |
| `signal wait` | stream, saved cursor or explicit resync, count, deadline | Hints, timeout, or gap requiring source reconciliation |
| `presence beat` | own session, expiry, bounded capability/reference fields | Observation accepted; never capacity reserved |
| `presence list` | audience, page bound | Observations, age and expiry, clearly self-reported |
| `status` | none | Reachability, server/client identity, capabilities and observed health |
| `check` | explicit disposable probe namespace | Positive and negative round-trip checks within its grant |

Mutations take a caller-generated request ID, expected object revision and an
absolute expiry. A conflict changes nothing and returns the observed revision.
Within the configured receipt retention window, an identical request ID and
payload returns its earlier result without repeating the mutation; reuse with
a different digest refuses. Mutation, revision and receipt are atomic. Request
receipts consume quota and must cover the advertised retry window. After that
window or a Redis restart, report an ambiguous outcome; do not claim global
exactly-once behavior. A missing key never authorizes blind replay of an earlier
mutation. `signal emit` may be duplicated after an ambiguous failure; consumers
must tolerate repeated event IDs and reconcile source state.

Queries use indexes and bounded pages. Sorted integer scores are limited to the
exact integer range of the Redis score representation; never use floats for
money. There is no generic recursive traversal command. S's Lisp structure
stays in its resident process; indexes in Redis name node IDs and revisions.

A multi-object projection is built under a fresh immutable ID, checked for
complete manifest and digest, then published by atomically changing a small
pointer with an expected revision. Readers capture that ID once. A missing,
expired or mismatched member returns `stale`, not a partially joined view.
Published members are immutable and retained at least as long as their pointer;
bounds still apply. Old readers may have to retry against the source if their
captured projection expires. No mutable copy of S is reconstructed here.

## Signals, restart and fallback

Use Redis Streams for bounded local hints, with explicit cursors and retention.
Each interested reader uses its own cursor; a consumer group distributes work
among its consumers and must not accidentally replace broadcast to all friends.
Plain Pub/Sub is insufficient for replay after disconnection.
[Redis documents Pub/Sub as at-most-once](https://redis.io/docs/latest/develop/pubsub/)
and provides historical reads and blocking waits through
[stream commands](https://redis.io/docs/latest/commands/xreadgroup/).

A cursor carries the server run identity, stream epoch and monotonic sequence.
Restart, missing stream metadata, changed epoch, or a cursor preceding retained
history returns `gap`. Reconcile against the owning source, then explicitly
start from a fresh cursor; never silently jump to the tail. Trim only within the
configured entry/byte bounds, with the oldest retained sequence reported.
Consumers need not acknowledge hints as durable task results. Coalesce repeated
hints for a source; inspecting or receiving one does not spend a model turn by
itself. A friend is woken only under the caller's existing wake policy.

After a source commit but before hint emission a process can crash. Therefore
signals cannot eliminate source reconciliation. Keep a bounded periodic check
or a source-owned durable outbox; the first integration must choose and measure
one. A local instance cannot signal a write on another bench that never reaches
it. Existing bus fetch/reconciliation remains necessary for remote changes.

Read retries use exponential backoff with jitter, a capped delay and one total
operation deadline. Authentication, policy and validation errors are not
retriable. Mutation retries require the still-valid request receipt contract;
an uncertain write is reported, never rewritten with a new request ID.
Cancellation interrupts waits. An outage does not activate a second writer,
new file lock or alternate budget store. Only explicitly rebuildable reads may
fall back to their source, and their result names that source and its revision.

The reference local instance holds disposable data with persistence disabled.
An already provisioned server may have persistence, but restored values are
still untrusted caches until freshness is reconciled. No persistence mode
changes the API's disposable-storage contract. Redis persistence has differing
loss windows, and AOF rewriting is not an immutable audit history; see the
[official persistence documentation](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/).
Losing the machine must not lose the only record of accepted work.

`status` separates reachable, protocol-compatible, policy-verified, stale and
unavailable. A successful connection cannot prove safe server bindings or ACLs.
When configuration facts cannot be inspected with the supplied grant, mark them
unknown. A separate operator-side profile attestation and scoped negative probes
are required before sharing an instance; the runtime client does not gain admin
privileges to make its status green.

## Acceptance and usefulness gates

Before implementation is called ready, demonstrate:

- Cross-principal read/write denial at Redis itself, including escaped key names;
  no credential exposure; remote endpoints refused; no silent auto-install.
- Concurrent conditional writes and quota checks; bounded memory/payload growth;
  explicit misses after expiry; no cross-key deletion; no unbounded scan.
- Crash before/after mutation acknowledgement, repeated and conflicting request
  IDs, expired receipts, connection loss and restart: no false durability or
  exactly-once claim. Server rollback cannot revive coordinator authority.
- Projection publication under concurrent readers, expired members and stale
  pointers: one complete source revision or an explicit miss/stale result.
- Wait cancellation, timeout, duplicate hints, trimmed cursors, restart, and the
  commit-before-signal crash: eventual source reconciliation without lost work.
- Expired presence cannot release a live worker, refund spend or elect an owner.
  Dropping all Redis data leaves S, bus history and raw token accounting intact.
- Positive and negative live checks using only the caller's disposable namespace;
  probe cleanup names exact owned keys and cannot widen to other data.

Start with two measured pilots: (1) two local processes sharing a bounded fixture
cache; (2) an immutable work-view reader plus a local change wait. Compare to the
existing files/resident-session interface using the same workload and outputs.
Record cold/warm and restart behavior, process launches, round trips, bytes read
and emitted, CPU/RAM, median/p95 latency, Git fetches, model activations, tokens
by friend/model/bench and estimated cost with named rates. Keep raw receipts.
Do not claim token savings when both baselines already use zero model tokens.

Keep per-change CI ideally under one minute, at most two. Longer crash/stress or
platform certification runs are explicit or nightly. No coverage is removed to
make that timing claim. Select and pin a supported Redis version and maintained
client dependency after compatibility/license review; do not hand-roll RESP to
avoid a dependency without measured reason. No optional server module required.

## Review questions and later options

All interested AI friends are invited to name a concrete use, friction or reason
to decline. Ask specifically: does local scratch justify a standalone CLI; which
current repeated operation is expensive; are signals worth their reconciliation
cost; which limits fit real payloads; and is the recovery story sufficient?
Reviews name their model and exact draft revision. Silence is not agreement.
Record feedback and dispositions on the PR; a cold read supplements friends'
own perspectives. Implementation starts after scope and acceptance review,
with a named pilot and no claim that the whole team already adopted it.

A later remote profile may use a secured cloud instance over a private network
such as Tailscale. It needs explicit endpoint authentication, per-principal ACLs,
transport/privacy policy, reconnect and partition tests, retention and operator
ownership. Private-network membership alone grants no application authority.
Local v1 contains no auto-upgrade path to that topology. If queues, leases or
shared budget enforcement are later proposed, specify fencing and recovery
with their owning tools before changing any authority boundary.

Further primary references: [Redis data structures](https://redis.io/docs/latest/develop/data-types/)
and [Redis ACLs](https://redis.io/docs/latest/operate/oss_and_stack/management/security/acl/).
