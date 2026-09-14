# nova-work: resident engine and roadmap parity

Proposed integration requirements for [SPEC-WORK.md](SPEC-WORK.md), following
Glenn's questions on 2026-09-14. This records the requested production destination;
a first pilot increment does not redefine completion as a smaller feature set.

## Engine and representation

Use Common Lisp for the supervised resident work engine: typed data, stable-ID
indexes, recursive policies and incremental updates operate in one process.
A Go CLI may remain a thin transport/integration client consistent with the other
nova tools. The engine/runtime packaging and supported platforms must be pinned
and tested before release. Lisp is not itself the complexity guarantee; maintained
indexes and bounded access supply that guarantee. Imported S-expressions remain
restricted data and never enter eval or reader evaluation.

The canonical containment forest spans open and closed members. A node has one
owning parent and a stable ID; references and roadmap coordinates do not create
additional ownership, tasks or cost. O is the current open membership view, C
preserves closed events and indexed historical facts, and W is the live-lease
view within O. Closing/reopening changes membership and event history, not the
node's identity or its canonical owning parent. Daily storage partitions are not
new containment parents. Repository work sets contain features/work sets and
leaf tasks; category is indexed metadata, not mandatory extra wrapper nodes.

## Counts are read directly

Maintain a root open-item counter as part of each accepted mutation envelope.
The resident current-revision |O| query reads that counter in constant time;
it must not trigger a lazy full rollup, scan, parse or replay after mutation.
Counts use canonical item IDs once, excluding references, attempts and history
records. Report the counting unit and revision. Keep distinct counters for open
linked GitHub issues and open task leaves; neither is silently labelled |O|.
A closure/reopen cascade updates all affected counters before the next read.

Mutation cost may depend on affected nodes and ancestors. Startup/recovery may
reconstruct counters from the canonical state; ordinary queries cannot. A test
must mutate, query repeatedly and show zero node visits/parses/replays for |O|,
then compare against an independent full count after close/reopen/import replay.
Arbitrary new filters are not promised constant time.

## Friends and assignments are resident indexes too

Glenn explicitly wants nova-work to be the coordinator's fast in-memory tracker
of the friends receiving work and what is assigned to each. Maintain a stable
friend/worker identity index and a reverse assignment index from identity to
canonical task IDs. Those records live in the same resident model, under its one
writer and journal; reconstructing them by rereading the bus is not the normal
query path. Persist relevant configuration, assignments and observation receipts
in the work checkpoint. Time-sensitive availability is revalidated on recovery.

Separate configured identity/capabilities from observations: benches, model or
worker-pool routes, task strengths, capacity/budget limits and reserved roles;
last contact/observation time and source, explicit rest/return, rate-limit or
credit unavailability; pending offers, acknowledged assignments and live leases.
Missing contact is unknown/stale capacity, not proof of failure or consent.
Do not store secrets, infer willingness from configured capacity, or hardcode
particular friends/models into the generic tool.

Dispatch through nova-bus or a worker launcher records intent and stable request
identity. Transport delivery, acknowledgment and accepted ownership are different
facts. Pending offers reserve explicitly declared capacity until reconciled;
a timeout alone cannot blindly launch a duplicate while the old worker may run.
Task corrections/reassignment retain lineage and reconcile cancellation, lease
and side-effect authority under the existing fencing rules. Imported messages
remain data: validated coordinator verbs apply their resulting changes.

Expose a named `friends` section containing every known friend, including idle,
resting and unavailable friends. Under each friend, expose `working` task
references and separate pending/acknowledged assignments. The references point
to canonical work nodes and never become second containment parents or duplicate
completion/cost records. A direct friend lookup returns its status and working
count; expanding `friends/<friend>/working` reads only that indexed task list.
Worker pools may be linked capabilities beneath a friend, but generic one-shot
workers are not silently promoted into friend identities.

