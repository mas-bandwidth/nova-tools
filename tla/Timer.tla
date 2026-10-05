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
\* and the next tick reads again; reading, rd and rdOpen, the tick's step in
\* flight: it read the store, rd is what it plans to raise and rdOpen the
\* open timers it read; moved, whether another fenced step (a set or a
\* cancel) committed since that read, which is the fence's generation moving.
\* The outside: a timer is set and cancelled at any time, including while the
\* machine is down or the tick's step is in flight, and the clock moves.
\*
\* The design, Broken = {}:
\*   Set      a timer is written to the store with a due time the clock has
\*            not passed, as a fenced step (store.AddTimer).
\*   Cancel   an open timer is taken off the store, as a fenced step
\*            (store.CancelTimer); a fired one cannot be (it is no longer
\*            open), so a cancelled timer never fires.
\*   TickRead the tick's step reads the store's open timers under its fence
\*            (store.Step.Timers) and plans to raise each one whose due time
\*            the clock has reached.
\*   TickCommit  the step commits only at the generation it read: when a set
\*            or a cancel moved the fence since, the try is lost and the
\*            step reads again; else it raises what it planned and its commit
\*            closes those ids on the record as the commit finds it
\*            (sprint.TimerChange), so a timer is raised once.
\*   Advance  the clock moves one unit; it never goes back.
\*   Restart  the in-memory state is lost (a step in flight with it) and the
\*            store is kept, so a timer set before a restart is raised after.
\* Reversed witnesses, each caught by one property below:
\*   "twice"  the tick raises a timer and does not close it: the next tick
\*            raises it again: AtMostOnce
\*   "early"  the tick raises a timer whose due time the clock has not
\*            reached: NeverEarly
\*   "lost"   a restart loses the store with the memory: a timer set before it
\*            is never raised: NoLapse
\*   "stale"  attempt 3's tick: set and cancel take no fence, the tick commits
\*            whatever moved, and writes back the record it read minus what it
\*            raised: a timer cancelled in the window fires:
\*            CancelledNeverFires (and one set in the window is lost)
EXTENDS Naturals

CONSTANTS MaxTime, MaxTimers, Broken

VARIABLES now, due, open, armed, gone, fired, mem, reading, rd, rdOpen, moved
vars == <<now, due, open, armed, gone, fired, mem, reading, rd, rdOpen, moved>>

Timers == 1..MaxTimers

TypeOK ==
    /\ now \in 0..MaxTime
    /\ due \in [Timers -> 0..MaxTime]
    /\ open \in SUBSET Timers
    /\ armed \in SUBSET Timers
    /\ gone \in SUBSET Timers
    /\ fired \in [Timers -> Nat]
    /\ mem \in SUBSET Timers
    /\ reading \in BOOLEAN
    /\ rd \in SUBSET Timers
    /\ rdOpen \in SUBSET Timers
    /\ moved \in BOOLEAN
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
    /\ reading = FALSE
    /\ rd = {}
    /\ rdOpen = {}
    /\ moved = FALSE

\* a set or a cancel is a fenced step: it moves the generation a tick's step
\* in flight read at (in "stale" it takes no fence)
Fence == moved' = (reading /\ "stale" \notin Broken)

\* a timer is written to the store, due at a clock time not yet reached
Set(t, d) ==
    /\ t \notin armed
    /\ d \in now..MaxTime
    /\ due' = [due EXCEPT ![t] = d]
    /\ open' = open \cup {t}
    /\ armed' = armed \cup {t}
    /\ Fence
    /\ UNCHANGED <<now, gone, fired, mem, reading, rd, rdOpen>>

\* an open timer is taken off the store; a timer the tick raised is no longer
\* open, so it is not there to cancel
Cancel(t) ==
    /\ t \in open
    /\ fired[t] = 0
    /\ open' = open \ {t}
    /\ gone' = gone \cup {t}
    /\ Fence
    /\ UNCHANGED <<now, due, armed, fired, mem, reading, rd, rdOpen>>

\* the timers this tick raises: the store's open ones whose due time the clock
\* has reached (sprint.DueNow, the one due test)
Raising == IF "early" \in Broken THEN open ELSE {t \in open : now >= due[t]}

\* the tick's step reads the record under its fence; a tick with none due
\* runs no step
TickRead ==
    /\ ~reading
    /\ Raising # {}
    /\ reading' = TRUE
    /\ rd' = Raising
    /\ rdOpen' = open
    /\ moved' = FALSE
    /\ UNCHANGED <<now, due, open, armed, gone, fired, mem>>

\* a try at a generation that moved is lost, and the step reads again
TickLose ==
    /\ reading
    /\ moved
    /\ reading' = FALSE
    /\ rd' = {}
    /\ rdOpen' = {}
    /\ moved' = FALSE
    /\ UNCHANGED <<now, due, open, armed, gone, fired, mem>>

\* the commit: the judgments, and the raised ids closed on the record as the
\* commit finds it ("stale": the record read, minus them, written back)
TickCommit ==
    /\ reading
    /\ ~moved
    /\ mem' = rdOpen
    /\ fired' = [t \in Timers |-> IF t \in rd THEN fired[t] + 1 ELSE fired[t]]
    /\ open' = CASE "twice" \in Broken -> open
                [] "stale" \in Broken -> rdOpen \ rd
                [] OTHER -> open \ rd
    /\ reading' = FALSE
    /\ rd' = {}
    /\ rdOpen' = {}
    /\ moved' = FALSE
    /\ UNCHANGED <<now, due, armed, gone>>

Advance ==
    /\ now < MaxTime
    /\ now' = now + 1
    /\ UNCHANGED <<due, open, armed, gone, fired, mem, reading, rd, rdOpen, moved>>

\* the in-memory state is lost, a step in flight with it; the store is kept
Restart ==
    /\ mem # {}
    /\ mem' = {}
    /\ open' = IF "lost" \in Broken THEN {} ELSE open
    /\ reading' = FALSE
    /\ rd' = {}
    /\ rdOpen' = {}
    /\ moved' = FALSE
    /\ UNCHANGED <<now, due, armed, gone, fired>>

Next ==
    \/ \E t \in Timers : \E d \in 0..MaxTime : Set(t, d)
    \/ \E t \in Timers : Cancel(t)
    \/ TickRead
    \/ TickLose
    \/ TickCommit
    \/ Advance
    \/ Restart

Spec == Init /\ [][Next]_vars /\ WF_vars(TickRead) /\ WF_vars(TickLose)
            /\ WF_vars(TickCommit) /\ WF_vars(Advance)

\* ---------------------------------------------------------------- the rules

\* a timer is raised at most once: the step that raises it closes it
AtMostOnce == \A t \in Timers : fired[t] <= 1

\* a timer is never raised before its due time
NeverEarly == \A t \in Timers : fired[t] = 0 \/ now >= due[t]

\* a cancelled timer never fires: a cancel takes it off the store and moves
\* the fence a tick's step in flight read at, and a fired timer is no longer
\* open to cancel
CancelledNeverFires == \A t \in gone : fired[t] = 0

\* an uncancelled timer whose due time the clock has passed is raised, under
\* fair ticks, whatever restart came between its set and its raise
NoLapse == \A t \in Timers :
    (t \in armed /\ t \notin gone /\ now >= due[t]) ~> (fired[t] >= 1 \/ t \in gone)

=============================================================================
