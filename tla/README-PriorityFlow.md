# Priority flow and its source boundary

`PriorityFlow` models the final ladder `fix > blocker > critical > high > reader > normal > low`.
FIX and READER are fixed consumer roles. A read always ranks READER and is orange;
a fix ranks FIX and is purple. Original producer urgency is a separate value used
inside read/fix queues. Work consumers inherit their producer's ordinary urgency.
Fix is non-preemptive and consumes the next eligible free slot. A logical job is a producer plus its
current consumer stage. Source work/read receipts map through their `primary`
field; the model does not infer that link from an id's spelling.

The truth is the sprint snapshot. Seven logical jobs, three owners, and bounded
stage/priority/attempt records fit in one process (well below one KiB of raw
scalar state before checker overhead). The checker explores a finite instance,
not the production fleet's size. Libraries considered: the existing TLA standard
modules and `tlacheck` runner; source receipts use the existing in-memory test rig.

| Contract | Normative/source mapping | Model and evidence |
| --- | --- | --- |
| Default normal; card header overrides stream default | SPEC-SPRINT Priority; `seedPriority`, `StreamPriority`, `SetPriority` | `PriorityDefaults`; actual `Add` and `SetPriority` bridge. Setting a stream also changes existing cards, unlike merely seeding admission. |
| Assigned priority orders creation | `TickDeal`, `friendDealByLadder`, `readOrder` | `PriorityFlowSource` replays the source's actual fleet-create receipt order. Reversing that observation violates an independent pre-state competitor monitor. |
| Work and reads share friend room | SPEC-SPRINT The deal; `friendRoom`, `friendReadsFirst` | Owner capacities and read/work eligibility; source fixture fills seven places plus one existing work card at room eight. Fleet work and fleet readers have distinct capacities. |
| Fixed roles retain producer urgency | `priorityOnRead`, work/fix creation; `priority_flow_model_test.go` | Read role remains READER; fix role remains FIX. Producer urgency is separate, survives attempts, and orders peers of one fixed role. Source subsequent read cards are checked after normal/high/critical/blocker producers are repaired. |
| Finding opens a fix attempt and review/merge retain urgency | SPEC-SPRINT Rework priority; `reworkPriority`, `Rework`, `Ask`, `Accept`, `MergeStep` | Bounded read → finding → fix → work → review → merge cycle; source bridge executes the same real verbs. `ReachCycleWitness` reaches completion rather than passing because no work runs. |
| Eligibility precedes urgency; dependencies remain barriers | `WaitsFor`, `MergePriorityOrder` | An urgent dependent waits while an independent card advances, then drains after its prerequisite lands. Source `MergeStep` bridge exercises an eligible critical ahead of a blocked blocker. |
| Promotion after creation changes the desired next decision | `SetPriority` changes the primary | `PriorityQueued` separates a primary already Working/Review from its created consumer queued/running/done. Promotion changes producer urgency; fixed role stays FIX/READER while copied role/urgency fields remain stale. Work/read/mixed scenarios, creation, stable ties, pause/stop and stale-copy reversal checks cover physical launch selection. `PriorityQueuedSource` requires an actual source queue/Take/read-begin observation before its replay is implementation evidence. |
| Pause/stop admit no new work/read | SPEC-SPRINT Pause and The machine | Control cases allow already-running completion while paused, hold rework, and forbid new admission. Stop is absorbing. Native stop-return details remain in the existing stop models. |
| A blocker reclaims lower-priority occupied room | SPEC-SPRINT marks this owed | Standalone future configs model the future contract only. No production preemption source bridge exists. |

The source receipt fixture is executable: `TestPriorityFlowSourceBridge` runs
`TickDeal`, extracts creates using the authoritative producer field, and compares
the observation with `PriorityFlowSource.SourceObserved`. TLC executes that
sequence using the model's eligibility/capacity/order checks. The source reversal
control deliberately admits the reversed sequence so the independent order
monitor fails; refusing all admissions cannot make that witness pass.

This proves a bounded assignment-order refinement. Physical launches, queue
packet selection, and Take are separate decisions. At the historical base `b5e21b480`,
`SetPriority` updated only the primary, existing work/read children retained copied
priority, and the physical queue and Take used score/turn order. `TestPriorityQueuedSourceBridge` checks the repair's source launch/Take and
read-begin observations against the frozen integrated queued-priority repair.
Those bridge tests passed on frozen source `9e477da5a008122de582a3c170e932534bacad58`
plus only these model/test overlays on serialized Linux. The full sprint/CLI
suites and scoped vet also passed; the required full CI package reported existing
friend deadlines/dashboard path errors and temporary snapshot Git limitations.
The evidence report separates those results and holds normative-spec alignment
for the integrated candidate. It
uses existing physical consumers, promotes the producer without another tick,
checks full room refuses Take, finishes one owned job, and then consumes the
observed selection. This bridge covers automatic `Take --limit` and first-N packet
selection followed by beginning that selected read; it does not constrain an
explicitly named authorized card selection. The initial base does not implement those selection obligations; a successful
TLA replay alone does not establish implementation. `MergePriorityOrder` resolves the primary for existing
merge bookkeeping, so its rank already responds to primary priority changes.

