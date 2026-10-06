-------------------------------- MODULE Timer -------------------------------
\* nova-sprint remind (docs/SPEC-SPRINT.md, "Timers"; internal/sprint/timers.go
\* and internal/sprint/store/timers.go): an actor sets a timer for itself or
\* for another, and the machine's tick raises it as one judgment addressed to
\* that actor at its due time, once, closing the timer in the same store step.
\*
\* The state: open, the store's open timers; armed, the timers ever written to
\* the store; gone, the timers a cancel took off; due[t], the clock time timer
\* t is due at (0 is never set); fired[t], how many judgments named timer t;
\* now, the clock; mem, the machine's read of the store, which a restart loses
\* and the next tick reads again. The outside: a timer is set and cancelled at
\* any time, including while the machine is down, and the clock moves.
\*
\* The design, Broken = {}:
\*   Set      a timer is written to the store with a due time the clock has
\*            not passed.
\*   Cancel   an open timer is taken off the store; a fired one cannot be
\*            (it is no longer open), so a cancelled timer never fires.
\*   Tick     the machine reads the store's open timers and raises each one
\*            whose due time the clock has reached as one judgment, closing it
\*            in the same step: the read is the raise's guard, so a timer is
\*            raised once. A tick that raises nothing changes nothing and is
\*            no step here.
\*   Advance  the clock moves one unit; it never goes back.
\*   Restart  the in-memory state is lost and the store is kept, so a timer
\*            set before a restart is raised after it.
\* Reversed witnesses, each caught by one property below:
\*   "twice"  the tick raises a timer and does not close it: the next tick
\*            raises it again: AtMostOnce
\*   "early"  the tick raises a timer whose due time the clock has not
\*            reached: NeverEarly
\*   "lost"   a restart loses the store with the memory: a timer set before it
\*            is never raised: NoLapse
EXTENDS Naturals

CONSTANTS MaxTime, MaxTimers, Broken

VARIABLES now, due, open, armed, gone, fired, mem
vars == <<now, due, open, armed, gone, fired, mem>>

Timers == 1..MaxTimers

TypeOK ==
    /\ now \in 0..MaxTime
    /\ due \in [Timers -> 0..MaxTime]
    /\ open \in SUBSET Timers
    /\ armed \in SUBSET Timers
    /\ gone \in SUBSET Timers
    /\ fired \in [Timers -> Nat]
    /\ mem \in SUBSET Timers
    /\ open \subseteq armed
    /\ mem \subseteq armed

Init ==
    /\ now = 0
    /\ due = [t \in Timers |-> 0]
    /\ open = {}
    /\ armed = {}
    /\ gone = {}
    /\ fired = [t \in Timers |-> 0]
    /\ mem = {}

\* a timer is written to the store, due at a clock time not yet reached
Set(t, d) ==
    /\ t \notin armed
    /\ d \in now..MaxTime
    /\ due' = [due EXCEPT ![t] = d]
    /\ open' = open \cup {t}
    /\ armed' = armed \cup {t}
    /\ UNCHANGED <<now, gone, fired, mem>>

\* an open timer is taken off the store; a timer the tick raised is no longer
\* open, so it is not there to cancel
Cancel(t) ==
    /\ t \in open
    /\ fired[t] = 0
    /\ open' = open \ {t}
    /\ gone' = gone \cup {t}
    /\ UNCHANGED <<now, due, armed, fired, mem>>

\* the timers this tick raises: the store's open ones whose due time the clock
\* has reached (sprint.DueNow, the tree's one clock comparison)
Raising == IF "early" \in Broken THEN open ELSE {t \in open : now >= due[t]}

Tick ==
    /\ Raising # {}
    /\ mem' = open
    /\ fired' = [t \in Timers |-> IF t \in Raising THEN fired[t] + 1 ELSE fired[t]]
    /\ open' = IF "twice" \in Broken THEN open ELSE open \ Raising
    /\ UNCHANGED <<now, due, armed, gone>>

Advance ==
    /\ now < MaxTime
    /\ now' = now + 1
    /\ UNCHANGED <<due, open, armed, gone, fired, mem>>

\* the in-memory state is lost; the store is kept
Restart ==
    /\ mem # {}
    /\ mem' = {}
    /\ open' = IF "lost" \in Broken THEN {} ELSE open
    /\ UNCHANGED <<now, due, armed, gone, fired>>

Next ==
    \/ \E t \in Timers : \E d \in 0..MaxTime : Set(t, d)
    \/ \E t \in Timers : Cancel(t)
    \/ Tick
    \/ Advance
    \/ Restart

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick) /\ WF_vars(Advance)

\* ---------------------------------------------------------------- the rules

\* a timer is raised at most once: the step that raises it closes it
AtMostOnce == \A t \in Timers : fired[t] <= 1

\* a timer is never raised before its due time
NeverEarly == \A t \in Timers : fired[t] = 0 \/ now >= due[t]

\* a cancelled timer never fires: a cancel takes it off the store, and a
\* fired timer is no longer open to cancel
CancelledNeverFires == \A t \in gone : fired[t] = 0

\* an uncancelled timer whose due time the clock has passed is raised, under
\* fair ticks, whatever restart came between its set and its raise
NoLapse == \A t \in Timers :
    (t \in armed /\ t \notin gone /\ now >= due[t]) ~> (fired[t] >= 1 \/ t \in gone)

=============================================================================
