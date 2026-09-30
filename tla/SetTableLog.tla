----------------------------- MODULE SetTableLog -----------------------------
\* Layer 2 of the sprint foundation, the log (tset/1), as a state machine,
\* written from the Layer 2 log contract (design/L2-CONTRACT.md, contract
\* revision 1, review candidate) with the coordinator's decisions on it
\* (rowan-3c6ca2c34063), composed with Layer 1 at revision 4 by section 1 of
\* the Layer 1 table contract and the Layer 2 alignments of its section 10
\* (design/L1-CONTRACT.md, Layer 1 revision 4 as amended, sha256
\* e055d64d0f6fc6e4d9bc587bf3a6789c1cacb28cbd6d4fda62a760459907973e). Of
\* those nine alignments the model has four: the pair cursor (4), the fence
\* returning before L.plan with no line (5), the guard-only rowset with no
\* line (6) and the notes op rule for caller notes (9, without the preplan
\* exemption, since there is no preplan here); it also has revision 3's
\* final occupancy. The other revision-4 items are in the NOT modelled list
\* below. Where Layer 1 revision 4 changes what Layer 2 revision 1 says, the
\* model follows Layer 1 revision 4; Layer 2 revision 2 is owed. Read with
\* addendum 1 of the event-driven tick design (a function that errors keeps
\* the writes it made; the order of two writes).
\*
\* The log is written in the same commit as the table plan: one stored line
\* per emitting entry and one per note, each with the stream id <seq>-0 the
\* plan allocated from one XINFO reading, and a history list per card
\* (primary) holding the sequences of the lines that name it. Reads page with
\* a cursor: lines by seq, and cardlines by the pair (list index, item index
\* within that line), an item being one (line, member id) projection. Replay
\* folds the log back into tables and compares them with the store.
\*
\* The variables are the state the two layers own for this purpose: the
\* tables as far as the log needs them (active epoch, rows per epoch, one
\* record per stored ID with its place, score, one application field and
\* revision; a cell is the record's place and score), the receipts with their
\* status (ok or fenced), the log stream of each epoch with its XINFO
\* metadata (entries-added, last-generated-id, whether the key exists), the
\* history lists; and the in-flight state of the callers, of one paged reader
\* and of one replay. The actions are the operations (Step, Advance, Fence,
\* RefusedStep, PrepareRefused, ReceiptReplay, ReadStart and ReadPage,
\* RpStart, RpPage and RpCompare) and the outside events (LostReply, Retry
\* and FenceIt, a runtime error between the table and the log commands, raw
\* writes by something that is not the supported writer).
\*
\* The contract's store never half-applies: the tables, the lines, the
\* history appends and the receipt are one action. Broken names a reversed
\* witness that changes exactly one rule (table below); "none" is the
\* contract.
\*
\*   Broken         rule it breaks                                   fails
\*   counter        the seq comes from a counter key bumped in plan   SeqGapless
\*                  (so a later refusal leaves a gap; W2-2)
\*   noopids        a member line names every id of its entry, also   OneLinePerChange
\*                  ids whose effect was empty (W2-6)
\*   refusedlines   a refused step still appends its lines (W2-5)     RefusedWritesNothing
\*   noscore        a create line carries no scores (W2-3)            ReplayEqualsStore
\*   split          the lines are written by a second action after    ReplayEqualsStore
\*                  the tables, and a crash may drop them (W2-1)
\*   firstonly      history appended for the first id of a set line   HistoryExact
\*                  only (W2-4)
\*   nodedupe       history appended once per id, not once per line   HistoryExact
\*   cursorlimit    a lines page advances its cursor by the limit     CursorNeitherSkipsNorRepeats
\*                  asked for, not by the lines it returned
\*   pairskip       a cardlines page that ends inside a line moves    CursorNeitherSkipsNorRepeats
\*                  the list index on, so the rest of that line's
\*                  items for the card are never emitted
\*   nodone         no receipt for a step with op whose tables did    RetryWritesOnce
\*                  not change (a notes-only step)
\*   opnotes        the revision-1 rule: a step with caller notes     RetryWritesOnce
\*                  and no op is accepted (MCSetTableLogNoOpNote)
\*   aboutmask      the changed-id mask is applied to a member line's AboutExact
\*                  ids but not to its abouts
\*   rowsetline     a guard-only rowset entry writes a line           OneLinePerChange
\*   fenceplan      the fence goes through the log plan (L.plan), so  FenceBypassesLog
\*                  a foreign log head refuses it LOGID
\*   perline        each line of a call samples its own time          TimeFrozen
\*   nocap          no LIMIT on the ids of one line                   LineBound
\*   noceiling      no OVERFLOW at the live sequence ceiling          SeqCeiling
\*   trim           the writer trims the log (XTRIM MAXLEN 2)         LogAppendOnly
\*   shape          the head check accepts any <n>-0 last id and      LogIdGuard
\*                  takes the next seq from it (the tracer's case)
\*
\* Notes require op (L1 revision 4 section 3; decision H2 of
\* rowan-3c6ca2c34063, scoped by rowan-a616f5fb9ebf item 2). Under Layer 2
\* revision 1 alone a notes-only step with no op, resent with the same bytes
\* after a lost reply, wrote its note line again: MCSetTableLogNoOpNote keeps
\* that rule (Broken = "opnotes") and fails RetryWritesOnce in 5 states. The
\* contract now refuses a step with caller notes and no op REQUEST, before
\* the receipt lookup; MCSetTableLogNotesOp runs that program, and a notes
\* step with op, under the contract, and RetryWritesOnce holds. RetryWritesOnce covers caller notes
\* only: every note in this model is a caller's. Notes a trusted preplan
\* appends in an op-less step are exempt from the rule; they rely on the
\* tick never resending a step (it plans fresh), which is the upper layer's
\* rule and is not modelled.
\*
\* Goal configurations, expected to FAIL on the contract model; each
\* counterexample was checked against the contract by hand:
\*
\*   MCSetTableLogReplay    ReplayVerdictSound. A paged replay of the live
\*       epoch, with verbs running, folds the log through the tail of its
\*       first page; a step then writes its line and its table change
\*       together, and the compare calls a correct log and tables "differ".
\*       Decided (rowan-3c6ca2c34063, H1): replay is defined for a closed
\*       epoch, or for the live epoch only while the machine is STOPPED and
\*       no verb runs; it replays into the Mem twin or an owned scratch
\*       store, never the live store; the check's log pass compares per piece
\*       at the piece's observed last. The text is owed to Layer 2 revision
\*       2. MCSetTableLogReplayClosed (an epoch already advanced past) and
\*       MCSetTableLogReplayQuiet (the live epoch, no store action while the
\*       replay runs) pass.
\*   MCSetTableLogError     ReplayEqualsStore. A runtime error after the table
\*       commands and before the log commands keeps the table change with no
\*       line and no receipt. Decided scope (rowan-3c6ca2c34063, FLAGGED, kept
\*       as stated scope): plan proves it cannot come from a refusal, only
\*       from a bug or an OOM; the order stays tables, then log, then done;
\*       the check's compare detects the tables ahead of the log and raises
\*       a judgment, and nothing repairs it silently. Under Layer 1 revision
\*       4 the caller's resend of the same bytes is refused PLACE (its guard
\*       reads the changed tables), which settles nothing; the caller then
\*       fences, and since no receipt was written the fence wins: it records
\*       status fenced and settles the identity as NOT APPLIED while the
\*       step's table change stands. For an errored advance the resend and
\*       the fence are refused STALE instead (the epoch moved and no receipt
\*       exists), which Layer 1 section 5 also counts as settling the
\*       identity, while the advance stands and the new epoch's log does not
\*       start with its advance line. Both are outside Layer 1's no-rollback
\*       promise, and the check's compare is what reports them. (Bench
\*       probes reach both; the model's caller reaches the fence after the
\*       PLACE refusal through a lost reply, since it does not model the
\*       settlement rule; Layer 1's model does.)
\*   MCSetTableLogDelKey    ReplayEqualsStore. A raw DEL of the log key:
\*       replay finds the tables ahead of the log, as section 2 says it will.
\*       Stated scope, not a hole.
\*
\* NOT modelled (the same list is in README-SetTableLog.md and the pull
\* request):
\*   - Byte sizes and budgets other than the id cap and the page limit (a page
\*     may end early, which stands for the byte budget): the 1 MiB line, the
\*     512 KiB cardlines item, the 8 MiB reply and fetch budgets, the XINFO
\*     reservation, fetching each distinct seq once per cardlines call, and
\*     the memory gate.
\*   - The line encoding (n, d, key order, shared/set/unset: one field, set
\*     only), include_meta and field projections, and lines items as {seq, n,
\*     d} with d verbatim.
\*   - Ranks of rows (rows are a set; a rowset compares row names only).
\*   - Several tables (one table, one column), and so the catalog.
\*   - Intent digests and OPCONFLICT (a retry is the same bytes; a fence
\*     carries the same op).
\*   - TWICE and the other static request checks, except notes without op
\*     (REQUEST, modelled) and a fence with entries or notes (written, never
\*     sent): a rowset out of place, an advance without op, field names.
\*   - EPOCHAHEAD, ADVANCE (advance.from), EPOCHGONE and teardown, and
\*     REVISION, count and rcount guards (Layer 1's model has the guards).
\*   - Redis types and ACL (their refusals, and a fence refused by
\*     S.fence_prepare, are the nondeterministic PrepareRefused).
\*   - The enclosing preplan: entries and notes appended in Lua before
\*     S.plan, log_plan.note_seqs aligned with ctx.notes (ReplySeqsTrue does
\*     not check note seqs), and the exemption of preplan notes in an op-less
\*     step from the notes-op rule.
\*   - Atomic lines as a bounded prefix with next and through (only the page
\*     chain is modelled), the last query, L.read_line_at, the done query,
\*     and the read request's wire (kind strings, answers aligned with
\*     queries).
\*   - The caller's side of Layer 1 section 5 (Layer 1's model has it): the
\*     rule that after an unknown outcome only ok, an ok replay, STALE,
\*     OPCONFLICT or fenced settles an identity, done before a replan, and
\*     a new op after a fenced settlement. Here a caller takes any reply as
\*     final and fences only from an unknown outcome.
\*   - Of the replay decision (H1): the STOPPED machine itself (stood for by
\*     no store action while a replay runs), the twin or scratch target, and
\*     the per-piece compare at the observed last.
\*   - Decided for Layer 2 revision 2, pending, not modelled: a note's about
\*     set counting under the 2,000-per-line cap (LineBound counts member ids
\*     only); skipping XINFO when the step writes no line (LogCode refuses
\*     LOGID on a zero-line step at a foreign head); DRIFT when the log key is
\*     absent but a touched history key exists; one RPUSH per history key per
\*     step.
\*   - The first epoch's tables are assumed to be exactly the fold of its log
\*     (decided: the fixture initializer writes rows through a step).
\*   - OneLinePerChange assumes at most one rows entry per step, which every
\*     menu keeps; normalizing repeated row names into their first entry is
\*     modelled in the builder but not checked independently.
\*   - The ABA case of an op-less table step (another caller restores its
\*     precondition before the resend) is outside the menus; it is Layer 1's
\*     op-less rule, not the log's.
\*   - A second advance (epochs are 0..1). DRIFT on an advance whose
\*     new-epoch log key exists is modelled: only something other than the
\*     supported writer can write that key first (epochs are monotone), and
\*     the raw kind "next" does, in MCSetTableLogRaw.
\*   - Liveness: no fairness, and page chains or callers ending are not
\*     checked; "at least one position advances on a nonempty page" is
\*     covered only as a page returning 1..PageLimit items.
EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS
  Members,      \* stored IDs
  Cards,        \* primary IDs, the caller's about; the history lists are per card
  CardOrder,    \* the cards as a sequence: the abouts of a batch cardlines read
  Rows,         \* row names (one table and one column: a cell is its row)
  Callers,      \* callers that run programs
  InitLog,      \* the log of epoch 0 at the start; the tables start as its fold
  Menu,         \* [Callers -> SUBSET Seq(part)]: the programs a caller may run
  MaxEpoch,     \* epochs are 0..MaxEpoch: at most MaxEpoch advances
  MaxCalls,     \* step calls to the store, in all (accepted, refused, replayed, fences)
  MaxLost,      \* replies lost, in all
  PrepFaults,   \* TRUE: a step or fence whose plans pass may be refused by prepare
  Fences,       \* TRUE: a caller whose outcome is unknown may fence its identity
  Ceiling,      \* the live sequence ceiling (9007199254740991 in the contract)
  IdCap,        \* ids in one line (2,000 in the contract)
  AutoId,       \* the id a raw XADD * would give, <ms>-0, below the ceiling
  PageLimit,    \* items one page may return (a budget may cut a page shorter)
  MaxReads,     \* paged reads (lines or cardlines), in all
  MaxReplays,   \* replays of an epoch, in all
  ReplayWhen,   \* "any": a replay may start on any epoch while verbs run; "closed":
                \* only on an epoch already advanced past; "quiet": on any epoch,
                \* and no store action runs while the replay does
  RawKinds,     \* raw writes by something that is not the supported writer:
                \* SUBSET {"auto", "next", "xdel", "delkey"}
  MaxRaw,       \* raw writes, in all
  MaxErrors,    \* runtime errors between the table and the log commands (fact F1), in all
  Broken        \* "none", or the reversed witness (table above)