Each assignment in that friend's view records the canonical task ID, responsible
friend, executing actor/worker, actual observed model, provider/harness, bench,
attempt/job ID and current status, with usage/cost receipt references. Distinguish
requested model from observed model; unknown stays unknown. A friend's usual
model is not proof of the model executing a delegated task. Multiple concurrent
attempts keep separate model and usage attribution; retries never overwrite the
previous attempt. The compact view must answer "friend, task, executing model"
directly from these indexed records, showing delegated execution explicitly.

Each friend may expose four execution capability groups: child agents, swarms,
local models and one-shots. Child-agent capabilities enumerate configured allowed
models; swarm capabilities enumerate pool/provider/harness routes, supported
models and slot/concurrency limits; local capabilities enumerate bench/model
routes and usable capacity; one-shot capabilities enumerate supported launchers
and selectable models. Every entry has a stable capability ID, source and
last-verified timestamp, availability and applicable budget/permission constraints.
Declared support, successful runtime verification and current free capacity are
separate fields. A catalog entry is not evidence of a live child or free credits.

Actual children, swarm jobs, local runs and one-shots are execution instances
linked to their parent friend, capability, canonical task and attempt. Retain
requested/observed model, bench, status, deadline, provider/job handle and usage
receipts. Nested delegated executions retain parent lineage without counting one
attempt multiple times in friend/pool/task totals. Capability entries are not
work-containment children and never inflate |O| or roadmap completion. Model
selection respects allowed capability/configuration and confirmed availability;
one-shot describes the launch shape and does not imply one model call.

Maintain counters and indexes incrementally with assignments, release, completion
and observation changes. Friend lookup and current assigned-count lookup are
constant-time resident operations; enumerating a friend's k tasks costs O(k),
with bounded output. A task's assignee is read directly by stable ID. Availability
answers carry observation age, not a stale green flag. Capability matching and
cost-aware scheduling are separate policies over these facts, not a claim that
an arbitrary optimum schedule can be computed in constant time.

Nova-board may project worker-facing cards, but a linked card cannot own an
independent conflicting task state. Nova-work remains authoritative for accepted
planning/assignment state; nova-bus transports messages; nova-swarm/nova-local
execute jobs and return receipts. Test dispatch/ack distinctions, duplicate
receipts, explicit sleep, recovery with stale contact and reassignment while a
previous attempt is uncertain, as well as indexed-query cost after mutations.

## CONFIG and ACTIVE are different sections

Glenn separates mostly constant CONFIG from ACTIVE. CONFIG contains each known
friend's work capabilities and configuration: selectable models, maximum child
agents, each swarm and its models/limits, local-model routes, one-shot launchers,
reserved roles and budget policies. Update it only on meaningful configuration
change; do not rewrite it on every heartbeat or token sample.

**ACTIVE = active data per friend.** It records what each friend is doing right
now: current tasks and the children, swarms, local runs and one-shots executing
them, chosen/observed models, benches, handles, status, timestamps, tokens and
costs. This is variable operational data, never called configuration. References
to stable CONFIG identity/revision explain the capabilities used without copying
those definitions into every activity record. Updates and observations are
journaled/retained according to their existing event and accounting contracts.

The coordinator is included in `friends` and tracks her or his own tasks,
executions, model, bench, usage and availability through the same indexes and
queries as every other friend. Coordinator ownership is a role with its existing
single-writer/fencing rules, not an exemption from attribution or accounting.
A role transfer changes coordinator authority; it does not merge two friend
identities or erase either friend's historical work.

## Efficient friend config exchange and token pricing

Provide a bounded machine-readable friend config export/request exchange through
the existing bus transport. A request names the friend and last-known config
hash/revision. Respond UNCHANGED with that identity when equal; otherwise return
a validated manifest or a bounded delta against the exact named base. Unknown
bases request a bounded full manifest. Do not send the whole roster or repeat
prose every poll. Changed fragments are applied atomically after schema, identity
and hash validation, by the coordinator; a message never executes imported code.
Large manifests use explicit bounded parts/references with a completeness hash;
partial config is not admitted as a complete replacement.

