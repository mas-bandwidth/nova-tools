----------------------------- MODULE FriendCard -----------------------------
EXTENDS Naturals, FiniteSets

(***************************************************************************)
(* Bounded lifecycle of sprint cards dealt to friends.  FriendPresence.tla    *)
(* models table presence; Friend.tla models the daemon/session challenge.     *)
(* This module composes the evidence those layers expose with delivery,      *)
(* start, progress, finish, return, take-back and the dashboard projection.  *)
(*                                                                           *)
(* Clocks saturate at Bound.  A clock at Bound means "Bound or older".       *)
(* Tick withdraws work when its evidence expires.  A dead run is finished   *)
(* as failed no later than DeadRunBound ticks after die.                      *)
(*                                                                           *)
(* This is a bounded design abstraction, not a Go refinement proof.  One    *)
(* card generation is represented; stream ordering, report parsing and the  *)
(* exact bus payload are abstracted to receipts.  All cards share the tier  *)
(* in CardTier and widths are the small instance's lane counts.              *)
(***************************************************************************)

CONSTANTS Friends, Cards, Tiers, CardTier, FriendTiers, Width,
          Bound, PingEvery, DeliveryBound, StartBound, ProgressBound,
          DeadRunBound, ReturnBound, MaxEvents

ASSUME
  Friends # {} /\ Cards # {} /\ Tiers # {} /\
  CardTier \in [Cards -> Tiers] /\ FriendTiers \in [Friends -> SUBSET Tiers] /\
  Width \in [Friends -> (Nat \ {0})] /\
  Bound \in (Nat \ {0}) /\
  PingEvery \in 1..Bound /\
  DeliveryBound \in 1..Bound /\ StartBound \in 1..Bound /\
  ProgressBound \in 1..Bound /\
  DeadRunBound \in 1..Bound /\ ReturnBound \in 1..Bound /\
  MaxEvents \in Nat

Pool == "pool"
Review == "review"
NoFriend == "none"

CardStates == {"pool", "dealt", "delivered", "started", "finished",
               "returned", "takenback"}
ActiveStates == {"dealt", "delivered", "started"}
TerminalStates == {"finished", "returned", "takenback"}
AlarmLayers == {"up", "daemon", "hears", "delivered", "started", "progressing",
                "finished", "returned"}

VARIABLES daemonLive, daemonAge, heardAge, pingOwed, archived, creditOut,
          held, cardState, holder, assigned, takenFrom, dealt, delivered,
          started, finishReceipt, busNote, returnReceipt, dealAge, deliveryAge,
          startAge, runAge, progressAge, goneAge, finishAge, returnAge,
          runLive, alarms, disruptions, shown

vars == <<daemonLive, daemonAge, heardAge, pingOwed, archived, creditOut,
          held, cardState, holder, assigned, takenFrom, dealt, delivered,
          started, finishReceipt, busNote, returnReceipt, dealAge, deliveryAge,
          startAge, runAge, progressAge, goneAge, finishAge, returnAge,
          runLive, alarms, disruptions, shown>>

Age(n) == IF n < Bound THEN n + 1 ELSE Bound
Active(c) == cardState[c] \in ActiveStates
Up(f) == daemonLive[f] /\ daemonAge[f] < Bound /\ heardAge[f] < Bound /\ ~creditOut[f]
Load(f) == Cardinality({c \in Cards : Active(c) /\ holder[c] = f})
Eligible(c) == {f \in Friends : Up(f) /\ ~held[f] /\
                                  CardTier[c] \in FriendTiers[f] /\
                                  Load(f) < Width[f]}
MinLoad(fs) == CHOOSE n \in {Load(f) : f \in fs} :
                        \A m \in {Load(f) : f \in fs} : n <= m
BestEligible(c) == IF Eligible(c) = {} THEN {} ELSE
                   {f \in Eligible(c) : Load(f) = MinLoad(Eligible(c))}

Init ==
  /\ daemonLive = [f \in Friends |-> TRUE]
  /\ daemonAge = [f \in Friends |-> Bound]
  /\ heardAge = [f \in Friends |-> Bound]
  /\ pingOwed = [f \in Friends |-> FALSE]
  /\ archived = [f \in Friends |-> FALSE]
  /\ creditOut = [f \in Friends |-> FALSE]
  /\ held = [f \in Friends |-> FALSE]
  /\ cardState = [c \in Cards |-> "pool"]
  /\ holder = [c \in Cards |-> Pool]
  /\ assigned = [c \in Cards |-> NoFriend]
  /\ takenFrom = [c \in Cards |-> NoFriend]
  /\ dealt = [c \in Cards |-> FALSE]
  /\ delivered = [c \in Cards |-> FALSE]
  /\ started = [c \in Cards |-> FALSE]
  /\ finishReceipt = [c \in Cards |-> FALSE]
  /\ busNote = [c \in Cards |-> FALSE]
  /\ returnReceipt = [c \in Cards |-> FALSE]
  /\ dealAge = [c \in Cards |-> Bound]
  /\ deliveryAge = [c \in Cards |-> Bound]
  /\ startAge = [c \in Cards |-> Bound]
  /\ runAge = [c \in Cards |-> Bound]
  /\ progressAge = [c \in Cards |-> Bound]
  /\ goneAge = [c \in Cards |-> Bound]
  /\ finishAge = [c \in Cards |-> Bound]
  /\ returnAge = [c \in Cards |-> Bound]
  /\ runLive = [c \in Cards |-> FALSE]
  /\ alarms = [f \in Friends |-> {}]
  /\ disruptions = 0
  /\ shown = DashboardMap

(* The first answer is a session's wake-ping answer, not a daemon beat. *)
Beat(f) ==
  /\ daemonLive[f]
  /\ daemonAge' = [daemonAge EXCEPT ![f] = 0]
  /\ UNCHANGED <<daemonLive, heardAge, pingOwed, archived, creditOut, held,
                 cardState, holder, assigned, takenFrom, dealt, delivered,
                 started, finishReceipt, busNote, returnReceipt, dealAge,
                 deliveryAge, startAge, runAge, progressAge, goneAge,
                 finishAge, returnAge, runLive, alarms, disruptions>>

Ping(f) ==
  /\ daemonLive[f] /\ ~pingOwed[f] /\ heardAge[f] >= PingEvery
  /\ pingOwed' = [pingOwed EXCEPT ![f] = TRUE]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, archived, creditOut, held,
                 cardState, holder, assigned, takenFrom, dealt, delivered,
                 started, finishReceipt, busNote, returnReceipt, dealAge,
                 deliveryAge, startAge, runAge, progressAge, goneAge,
                 finishAge, returnAge, runLive, alarms, disruptions>>

Answer(f) ==
  /\ pingOwed[f] /\ daemonLive[f] /\ ~archived[f] /\ ~creditOut[f]
  /\ pingOwed' = [pingOwed EXCEPT ![f] = FALSE]
  /\ heardAge' = [heardAge EXCEPT ![f] = 0]
  /\ UNCHANGED <<daemonLive, daemonAge, archived, creditOut, held, cardState,
                 holder, assigned, takenFrom, dealt, delivered, started,
                 finishReceipt, busNote, returnReceipt, dealAge, deliveryAge,
                 startAge, runAge, progressAge, goneAge, finishAge, returnAge,
                 runLive, alarms, disruptions>>

(* A deal reads a fresh heartbeat, a fresh session answer, tier and idle lane. *)
Deal(c, f) ==
  /\ cardState[c] = "pool" /\ f \in BestEligible(c)
  /\ cardState' = [cardState EXCEPT ![c] = "dealt"]
  /\ holder' = [holder EXCEPT ![c] = f]
  /\ assigned' = [assigned EXCEPT ![c] = f]
  /\ dealt' = [dealt EXCEPT ![c] = TRUE]
  /\ dealAge' = [dealAge EXCEPT ![c] = 0]
  /\ deliveryAge' = [deliveryAge EXCEPT ![c] = Bound]
  /\ startAge' = [startAge EXCEPT ![c] = Bound]
  /\ runAge' = [runAge EXCEPT ![c] = Bound]
  /\ progressAge' = [progressAge EXCEPT ![c] = Bound]
  /\ goneAge' = [goneAge EXCEPT ![c] = Bound]
  /\ finishAge' = [finishAge EXCEPT ![c] = Bound]
  /\ returnAge' = [returnAge EXCEPT ![c] = Bound]
  /\ delivered' = [delivered EXCEPT ![c] = FALSE]
  /\ started' = [started EXCEPT ![c] = FALSE]
  /\ finishReceipt' = [finishReceipt EXCEPT ![c] = FALSE]
  /\ busNote' = [busNote EXCEPT ![c] = FALSE]
  /\ returnReceipt' = [returnReceipt EXCEPT ![c] = FALSE]
  /\ runLive' = [runLive EXCEPT ![c] = FALSE]
  /\ alarms' = [alarms EXCEPT ![f] = @ \ {"delivered"}]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 creditOut, held, takenFrom, disruptions>>

(* Level preserves the card, but issues a fresh deal/delivery generation. *)
Level(c, g) ==
  /\ cardState[c] \in {"dealt", "delivered"}
  /\ holder[c] \in Friends /\ g \in BestEligible(c)
  /\ Load(g) < Load(holder[c]) /\ g # takenFrom[c]
  /\ cardState' = [cardState EXCEPT ![c] = "dealt"]
  /\ holder' = [holder EXCEPT ![c] = g]
  /\ assigned' = [assigned EXCEPT ![c] = g]
  /\ dealAge' = [dealAge EXCEPT ![c] = 0]
  /\ deliveryAge' = [deliveryAge EXCEPT ![c] = Bound]
  /\ delivered' = [delivered EXCEPT ![c] = FALSE]
  /\ alarms' = [f \in Friends |->
       IF f \in {holder[c], g} THEN alarms[f] \ {"delivered"} ELSE alarms[f]]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 creditOut, held, takenFrom, dealt, started, finishReceipt,
                 busNote, returnReceipt, startAge, runAge, progressAge,
                 goneAge, finishAge, returnAge, runLive, disruptions>>

Deliver(c) ==
  /\ cardState[c] = "dealt" /\ holder[c] \in Friends /\ Up(holder[c])
  /\ cardState' = [cardState EXCEPT ![c] = "delivered"]
  /\ delivered' = [delivered EXCEPT ![c] = TRUE]
  /\ deliveryAge' = [deliveryAge EXCEPT ![c] = 0]
  /\ alarms' = [alarms EXCEPT ![holder[c]] = @ \ {"delivered"}]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 creditOut, held, holder, assigned, takenFrom, dealt, started,
                 finishReceipt, busNote, returnReceipt, dealAge, startAge,
                 runAge, progressAge, goneAge, finishAge, returnAge, runLive,
                 disruptions>>

FailDelivery(c) ==
  /\ cardState[c] = "dealt" /\ holder[c] \in Friends
  /\ alarms' = [alarms EXCEPT ![holder[c]] = @ \cup {"delivered"}]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 creditOut, held, cardState, holder, assigned, takenFrom,
                 dealt, delivered, started, finishReceipt, busNote,
                 returnReceipt, dealAge, deliveryAge, startAge, runAge,
                 progressAge, goneAge, finishAge, returnAge, runLive,
                 disruptions>>

Start(c) ==
  /\ cardState[c] = "delivered" /\ holder[c] \in Friends
  /\ cardState' = [cardState EXCEPT ![c] = "started"]
  /\ started' = [started EXCEPT ![c] = TRUE]
  /\ startAge' = [startAge EXCEPT ![c] = 0]
  /\ runAge' = [runAge EXCEPT ![c] = 0]
  /\ progressAge' = [progressAge EXCEPT ![c] = 0]
  /\ goneAge' = [goneAge EXCEPT ![c] = Bound]
  /\ runLive' = [runLive EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 creditOut, held, holder, assigned, takenFrom, dealt, delivered,
                 finishReceipt, busNote, returnReceipt, dealAge, deliveryAge,
                 finishAge, returnAge, alarms, disruptions>>

Progress(c) ==
  /\ cardState[c] = "started" /\ runLive[c]
  /\ progressAge' = [progressAge EXCEPT ![c] = 0]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 creditOut, held, cardState, holder, assigned, takenFrom,
                 dealt, delivered, started, finishReceipt, busNote,
                 returnReceipt, dealAge, deliveryAge, startAge, runAge,
                 goneAge, finishAge, returnAge, runLive, alarms, disruptions>>

Die(c) ==
  /\ cardState[c] = "started" /\ runLive[c]
  /\ runLive' = [runLive EXCEPT ![c] = FALSE]
  /\ goneAge' = [goneAge EXCEPT ![c] = 0]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 creditOut, held, cardState, holder, assigned, takenFrom,
                 dealt, delivered, started, finishReceipt, busNote,
                 returnReceipt, dealAge, deliveryAge, startAge, runAge,
                 progressAge, finishAge, returnAge, alarms, disruptions>>

Finish(c) ==
  /\ cardState[c] = "started" /\ runLive[c]
  /\ cardState' = [cardState EXCEPT ![c] = "finished"]
  /\ holder' = [holder EXCEPT ![c] = Review]
  /\ finishReceipt' = [finishReceipt EXCEPT ![c] = TRUE]
  /\ busNote' = [busNote EXCEPT ![c] = TRUE]
  /\ finishAge' = [finishAge EXCEPT ![c] = 0]
  /\ runLive' = [runLive EXCEPT ![c] = FALSE]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 creditOut, held, assigned, takenFrom, dealt, delivered,
                 started, returnReceipt, dealAge, deliveryAge, startAge,
                 runAge, progressAge, goneAge, returnAge, alarms, disruptions>>

Return(c) ==
  /\ cardState[c] = "finished"
  /\ cardState' = [cardState EXCEPT ![c] = "returned"]
  /\ holder' = [holder EXCEPT ![c] = "coordinator"]
  /\ returnReceipt' = [returnReceipt EXCEPT ![c] = TRUE]
  /\ returnAge' = [returnAge EXCEPT ![c] = 0]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 creditOut, held, assigned, takenFrom, dealt, delivered,
                 started, finishReceipt, busNote, dealAge, deliveryAge,
                 startAge, runAge, progressAge, goneAge, finishAge, runLive,
                 alarms, disruptions>>

(* Coordinator take-back moves even a started run off the friend's row. *)
TakeBack(c) ==
  /\ Active(c) /\ holder[c] \in Friends
  /\ cardState' = [cardState EXCEPT ![c] = "takenback"]
  /\ holder' = [holder EXCEPT ![c] = Pool]
  /\ takenFrom' = [takenFrom EXCEPT ![c] = holder[c]]
  /\ runLive' = [runLive EXCEPT ![c] = FALSE]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 creditOut, held, assigned, dealt, delivered, started,
                 finishReceipt, busNote, returnReceipt, dealAge, deliveryAge,
                 startAge, runAge, progressAge, goneAge, finishAge, returnAge,
                 alarms, disruptions>>

ActiveCardsOf(f) == {c \in Cards : Active(c) /\ holder[c] = f}
TakeBackAll(f) ==
  /\ holder' = [c \in Cards |-> IF c \in ActiveCardsOf(f) THEN Pool ELSE holder[c]]
  /\ cardState' = [c \in Cards |-> IF c \in ActiveCardsOf(f) THEN "takenback" ELSE cardState[c]]
  /\ takenFrom' = [c \in Cards |-> IF c \in ActiveCardsOf(f) THEN f ELSE takenFrom[c]]
  /\ runLive' = [c \in Cards |-> IF c \in ActiveCardsOf(f) THEN FALSE ELSE runLive[c]]

Hold(f) ==
  /\ ~held[f] /\ disruptions < MaxEvents
  /\ held' = [held EXCEPT ![f] = TRUE]
  /\ TakeBackAll(f)
  /\ disruptions' = disruptions + 1
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 creditOut, assigned, dealt, delivered, started, finishReceipt,
                 busNote, returnReceipt, dealAge, deliveryAge, startAge,
                 runAge, progressAge, goneAge, finishAge, returnAge, alarms>>

Release(f) ==
  /\ held[f]
  /\ held' = [held EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 creditOut, cardState, holder, assigned, takenFrom, dealt,
                 delivered, started, finishReceipt, busNote, returnReceipt,
                 dealAge, deliveryAge, startAge, runAge, progressAge, goneAge,
                 finishAge, returnAge, runLive, alarms, disruptions>>

SessionArchived(f) ==
  /\ ~archived[f] /\ disruptions < MaxEvents
  /\ archived' = [archived EXCEPT ![f] = TRUE]
  /\ disruptions' = disruptions + 1
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, creditOut,
                 held, cardState, holder, assigned, takenFrom, dealt,
                 delivered, started, finishReceipt, busNote, returnReceipt,
                 dealAge, deliveryAge, startAge, runAge, progressAge, goneAge,
                 finishAge, returnAge, runLive, alarms>>

SessionRestored(f) ==
  /\ archived[f]
  /\ archived' = [archived EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, creditOut,
                 held, cardState, holder, assigned, takenFrom, dealt,
                 delivered, started, finishReceipt, busNote, returnReceipt,
                 dealAge, deliveryAge, startAge, runAge, progressAge, goneAge,
                 finishAge, returnAge, runLive, alarms, disruptions>>

CreditOut(f) ==
  /\ ~creditOut[f] /\ disruptions < MaxEvents
  /\ creditOut' = [creditOut EXCEPT ![f] = TRUE]
  /\ TakeBackAll(f)
  /\ alarms' = [alarms EXCEPT ![f] = @ \cup {"hears"}]
  /\ disruptions' = disruptions + 1
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 held, assigned, dealt, delivered, started, finishReceipt,
                 busNote, returnReceipt,
                 dealAge, deliveryAge, startAge, runAge, progressAge, goneAge,
                  finishAge, returnAge>>

CreditBack(f) ==
  /\ creditOut[f]
  /\ creditOut' = [creditOut EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 held, cardState, holder, assigned, takenFrom, dealt,
                 delivered, started, finishReceipt, busNote, returnReceipt,
                 dealAge, deliveryAge, startAge, runAge, progressAge, goneAge,
                 finishAge, returnAge, runLive, alarms, disruptions>>

Reboot(f) ==
  /\ daemonLive[f] /\ disruptions < MaxEvents
  /\ daemonLive' = [daemonLive EXCEPT ![f] = FALSE]
  /\ daemonAge' = [daemonAge EXCEPT ![f] = Bound]
  /\ pingOwed' = [pingOwed EXCEPT ![f] = FALSE]
  /\ TakeBackAll(f)
  /\ alarms' = [alarms EXCEPT ![f] = @ \cup {"daemon"}]
  /\ disruptions' = disruptions + 1
  /\ UNCHANGED <<heardAge, archived, creditOut, held, assigned, dealt,
                 delivered, started, finishReceipt, busNote, returnReceipt,
                 dealAge, deliveryAge, startAge, runAge, progressAge, goneAge,
                 finishAge, returnAge>>

BringBackUp(f) ==
  /\ ~daemonLive[f]
  /\ daemonLive' = [daemonLive EXCEPT ![f] = TRUE]
  /\ daemonAge' = [daemonAge EXCEPT ![f] = 0]
  /\ UNCHANGED <<heardAge, pingOwed, archived, creditOut, held, cardState,
                 holder, assigned, takenFrom, dealt, delivered, started,
                 finishReceipt, busNote, returnReceipt, dealAge, deliveryAge,
                 startAge, runAge, progressAge, goneAge, finishAge, returnAge,
                 runLive, alarms, disruptions>>

(* The tick ages evidence, alarms on expiry, reclaims stale cards and finishes
   a dead lane within DeadRunBound. *)
NextDaemonAge(f) == IF daemonLive[f] THEN Age(daemonAge[f]) ELSE Bound
NextHeardAge(f) == Age(heardAge[f])
DownAfterTick(f) == held[f] \/ creditOut[f] \/
                    NextDaemonAge(f) >= Bound \/ NextHeardAge(f) >= Bound
TimedOut(c) ==
  \/ cardState[c] = "dealt" /\ Age(dealAge[c]) >= DeliveryBound
  \/ cardState[c] = "delivered" /\ Age(deliveryAge[c]) >= StartBound
  \/ cardState[c] = "started" /\ runLive[c] /\
       Age(progressAge[c]) >= ProgressBound
DeadFinishes(c) ==
  cardState[c] = "started" /\ ~runLive[c] /\ Age(goneAge[c]) >= DeadRunBound
WillTakeBack(c) ==
  Active(c) /\ holder[c] \in Friends /\
  (DownAfterTick(holder[c]) \/ TimedOut(c))

Tick ==
  /\ daemonAge' = [f \in Friends |-> NextDaemonAge(f)]
  /\ heardAge' = [f \in Friends |-> NextHeardAge(f)]
  /\ dealAge' = [c \in Cards |-> Age(dealAge[c])]
  /\ deliveryAge' = [c \in Cards |-> Age(deliveryAge[c])]
  /\ startAge' = [c \in Cards |-> Age(startAge[c])]
  /\ runAge' = [c \in Cards |-> Age(runAge[c])]
  /\ progressAge' = [c \in Cards |-> Age(progressAge[c])]
  /\ goneAge' = [c \in Cards |-> Age(goneAge[c])]
  /\ finishAge' = [c \in Cards |-> IF DeadFinishes(c) THEN 0 ELSE Age(finishAge[c])]
  /\ returnAge' = [c \in Cards |-> Age(returnAge[c])]
  /\ cardState' = [c \in Cards |->
       IF DeadFinishes(c) THEN "finished"
       ELSE IF WillTakeBack(c) THEN "takenback"
       ELSE cardState[c]]
  /\ holder' = [c \in Cards |->
       IF DeadFinishes(c) THEN Review
       ELSE IF WillTakeBack(c) THEN Pool
       ELSE holder[c]]
  /\ takenFrom' = [c \in Cards |->
       IF WillTakeBack(c) THEN holder[c] ELSE takenFrom[c]]
  /\ finishReceipt' = [c \in Cards |-> finishReceipt[c] \/ DeadFinishes(c)]
  /\ busNote' = [c \in Cards |-> busNote[c] \/ DeadFinishes(c)]
  /\ runLive' = [c \in Cards |-> IF DeadFinishes(c) \/ WillTakeBack(c)
                                     THEN FALSE ELSE runLive[c]]
  /\ alarms' = [f \in Friends |-> alarms[f] \cup TickAlarms(f)]
  /\ UNCHANGED <<pingOwed, archived, creditOut, held, assigned, dealt,
                  delivered, started, returnReceipt, disruptions>>

