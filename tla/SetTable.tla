------------------------------- MODULE SetTable -------------------------------
\* Layer 1 of the sprint foundation, the table (tset/1), as a state machine,
\* written from its contract, design/L1-CONTRACT.md, contract revision 4 (the
\* published Layer 1 revision-4 pin, sha256 c4ba033f147fd925bb4628938d4156bf
\* 2286815f3422bacff6cd1c825baddbf1). One atomic step over several tables: a
\* caller reads, plans a request from what it read, and the store applies it
\* whole or refuses it whole. Guards are per member and per cell, evaluated
\* against the state at apply time. One place per member. Receipts keyed by
\* (request epoch, op), matched by the digest of the caller's intent, carrying
\* a status (ok, or fenced: settled as not applied). Epochs and advance, with
\* rowset guards before the advance, evaluated at the request epoch. The
\* explicit fence, and the caller's rule for an identity whose outcome is
\* unknown: only ok, an ok replay, STALE, OPCONFLICT or fenced settles it.
\*
\* The variables are the state Layer 1 owns (records, cells, rows, the active
\* epoch, the done receipts) and the in-flight state of each caller (its
\* program, where it is, what it read, the request it built, the reply it
\* has, its retries, requests it left in flight, whether the current part's
\* identity is unresolved, its op identities, how its next plan is made). The
\* actions are the operations (Read, Plan, Fence, the store's Answer) and the
\* outside events (LoseReply, Timeout, LateRun, Crash, Beat, a refused fence
\* the model does not compute, the other caller's steps).
\*
\* The store of the CONTRACT never half-applies: a step's guards are all
\* checked, then every write is made, in one action. Broken names a reversed
\* witness that changes exactly one rule (see the README); "none" is the
\* contract.
\*
\* NOT modelled: sizes and budgets, time and the 25 ms gate, Redis itself
\* (types, ACL, argv splitting, runtime errors: the contract's prepare is
\* ASSUMED complete here; a fence refused ENGINE, CONFIG, NOPERM or WRONGTYPE
\* is one outside event, FAULT), scores and row ranks (one application field stands for every
\* effective change; a rowset compares row names only), notes, reads other
\* than the planning snapshot and the done query, the log and history
\* (Layer 2), the Go twin, preplans, op-less caller steps (only the outside
\* writer's beat is op-less), TWICE and every static request check but two (a
\* move or remove with no source cell, and the rowset and fence shapes).
EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS
  Tables, TableSeq,  \* table names (the catalog), and the same as a sequence
  Rows, Cols,        \* row and column names of every table
  Members,           \* stored IDs
  Callers,           \* callers that run programs
  Vals,              \* values of the one application field
  MaxEpoch,          \* epochs are 0..MaxEpoch
  MaxRetries,        \* [Callers -> Nat]: resends, fences, done queries and replans, in all
  MaxCrashes,        \* caller crashes, in all
  MaxLate,           \* callers that may have a request left in flight at once
  MaxBeats,          \* outside writer steps, in all
  MaxReop,           \* new op identities a caller may take for one part after it
                     \* was settled fenced (the bounded family of op identities)
  MaxFaults,         \* fences refused with a code the model does not compute
                     \* (NOPERM, WRONGTYPE, ENGINE, CONFIG), in all
  BeatSet,           \* the outside writer's moves: <<table, member, cell, value>>
  InitRows,          \* [Tables -> SUBSET Rows]: the rows at epoch 0
  InitRecs,          \* [Tables -> [Members -> record]]: the records at epoch 0
  Menu,              \* [Callers -> SUBSET Seq(part)]: the programs a caller may run
  Ops,               \* every op identifier a program names
  DirectReplan,      \* TRUE: while an identity is unresolved a caller may replan
                     \* it from a fresh read without asking done first (FALSE:
                     \* the contract's path: the same bytes again, a fence, or
                     \* done first and then a plan under the same identity)
  Broken             \* "none", or the reversed witness: see the README

VARIABLES
  ep, rows, cells, rec, done,      \* the store
  hist, last,                      \* ghosts: applications so far; the last store step
  prog, pc, part, oe, snap, req, rep, tries, crashes, late, beats,
  unres, reop, via, faults

store == <<ep, rows, cells, rec, done>>
vars == <<ep, rows, cells, rec, done, hist, last,
          prog, pc, part, oe, snap, req, rep, tries, crashes, late, beats,
          unres, reop, via, faults>>

-----------------------------------------------------------------------------
\* Values.

Epochs == 0..MaxEpoch
NoEpoch == MaxEpoch + 1                 \* a verb that has not fixed its epoch
Cells == Rows \X Cols                   \* <<row, col>>
NoPlace == <<"-", "-">>
OpIds == Ops \X (0..MaxReop)            \* op identities: <<op, k>>, k-th new op of a part
NoOp == <<"none", 0>>                   \* the op of an op-less step (the beat)
Keys == Epochs \X OpIds                 \* receipt identities (request epoch, op)
Range(s) == {s[i] : i \in DOMAIN s}
Min(S) == CHOOSE i \in S : \A j \in S : i <= j
Terminal == {"finished", "stale", "refused", "lost", "conflict"}

\* A record. rev 0 with ex FALSE is an absent record. A removed record keeps
\* ex TRUE and has no place (EXISTS refuses its create).
Absent == [ex |-> FALSE, ep |-> 0, rev |-> 0, pl |-> NoPlace, f |-> 0]

\* A program is a sequence of parts [op, a]; a is the part's semantic
\* arguments, one shape for every part:
\*   mv: moves and removes [t, m, to, f, rm] (to NoPlace: a stay, the
\*   member's own cell);  cr: creates [t, m, to, f, rm];
\*   rt, add, del: one rows entry on table rt;  cap: count guard "to is
\*   empty" on every move's destination;  clear: a rowset guard for every
\*   catalog table, advance, and restoring every row the caller read.
NoArgs == [mv |-> <<>>, cr |-> <<>>, rt |-> "-", add |-> {}, del |-> {},
           cap |-> FALSE, clear |-> FALSE]
\* The intent: the part's semantic arguments and the verb's original epoch.
\* It excludes everything observed (places, revisions, rows read).
NoIntent == [a |-> NoArgs, oe |-> 0]
NoRes == [eb |-> 0, ea |-> 0, ch |-> 0]
NoRcpt == [has |-> FALSE, dig |-> NoIntent, res |-> NoRes, st |-> "ok"]
\* how (a ghost, not part of the bytes): "plan", a plan the contract's path
\* makes (a part's first plan, a rebuild after a final refusal, or a plan
\* under the same identity made after done said absent); "direct", a replan
\* of an unresolved identity without asking done (DirectReplan only);
\* "fence". Resent bytes keep the how of their plan.
NoReq == [ep |-> 0, op |-> NoOp, intent |-> NoIntent, es |-> <<>>, pv |-> "-",
          fence |-> FALSE, how |-> "plan"]
NoRep == [out |-> "-", code |-> "-", res |-> NoRes, st |-> "-"]
NoLast == [by |-> "-", R |-> NoReq, out |-> "-", code |-> "-", res |-> NoRes, st |-> "-"]
NoSnap == [ep |-> 0, rec |-> [k \in {} |-> Absent], rows |-> [t \in Tables |-> {}]]

\* Entries, one shape. rev 0 means "no expected revision given" (revisions of
\* existing records start at 1). from holds the cell of a count guard; add
\* holds the rows of a rows entry or the complete named row set of a rowset.
E(k, t, m, from, to, f, rev, max, add, del, efrom) ==
  [k |-> k, t |-> t, m |-> m, from |-> from, to |-> to, f |-> f, rev |-> rev,
   max |-> max, add |-> add, del |-> del, efrom |-> efrom]
CreateE(t, m, to, f)          == E("create", t, m, NoPlace, to, f, 0, 0, {}, {}, 0)
MoveE(t, m, from, to, f, rev) == E("move", t, m, from, to, f, rev, 0, {}, {}, 0)
RemoveE(t, m, from, f, rev)   == E("remove", t, m, from, NoPlace, f, rev, 0, {}, {}, 0)
CountE(t, x, max)             == E("count", t, "-", x, NoPlace, 0, 0, max, {}, {}, 0)
RowsE(t, add, del)            == E("rows", t, "-", NoPlace, NoPlace, 0, 0, 0, add, del, 0)
RowsetE(t, rs)                == E("rowset", t, "-", NoPlace, NoPlace, 0, 0, 0, rs, {}, 0)
AdvE(e)                       == E("advance", "-", "-", NoPlace, NoPlace, 0, 0, 0, {}, {}, e)

-----------------------------------------------------------------------------
\* A request R = [ep, op, intent, es, pv, fence, how]: request epoch, op,
\* intent, entries in order, (witness W1 only) the guard verdict of plan time,
\* the original fence bit, and the ghost how.

Kinds(R, k) == {i \in 1..Len(R.es) : R.es[i].k = k}
HasAdv(R) == Kinds(R, "advance") # {}
AdvFrom(R) == R.es[Min(Kinds(R, "advance"))].efrom
WEp(R) == IF HasAdv(R) THEN ep + 1 ELSE ep          \* the write epoch
Pre(R, t) == IF HasAdv(R) THEN {} ELSE rows[t][ep]  \* pre_rows at the write epoch
RowEs(R, t) == {R.es[i] : i \in {j \in Kinds(R, "rows") : R.es[j].t = t}}
RowsetEs(R) == {R.es[i] : i \in Kinds(R, "rowset")}
Adds(R, t) == UNION {e.add : e \in RowEs(R, t)}
Dels(R, t) == UNION {e.del : e \in RowEs(R, t)}
Prosp(R, t) == (Pre(R, t) \cup Adds(R, t)) \ Dels(R, t)
MemberEs(R) == {R.es[i] : i \in {j \in 1..Len(R.es) : R.es[j].k \in {"create", "move", "remove"}}}
EntryFor(R, t, m) == CHOOSE e \in MemberEs(R) : e.t = t /\ e.m = m
TablesOf(R) == {R.es[i].t : i \in 1..Len(R.es)} \ {"-"}
FirstTable(R) == R.es[Min({i \in 1..Len(R.es) : R.es[i].t # "-"})].t
Restrict(R, t) == [R EXCEPT !.es = SelectSeq(R.es, LAMBDA e : e.t = t)]
Dig(R) == <<R.ep, R.op, R.intent, R.es, R.fence>>   \* the request's bytes

\* Change means altered placement or application field (section 4).
Changed(e) ==
  CASE e.k = "create" -> TRUE
    [] e.k = "remove" -> TRUE
    [] e.k = "move"   -> e.to # rec[e.t][e.m].pl \/ e.f # rec[e.t][e.m].f

\* The members a step takes out of, and puts into, cell x of table t.
Out(R, t, x) == {e.m : e \in {y \in MemberEs(R) :
                   y.t = t /\ y.k \in {"move", "remove"} /\ y.from = x /\ Changed(y)}}
In(R, t, x) == {e.m : e \in {y \in MemberEs(R) :
                   y.t = t /\ y.k \in {"create", "move"} /\ y.to = x /\ Changed(y)}}

-----------------------------------------------------------------------------
\* The store's checks, in the contract's phase order (section 8): static
\* validity, the receipt, epoch and advance, the fence's own path, then the
\* rowset guards at the request epoch, topology, member and cell guards.

RdKey(R) == <<R.ep, R.op>>
WrKey(R) == IF Broken = "W4" THEN <<WEp(R), R.op>> ELSE <<R.ep, R.op>>
RcptDig(R) == IF Broken \in {"W3", "W3b"} THEN Dig(R) ELSE R.intent
RcptIntent(d) == IF Broken \in {"W3", "W3b"} THEN d.dig[3] ELSE d.dig
Found(R) ==
  /\ R.op # NoOp
  /\ done[RdKey(R)].has
  /\ (Broken = "W3b" => done[RdKey(R)].dig = Dig(R))
Matches(R) == done[RdKey(R)].dig = RcptDig(R)

\* Section 3: rowset entries form a prefix before the one advance, which is
\* the first non-rowset entry; at most one rowset per table; no rowset after
\* or without an advance. Row names are a set here, so duplicates cannot be
\* written; ranks are not modelled.
RowsetShapeOK(R) ==
  LET RS == Kinds(R, "rowset")  AD == Kinds(R, "advance") IN
  /\ Cardinality(AD) <= 1
  /\ RS # {} => AD # {}
  /\ \A j \in AD : \A i \in 1..(j - 1) : i \in RS
  /\ \A i \in RS : \A j \in AD : i < j
  /\ \A i, i2 \in RS : i # i2 => R.es[i].t # R.es[i2].t

\* Static validity comes before the receipt (section 8's phase order, and
\* section 3: "Static original request validity is still checked on
\* replay"). A fence carries no entries (section 5). A move or remove with no
\* source cell is what a plan from a read of an unplaced member has to offer
\* (from is a required cell reference; null is not accepted).
StaticCode(R) ==
  IF R.fence THEN (IF Len(R.es) = 0 THEN "ok" ELSE "REQUEST")
  ELSE IF \E i \in 1..Len(R.es) : R.es[i].k \in {"move", "remove"} /\ R.es[i].from = NoPlace
       THEN "REQUEST"
  ELSE IF ~RowsetShapeOK(R) THEN "REQUEST"
  ELSE "ok"

\* A fence with no receipt, at the active epoch, takes its own path: FENCE.
OpenCode(R) ==
  IF StaticCode(R) # "ok" THEN StaticCode(R)
  ELSE IF Found(R) THEN (IF Matches(R) THEN "REPLAY" ELSE "OPCONFLICT")
  ELSE IF R.ep > ep THEN "EPOCHAHEAD"
  ELSE IF R.ep < ep THEN "STALE"
  ELSE IF R.fence THEN "FENCE"
  ELSE IF HasAdv(R) /\ AdvFrom(R) # ep THEN "ADVANCE"
  ELSE IF HasAdv(R) /\ ep = MaxEpoch THEN "OVERFLOW"
  ELSE "ok"

\* The rowset guards, evaluated wholly at the request epoch before any
\* new-epoch planning (section 3). W7: they are not evaluated.
RowsetCode(R) ==
  IF Broken # "W7" /\ \E e \in RowsetEs(R) : rows[e.t][R.ep] # e.add THEN "ROWSET" ELSE "ok"

\* Final occupancy of a deleted row's cell: its members minus the members the
\* same step moves out or removes (section 3). A changed stay into a deleted
\* row is not counted here: its entry refuses ROWCONFLICT.
Left(R, t, x) == cells[t][WEp(R)][x] \ Out(R, t, x)
TopoCode(R) ==
  IF \E t \in Tables : Adds(R, t) \cap Dels(R, t) # {} THEN "ROWCONFLICT"
  ELSE IF \E t \in Tables : \E r \in Dels(R, t) \cap Pre(R, t) : \E c \in Cols :
            Left(R, t, <<r, c>>) # {} THEN "OCCUPIED"
  ELSE IF HasAdv(R) /\ (\E t \in Tables : rows[t][ep + 1] # {}
                                     \/ \E x \in Cells : cells[t][ep + 1][x] # {})
       THEN "DRIFT"
  ELSE "ok"

\* Where a create's or a move's destination must be.
Dest(R, t) == IF Broken = "W5" THEN Pre(R, t) \cup Adds(R, t) ELSE Prosp(R, t)

EntryCode(R, e) ==
  LET we == WEp(R) IN
  CASE e.k = "create" ->
         IF rec[e.t][e.m].ex THEN "EXISTS"
         ELSE IF Broken # "W5" /\ e.to[1] \in Dels(R, e.t) THEN "ROWCONFLICT"
         ELSE IF e.to[1] \notin Dest(R, e.t) THEN "NOROW"
         ELSE "ok"
    [] e.k \in {"move", "remove"} ->
         LET r == rec[e.t][e.m] IN
         IF ~r.ex THEN "MISSING"
         ELSE IF r.ep # we THEN "MEMBEREPOCH"
         ELSE IF Broken # "W6" /\ r.pl # e.from THEN "PLACE"
         ELSE IF e.from[1] \notin Pre(R, e.t) THEN "NOROW"
         ELSE IF Broken \notin {"W6", "W6cell"} /\ e.m \notin cells[e.t][we][e.from] THEN "DRIFT"
         ELSE IF e.rev # 0 /\ r.rev # e.rev THEN "REVISION"
         ELSE IF e.k = "move" /\ e.to[1] \in Dels(R, e.t) THEN "ROWCONFLICT"
         ELSE IF e.k = "move" /\ e.to[1] \notin Dest(R, e.t) THEN "NOROW"
         ELSE "ok"
    [] e.k = "count" ->
         IF e.from[1] \notin Pre(R, e.t) THEN "NOROW"
         ELSE IF Cardinality(cells[e.t][we][e.from]) > e.max THEN "CELLFULL"
         ELSE "ok"
    [] e.k \in {"rows", "advance", "rowset"} -> "ok"

Bad(R) == {i \in 1..Len(R.es) : EntryCode(R, R.es[i]) # "ok"}
MemberCode(R) == IF Bad(R) = {} THEN "ok" ELSE EntryCode(R, R.es[Min(Bad(R))])
GuardPhase(R) ==
  IF RowsetCode(R) # "ok" THEN RowsetCode(R)
  ELSE IF TopoCode(R) # "ok" THEN TopoCode(R)
  ELSE MemberCode(R)

\* The verdict: "REPLAY", "FENCE", "ok", or a refusal code. In W1 the guards
\* were checked once, at plan time, and the store trusts that verdict.
CheckCode(R) ==
  LET o == OpenCode(R) IN
  IF o # "ok" THEN o
  ELSE IF Broken = "W1" /\ R.pv # "-" THEN R.pv
  ELSE GuardPhase(R)
PlanVerdict(R) == IF OpenCode(R) = "ok" THEN GuardPhase(R) ELSE "ok"

-----------------------------------------------------------------------------
\* The writes of an accepted step, all of them, at its write epoch.

NewRec(R, e) ==
  LET r == rec[e.t][e.m] IN
  CASE e.k = "create" -> [ex |-> TRUE, ep |-> WEp(R), rev |-> 1, pl |-> e.to, f |-> e.f]
    [] e.k = "move"   -> IF Changed(e) THEN [r EXCEPT !.pl = e.to, !.f = e.f, !.rev = r.rev + 1]
                         ELSE r
    [] e.k = "remove" -> [r EXCEPT !.pl = NoPlace, !.f = e.f, !.rev = r.rev + 1]
Res(R) == [eb |-> ep, ea |-> WEp(R), ch |-> Cardinality({e \in MemberEs(R) : Changed(e)})]
FenceRes == [eb |-> ep, ea |-> ep, ch |-> 0]
NApplied(R) == Cardinality({h \in hist : h.ep = R.ep /\ h.op = R.op})

Writes(R, withRcpt) ==
  LET we == WEp(R) IN
  /\ ep' = we
  /\ rows' = [t \in Tables |-> [e \in Epochs |-> IF e = we THEN Prosp(R, t) ELSE rows[t][e]]]
  /\ cells' = [t \in Tables |-> [e \in Epochs |-> [x \in Cells |->
                 IF e = we THEN (cells[t][e][x] \ Out(R, t, x)) \cup In(R, t, x)
                 ELSE cells[t][e][x]]]]
  /\ rec' = [t \in Tables |-> [m \in Members |->
                 IF \E e \in MemberEs(R) : e.t = t /\ e.m = m
                 THEN NewRec(R, EntryFor(R, t, m)) ELSE rec[t][m]]]
  /\ done' = IF withRcpt /\ R.op # NoOp
             THEN [done EXCEPT ![WrKey(R)] = [has |-> TRUE, dig |-> RcptDig(R), res |-> Res(R),
                                              st |-> "ok"]]
             ELSE done

\* A winning fence writes only its receipt, status fenced (W8: status ok).
FenceWrites(R) ==
  /\ done' = [done EXCEPT ![WrKey(R)] = [has |-> TRUE, dig |-> RcptDig(R), res |-> FenceRes,
                                         st |-> IF Broken = "W8" THEN "ok" ELSE "fenced"]]
  /\ UNCHANGED <<ep, rows, cells, rec>>

\* The store executes R for who: a replay (with the saved status), the whole
\* step, a fence, or a refusal.
Exec(R, who) ==
  LET code == CheckCode(R) IN
  \/ /\ code = "REPLAY"
     /\ UNCHANGED <<store, hist>>
     /\ last' = [by |-> who, R |-> R, out |-> "replay", code |-> code,
                 res |-> done[RdKey(R)].res, st |-> done[RdKey(R)].st]
  \/ /\ code = "ok"
     /\ Writes(R, TRUE)
     /\ hist' = IF R.op = NoOp THEN hist
                ELSE hist \cup {[ep |-> R.ep, op |-> R.op, dig |-> R.intent,
                                 res |-> Res(R), n |-> NApplied(R) + 1]}
     /\ last' = [by |-> who, R |-> R, out |-> "apply", code |-> code, res |-> Res(R), st |-> "ok"]
  \/ /\ code = "FENCE"
     /\ FenceWrites(R)
     /\ UNCHANGED hist
     /\ last' = [by |-> who, R |-> R, out |-> "fenced", code |-> code, res |-> FenceRes,
                 st |-> "fenced"]
  \/ /\ code \notin {"REPLAY", "ok", "FENCE"}
     /\ UNCHANGED <<store, hist>>
     /\ last' = [by |-> who, R |-> R, out |-> "refused", code |-> code, res |-> NoRes, st |-> "-"]