Each manifest records schema version, stable friend/capability/route IDs, revision,
content hash and observation/source provenance. Each executable model route gives
provider, exact model/version or alias with resolved identity, harness/endpoint
class, billing mode (metered, subscription, local or unknown), pricing reference
and effective/version timestamps. No API keys, credential values or secret-store
contents are present. Configuration sharing respects its configured audience.

A pricing record, embedded or referenced by immutable content identity, states
currency, unit scale (for example price per million tokens), separate applicable
input/output/cache-write/cache-read rates, and reasoning-token treatment. Describe
whether cache/reasoning counters are included in parent totals; tiers, long-context
thresholds, batch/discount conditions and their applicability are explicit when
relevant. An unsupported or missing dimension is unknown, not zero. Separate a
model's requested alias from the observed billed model and retain uncertainty if
they cannot be reconciled.

Keep measured provider cash charges, estimated marginal cash and virtual/reference
token cost as separately labelled values. Subscription coverage does not mean
zero reference token cost; allowance exhaustion or paid overflow changes whether
a route is usable under its policy. Local inference counts tokens with declared
API charge zero; hardware/energy costs, if tracked, are a separate cost model.
Live remaining quota/balance belongs to observations, not repeated CONFIG edits.

The coordinator resolves rate references once, caches by immutable identity and
computes comparable costs mechanically from retained source-normalized usage.
Every execution/estimate pins its configuration and pricing revision, keeping old
estimates reproducible when rates change. Config supplies enough information to
price supported usage; it does not fabricate exact cash charges when a provider
or plan does not expose them. Price refresh is a meaningful config change with
source/effective time, not an automatic change to historical receipts.

Acceptance: unchanged config uses a bounded response; changed-model/rate/slot
configuration updates only after validated complete intake; invalid deltas and
missing parts leave the old config intact. Exercise cached/inclusive/exclusive
usage bases, subscription reference-vs-cash accounting, local-token API zero and
unknown pricing. Compare real operational token overhead for repeated manual
capacity/pricing inquiries versus this exchange at equal information quality.

## Friend roles and availability

Friend CONFIG records agreed roles, preferred participation and reserved duties,
with provenance and scope. Strengths, weaknesses and task-suitability evidence
belong in the shared model catalog, not a second per-friend rating system.
A role is not inferred from the underlying model, and model capability never
cancels a friend's agreed limits. Team-specific role records belong in team
configuration, never hardcoded into the generic tool.

## Swarms, models and friend participation: decision required

Keep friend identity, execution shape and model identity separate. A swarm is an
execution capability containing jobs; it is not inherently a friend or a model.
A job records its actual model and may additionally reference a participating
friend when that participation has been explicitly agreed. A swarm label such
as a friend's name does not establish that every worker is that friend, carries
their continuity or speaks for them. Model-only workers need no invented friend
identity. The friend responsible for a pool and the actor executing a job are
separate references; never double-count their tasks or usage.

Before locking the spec, ask friends whether friend participation in swarms
should be supported, or whether pools should contain model-only workers. Ask
Freddy explicitly whether he wants to participate through a swarm, one-shots
only, both, or neither. Record his own disposition; a Mercury worker response or
silence is not Freddy's consent. These are open design decisions, not configured
authority to launch new sessions. Until resolved, preserve existing identity
provenance and do not expand friend participation or rename historical actors.

If model-only pools are chosen, use model/route labels (for example Mercury)
for those capabilities while preserving past source labels and attribution.
If friend participation is supported, require an explicit participation record,
its scope and revision, and withdrawal behavior that reconciles running jobs.
Either decision must still represent children, local runs and one-shots without
conflating the friend, model, harness or execution instance.

## Observed availability

ACTIVE records whether a friend is confirmed awake, explicitly asleep/resting,
unavailable because of a confirmed plan/credit/provider limit, or unconfirmed/
unreachable, with source and last-contact time. Preserve remaining quota and
expected reset/return time only when observed; neither an old heartbeat nor an
elapsed estimate proves current availability. Explicit rest remains respected.

