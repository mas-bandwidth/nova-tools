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

   The worker can die without its drain (Crash: a SIGKILL, a crash, the OOM
   killer) and come back (Restart: a member of the same name started again,
   cmd/nova-swarm/member.go cmdMember). Its children do not die with it: a
   member's native leads a session of its own, which no signal to the member
   reaches, so every lane process and every read's process alive at the crash
   becomes an ORPHAN, owned by nobody, and runs on until its own deadline ends it
   (OrphanEnds) or a restart reaps it. Before v1.2.16 this module's restart said
   "every child and every read is gone" and assumed it; the code left them
   running, and a lane whose launch was never dealt to the member again (an epoch
   clear) ran on (b1, 2026-10-12, 13 hours at 100%). Restart now reaps the
   orphans before the worker takes a card (ReapOrphans; swarm.ReapOrphanLanes).
   The sprint deals a down worker's cards again, so a card that was held or
   running, and not owed, goes back to the queue at the crash.

   THE SELECTION (the cold read of nova-tools#5647). The reap ends every process
   whose environment names this worker's name and a launch under its slots
   directory (swarm.OrphanOf), and nothing in a process's environment says whether
   the worker that started it is dead. So the model has a second worker of the
   same name over the same slots, the TWIN (two daemons by mistake, a hand-run
   `member --once --as <name>` beside the daemon, a card's own test reusing a
   fleet machine's name and root), with lanes of its own (twinLive), and a
   STRANGER: a lane of a worker of another name under the same slots. The reap is
   one operator, Reap, run by whichever worker of the name starts (Restart,
   TwinStart): it ends the orphans and, when nothing keeps two live workers of one
   name apart, the other live worker's running lanes too. What keeps them apart is
   the MEMBER LOCK (cmd/nova-swarm/memberlock.go: the kernel's lock on
   <slots>/.member-<name>.lock, held for the worker's whole run and dropped with
   its process however it dies): with it a worker of the name starting while
   another lives is refused, which here is its start not happening. What keeps the
   stranger's lane out is the owner check (OrphanOf: the owner entry must be this
   name, never the mark alone).

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
     ReapOrphans      = FALSE: a restarted worker leaves what its dead incarnation
                        started running (the member before v1.2.16)
     MemberLock       = FALSE: no lock, so a worker of the name starting beside a
                        live one reaps the live one's running lanes (the first cut
                        of v1.2.16's reap, held by its cold read)
     OwnerChecked     = FALSE: the reap keys on the mark alone, so a lane of
                        another name under the same slots is ended with the orphans
   Each witness breaks the invariant its case names; three break a second one as
   well, by the same mechanism: GateBeforeStart = FALSE also breaks
   NoLaneWhileStopped (the lane launched after the stop runs while STOPPED),
   ReleaseHeld = FALSE breaks it the same way (the held card launched through runs
   while STOPPED), and PersistOwed = FALSE also breaks EveryLaneReturnsOnStop (the
   return forgotten never comes). The model trusts the store for what the worker
   cannot see: Return is sent whatever the word and StartMachine needs nothing
   owed; the worker's own hand-back every pass, whatever the word, is
   pkg/member/member.go machineStop and pkg/friend/stop.go returnOwed, tested as
   traces. MaxCrashes bounds the crashes and MaxTwinStarts the twin's starts, for a
   finite model. *)
EXTENDS Naturals, FiniteSets

CONSTANTS Lanes, Cards, None, CancelOnStop, GateBeforeStart, PersistOwed, NudgeGate, FinishCancelled, ReleaseHeld,
          ReapOrphans, MaxCrashes, MemberLock, OwnerChecked, MaxTwinStarts

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
          badFinish, \* a card the stop took was finished by the run it took it from
          up,        \* the worker's process is alive (FALSE between a Crash and its Restart)
          orphans,   \* cards whose lane or read process outlived the worker that started it, owned by nobody
          crashes,   \* how many times the worker died without its drain
          twin,      \* a second worker of this name over these slots is alive
          twinLive,  \* cards whose process the twin runs (its lanes)
          twinStarts,\* how many times the twin started
          stranger,  \* a lane of a worker of another name under these slots is alive
          badReap,   \* a reap ended a running lane of a live worker of this name
          strangerReaped \* a reap ended the stranger's lane

vars == <<machine, lane, card, queue, owed, returned, finished, cancelled, reading, badStart, badNudge, lostOwed, badFinish, up, orphans, crashes,
          twin, twinLive, twinStarts, stranger, badReap, strangerReaped>>

