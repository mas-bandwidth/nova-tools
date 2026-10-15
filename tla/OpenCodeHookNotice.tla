-------------------------- MODULE OpenCodeHookNotice --------------------------
\* The opencode shim's turn boundary (pkg/harness/notice/opencode.go). One
\* notice is one synthetic inbox item. Push admits it with delivery steer and
\* resume: a running turn takes it at its next step, an idle session is woken
\* and takes it. A turn never ends with an item still queued.
\*
\* The shim reads three things: the inbox (exact), session.active (which may
\* lag the drain in either direction: listed), and the session's idle marker on
\* the server clock. It calls the notice consumed when the item left the inbox,
\* the session is not listed, and the idle marker is at or after the item's
\* admission. Broken = "no-idle-check" is the reversed witness that drops the
\* marker: a lagging listing then reads a turn that still holds the item as
\* finished, and NoEarlyConsume fails.
\* The Core's own rules (one pending notice, re-push, only the model proves) are
\* tla/HarnessNoticeUnknown.tla's. Bench configurations are owed; this module has
\* no cfg until that run.
EXTENDS Naturals
CONSTANTS Broken, MaxClock
VARIABLES item, running, listed, idleAt, created, clock, observed

vars == <<item, running, listed, idleAt, created, clock, observed>>

Items == {"none", "queued", "inturn", "done"}

Init ==
    /\ item = "none"
    /\ running \in BOOLEAN
    /\ listed = running
    /\ idleAt = 0
    /\ created = 0
    /\ clock = 0
    /\ observed = FALSE

\* The shim admits the notice. An idle session is woken (resume).
Push ==
    /\ item \in {"none", "done"}
    /\ clock < MaxClock
    /\ clock' = clock + 1
    /\ created' = clock + 1
    /\ item' = "queued"
    /\ running' = TRUE
    /\ observed' = FALSE
    /\ UNCHANGED <<listed, idleAt>>

\* A running turn's step takes the steered item.
Take ==
    /\ running
    /\ item = "queued"
    /\ item' = "inturn"
    /\ UNCHANGED <<running, listed, idleAt, created, clock, observed>>

\* A turn ends and stamps the idle marker. It never ends over a queued item.
Finish ==
    /\ running
    /\ item # "queued"
    /\ clock < MaxClock
    /\ clock' = clock + 1
    /\ idleAt' = clock + 1
    /\ running' = FALSE
    /\ item' = IF item = "inturn" THEN "done" ELSE item
    /\ UNCHANGED <<listed, created, observed>>

\* The person starts a turn of their own.
Start ==
    /\ ~running
    /\ running' = TRUE
    /\ UNCHANGED <<item, listed, idleAt, created, clock, observed>>

\* session.active catches up with the drain.
Sync ==
    /\ listed # running
    /\ listed' = running
    /\ UNCHANGED <<item, running, idleAt, created, clock, observed>>

Consumed ==
    /\ item \in {"inturn", "done"}
    /\ ~listed
    /\ (Broken = "no-idle-check" \/ idleAt >= created)

\* The shim's Consumed, read in one sample.
Observe ==
    /\ Consumed
    /\ ~observed
    /\ observed' = TRUE
    /\ UNCHANGED <<item, running, listed, idleAt, created, clock>>

Next == Push \/ Take \/ Finish \/ Start \/ Sync \/ Observe

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ item \in Items
    /\ running \in BOOLEAN /\ listed \in BOOLEAN /\ observed \in BOOLEAN
    /\ idleAt \in 0..MaxClock /\ created \in 0..MaxClock /\ clock \in 0..MaxClock

\* A notice is consumed only once the turn that took it has ended.
NoEarlyConsume == observed => item = "done"

\* An idle marker at or after the admission is a finished turn that held it.
IdleMeansDone == (item # "none" /\ idleAt >= created /\ created > 0) => item = "done"

=============================================================================