The final policy supersedes the earlier ladder and inherited-reader color/rank.
Old `keep`/`high` rework settings cannot lower FIX scheduling priority. New requests
for those lower policies are refused; legacy stored settings have effective FIX
handling. A repair captures original producer urgency, keeps it across attempts,
and leaves subsequent reads at READER with that original urgency. Reprioritizing
a fix changes its secondary urgency, never its FIX role. `fixRole` and `level`
represent those two dimensions independently in the cycle model; the queued model
has fixed consumer kinds and separate live/copied producer urgency. Historical
logs of the earlier policy remain evidence of that earlier policy only.

Weight-computed critical remains displayed but unordered where the source/spec
state that limitation. The models use assigned critical, not an invented weight
rule. `PriorityDefaults` covers explicit header > stream default > normal for
ordinary priority. FIX is highest but has no action that interrupts an active job
in the v1.2 models.

The lifecycle model abstracts equal-level stream turns and score ties; the queued
model pins earlier-before-later stable ties. Exact tier/route selection,
generation fencing, multi-reader quorum, merge batches/stuck-card barriers,
budgets, recut/replacement transitions, and real-time latency are abstracted.
The source receipt gate is exact `9e477da`; later recut preservation repairs
require their own source fixture and final integrated gate, and are not proved
by that earlier snapshot. A read completion represents the
required verdict aggregate; one broken finding opens held/rework. The merge model
has one stream. Source tests additionally pin the real aggregate reads and real
merge step. Colors are a source rendering obligation, not a TLA state variable. Higher rank constrains decisions among eligible competitors; it does
not prove a wall-clock completion bound.

`FairSpec` drains the finite two/three-job cycle/dependency backlog under weak
fairness of admission and each enabled completion/rework/merge class, no further
arrivals, and a continuously running unpaused machine. There are finitely many
stage transitions in these instances, so aggregate completion fairness suffices.
No claim covers infinite blockers, a stopped machine, absent qualified readers,
nonterminating work, or universal freedom from starvation.

The future preemption contract chooses any strictly lower-priority occupied
friend/fleet consumer. It records its owner, attempt and progress, requests
cancellation, waits for observed native quiescence, then returns it exactly once.
The slot remains occupied through cancellation and observed termination until
return. The blocker is the next eligible assignment into that freed room.
Victim choice, cancellation transport, and preservation mechanics are intentionally
unspecified production policies. Reversed controls detect return before native
termination and loss of progress. These standalone future configs are outside `CASES.tsv`, not v1.2 release gates.

For the implementation brief: define authoritative producer-linked effective rank
at queue packet selection and Take; preserve stable ties and existing eligibility;
show an already-queued normal preceding a later/promoted blocker and a mixed read/
work width-one launch. Keep fixed roles and original urgency explicit, including legacy rework-setting handling. A later preemption change must
persist owner/card/attempt/generation/progress debt, quiesce the owned native job
before reuse, fence stale reports, retain durable completion/report debt, and
requeue/resume once across restart. Test equal/higher-priority victim refusal,
missing acknowledgement, late exit/report, pause/stop races, and both friend and
fleet ownership. Do not interpret this abstract model as those implementation
receipts.

The producer urgency domain excludes FIX and READER: those are roles. A new
FIX/default FIX without originating urgency uses normal urgency; changing an
existing ordinary card to FIX captures its prior urgency.

Run only on a serialized Linux bench with the pinned TLC jar, private Go/Java
temporary roots, and `nice -n 15`. `priority-flow` contains the assignment and lifecycle
checks; `priority-queued` contains the separate queued target and source replay; the standalone `PriorityFlowFuture*.cfg` files contain the unimplemented
preemption contract and are run directly against `MCPriorityFlow.tla`. The future
friend/fleet checks expect exit 0; Reach, Early and Drop expect exit 12 naming
ReachReclaimWitness, QuiescenceBeforeReuse and DisplacedPreserved respectively.
`CASES.tsv` declares the current normal, reversed, and reachable witness outcomes;
`RUNS.tsv` records their checked fingerprints. Future results remain experiment
logs, outside the release catalog; the class gate permits no new debt waivers.
