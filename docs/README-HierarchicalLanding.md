# Hierarchical landing and promotion

`HierarchicalLanding` composes a bounded branch chain: two child heads land into
one parent branch, that parent lands into sprint, and one frozen promotion cut
lands into dev. Truth lives in the destination Git graph. Store receipts and the
local `refs/promoted/last` marker are separate claims about that graph. The model
has four possible leaf-head atoms, three destination sets, three staged jobs and
one promotion. Its state fits one TLC process; it does not enumerate SHA strings,
file contents or an arbitrary-depth tree.

The source baseline is `7112de7a0ee568d7628cec80baddc9bf2a2b68ca`.
The ancestry/intent repair and its real-Git regressions are the separate source
donor `a60c2cc24d1368b3f6497dcacf673b466fe4de66`. Apply that source before the
unique Go bridge tests; the original baseline does not satisfy their contract.
The positive model's promotion guard is that repair's contract, not evidence
that the baseline implements it. Measured model runs and source bridge results
are recorded separately.

| Model event | Source behavior | Limits |
| --- | --- | --- |
| Build, Check | `landpass.go` prepare/merge/checkDecided pins each head and gates a batch cut; `land.go` carries head/attempt/epoch | Reviewed identity is one integer per child; ParentMoves supplies a reviewed parent revision as one outside event; complete review certificates and store epochs remain in the existing Land model/tests. |
| Push | `landpass.go` land/again/needsGate remerges on a moved base; disjoint files can skip a combined gate | Two siblings and one abstract file per child. A permitted disjoint combined tree can be red; it is not cached as green. Shrink/conflict policy remains in LandPass. |
| Report, Retry, Rework, ParentMoves | `steps_merge.go` landedRefusals holds current pinned head and destination ancestry | Rework invalidates review and report eligibility; an outside event supplying a newly reviewed authoritative parent head after staging invalidates the parent job payload. A stale check followed by a push is possible; the store report refuses it. Git history is preserved. |
| Parent Build/Push/Report | Repeated land passes use the configured card BASE, first parent, then sprint | The coordinator supplies this order. This is one fixed composition, not an automatic arbitrary-depth scheduler or the member's script-step tree. |
| Cut, GateCut, PushCut | `promote.go` step/fetch/mergeTarget cuts fetched origin/sprint, merges fetched dev, gates and pushes a separate frozen branch | One cut after this finite backlog reaches sprint; routine partial promotions and optional no-gate configuration are outside the instance. The successful gate oracle is an assumption for progress. |
| SaveIntent, OpenPR, PersistPR, RecoverPR, ConfirmCut, Enqueue, ForgeClaim | The repair saves stable branch/base/tip/cut before push/OpenPR, persists the PR number, and reconciles exactly one matching PR after a lost create response; watch observes checks/queue and merged metadata | Check polling is abstracted. ConfirmCut and Enqueue are distinct: the atomic expectedHeadOid request refuses a changed head after local confirmation. No property attests every GitHub merge group is green. |
| ActualDevMerge, Mark, Record | Actual remote dev graph versus promoter.record's marker/store writes | Baseline record trusts forge metadata; ForgeBaseline witnesses the false landing. Corrected Mark waits for saved cut→reported merge→actual dev ancestry. The two ancestry edges use abstract facts; the source bridge independently checks actual Git objects. |
| Crash, Resume | Landing push/report and promotion push/OpenPR/config/marker/store/cleanup boundaries | One crash or outside change. Exact repository/head/base/cut identity and uniqueness of the lookup result are source protocol obligations, abstracted by one saved intent and one existing PR. The baseline can leave an orphan or duplicate a PR after OpenPR/config crash. Recovery mode reconciles one existing PR from saved intent. A pre-create crash with no matching PR deliberately refuses; it needs explicit reconciliation. Orphan branch cleanup remains outside coverage. |

`TruthfulDev` requires every marked or recorded leaf to be present on actual dev.
`TruthfulReported` requires the claimed merge to contain the saved cut and to be
in actual dev.
`NoUncheckedQueued` checks the expected-head queue fence and has a reversed
witness that permits a changed head after confirmation.
`ReportsCurrent` prevents reporting an obsolete head as the current landing.
`PreservesBranches` checks monotone destination ancestry. `PriorityHeld` orders
ready checked siblings at their next free serial push opportunity; it does not
preempt a push or prove arbitrary-priority end-to-end scheduling.

