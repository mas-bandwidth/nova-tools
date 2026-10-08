# STOP return authority

`StopReturnAuthority.tla` extends the STOP proof boundary without replacing
`StopReturn.tla` or its eight cases. The older model checks simultaneous work
and read debts, branch retention, the operation fence, stale tick causes and
stale tick parts. This companion checks the authority those actions carry.

The bounded state fits comfortably in one process: two owners, two epochs,
one representative card selected from fleet/readers, and at most two launches
per epoch. Generations range from 1 through 3 at launch, and through 4 after
return, because owner transfer also advances generation. The launch counter
bounds execution; it is not the source's preserved `attempt` field. The debt
and each child/report token have five logical components: owner, table, card,
generation, and epoch. The implementation serializes the first four in
`StopLease`; epoch belongs to the store/step context. Reports and acknowledgements
remain available after a new launch and after epoch advance.

One authoritative table record and the captured machine debt determine store
transitions. The runner's observed exit is an environment fact. Receipt records
copy the request and live tuple at acceptance, so later state changes do not
turn a valid historical acceptance into a false stale-acceptance failure.
The table-kind choice covers fleet and readers separately; it does not claim
multi-card interleavings beyond those covered by the preserved model.

| Action or guard | Implementation |
| --- | --- |
| STOP captures exact active tuple | `internal/sprint/store/machine_transition.go`: `activeStopLeases`, under the operation fence |
| Same-owner ready/from/gen tuple permits START | `unsettledStopDebt` in that file; START clears debt |
| Owner/generation checks and return increment | `internal/sprint/stop_return.go`: `StopReturn`; replay is idempotent |
| Epoch and STOP checks | `internal/sprint/store/engine.go`: `Step.Epoch`, `RequiresStopped`, `ReportsWork` |
| Required epoch at CLI boundary | `cmd/nova-sprint/verbs.go`: `epochVerbs`, `runStep` |
| Debt prevents removal/transfer even after return | `stopDebtMutation` in `engine.go`; debt clears only at START |
| Transfer advances generation | `internal/sprint/rebalance.go`: `rebalanceMove`, `nextGen`; transfer abstracts reassignment of ready work |
| Clear refuses captured debt | `internal/sprint/store/clear.go`: `Clear`; restoring a fresh representative card is compressed into this action |

The passing safety case checks exact requested owner, generation and epoch;
no return before real cancellation; no START before the captured receipt; no
orphaned debt; and no stale report acceptance. `Requests` deliberately allows
caller-supplied wrong owners. Old-generation and old-epoch requests remain
selectable from retained tokens. The immutable report set makes delivery of
an old report after relaunch possible.

The progress case starts at every bounded owner/table/generation/epoch tuple
and measures settlement of that captured debt. It assumes weak fairness of STOP, child cancellation, and a
valid same-owner store return. It excludes clear, reassignment and new work
while draining that STOP. It promises eventual settlement under those explicit
assumptions, not availability of an absent runner, truthful acknowledgement,
or successful storage under permanent failure. Removing cancellation fairness
or return fairness produces a temporal counterexample.

The seven mutation cases each select exactly the invariant they must break:

| Suffix | Changed rule | Expected violation |
| --- | --- | --- |
| Owner | Ignore requested owner | `ExactReturnOwner` |
| Attempt | Ignore requested generation | `ExactReturnAttempt` |
| Epoch | Ignore held epoch | `ExactReturnEpoch` |
| Premature | Trust a receipt before child exit | `AuthorityNoPrematureReturn` |
| Start | Ignore unsettled debt | `AuthorityNoPrematureStart` |
| Orphan | Permit removal/transfer with debt | `AuthorityNoOrphanDebt` |
| Report | Ignore old report generation | `AuthorityNoStaleReport` |

Four `Reach` cases intentionally fail a negated reachability predicate. Their
traces demonstrate relaunch with an old report, transfer with the old owner's
report retained, reuse of a generation in epoch two with epoch-one input, and
a real STOP/return/START sequence. These are successful coverage witnesses,
not broken safety invariants. The two `Starve` cases must fail
`AuthorityEventuallyReturned`. No event boolean is permanently initialized
false and then used as evidence of rejection.

The model qualifies source claims. `--as` is a requested row, not cryptographic
owner authentication. The store trusts a nonempty cancellation reason; the
honest acknowledgement rule is a runner obligation, not a store check.
`StopReturnReq` and `StopReturnStep` do not themselves require an epoch. The
CLI requires `--epoch` and sets `Step.Epoch`; a direct library caller must set
it too. `engine.go` explicitly permits repinning when `Step.Epoch == nil`.
The Epoch counterexample also describes that omitted-epoch boundary: return
old epoch-one generation-one work, START, clear, start a new epoch-two
generation-one child, STOP, then deliver the old receipt without its epoch
fence. No unconditional epoch guarantee is established for that raw API.
The model adds no implementation fence and does not certify process-group
cancellation, storage crash recovery, or the runner's epoch propagation.
`AuthorityClear` is not a refinement or atomicity proof of `Clear` against
concurrent START or other store operations.

Reproduce on a serialized Linux bench with the pinned jar and private HOME,
TMPDIR and GOCACHE using `make tlc TLC_GROUP=stopauthority TLC_JAR=<pinned-jar>
TLC_OUT=<owned-output>`. `CASES.tsv` records exact expected outcomes and
`RUNS.tsv` records fingerprints, Java/jar identity, state counts,
exit codes and elapsed time. Raw traces are preserved in the release evidence.
