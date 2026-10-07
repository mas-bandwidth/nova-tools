------------------------- MODULE CoordinatorWake -------------------------
\* nova-sprint watch --wake (cmd/nova-sprint/watchwake.go): the coordinator is
\* woken by what is new for it, by one line and an exit, and the session runs
\* the verb again. The verb keeps its cursors in a state file, so an event
\* that arrives between two runs is not lost and an event woken is not woken
\* again.
\*
\* The state: the events that have arrived, in order, numbered 1..arrived
\* (a bus entry, a judgment, a stop: each is told apart by its own cursor in
\* the code, and one cursor stands for all of them here); cursor, the state
\* file's cursor, the last event a run has consumed; fired[e], how many wake
\* lines named event e; run, whether a run is blocked waiting (the session
\* starts one after each line). The outside: events arrive at any time,
\* including while no run is waiting.
\*
\* The design, Broken = {}:
\*   Arrive  an event is appended; nothing else changes.
\*   Rerun   the session starts the verb; it reads the cursor from the state
\*           file and blocks.
\*   Wake    a blocked run finds the first event after the cursor, writes the
\*           cursor to the state file (atomically, renamed into place) and
\*           only then prints the line and exits. A crash after the write and
\*           before the line loses that one line; the next event still wakes.
\*           The code never prints first: a line printed and a cursor not
\*           written would fire the event again.
\* Reversed witnesses:
\*   "nocursor"  the run does not write its cursor back: the next run finds
\*               the same event first, and it is woken twice (AtMostOnce).
\*   "skip"      the run writes the cursor at the newest arrival, past events
\*               it has not woken: those never are (NoLapse).
EXTENDS Naturals

CONSTANTS MaxEvents, Broken

VARIABLES arrived, cursor, fired, run
vars == <<arrived, cursor, fired, run>>

Events == 1..MaxEvents

TypeOK ==
    /\ arrived \in 0..MaxEvents
    /\ cursor \in 0..MaxEvents
    /\ cursor <= arrived
    /\ fired \in [Events -> Nat]
    /\ run \in {"idle", "blocked"}

Init ==
    /\ arrived = 0
    /\ cursor = 0
    /\ fired = [e \in Events |-> 0]
    /\ run = "idle"

Arrive ==
    /\ arrived < MaxEvents
    /\ arrived' = arrived + 1
    /\ UNCHANGED <<cursor, fired, run>>

Rerun ==
    /\ run = "idle"
    /\ run' = "blocked"
    /\ UNCHANGED <<arrived, cursor, fired>>

\* the first event after the cursor is the one the run names
Wake ==
    /\ run = "blocked"
    /\ cursor < arrived
    /\ run' = "idle"
    /\ fired' = [fired EXCEPT ![cursor + 1] = @ + 1]
    /\ cursor' = IF "nocursor" \in Broken THEN cursor
                 ELSE IF "skip" \in Broken THEN arrived
                 ELSE cursor + 1
    /\ UNCHANGED arrived

Next == Arrive \/ Rerun \/ Wake

Spec == Init /\ [][Next]_vars /\ WF_vars(Rerun) /\ WF_vars(Wake)

\* an event is woken at most once
AtMostOnce == \A e \in Events : fired[e] <= 1

\* every event that arrives is woken by some later run (under fair reruns)
NoLapse == \A e \in Events : (arrived >= e) ~> (fired[e] >= 1)
=============================================================================
