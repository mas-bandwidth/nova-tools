---------------------------- MODULE StopCancels ----------------------------
(* The engine side of "stop cancels jobs" (docs/SPEC-SPRINT.md section 14; the
   owner, 2026-10-08: "official machine stop must cancel every active fleet/friend
   sprint job, preserve progress, return work AND reads to their same owner ready
   pool automatically, clear working counts, and prevent resume until start").
   One worker (a fleet member, pkg/member/member.go machineStop; a friend's
   daemon, pkg/friend/stop.go) with its lanes, reading the machine's word off the
   answer it already gets (the queue, the beat) once per pass and again right
   before each start. A lane takes a card and holds it before it launches; a held
   card when the word turns STOPPED is given up with its return owed, its run
   never started (releaseHeld); a read under way is cancelled with its return owed
   too (stopReads), and every owed stop-return is sent whatever the word (the
   hand-back runs every step, RUNNING or STOPPED). The store's side (stop-return,
   the same-owner return at a new generation, start refused while a stop-return is
   owed) is the store's model; here it is the Return and StartMachine actions.

   Witnesses, each one FALSE a finding:
     CancelOnStop     = FALSE: the lanes only stop starting (the native stop of
                        2026-10-08 before this change: it recorded the state and
                        let every lane run on to a finish)
     GateBeforeStart  = FALSE: a start reads the word only at the pass's head,
                        so a lane launches after the stop arrived (the store's core
                        tests, 17:25Z)
     ReleaseHeld      = FALSE: a held card (taken, not launched) launches through
                        the stop instead of being given up (heldthrough)
     PersistOwed      = FALSE: the stop-returns owed live only in memory, and a
                        daemon restarted mid-stop finishes the cancelled card as
                        a run gone (lane_end.go endStarted) instead of returning it
     NudgeGate        = FALSE: the idle wake and the oldest-card urging go into the
                        session while STOPPED (be686, 16:45Z)
     FinishCancelled  = TRUE: a lane told to stop finishes its card when its child
                        ends (the finish path not guarded on the cancel)
   Each witness breaks the invariant its case names; three break a second one as
   well, by the same mechanism: GateBeforeStart = FALSE also breaks
   NoLaneWhileStopped (the lane launched after the stop runs while STOPPED),
   ReleaseHeld = FALSE breaks it the same way (the held card launched through runs
   while STOPPED), and PersistOwed = FALSE also breaks EveryLaneReturnsOnStop (the
   return forgotten never comes). The model trusts the store for what the worker
   cannot see: Return is sent whatever the word and StartMachine needs nothing
   owed; the worker's own hand-back every pass, whatever the word, is
   pkg/member/member.go machineStop and pkg/friend/stop.go returnOwed, tested as
   traces. *)
EXTENDS Naturals, FiniteSets

CONSTANTS Lanes, Cards, None, CancelOnStop, GateBeforeStart, PersistOwed, NudgeGate, FinishCancelled, ReleaseHeld

