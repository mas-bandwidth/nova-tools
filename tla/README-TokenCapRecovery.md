# Durable token baseline and recovery

`TokenCapRecovery` models the separately owned DSH usage contract. Existing
`LaneEnd` abstracts wall-cap exit/report/redeal; it has no cumulative usage,
known baseline, missing sample, or baseline adoption state. This companion
adds those states without changing any of the previous 776 measured cases.

One card, one immutable saved session/run association, two representative token
buckets and one restart fit in one process (under one KiB of scalar state).
The durable `Started` baseline and known bit are authority; a fresh cumulative
projection is an observation. Libraries considered: TLA Naturals and the existing
runner. Two buckets distinguish component regression from increasing totals;
actual input/cache/output fields and parser arithmetic remain executable duties.

| Action | Runtime contract and source seam |
|---|---|
| `RefuseBaseline`, `Claim` | Positive DSH cap requires a readable selected-session baseline before `ClaimLane`. A verified header-only/no-assistant session is known zero. Refusal emits a diagnostic/backoff, acquires no claim and writes no card REPORT or Started. |
| `SaveStarted`, `FailStartedSave`, `Launch` | Persist exact `TokenSession`, `TokenBase`, `TokenBaseKnown` in Started before launch. Failed save creates no child. Run/card/session identity binding and durable save completion are oracles. |
| `Crash`, `Restart`, `Adopt` | Restore the original saved baseline and presence bit; never reset the baseline to current usage. A legacy missing baseline remains unknown. |
| `Spend`, `Poll`, `LoseUsage`, `RegressBucket` | The soft poll checks cumulative usage. Missing/error, changed session, any bucket below baseline or delta at/above cap causes owned HOLD/cancel. Spend can exceed the cap before an observation; no hard ceiling is claimed. |
| `WorkerReport`, `ApplyCheck` | An absent report can become HOLD. An existing HOLD is safe. An existing non-HOLD is preserved and blocks settlement with Started/capacity retained when usage is unsafe. The model excludes an external writer replacing an existing report. |
| `FinalCheck`, `Settle` | Final-turn checks bypass polling cadence. The central endCard gate uses persisted baseline/session before completion, including synthetic recovery and STOP; an existing HOLD can pass without another usage read. Here settlement abstracts local completion/report or STOP-return debt; it is not authority to publish a server verdict behind STOP. |
| `NativeExit`, `ProvePrimaryQuiet`, `ProveAuxQuiet`, `AckSettlement`, `Release` | HOLD/cancel does not remove native debt. Capacity/Started releases only after positive primary/auxiliary quiet and durable local settlement/notice handoff. For an existing REPORT, runtime finishNote journals the notice before Started removal; this is not a requirement for prior canonical server Finish acknowledgement. Actual group identities, journal durability and finish transport are separate obligations. |

All twenty declared outcomes passed on Linux from exact model source
`ab6ac959f51f28435a47ac47f201967e1fd89c2f`. Normal safety explored
260,108 distinct states; the legacy Started case explored 31,896. The seven
mutants and eleven witnesses produced their declared invariant counterexamples.
Prior 776 CASES/RUNS rows remain unchanged. The cases include two normal cases; seven mutants
for unknown claim, reset on adoption, ignored missing sample or regression,
skipped final gate, overwritten report and premature capacity release; eleven
witnesses for known zero, refusal without artifacts, restored spend, missing live
debt, component regression, non-HOLD preservation, STOP final check, uncapped
native watcher, soft overshoot, legacy adoption and synthetic recovery.

The source contract is frozen at `39d874bb0de80a7bc77228ce7cd57ed8f344a43b`
in the separately owned runtime checkout. This is a contract model, not
executable alignment evidence. The bridge plan projects real DSH fixtures:
validated rc.2/v4 header/session/cwd and complete usage projection; preclaim
missing-usage refusal; Started save with known zero or nonzero baseline; actual
restart/adoption preserving it; component regression despite a larger total;
missing poll/final sample; safe HOLD versus retained non-HOLD; quiet and settlement
before Started removal. Native watcher use with explicit cap0 remains valid.
Unsupported/ambiguous/newer formats map to unavailable in the model; validating
those formats and full checksummed concatenated-zstd completion needs source tests.

The model excludes parser/compression implementation, filesystem power loss,
multiple cards or sessions, arbitrary retries, store replay, native identity
proof, real poll clocks and fairness. Reachable drain is a chosen trace, not a
universal completion or strict provider-spend bound.
