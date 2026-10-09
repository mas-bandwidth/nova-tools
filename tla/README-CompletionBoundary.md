# Restart completion boundary

`CompletionBoundary.tla` isolates the outer completion boundary that
`OutboxFinish` and `FriendLaneCompletion` do not compose: a restarted daemon
can see REPORT before loading prior durable Started ownership. The diagnostic
source references are lane settlement `b090fa9c5`, failed/partial load guards
`043aec7be`, and outbox restart guard `04672537b`. Batch snapshot/recovery
guards are separate source deltas. These references identify boundaries;
they do not claim the model refines a particular integrated executable.

Truth lives in durable lane state, the matching OS process group, canonical
REPORT and the finish-note journal. The in-memory ownership snapshot is
usable only after a successful complete load. One modeled prior claim has
twenty small state/history fields, less than 128 bytes of abstract payload.
The pattern is recover ownership, verify quiescence, durably settle the same
claim, then permit outer completion or reuse of capacity. One hundred
independent claims repeat that record; the model introduces no production
cache or additional source of truth.

The model starts with one prior durable Started claim and a live group.
REPORT may appear while that group is live. One daemon restart discards the
volatile snapshot and group observation; its initial mode is batch, as in
`Daemon.Run`. A complete load reestablishes known ownership. Failed and partial
loads hold both completion and admission, and never overwrite the durable
claim. Changing between batch and one-shot changes no claim.

Physical group exit and a verified observation are separate actions. An
available report is separate from canonical report publication and durable
finish-note journaling. Successful durable Started removal is the capacity
release boundary. Outer Finish requires known ownership, group quiescence,
canonical report and journal durability, and the committed Started clear.
New batch work uses that same prior-debt boundary; `BatchWorkStart` abstracts
admission of the next turn and executes no provider or native program.

Writes that fail do not advance the durable fields. No fairness assumption
promises a healthy disk, successful load or eventual group exit. RUN identity,
PID/birth matching, frozen ending payload, atomic-file mechanics, STOP return
acknowledgment and outer Finish payload/replay semantics remain obligations
of the existing native, lane-completion, StopReturn and OutboxFinish layers.
The model covers one prior Started claim; it does not refine arbitrary
multiple-claim or canceled-return executions.

The `completionboundary` group has seven small cases: required safety, a
reachable safe restart settlement, the three diagnostic witnesses below,
and mutations of release and batch admission barriers. Expected failures are
individually named in `CASES.tsv`; an unrelated failure is red.

| Diagnostic witness | Required order |
|---|---|
| Outbox before load | Restart, select one-shot, visible REPORT, outer Finish before ownership load |
| Partial error overwrite | Restart, partial load with error, save incomplete snapshot over the durable claim |
| Batch debt bypass | Restart, known prior Started debt, batch mode, visible REPORT, outer Finish while the debt remains |

## Whole-daemon trace bridge

`TestRestartOutboxWaitsForDurableLaneSettlement` calls the whole daemon and
asserts ownership loading before Finish, holds unreadable state and unknown
machine ownership, preserves multiple Started/adopted runs, and permits
completion after simulated quiet settlement. Its RUN/PID fixtures and process
hooks are synthetic unit observations, not OS process exit receipts.

`TestBatchRestartOutboxPreservesPriorOneShotDebt` calls the whole daemon with
a batch row and prior Started/STOP debt. It asserts that partial state plus
error holds reports, unrelated batch work can settle, prior reports remain
held, and simulated durable clear/stop acknowledgment permits their later
settlement. Its synthetic snapshot clears do not prove native quiescence.

`TestLaneCompletionWaitsForMatchingProcessGroup` separately runs a real Linux
shell group with a surviving descendant. Its matching RUN/PID/birth checks
and durable lane writes provide native boundary evidence; this model does
not claim those actual receipts form a whole-daemon restart trace.

A future trace check must join observations by one public job/epoch/generation/
run identity, recording `daemon_restart`, `mode_selected`, `lane_load_known`,
`lane_load_failed` or `lane_load_partial_error`, `group_quiet_verified`,
`canonical_report_saved`, `finish_journal_saved`, `started_clear_saved`,
`outer_finish_requested`, and `batch_work_started`. Mark a group observation
as native or simulated, and mark file events only after the corresponding
successful write/readback. Record prior Started and owed STOP-return counts
without copying reports, prompts or credentials. A LoadLanes invocation alone
is not a known-ownership receipt; a fake PID or report file is not native exit
proof. Existing test assertions are bridge obligations, not a formal emitted
trace protocol, and this model alone does not certify the repaired code.
