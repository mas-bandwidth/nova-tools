-------------------------------- MODULE Delivery --------------------------------
\* nova-friend's delivery machine (docs/SPEC-FRIEND.md, the loop: a broken
\* session; internal/friend/daemon.go loop.settle, batchDone, clearBroken,
\* loadDelivery). One friend's daemon handing messages into one session, one
\* turn at a time. Per message: pending, delivering, deferred, delivered,
\* given-up. Per session: ok, busy (a turn is in it), broken.
\*
\* The state the code owns, in two copies: the working copy the loop reads
\* (fails, streak, session, told) and the state file (dFails, dStreak, dBroken,
\* dTold, internal/friend/state.go Delivery) that every step writes through
\* (loop.saveDelivery). Restart is the daemon starting again: it keeps only the
\* state file, so a turn in flight is lost (its message is pending again) and
\* the working copy is the file.
\*
\* The outcomes of a turn (loop.settle, batchDone):
\*   Succeed  exit 0: delivered and acked, the refusal streak ends
\*   Fail     a failure that is neither a deferral nor a refusal: counts toward
\*            the message; the MaxDeliveries-th is given up and acked
\*   Defer    a rate limit or a session that cannot take a turn now: the message
\*            stays in hand, counted toward nothing, never acked
\*   Refuse   the provider refused the session: the message stays pending,
\*            counted toward nothing; BrokenAfter in a row mark the session broken
\* Clear is a reset, or the session's own pong: the mark is lifted. Tell is the
\* one message to the coordinator, once per mark.
\*
\* Broken = "none" is the design. Every other value is a reversed witness, each
\* caught by one invariant below:
\*   "twoturns"       a turn starts while another is in the session:
\*                    AtMostOneDelivering
\*   "deliverbroken"  a turn starts while the session is broken:
\*                    NothingDeliveringWhileBroken
\*   "deferralcounts" a deferral counts as a failed delivery: FailsCountPlainOnly

EXTENDS Naturals, FiniteSets

CONSTANTS Msgs, MaxDeliveries, BrokenAfter, MaxMarks, Broken

VARIABLES mstate, acked, fails, plain, streak, session, told, marks, tells,
          dFails, dStreak, dBroken, dTold, restarts
vars == <<mstate, acked, fails, plain, streak, session, told, marks, tells,
          dFails, dStreak, dBroken, dTold, restarts>>

States == {"pending", "delivering", "deferred", "delivered", "given_up"}
Delivering == {m \in Msgs : mstate[m] = "delivering"}

TypeOK ==
  /\ mstate \in [Msgs -> States]
  /\ acked \in [Msgs -> BOOLEAN]
  /\ fails \in [Msgs -> 0..MaxDeliveries]
  /\ plain \in [Msgs -> 0..MaxDeliveries]
  /\ streak \in 0..BrokenAfter
  /\ session \in {"ok", "busy", "broken"}
  /\ told \in BOOLEAN
  /\ marks \in 0..MaxMarks
  /\ tells \in 0..MaxMarks
  /\ dFails \in [Msgs -> 0..MaxDeliveries]
  /\ dStreak \in 0..BrokenAfter
  /\ dBroken \in BOOLEAN
  /\ dTold \in BOOLEAN

Init ==
  /\ mstate = [m \in Msgs |-> "pending"]
  /\ acked = [m \in Msgs |-> FALSE]
  /\ fails = [m \in Msgs |-> 0]
  /\ plain = [m \in Msgs |-> 0]
  /\ streak = 0
  /\ session = "ok"
  /\ told = FALSE
  /\ marks = 0
  /\ tells = 0
  /\ dFails = [m \in Msgs |-> 0]
  /\ dStreak = 0
  /\ dBroken = FALSE
  /\ dTold = FALSE
  /\ restarts = 0

\* every step but Restart writes the working copy through to the file
Write ==
  /\ dFails' = fails'
  /\ dStreak' = streak'
  /\ dBroken' = (session' = "broken")
  /\ dTold' = told'

CanStart ==
  \/ session = "ok"
  \/ (Broken = "twoturns" /\ session = "busy")
  \/ (Broken = "deliverbroken" /\ session = "broken")

Start(m) ==
  /\ mstate[m] \in {"pending", "deferred"}
  /\ CanStart
  /\ mstate' = [mstate EXCEPT ![m] = "delivering"]
  /\ session' = IF session = "broken" THEN "broken" ELSE "busy"
  /\ UNCHANGED <<acked, fails, plain, streak, told, marks, tells, restarts>>
  /\ Write

\* the session is ok again once the turn is out of it (broken stays broken)
Settled == IF session = "broken" THEN "broken" ELSE "ok"

Succeed(m) ==
  /\ mstate[m] = "delivering"
  /\ mstate' = [mstate EXCEPT ![m] = "delivered"]
  /\ acked' = [acked EXCEPT ![m] = TRUE]
  /\ streak' = 0
  /\ session' = Settled
  /\ UNCHANGED <<fails, plain, told, marks, tells, restarts>>
  /\ Write

