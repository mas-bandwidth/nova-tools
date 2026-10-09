---------------------------- MODULE StopCancelsRecovery ----------------------------
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
                        session while STOPPED (be686, 16:45Z)
     FinishCancelled  = TRUE: a lane told to stop finishes its card when its child
                        ends (the finish path not guarded on the cancel)
   Each witness breaks the invariant its case names; two break a second one as
   well, by the same mechanism: GateBeforeStart = FALSE also breaks
   NoLaneWhileStopped (the lane launched after the stop runs while STOPPED), and
   PersistOwed = FALSE also breaks EveryLaneReturnsOnStop (the return forgotten
   never comes). The model trusts the store for what the worker cannot see:
   Return needs STOPPED and StartMachine needs nothing owed; the worker's own
   hand-back every pass, whatever the word, is internal/member machineStop and
   internal/friend returnOwed, tested as traces. *)
EXTENDS Naturals, FiniteSets

CONSTANTS Lanes, Cards, None, CancelOnStop, GateBeforeStart, PersistOwed, NudgeGate, FinishCancelled,
          RequireExitProof, AllowIdentityLoss, DropLiveOnRestart, MaxAttempts,
          SkipSecondReturn

VARIABLES machine,   \* "RUNNING" or "STOPPED"
          lane,      \* each lane: "idle", "running" (a child spends), "cancelled" (told to stop, not yet ended)
          card,      \* each lane's card, or None
          queue,     \* cards the worker may take
          owed,      \* cards cancelled by the stop whose stop-return is not yet taken (recorded on disk when PersistOwed)
          returned,  \* cards the store took back by stop-return
          finished,  \* cards a lane finished
          cancelled, \* every card the stop ever cancelled
          live,      \* native groups still running, including after daemon restart
          pin,       \* groups whose leader or anchor birth still verifies signal authority
          badStart,  \* a lane started while STOPPED
          badNudge,  \* a work nudge went into the session while STOPPED
          lostOwed,  \* a stop-return owed was dropped by a restart
          badFinish, \* a card the stop took was finished by the run it took it from
          badReturn, \* a stop-return sent while the native group was still live
          badLostLive, \* restart discarded a surviving group without exit proof
          attempts,  \* bounded launches per card
          cancelCount, \* each cancelled attempt, not only whether this card ever stopped
          returnCount \* each acknowledged hand-back of a cancelled attempt

vars == <<machine, lane, card, queue, owed, returned, finished, cancelled, live, pin,
          badStart, badNudge, lostOwed, badFinish, badReturn, badLostLive,
          attempts, cancelCount, returnCount>>

TypeOK == /\ machine \in {"RUNNING", "STOPPED"}
          /\ lane \in [Lanes -> {"idle", "running", "cancelled"}]
          /\ card \in [Lanes -> Cards \cup {None}]
          /\ queue \subseteq Cards /\ owed \subseteq Cards /\ returned \subseteq Cards
          /\ finished \subseteq Cards /\ cancelled \subseteq Cards
          /\ live \subseteq Cards /\ pin \subseteq live
          /\ badStart \in BOOLEAN /\ badNudge \in BOOLEAN /\ lostOwed \in BOOLEAN
          /\ badFinish \in BOOLEAN /\ badReturn \in BOOLEAN /\ badLostLive \in BOOLEAN
          /\ attempts \in [Cards -> 0..MaxAttempts]
          /\ cancelCount \in [Cards -> 0..MaxAttempts]
          /\ returnCount \in [Cards -> 0..MaxAttempts]

Init == /\ machine = "RUNNING"
        /\ lane = [l \in Lanes |-> "idle"]
        /\ card = [l \in Lanes |-> None]
        /\ queue = Cards
        /\ owed = {} /\ returned = {} /\ finished = {} /\ cancelled = {}
        /\ live = {} /\ pin = {}
        /\ badStart = FALSE /\ badNudge = FALSE /\ lostOwed = FALSE /\ badFinish = FALSE
        /\ badReturn = FALSE /\ badLostLive = FALSE
        /\ attempts = [c \in Cards |-> 0]
        /\ cancelCount = [c \in Cards |-> 0]
        /\ returnCount = [c \in Cards |-> 0]

InHand == {card[l] : l \in Lanes} \ {None}

(* A lane takes a card and launches its child. The word is read right before the
   launch (GateBeforeStart); without the gate a start goes through while STOPPED,
   which is the finding, recorded on badStart. *)
