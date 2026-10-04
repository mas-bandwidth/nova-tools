---------------------------- MODULE TableOrder ----------------------------
\* The order of a nova-table's rows and columns as state (Glenn 2026-09-27:
\* "take column y and put it after column z", "friends on top, machines on
\* bottom"). table.lua at nova-tools#4457 47bbca675: T.reorder, T.sorted,
\* T.rank, the standing-sort step of T.finish, ns_table_bind, and the
\* col_add, col_del, col_move, row_move, row_order and row_sort keys of
\* ns_table_set, alone and combined.
\*
\* rows and cols are sequences without repeats. Row names are numbers, so
\* "sorted by name" is ascending. holds is the cells that hold a member or a
\* text value. sorted is the standing sort (row sort --keep). req is what
\* the call asked for, kept so the invariants can say the result is the one
\* requested and not merely some permutation (Stella's read,
\* stella-67bb4e7103bb: an existential over the result is vacuous).
\*
\* Broken = "none" is the code. Every other value is a reversed witness, a
\* misimplementation one invariant must catch:
\*   "sort"      row add does not honour the standing sort
\*   "bind"      bind writes its input order under a standing sort
\*   "combined"  one call sets a standing sort and places a row by hand
\*   "prefix"    row order puts the named rows last
\*   "item"      row move --first moves the last row, whatever was asked
\*   "del"       col del removes a column that holds something
\*   "bindloss"  bind drops an omitted row that holds something
\*   "once"      row sort without --keep leaves the rows as they were
\*
\* Bounds of this instance, stated so the claim is no wider than the check:
\* a combined call that sorts and moves places at --first or --last only,
\* and one that sorts and orders names one row; alone, row move takes all
\* four places and row order every list. Three rows, three columns, depth 4.
\*
\* Not here: labels and sorting by a column's value (modelled as sorting by
\* name: the key differs, the properties do not), epochs, receipts, rename,
\* bound cells. Those are functional tests: order_functional_test.go and the
\* regression tests of #4457.
EXTENDS Naturals, Sequences, FiniteSets
CONSTANTS NRows, Cols, InitCols, Broken, MaxSteps
Rows == 1..NRows
Cells == Rows \X Cols
NoRow == 0
NoCol == "-"
VARIABLES rows, cols, holds, sorted, op, outcome, prev, req, step
vars == <<rows, cols, holds, sorted, op, outcome, prev, req, step>>
State == [rows |-> rows, cols |-> cols, holds |-> holds, sorted |-> sorted]
Range(s) == {s[i] : i \in DOMAIN s}
NoRepeat(s) == \A i, j \in DOMAIN s : s[i] = s[j] => i = j
Without(s, x) == SelectSeq(s, LAMBDA v : v # x)
Index(s, x) == CHOOSE i \in DOMAIN s : s[i] = x
InsertAt(s, i, x) == SubSeq(s, 1, i - 1) \o <<x>> \o SubSeq(s, i, Len(s))
Places == {"first", "last", "before", "after"}
Ends == {"first", "last"}
\* T.reorder: the item leaves, then enters at the place.
Reorder(s, x, where, ref) ==
 LET out == Without(s, x)
 IN CASE where = "first" -> InsertAt(out, 1, x)
      [] where = "last" -> out \o <<x>>
      [] where = "before" -> InsertAt(out, Index(out, ref), x)
      [] where = "after" -> InsertAt(out, Index(out, ref) + 1, x)
CanReorder(s, x, where, ref) ==
 /\ x \in Range(s)
 /\ where \in {"before", "after"} => (ref \in Range(s) /\ ref # x)
RowReorder(s, x, where, ref) ==
 IF Broken = "item" /\ where = "first" THEN Reorder(s, s[Len(s)], where, ref)
 ELSE Reorder(s, x, where, ref)
Ascending(s) == \A i, j \in DOMAIN s : i < j => s[i] < s[j]
Sorted(S) == CHOOSE s \in [1..Cardinality(S) -> S] : Range(s) = S /\ Ascending(s)
Lists == UNION {{s \in [1..n -> Rows] : NoRepeat(s)} : n \in 0..NRows}
Firsts == Lists \ {<<>>}
Ones == {s \in Firsts : Len(s) = 1}
Modes == {"once", "keep", "manual"}
NoReq == [r |-> NoRow, rref |-> NoRow, c |-> NoCol, cref |-> NoCol,
          where |-> "-", list |-> <<>>, mode |-> "-"]
TypeOK ==
 /\ rows \in Lists
 /\ cols \in Seq(Cols) /\ NoRepeat(cols) /\ Len(cols) >= 1
 /\ holds \subseteq Cells
 /\ sorted \in BOOLEAN
 /\ outcome \in {"ok", "refused"}
 /\ req.r \in Rows \cup {NoRow} /\ req.rref \in Rows \cup {NoRow}
 /\ req.c \in Cols \cup {NoCol} /\ req.cref \in Cols \cup {NoCol}
 /\ req.where \in Places \cup {"-"} /\ req.list \in Lists
 /\ req.mode \in Modes \cup {"-"}
 /\ step \in 0..MaxSteps
Init ==
 /\ rows = <<>> /\ cols = InitCols /\ holds = {} /\ sorted = FALSE
 /\ op = "initial" /\ outcome = "ok" /\ step = 0 /\ req = NoReq
 /\ prev = [rows |-> <<>>, cols |-> InitCols, holds |-> {}, sorted |-> FALSE]
Leave(kind, result, s, q) ==
 /\ rows' = s.rows /\ cols' = s.cols /\ holds' = s.holds /\ sorted' = s.sorted
 /\ op' = kind /\ outcome' = result /\ prev' = State /\ req' = q
 /\ step' = step + 1
Commit(kind, s, q) == Leave(kind, "ok", s, q)
Refuse(kind, q) == Leave(kind, "refused", State, q)
RowOf(x) == {k \in holds : k[1] = x}
ColOf(c) == {k \in holds : k[2] = c}
\* row add: new rows go last in the order given, or to their place under a
\* standing sort (T.finish). A row already there keeps its place.
RowsAdd(L) ==
 LET new == SelectSeq(L, LAMBDA v : v \notin Range(rows))
     q == [NoReq EXCEPT !.list = L]
 IN Commit("rows-add", [State EXCEPT !.rows =
      IF sorted /\ Broken # "sort" THEN Sorted(Range(rows) \cup Range(L))
      ELSE rows \o new], q)
\* row del: a row that is not there is an accepted no-op; a row's own cells
\* go with it.
RowDel(r) ==
 LET q == [NoReq EXCEPT !.r = r]
 IN IF r \notin Range(rows) THEN Commit("row-del", State, q)
    ELSE Commit("row-del", [State EXCEPT !.rows = Without(rows, r),
                                         !.holds = holds \ RowOf(r)], q)
\* ns_table_bind: the rows are the list given, in its order; an omitted row
\* that holds something refuses the call; under a standing sort the order is
\* the sort's.
Bind(L) ==
 LET gone == Range(rows) \ Range(L)
     q == [NoReq EXCEPT !.list = L]
 IN IF Broken # "bindloss" /\ \E x \in gone : RowOf(x) # {} THEN Refuse("bind", q)
    ELSE Commit("bind", [State EXCEPT
           !.rows = IF sorted /\ Broken # "bind" THEN Sorted(Range(L)) ELSE L,
           !.holds = {k \in holds : k[1] \in Range(L)}], q)
RowMove(r, where, ref) ==
 LET q == [NoReq EXCEPT !.r = r, !.where = where, !.rref = ref]
 IN IF sorted \/ ~CanReorder(rows, r, where, ref) THEN Refuse("row-move", q)
    ELSE Commit("row-move", [State EXCEPT !.rows = RowReorder(rows, r, where, ref)], q)
\* row order: the named rows first, in the order given; the rest keep theirs.
Ordered(base, first) ==
 IF Broken = "prefix" THEN SelectSeq(base, LAMBDA v : v \notin Range(first)) \o first
 ELSE first \o SelectSeq(base, LAMBDA v : v \notin Range(first))
RowOrder(first) ==
 LET q == [NoReq EXCEPT !.list = first]
 IN IF sorted \/ ~(Range(first) \subseteq Range(rows)) THEN Refuse("row-order", q)
    ELSE Commit("row-order", [State EXCEPT !.rows = Ordered(rows, first)], q)
\* row sort: once, standing (--keep), or the standing sort ended (--manual).
Base(mode) == IF mode = "manual" \/ (mode = "once" /\ Broken = "once") THEN rows
              ELSE Sorted(Range(rows))
RowSort(mode) ==
 Commit("row-sort", [State EXCEPT !.rows = Base(mode), !.sorted = (mode = "keep")],
        [NoReq EXCEPT !.mode = mode])
\* One ns_table_set call with row_sort and row_move, or row_sort and
\* row_order: the sort first, then the placing; a standing sort and a
\* placing by hand in one call are refused.
SortMove(mode, r, where) ==
 LET q == [NoReq EXCEPT !.mode = mode, !.r = r, !.where = where]
     can == CanReorder(rows, r, where, NoRow)
 IN IF ~can \/ (mode = "keep" /\ Broken # "combined") THEN Refuse("sort-move", q)
    ELSE Commit("sort-move", [State EXCEPT
           !.rows = RowReorder(Base(mode), r, where, NoRow),
           !.sorted = (mode = "keep")], q)
SortOrder(mode, first) ==
 LET q == [NoReq EXCEPT !.mode = mode, !.list = first]
 IN IF ~(Range(first) \subseteq Range(rows)) \/ (mode = "keep" /\ Broken # "combined")
    THEN Refuse("sort-order", q)
    ELSE Commit("sort-order", [State EXCEPT
           !.rows = Ordered(Base(mode), first), !.sorted = (mode = "keep")], q)
ColMove(c, where, ref) ==
 LET q == [NoReq EXCEPT !.c = c, !.where = where, !.cref = ref]
 IN IF ~CanReorder(cols, c, where, ref) THEN Refuse("col-move", q)
    ELSE Commit("col-move", [State EXCEPT !.cols = Reorder(cols, c, where, ref)], q)
ColAdd(c, where, ref) ==
 LET q == [NoReq EXCEPT !.c = c, !.where = where, !.cref = ref]
 IN IF c \in Range(cols) \/ ~CanReorder(cols \o <<c>>, c, where, ref) THEN Refuse("col-add", q)
    ELSE Commit("col-add", [State EXCEPT !.cols = Reorder(cols \o <<c>>, c, where, ref)], q)
ColDel(c) ==
 LET q == [NoReq EXCEPT !.c = c]
 IN IF c \notin Range(cols) \/ Len(cols) = 1 \/ (Broken # "del" /\ ColOf(c) # {})
    THEN Refuse("col-del", q)
    ELSE Commit("col-del", [State EXCEPT !.cols = Without(cols, c),
                                         !.holds = holds \ ColOf(c)], q)
\* cell add, row set: a cell comes to hold something; cell remove: it stops.
Fill(r, c) ==
 LET q == [NoReq EXCEPT !.r = r, !.c = c]
 IN IF r \notin Range(rows) \/ c \notin Range(cols) THEN Refuse("fill", q)
    ELSE Commit("fill", [State EXCEPT !.holds = holds \cup {<<r, c>>}], q)
Empty(r, c) ==
 Commit("empty", [State EXCEPT !.holds = holds \ {<<r, c>>}],
        [NoReq EXCEPT !.r = r, !.c = c])
Next ==
 /\ step < MaxSteps
 /\ \/ \E L \in Firsts : RowsAdd(L) \/ RowOrder(L)
    \/ \E L \in Lists : Bind(L)
    \/ \E r \in Rows : RowDel(r)
    \/ \E r \in Rows, w \in Places, ref \in Rows : RowMove(r, w, ref)
    \/ \E m \in Modes : RowSort(m)
    \/ \E m \in Modes, r \in Rows, w \in Ends : SortMove(m, r, w)
    \/ \E m \in Modes, L \in Ones : SortOrder(m, L)
    \/ \E c \in Cols, w \in Places, ref \in Cols : ColMove(c, w, ref) \/ ColAdd(c, w, ref)
    \/ \E c \in Cols : ColDel(c)
    \/ \E r \in Rows, c \in Cols : Fill(r, c) \/ Empty(r, c)
Spec == Init /\ [][Next \/ (step = MaxSteps /\ UNCHANGED vars)]_vars
Ok(kinds) == op \in kinds /\ outcome = "ok"
RefusalWritesNothing == outcome = "refused" => State = prev
\* Where the request put the item, said without the helper that put it there.
Placed(s, x, where, ref) ==
 CASE where = "first" -> s[1] = x
   [] where = "last" -> s[Len(s)] = x
   [] where = "before" -> Index(s, x) + 1 = Index(s, ref)
   [] where = "after" -> Index(s, x) = Index(s, ref) + 1
\* The order a combined call starts placing from: sorted, unless --manual.
Was == IF op \in {"sort-move", "sort-order"} /\ req.mode # "manual"
       THEN Sorted(Range(prev.rows)) ELSE prev.rows
\* The row asked for is at the place asked for, and no other row moved.
RowMoveIsExact == Ok({"row-move", "sort-move"}) =>
 /\ Placed(rows, req.r, req.where, req.rref)
 /\ Without(rows, req.r) = Without(Was, req.r)
\* The rows named lead, in the order named; the rest follow in theirs.
RowOrderIsExact == Ok({"row-order", "sort-order"}) =>
 rows = req.list \o SelectSeq(Was, LAMBDA v : v \notin Range(req.list))
\* The column asked for is at the place asked for, and no other moved.
ColMoveIsExact == Ok({"col-move", "col-add"}) =>
 /\ Placed(cols, req.c, req.where, req.cref)
 /\ Without(cols, req.c) = Without(prev.cols, req.c)
\* Without a standing sort, rows added go last in the order given, and bind
\* leaves exactly the list given.
AddIsExact == (Ok({"rows-add"}) /\ ~prev.sorted) =>
 rows = prev.rows \o SelectSeq(req.list, LAMBDA v : v \notin Range(prev.rows))
BindIsExact == Ok({"bind"}) =>
 /\ Range(rows) = Range(req.list)
 /\ ~prev.sorted => rows = req.list
\* A sort leaves the same rows in ascending order, once or standing, and
\* says which it is; --manual leaves them where they were.
RowSortIsExact == Ok({"row-sort"}) =>
 /\ Range(rows) = Range(prev.rows)
 /\ req.mode = "manual" => rows = prev.rows
 /\ req.mode # "manual" => Ascending(rows)
 /\ sorted = (req.mode = "keep")
\* While a sort stands, the rows are in its order after every call.
StandingSortHolds == sorted => Ascending(rows)
\* Order verbs permute; they add nothing, lose nothing, and hold what was held.
Reorders == {"row-move", "row-order", "row-sort", "sort-move", "sort-order", "col-move"}
ReorderIsPermutation == op \in Reorders =>
 /\ Range(rows) = Range(prev.rows) /\ Range(cols) = Range(prev.cols)
 /\ holds = prev.holds
\* What is held sits in a row and a column the table has.
HeldInShape == \A k \in holds : k[1] \in Range(rows) /\ k[2] \in Range(cols)
\* Only row del and the cell verbs change what is held.
OnlyRowDelDrops ==
 /\ op \notin {"row-del", "fill", "empty"} => holds = prev.holds
 /\ Ok({"row-del"}) => holds = prev.holds \ {k \in prev.holds : k[1] = req.r}
\* Column verbs never touch the rows, row verbs never the columns.
RowsAndColumnsApart ==
 /\ op \in {"col-move", "col-add", "col-del"} => rows = prev.rows
 /\ op \in ({"rows-add", "row-del", "bind"} \cup (Reorders \ {"col-move"})) => cols = prev.cols
=============================================================================
