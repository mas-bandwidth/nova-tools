# Priority inheritance at STOP return

`PriorityStopReturn.tla` fills a gap between the existing priority and STOP models.
`PriorityQueued` permits promotion while running but has no return-to-queue event;
its STOP is absorbing and its desired selector reads live producer priority.
`StopReturn` covers cancellation, generation and owner preservation without
priority fields. Neither validates inherited metadata after STOP return.

The separately owned source repair is `63c35c322c07430ec3f94af47aed41b369af0819`.
The sprint snapshot is authoritative. Two producer scalars, two consumer records
and a bounded receipt fit in one process (under one KiB of scalar state). The
pattern is one atomic stopped-owner return: resolve the producer link, preserve
owner/branch/attempt, advance generation and refresh priority before requeue.
Libraries considered: existing TLA Naturals/Sequences and the repository runner.

| Model action | Source mapping and boundary |
|---|---|
| `Promote` | `SetPriority` updates the Work producer. The active Working/Reading consumer retains its taken metadata; queued peers can be refreshed separately. |
| `Stop`, `CancelQuiet` | The STOP owner receipt establishes quiescence. Actual process identity/termination is an oracle here and belongs to native debt models/tests. |
| `Return` | `Store.StopReturnStep` must load current Work alongside Fleet/Readers; `StopReturn` resolves the consumer's authoritative primary and applies `consumerPriorityFields` in the same gen+1 move unit. Working returns Ready; Reading returns Asked. |
| `ReplayReturn` | The old generation's repeated stop receipt is idempotent; it leaves the returned generation, identity and priority unchanged. |
| `Resume`, `Launch`, `Finish` | Explicit start follows returned debt. `QueueOrder` orders stored inherited fields, then fixed read/fix roles use producer urgency and ties retain earlier score order. An independent live-producer monitor checks the chosen eligible consumer. |

Twelve cases cover work/read/fix promotion and work demotion: four normal safety
cases, four omitted-refresh order diagnostics, and four reachable return/replay/
resume/drain witnesses. FIX stays highest and non-preemptive; READER stays its
fixed role. A changed producer affects active metadata only when that consumer
returns to the queue. All twelve declared outcomes passed on Linux from exact model source
`06adfa60bb56bbc6bec57aabb1781313811a09bc`. Each normal case explored 30
distinct states; each diagnostic and witness produced its declared order or
reachability counterexample. All previous 764 CASES/RUNS rows are preserved
unchanged.

The source bridge plan is a store fixture: take/begin an existing consumer,
change the linked producer while it is active, STOP, send its exact generation
receipt, observe the returned gen+1 fields and unchanged owner/branch/attempt,
replay that receipt, start and compare actual queue/Take/read-begin order against
the peer. Tests must use `Store.StopReturnStep` so a pure planner snapshot cannot
hide a missing Work load. Source repair, executable receipts and model outcomes
remain distinct evidence.

This instance has two consumers, one priority change, one STOP/resume and one
return. It excludes concurrent store commits, missing producer links, new work,
arbitrary stream turns, corrupt metadata, multiple STOP cycles, actual native
quiet, transport retries beyond the duplicate receipt, fairness and real-time
progress. Drain is a reachable witness under chosen completions, not universal
liveness under unbounded urgent arrivals.
