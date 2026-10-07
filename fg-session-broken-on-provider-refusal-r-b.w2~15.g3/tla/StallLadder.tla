---------------------------- MODULE StallLadder ----------------------------
\* The friend stall ladder (docs/SPEC-SPRINT.md, section friend-stall-ladder-r.w1).
\* A friend that stalls holds her dealt cards until the coordinator notices by hand.
\* The stall ladder makes recovery fully mechanical as a tick part in
\* internal/sprint/friend_stall.go, the fleet's update after presence (TickTables,
\* internal/sprint/steps_tick.go).
\*
\* A friend is stalled when she holds dealt cards and neither her activity nor a
\* progress stamp on any of her cards is newer than friend_stall_after (20m). Her
\* activity is her session's, a beat of hers naming running cards, and a finish on her
\* row; the one action FriendActivity stands for all three.
\* The ladder climbs one rung per friend_stall_step (5m) while she stays stalled:
\*   (1) a wake turn: a bus message to her that her daemon pushes in as a turn
\*   (2) a second wake
\*   (3) a coordinator note (a pushed judgment, "friend <f> stalled <d>: two wakes unanswered")
\*   (4) every card of hers she has not started taken back (FriendTake with All;
\*       a started card stays with her and finishes), ready for the next deal
\*   (5) she is marked down with reason "stalled", and released to up by the tick itself
\*       at her first activity after it (never card progress alone).
\* Every rung is a happened note pushed like the rest; any activity of hers or progress
\* resets her to rung 0.
\*
\* The wakes (pr-friend-stall-completeb.w1): the store's tick hands the part a
\* TickReq.WakeFriend that only collects the wakes of the plan being made, and sends
\* the last plan's through Store.WakeFriend (cmd/nova-sprint stallWaker, wakeFriendStall)
\* once the part's step commits (internal/sprint/store/tick.go, tickRun.parts). The tick
\* also plans a part it does not commit: to see whether it has work, and again after a
\* commit lost to another writer. PlanUncommitted is that plan; it sends nothing. woken
\* counts the wakes sent to her in this stall episode, and returns to 0 with her rung.
\*
\* Invariants:
\*   NoCardHeldPastBound: no card is held by a stalled friend for more than the
\*     bound (stall_after plus four steps).
\*   NoStartedRedealt: no card is redealt while it has started.
\*   ReleasedOnlyByActivity: a friend is released only by her own activity.
\*   WokenAtEveryWakeRung: a friend past rung n (n = 1, 2) was sent wake n.
\*   NoWakeWithoutRung: no wake is sent but at a rung the ladder climbed, once.
\*
\* Broken values:
\*   "none"          the design
\*   "notaken"       rung 4 fails to take back unstarted cards (breaks NoCardHeldPastBound)
\*   "takestarted"   rung 4 takes back started cards too, allowing redeal (breaks NoStartedRedealt)
\*   "releaseother"  friend is released to up without activity of hers (breaks ReleasedOnlyByActivity)
\*   "nowake"        the wake is planned and never sent: wakeFriendStall never called, as
\*                   friend-stall-ladder-r left it (breaks WokenAtEveryWakeRung)
\*   "wakeinplan"    the planner's TickReq.WakeFriend sends the wake as it plans (the
\*                   store's hook handed to it whole, as before this card): a plan not
\*                   committed wakes her again
\*                   (breaks NoWakeWithoutRung)

EXTENDS Naturals, FiniteSets

CONSTANTS Friends, Cards, StallAfter, StallStep, MaxTime, Broken

VARIABLES clock, rung, cardState, cardHolder, activity, cardProgress, status, wasStarted, releasedWithoutActivity, woken

vars == <<clock, rung, cardState, cardHolder, activity, cardProgress, status, wasStarted, releasedWithoutActivity, woken>>

\* The wake rungs: a wake goes at each of them.
WakeRungs == {1, 2}

Min(a, b) == IF a < b THEN a ELSE b

CardStates == {"dealt", "started", "taken", "redealt"}
FriendStatuses == {"up", "down"}