VARIABLES
  ep, rows, rec,                    \* Layer 1: the active epoch, rows per epoch, records
  done,                             \* Layer 1: receipts, {[ep, op, st, res]}
  log, hist, added, lastId, lkey,   \* Layer 2: streams, history lists, XINFO entries-added and last-generated-id, key present
  ctr, pend,                        \* witnesses only: a counter key (counter), lines waiting for a second write (split)
  prog, pc, part, rqe, rep, fz,     \* callers: program, where it is, which part, the part's request epoch, the reply held,
                                    \* whether the part's request is now its fence
  calls, lost, errs, raws, last,    \* bounds, and the ghost of the last store call
  rd, reads, rp, replays            \* the paged reader and the replay

tables == <<ep, rows, rec>>
store == <<ep, rows, rec, done, log, hist, added, lastId, lkey, ctr, pend>>
callervars == <<prog, pc, part, rqe, rep, fz>>
vars == <<ep, rows, rec, done, log, hist, added, lastId, lkey, ctr, pend,
          prog, pc, part, rqe, rep, fz, calls, lost, errs, raws, last, rd, reads, rp, replays>>

-----------------------------------------------------------------------------
\* Values.

Epochs == 0..MaxEpoch
NoEpoch == MaxEpoch + 1
NoRow == "-"
MemberKinds == {"create", "move", "remove"}
Range(s) == {s[i] : i \in DOMAIN s}
Min(a, b) == IF a < b THEN a ELSE b
IdsOf(s) == [i \in 1..Len(s) |-> s[i].id]
IsPrefix(s, t) == Len(s) <= Len(t) /\ SubSeq(t, 1, Len(s)) = s

\* A record: exists, epoch, revision, place (a row or NoRow), score and the
\* one application field (0 is no score, no field). A removed record keeps
\* ex TRUE and its field, and has no place.
Absent == [ex |-> FALSE, ep |-> 0, rev |-> 0, pl |-> NoRow, sc |-> 0, f |-> 0]

\* An item of a member line: the id, its card (about), after score, after
\* revision, and the field it sets (0: the line does not mention the field).
Item(m, p, sc, rv, f) == [m |-> m, p |-> p, sc |-> sc, rv |-> rv, f |-> f]
\* A line: stream id, kind word, member items, destination (NoRow on a stay
\* and a remove), rows added and deleted, the epochs of an advance, a note's
\* abouts, at_ms; call and rq are ghosts: the store call that wrote it and the
\* caller request it belongs to.
Line(id, k, its, to, add, del, efr, eto, na, ms, call, rq) ==
  [id |-> id, k |-> k, its |-> its, to |-> to, add |-> add, del |-> del,
   efr |-> efr, eto |-> eto, na |-> na, ms |-> ms, call |-> call, rq |-> rq]
Body(k, its, to, add, del, efr, eto, na) ==
  [k |-> k, its |-> its, to |-> to, add |-> add, del |-> del, efr |-> efr, eto |-> eto, na |-> na]

\* An entry of a request, one shape: kind, ids, aligned abouts, expected source
\* row, destination (NoRow: stay), aligned new scores and field values (0:
\* not given), rows added and deleted (a rowset's add is the complete row set
\* it guards). A part of a program is [op, es, ns]: op ("none": no op),
\* entries, and the notes' about sets.
E(k, ids, ab, fr, to, sc, f, add, del) ==
  [k |-> k, ids |-> ids, ab |-> ab, fr |-> fr, to |-> to, sc |-> sc, f |-> f, add |-> add, del |-> del]
Part(op, es, ns) == [op |-> op, es |-> es, ns |-> ns]

NoRes == [ea |-> 0, fs |-> 0, ls |-> 0, n |-> 0]
NoReq == [ep |-> 0, op |-> "none", es |-> <<>>, ns |-> <<>>, fc |-> FALSE, rq |-> <<"-", 0>>]
NoRep == [out |-> "-", code |-> "-", res |-> NoRes]
NoLast == [out |-> "-", R |-> NoReq, code |-> "-", res |-> NoRes, now |-> 0, we |-> 0, n |-> 0]

-----------------------------------------------------------------------------
\* The fold: what replay makes of a log. Lines apply in stream order.

