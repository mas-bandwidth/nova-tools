--------------------------- MODULE DSHSessionOpen ---------------------------
(* One DSH lane and its private open intent. Native first-turn execution can
   produce zero, one or multiple session records; only one successful record
   can be receipted. SaveLanes is a separate durability boundary shared by all
   one-shot harnesses. A crash loses only in-memory pending/ready state. *)
EXTENDS Naturals
CONSTANT Fault
VARIABLES phase, intent, nativeCalls, created, receipt, pending, durable, ready,
          sawFailedSave, sawAmbiguity
vars == <<phase, intent, nativeCalls, created, receipt, pending, durable, ready,
          sawFailedSave, sawAmbiguity>>
IDs == {"session-a", "session-b"}
Init == phase = "absent" /\ intent = "absent" /\ nativeCalls = 0
    /\ created = "none" /\ receipt = "none" /\ pending = "none"
    /\ durable = "none" /\ ready = FALSE /\ sawFailedSave = FALSE
    /\ sawAmbiguity = FALSE
Start == phase = "absent"
    /\ phase' = "running" /\ intent' = "pending" /\ nativeCalls' = nativeCalls + 1
    /\ UNCHANGED <<created, receipt, pending, durable, ready, sawFailedSave, sawAmbiguity>>
NativeOne(id) == phase = "running" /\ id \in IDs
    /\ phase' = "native-done" /\ created' = id
    /\ UNCHANGED <<intent, nativeCalls, receipt, pending, durable, ready, sawFailedSave, sawAmbiguity>>
NativeAmbiguous == phase = "running"
    /\ phase' = "held" /\ sawAmbiguity' = TRUE
    /\ UNCHANGED <<intent, nativeCalls, created, receipt, pending, durable, ready, sawFailedSave>>
SaveReceipt == phase = "native-done" /\ created \in IDs
    /\ phase' = "completed" /\ intent' = "completed" /\ receipt' = created
    /\ UNCHANGED <<nativeCalls, created, pending, durable, ready, sawFailedSave, sawAmbiguity>>
ReceiptFailure == phase = "native-done"
    /\ phase' = "held" /\ sawAmbiguity' = TRUE
    /\ UNCHANGED <<intent, nativeCalls, created, receipt, pending, durable, ready, sawFailedSave>>
ReuseCompleted == phase = "completed" /\ intent = "completed" /\ receipt \in IDs
    /\ phase' = "binding" /\ pending' = receipt
    /\ UNCHANGED <<intent, nativeCalls, created, receipt, durable, ready, sawFailedSave, sawAmbiguity>>
SaveLaneFailure == phase = "binding"
    /\ sawFailedSave' = TRUE
    /\ UNCHANGED <<phase, intent, nativeCalls, created, receipt, pending, durable, ready, sawAmbiguity>>
SaveLaneSuccess == phase = "binding" /\ pending = receipt
    /\ phase' = "ready" /\ durable' = pending /\ ready' = TRUE /\ pending' = "none"
    /\ UNCHANGED <<intent, nativeCalls, created, receipt, sawFailedSave, sawAmbiguity>>
Crash == phase \in {"running", "native-done", "completed", "binding", "ready"}
    /\ phase' = (IF intent = "completed" THEN "completed" ELSE "held")
    /\ pending' = "none" /\ ready' = FALSE
    /\ UNCHANGED <<intent, nativeCalls, created, receipt, durable, sawFailedSave, sawAmbiguity>>
(* Two deliberately broken actions are enabled only by witness configurations. *)
BadRepeat == Fault = "repeat" /\ phase = "held" /\ intent = "pending"
    /\ nativeCalls' = nativeCalls + 1
    /\ UNCHANGED <<phase, intent, created, receipt, pending, durable, ready, sawFailedSave, sawAmbiguity>>
BadReady == Fault = "early-ready" /\ phase = "binding"
    /\ phase' = "ready" /\ ready' = TRUE
    /\ UNCHANGED <<intent, nativeCalls, created, receipt, pending, durable, sawFailedSave, sawAmbiguity>>
Next == Start \/ (\E id \in IDs : NativeOne(id)) \/ NativeAmbiguous
     \/ SaveReceipt \/ ReceiptFailure \/ ReuseCompleted
     \/ SaveLaneFailure \/ SaveLaneSuccess \/ Crash \/ BadRepeat \/ BadReady
Spec == Init /\ [][Next]_vars
TypeOK == /\ phase \in {"absent", "running", "native-done", "held", "completed", "binding", "ready"}
          /\ intent \in {"absent", "pending", "completed"}
          /\ nativeCalls \in 0..2
          /\ created \in IDs \cup {"none"} /\ receipt \in IDs \cup {"none"}
          /\ pending \in IDs \cup {"none"} /\ durable \in IDs \cup {"none"}
          /\ ready \in BOOLEAN /\ sawFailedSave \in BOOLEAN /\ sawAmbiguity \in BOOLEAN
AtMostOneNativeOpen == nativeCalls <= 1
ReadyNeedsDurableBinding == ready => (durable = receipt /\ receipt = created /\ receipt \in IDs)
PendingUsesReceiptedID == pending # "none" => pending = receipt
AmbiguityNeverReady == sawAmbiguity => ~ready
WitnessReady == ~ready
WitnessFailedSave == ~sawFailedSave
WitnessAmbiguity == ~sawAmbiguity
=============================================================================
