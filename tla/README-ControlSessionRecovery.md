# Control session recovery

`ControlSessionRecovery.tla` models one lane's durable `OpenPending` entry,
one session binding, and retained primary and auxiliary native ownership.
The truth lives in `LaneState` saved by `SaveLanes` and the owned native RUN
receipts. The bounded model has two open attempts, one seed, one message,
one restart, and a mode change. The value occupies much less than 1 KiB;
this finite diagnostic fits one process. It adds no production protocol.

Source mapping uses the control recovery donor `23707720b9b054bf752508bf0959c25952a69c17`.
Source review, formatting, source gates and model measurements are separate
obligations. The 21 declared cases produce their expected outcomes on the assigned Linux
bench: one safety pass, seven reachable witnesses and thirteen reversed
boundary witnesses. The normal exploration generates 266,926 states with
79,474 distinct states; every named witness produces an actual counterexample.
Measured rows are in `RUNS.tsv`. Raw logs and ordered traces are retained in
`release-evidence-v1.2/recovery-20261009/control-session-recovery-model` outside
the checkout. The tested model source is
`190a4f347b495aa0a180574cfed4cfc3408e40f2`; the pinned jar SHA-256 is
`936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`.
These are model outcomes, not source mutation or native trace results.

Open recovery and binding require
a matching current harness. Control evidence collection requires authoritative
loaded state and journal-free quiet; it is independent of the current harness.
Their reversed cases preserve the diagnosed failure paths.

| Action or predicate | Source obligation |
| --- | --- |
| `SaveIntent`, `BeginOpen` | `laneStep` saves `nativeOpenPending{Job}` before `OpenSession` |
| `NativeSeedStart` | Primary process-start callback saves RUN PID/birth before the existing native launch gate releases |
| `BeginInspection` | `inspect` gives session-list, usage or price its own retained receipt |
| `ReturnKnownID`, `SaveReturnedID` | `controlResultQuiet` preserves a valid ID even when inspection returns an error; result job equals the pending job |
| `SaveFailure` | `saveControlOpen` and `persistOpenedSession` restore the old durable projection on save failure |
| `VerifyPrimaryQuiet`, `VerifyAuxQuiet` | `stopRunQuiescent` / `inspectionsQuiescent` require qualified group proof; unknown or corrupt ownership holds |
| `BindSession` | `persistOpenedSession` saves Sessions and removes OpenPending in one SaveLanes call |
| `ClearNeverSeeded` | `recoverControlOpen` clears an ID-less attempt only after authoritative no-RUN and auxiliary quiet |
| `Crash`, `Restart`, `LoadKnown`, `LoadError` | Restart loses volatile results, loads durable LaneState; a failed load cannot erase ownership |
| `LoadMalformedID`, `ValidID`, `SwitchHarness` | Malformed saved ID or a different current harness holds instead of binding |
| `BeginMessage`, `BeginBatch`, `ControlDebt` | `messageTurn`, idle lane scan and `batchLaneDebt` fence retained open/message receipts across modes |
| `CollectSettledReceipts` | `controlRunsQuiescent` removes settled roots only after known LaneState, quiet and no matching OpenPending |

The aggregate primary/auxiliary value represents all matching retained groups,
not the last harness return. A missing, malformed or unreadable inspection RUN
is `unknown`. Quiet means every relevant group has been verified quiescent.
Multiple commands, lanes, cards, queues, ordinary legacy batch execution and
provider correctness are outside this bound. Batch admission is only a single
observable boundary checked against prior control debt.

The primary absence assumption is essential. The native launcher durably
writes its owned primary RUN before release, retains it while its pending
open is journaled, and does not classify corrupt/read-error receipts as
absent. Thus no primary RUN plus auxiliary quiet proves this attempt did not
seed. `CollectSettledReceipts` may restore absence only after the journal is
removed and settlement is verified. The model does not prove filesystem
protection, callback durability or actual OS process identity. Unauthorized
receipt deletion cannot be interpreted as the model's `absent` value.
The source author confirms retention and pre-release ordering; native tests
must verify those seams independently.

A fresh successful intent save arms exactly one in-process open call. Restart
does not re-arm an old journal entry: the no-RUN recovery path must first
durably clear it and create a fresh job identity.

A valid returned ID is saved before result release. A crash before a failed
ID save can still lose its volatile ID: the remaining primary RUN then holds
an ambiguous open indefinitely. The model deliberately promises safety, not
automatic recovery from an unknowable ID. No TTL or expiry proves revocation.
A known valid ID permits adoption only after all groups are quiet and the
current harness matches the OpenCode-owned pending intent. Generic saved
Sessions migration across adapters is outside this repair. An existing
bound session may later own a live message; binding itself is not a global
quiet certificate.

The normal case checks the safety conjunction. Seven reachable witnesses require
error-ID restart adoption, failed-save retention, safe prelaunch retry, message
restart hold, batch restart hold malformed-ID hold and mismatched-harness refusal. Thirteen reversed
boundary cases require counterexamples: discard error ID, repeat a seed,
early binding, clear a present RUN, message bypass, batch bypass, seed before
journal, wrong result job, malformed-ID binding, failed-load overwrite and
journaled receipt collection, cross-harness binding and collection with an
unknown journal snapshot. The `bad` set records violations at the action
boundary, so later quiet or successful saves cannot erase an earlier violation.
These cases are discriminating model mutations, not source mutation results.

The bridge evidence must distinguish real native RUN/PID/birth/group probes
from simulated daemon/store hooks. Required projections include durable intent
save, primary RUN save before launch release, returned ID plus error, ID save
success/failure, auxiliary hold/verified quiet, atomic binding readback,
restart known/failed load, unknown-ID hold, no-RUN retry, message debt and batch
admission refusal. No formal runtime trace replay is claimed until actual
observations are projected and checked against these actions. This model does
not establish authentication, token accounting, provider work, readiness of
all adapters, installation or a release.

Run only on the assigned serialized Linux bench, with private Java temporary
storage and a nonblocking shared gate. Use the repository `tlacheck run`
`--group controlsessionrecovery --workers 1` runner; preserve parse errors and
raw logs, and append measured RUNS rows only after actual execution. A shared
module parse error ends the batch rather than repeating the same error.

The named follow-up obligation `control-session-recovery-native-trace` is
this companion's pending evidence brief, authorized by the coordinator for
the inspection recovery repair. It is not claimed queued or assigned.
Its scope is a provider-free native witness for valid ID plus inspection
error, durable ID save, restart, verified primary/auxiliary quiet and atomic
binding readback; also no-RUN retry and cross-mode control debt refusal.
The witness must record existing callback and receipt boundaries rather than
invent a new runtime trace protocol. Synthetic receipt probes are labelled
as unit witnesses, and simulated loop hooks do not prove an OS exit.
Completion requires projecting actual ordered observations onto the model
and documenting remaining abstraction limits. Until then coverage is partial,
even after all declared TLC cases produce their expected outcomes.
