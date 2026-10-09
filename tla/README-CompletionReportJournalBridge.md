# Completion report and journal bridge

`CompletionReportJournalBridge` joins one synthetic one-shot completion across
`lane_end.go:endCard`, `finish_notice.go:finishNote`, and the later outbox pass.
The group first becomes quiet, then `freezeEnding` saves the exit, the lane
writes its REPORT, and the direct server Finish may commit while its answer is
lost. The coordinator note is journaled **after** that call. A journal error
or a failed `SaveLanes` retaining `Started` can therefore coexist with a Done
server card. Restart reads the durable claim and report; it neither invents a
new exit nor repeats the direct Finish. The outbox is held until the claim is
durably cleared, then a server Done result has no second completion effect.

The normal instance checks journal truth, capacity retention through the
durable clear, and at most one server completion. Three reversed instances
remove one guard each: premature release, notice without REPORT, or a second
completion by outbox replay. Four causal witnesses reach the Done-with-owed-
notice state, lost-Finish-answer recovery after one journal failure, Started
save failure and restart, and a direct Finish that did not commit followed by
outbox settlement. Witness properties intentionally fail at the named state.

This is a bounded single-card safety model. `SafeDedup` abstracts the store's
current-row/idempotent Finish behavior; it does not prove that behavior from
Redis or establish exact server operation IDs. A failed write changes no
durable fact. The model assumes native group quiescence and one immutable
synthetic REPORT; it does not model a friend-written REPORT, STOP debt, bus
delivery, cost, retry timing, or arbitrary process identity. No fairness or
eventual recovery is claimed when disk, server, or native group remains
unavailable. Source trace bridge and executable gates remain separate work.

All eight declared outcomes passed on Linux from exact registered source
`b4c7fd944fa16f836c2e3d5cc86969c1ca8c188b`; the normal case explored 531 distinct
states. Guard omissions and causal witness states produced their named
invariant counterexamples. RUNS.tsv records the fresh fingerprints. This is
bounded model evidence, not executable source, disk, native or server alignment.
