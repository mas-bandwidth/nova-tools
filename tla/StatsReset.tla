---------------------------- MODULE StatsReset ----------------------------
(***************************************************************************)
(* nova-sprint stats reset (internal/sprint/stats_reset.go,                *)
(* internal/sprint/store/stats_reset.go; the owner, 2026-10-09: "Clear the *)
(* cost per-card right now. Clear the per-tier costs. Clear the total      *)
(* cost.", "Clear the done and the ok% for all friends now.").             *)
(*                                                                         *)
(* A reset moves nothing. It writes one mark, the figures as they stand,   *)
(* and every figure the mark covers is shown as the epoch's figure less    *)
(* the mark's, never below zero; a figure the mark does not know (a row or *)
(* a stream that came after it) is shown as it is. A second reset replaces *)
(* the mark.                                                               *)
(*                                                                         *)
(* A figure here is a count of finished things on a row (a fleet row's     *)
(* done cells; a stream's priced spend counts the same way). A ghost,      *)
(* after, counts the ones finished since the mark (all of them with no     *)
(* mark, or for a figure the mark does not know): that is what the reset   *)
(* promises to show.                                                       *)
(*                                                                         *)
(* A stats tidy takes the history off a row: the oldest finished cards,    *)
(* those before the mark first. Later wins: a tidy after the reset leaves  *)
(* the row's figure since the mark as it was, which holds only because the *)
(* tidy rebases the mark's counter by the cards it moved                   *)
(* (sprint.ResetMark.Rebase, in the tidy's own compare-and-set write of    *)
(* the stats record). Cards since the mark that a tidy moves are off the   *)
(* row as the tidy says: the later of the two.                             *)
(*                                                                         *)
(* The where record (store/where.go keepWhere) caches the shown figures:   *)
(* the tick reads the sprint and the stats record in one step and writes   *)
(* the record in a later one. where takes it only while it was counted at  *)
(* the table's revision and from the stats record in force (its stamp,     *)
(* statsStamp). A reset writes no table, so the revision alone would keep  *)
(* a record counted before it, including one a tick read before the reset  *)
(* and wrote after it.                                                     *)
(*                                                                         *)
(* Invariants: ShownIsSinceMark (the figure shown is the cards since the   *)
(* mark: the epoch's less the mark's, never below zero), RecordShowsShown  *)
(* (a record where takes shows exactly that), UnknownReadsAsBefore.        *)
(* Reversed witnesses: Broken = "norebase" (the tidy keeps the mark's      *)
(* counter: a row tidied after a reset reads 0 until new cards outnumber   *)
(* the mark) breaks ShownIsSinceMark; Broken = "nostamp" (where takes a    *)
(* record by its revision alone) breaks RecordShowsShown.                  *)
(***************************************************************************)
EXTENDS Integers, FiniteSets

CONSTANTS Figures,     \* the rows and streams, each one figure here
          MaxFig,      \* a figure's bound
          MaxRev,      \* the table's revision bound (every table write)
          MaxResets,   \* how many resets
          Broken       \* "none", "norebase" or "nostamp"

VARIABLES live,     \* the figures that exist
          fig,      \* the epoch's figure of each (cards on its cells)
          after,    \* ghost: of those, the ones finished since the mark
          marked,   \* a mark is in force
          mark,     \* the mark's counter of each it knows
          known,    \* the figures the mark knows
          stamp,    \* the stats record's stamp: one more at every reset
          rev,      \* the table's revision
          rec,      \* the where record: counted at rec.rev from rec.stamp
          reading   \* a tick's read not yet written: what it would write, or NoRecord

vars == <<live, fig, after, marked, mark, known, stamp, rev, rec, reading>>

NoRecord == [rev |-> -1, stamp |-> -1, vals |-> [f \in Figures |-> 0]]

Less(a, b) == IF a > b THEN a - b ELSE 0

\* the figure shown: the epoch's less the mark's when the mark knows it
Shown(f) == IF marked /\ f \in known THEN Less(fig[f], mark[f]) ELSE fig[f]

TypeOK ==
    /\ live \subseteq Figures
    /\ fig \in [Figures -> 0..MaxFig]
    /\ after \in [Figures -> 0..MaxFig]
    /\ \A f \in Figures : after[f] <= fig[f]
    /\ marked \in BOOLEAN
    /\ mark \in [Figures -> 0..MaxFig]
    /\ known \subseteq Figures
    /\ stamp \in 0..MaxResets
    /\ rev \in 0..MaxRev
    /\ rec.rev \in -1..MaxRev /\ rec.stamp \in -1..MaxResets
    /\ reading.rev \in -1..MaxRev /\ reading.stamp \in -1..MaxResets

Init ==
    /\ live = {}
    /\ fig = [f \in Figures |-> 0]
    /\ after = [f \in Figures |-> 0]
    /\ marked = FALSE
    /\ mark = [f \in Figures |-> 0]
    /\ known = {}
    /\ stamp = 0
    /\ rev = 0
    /\ rec = NoRecord
    /\ reading = NoRecord

\* a table write: the revision moves
Write == rev < MaxRev /\ rev' = rev + 1

Appear(f) ==
    /\ f \notin live
    /\ Write
    /\ live' = live \cup {f}
    /\ UNCHANGED <<fig, after, marked, mark, known, stamp, rec, reading>>

\* a card finishes on the row (a take or read priced, for a stream)
Grow(f) ==
    /\ f \in live
    /\ fig[f] < MaxFig
    /\ Write
    /\ fig' = [fig EXCEPT ![f] = @ + 1]
    /\ after' = [after EXCEPT ![f] = @ + 1]
    /\ UNCHANGED <<live, marked, mark, known, stamp, rec, reading>>

\* a stats tidy takes the k oldest finished cards off the row, those before
\* the mark first, and rebases the mark's counter by them
Tidy(f, k) ==
    /\ f \in live
    /\ k \in 1..fig[f]
    /\ Write
    /\ LET before == fig[f] - after[f]
       IN after' = [after EXCEPT ![f] = @ - Less(k, before)]
    /\ fig' = [fig EXCEPT ![f] = @ - k]
    /\ mark' = IF Broken = "norebase" THEN mark ELSE [mark EXCEPT ![f] = Less(@, k)]
    /\ UNCHANGED <<live, marked, known, stamp, rec, reading>>

\* the tick reads the sprint and the stats record in force: what it will write
CountRead ==
    /\ reading' = [rev |-> rev, stamp |-> stamp, vals |-> [f \in Figures |-> IF f \in live THEN Shown(f) ELSE 0]]
    /\ UNCHANGED <<live, fig, after, marked, mark, known, stamp, rev, rec>>

\* and writes the where record, maybe after a reset in between
CountWrite ==
    /\ reading.rev >= 0
    /\ rec' = reading
    /\ reading' = NoRecord
    /\ UNCHANGED <<live, fig, after, marked, mark, known, stamp, rev>>

\* the reset: one write of the mark into the stats record (its stamp moves),
\* no table written; the where record is emptied for the next tick
Reset ==
    /\ stamp < MaxResets
    /\ marked' = TRUE
    /\ mark' = [f \in Figures |-> IF f \in live THEN fig[f] ELSE 0]
    /\ after' = [f \in Figures |-> 0]
    /\ known' = live
    /\ stamp' = stamp + 1
    /\ rec' = NoRecord
    /\ UNCHANGED <<live, fig, rev, reading>>

Next ==
    \/ \E f \in Figures : Appear(f) \/ Grow(f) \/ \E k \in 1..MaxFig : Tidy(f, k)
    \/ CountRead
    \/ CountWrite
    \/ Reset

Spec == Init /\ [][Next]_vars

\* where takes the record only when it was counted at the table's revision
\* and from the stats record in force
Taken == rec.rev = rev /\ (Broken = "nostamp" \/ rec.stamp = stamp)

\* every shown figure is the cards since the mark: the epoch figure less the
\* figure at the mark, never below zero, a tidy later than the mark included
ShownIsSinceMark ==
    \A f \in live : Shown(f) = after[f]

\* a record where takes shows exactly that
RecordShowsShown ==
    Taken => \A f \in live : rec.vals[f] = Shown(f)

\* a figure the mark does not know reads as before
UnknownReadsAsBefore ==
    \A f \in live \ known : Shown(f) = fig[f]
=============================================================================