\* W2 only: the plan accepted a step over two tables, and the commit stopped
\* after the first table's writes (a runtime error mid-commit): no receipt,
\* and the caller hears an error of unknown outcome.
ExecTorn(R, who) ==
  /\ Broken = "W2"
  /\ CheckCode(R) = "ok"
  /\ ~HasAdv(R)
  /\ Cardinality(TablesOf(R)) >= 2
  /\ Writes(Restrict(R, FirstTable(R)), FALSE)
  /\ hist' = IF R.op = NoOp THEN hist
             ELSE hist \cup {[ep |-> R.ep, op |-> R.op, dig |-> R.intent,
                              res |-> NoRes, n |-> NApplied(R) + 1]}
  /\ last' = [by |-> who, R |-> R, out |-> "error", code |-> "ERROR", res |-> NoRes, st |-> "-"]

-----------------------------------------------------------------------------
\* Callers. A caller runs one program: parts in order, each one atomic step
\* with its own op identity. The verb fixes its original epoch at its first
\* read and keeps it, with its part identities (and each part's new-op
\* count), across retries and crashes (the resume manifest). A retry under
\* the same identity is the same op and intent: the same bytes again, a
\* fence, or (after done said absent) a fresh read and a new plan.
\*
\* unres[c]: the current part's identity had a dispatched request whose
\* outcome is unknown, and nothing has settled it (section 5). While it is
\* set, a refusal is not final: the caller resends, fences, or asks done.

