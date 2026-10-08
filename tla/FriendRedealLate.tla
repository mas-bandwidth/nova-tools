------------------------ MODULE FriendRedealLate ------------------------
EXTENDS Integers
\* docs/SPEC-SPRINT.md section 8, the rules table's row friend-take: a friend's
\* work card taken back and dealt to the next friend is measured from her own
\* deal, and a never-taken judgment raised before her deal, while the card sat
\* withdrawn, closes on it (internal/sprint/steps_tick.go WorkDeadline and
\* LateStands, judgment_rules.go ruleFriendTake). One work card, two friends,
\* running time in units. The take-back stamps untaken_since (since) and unsets
\* dealt; the deal to the next friend stamps dealt and keeps since. The tick is
\* one action: the deadlines part (raise a never-taken judgment when the card is
\* past the bound from the stamp it is measured by; close one whose cause no
\* longer stands), then the rule part (friend-take answers any open judgment on
\* a friend's row by taking the card back). A deal happens between ticks. The
\* model checks that the rule never takes a card back from a friend who has not
\* had her own whole bound, and that a tick leaves no judgment open on a
\* friend's row that was raised before her deal; not the start bound, the lanes
\* or the stamps' transport. 2026-10-08: one card, seven generations in thirty
\* minutes, started by no one (TestACardTakenBackFromOneFriendGetsTheNextFriendsOwnBound).
CONSTANTS Bound, MaxT, BadStaleStands
Friends == {"amy", "bob"}
None == "none"
VARIABLES place, clock, dealt, since, noteAt, bad
vars == <<place, clock, dealt, since, noteAt, bad>>

Max(a, b) == IF a > b THEN a ELSE b
\* WorkDeadline's field: on a friend's row the later of her deal and the take-back
\* stamp; withdrawn, the take-back stamp alone
From == IF place = None THEN since ELSE Max(dealt, since)
Late == clock - From > Bound
Open == noteAt # -1
\* LateStands: a never-taken judgment stands while the card is not taken, unless
\* the stamp it is measured by is at or after the note (the redeal to a friend)
Stands == BadStaleStands \/ place = None \/ From < noteAt

Init == /\ place = "amy" /\ clock = 0 /\ dealt = 0 /\ since = 0 /\ noteAt = -1 /\ bad = FALSE

\* the deadlines part on the state s, then the rule part on what it leaves
Deadlines(n) == IF ~Open /\ Late THEN clock ELSE IF Open /\ ~Stands THEN -1 ELSE n
Tick == /\ clock < MaxT
        /\ clock' = clock + 1
        /\ LET n == Deadlines(noteAt)
               takes == n # -1 /\ place # None
           IN /\ place' = IF takes THEN None ELSE place
              /\ dealt' = IF takes THEN -1 ELSE dealt
              /\ since' = IF takes THEN clock ELSE since
              /\ noteAt' = IF takes THEN -1 ELSE n
              \* a take-back from a friend who has not had her own whole bound: the first friend's clock
              /\ bad' = (bad \/ (takes /\ clock - dealt <= Bound))

\* the deal of the withdrawn card to a friend (friendRedealUnit, nextGen: dealt stamped, since kept)
Deal(f) == /\ place = None
           /\ place' = f /\ dealt' = clock
           /\ UNCHANGED <<clock, since, noteAt, bad>>

Next == Tick \/ \E f \in Friends : Deal(f)
Spec == Init /\ [][Next]_vars

TypeOK == /\ place \in Friends \cup {None} /\ clock \in 0..MaxT
          /\ dealt \in -1..MaxT /\ since \in 0..MaxT /\ noteAt \in -1..MaxT /\ bad \in BOOLEAN
\* the rule never takes a card back from a friend on another friend's clock
NeverOnAnothersClock == ~bad
\* a tick leaves no judgment open on a friend's row that was raised before her deal
TickLeavesNoStaleJudgment == [][Tick => (place' # None /\ noteAt' # -1 => noteAt' >= dealt')]_vars
=============================================================================
