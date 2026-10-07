------------------------------ MODULE Delivery ------------------------------
(***************************************************************************)
(* The daemon's delivery after a gap (internal/friend/present.go,          *)
(* docs/SPEC-FRIEND.md, The present). A gap is a new session, a relaunch,  *)
(* a stretch with no delivery, or the friend's own present request. A      *)
(* delivery that closes a gap carries the present and no superseded        *)
(* message. Broken = "backlog" is the reversed witness: the delivery after *)
(* a gap still carries the old messages.                                   *)
(***************************************************************************)
EXTENDS Naturals

CONSTANTS MaxPending, Broken

VARIABLES gap,     \* a start or a quiet stretch is open
          pending, \* superseded messages still unacked
          last,    \* what the last delivery carried
          closed   \* the last delivery closed a gap

vars == <<gap, pending, last, closed>>

Init == /\ gap = FALSE
        /\ pending = 0
        /\ last = "none"
        /\ closed = FALSE

Arrive == /\ pending < MaxPending
          /\ pending' = pending + 1
          /\ UNCHANGED <<gap, last, closed>>

Open == /\ gap' = TRUE
        /\ UNCHANGED <<pending, last, closed>>

Deliver ==
  IF gap
  THEN /\ IF Broken = "backlog"
             THEN /\ last' = "backlog"
                  /\ pending' = pending
             ELSE /\ last' = "present"
                  /\ pending' = 0
       /\ closed' = TRUE
       /\ gap' = FALSE
  ELSE /\ pending > 0
       /\ last' = "message"
       /\ closed' = FALSE
       /\ pending' = pending - 1
       /\ UNCHANGED gap

Next == Arrive \/ Open \/ Deliver

Spec == Init /\ [][Next]_vars

TypeOK == /\ gap \in BOOLEAN
          /\ pending \in 0..MaxPending
          /\ last \in {"none", "present", "message", "backlog"}
          /\ closed \in BOOLEAN

\* a delivery after a gap carries the present and no superseded message
DeliveryAfterGap == closed => last = "present" /\ pending = 0

=============================================================================
