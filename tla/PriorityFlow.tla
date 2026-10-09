--------------------------- MODULE PriorityFlow ---------------------------
EXTENDS Naturals, FiniteSets, Sequences, TLC

\* Final priority contract: FIX first and non-preemptive; READ has fixed READER
\* role, separate original producer urgency orders FIX/READ peers. The ordinary
\* ladder is FIX, BLOCKER, CRITICAL, HIGH, READER, NORMAL, LOW. Rework sets a FIX
\* role without replacing the producer's original urgency.
\* SPEC-SPRINT Priority, Rework priority, The deal, Pause and The machine.
\* Admission abstracts a committed assignment, not the physical runner. A friend
\* shares work/read room; fleet work and fleet reader rooms remain separate.
\* Desired adds an abstract blocker cancellation protocol. Production does not
\* implement that protocol. Victim choice is nondeterministic, strictly lower,
\* and never specifies a production selection or cancellation transport policy.
CONSTANTS Scenario, Desired, Broken, Controls, Lane
Owners == {"friend", "fleet", "reader"}
Ladder == <<"fix", "blocker", "critical", "high", "reader", "normal", "low">>
Jobs == CASE Scenario = "ladder" -> {"b", "c", "f", "h", "r", "n", "l"}
             [] Scenario = "dependency" -> {"parent", "urgent", "free"}
             [] Scenario \in {"cycle", "urgent-cycle"} -> {"repair", "normal"}
             [] OTHER -> {"blocker", "displaced"}
Capacity(o) == CASE Scenario = "ladder" -> IF o = "friend" THEN 7 ELSE 0
                   [] Scenario = "preempt" -> IF o = Lane THEN 1 ELSE 0
                   [] OTHER -> IF o \in {"friend", "reader"} THEN 1 ELSE 0
InitialLevel(j) == CASE Scenario = "ladder" ->
                          CASE j = "b" -> 1 [] j = "c" -> 2
                               [] j = "h" -> 3 [] j = "l" -> 6 [] OTHER -> 5
                       [] Scenario = "dependency" -> IF j = "urgent" THEN 2 ELSE 5
                       [] Scenario = "preempt" -> IF j = "blocker" THEN 1 ELSE 5
                       [] Scenario = "urgent-cycle" /\ j = "repair" -> 2
                       [] OTHER -> 5
Needs(j) == IF Scenario = "dependency" /\ j = "urgent" THEN {"parent"} ELSE {}
InitialState(j) == CASE Scenario = "ladder" /\ j = "r" -> "review"
                       [] Scenario \in {"cycle", "urgent-cycle"} /\ j = "repair" -> "review"
                       [] Scenario = "preempt" /\ j = "blocker" -> "hidden"
                       [] Scenario = "preempt" /\ j = "displaced" -> "work"
                       [] OTHER -> "ready"

VARIABLES state, level, fixRole, attempt, progress, owner, worker, debt, returned,
          mode, arrived, violations, seen, admissions
vars == <<state, level, fixRole, attempt, progress, owner, worker, debt, returned,
          mode, arrived, violations, seen, admissions>>
