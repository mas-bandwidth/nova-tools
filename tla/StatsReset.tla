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
(* The figures here are the ones the tick counts into the where record     *)
(* (store/where.go keepWhere: a stream's total, work, read and per-tier    *)
(* spend, and its per landed): the record is taken by where while it was   *)
(* counted at the work table's revision. A reset writes no table, so it    *)
(* does not move the revision: it must clear the record, or where would    *)
(* take a record counted against the mark before it. The fleet rows' done *)
(* and ok% are counted at read time from the mark in force (FleetSince),   *)
(* which is Since below with no record between.                           *)
(*                                                                         *)
(* Actions: a figure grows (a card lands, a take or read is priced), a     *)
(* figure shrinks (a tidy takes finished cards off a row's done cells, so  *)
(* the epoch's count can fall below the mark's), a new row or stream       *)
(* appears, the tick counts the record, and the reset.                     *)
(*                                                                         *)
(* Invariant RecordShowsEpochLessMark: every shown figure equals the epoch *)
(* figure less the figure at the mark, never below zero. Reversed witness: *)
(* with BrokenKeepsRecord the reset leaves the where record in place, and  *)
(* TLC finds the record counted before the reset taken after it.           *)
(***************************************************************************)
EXTENDS Integers, FiniteSets

CONSTANTS Figures,            \* the rows and streams, each one figure here
          MaxFig,             \* a figure's bound
          MaxRev,             \* the work table's revision bound
          BrokenKeepsRecord   \* TRUE: the reset does not clear the where record

VARIABLES live,    \* the figures that exist
          fig,     \* the epoch's figure of each
          marked,  \* a mark is in force
          mark,    \* the mark's figure of each it knows
          known,   \* the figures the mark knows
          rev,     \* the work table's revision
          rec      \* the where record: counted at rec.rev, rec.vals; or NoRecord

vars == <<live, fig, marked, mark, known, rev, rec>>

NoRecord == [rev |-> -1, vals |-> [f \in Figures |-> 0]]

Less(a, b) == IF a > b THEN a - b ELSE 0

\* the figure as it should be shown: since the mark when the mark knows it
Since(f) == IF marked /\ f \in known THEN Less(fig[f], mark[f]) ELSE fig[f]

TypeOK ==
    /\ live \subseteq Figures
    /\ fig \in [Figures -> 0..MaxFig]
    /\ marked \in BOOLEAN
    /\ mark \in [Figures -> 0..MaxFig]
    /\ known \subseteq Figures
    /\ rev \in 0..MaxRev
    /\ rec.rev \in -1..MaxRev
    /\ rec.vals \in [Figures -> 0..MaxFig]

Init ==
    /\ live = {}
    /\ fig = [f \in Figures |-> 0]
    /\ marked = FALSE
    /\ mark = [f \in Figures |-> 0]
    /\ known = {}
    /\ rev = 0
    /\ rec = NoRecord

\* a table write: the revision moves
Write == rev < MaxRev /\ rev' = rev + 1

Appear(f) ==
    /\ f \notin live
    /\ Write
    /\ live' = live \cup {f}
    /\ UNCHANGED <<fig, marked, mark, known, rec>>

Grow(f) ==
    /\ f \in live
    /\ fig[f] < MaxFig
    /\ Write
    /\ fig' = [fig EXCEPT ![f] = @ + 1]
    /\ UNCHANGED <<live, marked, mark, known, rec>>

\* a tidy takes finished cards off a row: the epoch's count falls
Shrink(f) ==
    /\ f \in live
    /\ fig[f] > 0
    /\ Write
    /\ fig' = [fig EXCEPT ![f] = @ - 1]
    /\ UNCHANGED <<live, marked, mark, known, rec>>

\* the tick counts the where record from the sprint and the mark in force
Count ==
    /\ rec' = [rev |-> rev, vals |-> [f \in Figures |-> IF f \in live THEN Since(f) ELSE 0]]
    /\ UNCHANGED <<live, fig, marked, mark, known, rev>>

\* the reset: one write of the mark into the stats record, no table written;
\* the where record is left to the next tick
Reset ==
    /\ marked' = TRUE
    /\ mark' = [f \in Figures |-> IF f \in live THEN fig[f] ELSE 0]
    /\ known' = live
    /\ rec' = IF BrokenKeepsRecord THEN rec ELSE NoRecord
    /\ UNCHANGED <<live, fig, rev>>

Next ==
    \/ \E f \in Figures : Appear(f) \/ Grow(f) \/ Shrink(f)
    \/ Count
    \/ Reset

Spec == Init /\ [][Next]_vars

\* where takes the record only when it was counted at the table's revision
Taken == rec.rev = rev

\* every shown figure equals the epoch figure less the figure at the mark,
\* never below zero (a figure the mark does not know: the epoch's)
RecordShowsEpochLessMark ==
    Taken => \A f \in live : rec.vals[f] = Since(f)

ShownNeverBelowZero ==
    \A f \in live : Since(f) >= 0 /\ (marked /\ f \in known => Since(f) <= fig[f])

\* a figure the mark does not know reads as before
UnknownReadsAsBefore ==
    \A f \in live \ known : Since(f) = fig[f]
=============================================================================
