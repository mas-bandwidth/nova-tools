---------------------------- MODULE WakeDrill ----------------------------
\* The daily wake drill (hook/wake.go Wake, cmd/nova-hook/serve.go wake;
\* docs/SPEC-HOOK.md, the daily wake drill). Once a day the hook server drills
\* each hooked friend. A friend is drilled or not. A pass (an answer inside the
\* window, or an epoch younger than a day) emits no judgment. A miss emits one
\* judgment. A second miss the same day emits none. The drill does not unhook,
\* and neither does an answer. The minute heartbeat is outside this machine: it
\* may unhook a friend who is not answering, and it never runs as a step of Drill.
\*
\* The code derives young and answered from the store clock it is passed (an
\* epoch younger than a day; an answer within a day and not older than armed).
\* The model takes those two as the world, and the day as a counter. The send of
\* a miss and the mark that the day was drilled are one step, the same write.
\*
\* Broken (reversed witnesses, each must fail the property named):
\*   "again"      NoSecondSameDay: a second miss the same day emits another judgment
\*   "passjudge"  PassEmitsNone: a pass emits a judgment
\*   "nojudge"    MissEmitsOne: a miss emits nothing
\*   "unhook"     AnsweringStaysHooked: the drill unhooks a friend who has answered
EXTENDS Naturals, FiniteSets

CONSTANTS Friends, MaxDay, Broken

VARIABLES
  hooked,    \* [Friends -> BOOLEAN]
  young,     \* [Friends -> BOOLEAN]: the epoch is younger than a day, so not due
  answered,  \* [Friends -> BOOLEAN]: answered inside the window
  drilled,   \* [Friends -> BOOLEAN]: today's drill has been taken
  judged,    \* [Friends -> 0..2]: judgments emitted today
  day        \* 0..MaxDay

vars == <<hooked, young, answered, drilled, judged, day>>

TypeOK ==
  /\ hooked \in [Friends -> BOOLEAN]
  /\ young \in [Friends -> BOOLEAN]
  /\ answered \in [Friends -> BOOLEAN]
  /\ drilled \in [Friends -> BOOLEAN]
  /\ judged \in [Friends -> 0..2]
  /\ day \in 0..MaxDay

\* A friend is drilled or not.
DrilledOrNot == \A f \in Friends : drilled[f] \in BOOLEAN

\* At most one judgment per friend per day.
AtMostOne == \A f \in Friends : judged[f] <= 1

Init ==
  /\ hooked = [f \in Friends |-> TRUE]
  /\ young = [f \in Friends |-> TRUE]
  /\ answered = [f \in Friends |-> FALSE]
  /\ drilled = [f \in Friends |-> FALSE]
  /\ judged = [f \in Friends |-> 0]
  /\ day = 0

Bump(n) == IF n >= 2 THEN 2 ELSE n + 1

\* Whether this drill emits a judgment for f. A pass and a young epoch emit
\* none. A miss emits one. Already drilled emits none, unless Broken = "again".
Emit(f) ==
  IF Broken = "nojudge" THEN FALSE
  ELSE IF ~hooked[f] \/ (drilled[f] /\ Broken # "again") THEN FALSE
  ELSE IF answered[f] THEN Broken = "passjudge"
  ELSE IF young[f] THEN FALSE
  ELSE TRUE

\* One drill of every hooked friend not yet drilled today (hook/wake.go).
Drill ==
  /\ \E f \in Friends : hooked[f] /\ (~drilled[f] \/ Broken = "again")
  /\ drilled' = [f \in Friends |-> IF hooked[f] THEN TRUE ELSE drilled[f]]
  /\ judged' = [f \in Friends |-> IF Emit(f) THEN Bump(judged[f]) ELSE judged[f]]
  /\ hooked' = [f \in Friends |->
       IF Broken = "unhook" /\ hooked[f] /\ ~drilled[f] THEN FALSE ELSE hooked[f]]
  /\ UNCHANGED <<young, answered, day>>

Answer(f) ==
  /\ hooked[f] /\ ~answered[f]
  /\ answered' = [answered EXCEPT ![f] = TRUE]
  /\ UNCHANGED <<hooked, young, drilled, judged, day>>

Expire(f) ==
  /\ answered[f]
  /\ answered' = [answered EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<hooked, young, drilled, judged, day>>

Age(f) ==
  /\ young[f]
  /\ young' = [young EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<hooked, answered, drilled, judged, day>>

\* The minute heartbeat may unhook a friend who is not answering. It never
\* unhooks one who is. Drill does not call it (unless Broken = "unhook").
Unhook(f) ==
  /\ hooked[f] /\ ~answered[f]
  /\ hooked' = [hooked EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<young, answered, drilled, judged, day>>

Hook(f) ==
  /\ ~hooked[f]
  /\ hooked' = [hooked EXCEPT ![f] = TRUE]
  /\ young' = [young EXCEPT ![f] = TRUE]
  /\ answered' = [answered EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<drilled, judged, day>>

NextDay ==
  /\ day < MaxDay
  /\ day' = day + 1
  /\ drilled' = [f \in Friends |-> FALSE]
  /\ judged' = [f \in Friends |-> 0]
  /\ UNCHANGED <<hooked, young, answered>>

Next ==
  \/ Drill
  \/ NextDay
  \/ \E f \in Friends : Answer(f) \/ Expire(f) \/ Age(f) \/ Unhook(f) \/ Hook(f)

Spec == Init /\ [][Next]_vars

\* A pass emits no judgment.
PassEmitsNone ==
  [][\A f \in Friends :
       answered[f] /\ ~drilled[f] /\ drilled'[f] => judged'[f] = judged[f]]_vars

\* A miss emits one judgment.
MissEmitsOne ==
  [][\A f \in Friends :
       hooked[f] /\ ~young[f] /\ ~answered[f] /\ ~drilled[f] /\ drilled'[f]
         => judged'[f] = judged[f] + 1]_vars

\* A second miss the same day emits none: while the friend stays drilled, the count stays.
NoSecondSameDay ==
  [][\A f \in Friends : drilled[f] /\ drilled'[f] => judged'[f] = judged[f]]_vars

\* A friend who is answering stays hooked. The drill does not unhook them.
AnsweringStaysHooked ==
  [][\A f \in Friends : answered[f] => hooked'[f] = hooked[f]]_vars

=============================================================================