Init == /\ state = [j \in Jobs |-> InitialState(j)]
        /\ level = [j \in Jobs |-> InitialLevel(j)]
        /\ fixRole = [j \in Jobs |-> Scenario = "ladder" /\ j = "f"]
        /\ attempt = [j \in Jobs |-> 1]
        /\ progress = [j \in Jobs |-> InitialState(j) = "work"]
        /\ owner = [j \in Jobs |-> IF InitialState(j) = "work" THEN Lane ELSE "none"]
        /\ worker = [j \in Jobs |-> "other"]
        /\ debt = [j \in Jobs |-> [stage |-> "none", who |-> "none", attempt |-> 1, progress |-> FALSE]]
        /\ returned = {} /\ mode = "running"
        /\ arrived = (Scenario # "preempt")
        /\ violations = {} /\ seen = {} /\ admissions = <<>>
Active == {j \in Jobs : state[j] \in {"work", "read", "cancel", "quiescent"}}
Load(o) == Cardinality({j \in Active : owner[j] = o})
Read(j) == state[j] \in {"review", "read"}
WorkRank(j) == IF fixRole[j] THEN 0 ELSE level[j]
Rank(j) == IF Read(j) THEN 4 ELSE WorkRank(j)
Eligible(j,o) == /\ state[j] \in {"ready", "review"}
                 /\ IF Read(j) THEN o \in {"friend", "reader"} /\ worker[j] # o
                                  ELSE o \in {"friend", "fleet"}
                 /\ Needs(j) \subseteq {k \in Jobs : state[k] = "done"}
Ahead(k,j) == Rank(k) < Rank(j) \/
  (Rank(k) = Rank(j) /\ Read(k) /\ Read(j) /\ level[k] < level[j]) \/
  (Rank(k) = Rank(j) /\ fixRole[k] /\ fixRole[j] /\ level[k] < level[j])
Best(j,o) == \A k \in Jobs : Eligible(k,o) => ~Ahead(k,j)
BlockerWaiting(o) == \E j \in Jobs : Eligible(j,o) /\ Rank(j) = 1
Victim(j) == /\ Desired /\ j \in Active /\ state[j] \in {"work", "read"}
             /\ j \notin returned /\ Rank(j) > 1
             /\ BlockerWaiting(owner[j]) /\ Load(owner[j]) = Capacity(owner[j])
Emergency == \E j \in Jobs : Victim(j) \/ state[j] \in {"cancel", "quiescent"}

\* Violations are an independent event monitor: the broken selector still
\* admits, while the monitor recomputes competitors from the pre-state.
AdmitObserved(j,o,checkSelector) == /\ (mode = "running" \/ Broken = "paused")
              /\ Eligible(j,o) /\ Load(o) < Capacity(o)
              /\ (Best(j,o) \/ ~checkSelector \/ Broken = "reverse")
              /\ state' = [state EXCEPT ![j] = IF Read(j) THEN "read" ELSE "work"]
              /\ owner' = [owner EXCEPT ![j] = o]
              /\ violations' = violations \cup
                    (IF Best(j,o) THEN {} ELSE {"order"}) \cup
                    (IF mode = "running" THEN {} ELSE {"control"})
              /\ admissions' = Append(admissions,j)
              /\ seen' = seen \cup (IF j \in returned /\ Rank(j) = 1 THEN {"blockerStarted"} ELSE {})
              /\ UNCHANGED <<level, fixRole, attempt, progress, worker, debt, returned, mode, arrived>>
Admit(j,o) == AdmitObserved(j,o,TRUE)
WorkFinish(j) == /\ mode # "stopped" /\ state[j] = "work"
                 /\ state' = [state EXCEPT ![j] = "review"]
                 /\ owner' = [owner EXCEPT ![j] = "none"]
                 /\ worker' = [worker EXCEPT ![j] = owner[j]]
                 /\ progress' = [progress EXCEPT ![j] = TRUE]
                 /\ UNCHANGED <<level, fixRole, attempt, debt, returned, mode, arrived, violations, seen, admissions>>
ReadFinish(j) == /\ mode # "stopped" /\ state[j] = "read"
                 /\ state' = [state EXCEPT ![j] = IF Scenario \in {"cycle", "urgent-cycle"} /\ j = "repair" /\ attempt[j] = 1 THEN "held" ELSE "merge"]
                 /\ owner' = [owner EXCEPT ![j] = "none"]
                 /\ seen' = seen \cup (IF Scenario \in {"cycle", "urgent-cycle"} /\ j = "repair" /\ attempt[j] = 2 THEN {"fixReviewed"} ELSE {})
                 /\ UNCHANGED <<level, fixRole, attempt, progress, worker, debt, returned, mode, arrived, violations, admissions>>
Rework(j) == /\ mode = "running" /\ state[j] = "held" /\ attempt[j] < 2
             /\ state' = [state EXCEPT ![j] = "ready"]
             /\ fixRole' = [fixRole EXCEPT ![j] = TRUE]
             /\ attempt' = [attempt EXCEPT ![j] = @ + 1]
             /\ progress' = [progress EXCEPT ![j] = FALSE]
             /\ seen' = seen \cup {"fixOpened"}
             /\ UNCHANGED <<level, owner, worker, debt, returned, mode, arrived, violations, admissions>>
Merge(j) == /\ mode # "stopped" /\ state[j] = "merge"
            /\ \A k \in Jobs : state[k] = "merge" =>
              (WorkRank(k) > WorkRank(j) \/
               (WorkRank(k) = WorkRank(j) /\ (~fixRole[k] \/ ~fixRole[j] \/ level[k] >= level[j])))
            /\ Needs(j) \subseteq {k \in Jobs : state[k] = "done"}
            /\ state' = [state EXCEPT ![j] = "done"]
            /\ seen' = seen \cup (IF j = "repair" /\ "fixReviewed" \in seen THEN {"cycleDone"} ELSE {})
            /\ UNCHANGED <<level, fixRole, attempt, progress, owner, worker, debt, returned, mode, arrived, violations, admissions>>
Arrival == /\ ~arrived /\ state["blocker"] = "hidden"
           /\ arrived' = TRUE /\ state' = [state EXCEPT !["blocker"] = "ready"]
           /\ UNCHANGED <<level, fixRole, attempt, progress, owner, worker, debt, returned, mode, violations, seen, admissions>>
Reclaim(j) == /\ mode = "running" /\ Victim(j)
              /\ debt' = [debt EXCEPT ![j] = [stage |-> state[j], who |-> owner[j], attempt |-> attempt[j], progress |-> progress[j]]]
              /\ state' = [state EXCEPT ![j] = "cancel"]
              /\ seen' = seen \cup {"reclaimed"}
              /\ UNCHANGED <<level, fixRole, attempt, progress, owner, worker, returned, mode, arrived, violations, admissions>>
NativeEnded(j) == /\ state[j] = "cancel"
                  /\ state' = [state EXCEPT ![j] = "quiescent"]
                  /\ UNCHANGED <<level, fixRole, attempt, progress, owner, worker, debt, returned, mode, arrived, violations, seen, admissions>>
Return(j) == /\ state[j] = "quiescent" \/ (Broken = "early" /\ state[j] = "cancel")
             /\ state' = [state EXCEPT ![j] = IF debt[j].stage = "read" THEN "review" ELSE "ready"]
             /\ owner' = [owner EXCEPT ![j] = "none"]
             /\ returned' = returned \cup {j}
             /\ progress' = [progress EXCEPT ![j] = IF Broken = "drop" THEN FALSE ELSE @]
             /\ violations' = violations \cup (IF state[j] = "cancel" THEN {"quiescence"} ELSE {})
             /\ seen' = seen \cup {"returned"}
             /\ UNCHANGED <<level, fixRole, attempt, worker, debt, mode, arrived, admissions>>
Pause == /\ Controls /\ mode = "running" /\ mode' = "paused"
         /\ UNCHANGED <<state, level, fixRole, attempt, progress, owner, worker, debt, returned, arrived, violations, seen, admissions>>
Unpause == /\ Controls /\ mode = "paused" /\ mode' = "running"
           /\ UNCHANGED <<state, level, fixRole, attempt, progress, owner, worker, debt, returned, arrived, violations, seen, admissions>>
Stop == /\ Controls /\ mode # "stopped" /\ mode' = "stopped"
        /\ UNCHANGED <<state, level, fixRole, attempt, progress, owner, worker, debt, returned, arrived, violations, seen, admissions>>
Admissions == \E j \in Jobs, o \in Owners : Admit(j,o)
Reclaims == \E j \in Jobs : Reclaim(j)
Ends == \E j \in Jobs : NativeEnded(j)
Returns == \E j \in Jobs : Return(j)
Finishes == \E j \in Jobs : WorkFinish(j) \/ ReadFinish(j) \/ Rework(j) \/ Merge(j)
Next == IF Scenario = "ladder" THEN Admissions ELSE
           Pause \/ Unpause \/ Stop \/ (Scenario = "preempt" /\ Arrival)
           \/ Admissions \/ Reclaims \/ Ends \/ Returns
           \/ (~Emergency /\ Finishes)
Spec == Init /\ [][Next]_vars
FairSpec == Spec /\ WF_vars(Admissions) /\ WF_vars(Reclaims) /\ WF_vars(Ends)
                /\ WF_vars(Returns) /\ WF_vars(Finishes) /\ WF_vars(Arrival)
TypeOK == /\ state \in [Jobs -> {"hidden", "ready", "work", "review", "read", "held", "merge", "done", "cancel", "quiescent"}]
          /\ level \in [Jobs -> 0..6] /\ fixRole \in [Jobs -> BOOLEAN] /\ attempt \in [Jobs -> 1..2]
          /\ progress \in [Jobs -> BOOLEAN] /\ owner \in [Jobs -> Owners \cup {"none"}]
          /\ worker \in [Jobs -> Owners \cup {"other"}]
          /\ returned \subseteq Jobs /\ mode \in {"running", "paused", "stopped"}
          /\ admissions \in Seq(Jobs) /\ Len(admissions) <= 3 * Cardinality(Jobs)
CapacitySafe == \A o \in Owners : Load(o) <= Capacity(o)
OrderedAdmission == "order" \notin violations
NoControlLaunch == "control" \notin violations
QuiescenceBeforeReuse == "quiescence" \notin violations
DisplacedPreserved == \A j \in returned :
   /\ attempt[j] = debt[j].attempt
   /\ progress[j] = debt[j].progress \/ state[j] \in {"review", "read", "merge", "done"}
   /\ debt[j].who \in Owners
DependenciesSafe == \A j \in Jobs : state[j] \in {"work", "review", "read", "merge", "done"} =>
                                             Needs(j) \subseteq {k \in Jobs : state[k] = "done"}
FixPersists == "fixOpened" \in seen => fixRole["repair"] /\ level["repair"] = InitialLevel("repair")
ReadRoleFixed == \A j \in Jobs : Read(j) => Rank(j) = 4
Drained == <> (\A j \in Jobs : state[j] = "done")
BlockerAdmitted == <> ("blocker" \in {admissions[i] : i \in 1..Len(admissions)})
ReachReclaimWitness == "returned" \notin seen
ReachCycleWitness == "cycleDone" \notin seen
ReachLadderWitness == Len(admissions) < 7
=============================================================================