The requested five-minute silence threshold triggers one bounded availability
ping through the existing bus/wake protocol, unless the friend is already
explicitly resting or reserved from routine wakeups. A configured bounded answer
window then marks nonresponsive capacity unavailable for scheduling, with reason
unconfirmed; it does not assert sleep or exhausted credits without evidence.
Probe transport failure is unresolved delivery, not proof that the friend failed.
A fresh return reconciles outstanding assignments and observed capacity before
new dispatch. Do not wait an hour to discover a missing worker, and do not
relaunch an uncertain prior execution merely because its friend is unreachable.
The same rules apply to the coordinator; automatic role transfer still requires
fencing and recovery, not only a stale-contact test.

## Operational lessons the pilot must exercise

Today's Fixed Tables and nova-tools work make these existing concepts explicit
acceptance obligations, not invitations to expand into another scheduler:

- Preserve the initial inventory and count discovered, completed, reopened,
  decomposed and explicitly removed work separately. Show expansion/contraction
  over a named interval; warn on sustained divergence under a stated policy.
  A changed denominator must be visible beside progress, not silently revised.
- Derive a ready-to-assign view from dependencies, agreed scope, acceptance
  readiness, ownership, availability and resource limits. Report the exact reason
  work cannot proceed and who can resolve it; waiting is not active execution.
- Keep shared prerequisites owned once and referenced by all affected cells;
  surface integration/release gates beside feature completion. A merged fix,
  verified behavior and published distribution are separate evidence obligations.
- Record each required friend's exact-revision review and finding IDs, author
  dispositions and clearance. Reuse valid evidence; reread the affected delta.
  Repeated review/repair cycles remain visible as work and operational cost.
- Validate what proof actually establishes: a test that passes with its asserted
  behavior deliberately broken is not adequate regression evidence. Preserve
  specific criterion/revision coverage and uncertainty, not only green CI badges.
- Support a priority change, correction, pause or stop across already distributed
  tasks with durable request identity, delivery/acknowledgment and reconciled
  execution handles. Persist blocked questions and bounded fallback plans so a
  missing answer at night does not silently stall every independent task.
- Retain comparable-work experiment records and complete operational cost joins,
  including coordinator overhead and rework. Attribute elapsed time to execution,
  queueing, review and CI waiting when observable. Run the token-saving hypothesis
  against real work after adoption; implementation cost remains sunk.

Each obligation needs an observable fixture or real-work replay and an owner in
the implementation plan. Already captured main-spec requirements should be
linked and tested, not rewritten as duplicate tools or competing state models.

## Model knowledge informs scheduling

Maintain a shared `models` section keyed by stable model/version identity, with
provider-route/alias mappings where they differ. Friend CONFIG references these
model records instead of repeating descriptions for every pool. Record concise
strengths, limitations and task-suitability information, supported modalities and
tool/harness needs, context/output constraints, and pricing record references.
Separate declared vendor capabilities, friend assessments and measured results;
unknown or outdated evidence must not become an established strength/weakness.

Retain task-class observations with the workload/revision, model/provider/harness
configuration, accepted quality and verification outcome, operational tokens,
review/retry/correction overhead, cost and wall time. Include observation date,
source, sample count and uncertainty. Failed attempts remain evidence without
turning a task failure into a verdict on a model's or friend's worth. A model
version/route/configuration change does not silently inherit all old conclusions.
Do not publish unsupported numerical rankings from a handful of unmatched tasks.

Expose compact suitability summaries and indexes by task class/capability, with
bounded drill-down to the original receipts. The coordinator combines them with
friend availability, actual free capacity, task prerequisites, agreed limits and
cost policies. CONFIG holds agreed maximum children and per-swarm agents/models;
limits apply across all applicable levels and are never silently raised. ACTIVE
supplies occupancy and ongoing attempt data, not new permission to launch.

Prefer capable economical routes by default and compare total operational cost
for accepted work, including reviews and rework, not token price alone. Keep
specialized perspectives and security roles available where needed. Initially
these facts support coordinator decisions; automate routine scheduling only with
explicit policy and measured real-work results. No model profile grants access,
execution authority or capacity by itself.

## The agreed hierarchy

