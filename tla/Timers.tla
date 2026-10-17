------------------------------ MODULE Timers --------------------------------
\* The sprint's timers (nova-sprint remind; internal/sprint/timer.go,
\* internal/sprint/store/timers.go; docs/SPEC-SPRINT.md, "Timers"). A timer is
\* a note for an actor at a due time; the tick fires it once, and it ends one
\* way: seen, expired or cancelled.
\*
\* The state, as the code keeps it: two records in the store, so a restart of
\* the server loses nothing. The verbs' record: cancelled and acked (remind
\* --cancel, remind --ack). The tick's record, the ends: firedAt, seenF,
\* expiredF. The inbox: open (the judgment raised for a fired timer, open
\* while it is unseen; an answer, ack, keeps it held as an acknowledgement)
\* and answered (the judgment acknowledged). The world: now (the clock), up
\* (the server runs), known (the actor is in the sprint). The ghosts: fires
\* and raises count the fires and the judgments written, gone marks a timer
\* dropped with no end (the "vanish" witness alone sets it).
\*
\* The tick is one pass over every timer, in two steps as the code does it:
\* TickEnds writes the ends (Advance, timer.go), then TickNotes makes the
\* judgments agree with them (TimerNotes). The server can go down between the
\* two, or anywhere: the pass is lost, the records stay, and the next pass
\* does it again. While the server runs the clock does not pass a reading the
\* tick has not finished (the tick runs every second; a timer is minutes);
\* while it is down the clock runs on.
\*
\* The design, Broken = {}. Every other value is a reversed witness:
\*   "double"  the tick fires a fired, unseen timer again while it is in its
\*             window: AtMostOnce fails.
\*   "early"   the tick fires a timer a step before its due time: NeverEarly
\*             fails.
\*   "vanish"  a fired, unseen timer whose window closes is dropped with no
\*             end and no note: EveryTimerEnds fails.

EXTENDS Naturals, FiniteSets

CONSTANTS Timers, Due, Within, MaxTime, MaxOutages, CanLeave, Broken

Faults == {"double", "early", "vanish"}
ASSUME Broken \subseteq Faults
ASSUME \A t \in Timers : Due[t] \in 2..MaxTime
None == 0 \* no time: the clock starts at 1, when the timers are set, and every due time is after it

VARIABLES now, up, outages, phase, known,
          cancelled, acked, firedAt, seenF, expiredF,
          open, answered, fires, raises, gone

vars == <<now, up, outages, phase, known, cancelled, acked, firedAt, seenF, expiredF, open, answered, fires, raises, gone>>

Closes(t) == Due[t] + Within

\* TimerState (timer.go): the one state, from the two records.
State(t) ==
    IF gone[t] THEN "gone"
    ELSE IF expiredF[t] THEN "expired"
    ELSE IF firedAt[t] # None /\ (seenF[t] \/ acked[t]) THEN "seen"
    ELSE IF firedAt[t] # None THEN "fired"
    ELSE IF cancelled[t] THEN "cancelled"
    ELSE "pending"

Ends == {"seen", "expired", "cancelled"}

\* Missed (timer.go): what remind --list --missed and view coordinator lead
\* with.
Missed(t) == State(t) = "fired" \/ (State(t) = "expired" /\ ~acked[t])

TypeOK ==
    /\ now \in 1..MaxTime /\ up \in BOOLEAN /\ outages \in 0..MaxOutages
    /\ phase \in {"ends", "notes", "done"} /\ known \in BOOLEAN
    /\ cancelled \in [Timers -> BOOLEAN] /\ acked \in [Timers -> BOOLEAN]
    /\ firedAt \in [Timers -> 0..MaxTime] /\ seenF \in [Timers -> BOOLEAN]
    /\ expiredF \in [Timers -> BOOLEAN] /\ open \in [Timers -> BOOLEAN]
    /\ answered \in [Timers -> BOOLEAN] /\ fires \in [Timers -> 0..MaxTime + 1]
    /\ raises \in [Timers -> 0..MaxTime + 1] /\ gone \in [Timers -> BOOLEAN]

