# Deferred work delivery and native admission

`DeferredStart.tla` is a bounded diagnostic of the batch path in
`internal/friend/daemon.go` and `inbox.go` at `b5e21b480`. It separates the
staged BRIEF, queued work-bearing turn, delivery attempt, retained deferral,
accepted native queue, actual native start, and active completion. The atomic
`Admit` in `SprintPause.tla` does not represent those separate boundaries.

The authoritative machine word lives in the coordinator store; the daemon
owns its staged/deferred turn, and the native session owns any accepted queue.
One modeled job has a phase, a bounded attempt count and fifteen Boolean
state/history fields: less than 128 bytes of payload. This is one small record,
not a new production cache. One hundred independent profiles repeat the same
record; fleet assignment and priority ordering belong to other models. The
pattern is staged work, bounded delivery attempts, fresh native admission, and
ordinary completion of work that already started.

The source checks `machineMayLaunch` before initially calling `startDealt`,
whose text says to read each brief and start. A deferred turn stays in
`l.busy`; its retry branch calls `startTurn` without that check. The model's
current case permits that work-bearing retry and native launch without fresh
admission. Its witness requires deferral, pause, retry while paused, then
native start while paused. No actual native trace is claimed by this source
path witness.

`GuardRetryDelivery` represents a fresh check before retrying a work-bearing
turn. Ordinary messages, pongs and control communications are outside this
job lifecycle and remain permitted during pause. Accepted delivery is not
native start. A delivery-only gate still permits an accepted native queue to
start after pause; a separate witness requires queue acceptance before pause
and native start after it.

`NativeGuard = "atomic"` is the **required admission contract**, not an
implemented-code assertion: the actual start and current authorization form
one modeled operation. It holds starts while paused, stopped or authorization
is unknown. `NativeGuard = "cached"` reads permission in one operation and
starts in another; its witness exposes pause, stop or unknown authorization
between the read and start. A prestart read alone does not serialize native
launch with a concurrent pause. The source owner must identify the concrete
admission/start boundary and its ordering before claiming the required guard.

Pause preserves the staged/deferred/active assignment and attempt count.
Active work can complete while paused, and a paused deferred turn can retry
and start after unpause. Stop remains enabled and cannot be undone by
unpause. Finish after stop abstracts settlement only; this model does not
replace `StopCancels` or `StopReturn` native cancellation/return obligations.
There is no unconditional liveness or assumption that a deferral resolves.

The eight `deferredstart` cases cover the ideal safety contract, current
delayed-start witness, missing retry guard, delivery-only limitation, missing
native guard, stale native permission, paused active drain, and deferred
resume. They use one job and at most two delivery attempts. Expected failing
properties are individually named in `CASES.tsv`; unrelated errors fail the
case.

## Bounded diagnostic results

All eight cases pass at source `13608eceb`. The pinned TLC jar runs on Linux
with Java 21.0.12.1, a private Java temporary directory and the shared bench
lock; the suite takes 7.222 seconds within its 20-second bound. `RUNS.tsv`
contains eight current hash-bound rows. The ideal control explores 200
distinct states and satisfies retry authorization, no native start while
held, preservation of assignments by pause, active drain and stopped unpause
refusal.

| Case | Distinct states | Expected result |
|---|---:|---|
| Current deferred after pause | 130 | Exact delayed-start witness reached |
| Missing retry check | 41 | `RetryDeliveryNeedsAuthority` violated |
| Delivery-only gate | 49 | Queued-before-pause start witness reached |
| Missing native guard | 49 | `NoNewNativeStartWhileHeld` violated |
| Cached native permission | 71 | Read, pause, actual start witness reached |
| Paused active drain | 70 | Active completion while paused reached |
| Deferred resume | 113 | Paused deferred job starts after unpause |

The current-path counterexample is `StageBrief`, `QueueStart`,
`BeginDelivery`, `DeliveryDeferred`, `Pause`, `BeginDelivery`,
`DeliveryAccepted`, `NativeStart`. The cached-read counterexample inserts
`ReadNativePermit`, `Pause`, `NativeStart` after queue acceptance. These are
model counterexamples over inspected source boundaries, not an observed
native run. Logs are retained under
`release-evidence-v1.2/recovery-20261008/deferred-start-model/run-01/`.

## Concrete trace bridge

The source regression should record ordered events with one public attempt
identity: `brief_staged`, `work_delivery_begin`, `delivery_deferred`,
`machine_paused` or `machine_stopped`, `work_retry_begin`,
`delivery_accepted`, `native_start`, and `active_finish`. Record authorization
as RUNNING/PAUSED/STOPPED/UNKNOWN at retry and actual native start, plus the
attempt identity returned by delivery. Do not put prompt bodies, credentials
or provider output in markers. A delivered turn or successful command exit is
insufficient evidence of native start. The queued-after-pause case needs an
independently observed native queue/start boundary; if the harness exposes no
such boundary, retain the limitation instead of treating delivery gating as
proof of no native start.
