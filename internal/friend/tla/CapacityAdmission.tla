------------------------- MODULE CapacityAdmission -------------------------
(* SPEC-FRIEND, jobs capacity: a lane consumes one measurement no more than
   two beats old, counting the filesystem walk as well as the wait afterward.
   StampAfterWalk reverses the reported defect: slow walks seem new. *)
EXTENDS Naturals
CONSTANT StampAfterWalk
VARIABLES phase, age, stampedAge, admitted
vars == <<phase, age, stampedAge, admitted>>

Init == /\ phase = "idle" /\ age = 0 /\ stampedAge = 0 /\ admitted = FALSE
Begin == /\ phase = "idle" /\ phase' = "walking"
         /\ UNCHANGED <<age, stampedAge, admitted>>
Beat == /\ phase \in {"walking", "ready"} /\ age < 3
        /\ age' = age + 1 /\ UNCHANGED <<phase, stampedAge, admitted>>
EndWalk == /\ phase = "walking" /\ phase' = "ready"
           /\ stampedAge' = IF StampAfterWalk THEN age ELSE 0
           /\ UNCHANGED <<age, admitted>>
Consume == /\ phase = "ready" /\ phase' = "consumed"
           /\ admitted' = (age - stampedAge <= 2)
           /\ UNCHANGED <<age, stampedAge>>
Next == Begin \/ Beat \/ EndWalk \/ Consume
Spec == Init /\ [][Next]_vars
TypeOK == /\ phase \in {"idle", "walking", "ready", "consumed"}
          /\ age \in 0..3 /\ stampedAge \in 0..age /\ admitted \in BOOLEAN
FreshFromWalkStart == admitted => age <= 2
=============================================================================
