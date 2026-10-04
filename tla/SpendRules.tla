---- MODULE SpendRules ----
EXTENDS Integers, FiniteSets, TLC
CONSTANTS Readers, Cards, MaxAttempts, Broken
VARIABLES asked, okreads, finder, attempt, failed, judged
vars == <<asked, okreads, finder, attempt, failed, judged>>
TypeOK ==
  /\ DOMAIN asked = Cards /\ DOMAIN okreads = Cards
  /\ DOMAIN finder = Cards /\ DOMAIN attempt = Cards
  /\ DOMAIN failed = Cards /\ DOMAIN judged = Cards
  /\ \A c \in Cards : asked[c] \subseteq Readers /\ okreads[c] \subseteq Readers
  /\ \A c \in Cards : finder[c] \in Readers \cup {""}
  /\ \A c \in Cards : attempt[c] \in 1..MaxAttempts
  /\ \A c \in Cards : failed[c] \in BOOLEAN /\ judged[c] \in BOOLEAN
Init ==
  /\ asked = [c \in Cards |-> {}]
  /\ okreads = [c \in Cards |-> {}]
  /\ finder = [c \in Cards |-> ""]
  /\ attempt = [c \in Cards |-> 1]
  /\ failed = [c \in Cards |-> FALSE]
  /\ judged = [c \in Cards |-> FALSE]
Needed(c) == IF Broken = "two" THEN 2 ELSE 1
Wanted(c) == Needed(c) - Cardinality(okreads[c])
Free(c) == {r \in Readers : r \notin asked[c] /\ r \notin okreads[c]}
Ask(c, r) ==
  /\ ~judged[c] /\ ~failed[c]
  /\ asked[c] = {} /\ Wanted(c) > 0
  /\ r \in Free(c)
  /\ asked' = [asked EXCEPT ![c] = @ \cup {r}]
  /\ UNCHANGED <<okreads, finder, attempt, failed, judged>>
AskFinder(c) ==
  /\ attempt[c] > 1 /\ finder[c] # "" /\ finder[c] \in Free(c)
  /\ asked[c] = {} /\ Wanted(c) > 0 /\ ~failed[c] /\ ~judged[c]
  /\ asked' = [asked EXCEPT ![c] = @ \cup {finder[c]}]
  /\ UNCHANGED <<okreads, finder, attempt, failed, judged>>
Ok(c, r) ==
  /\ r \in asked[c]
  /\ asked' = [asked EXCEPT ![c] = @ \ {r}]
  /\ okreads' = [okreads EXCEPT ![c] = @ \cup {r}]
  /\ UNCHANGED <<finder, attempt, failed, judged>>
BreakRead(c, r) ==
  /\ r \in asked[c]
  /\ asked' = [asked EXCEPT ![c] = @ \ {r}]
  /\ failed' = [failed EXCEPT ![c] = TRUE]
  /\ finder' = [finder EXCEPT ![c] = r]
  /\ UNCHANGED <<okreads, attempt, judged>>
Rework(c) ==
  /\ failed[c] /\ ~judged[c]
  /\ attempt[c] < MaxAttempts
  /\ attempt' = [attempt EXCEPT ![c] = @ + 1]
  /\ failed' = [failed EXCEPT ![c] = FALSE]
  /\ okreads' = [okreads EXCEPT ![c] = {}]
  /\ UNCHANGED <<asked, finder, judged>>
Accept(c) ==
  /\ Wanted(c) = 0 /\ asked[c] = {} /\ ~judged[c]
  /\ judged' = [judged EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<asked, okreads, finder, attempt, failed>>
Judge(c) ==
  /\ failed[c] /\ attempt[c] = MaxAttempts /\ ~judged[c]
  /\ judged' = [judged EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<asked, okreads, finder, attempt, failed>>
Next ==
  \/ \E c \in Cards : \E r \in Readers : Ask(c, r) \/ Ok(c, r) \/ BreakRead(c, r)
  \/ \E c \in Cards : AskFinder(c) \/ Rework(c) \/ Judge(c) \/ Accept(c)
NoReadLost ==
  \A c \in Cards : okreads[c] \subseteq okreads'[c]
NoCardStrandedInReview ==
  \A c \in Cards : ~judged[c] => (asked[c] # {} \/ Wanted(c) = 0 \/ failed[c] \/ (attempt[c] = 1 /\ okreads[c] = {}))
AttemptsBoundedWithJudgmentTerminal ==
  \A c \in Cards : attempt[c] <= MaxAttempts
Idle == UNCHANGED vars

Spec == Init /\ [][Next \/ Idle]_vars
====