FoldInit == [rows |-> {}, rec |-> [m \in Members |-> Absent]]
InLine(l, m) == \E j \in 1..Len(l.its) : l.its[j].m = m
ItemFor(l, m) == l.its[CHOOSE j \in 1..Len(l.its) : l.its[j].m = m]
FoldRec(r, l, m, e) ==
  LET it == ItemFor(l, m) IN
  CASE l.k = "create" -> [ex |-> TRUE, ep |-> e, rev |-> it.rv, pl |-> l.to, sc |-> it.sc, f |-> it.f]
    [] l.k = "move"   -> [ex |-> r.ex, ep |-> r.ep, rev |-> it.rv,
                          pl |-> IF l.to # NoRow THEN l.to ELSE r.pl,
                          sc |-> it.sc, f |-> IF it.f # 0 THEN it.f ELSE r.f]
    [] l.k = "remove" -> [ex |-> r.ex, ep |-> r.ep, rev |-> it.rv, pl |-> NoRow, sc |-> 0,
                          f |-> IF it.f # 0 THEN it.f ELSE r.f]
ApplyLine(st, l, e) ==
  IF l.k = "rows" THEN [rows |-> (st.rows \cup l.add) \ l.del, rec |-> st.rec]
  ELSE IF l.k \in MemberKinds
    THEN [rows |-> st.rows,
          rec |-> [m \in Members |-> IF InLine(l, m) THEN FoldRec(st.rec[m], l, m, e) ELSE st.rec[m]]]
  ELSE st                                     \* advance and note change no table
RECURSIVE FoldSeq(_, _, _)
FoldSeq(st, s, e) == IF s = <<>> THEN st ELSE FoldSeq(ApplyLine(st, Head(s), e), Tail(s), e)
Fold(s, e) == FoldSeq(FoldInit, s, e)

\* The tables of epoch e as the store holds them: its rows, and the records
\* that belong to it (a record's place and score are its cell).
Proj(e) == [rows |-> rows[e],
            rec |-> [m \in Members |-> IF rec[m].ex /\ rec[m].ep = e THEN rec[m] ELSE Absent]]

\* The cards a line names: a member line's abouts, a note's about set.
TrueAbout(l) == IF l.k = "note" THEN l.na ELSE {l.its[j].p : j \in 1..Len(l.its)}
HistTrue(s, p) == IdsOf(SelectSeq(s, LAMBDA l : p \in TrueAbout(l)))

\* The items a line projects to for card p, in order (L1 section 7): one per
\* member id of the line whose about is p (a (line, member id) projection),
\* or one for a note that names p ("-" in the member's place).
PItems(l, p) ==
  IF l.k = "note" THEN (IF p \in l.na THEN <<"-">> ELSE <<>>)
  ELSE LET js == SelectSeq([j \in 1..Len(l.its) |-> j], LAMBDA j : l.its[j].p = p)
       IN [x \in 1..Len(js) |-> l.its[js[x]].m]

-----------------------------------------------------------------------------
\* A request R = [ep, op, es, ns, fc, rq]: request epoch, op, entries, notes,
\* the original fence bit, and (a ghost) the caller and part it is.

AdvIdx(R) == {i \in 1..Len(R.es) : R.es[i].k = "advance"}
\* The advance is the first non-rowset entry, wherever it sits after a
\* rowset prefix (L1 section 3).
HasAdv(R) == AdvIdx(R) # {}
WEp(R) == IF HasAdv(R) THEN ep + 1 ELSE ep          \* the write epoch
Pre(R) == IF HasAdv(R) THEN {} ELSE rows[ep]         \* pre_rows at the write epoch
RowIdx(R) == {i \in 1..Len(R.es) : R.es[i].k = "rows"}
RowsetIdx(R) == {i \in 1..Len(R.es) : R.es[i].k = "rowset"}
Adds(R) == UNION {R.es[i].add : i \in RowIdx(R)}
Dels(R) == UNION {R.es[i].del : i \in RowIdx(R)}
Prosp(R) == (Pre(R) \cup Adds(R)) \ Dels(R)
\* Rows normalized into their first input entry (L1 section 3).
EffAdd(R, i) == R.es[i].add \ (Pre(R) \cup UNION {R.es[j].add : j \in {x \in RowIdx(R) : x < i}})
EffDel(R, i) == (R.es[i].del \cap Pre(R)) \ UNION {R.es[j].del : j \in {x \in RowIdx(R) : x < i}}

\* Whether an entry changes the member at position j: placement, score or
\* field (L1 section 4). A create and a remove always change it.
IdChanged(e, j) ==
  LET r == rec[e.ids[j]] IN
  CASE e.k = "create" -> TRUE
    [] e.k = "remove" -> TRUE
    [] e.k = "move"   -> \/ (e.to # NoRow /\ e.to # r.pl)
                         \/ (e.sc[j] # 0 /\ e.sc[j] # r.sc)
                         \/ (e.f[j] # 0 /\ e.f[j] # r.f)
    [] OTHER          -> FALSE

MemIdx(R) == {i \in 1..Len(R.es) : R.es[i].k \in MemberKinds}
Named(R, m) == \E i \in MemIdx(R) : m \in Range(R.es[i].ids)
PosOf(e, m) == CHOOSE j \in 1..Len(e.ids) : e.ids[j] = m

-----------------------------------------------------------------------------
\* The store's checks, in the phase order of L1 section 8: static validity,
\* receipt, epoch and advance, the fence's own path, the rowset guards at
\* the request epoch, topology and member guards, then the log plan (Layer
\* 2), then prepare.

Found(R) == R.op # "none" /\ \E d \in done : d.ep = R.ep /\ d.op = R.op
Rcpt(R) == CHOOSE d \in done : d.ep = R.ep /\ d.op = R.op

\* Static validity (L1 sections 3 and 5): a fence carries no entries and no
\* notes; any caller-supplied note requires op (witness opnotes: the
\* revision-1 rule, which accepts it).
StaticCode(R) ==
  IF R.fc THEN (IF R.es # <<>> \/ R.ns # <<>> THEN "REQUEST" ELSE "ok")
  ELSE IF Broken # "opnotes" /\ R.op = "none" /\ R.ns # <<>> THEN "REQUEST"
  ELSE "ok"

\* A fence with no receipt, at the active epoch, takes its own path: FENCE.
OpenCode(R) ==
  IF StaticCode(R) # "ok" THEN StaticCode(R)
  ELSE IF Found(R) THEN "REPLAY"
  ELSE IF R.ep < ep THEN "STALE"
  ELSE IF R.fc THEN "FENCE"
  ELSE IF HasAdv(R) /\ ep = MaxEpoch THEN "OVERFLOW"
  ELSE "ok"

\* Final occupancy (L1 revisions 3 and 4, section 3): the members of a deleted
\* row the step leaves there. A member a changed move or remove of this step
\* takes from the row does not count; a changed stay in the row is taken
\* out here too, and its own entry refuses ROWCONFLICT.
Out(R, r) ==
  {m \in Members : \E i \in MemIdx(R) :
     LET e == R.es[i] IN
     /\ e.k \in {"move", "remove"} /\ e.fr = r /\ m \in Range(e.ids)
     /\ IdChanged(e, PosOf(e, m))}
Left(R, r) == {m \in Members : rec[m].ex /\ rec[m].ep = WEp(R) /\ rec[m].pl = r} \ Out(R, r)
\* A move whose destination is its own row: a stay.
Stays(e) == e.k = "move" /\ (e.to = NoRow \/ e.to = e.fr)

EntryCode(R, e) ==
  CASE e.k = "create" ->
         IF \E j \in 1..Len(e.ids) : rec[e.ids[j]].ex THEN "EXISTS"
         ELSE IF e.to \in Dels(R) THEN "ROWCONFLICT"
         ELSE IF e.to \notin Prosp(R) THEN "NOROW"
         ELSE "ok"
    [] e.k \in {"move", "remove", "guard"} ->
         IF \E j \in 1..Len(e.ids) : ~rec[e.ids[j]].ex THEN "MISSING"
         ELSE IF \E j \in 1..Len(e.ids) : rec[e.ids[j]].ep # WEp(R) THEN "MEMBEREPOCH"
         ELSE IF \E j \in 1..Len(e.ids) : rec[e.ids[j]].pl # e.fr THEN "PLACE"
         ELSE IF e.k = "move" /\ ~Stays(e) /\ e.to \in Dels(R) THEN "ROWCONFLICT"
         ELSE IF Stays(e) /\ e.fr \in Dels(R) /\ \E j \in 1..Len(e.ids) : IdChanged(e, j)
              THEN "ROWCONFLICT"                 \* a changed stay into a deleted row
         ELSE IF e.k = "move" /\ ~Stays(e) /\ e.to \notin Prosp(R) THEN "NOROW"
         ELSE "ok"
    [] OTHER -> "ok"
BadEntries(R) == {i \in 1..Len(R.es) : EntryCode(R, R.es[i]) # "ok"}
GuardCode(R) ==
  IF \E i \in RowsetIdx(R) : R.es[i].add # rows[R.ep] THEN "ROWSET"
  ELSE IF Adds(R) \cap Dels(R) # {} THEN "ROWCONFLICT"
  ELSE IF \E r \in Dels(R) \cap Pre(R) : Left(R, r) # {} THEN "OCCUPIED"
  ELSE IF BadEntries(R) # {}
    THEN EntryCode(R, R.es[CHOOSE i \in BadEntries(R) : \A j \in BadEntries(R) : i <= j])
  ELSE "ok"

-----------------------------------------------------------------------------
\* The log plan (L2 sections 1 and 2): one line per entry with an effective
\* change, in input order, then one per note; ids from one XINFO reading. A
\* guard and a guard-only rowset emit no line.

\* The positions a member line names: the effective changed ids, in input
\* order (witness noopids: every id of the entry).
LinePos(e) == SelectSeq([j \in 1..Len(e.ids) |-> j], LAMBDA j : Broken = "noopids" \/ IdChanged(e, j))
AfterSc(e, j) ==
  CASE e.k = "create" -> IF Broken = "noscore" THEN 0 ELSE e.sc[j]
    [] e.k = "move"   -> IF e.sc[j] # 0 THEN e.sc[j] ELSE rec[e.ids[j]].sc
    [] OTHER          -> 0
AfterRev(e, j) ==
  IF e.k = "create" THEN 1
  ELSE IF IdChanged(e, j) THEN rec[e.ids[j]].rev + 1 ELSE rec[e.ids[j]].rev
SetF(e, j) == IF e.f[j] # 0 /\ (e.k = "create" \/ e.f[j] # rec[e.ids[j]].f) THEN e.f[j] ELSE 0
\* One ordered changed-id mask over every aligned output, the abouts
\* included (witness aboutmask: the abouts are taken unmasked, by line
\* position).
MItems(e) ==
  LET P == LinePos(e) IN
  [x \in 1..Len(P) |-> Item(e.ids[P[x]], IF Broken = "aboutmask" THEN e.ab[x] ELSE e.ab[P[x]],
                            AfterSc(e, P[x]), AfterRev(e, P[x]), SetF(e, P[x]))]

Effective(R, i) ==
  LET e == R.es[i] IN
  CASE e.k \in MemberKinds -> \E j \in 1..Len(e.ids) : IdChanged(e, j)
    [] e.k = "rows"        -> EffAdd(R, i) # {} \/ EffDel(R, i) # {}
    [] e.k = "advance"     -> TRUE
    [] e.k = "rowset"      -> Broken = "rowsetline"
    [] OTHER               -> FALSE              \* a guard emits no line
EffSeq(R) == SelectSeq([i \in 1..Len(R.es) |-> i], LAMBDA i : Effective(R, i))
EntryBody(R, i) ==
  LET e == R.es[i] IN
  CASE e.k \in MemberKinds -> Body(e.k, MItems(e), IF e.k = "remove" THEN NoRow ELSE e.to, {}, {}, 0, 0, {})
    [] e.k = "rows"        -> Body("rows", <<>>, NoRow, EffAdd(R, i), EffDel(R, i), 0, 0, {})
    [] e.k = "advance"     -> Body("advance", <<>>, NoRow, {}, {}, ep, ep + 1, {})
    [] e.k = "rowset"      -> Body("rowset", <<>>, NoRow, {}, {}, 0, 0, {})
Bodies(R) ==
  [x \in 1..Len(EffSeq(R)) |-> EntryBody(R, EffSeq(R)[x])]
  \o [n \in 1..Len(R.ns) |-> Body("note", <<>>, NoRow, {}, {}, 0, 0, R.ns[n])]

\* XINFO: last-generated-id must be exactly <entries-added>-0, within the
\* ceiling. The next seq is entries-added + 1, or 1 on an absent key.
HeadOK(e) == lastId[e] = added[e] /\ added[e] <= Ceiling
Base(e) ==
  IF ~lkey[e] THEN 0
  ELSE IF Broken = "shape" THEN lastId[e]
  ELSE IF Broken = "counter" THEN ctr[e]
  ELSE added[e]
LogCode(R) ==
  LET we == WEp(R)  B == Bodies(R) IN
  IF Broken # "nocap" /\ \E x \in 1..Len(B) : Len(B[x].its) > IdCap THEN "LIMIT"
  ELSE IF HasAdv(R) /\ lkey[we] THEN "DRIFT"
  ELSE IF lkey[we] /\ Broken # "shape" /\ ~HeadOK(we) THEN "LOGID"
  ELSE IF Broken # "noceiling" /\ Base(we) + Len(B) > Ceiling THEN "OVERFLOW"
  ELSE "ok"

\* The verdict: "REPLAY", "FENCE", "ok", or a refusal code. The fence returns
\* before any planner (witness fenceplan: it passes the log plan's checks).
Verdict(R) ==
  LET o == OpenCode(R) IN
  IF o = "FENCE" THEN (IF Broken = "fenceplan" /\ LogCode(R) # "ok" THEN LogCode(R) ELSE "FENCE")
  ELSE IF o # "ok" THEN o
  ELSE LET g == GuardCode(R) IN IF g # "ok" THEN g ELSE LogCode(R)

-----------------------------------------------------------------------------
\* The writes of an accepted step.

NewRec(R, m) ==
  IF ~Named(R, m) THEN rec[m]
  ELSE LET e == R.es[CHOOSE x \in MemIdx(R) : m \in Range(R.es[x].ids)]
           j == PosOf(e, m)
           r == rec[m]
       IN CASE e.k = "create" -> [ex |-> TRUE, ep |-> WEp(R), rev |-> 1, pl |-> e.to, sc |-> e.sc[j], f |-> e.f[j]]
            [] e.k = "move"   -> IF ~IdChanged(e, j) THEN r
                                 ELSE [ex |-> TRUE, ep |-> r.ep, rev |-> r.rev + 1,
                                       pl |-> IF e.to # NoRow THEN e.to ELSE r.pl,
                                       sc |-> IF e.sc[j] # 0 THEN e.sc[j] ELSE r.sc,
                                       f |-> IF e.f[j] # 0 THEN e.f[j] ELSE r.f]
            [] e.k = "remove" -> [ex |-> TRUE, ep |-> r.ep, rev |-> r.rev + 1, pl |-> NoRow, sc |-> 0,
                                  f |-> IF e.f[j] # 0 THEN e.f[j] ELSE r.f]
TablesChange(R) == WEp(R) # ep \/ Prosp(R) # rows[WEp(R)] \/ \E m \in Members : NewRec(R, m) # rec[m]

\* History appends of the new lines L for card p: the seq once per line that
\* names p (deduplicated per line), in order.
Appends(l, p) ==
  IF l.k # "note" /\ Broken = "nodedupe" THEN Cardinality({j \in 1..Len(l.its) : l.its[j].p = p})
  ELSE IF l.k # "note" /\ Broken = "firstonly" THEN (IF Len(l.its) > 0 /\ l.its[1].p = p THEN 1 ELSE 0)
  ELSE IF p \in TrueAbout(l) THEN 1 ELSE 0
RECURSIVE HistOf(_, _)
HistOf(L, p) == IF L = <<>> THEN <<>>
                ELSE [j \in 1..Appends(Head(L), p) |-> Head(L).id] \o HistOf(Tail(L), p)

\* The lines of one call, stamped: ids from base, one at_ms for the call.
Stamp(B, base, now, rq) ==
  [x \in 1..Len(B) |-> Line(base + x, B[x].k, B[x].its, B[x].to, B[x].add, B[x].del, B[x].efr, B[x].eto,
                            B[x].na, IF Broken = "perline" THEN now + x - 1 ELSE now, now, rq)]
Trim(s) == IF Broken = "trim" /\ Len(s) > 2 THEN SubSeq(s, Len(s) - 1, Len(s)) ELSE s

\* Append the lines L (stamped from base) to epoch e: XADD <seq>-0 per line,
\* RPUSH per card per line.
AppendLog(e, L, base) ==
  /\ log' = [log EXCEPT ![e] = Trim(@ \o L)]
  /\ hist' = [hist EXCEPT ![e] = [p \in Cards |-> @[p] \o HistOf(L, p)]]
  /\ added' = [added EXCEPT ![e] = @ + Len(L)]
  /\ lastId' = IF L # <<>> THEN [lastId EXCEPT ![e] = base + Len(L)] ELSE lastId
  /\ lkey' = IF L # <<>> THEN [lkey EXCEPT ![e] = TRUE] ELSE lkey

ResOf(R) ==
  LET n == Len(Bodies(R))  base == Base(WEp(R)) IN
  IF Broken = "split" \/ n = 0 THEN [ea |-> WEp(R), fs |-> 0, ls |-> 0, n |-> n]
  ELSE [ea |-> WEp(R), fs |-> base + 1, ls |-> base + n, n |-> n]
\* A fence's result: the original epoch before and after, both seqs 0.
FenceRes(R) == [ea |-> R.ep, fs |-> 0, ls |-> 0, n |-> 0]

\* One prepared list, one commit: tables, lines and receipt together. With
\* withLog FALSE the executor stopped after the table commands (MaxErrors).
Commit(R, now, withLog, withDone) ==
  LET we == WEp(R)  B == Bodies(R)  base == Base(we)  L == Stamp(B, base, now, R.rq) IN
  /\ ep' = we
  /\ rows' = [rows EXCEPT ![we] = Prosp(R)]
  /\ rec' = [m \in Members |-> NewRec(R, m)]
  /\ IF withLog /\ Broken # "split"
       THEN AppendLog(we, L, base) /\ UNCHANGED pend
     ELSE IF withLog
       THEN /\ UNCHANGED <<log, hist, added, lastId, lkey>>
            /\ pend' = IF B # <<>> THEN Append(pend, [e |-> we, B |-> B, now |-> now, rq |-> R.rq]) ELSE pend
     ELSE UNCHANGED <<log, hist, added, lastId, lkey, pend>>
  /\ ctr' = IF Broken = "counter" THEN [ctr EXCEPT ![we] = @ + Len(B)] ELSE ctr
  /\ done' = IF withDone /\ R.op # "none" /\ ~(Broken = "nodone" /\ ~TablesChange(R))
             THEN done \cup {[ep |-> R.ep, op |-> R.op, st |-> "ok", res |-> ResOf(R)]}
             ELSE done

-----------------------------------------------------------------------------
\* Callers. A caller runs one program: parts in order, each one step call.
\* A part's request epoch is fixed at its first call and kept by its retries
\* (a retry is the same bytes) and by its fence (the same original epoch and
\* op, no entries, no notes, fence:true).

Cur(c) == prog[c][part[c]]
ReqOf(c) ==
  LET e == IF rqe[c] = NoEpoch THEN ep ELSE rqe[c] IN
  IF fz[c]
    THEN [ep |-> e, op |-> Cur(c).op, es |-> <<>>, ns |-> <<>>, fc |-> TRUE, rq |-> <<c, part[c]>>]
    ELSE [ep |-> e, op |-> Cur(c).op, es |-> Cur(c).es, ns |-> Cur(c).ns, fc |-> FALSE, rq |-> <<c, part[c]>>]

\* The caller's side of every store call.
Called(c, to) ==
  /\ pc[c] = "ready"
  /\ calls < MaxCalls
  /\ calls' = calls + 1
  /\ rqe' = [rqe EXCEPT ![c] = ReqOf(c).ep]
  /\ pc' = [pc EXCEPT ![c] = to]
  /\ UNCHANGED <<prog, part, fz, lost, raws, rd, reads, rp, replays>>
Said(R, out, code, res, n) ==
  last' = [out |-> out, R |-> R, code |-> code, res |-> res, now |-> calls + 1, we |-> WEp(R), n |-> n]
Reply(c, out, code, res) == rep' = [rep EXCEPT ![c] = [out |-> out, code |-> code, res |-> res]]

\* An accepted step that is not an advance: the table changes and its lines
\* are written, together.
Step(c) ==
  LET R == ReqOf(c) IN
  /\ Called(c, "replied")
  /\ Verdict(R) = "ok" /\ ~HasAdv(R)
  /\ Commit(R, calls + 1, TRUE, TRUE)
  /\ Reply(c, "apply", "ok", ResOf(R))
  /\ Said(R, "apply", "ok", ResOf(R), Len(Bodies(R)))
  /\ UNCHANGED errs
\* An accepted advance: the epoch moves; the advance line is the first line of
\* the new epoch's log, followed by the restored rows and the other entries.
Advance(c) ==
  LET R == ReqOf(c) IN
  /\ Called(c, "replied")
  /\ Verdict(R) = "ok" /\ HasAdv(R)
  /\ Commit(R, calls + 1, TRUE, TRUE)
  /\ Reply(c, "apply", "ok", ResOf(R))
  /\ Said(R, "apply", "ok", ResOf(R), Len(Bodies(R)))
  /\ UNCHANGED errs
\* A fence that wins (L1 sections 1.1 and 5): S.fence_prepare, then commit,
\* before any preplan or planner: only the receipt, status fenced, both seqs
\* 0, no line.
Fence(c) ==
  LET R == ReqOf(c) IN
  /\ Called(c, "replied")
  /\ Verdict(R) = "FENCE"
  /\ done' = done \cup {[ep |-> R.ep, op |-> R.op, st |-> "fenced", res |-> FenceRes(R)]}
  /\ UNCHANGED <<ep, rows, rec, log, hist, added, lastId, lkey, ctr, pend>>
  /\ Reply(c, "fenced", "fenced", FenceRes(R))
  /\ Said(R, "fenced", "fenced", FenceRes(R), 0)
  /\ UNCHANGED errs
\* A refusal: nothing changes (witness refusedlines: the lines are appended).
RefusedStep(c) ==
  LET R == ReqOf(c)  code == Verdict(R) IN
  /\ Called(c, "replied")
  /\ code \notin {"ok", "REPLAY", "FENCE"}
  /\ IF Broken = "refusedlines" /\ code \in {"PLACE", "EXISTS", "MISSING", "OCCUPIED", "NOROW"}
       THEN /\ AppendLog(WEp(R), Stamp(Bodies(R), Base(WEp(R)), calls + 1, R.rq), Base(WEp(R)))
            /\ UNCHANGED <<ep, rows, rec, done, ctr, pend>>
       ELSE UNCHANGED store
  /\ Reply(c, "refused", code, NoRes)
  /\ Said(R, "refused", code, NoRes, 0)
  /\ UNCHANGED errs
\* Prepare refuses a step whose plans passed, or a fence (a wrong type, an
\* ACL, a budget found in the complete list): nothing changes. Witness
\* counter: the counter key was already bumped in plan.
PrepareRefused(c) ==
  LET R == ReqOf(c) IN
  /\ PrepFaults
  /\ Called(c, "replied")
  /\ Verdict(R) \in {"ok", "FENCE"}
  /\ UNCHANGED <<ep, rows, rec, done, log, hist, added, lastId, lkey, pend>>
  /\ ctr' = IF Broken = "counter" THEN [ctr EXCEPT ![WEp(R)] = @ + Len(Bodies(R))] ELSE ctr
  /\ Reply(c, "refused", "PREPARE", NoRes)
  /\ Said(R, "refused", "PREPARE", NoRes, 0)
  /\ UNCHANGED errs
\* The receipt matched: the recorded reply with its status, no plan, no write.
ReceiptReplay(c) ==
  LET R == ReqOf(c) IN
  /\ Called(c, "replied")
  /\ Verdict(R) = "REPLAY"
  /\ UNCHANGED store
  /\ Reply(c, "replay", Rcpt(R).st, Rcpt(R).res)
  /\ Said(R, "replay", Rcpt(R).st, Rcpt(R).res, 0)
  /\ UNCHANGED errs
\* Fact F1: an unexpected runtime error after the table commands and before
\* the log commands keeps the earlier writes. The contract promises no
\* rollback (L2 section 0); the caller hears an error of unknown outcome.
ErrorBetween(c) ==
  LET R == ReqOf(c) IN
  /\ errs < MaxErrors
  /\ Called(c, "unknown")
  /\ Verdict(R) = "ok" /\ Bodies(R) # <<>>
  /\ Commit(R, calls + 1, FALSE, FALSE)
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ errs' = errs + 1
  /\ Said(R, "error", "ERROR", NoRes, 0)

\* The caller's side of a reply.
Quietly == last' = NoLast
LostReply(c) ==
  /\ pc[c] = "replied" /\ lost < MaxLost
  /\ pc' = [pc EXCEPT ![c] = "unknown"]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ lost' = lost + 1
  /\ Quietly
  /\ UNCHANGED <<store, prog, part, rqe, fz, calls, errs, raws, rd, reads, rp, replays>>
\* Send the same bytes again (a fence's bytes, once the part is fencing).
Retry(c) ==
  /\ pc[c] = "unknown"
  /\ pc' = [pc EXCEPT ![c] = "ready"]
  /\ Quietly
  /\ UNCHANGED <<store, prog, part, rqe, rep, fz, calls, lost, errs, raws, rd, reads, rp, replays>>
\* Fence the identity instead (L1 section 5): the same original epoch and op,
\* no entries, no notes, fence:true. Only a part with op can be fenced.
FenceIt(c) ==
  /\ Fences /\ pc[c] = "unknown" /\ ~fz[c] /\ Cur(c).op # "none"
  /\ pc' = [pc EXCEPT ![c] = "ready"]
  /\ fz' = [fz EXCEPT ![c] = TRUE]
  /\ Quietly
  /\ UNCHANGED <<store, prog, part, rqe, rep, calls, lost, errs, raws, rd, reads, rp, replays>>
GiveUp(c) ==
  /\ \/ pc[c] = "unknown"
     \/ pc[c] = "ready" /\ calls = MaxCalls
  /\ pc' = [pc EXCEPT ![c] = "done"]
  /\ Quietly
  /\ UNCHANGED <<store, prog, part, rqe, rep, fz, calls, lost, errs, raws, rd, reads, rp, replays>>
AcceptReply(c) ==
  /\ pc[c] = "replied"
  /\ IF part[c] = Len(prog[c])
       THEN pc' = [pc EXCEPT ![c] = "done"] /\ UNCHANGED part
       ELSE pc' = [pc EXCEPT ![c] = "ready"] /\ part' = [part EXCEPT ![c] = @ + 1]
  /\ rqe' = [rqe EXCEPT ![c] = NoEpoch]
  /\ rep' = [rep EXCEPT ![c] = NoRep]
  /\ fz' = [fz EXCEPT ![c] = FALSE]
  /\ Quietly
  /\ UNCHANGED <<store, prog, calls, lost, errs, raws, rd, reads, rp, replays>>

-----------------------------------------------------------------------------
\* Witness split only: the lines written by a second action, or lost when the
\* process dies before it.
FlushLines ==
  /\ Broken = "split" /\ pend # <<>>
  /\ LET p == Head(pend)  base == Base(p.e) IN AppendLog(p.e, Stamp(p.B, base, p.now, p.rq), base)
  /\ pend' = Tail(pend)
  /\ Quietly
  /\ UNCHANGED <<ep, rows, rec, done, ctr, callervars, calls, lost, errs, raws, rd, reads, rp, replays>>
DropLines ==
  /\ Broken = "split" /\ pend # <<>>
  /\ pend' = Tail(pend)
  /\ Quietly
  /\ UNCHANGED <<ep, rows, rec, done, log, hist, added, lastId, lkey, ctr, callervars, calls, lost, errs, raws, rd, reads, rp, replays>>

\* Raw writes by something that is not the supported writer (section 2): an
\* XADD * of one entry on the active epoch's log key ("auto") or on the next
\* epoch's ("next", a prepopulated new-epoch key that an advance must refuse
\* DRIFT), an XDEL of one entry, a DEL of the log key.
RawUnchanged == UNCHANGED <<ep, rows, rec, done, hist, ctr, pend, callervars, calls, lost, errs, rd, reads, rp, replays>>
RawXAdd(e) ==
  /\ log' = [log EXCEPT ![e] = Append(@, Line(AutoId, "note", <<>>, NoRow, {}, {}, 0, 0, {}, 0, 0, <<"raw", 0>>))]
  /\ added' = [added EXCEPT ![e] = @ + 1]
  /\ lastId' = [lastId EXCEPT ![e] = AutoId]
  /\ lkey' = [lkey EXCEPT ![e] = TRUE]
RawAuto ==
  /\ "auto" \in RawKinds /\ raws < MaxRaw /\ raws' = raws + 1
  /\ RawXAdd(ep)
  /\ Quietly /\ RawUnchanged
RawNext ==
  /\ "next" \in RawKinds /\ raws < MaxRaw /\ ep < MaxEpoch /\ raws' = raws + 1
  /\ RawXAdd(ep + 1)
  /\ Quietly /\ RawUnchanged
RawXDel ==
  /\ "xdel" \in RawKinds /\ raws < MaxRaw /\ raws' = raws + 1
  /\ \E e \in Epochs : \E i \in 1..Len(log[e]) :
       log' = [log EXCEPT ![e] = SubSeq(@, 1, i - 1) \o SubSeq(@, i + 1, Len(@))]
  /\ UNCHANGED <<added, lastId, lkey>>
  /\ Quietly /\ RawUnchanged
RawDelKey ==
  /\ "delkey" \in RawKinds /\ raws < MaxRaw /\ raws' = raws + 1
  /\ \E e \in Epochs :
       /\ lkey[e]
       /\ log' = [log EXCEPT ![e] = <<>>]
       /\ added' = [added EXCEPT ![e] = 0]
       /\ lastId' = [lastId EXCEPT ![e] = 0]
       /\ lkey' = [lkey EXCEPT ![e] = FALSE]
  /\ Quietly /\ RawUnchanged

-----------------------------------------------------------------------------
\* Reads (L1 section 7, L2 section 4). One reader, one chain of pages:
\* lines (after_seq 0, through_seq fixed at the first page) or a batch
\* cardlines over CardOrder (each slot's high-water fixed at the first page,
\* slots drained in input order, the page limit counting projected (line,
\* member id) items across slots). A page may end early, as when the next
\* item does not fit the budget, and then it may end inside a line; a BUDGET
\* answer with no item changes nothing and is not an action here.

LastSeq(e) == IF lkey[e] THEN lastId[e] ELSE 0     \* the query last
HasLine(e, q) == \E i \in 1..Len(log[e]) : log[e][i].id = q
LineAt(e, q) == log[e][CHOOSE i \in 1..Len(log[e]) : log[e][i].id = q]
\* A slot: its card, its position, the pair (nx, it) of zero-based list index
\* and item index within that line, LLEN at the first page (through_index +
\* 1), the items emitted so far as <<seq, member>>, and (a ghost) the items
\* the chain owes: the projections for the card of the lines up to the tail
\* at the first page, read from the log, not from the history list.
Slot(p, n, want) == [p |-> p, nx |-> 0, it |-> 0, n |-> n, got |-> <<>>, want |-> want]
RECURSIVE OwedOf(_, _)
OwedOf(s, p) ==
  IF s = <<>> THEN <<>>
  ELSE LET its == PItems(Head(s), p) IN
       [x \in 1..Len(its) |-> <<Head(s).id, its[x]>>] \o OwedOf(Tail(s), p)
Owed(e, p) == OwedOf(SelectSeq(log[e], LAMBDA l : l.id <= LastSeq(e)), p)
\* want (a ghost): the seqs a lines chain owes, fixed at its first page.
NoRd == [st |-> "idle", md |-> "-", e |-> 0, thr |-> 0, nx |-> 0, got |-> <<>>, want |-> <<>>, sl |-> <<>>]
ReadUnchanged == UNCHANGED <<store, callervars, calls, lost, errs, raws, rp, replays>>

ReadStart ==
  /\ rd.st = "idle" /\ reads < MaxReads /\ reads' = reads + 1
  /\ \E e \in 0..ep, md \in {"lines", "card"} :
       rd' = IF lkey[e] /\ ~HeadOK(e) THEN [NoRd EXCEPT !.st = "refused", !.md = md, !.e = e]
             ELSE IF md = "lines"
               THEN [NoRd EXCEPT !.st = IF LastSeq(e) = 0 THEN "done" ELSE "open", !.md = md, !.e = e,
                                 !.thr = LastSeq(e), !.want = [i \in 1..LastSeq(e) |-> i]]
             ELSE LET sl == [i \in 1..Len(CardOrder) |-> Slot(CardOrder[i], Len(hist[e][CardOrder[i]]), Owed(e, CardOrder[i]))] IN
                  [NoRd EXCEPT !.st = IF \A i \in 1..Len(sl) : sl[i].n = 0 THEN "done" ELSE "open",
                               !.md = md, !.e = e, !.sl = sl]
  /\ Quietly /\ ReadUnchanged

ReadLines ==
  /\ rd.st = "open" /\ rd.md = "lines"
  /\ \E k \in 1..PageLimit :
       LET hi == Min(rd.nx + k, rd.thr)
           qs == [i \in 1..(hi - rd.nx) |-> rd.nx + i]
           nx2 == IF Broken = "cursorlimit" THEN rd.nx + PageLimit ELSE hi
       IN rd' = IF \E i \in 1..Len(qs) : ~HasLine(rd.e, qs[i])
                THEN [rd EXCEPT !.st = "refused"]                  \* a hole: LOGID
                ELSE [rd EXCEPT !.got = @ \o qs, !.nx = nx2,
                                !.st = IF nx2 >= rd.thr THEN "done" ELSE "open"]
  /\ UNCHANGED reads /\ Quietly /\ ReadUnchanged

\* Drain up to r items from slot s of epoch e, whole items only, resuming at
\* the pair (nx, it): [s, r, bad] is the slot after, the items the page has
\* left, and whether a line of the history is not in the log (a hole). A
\* position moves only for complete emitted items; a page that ends inside a
\* line leaves the pair at the next item of that line (witness pairskip:
\* the list index moves on and the item index resets).
RECURSIVE DrainSlot(_, _, _)
DrainSlot(e, s, r) ==
  IF r = 0 \/ s.nx >= s.n THEN [s |-> s, r |-> r, bad |-> FALSE]
  ELSE LET q == hist[e][s.p][s.nx + 1] IN
       IF ~HasLine(e, q) THEN [s |-> s, r |-> r, bad |-> TRUE]
       ELSE LET its == PItems(LineAt(e, q), s.p)
                t == Min(r, Len(its) - s.it)
                out == [x \in 1..t |-> <<q, its[s.it + x]>>]
                s2 == IF s.it + t >= Len(its) \/ Broken = "pairskip"
                        THEN [s EXCEPT !.nx = @ + 1, !.it = 0, !.got = @ \o out]
                        ELSE [s EXCEPT !.it = @ + t, !.got = @ \o out]
            IN DrainSlot(e, s2, r - t)
RECURSIVE Drain(_, _, _, _)
Drain(e, sl, i, r) ==
  IF i > Len(sl) THEN [sl |-> sl, bad |-> FALSE]
  ELSE LET d == DrainSlot(e, sl[i], r) IN
       IF d.bad THEN [sl |-> sl, bad |-> TRUE]
       ELSE Drain(e, [sl EXCEPT ![i] = d.s], i + 1, d.r)
ReadCards ==
  /\ rd.st = "open" /\ rd.md = "card"
  /\ \E k \in 1..PageLimit :
       LET d == Drain(rd.e, rd.sl, 1, k) IN
       rd' = IF d.bad
             THEN [rd EXCEPT !.st = "refused"]                    \* a hole: LOGID
             ELSE [rd EXCEPT !.sl = d.sl,
                             !.st = IF \A i \in 1..Len(d.sl) : d.sl[i].nx >= d.sl[i].n THEN "done" ELSE "open"]
  /\ UNCHANGED reads /\ Quietly /\ ReadUnchanged
ReadPage == ReadLines \/ ReadCards

\* Replay (L2 section 6): read the log in pages from seq 1 through the tail
\* seen at the first page, fold it into reconstructed tables, compare them
\* with the store. truth is a ghost: whether the whole log and the tables
\* really agree when the comparison is made.
NoRp == [st |-> "idle", e |-> 0, thr |-> 0, nx |-> 0, acc |-> FoldInit, verdict |-> "-", truth |-> FALSE]
RpUnchanged == UNCHANGED <<store, callervars, calls, lost, errs, raws, rd, reads>>
RpStart ==
  /\ rp.st = "idle" /\ replays < MaxReplays /\ replays' = replays + 1
  /\ \E e \in 0..ep :
       /\ ReplayWhen = "closed" => e < ep
       /\ rp' = IF lkey[e] /\ ~HeadOK(e) THEN [NoRp EXCEPT !.st = "refused", !.e = e]
                ELSE [NoRp EXCEPT !.st = "run", !.e = e, !.thr = LastSeq(e)]
  /\ Quietly /\ RpUnchanged
RpPage ==
  /\ rp.st = "run" /\ rp.nx < rp.thr
  /\ \E k \in 1..PageLimit :
       LET hi == Min(rp.nx + k, rp.thr)
           qs == [i \in 1..(hi - rp.nx) |-> rp.nx + i]
       IN rp' = IF \E i \in 1..Len(qs) : ~HasLine(rp.e, qs[i])
                THEN [rp EXCEPT !.st = "refused"]
                ELSE [rp EXCEPT !.acc = FoldSeq(@, [i \in 1..Len(qs) |-> LineAt(rp.e, qs[i])], rp.e), !.nx = hi]
  /\ UNCHANGED replays /\ Quietly /\ RpUnchanged
RpCompare ==
  /\ rp.st = "run" /\ rp.nx >= rp.thr
  /\ rp' = [rp EXCEPT !.st = "done",
                      !.verdict = IF rp.acc = Proj(rp.e) THEN "equal" ELSE "differ",
                      !.truth = (Fold(log[rp.e], rp.e) = Proj(rp.e))]
  /\ UNCHANGED replays /\ Quietly /\ RpUnchanged
Replay == RpStart \/ RpPage \/ RpCompare

\* The live epoch is replayed only while no verb runs (decision H1, the
\* STOPPED arm): with ReplayWhen "quiet" no store action runs while a replay
\* does.
Quiesced == ReplayWhen # "quiet" \/ rp.st # "run"

\* Every caller has ended and nothing is in flight: the system is at rest.
Quiet ==
  /\ \A c \in Callers : pc[c] = "done"
  /\ rd.st # "open" /\ rp.st # "run" /\ pend = <<>>
  /\ UNCHANGED vars

-----------------------------------------------------------------------------

Init ==
  /\ ep = 0
  /\ rows = [e \in Epochs |-> IF e = 0 THEN Fold(InitLog, 0).rows ELSE {}]
  /\ rec = Fold(InitLog, 0).rec
  /\ done = {}
  /\ log = [e \in Epochs |-> IF e = 0 THEN InitLog ELSE <<>>]
  /\ hist = [e \in Epochs |-> [p \in Cards |-> IF e = 0 THEN HistTrue(InitLog, p) ELSE <<>>]]
  /\ added = [e \in Epochs |-> IF e = 0 THEN Len(InitLog) ELSE 0]
  /\ lastId = [e \in Epochs |-> IF e = 0 THEN Len(InitLog) ELSE 0]
  /\ lkey = [e \in Epochs |-> e = 0 /\ InitLog # <<>>]
  /\ ctr = [e \in Epochs |-> IF e = 0 THEN Len(InitLog) ELSE 0]
  /\ pend = <<>>
  /\ prog \in {p \in [Callers -> UNION {Menu[c] : c \in Callers}] : \A c \in Callers : p[c] \in Menu[c]}
  /\ pc = [c \in Callers |-> "ready"]
  /\ part = [c \in Callers |-> 1]
  /\ rqe = [c \in Callers |-> NoEpoch]
  /\ rep = [c \in Callers |-> NoRep]
  /\ fz = [c \in Callers |-> FALSE]
  /\ calls = 0 /\ lost = 0 /\ errs = 0 /\ raws = 0
  /\ last = NoLast
  /\ rd = NoRd /\ reads = 0
  /\ rp = NoRp /\ replays = 0

StoreAct(c) ==
  /\ Quiesced
  /\ \/ Step(c) \/ Advance(c) \/ Fence(c) \/ RefusedStep(c) \/ PrepareRefused(c)
     \/ ReceiptReplay(c) \/ ErrorBetween(c)
CallerAct(c) == LostReply(c) \/ Retry(c) \/ FenceIt(c) \/ GiveUp(c) \/ AcceptReply(c)
Next ==
  \/ \E c \in Callers : StoreAct(c) \/ CallerAct(c)
  \/ ReadStart \/ ReadPage
  \/ Replay
  \/ FlushLines \/ DropLines
  \/ RawAuto \/ RawNext \/ RawXDel \/ RawDelKey
  \/ Quiet

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
\* What the contract guarantees, stated apart from the builder so that a
\* witness that changes the builder is caught.

TypeOK ==
  /\ ep \in Epochs
  /\ rows \in [Epochs -> SUBSET Rows]
  /\ \A m \in Members : rec[m].ex \in BOOLEAN /\ rec[m].ep \in Epochs /\ rec[m].rev \in Nat
                        /\ rec[m].pl \in Rows \cup {NoRow}
  /\ \A d \in done : d.st \in {"ok", "fenced"}
  /\ \A e \in Epochs : added[e] \in Nat /\ lastId[e] \in Nat /\ lkey[e] \in BOOLEAN
  /\ DOMAIN hist = Epochs /\ \A e \in Epochs : DOMAIN hist[e] = Cards
  /\ pc \in [Callers -> {"ready", "replied", "unknown", "done"}]
  /\ fz \in [Callers -> BOOLEAN]
  /\ calls \in 0..MaxCalls /\ lost \in 0..MaxLost /\ errs \in 0..MaxErrors /\ raws \in 0..MaxRaw
  /\ rd.st \in {"idle", "open", "done", "refused"} /\ rp.st \in {"idle", "run", "done", "refused"}

\* Line q of an epoch has stream id q-0: no gap, no counter key; the XINFO
\* metadata agrees (entries-added is the count, last-generated-id the last
\* seq) and the key exists exactly when a line does.
SeqGapless ==
  \A e \in Epochs :
    /\ \A i \in 1..Len(log[e]) : log[e][i].id = i
    /\ added[e] = Len(log[e])
    /\ lkey[e] = (Len(log[e]) > 0)
    /\ lkey[e] => lastId[e] = added[e]

\* The fold of each epoch's log equals that epoch's tables: cells (place and
\* score), records (place, field, revision, epoch) and rows.
ReplayEqualsStore == \A e \in Epochs : Fold(log[e], e) = Proj(e)

\* Each card's history list is exactly the sequences of the lines that name
\* it, in order; a topology line names no card.
HistoryExact == \A e \in Epochs, p \in Cards : hist[e][p] = HistTrue(log[e], p)

\* A line names at most IdCap ids.
LineBound == \A e \in Epochs : \A i \in 1..Len(log[e]) : Len(log[e][i].its) <= IdCap

\* No live log passes the sequence ceiling.
SeqCeiling == \A e \in Epochs : added[e] <= Ceiling

\* A caller request's lines are written by one store call: a retry after a
\* lost reply writes none again. Every note in this model is a caller's.
RetryWritesOnce ==
  \A e1, e2 \in Epochs : \A i \in 1..Len(log[e1]), j \in 1..Len(log[e2]) :
    (log[e1][i].rq = log[e2][j].rq /\ log[e1][i].rq[1] \in Callers)
      => log[e1][i].call = log[e2][j].call

\* A reply's first_seq..last_seq, fresh or replayed, name exactly the lines
\* that request wrote, in the log of its epoch_after.
ReplySeqsTrue ==
  \A c \in Callers :
    (pc[c] = "replied" /\ rep[c].out \in {"apply", "replay"} /\ rep[c].res.n > 0)
      => LET r == rep[c].res IN
         /\ r.ls - r.fs + 1 = r.n
         /\ \A q \in r.fs..r.ls : HasLine(r.ea, q) /\ LineAt(r.ea, q).rq = <<c, part[c]>>

\* A page chain emits exactly the items below its high-water, in order, each
\* once: what it has emitted is a prefix of that list, and all of it when the
\* chain is exhausted. A refusal (a hole: LOGID) stops the chain. For
\* cardlines the list is each card's (line, member id) projections, and the
\* position is the pair (list index, item index within that line): an
\* exhausted slot has emitted every item it owes, and a slot that is not
\* exhausted points at exactly the next item it owes.
PairThere(e, s) ==
  /\ s.nx < s.n /\ s.nx < Len(hist[e][s.p])
  /\ HasLine(e, hist[e][s.p][s.nx + 1])
  /\ s.it < Len(PItems(LineAt(e, hist[e][s.p][s.nx + 1]), s.p))
PairAt(e, s) ==
  LET q == hist[e][s.p][s.nx + 1] IN <<q, PItems(LineAt(e, q), s.p)[s.it + 1]>>
SlotOK(e, s) ==
  /\ IsPrefix(s.got, s.want)
  /\ s.nx >= s.n => s.got = s.want
  /\ PairThere(e, s) => Len(s.got) < Len(s.want) /\ PairAt(e, s) = s.want[Len(s.got) + 1]
CursorNeitherSkipsNorRepeats ==
  rd.st \in {"open", "done", "refused"} =>
    IF rd.md = "lines"
      THEN IsPrefix(rd.got, rd.want) /\ (rd.st = "done" => rd.got = rd.want)
      ELSE \A i \in 1..Len(rd.sl) : SlotOK(rd.e, rd.sl[i])

\* A replay's verdict is the truth: it reports "differ" only when the log and
\* the tables really disagree, and "equal" only when they agree.
ReplayVerdictSound == rp.st = "done" => ((rp.verdict = "equal") = rp.truth)

\* Actions.

NewLines(e) == SubSeq(log'[e], Len(log[e]) + 1, Len(log'[e]))
\* Whether entry i changed anything, read from the tables before and after.
ObsEff(R, i) ==
  LET e == R.es[i]  we == WEp(R)  pre == IF HasAdv(R) THEN {} ELSE rows[we] IN
  CASE e.k \in MemberKinds -> \E j \in 1..Len(e.ids) : rec'[e.ids[j]] # rec[e.ids[j]]
    [] e.k = "rows"        -> ((e.add \cap rows'[we]) \ pre) # {} \/ ((e.del \ rows'[we]) \cap pre) # {}
    [] e.k = "advance"     -> ep' # ep
    [] OTHER               -> FALSE
\* An accepted step writes one line for each entry that changed something, in
\* input order and of its kind, a member line naming exactly the ids whose
\* records changed; then one line per note; and nothing else. A step that
\* changes nothing and has no note writes no line; a guard and a guard-only
\* rowset write none.
OneLinePerChange ==
  [][last'.out = "apply" =>
       LET R == last'.R  new == NewLines(last'.we)
           eff == SelectSeq([i \in 1..Len(R.es) |-> i], LAMBDA i : ObsEff(R, i))
           tab == SelectSeq(new, LAMBDA l : l.k # "note")
           nts == SelectSeq(new, LAMBDA l : l.k = "note")
       IN /\ tab \o nts = new
          /\ Len(nts) = Len(R.ns)
          /\ Len(tab) = Len(eff)
          /\ \A x \in 1..Len(tab) :
               LET e == R.es[eff[x]] IN
               /\ tab[x].k = e.k
               /\ e.k \in MemberKinds =>
                    [j \in 1..Len(tab[x].its) |-> tab[x].its[j].m] = SelectSeq(e.ids, LAMBDA m : rec'[m] # rec[m])]_vars

\* Every about a new line carries is the caller's: a member line's about for
\* an id is the about the request aligned with that id, and the n-th note
\* line's about set is the request's n-th note (L1 section 1.3; L2 section
\* 1.1).
AboutExact ==
  [][last'.out = "apply" =>
       LET R == last'.R  new == NewLines(last'.we)
           nts == SelectSeq(new, LAMBDA l : l.k = "note")
       IN /\ \A x \in 1..Len(new) : new[x].k \in MemberKinds =>
               \A j \in 1..Len(new[x].its) :
                 \E i \in MemIdx(R) : \E k \in 1..Len(R.es[i].ids) :
                   R.es[i].ids[k] = new[x].its[j].m /\ R.es[i].ab[k] = new[x].its[j].p
          /\ \A n \in 1..Len(nts) : n <= Len(R.ns) /\ nts[n].na = R.ns[n]]_vars

\* A refused call and a receipt replay change no key: tables, log, history
\* lists, stream metadata, receipts.
RefusedWritesNothing == [][last'.out \in {"refused", "replay"} => UNCHANGED store]_vars

\* A fence that wins writes its receipt and nothing else: a new receipt at
\* its request epoch, status fenced, both seqs 0; no line, no history, no
\* table change (L1 sections 1.4 and 5; alignment 5 of section 10).
FenceWritesReceiptOnly ==
  [][last'.out = "fenced" =>
       LET R == last'.R IN
       /\ UNCHANGED <<ep, rows, rec, log, hist, added, lastId, lkey>>
       /\ ~\E d \in done : d.ep = R.ep /\ d.op = R.op
       /\ done' = done \cup {[ep |-> R.ep, op |-> R.op, st |-> "fenced",
                              res |-> [ea |-> R.ep, fs |-> 0, ls |-> 0, n |-> 0]]}
       /\ last'.res.fs = 0 /\ last'.res.ls = 0]_vars

\* The log plan never sees a fence (L.plan is bypassed): a fence is refused
\* only STALE, or by prepare's own preflight, never by a log head, the
\* ceiling, a new-epoch key or a guard.
FenceBypassesLog ==
  [][(last'.R.fc /\ last'.out = "refused") => last'.code \in {"STALE", "PREPARE"}]_vars

\* Every line of one call carries that call's one TIME sample.
TimeFrozen ==
  [][last'.out = "apply" => \A x \in 1..Len(NewLines(last'.we)) : NewLines(last'.we)[x].ms = last'.now]_vars

\* Nothing is rewritten or removed within an epoch.
LogAppendOnly == [][\A e \in Epochs : IsPrefix(log[e], log'[e])]_vars

\* The supported writer appends only after a head whose last-generated-id is
\* exactly <entries-added>-0 (an auto id refuses LOGID).
LogIdGuard ==
  [][(last'.out = "apply" /\ last'.n > 0 /\ lkey[last'.we]) => lastId[last'.we] = added[last'.we]]_vars

-----------------------------------------------------------------------------
\* Trace: a compact view of a state for reading a counterexample. "ALIAS
\* Trace" in a configuration prints this instead of the variables; it changes
\* nothing that is checked.
LSum(l) ==
  CASE l.k \in MemberKinds -> <<l.id, l.k, [j \in 1..Len(l.its) |-> <<l.its[j].m, l.its[j].p>>], l.to, l.ms, l.rq>>
    [] l.k = "rows"        -> <<l.id, "rows", l.add, l.del, l.ms, l.rq>>
    [] l.k = "advance"     -> <<l.id, "advance", l.efr, l.eto, l.ms, l.rq>>
    [] OTHER               -> <<l.id, l.k, l.na, l.ms, l.rq>>
Trace ==
  [ep |-> ep, pc |-> pc, part |-> part, rqe |-> rqe, fz |-> fz,
   rep |-> [c \in Callers |-> <<rep[c].out, rep[c].code, rep[c].res.ea, rep[c].res.fs, rep[c].res.ls>>],
   last |-> IF last.out = "-" THEN "-" ELSE <<last.out, last.code, last.R.rq, last.R.op, last.R.fc, last.we, last.now>>,
   rec |-> [m \in {x \in Members : rec[x].ex} |-> <<rec[m].ep, rec[m].pl, rec[m].sc, rec[m].f, rec[m].rev>>],
   rows |-> rows,
   log |-> [e \in Epochs |-> [i \in 1..Len(log[e]) |-> LSum(log[e][i])]],
   meta |-> [e \in Epochs |-> <<added[e], lastId[e], lkey[e]>>],
   hist |-> hist,
   done |-> {<<d.ep, d.op, d.st, d.res.ea, d.res.fs, d.res.ls>> : d \in done},
   rd |-> <<rd.st, rd.md, rd.e, rd.thr, rd.got,
            [i \in 1..Len(rd.sl) |-> <<rd.sl[i].p, rd.sl[i].nx, rd.sl[i].it, rd.sl[i].n, rd.sl[i].got>>]>>,
   rp |-> <<rp.st, rp.e, rp.thr, rp.nx, rp.verdict, rp.truth>>]
=============================================================================