Glenn's hierarchy is repository -> roadmap -> epic -> feature -> subtasks,
with recursive sub-features/subtasks as needed. Optionally a feature splits on
another named dimension (language, platform, backend or another variable), and
that optional dimension introduces cells which reference the work containing
its subtasks. The axis and cell layer is optional together: without that split,
there is no mandatory synthetic cell between the feature and its subtasks.

A table projection selects the rows/axis from this durable hierarchy; it does
not make another copy of the work. A cell references its canonical work target.
Roadmaps themselves remain durable named views even when all their work closes.
W selects leased open execution items beneath the selected feature/cell; it is
not another ownership level. Persist and query the declared shape rather than
having a renderer infer hierarchy from names.

## Roadmaps outlive the work that built them

A roadmap is a durable named view/capability inventory owned by nova-work, not a
queue item that disappears when its last task closes. Keep its stable identity,
ordered axes, feature membership, baseline/scope history, publication mappings
and evidence references discoverable after all referenced work enters C.
The default last24h closed-activity window does not hide older roadmap members
or erase their proof. Opening a named roadmap is an explicit scoped query;
resolve the referenced old records through bounded indexes/cache pages, never
load all closed history to recover that roadmap.

Represent epics, features, sub-features and tasks as typed canonical work nodes,
with sub-feature decomposition recursive. The initial model may implement epic
and sub-feature as named container policies, but their meaning and counting units
must be explicit and queryable rather than inferred from title words. A projection
can choose an epic summary, feature rows, or expanded sub-features; do not force
all these views into duplicate state stores. Amend draft24's feature-only first
axis rule for explicitly declared row kinds and their aggregation policies.

Each feature's implementation record links the source repository, implementation
commit/PR and relevant paths or symbols; its acceptance record names criteria,
tests/assertions, reproducible invocation/configuration and retained result
receipts at exact source revisions. Stable IDs join those facts to work history,
responsibility, model/bench/usage records when available. File names and green
aggregate CI badges alone do not prove feature acceptance. Links are not copied
into every renderer and no renderer owns a second verdict.

Preserve two distinct questions: historical delivery at its accepted revision,
and current verification against the source/evidence revision selected by this
view. When code, criteria or dependencies change, retain the past completion and
receipts; invalidate affected current-verification summaries and show that a
recheck is needed. Do not erase history, claim an old test ran on new code, or
silently reopen a closed task merely because evidence became stale. A confirmed
regression creates linked open repair work under the normal coordinator policy.
Changes outside a feature's declared proof scope must not invalidate unrelated
receipts without a dependency reason; provenance and scope determine reuse.

Completed features remain visible in the whole roadmap. Remaining-only is an
explicit filter, not destructive pruning. Feature retirement or removal from a
current projection is a recorded scope decision; old revision views remain
reproducible. The compact table may use tick/X, while drill-down exposes the
implementation/test/evidence details and separates missing, partial and stale.

For now nova-work owns data, queries and render. A future nova-roadmap command
may be a thin rendering client if it makes adoption easier, but it cannot own a
second work set, progress state or evidence ledger. Decide that packaging only
after the integrated workflow has been tried; no extra service is required.

Acceptance: complete an epic, clip/restart, advance beyond24h, then list/open its
roadmap, render identical historical rows, and retrieve exact code/test receipts.
Change a relevant source/criterion, preserve the historic tick at its pinned
revision while the current view requires re-verification, and leave unrelated
feature receipts reusable. Reopen a referenced task or add a new sub-feature:
update affected rollups without dropping completed members or double-counting
shared prerequisites. Exercise summary and expanded-row views over the same IDs.

## Port the whole fixed-table roadmap workflow

A :roadmap node stores ordered named axes, coordinate-to-:ref mappings, scope
revision and completion policy. Its references select canonical feature/work
nodes; each target can contain nested subtasks. The same target's ownership,
acceptance evidence and history feed every view. Arbitrary dimensions are allowed;
languages are one example. Rendering to a table requires an explicit two-axis
projection or fixed selections for extra axes, never a silently flattened matrix.