crashVars == <<up, orphans, crashes>>
twinVars == <<twin, twinLive, twinStarts>>
reapVars == <<stranger, badReap, strangerReaped>>

TypeOK == /\ machine \in {"RUNNING", "STOPPED"}
          /\ lane \in [Lanes -> {"idle", "held", "running", "cancelled"}]
          /\ card \in [Lanes -> Cards \cup {None}]
          /\ queue \subseteq Cards /\ owed \subseteq Cards /\ returned \subseteq Cards
          /\ finished \subseteq Cards /\ cancelled \subseteq Cards
          /\ reading \subseteq Cards
          /\ badStart \in BOOLEAN /\ badNudge \in BOOLEAN /\ lostOwed \in BOOLEAN
          /\ badFinish \in BOOLEAN
          /\ up \in BOOLEAN /\ orphans \subseteq Cards /\ crashes \in 0..MaxCrashes
          /\ twin \in BOOLEAN /\ twinLive \subseteq Cards /\ twinStarts \in 0..MaxTwinStarts
          /\ stranger \in BOOLEAN /\ badReap \in BOOLEAN /\ strangerReaped \in BOOLEAN

Init == /\ machine = "RUNNING"
        /\ lane = [l \in Lanes |-> "idle"]
        /\ card = [l \in Lanes |-> None]
        /\ queue = Cards
        /\ owed = {} /\ returned = {} /\ finished = {} /\ cancelled = {}
        /\ reading = {}
        /\ badStart = FALSE /\ badNudge = FALSE /\ lostOwed = FALSE /\ badFinish = FALSE
        /\ up = TRUE /\ orphans = {} /\ crashes = 0
        /\ twin = FALSE /\ twinLive = {} /\ twinStarts = 0
        /\ stranger = TRUE /\ badReap = FALSE /\ strangerReaped = FALSE

InHand == {card[l] : l \in Lanes} \ {None}

