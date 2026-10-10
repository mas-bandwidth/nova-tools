----------------------------- MODULE FriendLane -----------------------------
(***************************************************************************)
(* A friend's lanes as thin clients of the nova-sprint server (v1.2.6; the *)
(* owner, 2026-10-10: "always client/server, not distributed programming"). *)
(*                                                                         *)
(* The server (nova-sprint, Redis, keyed by epoch) is the one truth for    *)
(* what each lane holds. It keeps one record per (friend, lane, epoch):    *)
(* srv[l] is the card that record holds and the epoch it was granted at.   *)
(* A lane keeps nothing that decides what it holds: it asks.               *)
(*                                                                         *)
(* Every exchange is one request and its answer (an RPC): the lane sends,  *)
(* the server applies and answers, the lane does what the answer says. A   *)
(* lane that dies after the server applied and before it read the answer  *)
(* is Crash after the RPC. The verbs (nova-sprint `friend lane ...`):      *)
(*   Take(l)     take --lane l: names the friend and the lane, nothing    *)
(*               else; the server picks the epoch and the card (work or   *)
(*               read) and answers TAKEN card epoch, or the card the lane *)
(*               already holds (HOLDS), or NONE, or a refusal.            *)
(*   Held(l)     what do I hold: a lane after a crash, restart or clear.  *)
(*   Begin(l)    the run begins (a read's begin, a work card's start).    *)
(*   Progress(l) the run is alive.                                        *)
(*   Finish(l)   the run ended: done; TakenFinish(l), a card it holds and  *)
(*               cannot run (a report of a run before) finished failed.   *)
(*   Return(l)   the lane gives the card back (it drops it).              *)
(* Begin, Progress, Finish and Return carry the card and the epoch the    *)
(* server gave; the server applies them only when its record for this    *)
(* (friend, lane) holds that card at that epoch and the epoch is current. *)
(* Any refusal: the lane drops the job and asks again.                    *)
(*                                                                         *)
(* The server on its own: Gone(l), a lane that has not asked within the   *)
(* bound is gone, its card bounced back ready (the record dropped);       *)
(* Clear, tabula rasa: a new epoch, a new deal, every record dropped.     *)
(*                                                                         *)
(* Reversed witnesses (MCFriendLaneBroken*.cfg), each the code before:    *)
(*   RunOnNone   a take answered OK that moved nothing is logged "took"   *)
(*               and the lane runs a card still ready (2026-10-10 21:42Z, *)
(*               a friend host): WorkingOnlyOnTheServersSay fails.         *)
(*   LocalEpoch  the lane names the epoch its directory name says, 0 for  *)
(*               a job with none (progress at epoch 0): every progress is *)
(*               refused, the server's record still holds the card:      *)
(*               OneRequestApart fails.                                    *)
(*   KeepOnRefusal  a refused verb is logged and the lane runs on:        *)
(*               OneRequestApart fails.                                    *)
(*   CardOnly    the server applies a worker verb when the card is        *)
(*               working, whatever the epoch or lane (the server before): *)
(*               a stale lane's finish lands in the new epoch:            *)
(*               NoStaleStep fails.                                        *)
(*   NoGone      the server never bounces a silent lane's card back: a    *)
(*               lane that died stays holding it, WorkingResolves fails.  *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS Cards, Lanes, Steady, MaxClears, MaxCrashes, MaxGones, MaxReturns,
          RunOnNone, LocalEpoch, KeepOnRefusal, CardOnly, NoGone

None == "none"
Nobody == [card |-> None, ep |-> 0]

VARIABLES
    epoch,     \* the server: the current epoch
    col,       \* the server: card -> "ready" | "working" | "done" (this epoch's deal)
    srv,       \* the server: lane -> [card, ep], its (friend, lane, epoch) record
    alive,     \* the lane process is up
    lc,        \* the lane: [st, card, ep]; st "idle" | "restart" | "taken" | "running" | "down"
    stale,     \* ghost: the server changed this lane's record (or the lane lost its own) since its last RPC
    granted,   \* ghost: lane -> the <<card, ep>> the server's answers gave it
    staleStep, \* ghost: a verb naming an epoch not current changed state
    clears, crashes, gones, returns

vars == <<epoch, col, srv, alive, lc, stale, granted, staleStep, clears, crashes, gones, returns>>

Idle == [st |-> "idle", card |-> None, ep |-> 0]

Init ==
    /\ epoch = 1
    /\ col = [c \in Cards |-> "ready"]
    /\ srv = [l \in Lanes |-> Nobody]
    /\ alive = [l \in Lanes |-> TRUE]
    /\ lc = [l \in Lanes |-> Idle]
    /\ stale = [l \in Lanes |-> FALSE]
    /\ granted = [l \in Lanes |-> {}]
    /\ staleStep = FALSE
    /\ clears = 0 /\ crashes = 0 /\ gones = 0 /\ returns = 0

Holding(l) == lc[l].st \in {"taken", "running"}
LaneSays(l) == IF Holding(l) THEN <<lc[l].card, lc[l].ep>> ELSE <<None, 0>>
ServerSays(l) == <<srv[l].card, srv[l].ep>>
HeldBySome == {srv[l].card : l \in Lanes} \ {None}

\* The epoch the lane names: the server's, or (LocalEpoch) the directory's guess, 0.
Named(l) == IF LocalEpoch THEN 0 ELSE lc[l].ep

\* The server's check of a worker verb from lane l naming card c at epoch e.
Owns(l, c, e) ==
    IF CardOnly THEN col[c] = "working"
    ELSE /\ e = epoch
         /\ srv[l].card = c
         /\ srv[l].ep = e

\* every RPC clears the lane's staleness: after it, lane and server agree
Exchanged(l) == stale' = [stale EXCEPT ![l] = FALSE]

-----------------------------------------------------------------------------
\* take --lane l: the lane names itself only; the server picks.
TakeAsk(l) == alive[l] /\ lc[l].st = "idle"
TakeRest == UNCHANGED <<epoch, alive, staleStep, clears, crashes, gones, returns>>

\* HOLDS: the server's record holds a card: the lane is told it holds it.
TakeHolds(l) ==
    /\ TakeAsk(l)
    /\ srv[l].card # None
    /\ lc' = [lc EXCEPT ![l] = [st |-> "taken", card |-> srv[l].card, ep |-> srv[l].ep]]
    /\ granted' = [granted EXCEPT ![l] = @ \cup {ServerSays(l)}]
    /\ UNCHANGED <<col, srv>>
    /\ Exchanged(l) /\ TakeRest

\* TAKEN: the server picks a card ready on her row, at its epoch.
TakeGrant(l, c) ==
    /\ TakeAsk(l)
    /\ srv[l].card = None
    /\ col[c] = "ready"
    /\ col' = [col EXCEPT ![c] = "working"]
    /\ srv' = [srv EXCEPT ![l] = [card |-> c, ep |-> epoch]]
    /\ lc' = [lc EXCEPT ![l] = [st |-> "taken", card |-> c, ep |-> epoch]]
    /\ granted' = [granted EXCEPT ![l] = @ \cup {<<c, epoch>>}]
    /\ Exchanged(l) /\ TakeRest

\* NONE (nothing ready for her) or a refusal (STOPPED, her width full): nothing moves, and
\* the lane runs nothing (RunOnNone: it runs a card still ready, "took" on an OK that moved
\* nothing).
TakeNone(l) ==
    /\ TakeAsk(l)
    /\ srv[l].card = None
    /\ UNCHANGED <<col, srv, granted>>
    /\ IF RunOnNone /\ \E c \in Cards : col[c] = "ready"
       THEN \E c \in Cards : col[c] = "ready" /\
              lc' = [lc EXCEPT ![l] = [st |-> "taken", card |-> c, ep |-> epoch]]
       ELSE UNCHANGED lc
    /\ Exchanged(l) /\ TakeRest

Take(l) == TakeHolds(l) \/ (\E c \in Cards : TakeGrant(l, c)) \/ TakeNone(l)

\* what do I hold: the lane after a crash, a restart or a clear asks before anything else.
Held(l) ==
    /\ alive[l] /\ lc[l].st = "restart"
    /\ IF srv[l].card # None /\ srv[l].ep = epoch
       THEN /\ lc' = [lc EXCEPT ![l] = [st |-> "taken", card |-> srv[l].card, ep |-> srv[l].ep]]
            /\ granted' = [granted EXCEPT ![l] = @ \cup {ServerSays(l)}]
       ELSE /\ lc' = [lc EXCEPT ![l] = Idle]
            /\ UNCHANGED granted
    /\ Exchanged(l)
    /\ UNCHANGED <<epoch, col, srv, alive, staleStep, clears, crashes, gones, returns>>

\* A refused verb: the lane drops the job (KeepOnRefusal: it runs on regardless).
Drop(l) == lc' = [lc EXCEPT ![l] = IF KeepOnRefusal THEN lc[l] ELSE Idle]

\* The step a verb takes on the server, with the ghost of a stale epoch.
Applied(l, e) == staleStep' = (staleStep \/ e # epoch)

Begin(l) ==
    /\ alive[l] /\ lc[l].st = "taken"
    /\ LET c == lc[l].card IN
       IF Owns(l, c, Named(l))
       THEN /\ lc' = [lc EXCEPT ![l].st = "running"]
            /\ UNCHANGED staleStep
       ELSE /\ Drop(l) /\ UNCHANGED staleStep
    /\ Exchanged(l)
    /\ UNCHANGED <<epoch, col, srv, alive, granted, clears, crashes, gones, returns>>

Progress(l) ==
    /\ alive[l] /\ lc[l].st = "running"
    /\ IF Owns(l, lc[l].card, Named(l))
       THEN UNCHANGED lc
       ELSE Drop(l)
    /\ Exchanged(l)
    /\ UNCHANGED <<epoch, col, srv, alive, granted, staleStep, clears, crashes, gones, returns>>

Finish(l) ==
    /\ alive[l] /\ lc[l].st = "running"
    /\ LET c == lc[l].card IN
       IF Owns(l, c, Named(l))
       THEN /\ col' = [col EXCEPT ![c] = "done"]
            /\ srv' = [k \in Lanes |-> IF srv[k].card = c THEN Nobody ELSE srv[k]]
            /\ lc' = [lc EXCEPT ![l] = Idle]
            /\ Applied(l, lc[l].ep)
       ELSE /\ Drop(l) /\ UNCHANGED <<col, srv, staleStep>>
    /\ Exchanged(l)
    /\ UNCHANGED <<epoch, alive, granted, clears, crashes, gones, returns>>

\* The lane finishes, failed, a card it was told it holds and cannot run: its outbox holds
\* the report of a run before that no step finished (lane_take.go finishStale).
TakenFinish(l) ==
    /\ alive[l] /\ lc[l].st = "taken"
    /\ LET c == lc[l].card IN
       IF Owns(l, c, Named(l))
       THEN /\ col' = [col EXCEPT ![c] = "done"]
            /\ srv' = [k \in Lanes |-> IF srv[k].card = c THEN Nobody ELSE srv[k]]
            /\ lc' = [lc EXCEPT ![l] = Idle]
            /\ Applied(l, lc[l].ep)
       ELSE /\ Drop(l) /\ UNCHANGED <<col, srv, staleStep>>
    /\ Exchanged(l)
    /\ UNCHANGED <<epoch, alive, granted, clears, crashes, gones, returns>>

\* The lane gives its card back: it was told it holds a card it will not run (after a
\* restart, the job's tree gone), or its run was cut (the machine's stop, a cap).
Return(l) ==
    /\ alive[l] /\ Holding(l) /\ returns < MaxReturns
    /\ returns' = returns + 1
    /\ LET c == lc[l].card IN
       IF Owns(l, c, Named(l))
       THEN /\ col' = [col EXCEPT ![c] = "ready"]
            /\ srv' = [k \in Lanes |-> IF srv[k].card = c THEN Nobody ELSE srv[k]]
            /\ Applied(l, lc[l].ep)
       ELSE UNCHANGED <<col, srv, staleStep>>
    /\ lc' = [lc EXCEPT ![l] = Idle]
    /\ Exchanged(l)
    /\ UNCHANGED <<epoch, alive, granted, clears, crashes, gones>>

-----------------------------------------------------------------------------
\* The lane's process dies (a crash, the daemon stopped); what it held is the server's.
Crash(l) ==
    /\ l \notin Steady
    /\ alive[l] /\ crashes < MaxCrashes
    /\ alive' = [alive EXCEPT ![l] = FALSE]
    /\ lc' = [lc EXCEPT ![l] = [st |-> "down", card |-> None, ep |-> 0]]
    /\ stale' = [stale EXCEPT ![l] = TRUE]
    /\ crashes' = crashes + 1
    /\ UNCHANGED <<epoch, col, srv, granted, staleStep, clears, gones, returns>>

\* It comes back with nothing: its first word is Held.
Restart(l) ==
    /\ ~alive[l]
    /\ alive' = [alive EXCEPT ![l] = TRUE]
    /\ lc' = [lc EXCEPT ![l] = [st |-> "restart", card |-> None, ep |-> 0]]
    /\ UNCHANGED <<epoch, col, srv, stale, granted, staleStep, clears, crashes, gones, returns>>

\* The server: a lane that has not asked within the bound is gone; its card bounces back.
\* A dead lane never asks; a live one may be slow past the bound too (bounded).
Gone(l) ==
    /\ ~NoGone
    /\ srv[l].card # None
    /\ \/ ~alive[l]
       \/ gones < MaxGones
    /\ col' = [col EXCEPT ![srv[l].card] = "ready"]
    /\ srv' = [srv EXCEPT ![l] = Nobody]
    /\ stale' = [stale EXCEPT ![l] = TRUE]
    /\ gones' = IF alive[l] THEN gones + 1 ELSE gones
    /\ UNCHANGED <<epoch, alive, lc, granted, staleStep, clears, crashes, returns>>

\* clear: tabula rasa, a new epoch and a new deal; every record of the old epoch dropped.
Clear ==
    /\ clears < MaxClears
    /\ epoch' = epoch + 1
    /\ col' = [c \in Cards |-> "ready"]
    /\ srv' = [l \in Lanes |-> Nobody]
    /\ stale' = [l \in Lanes |-> TRUE]
    /\ clears' = clears + 1
    /\ UNCHANGED <<alive, lc, granted, staleStep, crashes, gones, returns>>

Next ==
    \/ \E l \in Lanes :
          Take(l) \/ Held(l) \/ Begin(l) \/ Progress(l) \/ Finish(l) \/ TakenFinish(l) \/ Return(l)
          \/ Crash(l) \/ Restart(l) \/ Gone(l)
    \/ Clear

Fairness ==
    /\ \A l \in Lanes : WF_vars(TakeHolds(l)) /\ WF_vars(\E c \in Cards : TakeGrant(l, c))
    /\ \A l \in Lanes : WF_vars(Held(l)) /\ WF_vars(Begin(l)) /\ WF_vars(Finish(l))
    \* the bound passes for a lane that is down: its card bounces back
    /\ \A l \in Lanes : WF_vars(~alive[l] /\ Gone(l))

Spec == Init /\ [][Next]_vars /\ Fairness

-----------------------------------------------------------------------------
TypeOK ==
    /\ epoch \in 1..(MaxClears + 1)
    /\ col \in [Cards -> {"ready", "working", "done"}]
    /\ srv \in [Lanes -> [card : Cards \cup {None}, ep : 0..(MaxClears + 1)]]
    /\ alive \in [Lanes -> BOOLEAN]
    /\ \A l \in Lanes : lc[l].st \in {"idle", "restart", "taken", "running", "down"}

\* A card is working only on the server's say-so: a lane holds only what an answer gave
\* it, and every working card is held by one lane's record.
WorkingOnlyOnTheServersSay ==
    /\ \A l \in Lanes : Holding(l) => LaneSays(l) \in granted[l]
    /\ \A c \in Cards : col[c] = "working" <=> c \in HeldBySome
    /\ \A k, l \in Lanes : (k # l /\ srv[k].card # None) => srv[k].card # srv[l].card

\* The lane and the server never disagree on what a lane holds beyond one request: they
\* differ only when the server changed the record (gone, clear) or the lane lost its own
\* (crash) since its last exchange, and its next exchange makes them agree.
OneRequestApart ==
    \A l \in Lanes : (alive[l] /\ lc[l].st # "restart" /\ ~stale[l]) => LaneSays(l) = ServerSays(l)

\* No step naming a stale epoch changes state.
NoStaleStep == ~staleStep

\* Every card dealt to a live lane is taken or returned: nothing stays working forever.
WorkingResolves == \A c \in Cards : col[c] = "working" ~> col[c] # "working"

\* With a steady lane, every card of the last deal is eventually done.
AllDone == <>[](\A c \in Cards : col[c] = "done")
=============================================================================
