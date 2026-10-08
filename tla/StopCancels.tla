---------------------------- MODULE StopCancels ----------------------------
(* The engine side of "stop cancels jobs" (docs/SPEC-SPRINT.md section 14; the
   owner, 2026-10-08: "official machine stop must cancel every active fleet/friend
   sprint job, preserve progress, return work AND reads to their same owner ready
   pool automatically, clear working counts, and prevent resume until start").
   One worker (a fleet member, internal/member machineStop; a friend's daemon,
   internal/friend/stop.go) with its lanes, reading the machine's word off the
   answer it already gets (the queue, the beat) once per pass and again right
   before each start. The store's side (stop-return, the same-owner return at a
   new generation, start refused while a stop-return is owed) is the store's
   model; here it is the Return and StartMachine actions.

   Witnesses, each one FALSE a finding of 2026-10-08:
     CancelOnStop     = FALSE: the lanes only stop starting (the native stop of
                        2026-10-08 before this change: it recorded the state and
                        let every lane run on to a finish)
     GateBeforeStart  = FALSE: a start reads the word only at the pass's head,
                        so a lane launches after the stop arrived (the store's core
                        tests, 17:25Z)
     PersistOwed      = FALSE: the stop-returns owed live only in memory, and a
                        daemon restarted mid-stop finishes the cancelled card as
                        a run gone (lane_end.go endStarted) instead of returning it
     NudgeGate        = FALSE: the idle wake and the oldest-card urging go into the
                        session while STOPPED (be686, 16:45Z) *)
EXTENDS Naturals, FiniteSets

CONSTANTS Lanes, Cards, None, CancelOnStop, GateBeforeStart, PersistOwed, NudgeGate

VARIABLES machine,   \* "RUNNING" or "STOPPED"
          lane,      \* each lane: "idle", "running" (a child spends), "cancelled" (told to stop, not yet ended)
          card,      \* each lane's card, or None
          queue,     \* cards the worker may take
          owed,      \* cards cancelled by the stop whose stop-return is not yet taken (recorded on disk when PersistOwed)
          returned,  \* cards the store took back by stop-return
          finished,  \* cards a lane finished
          cancelled, \* every card the stop ever cancelled
          badStart,  \* a lane started while STOPPED
          badNudge,  \* a work nudge went into the session while STOPPED
          lostOwed   \* a stop-return owed was dropped by a restart

vars == <<machine, lane, card, queue, owed, returned, finished, cancelled, badStart, badNudge, lostOwed>>

TypeOK == /\ machine \in {"RUNNING", "STOPPED"}
          /\ lane \in [Lanes -> {"idle", "running", "cancelled"}]
          /\ card \in [Lanes -> Cards \cup {None}]
          /\ queue \subseteq Cards /\ owed \subseteq Cards /\ returned \subseteq Cards
          /\ finished \subseteq Cards /\ cancelled \subseteq Cards
          /\ badStart \in BOOLEAN /\ badNudge \in BOOLEAN /\ lostOwed \in BOOLEAN

Init == /\ machine = "RUNNING"
        /\ lane = [l \in Lanes |-> "idle"]
        /\ card = [l \in Lanes |-> None]
        /\ queue = Cards
        /\ owed = {} /\ returned = {} /\ finished = {} /\ cancelled = {}
        /\ badStart = FALSE /\ badNudge = FALSE /\ lostOwed = FALSE

InHand == {card[l] : l \in Lanes} \ {None}

(* A lane takes a card and launches its child. The word is read right before the
   launch (GateBeforeStart); without the gate a start goes through while STOPPED,
   which is the finding, recorded on badStart. *)
Start(l, c) == /\ lane[l] = "idle" /\ c \in queue /\ c \notin InHand
               /\ (machine = "RUNNING" \/ ~GateBeforeStart)
               /\ lane' = [lane EXCEPT ![l] = "running"]
               /\ card' = [card EXCEPT ![l] = c]
               /\ queue' = queue \ {c}
               /\ badStart' = (badStart \/ machine = "STOPPED")
               /\ UNCHANGED <<machine, owed, returned, finished, cancelled, badNudge, lostOwed>>

(* The machine stops. The worker's next pass reads STOPPED and tells every running
   child to stop, its card owed a stop-return (CancelOnStop); the witness lets the
   children run on. *)
Stop == /\ machine = "RUNNING"
        /\ machine' = "STOPPED"
        /\ IF CancelOnStop
           THEN /\ lane' = [l \in Lanes |-> IF lane[l] = "running" THEN "cancelled" ELSE lane[l]]
                /\ owed' = owed \cup {card[l] : l \in {k \in Lanes : lane[k] = "running"}}
                /\ cancelled' = cancelled \cup {card[l] : l \in {k \in Lanes : lane[k] = "running"}}
           ELSE UNCHANGED <<lane, owed, cancelled>>
        /\ UNCHANGED <<card, queue, returned, finished, badStart, badNudge, lostOwed>>

