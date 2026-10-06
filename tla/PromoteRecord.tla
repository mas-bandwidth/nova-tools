---------------------------- MODULE PromoteRecord ----------------------------
\* promote's record of a merge the queue landed (cmd/nova-sprint/promote.go,
\* (*promoter).record; docs/SPEC-SPRINT.md section 11, promote). A pass that
\* sees the pull request merged writes the sprint's store (the promoted step,
\* sprint.PromotedOnce), then moves refs/promoted/last, then unsets the clone's
\* pending promotion (promote.branch, promote.pr). A pass may die after any of
\* them, and the store may refuse (no coordinator seat, no push proof); while
\* the promotion is pending, the next pass comes back to record.
EXTENDS Naturals

CONSTANTS
    MaxCrash,    \* passes that die part way
    MaxRefuse,   \* passes the store refuses
    NoOnceGuard, \* broken: the store step records a sha it already holds
    RefFirst     \* broken: the clone's record is written before the store

VARIABLES
    stored,  \* how many times the store recorded this merge
    ref,     \* refs/promoted/last names the merge
    pending, \* the clone still names the pull request (promote.branch)
    pc,      \* where the current pass is: "look", "stored", "reffed"
    crashes, refusals

vars == <<stored, ref, pending, pc, crashes, refusals>>

TypeOK ==
    /\ stored \in 0..(MaxCrash + 2)
    /\ ref \in BOOLEAN /\ pending \in BOOLEAN
    /\ pc \in {"look", "stored", "reffed"}
    /\ crashes \in 0..MaxCrash /\ refusals \in 0..MaxRefuse

Init ==
    /\ stored = 0 /\ ref = FALSE /\ pending = TRUE
    /\ pc = "look" /\ crashes = 0 /\ refusals = 0

StoreWrite ==
    IF NoOnceGuard \/ stored = 0 THEN stored + 1 ELSE stored

\* a pass finds the pull request merged and writes its first record
Look ==
    /\ pc = "look" /\ pending
    /\ IF RefFirst
         THEN /\ ref' = TRUE /\ UNCHANGED stored
         ELSE /\ stored' = StoreWrite /\ UNCHANGED ref
    /\ pc' = "stored"
    /\ UNCHANGED <<pending, crashes, refusals>>

\* the store refuses: the pass fails and nothing is moved
Refuse ==
    /\ pc = "look" /\ pending /\ refusals < MaxRefuse
    /\ refusals' = refusals + 1
    /\ UNCHANGED <<stored, ref, pending, pc, crashes>>

\* the second write: the ref (or, with RefFirst, the store)
Second ==
    /\ pc = "stored"
    /\ IF RefFirst
         THEN /\ stored' = StoreWrite /\ UNCHANGED ref
         ELSE /\ ref' = TRUE /\ UNCHANGED stored
    /\ pc' = "reffed"
    /\ UNCHANGED <<pending, crashes, refusals>>

Unset ==
    /\ pc = "reffed"
    /\ pending' = FALSE /\ pc' = "look"
    /\ UNCHANGED <<stored, ref, crashes, refusals>>

\* the pass dies part way; the loop's next pass starts over
Crash ==
    /\ pc \in {"stored", "reffed"} /\ crashes < MaxCrash
    /\ crashes' = crashes + 1 /\ pc' = "look"
    /\ UNCHANGED <<stored, ref, pending, refusals>>

Next == Look \/ Refuse \/ Second \/ Unset \/ Crash

Spec == Init /\ [][Next]_vars /\ WF_vars(Look) /\ WF_vars(Second) /\ WF_vars(Unset)

\* the merge is recorded in the store at most once
RecordedOnce == stored <= 1

\* the clone records nothing the store does not hold: a ref moved over a
\* store that never recorded would start the next landed list after a
\* promotion the sprint does not know
CloneFollowsStore == ref => stored >= 1

\* a promotion no longer pending is recorded in both
DoneIsRecorded == ~pending => (stored = 1 /\ ref)

\* every merge is recorded with no hand step
Terminates == <>(~pending /\ stored = 1 /\ ref)
=============================================================================
