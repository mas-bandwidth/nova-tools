# nova-work — resident session amendment

Draft proposal for integration into SPEC-WORK.md, 2026-09-13. Nothing here is
implemented. Glenn clarified the execution model after draft 3: the coordinator
loads S from its work repository, manipulates it in memory through nova-work
verbs, and periodically flushes ("clips in") to Git. Reloading and reparsing the
whole work set on every verb defeats this purpose. S remains the primary form;
GitHub issues are intake and roadmaps are projections.

## Keep the work set alive

A supervised, long-lived Lisp session owns the parsed S, its stable-ID indexes,
current event position, derived state and cached projections. Load and validate
once at session start; subsequent verbs operate on those resident objects.
A CLI or connector can be a thin client of this session. A fresh CLI process
must not imply a fresh Lisp process or a complete reload. An always-on system
daemon is not required. Session startup, identity, bounds and shutdown must be
explicit and inspectable.

Both structure and state are manipulated through typed verbs. Creating a node,
decomposing a feature, changing a dependency or assigning responsibility cannot
require the coordinator to rewrite Lisp text by hand. The precise public verb
names remain a pilot decision. Trusted implementation code may be Lisp; imported
S and issue text remain bounded data, never executable forms.

After a mutation, update affected indexes and invalidate affected derived values.
An indexed lookup does not traverse S; a changed leaf does not unconditionally
reparse the file or replay the whole event history. Full validation and broad
queries may still require O(V+E) work. No constant-time promise applies to an
arbitrary structural edit or a change affecting most of the graph. Store derived
state in memory; it does not become a second authoritative work set.

## Accept locally, then clip into Git

Proposed durability mechanism: validate each mutation against the current local
revision, append its stable event ID and payload to a local recovery journal,
and acknowledge acceptance only after that journal is durable. Failed validation
changes neither accepted S nor its journal. The same event cannot apply twice.
This is a crash-recovery proposal, distinct from Glenn's required periodic clips.

A clip captures a named local event boundary, fetches the upstream revision,
reconciles changes since the session's base, validates the combined candidate,
and writes a deterministic snapshot with the retained event history. Commit and
push the resulting checkpoint. On success, record the new shared revision and
which local events it contains. Later accepted events remain pending for the
next clip. Do not discard recovery records before successful checkpointing.

A failed network request leaves local accepted work and the pending clip intact;
report it as locally durable but not shared. Retry with bounded exponential
backoff. A rejected push or semantic conflict preserves both versions and names
the conflicting nodes/events; never force-push or silently prefer the last writer.
Apply nonconflicting incoming events incrementally where supported. An explicit
reload/rebuild is a recovery or maintenance operation, not the normal verb path.
Clip cadence is configurable; shutdown and coordinator handoff request a clip.
A Git checkpoint is not required for every small mutation.

Two local sessions cannot guarantee globally exclusive task claims while they
are disconnected. The spec must distinguish an unshared tentative claim from a
confirmed dispatch claim and define its synchronization protocol before claiming
exclusive scheduling. A shared lease file plus periodic Git pushes alone does
not solve this. Local recovery also does not guarantee another bench can recover
unclipped events after total loss of the original bench.

## Required measurements and replays

- Start once, run many queries/mutations: instrument actual parse count, full
  replay count, graph visits, journal writes and emitted bytes. Unchanged indexed
  queries cause zero additional parses or full replays.
- Incremental results equal a clean reconstruction of the same accepted revision;
  unrelated cached projections remain reusable after a local leaf mutation.
- Crash after journal durability but before acknowledgement, then retry the same
  request: one accepted event, no lost or duplicated mutation.
- Crash or disconnect during clip: recovery retains all accepted events and
  reports the last confirmed shared checkpoint honestly.
- Two coordinators edit independent nodes and then the same dependency or lease:
  reconcile the former; detect and preserve conflicts in the latter.
- Structural verbs update the tree and its evidence/scope history without manual
  source editing; the resulting ROADMAP remains reproducible.

These amend draft 3's file-per-verb interface, hand-written-only structure, full
checks around every append, and build-indexes-on-every-read wording. Integrate
those contracts together before approval; adding a resident wrapper around an
unchanged reparse-per-command engine does not satisfy the requirement. Dogfood
the workflow on Fixed Tables, record total builder plus review/repair cost, then
refine the production spec. This amendment does not authorize a production build.

## Reusable recursion, not a mandatory combinator

Glenn asks whether Y-combinator ideas can make the Lisp tooling more general.
The proposal is reusable recursive operators over typed nodes: a fold for
summaries, a bounded unfold for decomposition, and incremental propagation for
derived values. A recursive data structure is not itself the Y combinator.
Ordinary named recursion in Lisp suffices; a literal textbook Y combinator does
not terminate under eager evaluation without adaptation.

