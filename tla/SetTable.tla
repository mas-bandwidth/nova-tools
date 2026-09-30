------------------------------- MODULE SetTable -------------------------------
\* Layer 1 of the sprint foundation, the table (tset/1), as a state machine,
\* written from its contract, design/L1-CONTRACT.md, contract revision 2
\* (review candidate). One atomic step over several tables: a caller reads,
\* plans a request from what it read, and the store applies it whole or
\* refuses it whole. Guards are per member and per cell, evaluated against
\* the state at apply time. One place per member. Receipts keyed by
\* (request epoch, op), matched by the digest of the caller's intent, so a
\* retry after a fresh read returns the recorded result. Epochs and advance.
\*
\* The variables are the state Layer 1 owns (records, cells, rows, the active
\* epoch, the done receipts) and the in-flight state of each caller (its
\* program, where it is, what it read, the request it built, the reply it
\* has, its retries, requests it left in flight). The actions are the
\* operations (Read, Plan, Apply, Refuse, Replay) and the outside events
\* (LoseReply, Timeout, LateRun, Crash, Beat, the other caller's steps).
\*
\* The store of the CONTRACT never half-applies: a step's guards are all
\* checked, then every write is made, in one action. Broken names a reversed
\* witness that changes exactly one rule (see the README); "none" is the
\* contract.
\*
\* NOT modelled: sizes and budgets, time and the 25 ms gate, Redis itself
\* (types, ACL, argv splitting, runtime errors: the contract's prepare is
\* ASSUMED complete here), scores (one application field stands for every
\* effective change), notes, reads other than the planning snapshot and the
\* done query, the log and history (Layer 2), the Go twin, op-less caller
\* steps (only the outside writer's beat is op-less), TWICE and every static
\* request check but one (a move or remove with no source cell).
EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS
  Tables, TableSeq,  \* table names, and the same as a sequence
  Rows, Cols,        \* row and column names of every table
  Members,           \* stored IDs
  Callers,           \* callers that run programs
  Vals,              \* values of the one application field
  MaxEpoch,          \* epochs are 0..MaxEpoch
  MaxRetries,        \* resends, fresh replans and rebuilds per caller, in all
  MaxCrashes,        \* caller crashes, in all
  MaxLate,           \* callers that may have a request left in flight at once
  MaxBeats,          \* outside writer steps, in all
  BeatSet,           \* the outside writer's moves: <<table, member, cell, value>>
  InitRows,          \* [Tables -> SUBSET Rows]: the rows at epoch 0
  InitRecs,          \* [Tables -> [Members -> record]]: the records at epoch 0
  Menu,              \* [Callers -> SUBSET Seq(part)]: the programs a caller may run
  Ops,               \* every op identifier a program names
  DirectReplan,      \* TRUE: after a lost reply a caller may replan from a fresh
                     \* read without asking done first (FALSE: only the contract's
                     \* two paths, the same bytes again, or done first on resume)
  Broken             \* "none", or the reversed witness: see the README

VARIABLES
  ep, rows, cells, rec, done,      \* the store
  hist, last,                      \* ghosts: applications so far; the last store step
  prog, pc, part, oe, snap, req, rep, tries, crashes, late, beats

store == <<ep, rows, cells, rec, done>>
vars == <<ep, rows, cells, rec, done, hist, last,
          prog, pc, part, oe, snap, req, rep, tries, crashes, late, beats>>

-----------------------------------------------------------------------------
\* Values.

Epochs == 0..MaxEpoch
NoEpoch == MaxEpoch + 1                 \* a verb that has not fixed its epoch
Cells == Rows \X Cols                   \* <<row, col>>
NoPlace == <<"-", "-">>
Keys == Epochs \X Ops                   \* receipt identities (request epoch, op)
Range(s) == {s[i] : i \in DOMAIN s}
Min(S) == CHOOSE i \in S : \A j \in S : i <= j
Terminal == {"finished", "stale", "refused", "lost", "conflict"}

\* A record. rev 0 with ex FALSE is an absent record. A removed record keeps
\* ex TRUE and has no place (EXISTS refuses its create).
Absent == [ex |-> FALSE, ep |-> 0, rev |-> 0, pl |-> NoPlace, f |-> 0]

\* A program is a sequence of parts [op, a]; a is the part's semantic
\* arguments, one shape for every part:
\*   mv: moves and removes [t, m, to, f, rm];  cr: creates [t, m, to, f, rm];
\*   rt, add, del: one rows entry on table rt;  cap: count guard "to is
\*   empty" on every move's destination;  clear: advance, restoring every row
\*   the caller read.
NoArgs == [mv |-> <<>>, cr |-> <<>>, rt |-> "-", add |-> {}, del |-> {},
           cap |-> FALSE, clear |-> FALSE]
\* The intent: the part's semantic arguments and the verb's original epoch.
\* It excludes everything observed (places, revisions, rows read).
NoIntent == [a |-> NoArgs, oe |-> 0]
NoRes == [eb |-> 0, ea |-> 0, ch |-> 0]
NoRcpt == [has |-> FALSE, dig |-> NoIntent, res |-> NoRes]
NoReq == [ep |-> 0, op |-> "none", intent |-> NoIntent, es |-> <<>>, pv |-> "-"]
NoRep == [out |-> "-", code |-> "-", res |-> NoRes]
NoLast == [by |-> "-", R |-> NoReq, out |-> "-", code |-> "-", res |-> NoRes]
NoSnap == [ep |-> 0, rec |-> [k \in {} |-> Absent], rows |-> [t \in Tables |-> {}]]

\* Entries, one shape. rev 0 means "no expected revision given" (revisions of
\* existing records start at 1). from holds the cell of a count guard.
E(k, t, m, from, to, f, rev, max, add, del, efrom) ==
  [k |-> k, t |-> t, m |-> m, from |-> from, to |-> to, f |-> f, rev |-> rev,
   max |-> max, add |-> add, del |-> del, efrom |-> efrom]
CreateE(t, m, to, f)          == E("create", t, m, NoPlace, to, f, 0, 0, {}, {}, 0)
MoveE(t, m, from, to, f, rev) == E("move", t, m, from, to, f, rev, 0, {}, {}, 0)
RemoveE(t, m, from, f, rev)   == E("remove", t, m, from, NoPlace, f, rev, 0, {}, {}, 0)
CountE(t, x, max)             == E("count", t, "-", x, NoPlace, 0, 0, max, {}, {}, 0)
RowsE(t, add, del)            == E("rows", t, "-", NoPlace, NoPlace, 0, 0, 0, add, del, 0)
AdvE(e)                       == E("advance", "-", "-", NoPlace, NoPlace, 0, 0, 0, {}, {}, e)

-----------------------------------------------------------------------------
\* A request R = [ep, op, intent, es, pv]: request epoch, op, intent, entries
\* in order, and (witness W1 only) the guard verdict of plan time.

HasAdv(R) == Len(R.es) > 0 /\ R.es[1].k = "advance"
WEp(R) == IF HasAdv(R) THEN ep + 1 ELSE ep          \* the write epoch
Pre(R, t) == IF HasAdv(R) THEN {} ELSE rows[t][ep]  \* pre_rows at the write epoch
RowEs(R, t) == {R.es[i] : i \in {j \in 1..Len(R.es) : R.es[j].k = "rows" /\ R.es[j].t = t}}
Adds(R, t) == UNION {e.add : e \in RowEs(R, t)}
Dels(R, t) == UNION {e.del : e \in RowEs(R, t)}
Prosp(R, t) == (Pre(R, t) \cup Adds(R, t)) \ Dels(R, t)
MemberEs(R) == {R.es[i] : i \in {j \in 1..Len(R.es) : R.es[j].k \in {"create", "move", "remove"}}}
EntryFor(R, t, m) == CHOOSE e \in MemberEs(R) : e.t = t /\ e.m = m
TablesOf(R) == {R.es[i].t : i \in 1..Len(R.es)} \ {"-"}
FirstTable(R) == R.es[Min({i \in 1..Len(R.es) : R.es[i].t # "-"})].t
Restrict(R, t) == [R EXCEPT !.es = SelectSeq(R.es, LAMBDA e : e.t = t)]
Dig(R) == <<R.ep, R.op, R.intent, R.es>>            \* the request's bytes

\* Change means altered placement or application field (section 4).
Changed(e) ==
  CASE e.k = "create" -> TRUE
    [] e.k = "remove" -> TRUE
    [] e.k = "move"   -> e.to # rec[e.t][e.m].pl \/ e.f # rec[e.t][e.m].f

-----------------------------------------------------------------------------
\* The store's checks, in the contract's phase order (section 8): static
\* validity, the receipt, epoch and advance, topology, then member and cell
\* guards.

RdKey(R) == <<R.ep, R.op>>
WrKey(R) == IF Broken = "W4" THEN <<WEp(R), R.op>> ELSE <<R.ep, R.op>>
RcptDig(R) == IF Broken \in {"W3", "W3b"} THEN Dig(R) ELSE R.intent
RcptIntent(d) == IF Broken \in {"W3", "W3b"} THEN d.dig[3] ELSE d.dig
Found(R) ==
  /\ R.op # "none"
  /\ done[RdKey(R)].has
  /\ (Broken = "W3b" => done[RdKey(R)].dig = Dig(R))
Matches(R) == done[RdKey(R)].dig = RcptDig(R)

\* Static validity comes before the receipt (section 8's phase order, and
\* section 3: "Static request validity is still checked on replay"). The one
\* static fault a plan can make here: a move or remove with no source cell,
\* which is what a plan from a read of an unplaced member has to offer
\* (from is a required cell reference; null is not accepted).
StaticCode(R) ==
  IF \E i \in 1..Len(R.es) : R.es[i].k \in {"move", "remove"} /\ R.es[i].from = NoPlace
  THEN "REQUEST" ELSE "ok"

OpenCode(R) ==
  IF StaticCode(R) # "ok" THEN StaticCode(R)
  ELSE IF Found(R) THEN (IF Matches(R) THEN "REPLAY" ELSE "OPCONFLICT")
  ELSE IF R.ep > ep THEN "EPOCHAHEAD"
  ELSE IF R.ep < ep THEN "STALE"
  ELSE IF HasAdv(R) /\ R.es[1].efrom # ep THEN "ADVANCE"
  ELSE IF HasAdv(R) /\ ep = MaxEpoch THEN "OVERFLOW"
  ELSE "ok"

TopoCode(R) ==
  IF \E t \in Tables : Adds(R, t) \cap Dels(R, t) # {} THEN "ROWCONFLICT"
  ELSE IF \E t \in Tables : \E r \in Dels(R, t) \cap Pre(R, t) : \E c \in Cols :
            cells[t][WEp(R)][<<r, c>>] # {} THEN "OCCUPIED"
  ELSE IF HasAdv(R) /\ \E t \in Tables : rows[t][ep + 1] # {}
                                     \/ \E x \in Cells : cells[t][ep + 1][x] # {}
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
    [] e.k \in {"rows", "advance"} -> "ok"

Bad(R) == {i \in 1..Len(R.es) : EntryCode(R, R.es[i]) # "ok"}
MemberCode(R) == IF Bad(R) = {} THEN "ok" ELSE EntryCode(R, R.es[Min(Bad(R))])
GuardPhase(R) == LET tp == TopoCode(R) IN IF tp # "ok" THEN tp ELSE MemberCode(R)

\* The verdict: "REPLAY", "ok", or a refusal code. In W1 the guards were
\* checked once, at plan time, and the store trusts that verdict.
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
Out(R, t, x) == {e.m : e \in {y \in MemberEs(R) :
                   y.t = t /\ y.k \in {"move", "remove"} /\ y.from = x /\ Changed(y)}}
In(R, t, x) == {e.m : e \in {y \in MemberEs(R) :
                   y.t = t /\ y.k \in {"create", "move"} /\ y.to = x /\ Changed(y)}}
Res(R) == [eb |-> ep, ea |-> WEp(R), ch |-> Cardinality({e \in MemberEs(R) : Changed(e)})]
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
  /\ done' = IF withRcpt /\ R.op # "none"
             THEN [done EXCEPT ![WrKey(R)] = [has |-> TRUE, dig |-> RcptDig(R), res |-> Res(R)]]
             ELSE done

\* The store executes R for who: a replay, the whole step, or a refusal.
Exec(R, who) ==
  LET code == CheckCode(R) IN
  \/ /\ code = "REPLAY"
     /\ UNCHANGED <<store, hist>>
     /\ last' = [by |-> who, R |-> R, out |-> "replay", code |-> code, res |-> done[RdKey(R)].res]
  \/ /\ code = "ok"
     /\ Writes(R, TRUE)
     /\ hist' = IF R.op = "none" THEN hist
                ELSE hist \cup {[ep |-> R.ep, op |-> R.op, dig |-> R.intent,
                                 res |-> Res(R), n |-> NApplied(R) + 1]}
     /\ last' = [by |-> who, R |-> R, out |-> "apply", code |-> code, res |-> Res(R)]
  \/ /\ code \notin {"REPLAY", "ok"}
     /\ UNCHANGED <<store, hist>>
     /\ last' = [by |-> who, R |-> R, out |-> "refused", code |-> code, res |-> NoRes]

\* W2 only: the plan accepted a step over two tables, and the commit stopped
\* after the first table's writes (a runtime error mid-commit): no receipt,
\* and the caller hears an error of unknown outcome.
ExecTorn(R, who) ==
  /\ Broken = "W2"
  /\ CheckCode(R) = "ok"
  /\ ~HasAdv(R)
  /\ Cardinality(TablesOf(R)) >= 2
  /\ Writes(Restrict(R, FirstTable(R)), FALSE)
  /\ hist' = IF R.op = "none" THEN hist
             ELSE hist \cup {[ep |-> R.ep, op |-> R.op, dig |-> R.intent,
                              res |-> NoRes, n |-> NApplied(R) + 1]}
  /\ last' = [by |-> who, R |-> R, out |-> "error", code |-> "ERROR", res |-> NoRes]

-----------------------------------------------------------------------------
\* Callers. A caller runs one program: parts in order, each one atomic step
\* with its own op. The verb fixes its original epoch at its first read and
\* keeps it, with its part identities, across retries and crashes (the resume
\* manifest). A retry is the same op and intent: the same bytes again, or a
\* fresh read and a new plan.

Cur(c) == prog[c][part[c]]
IntentOf(c) == [a |-> Cur(c).a, oe |-> oe[c]]
Named(P) == {<<x.t, x.m>> : x \in Range(P.a.mv)}
View(P) == [ep |-> ep,
            rec |-> [k \in Named(P) |-> rec[k[1]][k[2]]],
            rows |-> [t \in Tables |-> IF P.a.clear THEN rows[t][ep] ELSE {}]]

MvE(x, s, wr) ==
  LET r == s.rec[<<x.t, x.m>>]
      rv == IF wr THEN r.rev ELSE 0
  IN IF x.rm THEN RemoveE(x.t, x.m, r.pl, x.f, rv) ELSE MoveE(x.t, x.m, r.pl, x.to, x.f, rv)
Seqn(n, F(_)) == [i \in 1..n |-> F(i)]
PlanEs(I, s, wr) ==
  LET a == I.a IN
  (IF a.clear THEN <<AdvE(I.oe)>> \o Seqn(Len(TableSeq), LAMBDA i : RowsE(TableSeq[i], s.rows[TableSeq[i]], {}))
   ELSE <<>>)
  \o (IF a.rt # "-" THEN <<RowsE(a.rt, a.add, a.del)>> ELSE <<>>)
  \o Seqn(Len(a.cr), LAMBDA i : CreateE(a.cr[i].t, a.cr[i].m, a.cr[i].to, a.cr[i].f))
  \o (IF a.cap THEN Seqn(Len(a.mv), LAMBDA i : CountE(a.mv[i].t, a.mv[i].to, 0)) ELSE <<>>)
  \o Seqn(Len(a.mv), LAMBDA i : MvE(a.mv[i], s, wr))

Unsure(c) == pc[c] = "unknown" \/ (pc[c] = "replied" /\ rep[c].out = "error")

Read(c) ==
  /\ pc[c] = "start"
  /\ oe' = [oe EXCEPT ![c] = IF @ = NoEpoch THEN ep ELSE @]
  /\ snap' = [snap EXCEPT ![c] = View(Cur(c))]
  /\ pc' = [pc EXCEPT ![c] = "read"]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, req, rep, tries, crashes, late, beats>>

\* The plan names expected revisions or not (revs are optional).
Plan(c) ==
  /\ pc[c] = "read"
  /\ \E wr \in BOOLEAN :
       LET R0 == [ep |-> oe[c], op |-> Cur(c).op, intent |-> IntentOf(c),
                  es |-> PlanEs(IntentOf(c), snap[c], wr), pv |-> "-"]
       IN req' = [req EXCEPT ![c] = IF Broken = "W1" THEN [R0 EXCEPT !.pv = PlanVerdict(R0)] ELSE R0]
  /\ snap' = [snap EXCEPT ![c] = NoSnap]
  /\ pc' = [pc EXCEPT ![c] = "planned"]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, rep, tries, crashes, late, beats>>

Deliver(c) ==
  /\ pc' = [pc EXCEPT ![c] = "replied"]
  /\ rep' = [rep EXCEPT ![c] = [out |-> last'.out, code |-> last'.code, res |-> last'.res]]
  /\ UNCHANGED <<prog, part, oe, snap, req, tries, crashes, late, beats>>

\* The store's side: the atomic step, a refusal, a replay; and a request a
\* caller left in flight, executed later with its reply going nowhere.
Apply(c)  == /\ pc[c] = "planned" /\ CheckCode(req[c]) = "ok" /\ Exec(req[c], c) /\ Deliver(c)
Refuse(c) == /\ pc[c] = "planned" /\ CheckCode(req[c]) \notin {"ok", "REPLAY"}
             /\ Exec(req[c], c) /\ Deliver(c)
Replay(c) == /\ pc[c] = "planned" /\ CheckCode(req[c]) = "REPLAY" /\ Exec(req[c], c) /\ Deliver(c)
Torn(c)   == /\ pc[c] = "planned" /\ ExecTorn(req[c], c) /\ Deliver(c)
LateRun(c) ==
  /\ \E R \in late[c] :
       /\ late' = [late EXCEPT ![c] = @ \ {R}]
       /\ Exec(R, c)
  /\ UNCHANGED <<prog, pc, part, oe, snap, req, rep, tries, crashes, beats>>

\* The caller's side of a reply.
Accept(c) ==
  /\ pc[c] = "replied" /\ rep[c].out \in {"apply", "replay"}
  /\ IF part[c] = Len(prog[c])
     THEN pc' = [pc EXCEPT ![c] = "finished"] /\ part' = part
     ELSE pc' = [pc EXCEPT ![c] = "start"] /\ part' = [part EXCEPT ![c] = @ + 1]
  /\ req' = [req EXCEPT ![c] = NoReq]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, oe, snap, tries, crashes, late, beats>>
Ends(c, why) ==
  /\ pc' = [pc EXCEPT ![c] = why]
  /\ req' = [req EXCEPT ![c] = NoReq]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, snap, tries, crashes, late, beats>>
Stale(c) == pc[c] = "replied" /\ rep[c].code = "STALE" /\ Ends(c, "stale")
Conflict(c) == pc[c] = "replied" /\ rep[c].code = "OPCONFLICT" /\ Ends(c, "conflict")
Refusal(c) == pc[c] = "replied" /\ rep[c].out = "refused" /\ rep[c].code \notin {"STALE", "OPCONFLICT"}
GiveUp(c) == Refusal(c) /\ Ends(c, "refused")
Again(c) ==
  /\ tries[c] < MaxRetries
  /\ pc' = [pc EXCEPT ![c] = "start"]
  /\ req' = [req EXCEPT ![c] = NoReq]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ tries' = [tries EXCEPT ![c] = @ + 1]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, snap, crashes, late, beats>>
Rebuild(c) == Refusal(c) /\ Again(c)          \* re-read and rebuild after a refusal
RetryFresh(c) == DirectReplan /\ Unsure(c) /\ Again(c)  \* same op and intent, after a fresh read
RetrySame(c) ==                               \* the same bytes again
  /\ Unsure(c)
  /\ tries[c] < MaxRetries
  /\ pc' = [pc EXCEPT ![c] = "planned"]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ tries' = [tries EXCEPT ![c] = @ + 1]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, snap, req, crashes, late, beats>>
LostCause(c) == Unsure(c) /\ tries[c] = MaxRetries /\ Ends(c, "lost")

\* The resume path after a crash: done for every part identity of the
\* manifest, then the first part that is not done.
DoneState(c, i) ==
  LET d == done[<<oe[c], prog[c][i].op>>] IN
  IF ~d.has THEN "absent"
  ELSE IF RcptIntent(d) = [a |-> prog[c][i].a, oe |-> oe[c]] THEN "match" ELSE "conflict"
Resume(c) ==
  /\ pc[c] = "resume"
  /\ IF oe[c] = NoEpoch
     THEN pc' = [pc EXCEPT ![c] = "start"] /\ part' = [part EXCEPT ![c] = 1]
     ELSE LET open == {i \in 1..Len(prog[c]) : DoneState(c, i) # "match"} IN
          IF open = {} THEN pc' = [pc EXCEPT ![c] = "finished"] /\ part' = part
          ELSE /\ part' = [part EXCEPT ![c] = Min(open)]
               /\ pc' = [pc EXCEPT ![c] = IF DoneState(c, Min(open)) = "conflict"
                                         THEN "conflict" ELSE "start"]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, oe, snap, req, rep, tries, crashes, late, beats>>

-----------------------------------------------------------------------------
\* Outside events.

\* The store answered and the reply never reached the caller.
LoseReply(c) ==
  /\ pc[c] = "replied"
  /\ pc' = [pc EXCEPT ![c] = "unknown"]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, snap, req, tries, crashes, late, beats>>
\* The caller stopped waiting before the store ran the request; the request
\* is still on its way and may run later (LateRun), or never.
Timeout(c) ==
  /\ pc[c] = "planned"
  /\ late[c] = {}
  /\ Cardinality({x \in Callers : late[x] # {}}) < MaxLate
  /\ late' = [late EXCEPT ![c] = {req[c]}]
  /\ pc' = [pc EXCEPT ![c] = "unknown"]
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, snap, req, rep, tries, crashes, beats>>
\* The caller's process dies; what it had in memory is gone, the manifest and
\* anything it left in flight are not.
Crash(c) ==
  /\ crashes < MaxCrashes
  /\ pc[c] \notin Terminal \cup {"resume"}
  /\ pc' = [pc EXCEPT ![c] = "resume"]
  /\ snap' = [snap EXCEPT ![c] = NoSnap]
  /\ req' = [req EXCEPT ![c] = NoReq]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ crashes' = crashes + 1
  /\ last' = NoLast
  /\ UNCHANGED <<store, hist, prog, part, oe, tries, late, beats>>
\* A second writer outside the programs (the sprint's beat): one guarded,
\* op-less step, read and applied at once, between any caller's read and
\* apply.
Beat ==
  /\ beats < MaxBeats
  /\ \E b \in BeatSet :
       LET t == b[1]  m == b[2]  x == b[3]  f == b[4]  r == rec[t][m] IN
       /\ r.ex /\ r.ep = ep /\ r.pl # NoPlace /\ x[1] \in rows[t][ep]
       /\ Exec([ep |-> ep, op |-> "none", intent |-> NoIntent,
                es |-> <<MoveE(t, m, r.pl, x, f, r.rev)>>, pv |-> "-"], "beat")
  /\ beats' = beats + 1
  /\ UNCHANGED <<prog, pc, part, oe, snap, req, rep, tries, crashes, late>>

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

CallerStep(c) ==
  \/ Read(c) \/ Plan(c) \/ Accept(c) \/ Stale(c) \/ Conflict(c) \/ GiveUp(c)
  \/ Rebuild(c) \/ RetryFresh(c) \/ RetrySame(c) \/ LostCause(c) \/ Resume(c)
StoreStep(c) == Apply(c) \/ Refuse(c) \/ Replay(c) \/ Torn(c) \/ LateRun(c)
Outside(c) == LoseReply(c) \/ Timeout(c) \/ Crash(c)

Next ==
  \/ \E c \in Callers : CallerStep(c) \/ StoreStep(c) \/ Outside(c)
  \/ Beat
  \/ Quiet

Spec == Init /\ [][Next]_vars
\* Fairness on what the callers and the store owe; none on outside events.
LiveSpec == Spec /\ \A c \in Callers : WF_vars(CallerStep(c)) /\ WF_vars(Apply(c) \/ Refuse(c) \/ Replay(c))

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
  /\ pc \in [Callers -> {"start", "read", "planned", "replied", "unknown", "resume"} \cup Terminal]
  /\ tries \in [Callers -> 0..MaxRetries]
  /\ crashes \in 0..MaxCrashes
  /\ beats \in 0..MaxBeats
  /\ oe \in [Callers -> 0..NoEpoch]

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

\* Every entry of a step that changed the store is applied, with its receipt.
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
  /\ R.op # "none" => done'[<<R.ep, R.op>>].has
AppliedIsWhole ==
  [][(last'.out # "-" /\ store' # store) => (last'.out = "apply" /\ Whole(last'.R))]_vars

\* A member's revision moves by exactly one in a step that changes the
\* member (placement, field, existence), and never otherwise.
RevisionMoves ==
  [][\A t \in Tables, m \in Members :
       LET r == rec[t][m]  s == rec'[t][m] IN
       IF s.pl # r.pl \/ s.f # r.f \/ s.ex # r.ex THEN s.rev = r.rev + 1 ELSE s.rev = r.rev]_vars

\* No applied step had a guard that was false at apply time.
GuardsHold(R) ==
  LET we == WEp(R) IN
  /\ (HasAdv(R) => R.es[1].efrom = ep) /\ R.ep = ep
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
                          /\ \A r \in e.del \cap Pre(R, e.t) : \A col \in Cols : cells[e.t][we][<<r, col>>] = {}
GuardsAtApply == [][last'.out = "apply" => GuardsHold(last'.R)]_vars

\* Receipts. Applied(R): R's identity was applied with R's intent before now.
Applied(R) == \E h \in hist : h.ep = R.ep /\ h.op = R.op /\ h.dig = R.intent
AppliedRes(R) == (CHOOSE h \in hist : h.ep = R.ep /\ h.op = R.op /\ h.dig = R.intent).res
ReceiptInv ==
  /\ \A h \in hist : h.n = 1                         \* at most once
  /\ \A k \in Keys : done[k].has =>                  \* a receipt is its own step's
       \E h \in hist : h.ep = k[1] /\ h.op = k[2] /\ h.dig = RcptIntent(done[k]) /\ h.res = done[k].res
  /\ \A c \in Callers : pc[c] = "replied" /\ rep[c].out \in {"apply", "replay"} =>
       \E h \in hist : h.ep = req[c].ep /\ h.op = req[c].op /\ h.res = rep[c].res
\* A statically valid request with an applied identity and the same intent,
\* however it was planned, is answered with the recorded result and writes
\* nothing.
ReceiptAct ==
  (last'.out # "-" /\ last'.R.op # "none" /\ StaticCode(last'.R) = "ok" /\ Applied(last'.R)) =>
    /\ last'.out = "replay"
    /\ last'.res = AppliedRes(last'.R)
    /\ UNCHANGED <<store, hist>>
ReceiptTruth == [][ReceiptAct /\ ReceiptInv']_vars

\* A request whose identity has a receipt of another intent is refused
\* OPCONFLICT and writes nothing.
ConflictOnChangedIntent ==
  [][(last'.out # "-" /\ last'.R.op # "none" /\ StaticCode(last'.R) = "ok" /\ done[RdKey(last'.R)].has
      /\ RcptIntent(done[RdKey(last'.R)]) # last'.R.intent)
     => (last'.out = "refused" /\ last'.code = "OPCONFLICT" /\ UNCHANGED <<store, hist>>)]_vars

\* Epochs: the epoch only moves forward, only by an applied advance; a step
\* writes cells, rows and records only at the epoch it names and its receipt
\* only at its request epoch; an applied identity is never answered STALE
\* (a lost reply at an advance is answered from the receipt).
AdvanceAnswered == last'.code = "STALE" => ~Applied(last'.R)
EpochAct ==
  /\ ep' >= ep
  /\ ep' # ep => (last'.out = "apply" /\ HasAdv(last'.R))
  /\ last'.out = "apply" =>
       LET R == last'.R  we == WEp(R) IN
       /\ \A t \in Tables, e \in Epochs \ {we} : cells'[t][e] = cells[t][e] /\ rows'[t][e] = rows[t][e]
       /\ \A t \in Tables, m \in Members : rec'[t][m] # rec[t][m] => rec'[t][m].ep = we
       /\ \A k \in Keys : done'[k] # done[k] => k = <<R.ep, R.op>>
  /\ AdvanceAnswered
EpochSafe == [][EpochAct]_vars
LostAdvanceAnswered == [][AdvanceAnswered]_vars

\* Every caller comes to an end (under fairness of what callers and the
\* store owe).
Settles == <>(\A c \in Callers : pc[c] \in Terminal)

-----------------------------------------------------------------------------
\* Goals of a verb that the contract gives the caller no way to secure, or
\* disclaims. Each is expected to FAIL on the contract model; the README
\* says which failure is a hole and which is the contract's stated scope.

\* A clear that restores "every row" restores the rows the closing epoch has
\* when it closes.
RestoreIsCurrent ==
  [][(last'.out = "apply" /\ HasAdv(last'.R) /\ last'.R.intent.a.clear)
     => \A t \in Tables : rows'[t][ep'] = rows[t][ep]]_vars
\* A verb that reported a refusal never has that part applied, then or later.
RefusedIsFinal ==
  \A c \in Callers : pc[c] = "refused" =>
    ~\E h \in hist : h.ep = oe[c] /\ h.op = prog[c][part[c]].op
\* A verb that ended STALE has none of its parts applied.
PartsWhole ==
  \A c \in Callers : pc[c] = "stale" =>
    ~\E h \in hist : \E i \in 1..Len(prog[c]) : h.ep = oe[c] /\ h.op = prog[c][i].op

=============================================================================