VARIABLES machine,   \* "RUNNING" or "STOPPED"
          lane,      \* each lane: "idle", "held" (taken, not launched), "running" (a child spends), "cancelled" (told to stop, not yet ended)
          card,      \* each lane's card, or None
          queue,     \* cards the worker may take
          owed,      \* cards cancelled by the stop whose stop-return is not yet taken (recorded on disk when PersistOwed)
          returned,  \* cards the store took back by stop-return
          finished,  \* cards a lane finished
          cancelled, \* every card the stop ever cancelled
          reading,   \* cards a read is spending (a read's child runs)
          badStart,  \* a lane started while STOPPED
          badNudge,  \* a work nudge went into the session while STOPPED
          lostOwed,  \* a stop-return owed was dropped by a restart
          badFinish  \* a card the stop took was finished by the run it took it from

vars == <<machine, lane, card, queue, owed, returned, finished, cancelled, reading, badStart, badNudge, lostOwed, badFinish>>

TypeOK == /\ machine \in {"RUNNING", "STOPPED"}
          /\ lane \in [Lanes -> {"idle", "held", "running", "cancelled"}]
          /\ card \in [Lanes -> Cards \cup {None}]
          /\ queue \subseteq Cards /\ owed \subseteq Cards /\ returned \subseteq Cards
          /\ finished \subseteq Cards /\ cancelled \subseteq Cards
          /\ reading \subseteq Cards
          /\ badStart \in BOOLEAN /\ badNudge \in BOOLEAN /\ lostOwed \in BOOLEAN
          /\ badFinish \in BOOLEAN

Init == /\ machine = "RUNNING"
        /\ lane = [l \in Lanes |-> "idle"]
        /\ card = [l \in Lanes |-> None]
        /\ queue = Cards
        /\ owed = {} /\ returned = {} /\ finished = {} /\ cancelled = {}
        /\ reading = {}
        /\ badStart = FALSE /\ badNudge = FALSE /\ lostOwed = FALSE /\ badFinish = FALSE

InHand == {card[l] : l \in Lanes} \ {None}

(* A lane takes a card from the queue and holds it, not yet launched (the friend's
   claim, lanes.go, and the member's claim ahead of its launch). The word is read
   again right before the launch, in Start, not here; nothing is taken while STOPPED. *)
Take(l, c) == /\ lane[l] = "idle" /\ machine = "RUNNING" /\ c \in queue /\ c \notin InHand
              /\ lane' = [lane EXCEPT ![l] = "held"]
              /\ card' = [card EXCEPT ![l] = c]
              /\ queue' = queue \ {c}
              /\ UNCHANGED <<machine, owed, returned, finished, cancelled, reading, badStart, badNudge, lostOwed, badFinish>>

(* A held lane launches its card. The word is read right before the launch
   (GateBeforeStart); without the gate, or without the held card being given up
   (ReleaseHeld), a held card launches after the stop arrived, recorded on badStart. *)
Start(l) == /\ lane[l] = "held"
            /\ \/ machine = "RUNNING"
               \/ (machine = "STOPPED" /\ (~GateBeforeStart \/ ~ReleaseHeld))
            /\ lane' = [lane EXCEPT ![l] = "running"]
            /\ badStart' = (badStart \/ machine = "STOPPED")
            /\ UNCHANGED <<machine, card, queue, owed, returned, finished, cancelled, reading, badNudge, lostOwed, badFinish>>

(* A held lane gives its card up when the word is STOPPED (the friend's releaseHeld,
   lanes.go: the window between the claim and the start): the card is owed a
   stop-return with no run to end, so it is out of the lane at once. *)
GiveUpHeld(l) == /\ lane[l] = "held" /\ machine = "STOPPED" /\ GateBeforeStart /\ ReleaseHeld
                  /\ lane' = [lane EXCEPT ![l] = "idle"]
                  /\ card' = [card EXCEPT ![l] = None]
                  /\ owed' = owed \cup {card[l]}
                  /\ cancelled' = cancelled \cup {card[l]}
                  /\ UNCHANGED <<machine, queue, returned, finished, reading, badStart, badNudge, lostOwed, badFinish>>

(* A card is read (the friend's read lanes): taken from the queue, its read under way
   until its run ends. The word gates the begin. *)
Read(c) == /\ machine = "RUNNING" /\ c \in queue /\ c \notin InHand /\ c \notin reading
           /\ reading' = reading \cup {c}
           /\ queue' = queue \ {c}
           /\ UNCHANGED <<machine, lane, card, owed, returned, finished, cancelled, badStart, badNudge, lostOwed, badFinish>>

(* A read's run ends. A read the stop cancelled (its card owed) stays owed, returned
   once its run ends; a read not cancelled puts the card back to ready. *)
ReadEnd(c) == /\ c \in reading
              /\ reading' = reading \ {c}
              /\ queue' = IF c \in owed THEN queue ELSE queue \cup {c}
              /\ UNCHANGED <<machine, lane, card, owed, returned, finished, cancelled, badStart, badNudge, lostOwed, badFinish>>

(* The machine stops. The worker's next pass reads STOPPED and tells every running
   child to stop, its card owed a stop-return (CancelOnStop), and every read with
   it; the witness lets the children run on. *)
Stop == /\ machine = "RUNNING"
        /\ machine' = "STOPPED"
        /\ IF CancelOnStop
           THEN /\ lane' = [l \in Lanes |-> IF lane[l] = "running" THEN "cancelled" ELSE lane[l]]
                /\ owed' = owed \cup {card[l] : l \in {k \in Lanes : lane[k] = "running"}} \cup reading
                /\ cancelled' = cancelled \cup {card[l] : l \in {k \in Lanes : lane[k] = "running"}} \cup reading
           ELSE UNCHANGED <<lane, owed, cancelled>>
        /\ UNCHANGED <<card, queue, returned, finished, reading, badStart, badNudge, lostOwed, badFinish>>

(* A child ends. A cancelled lane's card stays owed (the return goes once the process
   is dead) and is finished by no one (the finding when FinishCancelled); a running
   lane's card is finished. *)
End(l) == /\ lane[l] \in {"running", "cancelled"}
          /\ lane' = [lane EXCEPT ![l] = "idle"]
          /\ card' = [card EXCEPT ![l] = None]
          /\ finished' = IF lane[l] = "running" \/ FinishCancelled THEN finished \cup {card[l]} ELSE finished
          /\ badFinish' = (badFinish \/ (lane[l] = "cancelled" /\ FinishCancelled))
          /\ UNCHANGED <<machine, queue, owed, returned, cancelled, reading, badStart, badNudge, lostOwed>>

(* The stop-return: sent only after the run is dead (the card is in no lane and no
   read), whatever the word (the hand-back runs every step, RUNNING or STOPPED); the
   store puts the same card back in the owner's queue at a new generation. *)
Return(c) == /\ c \in owed /\ c \notin InHand /\ c \notin reading
             /\ owed' = owed \ {c}
             /\ returned' = returned \cup {c}
             /\ queue' = queue \cup {c}
             /\ UNCHANGED <<machine, lane, card, finished, cancelled, reading, badStart, badNudge, lostOwed, badFinish>>

(* The daemon restarts mid-stop: every child and every read is gone. What it owed is
   read back from its lane state (PersistOwed) and sent first; the witness forgets it
   and finishes the gone run instead (a FAIL for a card the stop took). *)
Restart == /\ machine = "STOPPED"
           /\ (\E l \in Lanes : lane[l] = "cancelled") \/ (reading \cap owed) # {}
           /\ lane' = [l \in Lanes |-> "idle"]
           /\ card' = [l \in Lanes |-> None]
           /\ reading' = {}
           /\ IF PersistOwed
              THEN UNCHANGED <<owed, finished, lostOwed, badFinish>>
              ELSE /\ finished' = finished \cup owed
                   /\ owed' = {}
                   /\ lostOwed' = TRUE
                   /\ badFinish' = (badFinish \/ owed # {})
           /\ UNCHANGED <<machine, queue, returned, cancelled, badStart, badNudge>>

(* A work nudge (the idle wake, the oldest-card urging) into the session: gated on
   the word (NudgeGate); the witness nudges while STOPPED. *)
Nudge == /\ (machine = "RUNNING" \/ ~NudgeGate)
         /\ badNudge' = (badNudge \/ machine = "STOPPED")
         /\ UNCHANGED <<machine, lane, card, queue, owed, returned, finished, cancelled, reading, badStart, lostOwed, badFinish>>

(* start: the store refuses it while a stop-return is owed (the store's rule). *)
StartMachine == /\ machine = "STOPPED" /\ owed = {}
                /\ machine' = "RUNNING"
                /\ UNCHANGED <<lane, card, queue, owed, returned, finished, cancelled, reading, badStart, badNudge, lostOwed, badFinish>>

Done == /\ finished \cup returned = Cards /\ queue = {} /\ InHand = {} /\ reading = {} /\ UNCHANGED vars

Next == \/ \E l \in Lanes, c \in Cards : Take(l, c)
        \/ \E l \in Lanes : Start(l)
        \/ \E l \in Lanes : GiveUpHeld(l)
        \/ \E l \in Lanes : End(l)
        \/ \E c \in Cards : Return(c)
        \/ \E c \in Cards : Read(c)
        \/ \E c \in Cards : ReadEnd(c)
        \/ Stop \/ StartMachine \/ Restart \/ Nudge \/ Done

Fairness == /\ \A l \in Lanes : WF_vars(End(l))
            /\ \A l \in Lanes : WF_vars(Start(l) \/ GiveUpHeld(l))
            /\ \A c \in Cards : WF_vars(Return(c))
            /\ \A c \in Cards : WF_vars(ReadEnd(c))
            /\ WF_vars(StartMachine)

Spec == Init /\ [][Next]_vars /\ Fairness

----
(* While STOPPED no lane spends: every lane was told to stop (or is idle, held, or
   given up). *)
NoLaneWhileStopped == machine = "STOPPED" => \A l \in Lanes : lane[l] # "running"

(* No lane launches after the stop arrived: the word is read right before each start. *)
NoLaunchAfterStop == ~badStart

(* A card the stop took is never finished by the run it took it from: a cancelled
   lane's end adds nothing to finished, and a restart turns nothing owed into a
   finish. (The first reading, cancelled and finished disjoint, failed on the
   legitimate second run: Start, Stop, End, Return, StartMachine, Start, End; the
   second, owed and finished disjoint, no witness could break: both checked
   against the code on 2026-10-08, member endEnded skipping a stopped launch and
   the daemon's stopDone returning before any finish. The flag names the act.) *)
NoFinishOfACancelledCard == ~badFinish

(* Every card the stop took comes back to its owner's queue by stop-return. *)
EveryLaneReturnsOnStop == \A c \in Cards : (c \in cancelled) ~> (c \in returned)

(* A stop-return owed survives the daemon's restart. *)
StopReturnsSurviveRestart == ~lostOwed

(* No work nudge while STOPPED. *)
NoNudgeWhileStopped == ~badNudge

(* What is owed was cancelled by the stop: nothing else is ever stop-returned. *)
OwedIsCancelled == owed \subseteq cancelled
=============================================================================