Init ==
    /\ now = 1 /\ up = TRUE /\ outages = 0 /\ phase = "ends" /\ known = TRUE
    /\ cancelled = [t \in Timers |-> FALSE] /\ acked = [t \in Timers |-> FALSE]
    /\ firedAt = [t \in Timers |-> None] /\ seenF = [t \in Timers |-> FALSE]
    /\ expiredF = [t \in Timers |-> FALSE] /\ open = [t \in Timers |-> FALSE]
    /\ answered = [t \in Timers |-> FALSE] /\ fires = [t \in Timers |-> 0]
    /\ raises = [t \in Timers |-> 0] /\ gone = [t \in Timers |-> FALSE]

\* Reached (timer.go): the one due-check.
Reached(at) == now >= at
DueNow(t) == IF "early" \in Broken THEN Reached(Due[t] - 1) ELSE Reached(Due[t])

\* Advance (timer.go) for one timer at now: what it fires, sees, expires.
Fire(t) ==
    \/ /\ State(t) = "pending" /\ DueNow(t) /\ now <= Closes(t) /\ known
    \/ /\ "double" \in Broken /\ State(t) = "fired" /\ now <= Closes(t)
Expire(t) ==
    \/ /\ State(t) = "pending" /\ DueNow(t) /\ (now > Closes(t) \/ ~known)
    \/ /\ State(t) = "fired" /\ ~answered[t] /\ now > Closes(t) /\ "vanish" \notin Broken
Vanish(t) == "vanish" \in Broken /\ State(t) = "fired" /\ ~answered[t] /\ now > Closes(t)
See(t) == State(t) = "fired" /\ answered[t]

TickEnds ==
    /\ up /\ phase = "ends"
    /\ firedAt' = [t \in Timers |-> IF Fire(t) THEN now ELSE firedAt[t]]
    /\ fires' = [t \in Timers |-> IF Fire(t) THEN fires[t] + 1 ELSE fires[t]]
    /\ expiredF' = [t \in Timers |-> expiredF[t] \/ Expire(t)]
    /\ seenF' = [t \in Timers |-> seenF[t] \/ See(t)]
    /\ gone' = [t \in Timers |-> gone[t] \/ Vanish(t)]
    /\ phase' = "notes"
    /\ UNCHANGED <<now, up, outages, known, cancelled, acked, open, answered, raises>>

\* TimerNotes: a judgment open exactly for the fired, unseen timers, written
\* once (a condition already open, or acknowledged, is not written again).
TickNotes ==
    /\ up /\ phase = "notes"
    /\ open' = [t \in Timers |-> State(t) = "fired"]
    /\ raises' = [t \in Timers |-> IF State(t) = "fired" /\ ~open[t] THEN raises[t] + 1 ELSE raises[t]]
    /\ answered' = [t \in Timers |-> answered[t] /\ State(t) = "fired"]
    /\ phase' = "done"
    /\ UNCHANGED <<now, up, outages, known, cancelled, acked, firedAt, seenF, expiredF, fires, gone>>

\* The clock: past a reading the running server has finished, or freely
\* while it is down.
Clock ==
    /\ now < MaxTime /\ (phase = "done" \/ ~up)
    /\ now' = now + 1 /\ phase' = "ends"
    /\ UNCHANGED <<up, outages, known, cancelled, acked, firedAt, seenF, expiredF, open, answered, fires, raises, gone>>

\* The server goes down anywhere, the pass in flight with it, and comes back.
Down ==
    /\ up /\ outages < MaxOutages
    /\ up' = FALSE /\ outages' = outages + 1 /\ phase' = "ends"
    /\ UNCHANGED <<now, known, cancelled, acked, firedAt, seenF, expiredF, open, answered, fires, raises, gone>>
