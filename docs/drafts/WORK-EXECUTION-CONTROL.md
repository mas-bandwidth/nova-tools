# Proposed distributed execution control — against PR231 `7db3b95`

This supplies owning verbs for pause, stop and execution reconciliation. It is a
proposal for friend review, not a second scheduler, provider adapter or approved
protocol. It complements the assignment proposal and the existing single writer,
lease, operation and attempt records.

## Control intent is not a terminal outcome

A task, its lease, a pending offer, and an executing attempt are separate facts.
`session stop` ends the coordinator session; it does not mean its workers stopped.
`operation cancel` requests cancellation of a local long operation; it is not a
distributed work stop. These new verbs must not overload either spelling.

```
nova-work execution pause --session <path> <write flags>
  (--node <id> | --repo <owner/name> | --all) --reason <text> [--dry-run]
nova-work execution stop --session <path> <write flags>
  (--node <id> | --repo <owner/name> | --all) --reason <text> [--dry-run]
nova-work execution resume --session <path> <write flags>
  --control <id> --action <release-hold|resume-workers> --reason <text> [--dry-run]
nova-work execution correct --session <path> <write flags>
  --node <id> --instructions <immutable-reference> --sha256 <digest>
  --reason <text> [--dry-run]
nova-work execution reconcile --session <path> <write flags>
  --control <id> --from <observation-manifest> --reason <text> [--dry-run]
```

Pause installs a durable scheduling hold and requests a cooperative safe pause for
current assignments. Stop installs that hold and requests their execution stop. A
worker lacking pause support reports unsupported; it is never silently killed or
counted paused. An unsupported stop remains visibly unresolved. Neither command
cancels the underlying task, marks it done, grants process permissions, nor restarts
a stopped worker. A later offer is a separately admitted operation.

The hold applies to the selected containment scope and to descendants subsequently
added or moved beneath it. The scope selector is typed, not a reserved node id:
`{ "kind": "node", "id": "n" }`, `{ "kind": "repo", "repo": "o/n" }`, or
`{ "kind": "all" }`; canonical forms are `(:node "n")`, `(:repo "o/n")`,
`(:all)`. Configured authorization and coordinator fencing still govern admission.
A held node cannot be moved out to bypass the hold while a captured or uncertain
execution remains; moving it is an explicit reconciliation decision, not an escape.

## Durable, bounded dispatch

Wire operations are `execution.pause`, `.stop`, `.resume`, `.correct`, `.reconcile`. Pause/stop
args are `scope` and `reason`; resume is `control`, `action`, `reason`; correct is
`node`, `instructions`, `sha256`, `reason`; reconcile is
`control`, `manifest` (content identity, not a local pathname), and `reason`.
The common request id is also the stable control identity. Each accepted control
writes a new `:execution-control` event with fixed fields:

```
:change :control :scope :action :instructions :sha256 :manifest :reason
```

Changes are exactly `:pause`, `:stop`, `:resume`, `:correct`, `:reconcile`. For a
resume/reconcile request, `:control` names the prior control; its own request id
still uniquely identifies the new action. Other changes create a control under their
request id. Correct uses a typed node selector in `:scope`. Unused fields encode
`(:absent)`, not omitted keys or an empty value. Common node subject is absent;
these are execution records, not containment nodes. Derived target capture, receipt
and counter updates are engine records outside the requester digest, as with the
existing long-operation envelope. The next protocol table must pin those derived
record codecs before implementation. Unknown/duplicate keys and invalid selectors
refuse. Reusing one request id with another payload refuses; retry returns its
original operation/control identity.

Admission journals the hold and a recoverable capture anchor before an
acknowledgement. The engine record pins the validated base snapshot hash, accepted
journal boundary and captured model revision; retention cannot discard that base
or journal span until target capture and its durable manifest have completed. A concurrent clip may publish, but it must carry the live capture pin forward and retain the anchored snapshot object plus committed journal span until that manifest is durable; it may not make the capture reconstruct from a newer scope. If the configured retention/clip representation cannot hold those exact references, admission refuses the control before acknowledgement rather than waiting through external I/O or silently weakening the capture. The durable pin is released only after the manifest's anchor and referenced bytes verify.
Recovery reconstructs that exact revision, not the newer mutable scope. Here C/O/W means the source's closed/open/working root and working predicate; it is **not** copy-on-write and its in-memory pointer alone is insufficient. A crash cannot leave an acknowledged pause with
no hold or no reproducible target set. Capture selects pending offers
and current or unresolved execution identities through the existing per-node/repo/
friend indexes; a lease expiry cannot remove a target from this capture. The
operation reads the captured immutable revision in bounded pages, stores the ordered
target manifest and its content hash, then stages per-target directives with stable
identities. No read of all historical C is needed. Cost is proportional to selected
assignments and affected scope/index updates; this does not promise an O(1) stop of
arbitrarily many workers.