TypeOK ==
  /\ clock \in 0..MaxTime
  /\ rung \in [Friends -> 0..5]
  /\ cardState \in [Cards -> CardStates]
  /\ cardHolder \in [Cards -> Friends]
  /\ activity \in [Friends -> 0..MaxTime]
  /\ cardProgress \in [Cards -> 0..MaxTime]
  /\ status \in [Friends -> FriendStatuses]
  /\ wasStarted \in [Cards -> BOOLEAN]
  /\ releasedWithoutActivity \in BOOLEAN
  /\ woken \in [Friends -> 0..MaxTime]

Init ==
  /\ clock = 0
  /\ rung = [f \in Friends |-> 0]
  /\ cardState = [c \in Cards |-> "dealt"]
  /\ cardHolder = [c \in Cards |-> CHOOSE f \in Friends : TRUE]
  /\ activity = [f \in Friends |-> 0]
  /\ cardProgress = [c \in Cards |-> 0]
  /\ status = [f \in Friends |-> "up"]
  /\ wasStarted = [c \in Cards |-> FALSE]
  /\ releasedWithoutActivity = FALSE
  /\ woken = [f \in Friends |-> 0]

\* Latest evidence of activity or progress for friend f
LastActive(f) ==
  LET progCards == {c \in Cards : cardHolder[c] = f /\ cardState[c] \in {"dealt", "started"}}
      progTimes == {cardProgress[c] : c \in progCards}
      allTimes == progTimes \cup {activity[f]}
  IN CHOOSE t \in allTimes : \A other \in allTimes : t >= other

\* Friend holds cards in dealt or started state
HoldsCards(f) ==
  \E c \in Cards : cardHolder[c] = f /\ cardState[c] \in {"dealt", "started"}

\* The rung the ladder is at for friend f, idle for idleDuration.
RungAt(f, idleDuration) ==
  IF ~HoldsCards(f) \/ idleDuration < StallAfter THEN 0
  ELSE IF idleDuration < StallAfter + StallStep THEN 1
  ELSE IF idleDuration < StallAfter + 2 * StallStep THEN 2
  ELSE IF idleDuration < StallAfter + 3 * StallStep THEN 3
  ELSE IF idleDuration < StallAfter + 4 * StallStep THEN 4
  ELSE 5

\* The wakes a climb from rung a to rung b sends: one for each wake rung it passes.
Climbed(a, b) == Cardinality({n \in WakeRungs : a < n /\ n <= b})

\* Clock tick: advances clock and updates stall ladder; the part's step commits, and
\* the binding sends the wakes its plan carries.
Tick ==
  /\ clock < MaxTime
  /\ clock' = clock + 1
  /\ rung' = [f \in Friends |-> RungAt(f, clock + 1 - LastActive(f))]
  /\ woken' = [f \in Friends |->
       IF rung'[f] = 0 THEN 0
       ELSE IF Broken = "nowake" THEN woken[f]
       ELSE woken[f] + Climbed(rung[f], rung'[f])]
  /\ cardState' = [c \in Cards |->
       LET f == cardHolder[c]
           idleDuration == clock + 1 - LastActive(f)
       IN IF HoldsCards(f) /\ idleDuration >= StallAfter + 3 * StallStep /\ rung[f] < 4 THEN
            IF Broken = "notaken" THEN cardState[c]
            ELSE IF Broken = "takestarted" THEN
              IF cardState[c] \in {"dealt", "started"} THEN "taken" ELSE cardState[c]
            ELSE
              IF cardState[c] = "dealt" THEN "taken" ELSE cardState[c]
          ELSE cardState[c]]
  /\ status' = [f \in Friends |->
       LET idleDuration == clock + 1 - LastActive(f)
       IN IF HoldsCards(f) /\ idleDuration >= StallAfter + 4 * StallStep THEN "down"
          ELSE status[f]]
  /\ UNCHANGED <<cardHolder, activity, cardProgress, wasStarted, releasedWithoutActivity>>

