# Durable native launch-held boundary (draft)

`NativeLaunchHeldBridge.tla` is a bounded refinement of the measured
`NativeCardStartBoundary` ordering. It separates a server StartAck from local
permission to release the pipe-blocked native child. A card's durable `Started`
record is marked `LaunchHeld` before the child is created. The process writes
its own primary RUN and anchor while still blocked. After an exact current
server ACK, the daemon's main loop checks the same card/job/generation/epoch,
lane, active RunID, owner and latest local PAUSE/STOP state, then durably clears
`LaunchHeld`; only that clear permits the gate to open. A same-operation
historical ACK is never permission to clear or release. The durable clear is
the local admission boundary: a PAUSE arriving after it does not retroactively
cancel that admitted turn. `RealExec` still checks its context before physical
release; this model does not assert an atomic check through provider exec.
If its context is canceled after the clear but before release, RealExec closes
the gate and the owned, quiet card settles a failed launch.
An in-memory held marker after a failed SaveLanes is not a durable claim;
the next attempt must retry the save before creating another native child.
The bounded callback context must still be live when the main loop accepts it.
A stale callback can be refused even when a later turn uses the same durable
card RunID.
The model's `callbackAlive` is phase dependent: it represents the bounded
request context at the main-loop clear and the parent RealExec context at
physical gate release. It does not claim one literal Go context spans both.

An ordinary failure before ACK cannot settle a held Ready card. A `cmd.Start`
failure or pre-run inspection refusal has no native child; only an observed
missing RUN with no PID and quiet auxiliary inspections permits another
attempt. A RUN written before an anchor failure leaves physical group debt;
the model holds it without assuming an anchor or inventing quiet proof. A
failed native launch can settle only after a valid ACK, a saved clear, and
verified group quiescence.

PAUSE before the clear keeps the gate closed, even if the server has already
committed Working+Started. The blocked child must quiesce. After unpause, a new
child can use a fresh caller operation only once the prior primary and any
auxiliary inspection are verified quiet; the server's current Working+Started
card yields an idempotent no-move ACK. The `Started.RunID` stays the same across
turns of that card, while each native child has a separate PID/RUN receipt.
Callbacks with a replaced RunID, an expired context, or a changed card cannot
clear the reservation. STOP retains stop-return debt until all owned groups
are quiet. A crash before durable clear retains the held reservation;
a crash after clear but before release cannot justify a blind provider rerun
and must settle a failed launch truthfully after quiet proof.
If an unsaved in-memory between-turn hold is lost in a crash after PAUSE lifts,
the durable unheld record also cannot justify a blind rerun; recovery settles
the quiet run failed. Ordinary successful between-turn rehold is modeled too.

The normal case is intended to check ACK and durable-clear order, active-run
and PAUSE/STOP checks, prior-group quiescence before retry, and completion
capacity. Reversed cases will each remove one guard: durable hold before
launch, clear requirement, exact active run, expired callback, post-clear
context cancellation, PAUSE fence,
historical-reply exclusion, or prior-group quiet.
Witness cases will establish the relevant reachable sequences, including
lost ACK, PAUSE then fresh retry, STOP return, stale callback, clear-save
failure, failed between-turn hold save and retry, and crashes on both sides of
the clear. Separate witnesses cover pre-ACK no-child retry and a physical
pre-anchor error that remains held. All 26 cases are registered and the
exact `be7f4a2f78bc418f48795db9f8c1db6b482aca28` model source was measured
on serialized Vision: one normal safety pass explored 213,037 generated and
64,556 distinct states, while 25 specifically named reversed or reachability
cases reached their expected counterexample. Final callback source and native
trace alignment remain separate obligations.

This finite machine does not prove Redis, disk fsync, actual group birth/PGID,
callback scheduling, provider execution, or eventual settlement. Those
require separately gated executable traces. The earlier 23 measured
`NativeCardStartBoundary` outcomes remain unchanged and retain their own
partial coverage scope.
