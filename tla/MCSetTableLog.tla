---------------------------- MODULE MCSetTableLog ----------------------------
\* The small instances of SetTableLog: 2 cards (primaries p1, p2), 3 stored
\* IDs (m1 and m2 placed at epoch 0, m3 free for a create after an advance),
\* 3 rows, 2 callers, at most 4 step calls, at most 1 advance (epochs 0..1),
\* at most 1 lost reply. Each configuration picks the programs its callers
\* may run (Menu), the bounds of the reader and the replay, the outside
\* events, whether a caller may fence, and the witness it turns on (Broken).
EXTENDS SetTableLog

MCMembers == {"m1", "m2", "m3"}
MCCards == {"p1", "p2"}
MCCardOrder == <<"p1", "p2">>
MCRows == {"r1", "r2", "r3"}
MCCallers == {"a", "b"}

\* Epoch 0 at the start: rows r1 and r2, then creates of m1 (card p1, score 1,
\* field 1) and m2 (card p2, score 2, field 2) at r1, one id a line so that
\* the seed keeps an id cap of 1. The tables are the fold of this log.
Seed(id, k, its, to, add) == Line(id, k, its, to, add, {}, 0, 0, {}, 0, 0, <<"init", 0>>)
MCInitLog ==
  << Seed(1, "rows", <<>>, NoRow, {"r1", "r2"}),
     Seed(2, "create", << Item("m1", "p1", 1, 1, 1) >>, "r1", {}),
     Seed(3, "create", << Item("m2", "p2", 2, 1, 2) >>, "r1", {}) >>

Zeros(n) == [j \in 1..n |-> 0]
Mv(ids, ab, fr, to, sc, f) == E("move", ids, ab, fr, to, sc, f, {}, {})
Cr(ids, ab, to, sc, f)     == E("create", ids, ab, NoRow, to, sc, f, {}, {})
Rm(ids, ab, fr, f)         == E("remove", ids, ab, fr, NoRow, Zeros(Len(ids)), f, {}, {})
Gd(ids, fr)                == E("guard", ids, [j \in 1..Len(ids) |-> "p1"], fr, NoRow, Zeros(Len(ids)), Zeros(Len(ids)), {}, {})
RowsE(add, del)            == E("rows", <<>>, <<>>, NoRow, NoRow, <<>>, <<>>, add, del)
RowsetE(rs)                == E("rowset", <<>>, <<>>, NoRow, NoRow, <<>>, <<>>, rs, {})
AdvE                       == E("advance", <<>>, <<>>, NoRow, NoRow, <<>>, <<>>, {}, {})

\* Caller a's parts.
\* A set move of m1 and m2 from r1 to r2: one line naming both cards.
AMoveSet == Part("a1", << Mv(<<"m1", "m2">>, <<"p1", "p2">>, "r1", "r2", <<0, 0>>, <<0, 0>>) >>, <<>>)
\* A stay that sets field 2 on m1 and m2: m2 has it already, so the line
\* names m1 only; then a note about both cards.
AStay    == Part("a2", << Mv(<<"m1", "m2">>, <<"p1", "p2">>, "r1", NoRow, <<0, 0>>, <<2, 2>>) >>, << {"p1", "p2"} >>)
\* Rows add r2 (present: no effect) and r3, then move m1 to r3 with score 2:
\* a rows line and a move line.
ARows    == Part("a3", << RowsE({"r2", "r3"}, {}), Mv(<<"m1">>, <<"p1">>, "r1", "r3", <<2>>, <<0>>) >>, <<>>)
\* Two notes and no entry, with an op: two note lines, no table change.
ANotes   == Part("a4", <<>>, << {"p1", "p2"}, {"p2"} >>)
\* The same kind of note with no op: REQUEST under the contract (L1 revision
\* 4 section 3); under the revision-1 rule (witness opnotes) it is written
\* again on a resend.
ANoteNoOp == Part("none", <<>>, << {"p1"} >>)
\* After an advance: set field 2 on m3 where it is, and a note about p2 (MISSING
\* before the advance created m3).
AThree   == Part("a5", << Mv(<<"m3">>, <<"p1">>, "r1", NoRow, <<0>>, <<2>>) >>, << {"p2"} >>)
\* A stay that sets field 1 on m1 (already 1: no effect) and m2 (2 -> 1),
\* with distinct abouts: the changed-id mask drops the FIRST id, so the line
\* names m2 with about p2 (witness aboutmask: m2 with about p1).
AStayLate == Part("a6", << Mv(<<"m1", "m2">>, <<"p1", "p2">>, "r1", NoRow, <<0, 0>>, <<1, 1>>) >>, <<>>)