Retain all current prototype capabilities: bounded restricted-data validation,
duplicate/dangling/cycle checks, required-work rollups, exact roadmap selection,
Unicode-safe marker replacement, deterministic rerendering, drift checks,
empty/whitespace-only evidence refusal and no sentinel collision at literal EOF.
Fix the prototype's known quadratic descendant-set and pre-parse depth gaps as
part of production work; copying the prototype verbatim is not completion.

Provide a read-only Markdown output mode for the selected roadmap so a coordinator
can paste a table here without creating a file or calling a model to recompute
cells. Use the same renderer for a marked region in ROADMAP.md. Render from one
captured work/evidence/scope revision; include that provenance in the receipt.
The file-update mode changes only the selected marker region, refuses missing,
duplicate or reversed markers, writes atomically and preserves all other bytes.
A check mode reports drift without writing. Respect existing working-tree edits;
rendering a target repository is not implicit permission to commit or push it.

Store each configured projection's target repository/path, marker pair and display
policy as non-executable view metadata associated with the roadmap, so the
coordinator does not reconstruct command flags from memory. Resolve only within
explicitly configured permitted target roots. Persist target state and publishing
receipts separately from the authoritative progress data. Missing mappings or
conflicting changes are explicit refusals, never guessed destinations.

Use Glenn's locked display: rows are features, columns are selected axis members;
center the status cells; tick only for fully verified, X for every other state;
percentage summaries show z% only. Preserve partial and unknown work internally.
Each column percentage is fully verified applicable feature cells divided by
applicable feature rows, not averaged subtask percentages. Empty denominators
remain explicitly undefined. Discoveries append visibly and preserve baseline
membership; scope removal cannot masquerade as completion.

Cache derived cell and column summaries, invalidate the affected references after
mutation, and render in time proportional to selected output size. The full table
cannot be constant-time if it contains many cells. Require byte-identical Markdown
between chat-output and file-render modes for the same projection/revision,
including required shared prerequisites and private-data filtering. Measure real
operational token use before and after adoption at equivalent report quality;
implementation tokens are sunk and excluded from that comparison.

## Coordinator verbs and asynchronous transport

The Go CLI is a thin client of the resident Common Lisp engine over an explicitly
named local Unix-domain socket. The engine owns canonical state, journal, indexes
and mutation ordering. Starting a CLI process does not reload the work set.
Retain the main spec's session/journal locks and coordinator fencing. Restrict
the socket directory and socket to the intended OS account; no network listener
or remote evaluation protocol is part of this scope. Local socket access is not
an independent grant of coordinator authority.

Use a versioned, bounded, length-prefixed UTF-8 JSON wire protocol, with a typed
operation name and validated arguments, never executable Lisp forms or shell
commands. Pin JSON integer and timestamp encoding in the protocol schema so IDs,
revision counters and usage totals survive Go/Lisp round trips without float
rounding. Negotiate protocol/schema versions before requests; unsupported versions
fail clearly. Restricted S-expressions remain the durable work-data format.

Each request carries caller provenance, request ID, expected revision where
required, limits and deadline. Mutations enter the single writer's queue; journal
and apply the accepted envelope before its success response. Socket disconnect
never implies rollback or cancellation. Retrying the same ID and identical body
returns the recorded disposition; reuse with different arguments refuses. Retain
the closed-history contract's bounded indexed deduplication, not an unbounded
resident request-ID map. Local durable revision and last shared Git revision
remain distinct in responses.

Quick queries/mutations return a bounded result. Long I/O operations (source
capture, import staging, exports and clip transport) return a durable operation
ID and status, permitting the CLI to exit while work continues. Provide
`operation status`, `operation list`, `operation wait --timeout ... --after ...`
and `operation cancel` with bounded output. Waiting uses an event/cursor and
bounded blocking, not model-driven repeated polling. Completed results are
retrievable by ID; a wait timeout leaves the operation running. A cancellation
request has its own acknowledgment and final disposition; it cannot erase
accepted mutations or undo an uncertain external side effect.

