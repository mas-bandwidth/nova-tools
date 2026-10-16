------------------------------- MODULE Friend -------------------------------
\* The daemon's card lanes (docs/SPEC-FRIEND.md; daemon.go, lanes.go).
\* Session messages belong to nova-hook and are not consumed by this machine.
\* A card runs alone. Provider refusals break the shared lane session after K
\* alike answers; silence stops only a quiet turn. Missing reports retry up to
\* CardTurns, while harness faults and provider limits keep the card uncounted.
\* The limit governor, reader slots, lane opening, mode changes, outbox writes
\* and coordinator notification delivery are below this model's grain.
\* Broken selects reversed witnesses: twoturns, deliverbroken, fixedkill,
\* asideearly and faultcounts. Each violates its named rule in CASES.tsv.

EXTENDS Naturals, FiniteSets
CONSTANTS MaxTime, Broken, Lanes, Cards, K, SilentStop, Refusals
VARIABLES now, lastStop, streak, reason, broken, lcard, lrun, lquiet, cst, tries
sessvars == <<streak, reason, broken>>
lanevars == <<lcard, lrun, lquiet, cst, tries>>
vars == <<now, lastStop, sessvars, lanevars>>
L == 1..Lanes
C == 1..Cards
CardTurns == 2
Reason(o) == IF o = "refused1" THEN 1 ELSE IF o = "refused2" THEN 2 ELSE 0
EndOutcomes == {"ok", "failed"} \cup {o \in {"refused1", "refused2"} : Reason(o) <= Refusals}
TypeOK ==
  /\ now \in 0..MaxTime
  /\ lastStop \in {"none", "silent", "printing"}
  /\ streak \in 0..K /\ reason \in 0..Refusals /\ broken \in BOOLEAN
  /\ lcard \in [L -> 0..Cards] /\ lrun \in [L -> 0..2]
  /\ lquiet \in [L -> 0..SilentStop]
  /\ cst \in [C -> {"queued", "lane", "done", "aside", "capped"}]
  /\ tries \in [C -> 0..CardTurns]
Init ==
  /\ now = 0 /\ lastStop = "none"
  /\ streak = 0 /\ reason = 0 /\ broken = FALSE
  /\ lcard = [l \in L |-> 0] /\ lrun = [l \in L |-> 0]
  /\ lquiet = [l \in L |-> 0]
  /\ cst = [c \in C |-> "queued"] /\ tries = [c \in C |-> 0]
Quieter(q, running) == IF running /\ q < SilentStop THEN q + 1 ELSE q
Tick ==
  /\ now < MaxTime /\ now' = now + 1
  /\ lquiet' = [l \in L |-> Quieter(lquiet[l], lrun[l] > 0)]
  /\ UNCHANGED <<lastStop, sessvars, lcard, lrun, cst, tries>>
Settle(o) ==
  LET r == Reason(o)
      s == IF r = 0 THEN 0 ELSE IF r = reason THEN streak + 1 ELSE 1
  IN /\ streak' = IF s > K THEN K ELSE s
     /\ reason' = r /\ broken' = (broken \/ s >= K)
LaneFree(l) == lrun[l] = 0 \/ (Broken = "twoturns" /\ lrun[l] < 2 /\ lcard[l] # 0)
LaneStart(l) ==
  /\ LaneFree(l) /\ (~broken \/ Broken = "deliverbroken")
  /\ \E c \in C :
       /\ IF lcard[l] # 0 THEN c = lcard[l] ELSE cst[c] = "queued"
       /\ lcard' = [lcard EXCEPT ![l] = c]
       /\ cst' = [cst EXCEPT ![c] = "lane"]
  /\ lrun' = [lrun EXCEPT ![l] = lrun[l] + 1]
  /\ lquiet' = [lquiet EXCEPT ![l] = 0]
  /\ UNCHANGED <<now, lastStop, sessvars, tries>>

CardOutcomes == {"done", "failed", "fault", "capped"}

CardAfter(l, e) ==
  LET c == lcard[l]
      t == tries[c] + 1
      step == IF t > CardTurns THEN CardTurns ELSE t
  IN CASE e = "done" ->
       /\ cst' = [cst EXCEPT ![c] = "done"] /\ lcard' = [lcard EXCEPT ![l] = 0]
       /\ UNCHANGED tries
     [] e = "fault" ->
       /\ UNCHANGED <<cst, lcard>>
       /\ IF Broken = "faultcounts"
            THEN tries' = [tries EXCEPT ![c] = step]
            ELSE UNCHANGED tries
     [] e = "capped" ->
       /\ cst' = [cst EXCEPT ![c] = "capped"] /\ lcard' = [lcard EXCEPT ![l] = 0]
       /\ tries' = [tries EXCEPT ![c] = step]
     [] OTHER ->
       /\ tries' = [tries EXCEPT ![c] = step]
       /\ IF t >= CardTurns \/ Broken = "asideearly"
            THEN cst' = [cst EXCEPT ![c] = "aside"] /\ lcard' = [lcard EXCEPT ![l] = 0]
            ELSE UNCHANGED <<cst, lcard>>


LaneEnds(l, o, e) ==
  /\ lrun[l] > 0
  /\ lrun' = [lrun EXCEPT ![l] = lrun[l] - 1]
  /\ Settle(o) /\ CardAfter(l, e)
  /\ UNCHANGED <<now, lastStop, lquiet>>
LaneStop(l, result) ==
  /\ lrun[l] > 0 /\ (lquiet[l] >= SilentStop \/ Broken = "fixedkill")
  /\ lrun' = [lrun EXCEPT ![l] = lrun[l] - 1]
  /\ lastStop' = IF lquiet[l] >= SilentStop THEN "silent" ELSE "printing"
  /\ Settle("failed") /\ CardAfter(l, IF result THEN "done" ELSE "failed")
  /\ UNCHANGED <<now, lquiet>>
LanePrint(l) ==
  /\ lrun[l] > 0 /\ lquiet[l] > 0
  /\ lquiet' = [lquiet EXCEPT ![l] = 0]
  /\ UNCHANGED <<now, lastStop, sessvars, lcard, lrun, cst, tries>>
LaneLimited(l) ==
  /\ lrun[l] > 0
  /\ lrun' = [lrun EXCEPT ![l] = lrun[l] - 1]
  /\ UNCHANGED <<now, lastStop, sessvars, lcard, lquiet, cst, tries>>
Next ==
  \/ Tick
  \/ \E l \in L :
       \/ LaneStart(l)
       \/ \E o \in EndOutcomes, e \in CardOutcomes : LaneEnds(l, o, e)
       \/ \E result \in BOOLEAN : LaneStop(l, result)
       \/ LanePrint(l)
       \/ LaneLimited(l)
Spec == Init /\ [][Next]_vars
OneTurnPerSession == \A l \in L : lrun[l] <= 1
TurnStarts == \E l \in L : lrun'[l] > lrun[l]
NoDeliveryWhenBroken == [][broken => ~TurnStarts]_vars
StopOnlyWhenSilent == lastStop # "printing"
SetAsideAfterCardTurns ==
  /\ \A c \in C : cst[c] = "aside" => tries[c] = CardTurns
  /\ \A c \in C : cst[c] = "lane" => tries[c] < CardTurns
  /\ \A l \in L : lcard[l] # 0 => cst[lcard[l]] = "lane"
  /\ \A l1, l2 \in L : l1 # l2 /\ lcard[l1] # 0 => lcard[l1] # lcard[l2]
=============================================================================