The same single-writer barrier interlocks offer creation, acknowledgement-to-lease
conversion and final dispatch. An accepted recipient reply arriving under a hold
is durably retained as held acceptance: no lease conversion, launch, or capacity
release occurs. Removing the hold does not auto-convert it; explicit reconciliation
rechecks its current generation, identity, capacity and other effective holds.
The admission barrier is also checked at the final dispatch boundary. This closes
the race where an offer prepared before the hold launches after it. Work already
launched remains in the manifest; an in-flight dispatch with an uncertain outcome
stays a target until reconciled. No new dispatch may slip between capture and hold.
A directive carries control id, offer/attempt identity, exact node generation,
coordinator fencing generation, action and content hash. Transport retry resends
that identity; it does not create a new model job. A receiver rejects stale fence
or mismatched target identity and returns an explicit bounded disposition.

Transport delivery, recipient acknowledgement and observed pause/exit are separate
receipts. A bus receipt proves delivery to the configured transport only. The
coordinator journals the validated facts; imported prose cannot mutate the work set.
Durable pending sends and uncertain outcomes survive clip/recovery so the successor
reconciles before resending. Worker-side enforcement, receipt trust and adapter
capabilities must match the shared swarm/assignment contracts; storing a fence in
a message is not proof that a provider enforces it.

These are external-effect operations under the existing long-operation protocol.
They cannot appear in an atomic batch promising rollback of external actions. An
independent batch can issue them with per-request correlation and existing accepted
prefix semantics. Returning `OPERATION OK` acknowledges durable control intent, not
a stopped fleet. Status/wait exposes bounded counts: selected, pending-delivery,
acknowledged, confirmed-paused/stopped, unsupported and unresolved. An observation
timeout prints pending/unknown; it neither completes the operation nor launches a
replacement. Exact output grammar is a protocol-lock decision.

## Reconciliation and resumption

An observation manifest is bounded, content-addressed and retained with provenance.
Each record binds control/offer/attempt id, node generation, source identity, observed
execution handle, observation time, outcome (`running`, `paused`, `stopped`,
`completed`, `not-started`, `unsupported`, `unknown`), and result/usage references when present.
`not-started` qualifies only when the responsible launch authority durably rejects
that exact assignment identity from future launch; a queue miss alone is insufficient.
This fact can release an unlaunched pending reservation without inventing an attempt.
Missing usage remains unknown and observed model/bench fields go through the existing
observation intake. Stop-control reports never synthesize zero cost. The exact
manifest codec and authorized source mapping need friend review before runtime intake.

A late report about a previous attempt is attached there; it cannot release the
new attempt's reservation or qualify its work. A negative process lookup is valid
only for its bound execution identity and declared observation semantics; silence,
an expired lease or an elapsed return estimate is not stop evidence. Contradictory
observations are preserved and unresolved until reconciled, not last-write-wins.

Confirmed pause may still occupy resources and retain a lease. Confirmed termination permits capacity reconciliation, but it does not bypass the
existing holder-only lease release rule. A validated holder release can join the
local envelope; otherwise the lease remains held-not-worked until its recorded
deadline or an existing authorized settlement path. The coordinator never fabricates
a holder signature or silently calls take/release under another friend's name. It does not manufacture a task verdict or erase results,
usage, pending side effects or historical ownership. A completed execution still
needs ordinary acceptance evidence to make its task done. Capacity held for unknown
execution is not advertised free. W continues to mean live leases; ACTIVE continues
to hold uncertain executions even when W no longer contains their task.

Resume `release-hold` removes only its named scheduling hold after required outcomes
and side-effect authority are reconciled; overlapping holds remain effective. It
never resumes or relaunches remote processes. Resume `resume-workers` instead stages
an explicit resume directive for each confirmed-paused bound execution, refuses
unsupported capabilities before sending, and retains the hold until current
receipts reconcile. It neither starts replacement attempts nor overrides another
control's hold. A running observation at the correct resumed boundary is required;
a delivery receipt alone cannot release that hold.
An unsupported outcome may close a control's transport operation with a visible
failure, but it does not clear the hold or the execution uncertainty.

The missing-verb register says nothing writes `cancel-requested`, but the existing
`state --to <state>` grammar and transition table already admit it. Reconcile that
source wording rather than adding a redundant writer. Task cancellation remains the
distinct two-step source rule: `state --to
cancel-requested` only on an allowed transition, then `event --kind cancel` with
validated evidence that every relevant execution has stopped and side effects are
reconciled. Add an explicit control/attempt-set evidence binding to that admission
gate; one old worker's stop note must not cancel work with another live attempt.
This preserves the existing state table, including its refusal of cancellation
from review until a separately reviewed transition amendment exists.

