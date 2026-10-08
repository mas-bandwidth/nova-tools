--------------------------- MODULE WakeCursor ---------------------------
EXTENDS Naturals
CONSTANTS MaxLines, Broken
VARIABLES lines, saved, offset, shown, armed, generation, bound, refused
vars == <<lines, saved, offset, shown, armed, generation, bound, refused>>
Init == /\ lines = 0 /\ saved = 0 /\ offset = 0 /\ shown = {}
        /\ armed = FALSE /\ generation = 0 /\ bound = 0 /\ refused = FALSE
Append == /\ lines < MaxLines /\ lines' = lines + 1
          /\ UNCHANGED <<saved, offset, shown, armed, generation, bound, refused>>
Arm == /\ ~armed /\ bound = generation /\ saved <= lines
       /\ armed' = TRUE /\ offset' = IF Broken THEN lines ELSE saved
       /\ UNCHANGED <<lines, saved, shown, generation, bound, refused>>
ReadLine == /\ armed /\ generation = bound /\ offset < lines
            /\ offset' = offset + 1 /\ shown' = shown \cup {offset + 1}
            /\ saved' = offset + 1 /\ armed' = FALSE
            /\ UNCHANGED <<lines, generation, bound, refused>>
BusReturn == /\ armed /\ generation = bound /\ saved' = offset /\ armed' = FALSE
             /\ UNCHANGED <<lines, offset, shown, generation, bound, refused>>
Crash == /\ armed /\ armed' = FALSE
         /\ UNCHANGED <<lines, saved, offset, shown, generation, bound, refused>>
Truncate == /\ lines > 0 /\ lines' = lines - 1
            /\ UNCHANGED <<saved, offset, shown, armed, generation, bound, refused>>
Replace == /\ generation = 0 /\ generation' = 1
           /\ UNCHANGED <<lines, saved, offset, shown, armed, bound, refused>>
Refuse == /\ (generation # bound \/ saved > lines) /\ refused' = TRUE /\ armed' = FALSE
          /\ UNCHANGED <<lines, saved, offset, shown, generation, bound>>
Next == Append \/ Arm \/ ReadLine \/ BusReturn \/ Crash \/ Replace \/ Truncate \/ Refuse
Spec == Init /\ [][Next]_vars
NoSkipped == \A n \in 1..saved : n \in shown
NoForeignRead == (generation # bound) => saved = offset
=============================================================================