\* Caller b's parts.
\* Advance 0 -> 1, restore row r1, create m3 (card p1) there, a note: the
\* new epoch's log is advance, rows, create, note.
BClear   == Part("b1", << AdvE, RowsE({"r1"}, {}), Cr(<<"m3">>, <<"p1">>, "r1", <<1>>, <<1>>) >>, << {"p1"} >>)
\* The same clear with a rowset prefix guarding the request epoch's rows
\* (r1, r2): ROWSET once caller a has added r3. The advance is the first
\* non-rowset entry, the second; the rowset writes no line, so the new
\* epoch's log is again advance, rows, create, note.
BClearRs == Part("b5", << RowsetE({"r1", "r2"}), AdvE, RowsE({"r1"}, {}), Cr(<<"m3">>, <<"p1">>, "r1", <<1>>, <<1>>) >>, << {"p1"} >>)
\* Remove m2 from r1, with a field delta (2 -> 1).
BRemove  == Part("b2", << Rm(<<"m2">>, <<"p2">>, "r1", <<1>>) >>, <<>>)
\* An op-less move of m1 from r2 back to r1 (applies only after AMoveSet).
BBack    == Part("none", << Mv(<<"m1">>, <<"p1">>, "r2", "r1", <<0>>, <<0>>) >>, <<>>)
\* A guard on m1 at r1 (no line) and a delete of r2 (OCCUPIED once anyone is there).
BDel     == Part("b3", << Gd(<<"m1">>, "r1"), RowsE({}, {"r2"}) >>, <<>>)
\* A rescore of m1 and m2 in place, both named for card p1: one line, one
\* history append for p1 (deduplicated per line), two cardlines items for p1.
BShared  == Part("b4", << Mv(<<"m1", "m2">>, <<"p1", "p1">>, "r1", NoRow, <<2, 1>>, <<0, 0>>) >>, <<>>)
\* After AMoveSet (m1 and m2 at r2): a changed stay of both in r2 with a
\* delete of r2 (ROWCONFLICT: a changed stay into a deleted row), then both
\* moved out of r2 to r1 with the same delete, which final occupancy accepts:
\* a move line and a rows line in one commit.
BDelStay == Part("b6", << Mv(<<"m1", "m2">>, <<"p1", "p2">>, "r2", NoRow, <<3, 3>>, <<0, 0>>), RowsE({}, {"r2"}) >>, <<>>)
BDelOut  == Part("b7", << Mv(<<"m1", "m2">>, <<"p1", "p2">>, "r2", "r1", <<0, 0>>, <<0, 0>>), RowsE({}, {"r2"}) >>, <<>>)

\* Raw writes allowed by a configuration.
MCRawNone == {}
MCRawAuto == {"auto"}
MCRawAll == {"auto", "next", "xdel"}
MCRawDelKey == {"delkey"}

Menu2(as, bs) == [c \in MCCallers |-> IF c = "a" THEN as ELSE bs]
MenuMain   == Menu2({<<AMoveSet, AStay>>, <<ARows, ANotes>>, <<AThree>>, <<AStayLate>>},
                    {<<BClear>>, <<BClearRs>>, <<BRemove, BBack>>, <<BDel>>, <<BShared>>, <<BDelStay, BDelOut>>})
\* Smaller menus, for the reader, the replay, the raw writes and the witnesses.
MenuRead    == Menu2({<<AMoveSet>>, <<ANotes>>, <<AThree>>}, {<<BClear>>, <<BShared>>})
MenuReplay  == Menu2({<<AMoveSet>>, <<ARows>>}, {<<BClear>>})
MenuRaw     == Menu2({<<AMoveSet>>}, {<<BRemove>>, <<BClear>>})
MenuSet     == Menu2({<<AMoveSet>>}, {<<BShared>>})
MenuStay    == Menu2({<<AStay>>}, {<<BRemove>>})
MenuClear   == Menu2({<<ARows>>}, {<<BClear>>})
MenuRace    == Menu2({<<AMoveSet>>}, {<<BRemove>>})
MenuNotes   == Menu2({<<ANotes>>}, {<<BRemove>>})
MenuNoOp    == Menu2({<<ANoteNoOp>>}, {<<BRemove>>})
MenuNotesOp == Menu2({<<ANoteNoOp>>, <<ANotes>>}, {<<BRemove>>})
MenuMany    == Menu2({<<ARows, ANotes>>}, {<<BShared>>})
MenuAbout   == Menu2({<<AStayLate>>}, {<<BRemove>>})
MenuRowset  == Menu2({<<ARows>>}, {<<BClearRs>>})
=============================================================================