(* The cards whose process is alive under the worker: each lane's that runs or was
   told to stop and has not ended, and each read's. *)
Live == {card[l] : l \in {k \in Lanes : lane[k] \in {"running", "cancelled"}}} \cup reading

(* A lane takes a card from the queue and holds it, not yet launched (the friend's
   claim, lanes.go, and the member's claim ahead of its launch). The word is read
   again right before the launch, in Start, not here; nothing is taken while STOPPED. *)
Take(l, c) == /\ up
              /\ lane[l] = "idle" /\ machine = "RUNNING" /\ c \in queue /\ c \notin InHand
              /\ lane' = [lane EXCEPT ![l] = "held"]
              /\ card' = [card EXCEPT ![l] = c]
              /\ queue' = queue \ {c}
              /\ UNCHANGED <<machine, owed, returned, finished, cancelled, reading, badStart, badNudge, lostOwed, badFinish>>
              /\ UNCHANGED <<crashVars, twinVars, reapVars>>

(* A held lane launches its card. The word is read right before the launch
   (GateBeforeStart); without the gate, or without the held card being given up
   (ReleaseHeld), a held card launches after the stop arrived, recorded on badStart. *)
Start(l) == /\ up
            /\ lane[l] = "held"
            /\ \/ machine = "RUNNING"
               \/ (machine = "STOPPED" /\ (~GateBeforeStart \/ ~ReleaseHeld))
            /\ lane' = [lane EXCEPT ![l] = "running"]
            /\ badStart' = (badStart \/ machine = "STOPPED")
            /\ UNCHANGED <<machine, card, queue, owed, returned, finished, cancelled, reading, badNudge, lostOwed, badFinish>>
            /\ UNCHANGED <<crashVars, twinVars, reapVars>>

(* A held lane gives its card up when the word is STOPPED (the friend's releaseHeld,
   lanes.go: the window between the claim and the start): the card is owed a
   stop-return with no run to end, so it is out of the lane at once. *)
GiveUpHeld(l) == /\ up
                 /\ lane[l] = "held" /\ machine = "STOPPED" /\ GateBeforeStart /\ ReleaseHeld
                 /\ lane' = [lane EXCEPT ![l] = "idle"]
                 /\ card' = [card EXCEPT ![l] = None]
                 /\ owed' = owed \cup {card[l]}
                 /\ cancelled' = cancelled \cup {card[l]}
                 /\ UNCHANGED <<machine, queue, returned, finished, reading, badStart, badNudge, lostOwed, badFinish>>
                 /\ UNCHANGED <<crashVars, twinVars, reapVars>>

(* A card is read (the friend's read lanes): taken from the queue, its read under way
   until its run ends. The word gates the begin. *)
Read(c) == /\ up
           /\ machine = "RUNNING" /\ c \in queue /\ c \notin InHand /\ c \notin reading
           /\ reading' = reading \cup {c}
           /\ queue' = queue \ {c}
           /\ UNCHANGED <<machine, lane, card, owed, returned, finished, cancelled, badStart, badNudge, lostOwed, badFinish>>
           /\ UNCHANGED <<crashVars, twinVars, reapVars>>

(* A read's run ends. A read the stop cancelled (its card owed) stays owed, returned
   once its run ends; a read not cancelled puts the card back to ready. *)
ReadEnd(c) == /\ up
              /\ c \in reading
              /\ reading' = reading \ {c}
              /\ queue' = IF c \in owed THEN queue ELSE queue \cup {c}
              /\ UNCHANGED <<machine, lane, card, owed, returned, finished, cancelled, badStart, badNudge, lostOwed, badFinish>>
              /\ UNCHANGED <<crashVars, twinVars, reapVars>>

(* The machine stops. The next pass of a worker of this name reads STOPPED and tells
   every running child to stop, its card owed a stop-return (CancelOnStop), and
   every read with it, and the twin's lanes the same (a twin is a member process
   with the same hand-back); the witness lets the children run on. A name with no
   worker up reads nothing: the word waits for a restart's first pass. *)
Stop == /\ (up \/ twin)
        /\ machine = "RUNNING"
        /\ machine' = "STOPPED"
        /\ IF CancelOnStop
           THEN /\ lane' = [l \in Lanes |-> IF lane[l] = "running" THEN "cancelled" ELSE lane[l]]
                /\ owed' = owed \cup {card[l] : l \in {k \in Lanes : lane[k] = "running"}} \cup reading \cup twinLive
                /\ cancelled' = cancelled \cup {card[l] : l \in {k \in Lanes : lane[k] = "running"}} \cup reading \cup twinLive
           ELSE UNCHANGED <<lane, owed, cancelled>>
        /\ UNCHANGED <<card, queue, returned, finished, reading, badStart, badNudge, lostOwed, badFinish>>
        /\ UNCHANGED <<crashVars, twinVars, reapVars>>

(* A child ends. A cancelled lane's card stays owed (the return goes once the process
   is dead) and is finished by no one (the finding when FinishCancelled); a running
   lane's card is finished. *)
End(l) == /\ up
          /\ lane[l] \in {"running", "cancelled"}
          /\ lane' = [lane EXCEPT ![l] = "idle"]
          /\ card' = [card EXCEPT ![l] = None]
          /\ finished' = IF lane[l] = "running" \/ FinishCancelled THEN finished \cup {card[l]} ELSE finished
          /\ badFinish' = (badFinish \/ (lane[l] = "cancelled" /\ FinishCancelled))
          /\ UNCHANGED <<machine, queue, owed, returned, cancelled, reading, badStart, badNudge, lostOwed>>
          /\ UNCHANGED <<crashVars, twinVars, reapVars>>

(* The stop-return: sent only after the run is dead (the card is in no lane and no
   read of either worker), whatever the word (the hand-back runs every step, RUNNING
   or STOPPED), by whichever worker of the name is up; the store puts the same card
   back in the owner's queue at a new generation. *)
Return(c) == /\ (up \/ twin)
             /\ c \in owed /\ c \notin InHand /\ c \notin reading /\ c \notin twinLive
             /\ owed' = owed \ {c}
             /\ returned' = returned \cup {c}
             /\ queue' = queue \cup {c}
             /\ UNCHANGED <<machine, lane, card, finished, cancelled, reading, badStart, badNudge, lostOwed, badFinish>>
             /\ UNCHANGED <<crashVars, twinVars, reapVars>>

(* The worker dies without its drain while a child of it is alive. Its lanes and reads
   are gone from its memory, and their processes are not: each becomes an orphan. A
   card held or running and not owed goes back to the queue (the sprint deals a down
   worker's cards again). What it owed is read back from disk at the restart
   (PersistOwed); the witness forgets it and finishes the gone run instead (a FAIL for
   a card the stop took). The lock dies with the process: the kernel drops it. *)
Crash == /\ up /\ crashes < MaxCrashes /\ Live # {}
         /\ up' = FALSE
         /\ crashes' = crashes + 1
         /\ orphans' = orphans \cup Live
         /\ lane' = [l \in Lanes |-> "idle"]
         /\ card' = [l \in Lanes |-> None]
         /\ reading' = {}
         /\ queue' = queue \cup ((({card[l] : l \in {k \in Lanes : lane[k] \in {"held", "running"}}} \cup reading) \ owed))
         /\ IF PersistOwed
            THEN UNCHANGED <<owed, finished, lostOwed, badFinish>>
            ELSE /\ finished' = finished \cup owed
                 /\ owed' = {}
                 /\ lostOwed' = (lostOwed \/ owed # {})
                 /\ badFinish' = (badFinish \/ owed # {})
         /\ UNCHANGED <<machine, returned, cancelled, badStart, badNudge>>
         /\ UNCHANGED <<twinVars, reapVars>>

(* The reap a starting worker of this name runs before its first pass
   (swarm.ReapOrphanLanes, from cmdMember): every process whose environment names
   this name and a launch under these slots, and the session each such process
   leads. Which processes those are is the whole of the finding:
     - the orphans its dead incarnations left (ReapOrphans; the witness leaves them);
     - the stranger's lane, a worker of another name's under the same slots, only
       when the owner is not checked (~OwnerChecked: the mark alone);
     - the running lanes of the OTHER live worker of this name, when there is one:
       the reap cannot tell them from orphans, and only the member lock keeps a
       second worker of the name from starting beside a live one (MemberLock, the
       guard on Restart and TwinStart). A reaped running lane is a run killed: its
       card goes back to the queue unless the stop owes it (as a crash's does). *)
ReapStranger == /\ stranger' = (stranger /\ OwnerChecked)
                /\ strangerReaped' = (strangerReaped \/ (stranger /\ ~OwnerChecked))

(* A worker of the same name starts again (the primary). With the lock, a live twin
   refuses this start, which here is the start not happening until the twin is
   gone. Before its first pass, so before it takes any card, it reaps (above): the
   orphans, and without the lock the twin's running lanes too. *)
Restart == /\ ~up
           /\ (MemberLock => ~twin)
           /\ up' = TRUE
           /\ orphans' = IF ReapOrphans THEN {} ELSE orphans
           /\ twinLive' = IF twin THEN {} ELSE twinLive
           /\ queue' = queue \cup (IF twin THEN twinLive \ owed ELSE {})
           /\ badReap' = (badReap \/ (twin /\ twinLive # {}))
           /\ ReapStranger
           /\ UNCHANGED <<machine, lane, card, owed, returned, finished, cancelled, reading, badStart, badNudge, lostOwed, badFinish, crashes, twin, twinStarts>>

(* A second worker of this name starts over the same slots while the primary may be
   alive (two daemons by mistake, a hand-run `member --once`, a card's test on a
   fleet machine's name and root). With the lock, a live primary refuses it, which
   here is its start not happening. Without the lock it starts, and its reap ends
   the primary's running lanes and reads as orphans of the name. *)
TwinStart == /\ ~twin /\ twinStarts < MaxTwinStarts
             /\ (MemberLock => ~up)
             /\ twin' = TRUE
             /\ twinStarts' = twinStarts + 1
             /\ orphans' = IF ReapOrphans THEN {} ELSE orphans
             /\ IF up
                THEN /\ lane' = [l \in Lanes |-> IF lane[l] \in {"running", "cancelled"} THEN "idle" ELSE lane[l]]
                     /\ card' = [l \in Lanes |-> IF lane[l] \in {"running", "cancelled"} THEN None ELSE card[l]]
                     /\ reading' = {}
                     /\ queue' = queue \cup (Live \ owed)
                     /\ badReap' = (badReap \/ Live # {})
                ELSE UNCHANGED <<lane, card, reading, queue, badReap>>
             /\ ReapStranger
             /\ UNCHANGED <<machine, owed, returned, finished, cancelled, badStart, badNudge, lostOwed, badFinish, up, crashes, twinLive>>

(* The twin takes a card and runs it (one at a time, for a small model); the word
   gates the take as it gates the primary's. *)
TwinTake(c) == /\ twin /\ twinLive = {}
               /\ machine = "RUNNING" /\ c \in queue /\ c \notin InHand /\ c \notin reading
               /\ twinLive' = {c}
               /\ queue' = queue \ {c}
               /\ UNCHANGED <<machine, lane, card, owed, returned, finished, cancelled, reading, badStart, badNudge, lostOwed, badFinish>>
               /\ UNCHANGED <<crashVars, twin, twinStarts, reapVars>>

(* The twin's child ends: its card finished, unless the stop took it (owed: returned
   once the process is dead, finished by no one). *)
TwinEnd(c) == /\ twin /\ c \in twinLive
              /\ twinLive' = twinLive \ {c}
              /\ finished' = IF c \in owed THEN finished ELSE finished \cup {c}
              /\ UNCHANGED <<machine, lane, card, queue, owed, returned, cancelled, reading, badStart, badNudge, lostOwed, badFinish>>
              /\ UNCHANGED <<crashVars, twin, twinStarts, reapVars>>

(* The twin stops with its drain (its `--once` pass done, or stopped by hand): its
   lanes ended, its lock dropped. *)
TwinStop == /\ twin /\ twinLive = {}
            /\ twin' = FALSE
            /\ UNCHANGED <<machine, lane, card, queue, owed, returned, finished, cancelled, reading, badStart, badNudge, lostOwed, badFinish>>
            /\ UNCHANGED <<crashVars, twinLive, twinStarts, reapVars>>

(* An orphan ends by itself: its native's own deadline. *)
OrphanEnds(c) == /\ c \in orphans
                 /\ orphans' = orphans \ {c}
                 /\ UNCHANGED <<machine, lane, card, queue, owed, returned, finished, cancelled, reading, badStart, badNudge, lostOwed, badFinish, up, crashes>>
                 /\ UNCHANGED <<twinVars, reapVars>>

(* A work nudge (the idle wake, the oldest-card urging) into the session: gated on
   the word (NudgeGate); the witness nudges while STOPPED. *)
Nudge == /\ up
         /\ (machine = "RUNNING" \/ ~NudgeGate)
         /\ badNudge' = (badNudge \/ machine = "STOPPED")
         /\ UNCHANGED <<machine, lane, card, queue, owed, returned, finished, cancelled, reading, badStart, lostOwed, badFinish>>
         /\ UNCHANGED <<crashVars, twinVars, reapVars>>

(* start: the store refuses it while a stop-return is owed (the store's rule). *)
StartMachine == /\ machine = "STOPPED" /\ owed = {}
                /\ machine' = "RUNNING"
                /\ UNCHANGED <<lane, card, queue, owed, returned, finished, cancelled, reading, badStart, badNudge, lostOwed, badFinish>>
                /\ UNCHANGED <<crashVars, twinVars, reapVars>>

Done == /\ finished \cup returned = Cards /\ queue = {} /\ InHand = {} /\ reading = {} /\ twinLive = {} /\ UNCHANGED vars

Next == \/ \E l \in Lanes, c \in Cards : Take(l, c)
        \/ \E l \in Lanes : Start(l)
        \/ \E l \in Lanes : GiveUpHeld(l)
        \/ \E l \in Lanes : End(l)
        \/ \E c \in Cards : Return(c)
        \/ \E c \in Cards : Read(c)
        \/ \E c \in Cards : ReadEnd(c)
        \/ \E c \in Cards : OrphanEnds(c)
        \/ \E c \in Cards : TwinTake(c)
        \/ \E c \in Cards : TwinEnd(c)
        \/ Stop \/ StartMachine \/ Crash \/ Restart \/ TwinStart \/ TwinStop \/ Nudge \/ Done

Fairness == /\ \A l \in Lanes : WF_vars(End(l))
            /\ \A l \in Lanes : WF_vars(Start(l) \/ GiveUpHeld(l))
            /\ \A c \in Cards : WF_vars(Return(c))
            /\ \A c \in Cards : WF_vars(ReadEnd(c))
            /\ \A c \in Cards : WF_vars(TwinEnd(c))
            /\ WF_vars(StartMachine)
            /\ WF_vars(Restart)
            /\ WF_vars(TwinStop)

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

(* No lane process outlives its worker's restart: once a worker is up again, nothing
   an earlier incarnation started is running owned by nobody (v1.2.16). *)
NoLaneProcessOutlivesRestart == up => orphans = {}

(* A running lane of a live worker of this name is never ended by a reap: the reap
   keys on name and slots, and the member lock is what says the earlier worker of
   the name is dead (cmd/nova-swarm/memberlock.go; the cold read of #5647). *)
NoLiveMembersLaneReaped == ~badReap

(* A lane of a worker of another name under the same slots is never ended: the
   owner is checked, never the mark alone (swarm.OrphanOf). *)
NoStrangerReaped == ~strangerReaped
=============================================================================