`execution correct` is the owning distributed-correction operation. It atomically
installs the node hold, captures old-generation executions, writes the existing
`:correct` event and binds the new generation to immutable instruction bytes and
their SHA-256. The instruction reference is data, not a command or access grant.
Its envelope identity covers both generation change and control so retry cannot
bump generation twice. The configured content reader validates and bounds instruction bytes **outside** the mutation loop; only after it has returned immutable staged bytes does the writer revalidate the expected revision, target generation and holds, then durably retain those bytes and acknowledge the generation change. Missing, mismatched or stale staged content refuses atomically. Dispatch uses that retained body through the configured transport, never newer text at the reference.

Each capable worker receives a directive naming old/new generations, its original
offer/attempt, and exact instruction hash. A worker acknowledges the applied
execution boundary and preserves old-generation usage/results separately from the
new segment. This is a linked execution segment, not a rewrite of an earlier
attempt. Unknown or unsupported correction stays held; the explicit fallback is
stop/reconcile followed by a new assignment with lineage. No automatic duplicate
launch occurs. The bare local `correct` verb still invalidates evidence without
claiming transport delivery; while any execution remains active/uncertain, it must
refuse naming `execution correct`, otherwise it bypasses the dispatch barrier.


## Undo disposition — proposed

The parent undo table requires a named disposition for every control mutation. A local reversal only appends a new guarded envelope while no target directive was handed to transport and the control's exact postimage, captured target set, and hold generation are still current. It never deletes a control, receipt, observation, usage segment, or external effect.

| verb | reversible before transport handoff, with exact postimage guard | refused or compensating action after handoff/delivery/uncertainty |
| --- | --- | --- |
| `execution pause` | append a control reversal that removes only this untouched hold and cancels its unsent directives | after any directive delivery or target uncertainty; retain the pause and use `execution resume --action release-hold` only after the named control's required outcomes reconcile |
| `execution stop` | append a control reversal that removes only this untouched hold and cancels its unsent stop directives | after delivery, acknowledgement, or uncertain process state; retain the stop and use a new authorized resume/reconcile action. No undo claims a worker restarted |
| `execution resume` | append a reversal that restores only the prior untouched hold and cancels unsent resume directives | after resume delivery or a running/unknown observation; retain the control and use pause, stop, or reconcile as a new action |
| `execution correct` | append a reversal only before any correction directive leaves the coordinator and only if old/new generation postimages and retained instruction identity are unchanged | after delivery or any old/new execution uncertainty; retain linked segments and use stop/reconcile followed by a new assignment or correction, never a generation rewind |
| `execution reconcile` | never: it records validated observations and their disposition | always; a later reconciliation records a new observation set and preserves the earlier one, including contradictions |

Transport handoff includes a claimed or in-flight send even when no delivery receipt exists; absence of a receipt does not prove that a directive was never sent. A correction compensator that reapplies earlier instruction content allocates a new generation and preserves its predecessor lineage; it never restores an old generation number or relabels old results as current. The exact canonical reversal event shape and its relationship to the parent `undo` verb remain a friend-review decision. Until pinned, an implementation must refuse a requested undo rather than infer that a transport queue, worker, or lease can be restored.

## Required witnesses and remaining decisions

- Crash after hold durability and before target capture/send: recover hold and same
  target identities, no duplicate worker launch.
- Race launch/correction/move against scope pause: dispatch barrier and scope guards
  prevent escaped work; prior-generation results retain lineage.
- Mix live, expired, pending, paused, unsupported and unreachable targets: exact
  indexed capture, bounded pages, unknown occupancy retained.
- Duplicate/delayed/out-of-order receipts and forged source identities: no inferred
  ownership, current-attempt release, cancellation or successful completion.
- Stop two attempts on one task: one stopped receipt cannot cancel both; raw usage
  is retained independently and the task id is counted once in W.
- Restart coordinator, release one of overlapping holds, undo after delivery and
  return a friend: no implicit restart, cleared uncertainty or permission change.
- **Capture survives clip:** a clip completes after a control anchors its base snapshot and journal boundary but before its target manifest. The retained pin still resolves the same revision and span; an unavailable pin representation refuses admission before the control acknowledgement.
- **Staged correction refusal:** a bounded instruction read or target verifier completes after a conflicting revision. The writer records no hold/generation/control envelope and status remains responsive while that read runs.
- **Undo boundary:** an undispatched pause/stop/resume/correct with unchanged postimage appends its named local reversal; a claimed send with no receipt, delivery, receipt or unknown process state refuses and preserves all receipts and segments. Reapplying old correction content produces a fresh generation with lineage, never a generation rewind.

Agree the derived capture/receipt and observation codecs, the durable capture-pin/clip representation, the exact guarded reversal-envelope codec, hold/dispatch barrier implementation, adapter pause/resume/correction capabilities and segment-boundary codec, fencing enforcement, and trusted execution-outcome evidence. These refine existing assignment/ACTIVE
and distributed-stop requirements; no scheduler or test suite is complete yet.

Related proposal: [assignment admission](WORK-ASSIGNMENT-VERBS.md).