AlarmLayer(f) ==
  IF held[f] THEN "none"
  ELSE IF ~daemonLive[f] \/ daemonAge[f] >= Bound THEN "daemon"
  ELSE IF heardAge[f] >= Bound \/ creditOut[f] THEN "hears"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "dealt" /\
                                "delivered" \in alarms[f] THEN "delivered"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "dealt" /\
                                dealAge[c] >= DeliveryBound THEN "delivered"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "delivered" /\
                                deliveryAge[c] >= StartBound THEN "started"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "started" /\
                                runLive[c] /\
                                progressAge[c] >= ProgressBound
       THEN "progressing"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "started" /\
                                ~runLive[c] /\ goneAge[c] >= DeadRunBound
       THEN "finished"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "finished" /\
                                finishAge[c] >= ReturnBound THEN "returned"
  ELSE "none"

TickAlarms(f) ==
  IF held[f] THEN {}
  ELSE IF ~daemonLive[f] \/ NextDaemonAge(f) >= Bound THEN {"daemon"}
  ELSE IF creditOut[f] \/ NextHeardAge(f) >= Bound THEN {"hears"}
  ELSE
    (IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "dealt" /\
                              Age(dealAge[c]) >= DeliveryBound
       THEN {"delivered"} ELSE {})
    \cup (IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "delivered" /\
                                  Age(deliveryAge[c]) >= StartBound
             THEN {"started"} ELSE {})
    \cup (IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "started" /\
                                  runLive[c] /\
                                  Age(progressAge[c]) >= ProgressBound
             THEN {"progressing"} ELSE {})
    \cup (IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "started" /\
                                  ~runLive[c] /\ Age(goneAge[c]) >= DeadRunBound
             THEN {"finished"} ELSE {})
    \cup (IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "finished" /\
                                  Age(finishAge[c]) >= ReturnBound
             THEN {"returned"} ELSE {})