Separate traversal from per-kind policy. A completion fold and a cost fold share
traversal machinery but not counting rules: attempts contribute incurred cost,
while only acceptance-qualified work contributes completion. References do not
become extra owned children. Cache with revision and policy identity; invalidate
through the retained indexes. A generic operator must preserve these semantics.

For an acyclic dependency graph, use an affected topological pass instead of
repeatedly rescanning all of S until nothing changes. Any future fixed-point
solver over cyclic analysis facts requires a defined domain, convergence argument
or explicit iteration bound, and an honest non-convergence result. It does not
permit cycles in counted containment or declare tasks complete by convergence.
Decomposition is proposed work, with depth/work/cost bounds and stopping criteria;
it never grants itself permission to execute an expanding tree of tasks.

The bounded pilot should demonstrate two different summary policies over the
same tree, shared-reference counting, and equivalent full versus incremental
results after a change. Adopt the abstraction only if it reduces implementation
or coordination cost without obscuring acceptance evidence.

## Public issue correspondence survives intake

Glenn clarified: external people continue opening issues on public repositories
such as yojimbo. Intake represents and links that issue in S; it does not move or
delete the GitHub issue. S owns planning and decomposition, while GitHub retains
the public discussion and externally observed issue lifecycle.

Store a stable provider/repository/issue identity plus its current URL and last
observed remote revision. Use an explicit mapping: one public issue may require
many work nodes, and one work node or landed fix may address several issues.
Repeated intake updates the existing correspondence, never duplicates the work.
Retain external reports separately from the coordinator's accepted plan; remote
text is data and cannot execute verbs or silently change scope or authority.

Track correspondence actions as pending, confirmed or failed with request IDs
and receipts. Reporting a fix or closing an issue is a distinct outbound action
under the team's configured authority, tied to the required work and actual
landing/acceptance evidence. A locally completed attempt, a deferred work node,
or a scope reduction does not by itself close the public issue. Retry uncertain
outbound actions idempotently and preserve concurrent human changes. A reopened
issue creates a reconciliation signal; it neither disappears nor silently erases
previous completion evidence. Ordinary linked intake and clipping never delete an issue. The separately chosen
absorb operation below is the explicit exception.

## Link versus absorb

Glenn authorizes a second intake mode for issues created by him or participating
AI friends, on public or private repositories: `absorb` moves their lasting work
record into S and may remove the original GitHub issue. This is distinct from
`link`, the default for outside contributors. Author identity alone does not make
an issue selected for absorption; scope and intake mode must be explicit, with
team configuration identifying participating authors and applicable repositories.

Before deletion, retain the original issue identity, authorship, body, discussion,
labels, state, relevant relationships and available attachments in an archive
associated with S. Preserve provenance separately from planning. Unavailable or
unpreserved content is reported and leaves deletion pending, not silently skipped.
The destination must preserve the source's access scope; a private issue is not
published by becoming part of S.

Order the operation: capture source at a named remote revision; validate the
archive and mapping; commit and confirm the shared Git checkpoint; recheck for
source changes; then delete only the selected source issue under the configured
authority. If the source changed, reconcile and checkpoint the added content first.
Keep the original external identity and an absorbed tombstone plus deletion
receipt so later intake cannot recreate the work. A network failure or uncertain
delete result preserves the archive and a pending reconciliation state. Do not
claim atomicity between Git and GitHub or restore an issue by inventing an author.
These semantics are a specification, not a request to absorb existing issues now.

## Initial migration: preserve first, reconcile, then choose absorption

Glenn's initial rollout is importing the team's existing work sets first, while
identifying external issues that remain on GitHub. No data may be lost. Migration
is not a bulk delete of the source issue lists.

Begin with an explicit inventory of authorized source work sets and issues. Each
entry records its source identity, destination node/mapping, capture revision,
content manifest and disposition: keep linked, eligible for absorption, or
unresolved. Outside-contributor issues stay linked; unknown ownership, mixed
provenance or unclear disposition remains unresolved without source deletion.
This is a migration classification, not a claim that an author's identity grants
new access or that all team issues have already been selected for deletion.

Import in resumable batches without deleting originals. Preserve original records
alongside the normalized representation, reconcile counts and content manifests,
deduplicate stable identities, and account explicitly for every inventory entry.
Record missing attachments or inaccessible discussion as incomplete. Verify the
shared checkpoint can be loaded and replayed, and that original records can be
retrieved from it, before declaring an import complete. Feature decomposition,
links, ownership, dependencies, open questions and completion evidence must not
be collapsed into a flat title/status list.

Absorption is a subsequent selected operation after this reconciliation, never
an import side effect. If the provider cannot support a safe revision boundary
against concurrent source changes, leave removal pending rather than claim a
lossless deletion. A backup receipt alone does not prove that newer comments were
captured. Report imported, linked, eligible, absorbed and unresolved separately;
retain batch checkpoints and provenance so interruption or retry does not lose
records or duplicate work. The prototype must exercise interruption and a source
edit during migration before it is trusted with removal.
