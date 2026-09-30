----------------------------- MODULE MCSetTable ------------------------------
\* The small instance of SetTable: 2 tables, 2 rows and 2 columns each,
\* 3 members, 2 callers, at most 2 retries per caller, at most 1 advance
\* (epochs 0..1), 1 crash, 1 request left in flight, 1 outside beat.
\* Each configuration picks the programs its callers may run (Menu) and the
\* witness it turns on (Broken).
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

\* The outside beat: m1 back to t1 r1:c1 with its field 0, or a field
\* change on m2 where it is.
MCBeatSet == {<<"t1", "m1", <<"r1", "c1">>, 0>>, <<"t2", "m2", <<"r1", "c1">>, 1>>}
NoBeats == {}

Mv(t, m, to, f) == [t |-> t, m |-> m, to |-> to, f |-> f, rm |-> FALSE]
Rm(t, m, f) == [t |-> t, m |-> m, to |-> NoPlace, f |-> f, rm |-> TRUE]
Part(op, a) == [op |-> op, a |-> a]

\* Caller a's programs.
AMove   == << Part("a1", [NoArgs EXCEPT !.mv = << Mv("t1", "m1", <<"r2", "c1">>, 1) >>]) >>
ATwo    == << Part("a1", [NoArgs EXCEPT !.mv = << Mv("t1", "m1", <<"r2", "c1">>, 1),
                                                   Mv("t2", "m2", <<"r1", "c2">>, 1) >>]) >>
AChunk  == << Part("a1", [NoArgs EXCEPT !.mv = << Mv("t1", "m1", <<"r2", "c1">>, 1) >>]),
              Part("a2", [NoArgs EXCEPT !.mv = << Mv("t2", "m2", <<"r1", "c2">>, 1) >>]) >>
ACap    == << Part("a1", [NoArgs EXCEPT !.mv = << Mv("t1", "m1", <<"r2", "c2">>, 1) >>, !.cap = TRUE]) >>
AAddRow == << Part("a1", [NoArgs EXCEPT !.rt = "t2", !.add = {"r2"}]) >>
\* Caller b's programs.
BMove      == << Part("b1", [NoArgs EXCEPT !.mv = << Mv("t1", "m1", <<"r1", "c2">>, 0) >>]) >>
BClear     == << Part("b1", [NoArgs EXCEPT !.clear = TRUE]) >>
BDelCreate == << Part("b1", [NoArgs EXCEPT !.rt = "t1", !.del = {"r2"},
                                           !.cr = << Mv("t1", "m3", <<"r2", "c1">>, 0) >>]) >>
BCreate    == << Part("b1", [NoArgs EXCEPT !.cr = << Mv("t1", "m3", <<"r2", "c2">>, 0) >>]) >>
BReuse     == << Part("a1", [NoArgs EXCEPT !.mv = << Mv("t1", "m1", <<"r1", "c2">>, 0) >>]) >>
BRemove    == << Part("b1", [NoArgs EXCEPT !.mv = << Rm("t1", "m1", 1) >>]) >>

Menu2(as, bs) == [c \in MCCallers |-> IF c = "a" THEN as ELSE bs]
AAll == {AMove, ATwo, AChunk, ACap, AAddRow}
BAll == {BMove, BClear, BDelCreate, BCreate, BReuse, BRemove}
MenuMain  == Menu2(AAll, BAll)
\* Witness, goal and liveness menus.
MenuRace   == Menu2({AMove}, {BMove})
MenuTwo    == Menu2({ATwo}, {BMove})
MenuLost   == Menu2({AMove}, {BClear})
MenuClear  == Menu2({AMove}, {BClear})
MenuDel    == Menu2({AMove}, {BDelCreate})
MenuAddRow == Menu2({AAddRow}, {BClear})
MenuChunk  == Menu2({AChunk}, {BClear})
MenuRemove == Menu2({AMove}, {BRemove})
MenuLive   == Menu2({AMove, AChunk}, {BMove, BClear, BReuse})
=============================================================================