Start(l, c) == /\ lane[l] = "idle" /\ c \in queue /\ c \notin InHand
               /\ attempts[c] < MaxAttempts
               /\ (machine = "RUNNING" \/ ~GateBeforeStart)
               /\ lane' = [lane EXCEPT ![l] = "running"]
               /\ card' = [card EXCEPT ![l] = c]
               /\ queue' = queue \ {c}
               /\ live' = live \cup {c} /\ pin' = pin \cup {c}
               /\ attempts' = [attempts EXCEPT ![c] = @ + 1]
               /\ badStart' = (badStart \/ machine = "STOPPED")
               /\ UNCHANGED <<machine, owed, returned, finished, cancelled, badNudge, lostOwed,
                             badFinish, badReturn, badLostLive, cancelCount, returnCount>>

(* The machine stops. The worker's next pass reads STOPPED and tells every running
   child to stop, its card owed a stop-return (CancelOnStop); the witness lets the
   children run on. *)
Stop == /\ machine = "RUNNING"
        /\ machine' = "STOPPED"
        /\ IF CancelOnStop
           THEN /\ lane' = [l \in Lanes |-> IF lane[l] = "running" THEN "cancelled" ELSE lane[l]]
                /\ owed' = owed \cup {card[l] : l \in {k \in Lanes : lane[k] = "running"}}
                /\ cancelled' = cancelled \cup {card[l] : l \in {k \in Lanes : lane[k] = "running"}}
                /\ cancelCount' = [c \in Cards |-> cancelCount[c] +
                     Cardinality({l \in Lanes : lane[l] = "running" /\ card[l] = c})]
           ELSE UNCHANGED <<lane, owed, cancelled, cancelCount>>
        /\ UNCHANGED <<card, queue, returned, finished, live, pin, badStart, badNudge,
                      lostOwed, badFinish, badReturn, badLostLive, attempts, returnCount>>

(* A child ends. A cancelled lane's card stays owed (the return goes once the process
   is dead) and is finished by no one (the finding when FinishCancelled); a running
   lane's card is finished. *)
End(l) == /\ lane[l] \in {"running", "cancelled"}
          /\ lane' = [lane EXCEPT ![l] = "idle"]
          /\ card' = [card EXCEPT ![l] = None]
          /\ live' = live \ {card[l]} /\ pin' = pin \ {card[l]}
          /\ finished' = IF lane[l] = "running" \/ FinishCancelled THEN finished \cup {card[l]} ELSE finished
          /\ badFinish' = (badFinish \/ (lane[l] = "cancelled" /\ FinishCancelled))
          /\ UNCHANGED <<machine, queue, owed, returned, cancelled, badStart, badNudge,
                        lostOwed, badReturn, badLostLive, attempts, cancelCount, returnCount>>

(* A verified pin lets a restarted owner end a surviving group. If the pin is
   lost, the group may exit on its own, but no fair action forces that outcome. *)
Quiesce(c) == /\ c \in owed /\ c \in live /\ c \in pin
              /\ live' = live \ {c} /\ pin' = pin \ {c}
              /\ UNCHANGED <<machine, lane, card, queue, owed, returned, finished, cancelled,
                            badStart, badNudge, lostOwed, badFinish, badReturn, badLostLive,
                            attempts, cancelCount, returnCount>>

ExitDetached(c) == /\ c \in live /\ c \notin InHand
                   /\ live' = live \ {c} /\ pin' = pin \ {c}
                   /\ UNCHANGED <<machine, lane, card, queue, owed, returned, finished, cancelled,
                                 badStart, badNudge, lostOwed, badFinish, badReturn, badLostLive,
                                 attempts, cancelCount, returnCount>>

LoseIdentity(c) == /\ AllowIdentityLoss /\ c \in live /\ c \in pin
                   /\ pin' = pin \ {c}
                   /\ UNCHANGED <<machine, lane, card, queue, owed, returned, finished, cancelled,
                                 live, badStart, badNudge, lostOwed, badFinish, badReturn, badLostLive,
                                 attempts, cancelCount, returnCount>>

(* The stop-return: sent only after the process is dead (the card is in no lane),
   only while STOPPED; the store puts the same card back in the owner's queue at a
   new generation. *)