Alarm(f) ==
  /\ AlarmLayer(f) # "none"
  /\ AlarmLayer(f) \notin alarms[f]
  /\ alarms' = [alarms EXCEPT ![f] = @ \cup {AlarmLayer(f)}]
  /\ UNCHANGED <<daemonLive, daemonAge, heardAge, pingOwed, archived,
                 creditOut, held, cardState, holder, assigned, takenFrom,
                 dealt, delivered, started, finishReceipt, busNote,
                 returnReceipt, dealAge, deliveryAge, startAge, runAge,
                 progressAge, goneAge, finishAge, returnAge, runLive,
                 disruptions>>

NextCore ==
  \/ Tick
  \/ \E f \in Friends :
       Beat(f) \/ Ping(f) \/ Answer(f) \/ Hold(f) \/ Release(f) \/
       SessionArchived(f) \/ SessionRestored(f) \/ CreditOut(f) \/
       CreditBack(f) \/ Reboot(f) \/ BringBackUp(f) \/ Alarm(f)
  \/ \E c \in Cards :
       FailDelivery(c) \/ Deliver(c) \/ Start(c) \/ Progress(c) \/ Die(c) \/
       Finish(c) \/ Return(c) \/ TakeBack(c)
  \/ \E c \in Cards, f \in Friends : Deal(c, f) \/ Level(c, f)