Cur(c) == prog[c][part[c]]
OpId(c, i) == <<prog[c][i].op, reop[c][i]>>
CurOp(c) == OpId(c, part[c])
PartIntent(c, i) == [a |-> prog[c][i].a, oe |-> oe[c]]
IntentOf(c) == PartIntent(c, part[c])
Named(P) == {<<x.t, x.m>> : x \in Range(P.a.mv)}
View(P) == [ep |-> ep,
            rec |-> [k \in Named(P) |-> rec[k[1]][k[2]]],
            rows |-> [t \in Tables |-> IF P.a.clear THEN rows[t][ep] ELSE {}]]

MvE(x, s, wr) ==
  LET r == s.rec[<<x.t, x.m>>]
      rv == IF wr THEN r.rev ELSE 0
      to == IF x.to = NoPlace THEN r.pl ELSE x.to    \* a stay: the member's own cell
  IN IF x.rm THEN RemoveE(x.t, x.m, r.pl, x.f, rv) ELSE MoveE(x.t, x.m, r.pl, to, x.f, rv)
Seqn(n, F(_)) == [i \in 1..n |-> F(i)]
\* A clear: one rowset per catalog table, including a table read empty (W7b
\* leaves those out), the advance, and the restoration of every row read.
PlanEs(I, s, wr) ==
  LET a == I.a IN
  (IF a.clear
   THEN SelectSeq(Seqn(Len(TableSeq), LAMBDA i : RowsetE(TableSeq[i], s.rows[TableSeq[i]])),
                  LAMBDA e : Broken # "W7b" \/ e.add # {})
        \o <<AdvE(I.oe)>>
        \o Seqn(Len(TableSeq), LAMBDA i : RowsE(TableSeq[i], s.rows[TableSeq[i]], {}))
   ELSE <<>>)
  \o (IF a.rt # "-" THEN <<RowsE(a.rt, a.add, a.del)>> ELSE <<>>)
  \o Seqn(Len(a.cr), LAMBDA i : CreateE(a.cr[i].t, a.cr[i].m, a.cr[i].to, a.cr[i].f))
  \o (IF a.cap THEN Seqn(Len(a.mv), LAMBDA i : CountE(a.mv[i].t, a.mv[i].to, 0)) ELSE <<>>)
  \o Seqn(Len(a.mv), LAMBDA i : MvE(a.mv[i], s, wr))
FenceReq(c) == [ep |-> oe[c], op |-> CurOp(c), intent |-> IntentOf(c), es |-> <<>>,
                pv |-> "-", fence |-> TRUE, how |-> "fence"]

Unsure(c) == pc[c] = "unknown" \/ (pc[c] = "replied" /\ rep[c].out = "error")
Refusal(c) == pc[c] = "replied" /\ rep[c].out = "refused" /\ rep[c].code \notin {"STALE", "OPCONFLICT"}
\* An unresolved identity with no settlement in hand.
Open(c) == Unsure(c) \/ (Refusal(c) /\ unres[c])
\* Settled as not applied: a fresh fence, or a replay or done of a fenced receipt.
NotApplied(c) == pc[c] = "replied" /\ (rep[c].out = "fenced" \/ (rep[c].out = "replay" /\ rep[c].st = "fenced"))

Read(c) ==
  /\ pc[c] = "start"
  /\ oe' = [oe EXCEPT ![c] = IF @ = NoEpoch THEN ep ELSE @]
  /\ snap' = [snap EXCEPT ![c] = View(Cur(c))]
  /\ pc' = [pc EXCEPT ![c] = "read"]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, req, rep, tries, crashes, late, beats, unres, reop, via, faults>>

\* The plan names expected revisions or not (revs are optional).
Plan(c) ==
  /\ pc[c] = "read"
  /\ \E wr \in BOOLEAN :
       LET R0 == [ep |-> oe[c], op |-> CurOp(c), intent |-> IntentOf(c),
                  es |-> PlanEs(IntentOf(c), snap[c], wr), pv |-> "-", fence |-> FALSE,
                  how |-> via[c]]
       IN req' = [req EXCEPT ![c] = IF Broken = "W1" THEN [R0 EXCEPT !.pv = PlanVerdict(R0)] ELSE R0]
  /\ snap' = [snap EXCEPT ![c] = NoSnap]
  /\ pc' = [pc EXCEPT ![c] = "planned"]
  /\ via' = [via EXCEPT ![c] = "plan"]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, rep, tries, crashes, late, beats, unres, reop, faults>>

\* The fence: the same original epoch, op and intent, no entries (section 5),
\* sent only for an unresolved identity: on an unknown outcome, on a refusal
\* that does not settle it, or after done said absent.
Fence(c) ==
  /\ unres[c] /\ oe[c] # NoEpoch
  /\ (Open(c) \/ pc[c] = "start")
  /\ tries[c] < MaxRetries[c]
  /\ req' = [req EXCEPT ![c] = FenceReq(c)]
  /\ pc' = [pc EXCEPT ![c] = "planned"]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ tries' = [tries EXCEPT ![c] = @ + 1]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, snap, crashes, late, beats, unres, reop, via, faults>>

Deliver(c) ==
  /\ pc' = [pc EXCEPT ![c] = "replied"]
  /\ rep' = [rep EXCEPT ![c] = [out |-> last'.out, code |-> last'.code, res |-> last'.res,
                                st |-> last'.st]]
  /\ UNCHANGED <<prog, part, oe, snap, req, tries, crashes, late, beats, reop, via>>

\* The store's side: the atomic step, a refusal, a replay or a fence; a fence
\* refused with a code the model does not compute; and a request a caller
\* left in flight, executed later with its reply going nowhere.
Answer(c) == /\ pc[c] = "planned" /\ Exec(req[c], c) /\ Deliver(c) /\ UNCHANGED <<unres, faults>>
Torn(c)   == /\ pc[c] = "planned" /\ ExecTorn(req[c], c) /\ Deliver(c)
             /\ unres' = [unres EXCEPT ![c] = TRUE] /\ UNCHANGED faults
\* FAULT stands for every refusal the model does not compute: ENGINE and
\* CONFIG, which section 8's phase order puts BEFORE the receipt lookup (so
\* they can answer a fence whose identity already has a receipt), and NOPERM
\* or WRONGTYPE from S.fence_prepare. Section 5: a fence refused with any code
\* but STALE or OPCONFLICT writes nothing and settles nothing.
FenceFault(c) ==
  /\ pc[c] = "planned" /\ req[c].fence
  /\ faults < MaxFaults
  /\ faults' = faults + 1
  /\ UNCHANGED <<store, hist, unres>>
  /\ last' = [by |-> c, R |-> req[c], out |-> "refused", code |-> "FAULT", res |-> NoRes, st |-> "-"]
  /\ Deliver(c)
LateRun(c) ==
  /\ \E R \in late[c] :
       /\ late' = [late EXCEPT ![c] = @ \ {R}]
       /\ Exec(R, c)
  /\ UNCHANGED <<prog, pc, part, oe, snap, req, rep, tries, crashes, beats, unres, reop, via, faults>>

\* The caller's side of a reply. ok, or an ok replay: the part is applied.
Accept(c) ==
  /\ pc[c] = "replied" /\ (rep[c].out = "apply" \/ (rep[c].out = "replay" /\ rep[c].st = "ok"))
  /\ IF part[c] = Len(prog[c])
     THEN pc' = [pc EXCEPT ![c] = "finished"] /\ part' = part
     ELSE pc' = [pc EXCEPT ![c] = "start"] /\ part' = [part EXCEPT ![c] = @ + 1]
  /\ req' = [req EXCEPT ![c] = NoReq]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ unres' = [unres EXCEPT ![c] = FALSE]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, oe, snap, tries, crashes, late, beats, reop, via, faults>>
Ends(c, why) ==
  /\ pc' = [pc EXCEPT ![c] = why]
  /\ req' = [req EXCEPT ![c] = NoReq]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, snap, tries, crashes, late, beats, unres, reop, via, faults>>
Stale(c) == pc[c] = "replied" /\ rep[c].code = "STALE" /\ Ends(c, "stale")
Conflict(c) == pc[c] = "replied" /\ rep[c].code = "OPCONFLICT" /\ Ends(c, "conflict")
\* A refusal is reported final only when nothing unresolved is behind it
\* (W9: always).
GiveUp(c) == Refusal(c) /\ (~unres[c] \/ Broken = "W9") /\ Ends(c, "refused")
\* Settled as not applied: report the part refused, or replan it under a
\* new op (section 5).
ReportRefused(c) == NotApplied(c) /\ Ends(c, "refused")
NewOp(c) ==
  /\ NotApplied(c)
  /\ reop[c][part[c]] < MaxReop
  /\ reop' = [reop EXCEPT ![c][part[c]] = @ + 1]
  /\ unres' = [unres EXCEPT ![c] = FALSE]
  /\ pc' = [pc EXCEPT ![c] = "start"]
  /\ req' = [req EXCEPT ![c] = NoReq]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, snap, tries, crashes, late, beats, via, faults>>
Again(c, how) ==
  /\ tries[c] < MaxRetries[c]
  /\ pc' = [pc EXCEPT ![c] = "start"]
  /\ req' = [req EXCEPT ![c] = NoReq]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ tries' = [tries EXCEPT ![c] = @ + 1]
  /\ via' = [via EXCEPT ![c] = how]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, snap, crashes, late, beats, unres, reop, faults>>
\* Re-read and rebuild after a refusal, under the same identity: after a
\* final refusal, or (DirectReplan only) while the identity is unresolved.
Rebuild(c) == Refusal(c) /\ (~unres[c] \/ DirectReplan) /\ Again(c, IF unres[c] THEN "direct" ELSE "plan")
RetryFresh(c) == DirectReplan /\ Unsure(c) /\ Again(c, "direct")
RetrySame(c) ==                               \* the same bytes again
  /\ Open(c)
  /\ req[c] # NoReq
  /\ tries[c] < MaxRetries[c]
  /\ pc' = [pc EXCEPT ![c] = "planned"]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ tries' = [tries EXCEPT ![c] = @ + 1]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, snap, req, crashes, late, beats, unres, reop, via, faults>>
\* Ask done for the manifest's identities without a crash (the resume path).
AskDone(c) ==
  /\ Open(c)
  /\ tries[c] < MaxRetries[c]
  /\ pc' = [pc EXCEPT ![c] = "resume"]
  /\ req' = [req EXCEPT ![c] = NoReq]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ tries' = [tries EXCEPT ![c] = @ + 1]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, snap, crashes, late, beats, unres, reop, via, faults>>
\* Retries used up with the identity unresolved: the caller keeps reporting
\* the outcome unknown.
LostCause(c) == Open(c) /\ tries[c] = MaxRetries[c] /\ Ends(c, "lost")

\* The resume path: done for every part identity of the manifest (each part
\* under its current op), then the first part that is not done with status
\* ok. Absent: a plan under the same identity (W10: a new op instead, even
\* while an earlier copy may be in flight); fenced: settled as not applied;
\* conflict: ended.
DoneState(c, i) ==
  LET d == done[<<oe[c], OpId(c, i)>>] IN
  IF ~d.has THEN "absent"
  ELSE IF RcptIntent(d) # PartIntent(c, i) THEN "conflict"
  ELSE d.st
Resume(c) ==
  /\ pc[c] = "resume"
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, oe, snap, req, tries, crashes, late, beats, faults>>
  /\ IF oe[c] = NoEpoch
     THEN /\ pc' = [pc EXCEPT ![c] = "start"] /\ part' = [part EXCEPT ![c] = 1]
          /\ unres' = [unres EXCEPT ![c] = FALSE]
          /\ UNCHANGED <<rep, reop, via>>
     ELSE LET open == {i \in 1..Len(prog[c]) : DoneState(c, i) # "ok"} IN
          IF open = {}
          THEN /\ pc' = [pc EXCEPT ![c] = "finished"]
               /\ UNCHANGED <<part, rep, unres, reop, via>>
          ELSE LET i == Min(open)
                   now == i = part[c] /\ unres[c]      \* the unresolved part itself
               IN
               /\ part' = [part EXCEPT ![c] = i]
               /\ CASE DoneState(c, i) = "conflict" ->
                         /\ pc' = [pc EXCEPT ![c] = "conflict"]
                         /\ unres' = [unres EXCEPT ![c] = now]
                         /\ UNCHANGED <<rep, reop, via>>
                    [] DoneState(c, i) = "fenced" ->
                         /\ pc' = [pc EXCEPT ![c] = "replied"]
                         /\ rep' = [rep EXCEPT ![c] = [out |-> "replay", code |-> "REPLAY",
                                      res |-> done[<<oe[c], OpId(c, i)>>].res, st |-> "fenced"]]
                         /\ unres' = [unres EXCEPT ![c] = now]
                         /\ UNCHANGED <<reop, via>>
                    [] OTHER ->
                         \/ /\ pc' = [pc EXCEPT ![c] = "start"]
                            /\ unres' = [unres EXCEPT ![c] = now]
                            /\ UNCHANGED <<rep, reop, via>>
                         \/ /\ Broken = "W10" /\ now /\ reop[c][i] < MaxReop
                            /\ reop' = [reop EXCEPT ![c][i] = @ + 1]
                            /\ pc' = [pc EXCEPT ![c] = "start"]
                            /\ unres' = [unres EXCEPT ![c] = FALSE]
                            /\ UNCHANGED <<rep, via>>