Fail(m) ==
  /\ mstate[m] = "delivering"
  /\ fails[m] + 1 <= MaxDeliveries
  /\ fails' = [fails EXCEPT ![m] = @ + 1]
  /\ plain' = [plain EXCEPT ![m] = @ + 1]
  /\ IF fails[m] + 1 >= MaxDeliveries
        THEN /\ mstate' = [mstate EXCEPT ![m] = "given_up"]
             /\ acked' = [acked EXCEPT ![m] = TRUE]
        ELSE /\ mstate' = [mstate EXCEPT ![m] = "pending"]
             /\ UNCHANGED acked
  /\ session' = Settled
  /\ UNCHANGED <<streak, told, marks, tells, restarts>>
  /\ Write

Defer(m) ==
  /\ mstate[m] = "delivering"
  /\ mstate' = [mstate EXCEPT ![m] = "deferred"]
  /\ IF Broken = "deferralcounts" /\ fails[m] + 1 < MaxDeliveries
        THEN fails' = [fails EXCEPT ![m] = @ + 1]
        ELSE UNCHANGED fails
  /\ session' = Settled
  /\ UNCHANGED <<acked, plain, streak, told, marks, tells, restarts>>
  /\ Write

Refuse(m) ==
  /\ mstate[m] = "delivering"
  /\ mstate' = [mstate EXCEPT ![m] = "pending"]
  /\ IF streak + 1 >= BrokenAfter /\ session # "broken" /\ marks < MaxMarks
        THEN /\ streak' = BrokenAfter
             /\ session' = "broken"
             /\ marks' = marks + 1
             /\ told' = FALSE
        ELSE /\ streak' = IF streak + 1 > BrokenAfter THEN BrokenAfter ELSE streak + 1
             /\ session' = Settled
             /\ UNCHANGED <<marks, told>>
  /\ UNCHANGED <<acked, fails, plain, tells, restarts>>
  /\ Write

\* reset, or the session's own pong: the mark is lifted and the streak starts again
Clear ==
  /\ session = "broken"
  /\ session' = "ok"
  /\ streak' = 0
  /\ told' = FALSE
  /\ UNCHANGED <<mstate, acked, fails, plain, marks, tells, restarts>>
  /\ Write

\* the one message to the coordinator, once per mark
Tell ==
  /\ session = "broken"
  /\ ~told
  /\ told' = TRUE
  /\ tells' = tells + 1
  /\ UNCHANGED <<mstate, acked, fails, plain, streak, session, marks, restarts>>
  /\ Write

\* the daemon starts again with the state file alone: a turn in flight is gone,
\* a deferral in hand is pending again, the counts and the mark are the file's
Restart ==
  /\ restarts < 2
  /\ restarts' = restarts + 1
  /\ mstate' = [m \in Msgs |-> IF mstate[m] \in {"delivering", "deferred"} THEN "pending" ELSE mstate[m]]
  /\ fails' = dFails
  /\ streak' = dStreak
  /\ session' = IF dBroken THEN "broken" ELSE "ok"
  /\ told' = dTold
  /\ UNCHANGED <<acked, plain, marks, tells, dFails, dStreak, dBroken, dTold>>

Next ==
  \/ \E m \in Msgs : Start(m) \/ Succeed(m) \/ Fail(m) \/ Defer(m) \/ Refuse(m)
  \/ Clear \/ Tell \/ Restart

Spec == Init /\ [][Next]_vars

\* ---- the invariants -------------------------------------------------------

AtMostOneDelivering == Cardinality(Delivering) <= 1

NothingDeliveringWhileBroken == session = "broken" => Delivering = {}

SessionBusyIffDelivering == (session = "busy") <=> (Delivering # {})

\* a message is acked only delivered or given up, and those are acked
AckedOnlyDeliveredOrGivenUp ==
  \A m \in Msgs : acked[m] <=> mstate[m] \in {"delivered", "given_up"}

\* the count is the plain failures: deferrals and refusals are in none of it
FailsCountPlainOnly == \A m \in Msgs : fails[m] = plain[m]

GivenUpOnlyAfterMaxPlainFailures ==
  \A m \in Msgs : mstate[m] = "given_up" => plain[m] >= MaxDeliveries

\* the file is the working copy, and the told message is at most one per mark
FileIsTheWorkingCopy ==
  /\ dFails = fails
  /\ dStreak = streak
  /\ dBroken = (session = "broken")
  /\ dTold = told

TellsAtMostMarks == tells <= marks

\* a restart changes none of the record: counts, streak and the mark stay
RestartKeepsTheRecord ==
  [][Restart => /\ fails' = fails /\ streak' = streak
                /\ (session = "broken") = (session' = "broken")
                /\ told' = told]_vars
=================================================================================
