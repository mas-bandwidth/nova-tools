# Read BEGIN intent and recovery

`ReadBeginRecovery.tla` models one read lease before native admission. The
lease binds its reader, ID, generation, epoch and head. Its stable BEGIN
operation binds the same identity except the head, matching `readOp` in
`internal/friend/read_lanes.go`. A same-generation changed head must therefore
hold the saved intent instead of rebinding or replaying it for new content.

Source contract: `ed95aec458189e7d627a37ea07b3e43b9f79e6fc`, separately owned
from this model. `beginRead` requires a successful queue revision newer than
the last attempt, an exact same lease, and current native/control authority.
It saves `BEGIN.json` before sending the RPC. An accepted response lost before
`ACTIVE.json` exists can be recovered after restart: refresh the queue, see
the same reading lease, and replay the exact stable operation. A failed queue
observation or save does not justify sending or launching.

| Model action | Source boundary / executable obligation |
|---|---|
| `PrepareBegin`, `RefreshQueue`, `QueueFailure` | `beginRead`, `queueRevision`, `beginAfter` and `reconcileReadBegins` require a newer successful queue and exact saved lease. A native-authority validator is an oracle in this model. |
| `SaveIntent`, `FailIntentSave`, `SendBegin` | Durable BEGIN precedes RPC. Save failure leaves no RPC; the phase's stable op is preserved across retry/restart. |
| `CommitBegin`, `LoseBeginReply`, `AcceptBeginReply` | The server accepts asked-to-reading before an answer may be lost. A retry with the same stable op receives the accepted historical result only after fresh same-lease local authority. |
| `RecoverActive` | After restart, held ACTIVE can retry only after a successful same-lease reading queue and launch/capacity permission. This permission is captured before the separate clear and native gate. |
| `SaveActive`, `FailActiveSave`, `ClearIntent`, `FailIntentClear` | `startRead` saves ACTIVE with a held no-child reservation before clearing BEGIN. Failed ACTIVE save or BEGIN removal leaves intent/native admission held. |
| `AdmitOtherWork` | `childRoom` deduplicates BEGIN/ACTIVE by read ID. BEGIN alone owns a shared child slot even when no native process exists. |
| `SaveSettlement`, `CleanupStaleIntent` | ACTIVE or settlement evidence wins over stale-BEGIN cleanup. Ordinary ACTIVE-to-settlement handoff can clear BEGIN; stale cleanup cannot discard it over stronger evidence. |
| `SaveStopProof`, `SaveStopDebt`, `ApplyStopReturn`, `ConfirmStopReturn` | A BEGIN-only reading lease under STOP has no native child: persist STOP-NO-LAUNCH proof, then durable STOP debt, then return it. A save failure retains BEGIN. Native STOP debt waits for quiet proof. |
| `SendSettlement` | A durable STOP-owned lease cannot publish a verdict behind that debt. Existing settlement evidence is preserved and may settle under STOP when no STOP debt owns it. |

The source checks, BEGIN write, RPC and native gate are separate events.
`PrepareBegin` captures the permission check before save/RPC; it does not claim
atomic distributed authority through the network or filesystem. Native launch
has the later local PAUSE/STOP/control check. Positive quiet, save completion,
queue parsing and stable-op server semantics remain implementation oracles.

Nineteen registered cases contain one normal safety case, eight reversed
guards, and ten reachable sequences. They include accepted lost ACK plus
restart with BEGIN and no ACTIVE, then stable replay after that restart, BEGIN capacity, changed-head hold, intent/ACTIVE
save refusal, failed BEGIN removal, accepted lost ACK under STOP, failed STOP
debt save, stronger-evidence cleanup refusal, and completed settlement under
STOP. All nineteen declared outcomes passed on Linux from exact model source
`5e3eab8114ddde52500ed673d5190f1a5e897214`: the normal case explored
1,039,728 distinct states; the eight reversed guards and ten witnesses each
produced their declared invariant counterexample. The previous 745
CASES/RUNS rows and all prior models are unchanged.

The source bridge plan uses `read_begin_retry_test.go`'s real reader loop,
mocked server and durable files: record BEGIN existence before every RPC,
identical operation/lease arguments, successful queue revisions, actual
restart after an accepted lost reply, ACTIVE-before-BEGIN-removal, child-room
reservation, and STOP proof/debt save barriers. Project the observed events
onto these actions and compare native launch counts and retained evidence.
That projection and actual executable gates are separate from TLC outcomes.

The instance has one lease, two sends, three successful queue revisions and
one restart. A new generation permits stale intent cleanup; admission of the
next generation is a separate instance. It excludes actual Redis replay,
filesystem power-loss durability, corrupt-file parsing, multiple readers,
arbitrary retries, kernel/process identity, cross-host revocation, fairness
and eventual completion. The native result and group proof are abstract; this
companion is not a complete read lifecycle or provider proof.
