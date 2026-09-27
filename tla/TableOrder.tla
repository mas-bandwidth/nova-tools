---------------------------- MODULE TableOrder ----------------------------
\* The order of a nova-table's rows and columns as state (Glenn 2026-09-27:
\* "take column y and put it after column z", "friends on top, machines on
\* bottom"). table.lua: T.reorder, T.sorted, T.rank and the col_add,
\* col_del, col_move, row_move, row_order and row_sort keys of ns_table_set.
\*
\* rows and cols are sequences without repeats. Row names are numbers, so
\* "sorted by name" is ascending. holds is what a column keeps (members or
\* text): a column that holds anything is never removed. sorted is the
\* standing sort (row sort --keep); while it stands every row added takes
\* its place and no row is placed by hand.
\*
\* Staged = TRUE is the code. Staged = FALSE is the reversed witness: a
\* standing sort that row add does not honour, and a col del that does not
\* look at what the column holds.
EXTENDS Naturals, Sequences, FiniteSets
CONSTANTS NRows, Cols, InitCols, Staged, MaxSteps
Rows == 1..NRows
VARIABLES rows, cols, holds, sorted, op, outcome, prev, step
vars == <<rows, cols, holds, sorted, op, outcome, prev, step>>
State == [rows |-> rows, cols |-> cols, holds |-> holds, sorted |-> sorted]
Range(s) == {s[i] : i \in DOMAIN s}
NoRepeat(s) == \A i, j \in DOMAIN s : s[i] = s[j] => i = j
Without(s, x) == SelectSeq(s, LAMBDA v : v # x)
Index(s, x) == CHOOSE i \in DOMAIN s : s[i] = x
InsertAt(s, i, x) == SubSeq(s, 1, i - 1) \o <<x>> \o SubSeq(s, i, Len(s))
Places == {"first", "last", "before", "after"}
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
Ascending(s) == \A i, j \in DOMAIN s : i < j => s[i] < s[j]
Sorted(S) == CHOOSE s \in [1..Cardinality(S) -> S] : Range(s) = S /\ Ascending(s)
TypeOK ==
 /\ rows \in Seq(Rows) /\ NoRepeat(rows)
 /\ cols \in Seq(Cols) /\ NoRepeat(cols) /\ Len(cols) >= 1
 /\ holds \subseteq Cols
 /\ sorted \in BOOLEAN
 /\ outcome \in {"ok", "refused"}
 /\ step \in 0..MaxSteps
Init ==
 /\ rows = <<>> /\ cols = InitCols /\ holds = {} /\ sorted = FALSE
 /\ op = "initial" /\ outcome = "ok" /\ step = 0
 /\ prev = [rows |-> <<>>, cols |-> InitCols, holds |-> {}, sorted |-> FALSE]
Leave(kind, result, s) ==
 /\ rows' = s.rows /\ cols' = s.cols /\ holds' = s.holds /\ sorted' = s.sorted
 /\ op' = kind /\ outcome' = result /\ prev' = State /\ step' = step + 1
Commit(kind, s) == Leave(kind, "ok", s)
Refuse(kind) == Leave(kind, "refused", State)
\* row add: new rows go last, or to their place under a standing sort.
RowsAdd(R) ==
 LET new == R \ Range(rows)
     tail == rows \o Sorted(new)
 IN Commit("rows-add", [State EXCEPT !.rows =
      IF sorted /\ Staged THEN Sorted(Range(rows) \cup new) ELSE tail])
RowDel(r) ==
 IF r \notin Range(rows) THEN Refuse("row-del")
 ELSE Commit("row-del", [State EXCEPT !.rows = Without(rows, r)])
RowMove(r, where, ref) ==
 IF sorted \/ ~CanReorder(rows, r, where, ref) THEN Refuse("row-move")
 ELSE Commit("row-move", [State EXCEPT !.rows = Reorder(rows, r, where, ref)])
\* row order: the named rows first, in the order given; the rest keep theirs.
RowOrder(first) ==
 IF sorted \/ ~(Range(first) \subseteq Range(rows)) THEN Refuse("row-order")
 ELSE Commit("row-order", [State EXCEPT !.rows =
        first \o SelectSeq(rows, LAMBDA v : v \notin Range(first))])
RowSort(keep) ==
 Commit("row-sort", [State EXCEPT !.rows = Sorted(Range(rows)), !.sorted = keep])
RowSortManual == Commit("row-manual", [State EXCEPT !.sorted = FALSE])
ColMove(c, where, ref) ==
 IF ~CanReorder(cols, c, where, ref) THEN Refuse("col-move")
 ELSE Commit("col-move", [State EXCEPT !.cols = Reorder(cols, c, where, ref)])
ColAdd(c, where, ref) ==
 IF c \in Range(cols) \/ ~CanReorder(cols \o <<c>>, c, where, ref) THEN Refuse("col-add")
 ELSE Commit("col-add", [State EXCEPT !.cols = Reorder(cols \o <<c>>, c, where, ref)])
ColDel(c) ==
 IF c \notin Range(cols) \/ Len(cols) = 1 \/ (Staged /\ c \in holds) THEN Refuse("col-del")
 ELSE Commit("col-del", [State EXCEPT !.cols = Without(cols, c), !.holds = holds \ {c}])
\* cell add, row set: a column comes to hold something; cell remove: it stops.
Fill(c) ==
 IF c \notin Range(cols) \/ rows = <<>> THEN Refuse("fill")
 ELSE Commit("fill", [State EXCEPT !.holds = holds \cup {c}])
Empty(c) == Commit("empty", [State EXCEPT !.holds = holds \ {c}])
Firsts == UNION {{s \in [1..n -> Rows] : NoRepeat(s)} : n \in 1..NRows}
Next ==
 /\ step < MaxSteps
 /\ \/ \E R \in SUBSET Rows \ {{}} : RowsAdd(R)
    \/ \E r \in Rows : RowDel(r)
    \/ \E r \in Rows, w \in Places, ref \in Rows : RowMove(r, w, ref)
    \/ \E f \in Firsts : RowOrder(f)
    \/ \E k \in BOOLEAN : RowSort(k)
    \/ RowSortManual
    \/ \E c \in Cols, w \in Places, ref \in Cols : ColMove(c, w, ref) \/ ColAdd(c, w, ref)
    \/ \E c \in Cols : ColDel(c) \/ Fill(c) \/ Empty(c)
Spec == Init /\ [][Next \/ (step = MaxSteps /\ UNCHANGED vars)]_vars
RefusalWritesNothing == outcome = "refused" => State = prev
\* A move, an order or a sort is a permutation of what was there.
Reorders == {"row-move", "row-order", "row-sort", "row-manual", "col-move"}
ReorderIsPermutation == op \in Reorders =>
 /\ Range(rows) = Range(prev.rows) /\ Range(cols) = Range(prev.cols)
 /\ holds = prev.holds
\* One row or column moved: the others keep their order among themselves.
OthersKeepOrder ==
 /\ op = "row-move" /\ outcome = "ok" =>
      \E r \in Range(rows) : Without(rows, r) = Without(prev.rows, r)
 /\ op \in {"col-move", "col-add"} /\ outcome = "ok" =>
      \E c \in Range(cols) : Without(cols, c) = Without(prev.cols, c)
\* The named rows lead, in the order named; the rest follow in theirs.
OrderKeepsTheRest == (op = "row-order" /\ outcome = "ok") =>
 \E n \in 0..Len(rows) :
   LET first == SubSeq(rows, 1, n)
   IN SubSeq(rows, n + 1, Len(rows)) = SelectSeq(prev.rows, LAMBDA v : v \notin Range(first))
\* While a sort stands, the rows are in its order after every call.
StandingSortHolds == sorted => Ascending(rows)
\* A column that holds members or text is never removed.
HeldColumnsStay == holds \subseteq Range(cols)
\* col del removes an empty column only: what the table holds is unchanged.
ColDelLosesNothing == (op = "col-del" /\ outcome = "ok") => holds = prev.holds
\* Column verbs never touch the rows, row verbs never the columns.
RowsAndColumnsApart ==
 /\ op \in {"col-move", "col-add", "col-del"} => rows = prev.rows
 /\ op \in {"rows-add", "row-del", "row-move", "row-order", "row-sort"} => cols = prev.cols
=============================================================================
