--------------------------- MODULE ToolResultBinding ---------------------------
\* SPEC-FRIEND.md, Active Codex tool-result binding. An explicit root tool wait
\* owns a short lease; durable transport emission is never receipt or proof.
EXTENDS Naturals, FiniteSets
CONSTANTS Events, MaxTime, LeaseTicks, Broken
VARIABLES now, binding, until, pending, emitted, output, proven, wrongEmission,
          lostPending
vars == <<now, binding, until, pending, emitted, output, proven, wrongEmission,
          lostPending>>
Live == binding = "root" /\ now < until
Init == /\ now = 0 /\ binding = "none" /\ until = 0
        /\ pending = {} /\ emitted = {} /\ output = {} /\ proven = {}
        /\ wrongEmission = FALSE /\ lostPending = FALSE
Arm(who) == /\ who \in {"root", "other"} /\ now < MaxTime
            /\ (binding = who \/ now >= until)
            /\ binding' = who /\ until' = now + LeaseTicks
            /\ UNCHANGED <<now, pending, emitted, output, proven,
                           wrongEmission, lostPending>>
Tick == /\ now < MaxTime /\ now' = now + 1
        /\ UNCHANGED <<binding, until, pending, emitted, output, proven,
                       wrongEmission, lostPending>>
\* Release preserves the historical scope; Stage can retain after disarming.
Stop == /\ binding = "root" /\ until' = now
        /\ UNCHANGED <<now, binding, pending, emitted, output, proven,
                       wrongEmission, lostPending>>
Stage(e) == /\ e \in Events \ emitted
            /\ binding \in {"root", "other"}
            /\ pending' = pending \cup {e}
            /\ UNCHANGED <<now, binding, until, emitted, output, proven,
                           wrongEmission, lostPending>>
Deliver(e) == /\ e \in Events \ emitted
              /\ (Live \/ "no-lease" \in Broken)
              /\ emitted' = emitted \cup {e} /\ pending' = pending \cup {e}
              /\ wrongEmission' = (wrongEmission \/ ~Live)
              /\ proven' = IF "emit-is-proof" \in Broken
                            THEN proven \cup {e} ELSE proven
              /\ UNCHANGED <<now, binding, until, output, lostPending>>
Output(e) == /\ e \in pending /\ Live
             /\ output' = output \cup {e}
             /\ emitted' = emitted \cup {e}
             /\ UNCHANGED <<now, binding, until, pending, proven,
                            wrongEmission, lostPending>>
Ack(e) == /\ e \in pending
          \* Output-before-Ack is the trusted root wrapper's caller contract.
          /\ (e \in output \/ "ack-before-output" \in Broken)
          /\ pending' = pending \ {e}
          /\ lostPending' = (lostPending \/ e \notin output)
          /\ UNCHANGED <<now, binding, until, emitted, output, proven,
                         wrongEmission>>
Answer(e) == /\ e \in output /\ proven' = proven \cup {e}
             /\ UNCHANGED <<now, binding, until, pending, emitted, output,
                            wrongEmission, lostPending>>
Next == (\E who \in {"root", "other"}: Arm(who)) \/ Tick \/ Stop
        \/ (\E e \in Events: Stage(e) \/ Deliver(e) \/ Output(e) \/ Ack(e) \/ Answer(e))
Spec == Init /\ [][Next]_vars
TypeOK == /\ now \in 0..MaxTime /\ binding \in {"none", "root", "other"}
          /\ until \in Nat /\ pending \subseteq Events /\ emitted \subseteq Events
          /\ output \subseteq emitted /\ proven \subseteq Events
LeaseRequired == ~wrongEmission
OutputRequiredForProof == proven \subseteq output
PendingRetainedUntilOutput == ~lostPending
=============================================================================