`FairSpec` checks finite backlog drain with green trees, one bounded outside
change/crash, weakly fair job steps, review/restart and promotion steps, and an
eventual actual dev merge. Positive progress excludes a crash after durable intent
but before a PR exists: lookup zero deliberately fails closed. A separate temporal
diagnostic exhibits the resulting refusal/no-progress state. This excludes permanent forge refusal, persistent
conflicts, unavailable Git/store, infinite new urgent work and an infinite stream
of crashes. It is a conditional bounded progress claim. The source can observe a PR already
merged externally; drain therefore permits a verified external merge without
this invocation confirming or enqueueing it. `NoUncheckedQueued` holds enqueue
actions when they occur, not a universal claim that every merge was queued here.

Diagnostic cases deliberately find stale-check-to-push, disjoint red push,
promotion orphan, duplicate PR after OpenPR/config crash and an externally changed frozen branch. The external frozen-branch event is
excluded from the ordinary positive instance: the tool's own writers freeze the
cut. A separate changed-head control checks that a promotion with extra unchecked
commits cannot be recorded, and a reversed head-fence case witnesses the failure.
The graph can still change externally, so physical branch immutability is not
proved. Complete GitHub check-to-head/merge-group binding remains outside coverage. The real baseline forge-claim witness is distinct from deliberate
reversed report and priority controls.

`HierarchicalLandingSource` independently replays actual bare-origin ancestry,
marker and store observations. `TestHierarchicalLandingPromotionSourceBridge`
runs the actual promote command with a fake forge and real Git destinations:
a merged flag without dev movement must leave both claims empty; a real merge
with deletion of the PR branch must record the actual dev SHA. Observations
bypass the model's Record guard and replay must finish. TLC consumes authored
receipt constants; source coverage also requires the named Go fixtures to run
and compare their independently observed Git/marker/store results to those
constants. A TLC pass alone is not source execution evidence. The third checkpoint in `TestHierarchicalLandingPromotionSourceBridge` adds
actual restart/reconcile with one create. These are three independent fixture
runs (not one promotion history); their complete collected sequence must
equal `SourceObserved`, so a missing checkpoint fails the Go bridge. `TestHierarchicalLandingChainSourceBridge` runs two
child streams (assigned critical versus normal) through actual land passes into
parent in priority order, a parent land pass into
sprint, and actual promotion into dev, checking destination ancestry and store
claims. `HierarchicalLandingChainSource` replays these checkpoints independently
of the idealized selectors; the Go bridge compares its complete actual checkpoint
sequence to `ChainObserved`. The bridges are scoped examples; they do not explore
every source interleaving represented in the abstract model. Their Linux results
are separate evidence from the declared model cases.

Libraries considered: existing TLA Naturals/FiniteSets/Sequences and the repository
TLC runner; Go source fixtures reuse promoteTwin, gitrun and testify. No new
runtime, protocol or production helper is introduced.

Exact lookup identity is separately pinned by the repair donor's
`TestFindPromotionPRRequiresTheExactRepositoryHeadAndBase`; zero-match and
legacy incomplete-intent refusals are source tests. Ambiguous matches and
per-key partial config writes are not enumerated in this bounded model.
Destination ancestry is monotone: competing forward-only dev commits add no
new leaf atom here, and force-push/removal of recorded ancestry is outside scope.

The 22 declared hierarchy cases were measured with one TLC worker on Vision
using the pinned `tla2tools.jar` (SHA256
`936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`).
All declared outcomes passed, including the expected diagnostic violations.
`RUNS.tsv` preserves those individual receipts; the raw logs are in
`release-evidence-v1.2/recovery-20261009/hierarchical-land-model/vision-check2`.
On Hetzner, both final Go bridges passed in 0.601 seconds on the exact baseline plus
a60 source overlay, collecting the complete authored sequences. Raw source
logs and hashes are in that evidence group's `source-go2` directory. The later
added sibling receives critical priority and must land before the earlier normal
sibling; preserving ordinary order would fail this fixture. Scoped command vet
also passed.
The required full `internal/ci` invocation did not pass: Mac archive metadata
caused parse failures, and the 7112 baseline also had unrelated friend
error-handling and capability-example findings. The private archive metadata was
removed. Those source findings have separate approved repairs; coherent full CI
belongs to the integrated candidate gate. No full CI pass is
claimed for this model donor.
