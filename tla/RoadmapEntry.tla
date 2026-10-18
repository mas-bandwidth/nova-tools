---------------------------- MODULE RoadmapEntry -----------------------------
\* nova-work roadmap (cmd/nova-work/roadmap.go, internal/roadmap): the life of
\* one entry of a repository's own work record, docs/roadmap.sexp (ROADMAP.md)
\* and docs/fixes.sexp (FIXES.md). An entry is a roadmap item under a group
\* (todo), a fix in a point release (open: planned or in-progress), a scheduled
\* note (a roadmap item moved to the :scheduled list, naming a release whose
\* :release is not checked), or done: a roadmap item moved to the roadmap's
\* :done list, or a fix whose status is done (or shipped, when its release is
\* cut).
\*
\* The state: where[e], the places whose file holds a record of e (a group or
\* "donelist" in the roadmap file, a release in the fixes file); st[e], the
\* entry's state; live, the groups and releases the files declare; shipped, the
\* releases cut; added, the entries added and not removed; pend, a save across
\* the two files whose writes are not all done (<<>> when none); torn, the
\* entries a crash left in both files. The outside: a hand edit declares a group
\* or release with its first entry, a scheduled note is one whose release is
\* read without being checked, a release is cut, and a verb can stop between any
\* two of its writes.
\*
\* The design, Broken = {}:
\*   Add      an absent id goes into a live group or an uncut release.
\*   Remove   a todo or open entry leaves; never the last of its group or
\*            release (every group and release holds an entry), never from a
\*            cut release.
\*   Pull     a todo or open entry moves to another live group or uncut
\*            release, never out of a cut one and never as the last of its
\*            place. Within one file it is one write. Across the files Save
\*            writes four times, destination file first: its data, its page,
\*            then the source's data, then its page. The destination's data
\*            is written before the source's data, so a stop between writes
\*            leaves the entry twice (torn), never nowhere; within one file
\*            the data is written before its page, so a stop leaves the page
\*            stale, never ahead.
\*   Note     an open entry's title and text change in place; the place, the
\*            state and the rest do not.
\*   Done     a todo item moves to the roadmap's :done list (never the last of
\*            its group); an open fix becomes done in place. Done is final.
\*   Repair   a torn entry is cut back to one copy; every verb refuses while
\*            an id stands twice (check names it), so nothing builds on a tear.
\*   Ship     a release is cut (its :status, a hand edit): nothing is added
\*            to it, removed from it or pulled into or out of it again. Its
\*            fixes' own statuses are a separate edit, so the release's guard
\*            stands on its own.
\* Reversed witnesses, each caught by one property below:
\*   "lastchild"   remove, pull and done skip the last-entry guard: ParentsHold
\*   "sourcefirst" a save across the files writes the source's data first: NoLoss
\*   "pagefirst"   a save writes a file's page before its data: PageNotAhead
\*   "movedone"    pull takes a done entry: DoneFinal
\*   "shippedmove" pull takes an entry out of a cut release: ShippedFrozen
EXTENDS Naturals, FiniteSets

CONSTANTS Entries, Groups, Releases, Broken, Scheduled

VARIABLES where, st, live, shipped, added, pend, torn, title, text, step, page
vars == <<where, st, live, shipped, added, pend, torn, title, text, step, page>>

Files == {"roadmap", "fixes"}
Parents == Groups \cup Releases
Places == Parents \cup {"donelist"}
States == {"absent", "todo", "open", "done", "scheduled"}

\* the state an entry has at a place: an item under a group, a fix in a release
Kind(p) == IF p \in Groups THEN "todo" ELSE "open"
File(p) == IF p \in Releases THEN "fixes" ELSE "roadmap"
Holding(p) == {e \in Entries : p \in where[e]}
\* the one place an entry at rest is in (where[e] is a singleton then)
Home(e) == CHOOSE p \in where[e] : TRUE

TypeOK ==
    /\ where \in [Entries -> SUBSET Places]
    /\ st \in [Entries -> States]
    /\ live \subseteq Parents
    /\ shipped \subseteq live \cap Releases
    /\ added \subseteq Entries
    /\ torn \subseteq Entries
    /\ pend = <<>> \/ (pend[1] \in Entries /\ pend[2] \in Parents /\ pend[3] \in Parents)
    /\ title \in [Entries -> {"", "t"}]
    /\ text \in [Entries -> {"", "x"}]
    /\ step \in 0..4
    /\ page \in [Files -> {"match", "stale", "ahead"}]
    /\ (pend = <<>>) = (step = 0)

Init ==
    /\ \E r \in [Scheduled -> Releases] :
        where = [e \in Entries |-> IF e \in Scheduled THEN {r[e]} ELSE {}]
    /\ st = [e \in Entries |-> IF e \in Scheduled THEN "scheduled" ELSE "absent"]
    /\ live = {}
    /\ shipped = {}
    /\ added = Scheduled
    /\ pend = <<>>
    /\ torn = {}
    /\ title = [e \in Entries |-> ""]
    /\ text = [e \in Entries |-> ""]
    /\ step = 0
    /\ page = [f \in Files |-> "match"]

