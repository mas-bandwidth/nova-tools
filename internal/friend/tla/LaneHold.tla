---------------------------- MODULE LaneHold ----------------------------
(* A provider failure stops every lane (internal/friend/lane_parity_loop.go,
   docs/SPEC-FRIEND.md opencode-lanes-parity-r2b.w1). Lanes run cards; a
   provider failure met on one running card holds the lanes, and with Stop
   every started card (running, or between turns) is ended and kept: queued again, not started (so a
   daemon restart does not finish it failed, lane_end.go endStarted), and no
   attempt counted. Nothing starts while held; only a person resumes (the hold
   file removed). Stop = FALSE is the reversed witness: the attempt that left
   the other running lanes going on a provider failure. *)
EXTENDS FiniteSets, Naturals

CONSTANTS Cards, Width, Stop

VARIABLES st,       \* each card: "queued", "running", "done", "failed" (finished failed at a restart)
          started,  \* the cards lanes.json marks started
          att,      \* each card's counted attempts
          held      \* the lanes are held by a provider failure

vars == <<st, started, att, held>>

Running == {c \in Cards : st[c] = "running"}

Init == /\ st = [c \in Cards |-> "queued"]
        /\ started = {}
        /\ att = [c \in Cards |-> 0]
        /\ held = FALSE

\* a free lane is handed a queued card: never while held, at most Width at once
Start(c) == /\ ~held /\ st[c] = "queued" /\ Cardinality(Running) < Width
            /\ st' = [st EXCEPT ![c] = "running"]
            /\ started' = started \cup {c}
            /\ UNCHANGED <<att, held>>

\* the card's turn writes its report
Finish(c) == /\ st[c] = "running"
             /\ st' = [st EXCEPT ![c] = "done"]
             /\ started' = started \ {c}
             /\ UNCHANGED <<att, held>>

\* an ordinary turn ends with no result: handed again, one attempt counted (bounded)
Again(c) == /\ st[c] = "running" /\ att[c] < 1
            /\ st' = [st EXCEPT ![c] = "queued"]
            /\ att' = [att EXCEPT ![c] = @ + 1]
            /\ UNCHANGED <<started, held>>

\* a provider failure on card c: the lanes held; with Stop every started card, its turn running
\* or between turns, ended and kept (TLC's first counterexample: a card handed again, between
\* turns while held, stayed started, and a restart finished it failed)
Provider(c) == /\ st[c] = "running"
               /\ held' = TRUE
               /\ LET stopped == IF Stop THEN started ELSE {c}
                  IN /\ st' = [d \in Cards |-> IF d \in stopped THEN "queued" ELSE st[d]]
                     /\ started' = started \ stopped
               /\ UNCHANGED att

\* a person removes the hold file
Resume == /\ held /\ held' = FALSE /\ UNCHANGED <<st, started, att>>

\* the daemon restarts: every card still marked started is finished failed (endStarted);
\* a running card's run is gone with it
Restart == /\ started # {}
           /\ st' = [c \in Cards |-> IF c \in started THEN "failed" ELSE st[c]]
           /\ started' = {}
           /\ UNCHANGED <<att, held>>

\* every card finished: nothing is owed
Done == /\ \A c \in Cards : st[c] \in {"done", "failed"}
        /\ UNCHANGED vars

Next == \/ \E c \in Cards : Start(c) \/ Finish(c) \/ Again(c) \/ Provider(c)
        \/ Resume \/ Restart \/ Done

Spec == Init /\ [][Next]_vars

TypeOK == /\ st \in [Cards -> {"queued", "running", "done", "failed"}]
          /\ started \subseteq Cards
          /\ att \in [Cards -> 0..1]
          /\ held \in BOOLEAN

\* held, no lane runs a card
HeldStopsAll == held => Running = {}

\* held, no card is marked started: a restart while held fails none of the kept cards
HeldFailsNone == held => started = {}

\* a provider failure counts no attempt
KeptUncounted == [][\A c \in Cards : Provider(c) => att' = att]_vars
=============================================================================