Slow I/O stages immutable inputs/results outside the canonical mutation loop;
only the owning engine admits validated results at an expected revision. Exports
pin a captured revision. Concurrent source captures do not gain concurrent write
authority. Backpressure bounds queues, jobs, staged bytes and retained results;
limits and retention must be explicit, with accepted work recoverable. Recovery
reconciles interrupted operation IDs and external outcomes before retrying. No
unbounded scan or network wait may monopolize the mutation loop: paginate or
stage it, and measure status/cancellation responsiveness under load.

Complete the main spec's verb contract before lock. Map every canonical field to
its owning typed mutation, or explicitly mark it derived/immutable. Required
families below are coverage requirements; finalize exact spelling in one CLI
schema, without parallel aliases that acquire different semantics:

| Data or action | Required coordinator operations |
| --- | --- |
| Session and durability | start, status, clip, checkpoint list/create/verify, isolated restore/compare, export/replay, stop, fenced handoff |
| Reversible mistakes | undo-plan/undo, redo-plan/redo of named requests; append compensating history and refuse conflicting or irreversible effects |
| Work structure | add, edit permitted metadata, move/reparent, decompose, link/unlink, require, retire; preserve stable IDs and historical scope |
| Scope and dependencies | baseline, discovery, dependency add/remove, prioritize, defer, cancel, reopen, supersede |
| Assignments and execution | offer, acknowledge/decline, assign/responsible, take/heartbeat/release, attempt/result/usage intake, correction, pause/stop/reconcile |
| Evidence and completion | criteria add/change/retire, source revision, attest/evidence, review/finding/disposition, verify, settle/revive with explicit guards |
| Roadmaps | create/edit view, axes and members, cells/references, projection targets/policy, query/render/check; no duplicated task state |
| Friends and CONFIG | register/retire, role/participation changes, capability/limit changes, config request/export/validated intake |
| ACTIVE | observation/availability intake, current attempts and pending offers, return reconciliation; occupancy derived from execution records |
| Models and prices | register/version, evidence and suitability updates, route/rate revisions and provenance; historical receipts immutable |
| Issue correspondence | inventory, read-only capture/plan, non-destructive import, reconcile, archive/export/restore, separately selected absorb or external state update |
| Queries and operations | indexed counts, focus/subtree, ready/blockers, friend/model/cost/history views; bounded operation status/wait/cancel |

No generic set-field escape hatch may bypass invariants. Each verb specifies
argument types, prerequisites, read/write set, invalidation/counter effects,
authority/fencing checks, all-or-none boundary, idempotency, success/refusal/unknown
outcomes and its evidence receipt. Multi-node changes use atomic validated
request envelopes. A dry-run produces a revision-bound plan without accepting a
mutation; applying a stale plan revalidates and refuses conflicting assumptions.
Do not overload import with deletion, completion with retirement, or a correction
with proof that a worker has received it.

Exercise all normal coordinator workflows through these public verbs, without
hand-editing Lisp, JSON, journal records or derived caches. The implementation
plan must identify missing verbs and conflicting existing semantics before lock.

## Lossless migration and round-trip release gates

No data-loss guarantee rests on parser success, counts alone, Git commits alone
or a backup that nobody restored. Required executable acceptance coverage is in
[SPEC-WORK-VALIDATION.md](SPEC-WORK-VALIDATION.md). Import is non-destructive by
default; dry-run/capture and all initial pilot imports must have no source-delete
or source-update capability. Destructive absorption is a separate operation and
remains disabled until its independent reconciliation and authorization gates.

## Agreement and lock gate

Integrate this companion with the main spec into one unambiguous revision before
implementation approval. Obtain each requested friend's explicit disposition at
that revision; record unresolved, unavailable or reserved reviewers separately.
Cold model reads supplement friend discussion. No reply is not approval. Require
resolved participation policy, complete verb/protocol schemas, migration and
round-trip acceptance coverage, and named implementation slices. Lock the agreed
revision and keep subsequent changes explicit, scoped and reviewed; recording a
requirement or passing fixtures does not mean the runtime exists or is adopted.
