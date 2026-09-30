----------------------------- MODULE MCSetTable ------------------------------
\* The small instance of SetTable: 2 tables (3 for the empty-table cases),
\* 2 rows and 2 columns each, 3 members, 2 callers, bounded retries, at most
\* 1 advance (epochs 0..1; 0..2 for the two-advance case), bounded crashes,
\* requests left in flight, outside beats, new op identities per part and
\* refused fences. Each configuration picks the programs its callers may run
\* (Menu), the bounds, and the witness it turns on (Broken).
EXTENDS SetTable

MCTables == {"t1", "t2"}
MCTableSeq == <<"t1", "t2">>
MCRows == {"r1", "r2"}
MCCols == {"c1", "c2"}
MCMembers == {"m1", "m2", "m3"}
MCCallers == {"a", "b"}
MCVals == {0, 1}
MCOps == {"a1", "a2", "b1"}

\* Epoch 0: t1 has rows r1 and r2, t2 has r1 only. m1 is in t1 at r1:c1 and
\* in t2 at r1:c2 (one primary, two tables); m2 is in t2 at r1:c1; m3 is
\* nowhere yet.
Placed(x) == [ex |-> TRUE, ep |-> 0, rev |-> 1, pl |-> x, f |-> 0]
MCInitRows == [t \in MCTables |-> IF t = "t1" THEN {"r1", "r2"} ELSE {"r1"}]
MCInitRecs == [t \in MCTables |-> [m \in MCMembers |->
  IF <<t, m>> = <<"t1", "m1">> THEN Placed(<<"r1", "c1">>)
  ELSE IF <<t, m>> = <<"t2", "m1">> THEN Placed(<<"r1", "c2">>)
  ELSE IF <<t, m>> = <<"t2", "m2">> THEN Placed(<<"r1", "c1">>)
  ELSE Absent]]

\* The catalog with a third table, t3, that has no rows at epoch 0 (the
\* empty table a complete clear must still guard with rows:[]).
MCTables3 == {"t1", "t2", "t3"}
MCTableSeq3 == <<"t1", "t2", "t3">>
MCInitRows3 == [t \in MCTables3 |-> IF t = "t3" THEN {} ELSE MCInitRows[t]]
MCInitRecs3 == [t \in MCTables3 |-> IF t = "t3" THEN [m \in MCMembers |-> Absent] ELSE MCInitRecs[t]]

\* The outside beat: m1 back to t1 r1:c1 with its field 0, or a field
\* change on m2 where it is.
MCBeatSet == {<<"t1", "m1", <<"r1", "c1">>, 0>>, <<"t2", "m2", <<"r1", "c1">>, 1>>}
NoBeats == {}

\* Retry bounds per caller: RetAB gives caller a A retries and caller b B.
Ret(x, y) == [c \in MCCallers |-> IF c = "a" THEN x ELSE y]
Ret00 == Ret(0, 0)  Ret01 == Ret(0, 1)  Ret02 == Ret(0, 2)
Ret10 == Ret(1, 0)  Ret11 == Ret(1, 1)  Ret12 == Ret(1, 2)
Ret20 == Ret(2, 0)  Ret21 == Ret(2, 1)  Ret22 == Ret(2, 2)

Mv(t, m, to, f) == [t |-> t, m |-> m, to |-> to, f |-> f, rm |-> FALSE]
Rm(t, m, f) == [t |-> t, m |-> m, to |-> NoPlace, f |-> f, rm |-> TRUE]
Part(op, a) == [op |-> op, a |-> a]

\* Caller a's programs.
AMove     == << Part("a1", [NoArgs EXCEPT !.mv = << Mv("t1", "m1", <<"r2", "c1">>, 1) >>]) >>
ATwo      == << Part("a1", [NoArgs EXCEPT !.mv = << Mv("t1", "m1", <<"r2", "c1">>, 1),
                                                     Mv("t2", "m2", <<"r1", "c2">>, 1) >>]) >>
AChunk    == << Part("a1", [NoArgs EXCEPT !.mv = << Mv("t1", "m1", <<"r2", "c1">>, 1) >>]),
                Part("a2", [NoArgs EXCEPT !.mv = << Mv("t2", "m2", <<"r1", "c2">>, 1) >>]) >>
ACap      == << Part("a1", [NoArgs EXCEPT !.mv = << Mv("t1", "m1", <<"r2", "c2">>, 1) >>, !.cap = TRUE]) >>
AAddRow   == << Part("a1", [NoArgs EXCEPT !.rt = "t2", !.add = {"r2"}]) >>
AAddRowT3 == << Part("a1", [NoArgs EXCEPT !.rt = "t3", !.add = {"r1"}]) >>
AClear    == << Part("a1", [NoArgs EXCEPT !.clear = TRUE]) >>
\* Caller b's programs.
BMove      == << Part("b1", [NoArgs EXCEPT !.mv = << Mv("t1", "m1", <<"r1", "c2">>, 0) >>]) >>
BClear     == << Part("b1", [NoArgs EXCEPT !.clear = TRUE]) >>
BDelCreate == << Part("b1", [NoArgs EXCEPT !.rt = "t1", !.del = {"r2"},
                                           !.cr = << Mv("t1", "m3", <<"r2", "c1">>, 0) >>]) >>