\* every verb loads both files first, and the load refuses an id that stands
\* twice; no verb runs while another's writes are half done
Quiet == pend = <<>> /\ torn = {}

\* the last-entry guard: an entry may leave p only when p holds another
Leaves(p) == "lastchild" \in Broken \/ Cardinality(Holding(p)) > 1

\* the outside: a hand edit declares a group or release with its first entry
Declare(p, e) ==
    /\ Quiet
    /\ p \notin live
    /\ st[e] = "absent"
    /\ where' = [where EXCEPT ![e] = {p}]
    /\ st' = [st EXCEPT ![e] = Kind(p)]
    /\ live' = live \cup {p}
    /\ added' = added \cup {e}
    /\ UNCHANGED <<shipped, pend, torn, title, text, step, page>>

Add(e, p) ==
    /\ Quiet
    /\ st[e] = "absent"
    /\ p \in live
    /\ p \notin shipped
    /\ where' = [where EXCEPT ![e] = {p}]
    /\ st' = [st EXCEPT ![e] = Kind(p)]
    /\ added' = added \cup {e}
    /\ UNCHANGED <<live, shipped, pend, torn, title, text, step, page>>

Remove(e) ==
    /\ Quiet
    /\ st[e] \in {"todo", "open"}
    /\ Home(e) \notin shipped
    /\ Leaves(Home(e))
    /\ where' = [where EXCEPT ![e] = {}]
    /\ st' = [st EXCEPT ![e] = "absent"]
    /\ added' = added \ {e}
    /\ UNCHANGED <<live, shipped, pend, torn, title, text, step, page>>

\* the guards every pull shares
CanPull(e, to) ==
    /\ Quiet
    /\ st[e] \in IF "movedone" \in Broken THEN {"todo", "open", "done"} ELSE {"todo", "open"}
    /\ where[e] # {}
    /\ Home(e) \in Parents
    /\ to \in live
    /\ to # Home(e)
    /\ to \notin shipped
    /\ "shippedmove" \in Broken \/ Home(e) \notin shipped
    /\ Leaves(Home(e))

\* within one file: one atomic write
PullWithin(e, to) ==
    /\ CanPull(e, to)
    /\ File(to) = File(Home(e))
    /\ where' = [where EXCEPT ![e] = {to}]
    /\ st' = [st EXCEPT ![e] = Kind(to)]
    /\ UNCHANGED <<live, shipped, added, pend, torn, title, text, step, page>>

\* the broken write orders of Save
SourceFirst == "sourcefirst" \in Broken
PageFirst == "pagefirst" \in Broken

\* the first of Save's four writes: the destination's data, or the source's
\* data when the source is written first, or the destination's page when the
\* page is written before its data
PullStart(e, to) ==
    /\ CanPull(e, to)
    /\ File(to) # File(Home(e))
    /\ \/ /\ ~SourceFirst /\ ~PageFirst
          /\ where' = [where EXCEPT ![e] = {Home(e), to}]
          /\ page' = [page EXCEPT ![File(to)] = "stale"]
       \/ /\ SourceFirst
          /\ where' = [where EXCEPT ![e] = {}]
          /\ page' = [page EXCEPT ![File(Home(e))] = "stale"]
       \/ /\ PageFirst
          /\ UNCHANGED where
          /\ page' = [page EXCEPT ![File(to)] = "ahead"]
    /\ pend' = <<e, Home(e), to>>
    /\ step' = 1
    /\ UNCHANGED <<st, live, shipped, added, torn, title, text>>

\* Save's second write: the first file's page, or its data when the page came
\* first (pagefirst)
Save2 ==
    /\ pend # <<>>
    /\ step = 1
    /\ \/ /\ ~SourceFirst /\ ~PageFirst
          /\ UNCHANGED where
          /\ page' = [page EXCEPT ![File(pend[3])] = "match"]
       \/ /\ SourceFirst
          /\ UNCHANGED where
          /\ page' = [page EXCEPT ![File(pend[2])] = "match"]
       \/ /\ PageFirst
          /\ where' = [where EXCEPT ![pend[1]] = {pend[2], pend[3]}]
          /\ page' = [page EXCEPT ![File(pend[3])] = "match"]
    /\ step' = 2
    /\ UNCHANGED <<st, live, shipped, added, pend, torn, title, text>>

\* Save's third write: the source's data, or the source's page when pagefirst
Save3 ==
    /\ pend # <<>>
    /\ step = 2
    /\ \/ /\ ~SourceFirst /\ ~PageFirst
          /\ where' = [where EXCEPT ![pend[1]] = {pend[3]}]
          /\ page' = [page EXCEPT ![File(pend[2])] = "stale"]
       \/ /\ SourceFirst
          /\ where' = [where EXCEPT ![pend[1]] = {pend[3]}]
          /\ page' = [page EXCEPT ![File(pend[3])] = "stale"]
       \/ /\ PageFirst
          /\ UNCHANGED where
          /\ page' = [page EXCEPT ![File(pend[2])] = "ahead"]
    /\ step' = 3
    /\ UNCHANGED <<st, live, shipped, added, pend, torn, title, text>>

