------------------------ MODULE FriendEmptyRun ------------------------
EXTENDS Integers, FiniteSets
\* docs/SPEC-SPRINT.md section 8, the rules table's row failed: an attempt a
\* friend's lane ran empty (no report, not one token: ClassEmptyRun,
\* internal/sprint/harness_fault.go) is reworked by the failed rule (ruleHarness)
\* and never dealt again to the friend whose lane ran it empty: the rule writes
\* her on the primary's friends_left (emptyRunLeft), which the friends' deal
\* reads (friend_deal.go cardLeft). A card that names its friend (WHO: friend
\* <name>) is hers alone after a rework (ReworkPinned) and is never left. One
\* card for any friend, two friends, attempts up to MaxAttempts. 2026-10-09/10:
\* Freddy's opencode lanes ran 46 attempts empty and the rework dealt each
\* straight back to them.
CONSTANTS MaxAttempts, Named, BadNoLeft
Friends == {"amy", "bob"}
None == "none"
VARIABLES place, state, attempt, left, emptyOn
vars == <<place, state, attempt, left, emptyOn>>

Init == /\ place = None /\ state = "ready" /\ attempt = 0
        /\ left = {} /\ emptyOn = {}

\* the friends' deal: a ready card to a friend it has not left; a named card to
\* its friend alone
Deal(f) == /\ state = "ready" /\ attempt < MaxAttempts
           /\ f \notin left
           /\ (Named => f = "amy")
           /\ place' = f /\ state' = "working" /\ attempt' = attempt + 1
           /\ UNCHANGED <<left, emptyOn>>

\* her lane ends the attempt: ok, or empty (no report, no tokens)
FinishOk == /\ state = "working"
            /\ state' = "done" /\ place' = None
            /\ UNCHANGED <<attempt, left, emptyOn>>
FinishEmpty == /\ state = "working"
               /\ state' = "review" /\ emptyOn' = emptyOn \cup {place}
               /\ UNCHANGED <<place, attempt, left>>

\* the failed rule's rework: an empty run leaves the friend unless the card
\* names her (BadNoLeft: the rule as it was, which left no one)
Rework == /\ state = "review"
          /\ state' = "ready" /\ place' = None
          /\ left' = IF Named \/ BadNoLeft THEN left ELSE left \cup {place}
          /\ UNCHANGED <<attempt, emptyOn>>

Next == \/ \E f \in Friends : Deal(f)
        \/ FinishOk \/ FinishEmpty \/ Rework
Spec == Init /\ [][Next]_vars

TypeOK == /\ place \in Friends \cup {None}
          /\ state \in {"ready", "working", "review", "done"}
          /\ attempt \in 0..MaxAttempts
          /\ left \subseteq Friends /\ emptyOn \subseteq Friends
\* an any-friend card is never working on a friend whose lane ran it empty
NeverBackToTheEmptyLane == (~Named /\ state = "working") => place \notin emptyOn
\* a named card is never left by its friend: it is not stranded ready
NamedNeverLeft == Named => left = {}
=============================================================================