BCreate    == << Part("b1", [NoArgs EXCEPT !.cr = << Mv("t1", "m3", <<"r2", "c2">>, 0) >>]) >>
BReuse     == << Part("a1", [NoArgs EXCEPT !.mv = << Mv("t1", "m1", <<"r1", "c2">>, 0) >>]) >>
BRemove    == << Part("b1", [NoArgs EXCEPT !.mv = << Rm("t1", "m1", 1) >>]) >>
\* A stay: m2 keeps its cell in t2 and takes field 1 (a no-op when it
\* already has it).
BStay      == << Part("b1", [NoArgs EXCEPT !.mv = << Mv("t2", "m2", NoPlace, 1) >>]) >>
\* Final occupancy: delete t1's row r2 and move m1 to r1:c2 in the same step
\* (applies when m1 was the last member in r2; OCCUPIED when the plan is
\* stale and m1 is left behind).
BDelMove   == << Part("b1", [NoArgs EXCEPT !.rt = "t1", !.del = {"r2"},
                                           !.mv = << Mv("t1", "m1", <<"r1", "c2">>, 0) >>]) >>

\* Reachability probes (settable-bench/MCSetTableReach*.cfg, outside the
\* gate). Each says a path never happens; TLC finding it violated shows that
\* the configuration it copies reaches the path a contract property is meant
\* to cover, so that property's pass is not vacuous.
NoFenceWhileInFlight == ~\E c \in MCCallers : NotApplied(c) /\ late[c] # {}
NoLateFencedReplay == ~(last.out = "replay" /\ last.st = "fenced" /\ ~last.R.fence)
NoFault == last.code # "FAULT"
NoNewOpFinished == ~\E c \in MCCallers : pc[c] = "finished" /\ \E i \in DOMAIN reop[c] : reop[c][i] > 0
NoResumeFenced == ~\E c \in MCCallers : NotApplied(c) /\ req[c] = NoReq
NoRowsetRefusal == last.code # "ROWSET"
NoOccupied == last.code # "OCCUPIED"
NoDeleteWithMoveOut ==
  ~(last.out = "apply" /\ \E i, j \in 1..Len(last.R.es) :
       /\ last.R.es[i].k = "rows" /\ last.R.es[i].del # {}
       /\ last.R.es[j].k = "move" /\ last.R.es[j].from[1] \in last.R.es[i].del)
StayApplied(ch) ==
  last.out = "apply" /\ last.res.ch = ch /\ last.by \in MCCallers
  /\ \E i \in 1..Len(last.R.es) : last.R.es[i].k = "move" /\ last.R.es[i].from = last.R.es[i].to
NoNoopStay == ~StayApplied(0)
NoFieldStay == ~StayApplied(1)
NoReplayAcrossTwo == ~(last.out = "replay" /\ last.R.ep = 0 /\ ep = 2)
NoFenceAfterTwo == ~(last.out # "-" /\ last.R.fence /\ last.R.ep = 0 /\ ep = 2)

Menu2(as, bs) == [c \in MCCallers |-> IF c = "a" THEN as ELSE bs]
AAll == {AMove, ATwo, AChunk, ACap, AAddRow}
BAll == {BMove, BClear, BDelCreate, BCreate, BReuse, BRemove, BStay, BDelMove}
MenuMain  == Menu2(AAll, BAll)
\* The full instance, split by b's programs so each half fits the bench cap.
MenuMainA == Menu2(AAll, {BMove, BClear, BDelCreate, BCreate})
MenuMainB == Menu2(AAll, {BReuse, BRemove, BStay, BDelMove})
MenuMainB1 == Menu2(AAll, {BReuse, BRemove})
MenuMainB2 == Menu2(AAll, {BStay, BDelMove})
\* Witness, contract and liveness menus.
MenuRace   == Menu2({AMove}, {BMove})
MenuTwo    == Menu2({ATwo}, {BMove})
MenuClear  == Menu2({AMove}, {BClear})
MenuDel    == Menu2({AMove}, {BDelCreate})
MenuAddRow == Menu2({AAddRow, AAddRowT3}, {BClear})
MenuAddRow2 == Menu2({AAddRow}, {BClear})
MenuAddT3  == Menu2({AAddRowT3}, {BClear})
MenuChunk  == Menu2({AChunk}, {BClear})
MenuRemove == Menu2({AMove}, {BRemove})
MenuEpochs == Menu2({AClear}, {BClear})
MenuEpochsFull == Menu2({AClear, AMove}, {BClear})
MenuOccupy == Menu2({AMove}, {BDelMove, BStay})
MenuLive   == Menu2({AMove, AChunk}, {BMove, BClear, BReuse})
=============================================================================