Return(c) == /\ c \in owed /\ c \notin InHand /\ machine = "STOPPED"
             /\ (c \notin live \/ ~RequireExitProof)
             /\ (returnCount[c] = 0 \/ ~SkipSecondReturn)
             /\ owed' = owed \ {c}
             /\ returned' = returned \cup {c}
             /\ queue' = queue \cup {c}
             /\ returnCount' = [returnCount EXCEPT ![c] = @ + 1]
             /\ badReturn' = (badReturn \/ c \in live)
             /\ UNCHANGED <<machine, lane, card, finished, cancelled, live, pin, badStart,
                           badNudge, lostOwed, badFinish, badLostLive, attempts, cancelCount>>

(* A restart loses lane memory but not the native group: live and its verified
   pin survive independently of card/lane. An old implementation assumed every
   child gone, represented by DropLiveOnRestart. PersistOwed controls only the
   durable debt, not actual process liveness. *)
Restart == /\ machine = "STOPPED"
           /\ \E l \in Lanes : lane[l] = "cancelled"
           /\ lane' = [l \in Lanes |-> "idle"]
           /\ card' = [l \in Lanes |-> None]
           /\ IF DropLiveOnRestart
              THEN /\ live' = {} /\ pin' = {}
                   /\ badLostLive' = (badLostLive \/ live # {})
              ELSE UNCHANGED <<live, pin, badLostLive>>
           /\ IF PersistOwed
              THEN UNCHANGED <<owed, finished, lostOwed, badFinish>>
              ELSE /\ finished' = finished \cup owed
                   /\ owed' = {}
                   /\ lostOwed' = TRUE
                   /\ badFinish' = (badFinish \/ owed # {})
           /\ UNCHANGED <<machine, queue, returned, cancelled, badStart, badNudge, badReturn,
                         attempts, cancelCount, returnCount>>

(* A work nudge (the idle wake, the oldest-card urging) into the session: gated on
   the word (NudgeGate); the witness nudges while STOPPED. *)
Nudge == /\ (machine = "RUNNING" \/ ~NudgeGate)
         /\ badNudge' = (badNudge \/ machine = "STOPPED")
         /\ UNCHANGED <<machine, lane, card, queue, owed, returned, finished, cancelled, live,
                       pin, badStart, lostOwed, badFinish, badReturn, badLostLive,
                       attempts, cancelCount, returnCount>>

(* start: the store refuses it while a stop-return is owed (the store's rule). *)
StartMachine == /\ machine = "STOPPED" /\ owed = {}
                /\ machine' = "RUNNING"
                /\ UNCHANGED <<lane, card, queue, owed, returned, finished, cancelled, live, pin,
                              badStart, badNudge, lostOwed, badFinish, badReturn, badLostLive,
                              attempts, cancelCount, returnCount>>

Done == /\ finished \cup returned = Cards /\ queue = {} /\ InHand = {} /\ UNCHANGED vars

Next == \/ \E l \in Lanes, c \in Cards : Start(l, c)
        \/ \E l \in Lanes : End(l)
        \/ \E c \in Cards : Quiesce(c) \/ ExitDetached(c) \/ LoseIdentity(c)
        \/ \E c \in Cards : Return(c)
        \/ Stop \/ StartMachine \/ Restart \/ Nudge \/ Done

Fairness == /\ \A l \in Lanes : WF_vars(End(l))
            /\ \A c \in Cards : WF_vars(Quiesce(c))
            /\ \A c \in Cards : WF_vars(Return(c))
            /\ WF_vars(StartMachine)

Spec == Init /\ [][Next]_vars /\ Fairness

----
(* While STOPPED no lane spends: every lane was told to stop (or is idle). *)
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
EveryLaneReturnsOnStop == \A c \in Cards, n \in 1..MaxAttempts :
                            (cancelCount[c] >= n) ~> (returnCount[c] >= n)

(* A stop-return owed survives the daemon's restart. *)
StopReturnsSurviveRestart == ~lostOwed

(* No work nudge while STOPPED. *)
NoNudgeWhileStopped == ~badNudge

(* A stop-return requires group exit proof, even after restart detached it. *)
NoPrematureReturn == ~badReturn

(* A daemon restart cannot make a still-running native group disappear. *)
LiveGroupsSurviveRestart == ~badLostLive

(* What is owed was cancelled by the stop: nothing else is ever stop-returned. *)
OwedIsCancelled == owed \subseteq cancelled
=============================================================================
