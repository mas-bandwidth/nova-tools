---------------------------- MODULE TableEdit ----------------------------
\* The edit verbs of nova-table (table.lua as composed in nova-tools#4457):
\* set (footer, rename, columns), row add (many), row hide/show (many),
\* row set (text), column hide/show, beside cell add/move which place the
\* members the edits must keep. One table; Taken are the names other
\* tables hold.
\*
\* Staged = TRUE is the kernel of #4457: every check, the stored key types
\* and the command permissions included, comes before the first write.
\* Staged = FALSE is table.lua at 109939a85, kept as the reversed witness:
\* a batch writes item by item, set writes the footer before RENAME, and an
\* unparsable stored definition empties the OCCUPIED check (Stella's read
\* of #4456, stella-64cde7fb5261, six findings verified against the code).
EXTENDS Naturals, FiniteSets
CONSTANTS NRows, Cols, Members, Texts, Names, Taken, InitName, InitDef,
          Staged, MaxSteps
Rows == 1..NRows
Stored == {"absent","set","text","formula","invalid"}
Shapes == [Cols -> {"absent","set","text","formula"}]
None == <<0,"none">>
Places == (Rows \X Cols) \cup {None}
VARIABLES name, def, rows, hiddenR, hiddenC, place, text, footer,
          op, outcome, prev, step
State == [name |-> name, def |-> def, rows |-> rows, hiddenR |-> hiddenR,
          hiddenC |-> hiddenC, place |-> place, text |-> text,
          footer |-> footer]
vars == <<name, def, rows, hiddenR, hiddenC, place, text, footer,
          op, outcome, prev, step>>
Labels == {"", "total"}
TypeOK ==
 /\ name \in Names \ Taken
 /\ def \in [Cols -> Stored]
 /\ rows \subseteq Rows
 /\ hiddenR \subseteq Rows
 /\ hiddenC \subseteq Cols
 /\ place \in [Members -> Places]
 /\ text \in [Rows \X Cols -> Texts \cup {""}]
 /\ footer \in Labels
 /\ outcome \in {"ok","refused"}
 /\ step \in 0..MaxSteps
Init ==
 /\ name = InitName /\ def = InitDef /\ rows = {} /\ hiddenR = {}
 /\ hiddenC = {} /\ place = [m \in Members |-> None]
 /\ text = [c \in Rows \X Cols |-> ""] /\ footer = ""
 /\ op = "initial" /\ outcome = "ok" /\ step = 0
 /\ prev = [name |-> InitName, def |-> InitDef, rows |-> {}, hiddenR |-> {},
            hiddenC |-> {}, place |-> [m \in Members |-> None],
            text |-> [c \in Rows \X Cols |-> ""], footer |-> ""]
\* Leave(kind, result, s): the store holds s after the call.
Leave(kind, result, s) ==
 /\ name' = s.name /\ def' = s.def /\ rows' = s.rows
 /\ hiddenR' = s.hiddenR /\ hiddenC' = s.hiddenC /\ place' = s.place
 /\ text' = s.text /\ footer' = s.footer
 /\ op' = kind /\ outcome' = result /\ prev' = State /\ step' = step + 1
Commit(kind, s) == Leave(kind, "ok", s)
Refuse(kind) == Leave(kind, "refused", State)
\* A refusal that arrives after writes: only the unstaged code can do it.
Partial(kind, s) == Leave(kind, "refused", IF Staged THEN State ELSE s)
Before(R, bad) == {r \in R : \A b \in R \cap bad : r < b}
Holds(c) == \E m \in Members : place[m] # None /\ place[m][2] = c
\* ns_table_rows_add: bad are the row keys holding the wrong type.
RowsAdd(R, bad) ==
 IF R \cap bad = {} THEN Commit("rows-add", [State EXCEPT !.rows = rows \cup R])
 ELSE Partial("rows-add", [State EXCEPT !.rows = rows \cup Before(R, bad)])
\* ns_table_rows_hide
RowsHide(R, hide, bad) ==
 LET to(S) == IF hide THEN hiddenR \cup S ELSE hiddenR \ S
 IN IF ~(R \subseteq rows) THEN Refuse("rows-hide")
    ELSE IF R \cap bad = {} THEN Commit("rows-hide", [State EXCEPT !.hiddenR = to(R)])
    ELSE Partial("rows-hide", [State EXCEPT !.hiddenR = to(Before(R, bad))])
\* ns_table_set footer and rename; denied is the ACL refusing RENAME.
SetName(f, n, denied) ==
 IF n \in Taken THEN Refuse("set")
 ELSE IF denied /\ n # name THEN Partial("set", [State EXCEPT !.footer = f])
 ELSE Commit("set", [State EXCEPT !.footer = f, !.name = n])
\* ns_table_set columns: the definition replaced, rows kept.
Known(c) == IF Staged THEN def[c] \in {"set","invalid"}
            ELSE def[c] = "set" /\ \A d \in Cols : def[d] # "invalid"
Occupied(nd) == \E c \in Cols : Known(c) /\ nd[c] # "set" /\ Holds(c)
TextHeld(nd) == \E c \in Cols, r \in Rows :
                  def[c] = "text" /\ nd[c] # "text" /\ text[<<r,c>>] # ""
SetColumns(nd) ==
 IF Occupied(nd) \/ (Staged /\ TextHeld(nd)) THEN Refuse("shape")
 ELSE Commit("shape", [State EXCEPT
   !.def = nd,
   !.hiddenC = {c \in hiddenC : nd[c] # "absent"},
   !.place = [m \in Members |->
               IF place[m] # None /\ nd[place[m][2]] # "set" THEN None ELSE place[m]],
   !.text = [k \in Rows \X Cols |-> IF nd[k[2]] = "text" THEN text[k] ELSE ""]])
ColsHide(C, hide) ==
 IF \E c \in C : def[c] = "absent" THEN Refuse("cols-hide")
 ELSE Commit("cols-hide", [State EXCEPT !.hiddenC = IF hide THEN hiddenC \cup C ELSE hiddenC \ C])
\* ns_table_row_set: text columns only; "" clears.
RowSet(r, c, v) ==
 IF r \notin rows \/ def[c] # "text" THEN Refuse("row-set")
 ELSE Commit("row-set", [State EXCEPT !.text[<<r,c>>] = v])
\* ONE PLACE: add refuses a member that has a place.
CellAdd(m, r, c) ==
 IF r \notin rows \/ def[c] # "set" \/ place[m] # None THEN Refuse("cell-add")
 ELSE Commit("cell-add", [State EXCEPT !.place[m] = <<r,c>>])
CellMove(m, c) ==
 IF place[m] = None \/ def[c] # "set" \/ def[place[m][2]] # "set" THEN Refuse("cell-move")
 ELSE Commit("cell-move", [State EXCEPT !.place[m] = <<place[m][1], c>>])
CellRemove(m) ==
 IF place[m] = None \/ def[place[m][2]] # "set" THEN Refuse("cell-remove")
 ELSE Commit("cell-remove", [State EXCEPT !.place[m] = None])
\* Outside event: a stored column definition no later library parses (the
\* legacy pct:avg of the repair-occupied probe). The set stays where it was.
Legacy(c) ==
 /\ def[c] = "set" /\ \A d \in Cols : def[d] # "invalid"
 /\ Commit("legacy", [State EXCEPT !.def[c] = "invalid"])
Next ==
 /\ step < MaxSteps
 /\ \/ \E R \in SUBSET Rows \ {{}}, bad \in SUBSET Rows : RowsAdd(R, bad)
    \/ \E R \in SUBSET Rows \ {{}}, h \in BOOLEAN, bad \in SUBSET Rows : RowsHide(R, h, bad)
    \/ \E f \in Labels, n \in Names, d \in BOOLEAN : SetName(f, n, d)
    \/ \E nd \in Shapes : SetColumns(nd)
    \/ \E C \in SUBSET Cols \ {{}}, h \in BOOLEAN : ColsHide(C, h)
    \/ \E r \in Rows, c \in Cols, v \in Texts \cup {""} : RowSet(r, c, v)
    \/ \E m \in Members, r \in Rows, c \in Cols : CellAdd(m, r, c)
    \/ \E m \in Members, c \in Cols : CellMove(m, c)
    \/ \E m \in Members : CellRemove(m)
    \/ \E c \in Cols : Legacy(c)
Spec == Init /\ [][Next \/ (step = MaxSteps /\ UNCHANGED vars)]_vars
\* A refusal writes nothing.
RefusalWritesNothing == outcome = "refused" => State = prev
\* A definition change never drops a placed member or a text value.
ShapeLosesNothing == (op = "shape" /\ outcome = "ok") =>
 /\ \A m \in Members : prev.place[m] # None => place[m] = prev.place[m]
 /\ \A k \in Rows \X Cols : prev.text[k] # "" => text[k] = prev.text[k]
\* Every placed member sits in a row and a set column the table has.
PlacedInShape == \A m \in Members : place[m] # None =>
 place[m][1] \in rows /\ def[place[m][2]] \in {"set","invalid"}
\* Text lives in text columns only.
TextInTextColumns == \A k \in Rows \X Cols : text[k] # "" => def[k[2]] = "text"
\* Hiding changes what is drawn, never what is held.
HideKeepsData == op \in {"rows-hide","cols-hide"} =>
 place = prev.place /\ text = prev.text /\ rows = prev.rows /\ def = prev.def
\* Rename and footer move nothing else.
RenameKeepsData == op = "set" =>
 place = prev.place /\ text = prev.text /\ rows = prev.rows /\ def = prev.def
   /\ hiddenR = prev.hiddenR /\ hiddenC = prev.hiddenC
=============================================================================