\* Save's fourth write: the second file's page, or its data when pagefirst
Save4 ==
    /\ pend # <<>>
    /\ step = 3
    /\ \/ /\ ~SourceFirst /\ ~PageFirst
          /\ UNCHANGED where
          /\ page' = [page EXCEPT ![File(pend[2])] = "match"]
       \/ /\ SourceFirst
          /\ UNCHANGED where
          /\ page' = [page EXCEPT ![File(pend[3])] = "match"]
       \/ /\ PageFirst
          /\ where' = [where EXCEPT ![pend[1]] = {pend[3]}]
          /\ page' = [page EXCEPT ![File(pend[2])] = "match"]
    /\ pend' = <<>>
    /\ step' = 0
    /\ UNCHANGED <<st, live, shipped, added, torn, title, text>>

\* the verb stops between two of its writes: what is on disk stays
Crash ==
    /\ pend # <<>>
    /\ pend' = <<>>
    /\ step' = 0
    /\ page' = [f \in Files |-> "match"]
    /\ torn' = IF Cardinality(where[pend[1]]) > 1 THEN torn \cup {pend[1]} ELSE torn
    /\ UNCHANGED <<where, st, live, shipped, added, title, text>>

\* check names the id that stands twice; one copy is cut (by hand)
Repair(e, p) ==
    /\ pend = <<>>
    /\ e \in torn
    /\ p \in where[e]
    /\ where' = [where EXCEPT ![e] = {p}]
    /\ st' = [st EXCEPT ![e] = Kind(p)]
    /\ torn' = torn \ {e}
    /\ UNCHANGED <<live, shipped, added, pend, title, text, step, page>>

\* Note changes an open entry's title and text in place; the values are
\* abstracted to a flag ("" none, "t" a title, "x" text) since no invariant
\* reads them
Note(e) ==
    /\ Quiet
    /\ st[e] \in {"todo", "open"}
    /\ title' \in {title, [title EXCEPT ![e] = "t"]}
    /\ text' \in {text, [text EXCEPT ![e] = "x"]}
    /\ title' # title \/ text' # text
    /\ UNCHANGED <<where, st, live, shipped, added, pend, torn, step, page>>

Done(e) ==
    /\ Quiet
    /\ st[e] \in {"todo", "open"}
    /\ Home(e) \notin shipped
    /\ \/ /\ Home(e) \in Groups
          /\ Leaves(Home(e))
          /\ where' = [where EXCEPT ![e] = {"donelist"}]
       \/ /\ Home(e) \in Releases
          /\ UNCHANGED where
    /\ st' = [st EXCEPT ![e] = "done"]
    /\ UNCHANGED <<live, shipped, added, pend, torn, title, text, step, page>>

\* the outside: a release is cut
Ship(r) ==
    /\ Quiet
    /\ r \in live \cap Releases
    /\ r \notin shipped
    /\ shipped' = shipped \cup {r}
    /\ UNCHANGED <<where, st, live, added, pend, torn, title, text, step, page>>

Next ==
    \/ \E p \in Parents, e \in Entries : Declare(p, e)
    \/ \E e \in Entries, p \in Parents : Add(e, p)
    \/ \E e \in Entries : Remove(e)
    \/ \E e \in Entries, p \in Parents : PullWithin(e, p) \/ PullStart(e, p)
    \/ Save2 \/ Save3 \/ Save4
    \/ Crash
    \/ \E e \in Entries, p \in Places : Repair(e, p)
    \/ \E e \in Entries : Done(e)
    \/ \E e \in Entries : Note(e)
    \/ \E r \in Releases : Ship(r)

Spec == Init /\ [][Next]_vars

\* ---------------------------------------------------------------- the rules

\* every group and release the files declare holds an entry (the decoder
\* refuses "group holds no item" and "release holds no fix")
ParentsHold == \A p \in live : Holding(p) # {}

\* every record sits under a group or release the files declare, except a
\* scheduled note, whose :release decode reads without checking (the fixes'
\* items are checked, fixes.go:130-131, the scheduled note's is not,
\* roadmap.go:278)
ChildLinked == \A e \in Entries \ Scheduled : \A p \in where[e] : p \in live \/ p = "donelist"

\* an entry added and not removed is in a file: no write order loses it
NoLoss == \A e \in added : where[e] # {}

\* at rest an entry is in one place; twice only mid-save or after a stop that
\* check names
OneHome == \A e \in Entries :
    Cardinality(where[e]) <= 1 \/ (pend # <<>> /\ pend[1] = e) \/ e \in torn

\* done is final: a done entry never moves and never comes back
DoneFinal == [][\A e \in Entries : st[e] = "done" => (st'[e] = "done" /\ where'[e] = where[e])]_vars

\* a cut release's entries never change
ShippedFrozen == [][\A r \in shipped : {e \in Entries : r \in where'[e]} = Holding(r)]_vars

\* Save writes each file's data before its page, so a stop between them leaves
\* the page stale, never ahead of the data
PageNotAhead == \A f \in Files : page[f] # "ahead"

=============================================================================