Next ==
  /\ NextCore
  /\ shown' = DashboardMap'

Spec == Init /\ [][Next]_vars

(* Disruptions are finite (MaxEvents). Recoveries and the clock are fair.     *)
SpecLive ==
  /\ Spec
  /\ WF_vars(Tick)
  /\ \A f \in Friends :
       WF_vars(Beat(f)) /\ WF_vars(Ping(f)) /\ WF_vars(Answer(f)) /\
       WF_vars(BringBackUp(f)) /\ WF_vars(Release(f)) /\
       WF_vars(SessionRestored(f)) /\ WF_vars(CreditBack(f))
  /\ \A c \in Cards :
       SF_vars(\E f \in Friends : Deal(c, f)) /\
       WF_vars(\E f \in Friends : Level(c, f)) /\
       WF_vars(Deliver(c)) /\ WF_vars(Start(c)) /\ WF_vars(Progress(c)) /\
       WF_vars(Finish(c)) /\ WF_vars(Return(c)) /\ WF_vars(TakeBack(c))

TypeOK ==
  /\ daemonLive \in [Friends -> BOOLEAN]
  /\ daemonAge \in [Friends -> 0..Bound]
  /\ heardAge \in [Friends -> 0..Bound]
  /\ pingOwed \in [Friends -> BOOLEAN]
  /\ archived \in [Friends -> BOOLEAN]
  /\ creditOut \in [Friends -> BOOLEAN]
  /\ held \in [Friends -> BOOLEAN]
  /\ cardState \in [Cards -> CardStates]
  /\ holder \in [Cards -> Friends \cup {Pool, Review, "coordinator"}]
  /\ assigned \in [Cards -> Friends \cup {NoFriend}]
  /\ takenFrom \in [Cards -> Friends \cup {NoFriend}]
  /\ dealt \in [Cards -> BOOLEAN]
  /\ delivered \in [Cards -> BOOLEAN]
  /\ started \in [Cards -> BOOLEAN]
  /\ finishReceipt \in [Cards -> BOOLEAN]
  /\ busNote \in [Cards -> BOOLEAN]
  /\ returnReceipt \in [Cards -> BOOLEAN]
  /\ dealAge \in [Cards -> 0..Bound]
  /\ deliveryAge \in [Cards -> 0..Bound]
  /\ startAge \in [Cards -> 0..Bound]
  /\ runAge \in [Cards -> 0..Bound]
  /\ progressAge \in [Cards -> 0..Bound]
  /\ goneAge \in [Cards -> 0..Bound]
  /\ finishAge \in [Cards -> 0..Bound]
  /\ returnAge \in [Cards -> 0..Bound]
  /\ runLive \in [Cards -> BOOLEAN]
  /\ alarms \in [Friends -> SUBSET AlarmLayers]
  /\ disruptions \in 0..MaxEvents
  /\ shown \in [Friends -> {"held", "daemon", "up", "hears", "delivered", "started",
                              "progressing", "working", "ready", "returned"}]

