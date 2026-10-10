---------------------------- MODULE Deal ----------------------------
EXTENDS Naturals, FiniteSets
\* docs/SPEC-SPRINT.md, a dealt card handed back
\* (internal/sprint/handback.go HandBack; internal/sprint/friend_deal.go
\* friendsLeft). A card is in the pool or held by one friend. Deal gives a
\* pooled card to a friend it has not left, and counts one attempt. HandBack
\* returns a held card to the pool and records that friend in left. The
\* attempt does not change. BadAttempt is the reversed witness: a hand-back
\* that counts an attempt.
CONSTANTS Cards, Friends, MaxAttempt, BadAttempt
VARIABLES place, holder, attempt, left
vars == <<place, holder, attempt, left>>

Init == /\ place = [c \in Cards |-> "pool"]
        /\ holder = [c \in Cards |-> "none"]
        /\ attempt = [c \in Cards |-> 0]
        /\ left = [c \in Cards |-> {}]

\* the friends' deal of one pooled card to a friend it has not left
Deal(c, f) == /\ place[c] = "pool"
              /\ f \notin left[c]
              /\ attempt[c] < MaxAttempt
              /\ place' = [place EXCEPT ![c] = f]
              /\ holder' = [holder EXCEPT ![c] = f]
              /\ attempt' = [attempt EXCEPT ![c] = attempt[c] + 1]
              /\ UNCHANGED left

\* handback: back to the pool, that friend left, the attempt as it was
HandBack(c) == /\ holder[c] \in Friends
               /\ place' = [place EXCEPT ![c] = "pool"]
               /\ holder' = [holder EXCEPT ![c] = "none"]
               /\ left' = [left EXCEPT ![c] = left[c] \cup {holder[c]}]
               /\ attempt' = [attempt EXCEPT ![c] =
                                 IF BadAttempt THEN attempt[c] + 1 ELSE attempt[c]]

Next == \/ \E c \in Cards, f \in Friends : Deal(c, f)
        \/ \E c \in Cards : HandBack(c)

Spec == Init /\ [][Next]_vars

TypeOK == /\ place \in [Cards -> {"pool"} \cup Friends]
          /\ holder \in [Cards -> {"none"} \cup Friends]
          /\ attempt \in [Cards -> 0..(MaxAttempt + 1)]
          /\ left \in [Cards -> SUBSET Friends]
          /\ \A c \in Cards : (place[c] = "pool") <=> (holder[c] = "none")

\* a card is never held by a friend it has left
NotDealtBack == \A c \in Cards : holder[c] = "none" \/ holder[c] \notin left[c]

\* a step that returns a held card to the pool leaves its attempt
HandBackKeepsAttempt == [][
    \A c \in Cards : (place[c] \in Friends /\ place'[c] = "pool") => attempt'[c] = attempt[c]
]_vars
=============================================================================