\* The tick plans the stall part for friend f and does not commit the plan: a part
\* planned to see whether it has work, or planned again after a commit lost to another
\* writer. The design sends nothing from a plan (a stutter); "wakeinplan" sends the wake
\* of a rung the plan would climb to.
PlanUncommitted(f) ==
  /\ Broken = "wakeinplan"
  /\ woken[f] < MaxTime
  /\ LET next == RungAt(f, clock + 1 - LastActive(f))
     IN Climbed(rung[f], next) > 0
  /\ woken' = [woken EXCEPT ![f] = @ + 1]
  /\ UNCHANGED <<clock, rung, cardState, cardHolder, activity, cardProgress, status, wasStarted, releasedWithoutActivity>>

\* Friend has activity: her session's, a beat naming running cards, or a finish on her row
FriendActivity(f) ==
  /\ activity' = [activity EXCEPT ![f] = clock]
  /\ rung' = [rung EXCEPT ![f] = 0]
  /\ woken' = [woken EXCEPT ![f] = 0]
  /\ status' = [status EXCEPT ![f] = "up"]
  /\ UNCHANGED <<clock, cardState, cardHolder, cardProgress, wasStarted, releasedWithoutActivity>>

\* Friend stamps progress on card c
CardProgressStamp(c) ==
  /\ cardState[c] \in {"dealt", "started"}
  /\ cardProgress' = [cardProgress EXCEPT ![c] = clock]
  /\ rung' = [rung EXCEPT ![cardHolder[c]] = 0]
  /\ woken' = [woken EXCEPT ![cardHolder[c]] = 0]
  /\ UNCHANGED <<clock, cardState, cardHolder, activity, status, wasStarted, releasedWithoutActivity>>

\* Friend starts card c
FriendStartCard(c) ==
  /\ cardState[c] = "dealt"
  /\ cardState' = [cardState EXCEPT ![c] = "started"]
  /\ wasStarted' = [wasStarted EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<clock, rung, cardHolder, activity, cardProgress, status, releasedWithoutActivity, woken>>

\* Taken card is redealt
RedealCard(c) ==
  /\ cardState[c] = "taken"
  /\ cardState' = [cardState EXCEPT ![c] = "redealt"]
  /\ UNCHANGED <<clock, rung, cardHolder, activity, cardProgress, status, wasStarted, releasedWithoutActivity, woken>>

\* Spurious release without session activity (reversed witness)
SpuriousRelease(f) ==
  /\ Broken = "releaseother"
  /\ status[f] = "down"
  /\ status' = [status EXCEPT ![f] = "up"]
  /\ releasedWithoutActivity' = TRUE
  /\ UNCHANGED <<clock, rung, cardState, cardHolder, activity, cardProgress, wasStarted, woken>>

Next ==
  \/ Tick
  \/ \E f \in Friends : FriendActivity(f)
  \/ \E c \in Cards : CardProgressStamp(c)
  \/ \E c \in Cards : FriendStartCard(c)
  \/ \E c \in Cards : RedealCard(c)
  \/ \E f \in Friends : SpuriousRelease(f)
  \/ \E f \in Friends : PlanUncommitted(f)

Spec == Init /\ [][Next]_vars

\* Invariant 1: No card is held by a stalled friend for more than the bound (stall_after plus four steps)
NoCardHeldPastBound ==
  \A f \in Friends :
    \A c \in Cards :
      (cardHolder[c] = f /\ cardState[c] = "dealt") =>
        (clock - LastActive(f) <= StallAfter + 4 * StallStep)

\* Invariant 2: No card is redealt while it has started
NoStartedRedealt ==
  \A c \in Cards :
    wasStarted[c] => (cardState[c] # "taken" /\ cardState[c] # "redealt")

\* Invariant 3: A friend is released only by her own activity
ReleasedOnlyByActivity ==
  ~releasedWithoutActivity

\* Invariant 4: A friend past a wake rung was sent its wake
WokenAtEveryWakeRung ==
  \A f \in Friends : woken[f] >= Min(rung[f], 2)

\* Invariant 5: No wake is sent but at a rung the ladder climbed, and that once
NoWakeWithoutRung ==
  \A f \in Friends : woken[f] <= Min(rung[f], 2)

=============================================================================