WorkingOnlyAfterStart ==
  \A c \in Cards : cardState[c] = "started" => started[c]

FinishedHasEvidenceAndNotice ==
  \A c \in Cards : cardState[c] \in {"finished", "returned"} =>
       finishReceipt[c] /\ busNote[c]

NoCardOffUp ==
  \A c \in Cards : Active(c) /\ holder[c] \in Friends => Up(holder[c])

TierHeld ==
  \A c \in Cards : Active(c) /\ holder[c] \in Friends =>
       CardTier[c] \in FriendTiers[holder[c]]

WidthRespected == \A f \in Friends : Load(f) <= Width[f]

DeliveryFailureVisible ==
  \A f \in Friends : ~held[f] /\ Up(f) /\
       (\E c \in Cards : assigned[c] = f /\ cardState[c] = "dealt" /\
                           "delivered" \in alarms[f]) => shown[f] = "delivered"

DeadRunFinishedWithinBound ==
  \A c \in Cards : cardState[c] = "started" /\ ~runLive[c] =>
       goneAge[c] < DeadRunBound

Dashboard(f) ==
  IF held[f] THEN "held"
  ELSE IF ~daemonLive[f] \/ daemonAge[f] >= Bound THEN "daemon"
  ELSE IF heardAge[f] >= Bound \/ creditOut[f] THEN "hears"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "dealt" /\
                                "delivered" \in alarms[f] THEN "delivered"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "dealt" /\
                                dealAge[c] >= DeliveryBound THEN "delivered"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "delivered" /\
                                deliveryAge[c] >= StartBound THEN "started"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "started" /\
                                runLive[c] /\
                                progressAge[c] >= ProgressBound
       THEN "progressing"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "started" /\ ~runLive[c]
       THEN "finished"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "finished" /\
                                finishAge[c] >= ReturnBound THEN "returned"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] = "started" THEN "working"
  ELSE IF \E c \in Cards : assigned[c] = f /\ cardState[c] \in {"dealt", "delivered"}
       THEN "ready"
  ELSE "up"

DashboardMap == [f \in Friends |-> Dashboard(f)]

DashboardIsDerived == shown = DashboardMap

EveryDealtSettles ==
  \A c \in Cards : dealt[c] ~> cardState[c] \in TerminalStates

EveryFinishedReturns ==
  \A c \in Cards : cardState[c] = "finished" ~> cardState[c] = "returned"

AbleToWork(f) == ~held[f] /\ ~archived[f] /\ ~creditOut[f]
EveryAbleFriendUp ==
  \A f \in Friends : AbleToWork(f) ~> Up(f)

=============================================================================
