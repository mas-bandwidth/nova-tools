-------------------------- MODULE SprintRecovery --------------------------
EXTENDS Naturals, TLC
CONSTANT MaxVersion
VARIABLES version, saved, planned, eligible, prepared, applied, receipt
vars == <<version, saved, planned, eligible, prepared, applied, receipt>>

Init == /\ version = 0 /\ saved = 0 /\ planned = FALSE
        /\ eligible = FALSE /\ prepared = FALSE /\ applied = FALSE
        /\ receipt = -1

Plan == /\ ~applied /\ saved' = version /\ planned' = TRUE
        /\ eligible' \in BOOLEAN /\ prepared' = FALSE
        /\ UNCHANGED <<version, applied, receipt>>

Drift == /\ version < MaxVersion /\ version' = version + 1
         /\ prepared' = FALSE
         /\ UNCHANGED <<saved, planned, eligible, applied, receipt>>

Prepare == /\ planned /\ eligible /\ saved = version /\ ~applied
           /\ prepared' = TRUE
           /\ UNCHANGED <<version, saved, planned, eligible, applied, receipt>>

Commit == /\ prepared /\ eligible /\ saved = version /\ ~applied
          /\ applied' = TRUE /\ receipt' = saved /\ prepared' = FALSE
          /\ UNCHANGED <<version, saved, planned, eligible>>

Replay == /\ applied /\ receipt = saved /\ UNCHANGED vars
Next == Plan \/ Drift \/ Prepare \/ Commit \/ Replay
Spec == Init /\ [][Next]_vars
TypeOK == /\ version \in 0..MaxVersion /\ saved \in 0..MaxVersion
          /\ planned \in BOOLEAN /\ eligible \in BOOLEAN
          /\ prepared \in BOOLEAN /\ applied \in BOOLEAN
          /\ receipt \in {-1} \cup (0..MaxVersion)
NoUnsafePrepare == prepared => (planned /\ eligible /\ saved = version /\ ~applied)
NoInventedReceipt == applied => (receipt = saved /\ eligible /\ planned)
=============================================================================