(* A child ends. A cancelled lane's card stays owed (the return goes once the process
   is dead); a running lane's card is finished, which while STOPPED is the finding. *)
End(l) == /\ lane[l] \in {"running", "cancelled"}
          /\ lane' = [lane EXCEPT ![l] = "idle"]
          /\ card' = [card EXCEPT ![l] = None]
          /\ finished' = IF lane[l] = "running" THEN finished \cup {card[l]} ELSE finished
          /\ UNCHANGED <<machine, queue, owed, returned, cancelled, badStart, badNudge, lostOwed>>

(* The stop-return: sent only after the process is dead (the card is in no lane),
   only while STOPPED; the store puts the same card back in the owner's queue at a
   new generation. *)
Return(c) == /\ c \in owed /\ c \notin InHand /\ machine = "STOPPED"
             /\ owed' = owed \ {c}
             /\ returned' = returned \cup {c}
             /\ queue' = queue \cup {c}
             /\ UNCHANGED <<machine, lane, card, finished, cancelled, badStart, badNudge, lostOwed>>

(* The daemon restarts mid-stop: every child is gone. What it owed is read back from
   its lane state (PersistOwed) and sent first; the witness forgets it and finishes
   the gone run instead (a FAIL for a card the stop took). *)
Restart == /\ machine = "STOPPED"
           /\ \E l \in Lanes : lane[l] = "cancelled"
           /\ lane' = [l \in Lanes |-> "idle"]
           /\ card' = [l \in Lanes |-> None]
           /\ IF PersistOwed
              THEN UNCHANGED <<owed, finished, lostOwed>>
              ELSE /\ finished' = finished \cup owed
                   /\ owed' = {}
                   /\ lostOwed' = TRUE
           /\ UNCHANGED <<machine, queue, returned, cancelled, badStart, badNudge>>

(* A work nudge (the idle wake, the oldest-card urging) into the session: gated on
   the word (NudgeGate); the witness nudges while STOPPED. *)
Nudge == /\ (machine = "RUNNING" \/ ~NudgeGate)
         /\ badNudge' = (badNudge \/ machine = "STOPPED")
         /\ UNCHANGED <<machine, lane, card, queue, owed, returned, finished, cancelled, badStart, lostOwed>>

(* start: the store refuses it while a stop-return is owed (the store's rule). *)
StartMachine == /\ machine = "STOPPED" /\ owed = {}
                /\ machine' = "RUNNING"
                /\ UNCHANGED <<lane, card, queue, owed, returned, finished, cancelled, badStart, badNudge, lostOwed>>

Done == /\ finished \cup returned = Cards /\ queue = {} /\ InHand = {} /\ UNCHANGED vars

Next == \/ \E l \in Lanes, c \in Cards : Start(l, c)
        \/ \E l \in Lanes : End(l)
        \/ \E c \in Cards : Return(c)
        \/ Stop \/ StartMachine \/ Restart \/ Nudge \/ Done

Fairness == /\ \A l \in Lanes : WF_vars(End(l))
            /\ \A c \in Cards : WF_vars(Return(c))
            /\ WF_vars(StartMachine)

Spec == Init /\ [][Next]_vars /\ Fairness

----
(* While STOPPED no lane spends: every lane was told to stop (or is idle). *)
NoLaneWhileStopped == machine = "STOPPED" => \A l \in Lanes : lane[l] # "running"

(* No lane launches after the stop arrived: the word is read right before each start. *)
NoLaunchAfterStop == ~badStart

(* A card the stop took is never finished by the run it took it from: while its
   stop-return is owed it is finished by no one; once returned, the store deals it
   again at a new generation and that run may finish it. (TLC's first trace of a
   stronger reading, cancelled \cap finished = {}, was Start, Stop, End, Return,
   StartMachine, Start, End: the legitimate second run, checked against the code
   on 2026-10-08: member endEnded skips a stopped launch and the daemon's stopDone
   returns before any finish, so no cancelled run finishes.) *)
NoFinishOfACancelledCard == owed \cap finished = {}

(* Every card the stop took comes back to its owner's queue by stop-return. *)
EveryLaneReturnsOnStop == \A c \in Cards : (c \in cancelled) ~> (c \in returned)

(* A stop-return owed survives the daemon's restart. *)
StopReturnsSurviveRestart == ~lostOwed

(* No work nudge while STOPPED. *)
NoNudgeWhileStopped == ~badNudge

(* What is owed was cancelled by the stop: nothing else is ever stop-returned. *)
OwedIsCancelled == owed \subseteq cancelled
=============================================================================