-----------------------------------------------------------------------------
\* Outside events.

\* The store answered and the reply never reached the caller.
LoseReply(c) ==
  /\ pc[c] = "replied"
  /\ pc' = [pc EXCEPT ![c] = "unknown"]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ unres' = [unres EXCEPT ![c] = TRUE]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, snap, req, tries, crashes, late, beats, reop, via, faults>>
\* The caller stopped waiting before the store ran the request; the request
\* is still on its way and may run later (LateRun), or never.
Timeout(c) ==
  /\ pc[c] = "planned"
  /\ late[c] = {}
  /\ Cardinality({x \in Callers : late[x] # {}}) < MaxLate
  /\ late' = [late EXCEPT ![c] = {req[c]}]
  /\ pc' = [pc EXCEPT ![c] = "unknown"]
  /\ unres' = [unres EXCEPT ![c] = TRUE]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, snap, req, rep, tries, crashes, beats, reop, via, faults>>
\* The caller's process dies; what it had in memory is gone, the manifest and
\* anything it left in flight are not. After a crash the unfinished part is
\* presumed dispatched (section 5), once the verb has fixed its epoch.
Crash(c) ==
  /\ crashes < MaxCrashes
  /\ pc[c] \notin Terminal \cup {"resume"}
  /\ pc' = [pc EXCEPT ![c] = "resume"]
  /\ snap' = [snap EXCEPT ![c] = NoSnap]
  /\ req' = [req EXCEPT ![c] = NoReq]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ unres' = [unres EXCEPT ![c] = oe[c] # NoEpoch]
  /\ via' = [via EXCEPT ![c] = "plan"]
  /\ crashes' = crashes + 1
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, tries, late, beats, reop, faults>>
\* A second writer outside the programs (the sprint's beat): one guarded,
\* op-less step, read and applied at once, between any caller's read and
\* apply.
Beat ==
  /\ beats < MaxBeats
  /\ \E b \in BeatSet :
       LET t == b[1]  m == b[2]  x == b[3]  f == b[4]  r == rec[t][m] IN
       /\ r.ex /\ r.ep = ep /\ r.pl # NoPlace /\ x[1] \in rows[t][ep]
       /\ Exec([ep |-> ep, op |-> NoOp, intent |-> NoIntent,
                es |-> <<MoveE(t, m, r.pl, x, f, r.rev)>>, pv |-> "-", fence |-> FALSE,
                how |-> "plan"], "beat")
  /\ beats' = beats + 1
  /\ UNCHANGED <<prog, pc, part, oe, snap, req, rep, tries, crashes, late, unres, reop, via, faults>>

\* Every caller has ended: the system is at rest (not a deadlock).
Quiet == (\A c \in Callers : pc[c] \in Terminal) /\ UNCHANGED vars

-----------------------------------------------------------------------------

Init ==
  /\ ep = 0
  /\ rows = [t \in Tables |-> [e \in Epochs |-> IF e = 0 THEN InitRows[t] ELSE {}]]
  /\ rec = InitRecs
  /\ cells = [t \in Tables |-> [e \in Epochs |-> [x \in Cells |->
               {m \in Members : InitRecs[t][m].ex /\ InitRecs[t][m].ep = e
                                /\ InitRecs[t][m].pl = x}]]]
  /\ done = [k \in Keys |-> NoRcpt]
  /\ hist = {}
  /\ last = NoLast
  /\ prog \in {p \in [Callers -> UNION {Menu[c] : c \in Callers}] : \A c \in Callers : p[c] \in Menu[c]}
  /\ pc = [c \in Callers |-> "start"]
  /\ part = [c \in Callers |-> 1]
  /\ oe = [c \in Callers |-> NoEpoch]
  /\ snap = [c \in Callers |-> NoSnap]
  /\ req = [c \in Callers |-> NoReq]
  /\ rep = [c \in Callers |-> NoRep]
  /\ tries = [c \in Callers |-> 0]
  /\ crashes = 0
  /\ late = [c \in Callers |-> {}]
  /\ beats = 0
  /\ unres = [c \in Callers |-> FALSE]
  /\ reop = [c \in Callers |-> [i \in 1..Len(prog[c]) |-> 0]]
  /\ via = [c \in Callers |-> "plan"]
  /\ faults = 0

CallerStep(c) ==
  \/ Read(c) \/ Plan(c) \/ Accept(c) \/ Stale(c) \/ Conflict(c) \/ GiveUp(c)
  \/ ReportRefused(c) \/ NewOp(c) \/ Rebuild(c) \/ RetryFresh(c) \/ RetrySame(c)
  \/ Fence(c) \/ AskDone(c) \/ LostCause(c) \/ Resume(c)
StoreStep(c) == Answer(c) \/ Torn(c) \/ FenceFault(c) \/ LateRun(c)
Outside(c) == LoseReply(c) \/ Timeout(c) \/ Crash(c)

Next ==
  \/ \E c \in Callers : CallerStep(c) \/ StoreStep(c) \/ Outside(c)
  \/ Beat
  \/ Quiet

Spec == Init /\ [][Next]_vars
\* Fairness on what the callers and the store owe; none on outside events.
LiveSpec == Spec /\ \A c \in Callers : WF_vars(CallerStep(c)) /\ WF_vars(Answer(c))

-----------------------------------------------------------------------------
\* What the contract guarantees (section 2), stated apart from the store's
\* own check so that a witness that changes the store is caught.

TypeOK ==
  /\ ep \in Epochs
  /\ rows \in [Tables -> [Epochs -> SUBSET Rows]]
  /\ cells \in [Tables -> [Epochs -> [Cells -> SUBSET Members]]]
  /\ \A t \in Tables, m \in Members :
       /\ rec[t][m].ex \in BOOLEAN /\ rec[t][m].ep \in Epochs /\ rec[t][m].rev \in Nat
       /\ rec[t][m].pl \in Cells \cup {NoPlace} /\ rec[t][m].f \in Vals
  /\ DOMAIN done = Keys
  /\ \A k \in Keys : done[k].st \in {"ok", "fenced"}
  /\ pc \in [Callers -> {"start", "read", "planned", "replied", "unknown", "resume"} \cup Terminal]
  /\ \A c \in Callers : tries[c] \in 0..MaxRetries[c]
  /\ crashes \in 0..MaxCrashes
  /\ beats \in 0..MaxBeats
  /\ faults \in 0..MaxFaults
  /\ oe \in [Callers -> 0..NoEpoch]
  /\ unres \in [Callers -> BOOLEAN]
  /\ via \in [Callers -> {"plan", "direct"}]
  /\ \A c \in Callers : reop[c] \in [1..Len(prog[c]) -> 0..MaxReop]

\* One place: a member is in at most one cell of a table, only in its own
\* epoch, exactly where its record says; a record's place is in its cell.
OnePlace ==
  /\ \A t \in Tables, e \in Epochs, m \in Members :
       Cardinality({x \in Cells : m \in cells[t][e][x]}) <= 1
  /\ \A t \in Tables, e \in Epochs, x \in Cells : \A m \in cells[t][e][x] :
       rec[t][m].ex /\ rec[t][m].ep = e /\ rec[t][m].pl = x
  /\ \A t \in Tables, m \in Members :
       rec[t][m].pl # NoPlace => m \in cells[t][rec[t][m].ep][rec[t][m].pl]

\* No member sits in a cell of a row its table does not have.
RowsHold ==
  /\ \A t \in Tables, e \in Epochs, x \in Cells :
       x[1] \notin rows[t][e] => cells[t][e][x] = {}
  /\ \A t \in Tables, m \in Members :
       rec[t][m].pl # NoPlace => rec[t][m].pl[1] \in rows[t][rec[t][m].ep]

\* A refused step leaves every variable of the store unchanged.
RefusedWritesNothing == [][last'.out = "refused" => UNCHANGED <<store, hist>>]_vars

\* Every entry of a step that changed the store is applied, with its receipt;
\* a fence that changed the store wrote its fenced receipt and nothing else.
Whole(R) ==
  LET we == WEp(R) IN
  /\ \A e \in MemberEs(R) :
       CASE e.k = "create" -> /\ rec'[e.t][e.m] = [ex |-> TRUE, ep |-> we, rev |-> 1, pl |-> e.to, f |-> e.f]
                              /\ e.m \in cells'[e.t][we][e.to]
         [] e.k = "move"   -> /\ rec'[e.t][e.m].pl = e.to /\ rec'[e.t][e.m].f = e.f
                              /\ e.m \in cells'[e.t][we][e.to]
                              /\ (e.from # e.to => e.m \notin cells'[e.t][we][e.from])
         [] e.k = "remove" -> /\ rec'[e.t][e.m].pl = NoPlace /\ rec'[e.t][e.m].f = e.f
                              /\ e.m \notin cells'[e.t][we][e.from]
  /\ \A i \in 1..Len(R.es) : R.es[i].k = "rows" =>
       /\ R.es[i].add \subseteq rows'[R.es[i].t][we]
       /\ R.es[i].del \cap rows'[R.es[i].t][we] = {}
  /\ ep' = we
  /\ R.op # NoOp => done'[<<R.ep, R.op>>].has /\ done'[<<R.ep, R.op>>].st = "ok"
FenceWhole(R) ==
  /\ R.fence
  /\ UNCHANGED <<ep, rows, cells, rec>>
  /\ done'[<<R.ep, R.op>>].has /\ done'[<<R.ep, R.op>>].st = "fenced"
  /\ \A k \in Keys \ {<<R.ep, R.op>>} : done'[k] = done[k]
AppliedIsWhole ==
  [][(last'.out # "-" /\ store' # store) =>
       \/ last'.out = "apply" /\ Whole(last'.R)
       \/ last'.out = "fenced" /\ FenceWhole(last'.R)]_vars

\* A member's revision moves by exactly one in a step that changes the
\* member (placement, field, existence), and never otherwise.
RevisionMoves ==
  [][\A t \in Tables, m \in Members :
       LET r == rec[t][m]  s == rec'[t][m] IN
       IF s.pl # r.pl \/ s.f # r.f \/ s.ex # r.ex THEN s.rev = r.rev + 1 ELSE s.rev = r.rev]_vars

\* No applied step had a guard that was false at apply time.
GuardsHold(R) ==
  LET we == WEp(R) IN
  /\ (HasAdv(R) => AdvFrom(R) = ep) /\ R.ep = ep
  /\ \A i \in 1..Len(R.es) :
       LET e == R.es[i] IN
       /\ e.k = "create" => ~rec[e.t][e.m].ex /\ e.to[1] \in Prosp(R, e.t)
       /\ e.k \in {"move", "remove"} =>
            LET r == rec[e.t][e.m] IN
            /\ r.ex /\ r.ep = we /\ r.pl = e.from /\ e.from[1] \in Pre(R, e.t)
            /\ e.m \in cells[e.t][we][e.from]
            /\ (e.rev # 0 => r.rev = e.rev)
            /\ (e.k = "move" => e.to[1] \in Prosp(R, e.t))
       /\ e.k = "count" => e.from[1] \in Pre(R, e.t) /\ Cardinality(cells[e.t][we][e.from]) <= e.max
       /\ e.k = "rows" => /\ Adds(R, e.t) \cap Dels(R, e.t) = {}
                          /\ \A r \in e.del \cap Pre(R, e.t) : \A col \in Cols :
                               cells[e.t][we][<<r, col>>] \ Out(R, e.t, <<r, col>>) = {}
       /\ e.k = "rowset" => rows[e.t][R.ep] = e.add
GuardsAtApply == [][last'.out = "apply" => GuardsHold(last'.R)]_vars

\* Receipts. Applied(R): R's identity was applied with R's intent before now.
Applied(R) == \E h \in hist : h.ep = R.ep /\ h.op = R.op /\ h.dig = R.intent
AppliedRes(R) == (CHOOSE h \in hist : h.ep = R.ep /\ h.op = R.op /\ h.dig = R.intent).res
ReceiptInv ==
  /\ \A h \in hist : h.n = 1                         \* at most once
  /\ \A k \in Keys : done[k].has /\ done[k].st = "ok" =>   \* an ok receipt is its own step's
       \E h \in hist : h.ep = k[1] /\ h.op = k[2] /\ h.dig = RcptIntent(done[k]) /\ h.res = done[k].res
  /\ \A k \in Keys : done[k].has /\ done[k].st = "fenced" =>   \* a fenced identity never applied
       ~\E h \in hist : h.ep = k[1] /\ h.op = k[2]
  /\ \A c \in Callers : pc[c] = "replied" /\ (rep[c].out = "apply" \/ (rep[c].out = "replay" /\ rep[c].st = "ok")) =>
       \E h \in hist : h.ep = req[c].ep /\ h.op = req[c].op /\ h.res = rep[c].res
  /\ \A c \in Callers : NotApplied(c) => ~\E h \in hist : h.ep = oe[c] /\ h.op = CurOp(c)
\* A statically valid request with an applied identity and the same intent is
\* answered with the recorded ok result and writes nothing: resent bytes, a
\* plan made after done, or a fence. Narrowed (section 2, H3): a replan of an
\* unresolved identity made without asking done ("direct") is not claimed.
\* A FAULT is outside the claim: ENGINE and CONFIG come before the receipt.
ReceiptAct ==
  (last'.out # "-" /\ last'.code # "FAULT" /\ last'.R.op # NoOp /\ last'.R.how # "direct"
   /\ StaticCode(last'.R) = "ok" /\ Applied(last'.R)) =>
    /\ last'.out = "replay" /\ last'.st = "ok"
    /\ last'.res = AppliedRes(last'.R)
    /\ UNCHANGED <<store, hist>>
ReceiptTruth == [][ReceiptAct /\ ReceiptInv']_vars

\* A request whose identity has a receipt of another intent is refused
\* OPCONFLICT and writes nothing.
ConflictOnChangedIntent ==
  [][(last'.out # "-" /\ last'.code # "FAULT" /\ last'.R.op # NoOp /\ StaticCode(last'.R) = "ok"
      /\ done[RdKey(last'.R)].has
      /\ RcptIntent(done[RdKey(last'.R)]) # last'.R.intent)
     => (last'.out = "refused" /\ last'.code = "OPCONFLICT" /\ UNCHANGED <<store, hist>>)]_vars

\* Epochs: the epoch only moves forward, only by an applied advance; a step
\* writes cells, rows and records only at the epoch it names and its receipt
\* (or a fence's) only at its request epoch; an applied identity is never
\* answered STALE (a lost reply at an advance is answered from the receipt).
AdvanceAnswered == last'.code = "STALE" => ~Applied(last'.R)
EpochAct ==
  /\ ep' >= ep
  /\ ep' # ep => (last'.out = "apply" /\ HasAdv(last'.R))
  /\ last'.out = "apply" =>
       LET R == last'.R  we == WEp(R) IN
       /\ \A t \in Tables, e \in Epochs \ {we} : cells'[t][e] = cells[t][e] /\ rows'[t][e] = rows[t][e]
       /\ \A t \in Tables, m \in Members : rec'[t][m] # rec[t][m] => rec'[t][m].ep = we
       /\ \A k \in Keys : done'[k] # done[k] => k = <<R.ep, R.op>>
  /\ last'.out = "fenced" => \A k \in Keys : done'[k] # done[k] => k = <<last'.R.ep, last'.R.op>>
  /\ AdvanceAnswered
EpochSafe == [][EpochAct]_vars
LostAdvanceAnswered == [][AdvanceAnswered]_vars

\* H1 (section 2): a complete topology-preserving clear guards every catalog
\* table at the request epoch, so the rows it restores are the rows the
\* closing epoch has when it closes.
RestoreIsCurrent ==
  [][(last'.out = "apply" /\ HasAdv(last'.R) /\ last'.R.intent.a.clear)
     => \A t \in Tables : rows'[t][ep'] = rows[t][ep]]_vars

\* H2 (sections 2 and 5). PartApplied(c, i): part i of c's program has been
\* applied under some op identity of its family.
PartApplied(c, i) == \E h \in hist : h.op[1] = prog[c][i].op /\ h.dig = PartIntent(c, i)
\* A verb that reported a part refused never has that part applied, then or
\* later, under any of its op identities.
RefusedIsFinal ==
  \A c \in Callers : pc[c] = "refused" => ~PartApplied(c, part[c])
\* A verb that ended finished has every part applied, a crash after a fence
\* included.
FinishedIsApplied ==
  \A c \in Callers : pc[c] = "finished" => \A i \in 1..Len(prog[c]) : PartApplied(c, i)
\* No part's effects are applied under two op identities.
PartOnce ==
  \A h1, h2 \in hist : (h1.op[1] = h2.op[1] /\ h1.dig = h2.dig) => h1.op = h2.op

\* Every caller comes to an end (under fairness of what callers and the
\* store owe).
Settles == <>(\A c \in Callers : pc[c] \in Terminal)

-----------------------------------------------------------------------------
\* A goal of a verb that the contract disclaims (section 5: "This contract
\* guarantees each identified atomic part, not a whole multi-part
\* transaction"). It is expected to FAIL on the contract model.

\* A verb that ended STALE has none of its parts applied.
PartsWhole ==
  \A c \in Callers : pc[c] = "stale" =>
    ~\E h \in hist : \E i \in 1..Len(prog[c]) : h.ep = oe[c] /\ h.op[1] = prog[c][i].op

=============================================================================
