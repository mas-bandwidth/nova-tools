------------------------- MODULE PriorityDefaults -------------------------
EXTENDS Naturals, FiniteSets
\* SPEC-SPRINT Priority: an explicit card header overrides its stream default.
\* Ordinary SetStream sets existing ordinary cards and the future admission default.
\* FIX-role retention and secondary urgency belong to PriorityFlow/PriorityQueued.
CONSTANT Broken
Jobs == {"a", "b"}
Levels == {1,2,3,5,6}
VARIABLES streamDefault, present, level, violation, changed
vars == <<streamDefault, present, level, violation, changed>>
Init == /\ streamDefault = 5 /\ present = {} /\ level = [j \in Jobs |-> 5]
        /\ violation = FALSE /\ changed = FALSE
Add(j,override) == /\ j \notin present /\ override \in Levels \cup {7}
                   /\ present' = present \cup {j}
                   /\ level' = [level EXCEPT ![j] = IF Broken = "default" THEN streamDefault ELSE IF override # 7 THEN override ELSE streamDefault]
                   /\ violation' = (violation \/ (override # 7 /\ level'[j] # override))
                   /\ UNCHANGED <<streamDefault,changed>>
SetStream(l) == /\ l \in Levels /\ ~changed
                /\ streamDefault' = l /\ changed' = TRUE
                /\ level' = [j \in Jobs |-> IF j \in present THEN l ELSE level[j]]
                /\ UNCHANGED <<present,violation>>
Next == (\E j \in Jobs, p \in Levels \cup {7} : Add(j,p)) \/ (\E l \in Levels : SetStream(l))
Spec == Init /\ [][Next]_vars
TypeOK == /\ streamDefault \in Levels /\ present \subseteq Jobs /\ level \in [Jobs -> Levels]
HeaderWins == ~violation
=============================================================================
