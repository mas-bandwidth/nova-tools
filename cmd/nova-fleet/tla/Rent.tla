-------------------------------- MODULE Rent --------------------------------
(* nova-fleet rent and return (cmd/nova-fleet/rent.go) over one name: the record (config.rented, with the     *)
(* store's guards), the boxes and spot requests at the provider, rent and return runs that may stop at any    *)
(* step, and what happens outside them: a box interrupted or deleted by hand, an open spot request launching   *)
(* its box, a hand remove of the record, and the ask growing older than the create window. Each action is a   *)
(* step rent.go names to rentDeps.step; RentTrace.tla checks the code's walks are behaviours of this module.   *)
(*                                                                                                           *)
(* The guarantee (NoUnseenBill): wherever runs stop, every box that runs and every spot request that lives is  *)
(* named by the record: its id, its request, or its ask (creating) while a create may be under way. One        *)
(* assumption, the create window: a rent that is alive goes from its add to its note within it (Expire).      *)
(*                                                                                                           *)
(* Reversed witnesses (Broken): NoAsk (the add holds no ask), NoBusyWait (return ignores a young ask),        *)
(* CreateBeforeRow, RemoveRowBeforeBox, CreateWithoutFind (a resumed rent creates again), OnlyRequest (the     *)
(* record removed while only its spot request lives), StoreNoAsk (the store lets a record holding an ask go),  *)
(* Persistent (a spot request that opens again), Fallback (a refused spot rent made on demand), NoCheck (a box *)
(* kept on the wrong hardware).                                                                               *)
EXTENDS Naturals, FiniteSets

CONSTANTS Rents, Returns, Boxes, Reqs, Spot, Broken

Procs == Rents \cup Returns
None == ""

VARIABLES rec, young, box, boxReq, hw, req, reqBox, pc, mine, seen, okBox

vars == <<rec, young, box, boxReq, hw, req, reqBox, pc, mine, seen, okBox>>
prov == <<box, boxReq, hw, req, reqBox>>

NoRec == [ex |-> FALSE, id |-> None, rq |-> None, ask |-> FALSE, gen |-> None]
NoMine == [gen |-> None, id |-> None, rq |-> None, ask |-> FALSE, added |-> FALSE, new |-> None, newRq |-> None]

TypeOK ==
  /\ rec \in [ex : BOOLEAN, id : Boxes \cup {None}, rq : Reqs \cup {None}, ask : BOOLEAN, gen : Rents \cup {None}]
  /\ young \in BOOLEAN
  /\ box \in [Boxes -> {"none", "run", "gone"}]
  /\ boxReq \in [Boxes -> Reqs \cup {None}]
  /\ hw \in [Boxes -> {"right", "wrong"}]
  /\ req \in [Reqs -> {"none", "live", "dead"}]
  /\ reqBox \in [Reqs -> Boxes \cup {None}]
  /\ okBox \subseteq Boxes

Init ==
  /\ rec = NoRec /\ young = FALSE
  /\ box = [b \in Boxes |-> "none"] /\ boxReq = [b \in Boxes |-> None] /\ hw = [b \in Boxes |-> "right"]
  /\ req = [q \in Reqs |-> "none"] /\ reqBox = [q \in Reqs |-> None]
  /\ pc = [p \in Procs |-> "start"] /\ mine = [p \in Procs |-> NoMine]
  /\ seen = [p \in Procs |-> [boxes |-> {}, reqs |-> {}]] /\ okBox = {}

Held(r) == r.id /= None \/ r.rq /= None \/ r.ask

(* The store's guards: a set gives or clears a handle, never replaces one (config.rented_fixed); a remove    *)
(* takes a record that holds nothing (config.rented_keep). A write to a record that is gone writes nothing.  *)
SetOK(p, new) ==
  /\ rec.ex
  /\ Held(rec) => rec.gen = mine[p].gen
  /\ rec.id = None \/ new.id \in {rec.id, None}
  /\ rec.rq = None \/ new.rq \in {rec.rq, None}
RemoveOK ==
  /\ rec.ex /\ rec.id = None
  /\ rec.rq = None \/ Broken = "OnlyRequest"
  /\ ~rec.ask \/ Broken = "StoreNoAsk"

Go(p, l) == pc' = [pc EXCEPT ![p] = l]
Fresh(S, st) == {x \in S : st[x] = "none"}
Quiet == UNCHANGED <<rec, young, box, boxReq, hw, req, reqBox, mine, seen, okBox>>
Read(p) == [NoMine EXCEPT !.gen = rec.gen, !.id = rec.id, !.rq = rec.rq, !.ask = rec.ask]

\* a box goes; a one-time request ends with it, a persistent one opens again
BoxGoes(b) ==
  /\ box' = [box EXCEPT ![b] = "gone"]
  /\ IF boxReq[b] /= None
     THEN IF Broken = "Persistent"
          THEN reqBox' = [reqBox EXCEPT ![boxReq[b]] = None] /\ UNCHANGED req
          ELSE req' = [req EXCEPT ![boxReq[b]] = "dead"] /\ UNCHANGED reqBox
     ELSE UNCHANGED <<req, reqBox>>
  /\ UNCHANGED <<boxReq, hw>>

-----------------------------------------------------------------------------
(* rent *)

RAdd(p) ==
  /\ p \in Rents /\ pc[p] = "start" /\ ~rec.ex /\ Broken /= "CreateBeforeRow"
  /\ rec' = [ex |-> TRUE, id |-> None, rq |-> None, ask |-> Broken /= "NoAsk", gen |-> p]
  /\ young' = (Broken /= "NoAsk")
  /\ mine' = [mine EXCEPT ![p] = [NoMine EXCEPT !.gen = p, !.added = TRUE, !.ask = (Broken /= "NoAsk")]]
  /\ Go(p, "find") /\ UNCHANGED <<box, boxReq, hw, req, reqBox, seen, okBox>>

\* CreateBeforeRow: a rent that asks the provider first and writes the record with the ids
RSkipAdd(p) ==
  /\ Broken = "CreateBeforeRow" /\ p \in Rents /\ pc[p] = "start" /\ ~rec.ex
  /\ mine' = [mine EXCEPT ![p] = [NoMine EXCEPT !.gen = p, !.added = TRUE]]
  /\ Go(p, "find") /\ UNCHANGED <<rec, young, box, boxReq, hw, req, reqBox, seen, okBox>>

RBusy(p) == p \in Rents /\ pc[p] = "start" /\ rec.ex /\ young /\ Go(p, "done") /\ Quiet

RHave(p) ==
  /\ p \in Rents /\ pc[p] = "start" /\ rec.ex /\ ~young
  /\ mine' = [mine EXCEPT ![p] = Read(p)]
  /\ Go(p, "find") /\ UNCHANGED <<rec, young, box, boxReq, hw, req, reqBox, seen, okBox>>

Find(p, l) ==
  /\ seen' = [seen EXCEPT ![p] = [boxes |-> {b \in Boxes : box[b] = "run"}, reqs |-> {q \in Reqs : req[q] = "live"}]]
  /\ Go(p, l) /\ UNCHANGED <<rec, young, box, boxReq, hw, req, reqBox, mine, okBox>>

RFind(p) ==
  /\ p \in Rents /\ pc[p] = "find"
  /\ IF Broken = "CreateWithoutFind" /\ ~mine[p].added
     THEN seen' = [seen EXCEPT ![p] = [boxes |-> {}, reqs |-> {}]] /\ Go(p, "decide")
          /\ UNCHANGED <<rec, young, box, boxReq, hw, req, reqBox, mine, okBox>>
     ELSE Find(p, "decide")

(* note: the ids the run does not hold yet into the record, and the ask it holds cleared in the same write *)
(* once it holds an id or a request (rentDeps.note); nothing to write is no write. A refused write, or one *)
(* to a record that is gone, ends the run.                                                                 *)
Note(p, b, q, l) ==
  LET setId == b /= None /\ mine[p].id = None
      setRq == q /= None /\ mine[p].rq = None
      id == IF setId THEN b ELSE mine[p].id
      rq == IF setRq THEN q ELSE mine[p].rq
      clr == mine[p].ask /\ (id /= None \/ rq /= None)
      new == [rec EXCEPT !.id = IF setId THEN b ELSE @, !.rq = IF setRq THEN q ELSE @, !.ask = IF clr THEN FALSE ELSE @, !.gen = mine[p].gen]
      m == [mine[p] EXCEPT !.id = id, !.rq = rq, !.ask = @ /\ ~clr]
  IN \/ /\ ~setId /\ ~setRq /\ ~clr
        /\ Go(p, l) /\ UNCHANGED <<rec, young, mine>>
     \/ /\ Broken = "CreateBeforeRow" /\ ~rec.ex /\ (setId \/ setRq)
        /\ rec' = [ex |-> TRUE, id |-> id, rq |-> rq, ask |-> FALSE, gen |-> p] /\ young' = young
        /\ mine' = [mine EXCEPT ![p] = m] /\ Go(p, l)
     \/ /\ (setId \/ setRq \/ clr) /\ rec.ex /\ SetOK(p, new)
        /\ rec' = new /\ young' = (young /\ new.ask) /\ mine' = [mine EXCEPT ![p] = m] /\ Go(p, l)
     \/ /\ (setId \/ setRq \/ clr) /\ ~(rec.ex /\ SetOK(p, new)) /\ ~(Broken = "CreateBeforeRow" /\ ~rec.ex)
        /\ Go(p, "done") /\ UNCHANGED <<rec, young, mine>>

Decide(p) == p \in Rents /\ pc[p] = "decide"
SB(p) == seen[p].boxes
SQ(p) == seen[p].reqs
Few(p) == Cardinality(SB(p)) <= 1 /\ Cardinality(SQ(p)) <= 1

RMany(p) == Decide(p) /\ ~Few(p) /\ Go(p, "done") /\ Quiet

RTwo(p) ==
  /\ Decide(p) /\ Few(p)
  /\ \/ \E b \in SB(p) : mine[p].id \notin {None, b}
     \/ SB(p) = {} /\ \E q \in SQ(p) : mine[p].rq \notin {None, q}
  /\ Go(p, "done") /\ Quiet

RAdoptBox(p) ==
  /\ Decide(p) /\ Few(p)
  /\ \E b \in SB(p) : mine[p].id = None /\ Note(p, b, boxReq[b], "wait")
  /\ UNCHANGED <<box, boxReq, hw, req, reqBox, seen, okBox>>

RAdoptReq(p) ==
  /\ Decide(p) /\ Few(p) /\ SB(p) = {}
  /\ \E q \in SQ(p) : mine[p].rq = None /\ Note(p, None, q, "wait")
  /\ UNCHANGED <<box, boxReq, hw, req, reqBox, seen, okBox>>

Nothing(p) == Decide(p) /\ SB(p) = {} /\ SQ(p) = {}

RGone(p) == Nothing(p) /\ mine[p].id /= None /\ Go(p, "done") /\ Quiet

\* only the run whose add won the name asks the provider
RNoBox(p) == Nothing(p) /\ mine[p].id = None /\ ~mine[p].added /\ Broken /= "CreateWithoutFind" /\ Go(p, "done") /\ Quiet

RFound(p) ==
  /\ Decide(p) /\ Few(p)
  /\ \/ \E b \in SB(p) : mine[p].id = b
     \/ SB(p) = {} /\ \E q \in SQ(p) : mine[p].rq = q
  /\ Go(p, "wait") /\ Quiet

CanCreate(p) == Nothing(p) /\ mine[p].id = None /\ (mine[p].added \/ Broken = "CreateWithoutFind")

\* the provider makes the box, or for spot a one-time request with its box or still open
RCreate(p) ==
  /\ CanCreate(p)
  /\ \E b \in Fresh(Boxes, box), h \in {"right", "wrong"} :
       IF Spot
       THEN \E q \in Fresh(Reqs, req), now \in BOOLEAN :
              /\ req' = [req EXCEPT ![q] = "live"]
              /\ IF now
                 THEN /\ box' = [box EXCEPT ![b] = "run"] /\ hw' = [hw EXCEPT ![b] = h]
                      /\ reqBox' = [reqBox EXCEPT ![q] = b] /\ boxReq' = [boxReq EXCEPT ![b] = q]
                      /\ mine' = [mine EXCEPT ![p].new = b, ![p].newRq = q]
                 ELSE UNCHANGED <<box, hw, reqBox, boxReq>> /\ mine' = [mine EXCEPT ![p].newRq = q]
       ELSE /\ box' = [box EXCEPT ![b] = "run"] /\ hw' = [hw EXCEPT ![b] = h]
            /\ UNCHANGED <<req, reqBox, boxReq>> /\ mine' = [mine EXCEPT ![p].new = b]
  /\ Go(p, "note") /\ UNCHANGED <<rec, young, seen, okBox>>

\* refused: the ask is cleared and the record stays with no box (a refused spot rent is never made on demand)
RRefused(p) ==
  /\ CanCreate(p) /\ ~(Broken = "Fallback" /\ Spot) /\ rec.ex /\ SetOK(p, [rec EXCEPT !.ask = FALSE])
  /\ rec' = [rec EXCEPT !.ask = FALSE, !.gen = mine[p].gen] /\ young' = FALSE
  /\ Go(p, "done") /\ UNCHANGED <<box, boxReq, hw, req, reqBox, mine, seen, okBox>>

\* Fallback: a refused spot rent made on demand, a box with no request
RFallback(p) ==
  /\ Broken = "Fallback" /\ Spot /\ CanCreate(p)
  /\ \E b \in Fresh(Boxes, box) : box' = [box EXCEPT ![b] = "run"] /\ mine' = [mine EXCEPT ![p].new = b]
  /\ Go(p, "note") /\ UNCHANGED <<rec, young, boxReq, hw, req, reqBox, seen, okBox>>

RNote(p) ==
  /\ p \in Rents /\ pc[p] = "note"
  /\ Note(p, mine[p].new, mine[p].newRq, "wait") /\ UNCHANGED <<box, boxReq, hw, req, reqBox, seen, okBox>>

Wait(p) == p \in Rents /\ pc[p] = "wait"

\* a request's box came: noted
RWaitBox(p) ==
  /\ Wait(p) /\ mine[p].id = None /\ mine[p].rq /= None
  /\ \E b \in Boxes : box[b] = "run" /\ Note(p, b, None, "wait")
  /\ UNCHANGED <<box, boxReq, hw, req, reqBox, seen, okBox>>

RWaitDead(p) ==
  /\ Wait(p) /\ mine[p].id = None /\ mine[p].rq /= None /\ req[mine[p].rq] /= "live"
  /\ \A b \in Boxes : box[b] /= "run"
  /\ Go(p, "done") /\ Quiet

RCheckGone(p) == Wait(p) /\ mine[p].id /= None /\ box[mine[p].id] = "gone" /\ Go(p, "done") /\ Quiet

RCheckOK(p) ==
  /\ Wait(p) /\ mine[p].id /= None /\ box[mine[p].id] = "run"
  /\ hw[mine[p].id] = "right" \/ Broken = "NoCheck"
  /\ okBox' = okBox \cup {mine[p].id}
  /\ Go(p, "done") /\ UNCHANGED <<rec, young, box, boxReq, hw, req, reqBox, mine, seen>>

\* the wrong hardware: the rent gives it back (give, below) and refuses
RCheckBad(p) ==
  /\ Wait(p) /\ mine[p].id /= None /\ box[mine[p].id] = "run" /\ hw[mine[p].id] = "wrong" /\ Broken /= "NoCheck"
  /\ Go(p, "gfind") /\ Quiet

RRefusedHW(p) == p \in Rents /\ pc[p] = "given" /\ Go(p, "done") /\ Quiet

-----------------------------------------------------------------------------
(* return, and give: the box half of return, also run by a rent that found the wrong hardware. Each step   *)
(* that writes is a label; a step with nothing to write passes to the next.                                *)

XNone(p) == p \in Returns /\ pc[p] = "start" /\ ~rec.ex /\ Go(p, "done") /\ Quiet

XBusy(p) == p \in Returns /\ pc[p] = "start" /\ rec.ex /\ young /\ Broken /= "NoBusyWait" /\ Go(p, "done") /\ Quiet

XRead(p) ==
  /\ p \in Returns /\ pc[p] = "start" /\ rec.ex /\ (~young \/ Broken = "NoBusyWait")
  /\ mine' = [mine EXCEPT ![p] = Read(p)]
  /\ Go(p, IF Broken = "RemoveRowBeforeBox" THEN "gdrop" ELSE "gfind")
  /\ UNCHANGED <<rec, young, box, boxReq, hw, req, reqBox, seen, okBox>>

XFind(p) == pc[p] = "gfind" /\ Find(p, "gcancel")

ToCancel(p) == IF Broken = "OnlyRequest" THEN {} ELSE {q \in SQ(p) \cup ({mine[p].rq} \ {None}) : req[q] = "live"}
ToRemove(p) == {b \in SB(p) \cup ({mine[p].id} \ {None}) : box[b] = "run"}
ClearRq(p) == mine[p].rq /= None /\ Broken /= "OnlyRequest"
ClearBox(p) == mine[p].id /= None \/ mine[p].ask \/ ClearRq(p)
AtRemove(p) == pc[p] = "gremove"
AtClearBox(p) == AtRemove(p) /\ ToRemove(p) = {}
AtDrop(p) == pc[p] = "gdrop" \/ (AtClearBox(p) /\ ~ClearBox(p))
Given(p) == IF p \in Rents THEN "given" ELSE "done"

XCancel(p) ==
  /\ pc[p] = "gcancel"
  /\ \E q \in ToCancel(p) : req' = [req EXCEPT ![q] = "dead"]
  /\ UNCHANGED <<rec, young, box, boxReq, hw, reqBox, pc, mine, seen, okBox>>

\* looked for again once every request is dead: an open one may have launched its box after the first look
XRefind(p) ==
  /\ pc[p] = "gcancel" /\ ToCancel(p) = {}
  /\ seen' = [seen EXCEPT ![p].boxes = {b \in Boxes : box[b] = "run"}]
  /\ Go(p, "gremove") /\ UNCHANGED <<rec, young, box, boxReq, hw, req, reqBox, mine, okBox>>

XRemove(p) ==
  /\ AtRemove(p)
  /\ \E b \in ToRemove(p) : BoxGoes(b)
  /\ Go(p, "gremove") /\ UNCHANGED <<rec, young, mine, seen, okBox>>

\* once every request is dead and every box gone, one write clears what was read at the start: an id, a
\* request or an ask written since names what this run did not see
XClearBox(p) ==
  /\ AtClearBox(p) /\ ClearBox(p)
  /\ LET new == [rec EXCEPT !.id = IF mine[p].id /= None THEN None ELSE @,
                            !.rq = IF ClearRq(p) THEN None ELSE @,
                            !.ask = IF mine[p].ask THEN FALSE ELSE @, !.gen = mine[p].gen]
     IN IF SetOK(p, new)
        THEN rec' = new /\ young' = (young /\ new.ask)
             /\ mine' = [mine EXCEPT ![p].id = None, ![p].rq = None, ![p].ask = FALSE]
             /\ Go(p, "gdrop")
        ELSE UNCHANGED <<rec, young, mine>> /\ Go(p, "done")
  /\ UNCHANGED <<box, boxReq, hw, req, reqBox, seen, okBox>>

XDrop(p) ==
  /\ AtDrop(p)
  /\ \/ /\ RemoveOK \/ (Broken = "RemoveRowBeforeBox" /\ p \in Returns /\ pc[p] = "gdrop" /\ rec.ex)
        /\ rec' = NoRec /\ young' = FALSE
        /\ Go(p, IF Broken = "RemoveRowBeforeBox" /\ p \in Returns /\ pc[p] = "gdrop" THEN "gfind" ELSE Given(p))
     \/ /\ ~RemoveOK /\ ~(Broken = "RemoveRowBeforeBox" /\ p \in Returns /\ pc[p] = "gdrop" /\ rec.ex)
        /\ Go(p, "done") /\ UNCHANGED <<rec, young>>
  /\ UNCHANGED <<box, boxReq, hw, req, reqBox, mine, seen, okBox>>

-----------------------------------------------------------------------------
(* outside the runs *)

\* a spot interruption (terminate) or a delete by hand
Interrupt == \E b \in Boxes : box[b] = "run" /\ BoxGoes(b) /\ UNCHANGED <<rec, young, pc, mine, seen, okBox>>

\* an open request is fulfilled: its box launches
Launch ==
  \E q \in Reqs, b \in Fresh(Boxes, box), h \in {"right", "wrong"} :
    /\ req[q] = "live" /\ reqBox[q] = None
    /\ box' = [box EXCEPT ![b] = "run"] /\ hw' = [hw EXCEPT ![b] = h]
    /\ boxReq' = [boxReq EXCEPT ![b] = q] /\ reqBox' = [reqBox EXCEPT ![q] = b]
    /\ UNCHANGED <<rec, young, req, pc, mine, seen, okBox>>

\* nova-config rented remove by hand: the store's guard decides
HandRemove == RemoveOK /\ rec' = NoRec /\ young' = FALSE /\ UNCHANGED <<box, boxReq, hw, req, reqBox, pc, mine, seen, okBox>>

\* the ask passes the create window; a live rent between its add and its note is within it (the assumption)
Expire ==
  /\ young /\ \A p \in Rents : ~(mine[p].added /\ pc[p] \in {"find", "decide", "note"})
  /\ young' = FALSE /\ UNCHANGED <<rec, box, boxReq, hw, req, reqBox, pc, mine, seen, okBox>>

\* a run stops at any step (killed, a failed call): what it held in memory is lost
Stop(p) == pc[p] \notin {"start", "done"} /\ Go(p, "done") /\ Quiet

-----------------------------------------------------------------------------

RentStep(p) ==
  \/ RAdd(p) \/ RSkipAdd(p) \/ RBusy(p) \/ RHave(p) \/ RFind(p) \/ RMany(p) \/ RTwo(p) \/ RAdoptBox(p)
  \/ RAdoptReq(p) \/ RGone(p) \/ RNoBox(p) \/ RFound(p) \/ RCreate(p) \/ RRefused(p) \/ RFallback(p)
  \/ RNote(p) \/ RWaitBox(p) \/ RWaitDead(p) \/ RCheckGone(p) \/ RCheckOK(p) \/ RCheckBad(p) \/ RRefusedHW(p)

GiveStep(p) == XFind(p) \/ XRefind(p) \/ XCancel(p) \/ XRemove(p) \/ XClearBox(p) \/ XDrop(p)

ReturnStep(p) == XNone(p) \/ XBusy(p) \/ XRead(p)

Next ==
  \/ \E p \in Procs : RentStep(p) \/ GiveStep(p) \/ ReturnStep(p) \/ Stop(p)
  \/ Interrupt \/ (Spot /\ Launch) \/ HandRemove \/ Expire

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------

(* Every box that runs and every spot request that lives is named by the record: its id, its request, or    *)
(* its ask while a create may be under way. A box billing with nothing naming it is what this forbids.      *)
NoUnseenBill ==
  /\ \A b \in Boxes : box[b] = "run" =>
       rec.ex /\ (rec.id = b \/ rec.ask \/ (boxReq[b] /= None /\ rec.rq = boxReq[b]))
  /\ \A q \in Reqs : req[q] = "live" =>
       rec.ex /\ (rec.rq = q \/ rec.ask \/ (reqBox[q] /= None /\ rec.id = reqBox[q]))

\* never a second box beside the one rented
AtMostOneBox == Cardinality({b \in Boxes : box[b] = "run"}) <= 1

\* a box reported rented is on the hardware asked for
OkIsRight == \A b \in okBox : hw[b] = "right"

\* a spot rent is a spot box or nothing
SpotIsSpot == Spot => \A b \in Boxes : box[b] /= "none" => boxReq[b] /= None
=============================================================================