Up ==
    /\ ~up /\ up' = TRUE
    /\ UNCHANGED <<now, outages, phase, known, cancelled, acked, firedAt, seenF, expiredF, open, answered, fires, raises, gone>>

\* The verbs and the coordinator.
Cancel(t) ==
    /\ State(t) = "pending" /\ cancelled' = [cancelled EXCEPT ![t] = TRUE]
    /\ UNCHANGED <<now, up, outages, phase, known, acked, firedAt, seenF, expiredF, open, answered, fires, raises, gone>>
Ack(t) ==
    /\ State(t) \in {"fired", "expired"} /\ ~acked[t] /\ acked' = [acked EXCEPT ![t] = TRUE]
    /\ UNCHANGED <<now, up, outages, phase, known, cancelled, firedAt, seenF, expiredF, open, answered, fires, raises, gone>>
Answer(t) ==
    /\ open[t] /\ ~answered[t] /\ answered' = [answered EXCEPT ![t] = TRUE]
    /\ UNCHANGED <<now, up, outages, phase, known, cancelled, acked, firedAt, seenF, expiredF, open, fires, raises, gone>>
Leave ==
    /\ CanLeave /\ known /\ known' = FALSE
    /\ UNCHANGED <<now, up, outages, phase, cancelled, acked, firedAt, seenF, expiredF, open, answered, fires, raises, gone>>

Next ==
    \/ TickEnds \/ TickNotes \/ Clock \/ Down \/ Up \/ Leave
    \/ \E t \in Timers : Cancel(t) \/ Ack(t) \/ Answer(t)

Spec == Init /\ [][Next]_vars

\* The tick runs whenever the server is up, the server comes back, and the
\* clock runs on; nobody has to see or cancel a timer.
FairSpec == Spec /\ WF_vars(TickEnds) /\ WF_vars(TickNotes) /\ WF_vars(Up) /\ WF_vars(Clock)

\* ---- safety ----

\* A timer never fires before its due time.
NeverEarly == \A t \in Timers : firedAt[t] # None => firedAt[t] >= Due[t]

\* A timer fires at most once, and its judgment is written at most once.
AtMostOnce == \A t \in Timers : fires[t] <= 1 /\ raises[t] <= 1

\* One end at most: a timer seen is never expired, and a cancelled one never
\* fires.
OneEnd == \A t \in Timers :
    /\ ~(seenF[t] /\ expiredF[t])
    /\ (cancelled[t] => firedAt[t] = None)

\* A judgment is open only for a timer that fired.
JudgmentOnlyWhenFired == \A t \in Timers : open[t] => firedAt[t] # None

\* A fired timer nobody has seen is listed as missed, and so is an expired
\* one not acknowledged.
MissedListed == \A t \in Timers :
    /\ (firedAt[t] # None /\ ~seenF[t] /\ ~acked[t]) => Missed(t)
    /\ (expiredF[t] /\ ~acked[t]) => Missed(t)

\* An end is final.
EndIsFinal == [][\A t \in Timers : State(t) \in Ends => State(t)' = State(t)]_vars

\* ---- liveness (FairSpec) ----

\* Every timer reaches one end: seen, expired or cancelled; never none.
EveryTimerEnds == \A t \in Timers : <>(State(t) \in Ends)

\* With the server never down and every actor in the sprint, every timer not
\* cancelled fires.
DueTimerFires == \A t \in Timers : <>(cancelled[t] \/ firedAt[t] # None)

\* ---- reachability: each is false in a state the design must reach ----

NoLateFire == \A t \in Timers : firedAt[t] # None => firedAt[t] = Due[t]
NoExpiryUnfired == \A t \in Timers : ~(expiredF[t] /\ firedAt[t] = None)
NoExpiryUnseen == \A t \in Timers : ~(expiredF[t] /\ firedAt[t] # None)
NoSeenByAnswer == \A t \in Timers : ~seenF[t]
=============================================================================
