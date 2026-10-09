# Token cap STOP settlement boundary

`TokenCapStopSettlement` supplies the implementation events that
TokenCapRecovery abstracts into guarded Settle. One owned positive-cap DSH
work card, one worker report, one native primary plus auxiliary group, one
STOP debt and one restart fit in one process (under one KiB scalar state).
Libraries considered: TLA Naturals and the existing case runner.

The canonical Started entry remains the durable ACK/outbox fence. A quiet
native group can make StopReturn.Ended true without making an unsafe existing
non-HOLD report eligible for hand-back. Heap usageHeld additionally retains the
live lane/card reservation. Crash loses heap state; persisted Started and the
same usage projection still require the final gate after restart.

| Model action | Source contract at runtime donor 6ba8577d3da43369508dfd20c90a55139f5f2c3f |
|---|---|
| Stop, WorkerReport | STOP before a late worker report leaves a return debt. A completed artifact itself does not create a new cancellation debt. |
| UsageFault, FinalUsageFault, TurnReturns | Owned positive-cap usage rejection retains an existing non-HOLD report and cancels the native turn. Returned harness call is separate from group quiet. Final-only rejection can have no heap usageHeld; Started must still fence settlement after idle lane release. |
| ReleaseHeld | stopStep refuses the idle-lane releaseHeld path when usageHeld has an existing non-HOLD report. The path otherwise drops only heap card/reservation and creates debt. |
| MarkEnded, SettleStopDebt | settleStopDebts first proves exact native quiet, then checks tokenCompletionGate before deleting Started. Rejection may mark Ended but leaves Started. |
| ReturnAck, ClearAckDebt | returnOwed skips any job still in Started. ACK durability/compaction is abstracted as a successful separate step. |
| Outbox | Started independently excludes report publication even when a cached holder row says Working. Server queue freshness/head/ancestry and verdict transport are outside this model. |
| Crash, Restart, RejectUnsafeSettlement | Heap usageHeld can disappear at restart; durable Started and current final usage reject settlement again. |

All twelve declared outcomes passed on Linux from exact registered source
`b4c7fd944fa16f836c2e3d5cc86969c1ca8c188b`; the normal case explored 1,496
distinct states. These are bounded model measurements, not runtime alignment.
The twelve cases include: one normal, four guard
omissions, six safe reachability witnesses and one diagnostic full baseline
bypass witness. The last uses both omitted source guards to reach releaseHeld,
Started deletion, stop ACK/debt clear and outbox LAND. The normal case excludes
that chain. Existing model inputs and historical receipts remain unchanged.

The executable bridge is the separately owned
TestDSHUsageHeldLANDCannotBeStopReturnedOrFinished and restored-Started companion:
final cap rejection with worker LAND, STOP owed debt, native quiet, stopStep,
settleStopDebts, returnOwed and outbox must retain Started, publish/ACK nothing,
and preserve REPORT. A matching observation trace remains an implementation
obligation; model measurements alone are not that bridge.

Usage availability/session/baseline and any-bucket regression are input oracles
here, modeled in TokenCapRecovery and validated by source parser tests. Soft
polling permits overshoot; no strict provider spend ceiling is claimed. Exact
native identity, durable disk/RPC saves, multi-card scheduling, external report
writers and generic successful-safe-settlement save-failure rollback are outside
this bounded contract. There is no universal liveness claim; an unsafe non-HOLD
report intentionally stays held without operator repair.
