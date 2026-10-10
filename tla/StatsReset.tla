---------------------------- MODULE StatsReset ----------------------------
(***************************************************************************)
(* nova-sprint stats reset and stats tidy together                         *)
(* (internal/sprint/stats_reset.go, internal/sprint/store/stats_reset.go,  *)
(* internal/sprint/store/stats_tidy.go; the owner, 2026-10-09: "Clear the  *)
(* cost per-card right now. Clear the per-tier costs. Clear the total      *)
(* cost.", "Clear the done and the ok% for all friends now.").             *)
(*                                                                         *)
(* A reset moves nothing: it writes one mark, the figures as they stand,   *)
(* and every figure the mark covers is shown as the epoch's figure less    *)
(* the mark's, never below zero. A figure here is a count of finished      *)
(* cards on a row (a fleet row's done cells; a stream's priced spend       *)
(* counts the same way). Ghosts count the cards by when they finished:     *)
(* after[f], those since the mark in force (all of them with no mark, or   *)
(* for a figure the mark does not know), and pend[f], those since a        *)
(* reset's read still to be written.                                       *)
(*                                                                         *)
(* Both writers are two steps, as in the code, so TLC sees them            *)
(* interleave:                                                             *)
(*  - a reset reads the stats record (its version, ver; refused while a    *)
(*    tidy is in flight) and the sprint (ResetRead), then writes the mark  *)
(*    by compare-and-set: only while the record is the version it read,    *)
(*    else it reads again (ResetWrite, ResetRetry);                        *)
(*  - a tidy writes its marker, the mark in force then (TidyStart), moves  *)
(*    any subset of a row's finished cards, not the oldest first           *)
(*    (TidyMove: a card whose primary has not landed stays whatever its    *)
(*    age), then writes its end: the marker cleared and the mark it saw    *)
(*    rebased by the moved cards that finished before it (TidyEnd;         *)
(*    sprint.ResetMark.Rebase, by each card's stamp).                      *)
(* The tidy takes cards off for good: the figure since the mark is the     *)
(* cards since it still on the row (later wins).                           *)
(*                                                                         *)
(* The where record (store/where.go keepWhere) caches the shown figures:   *)
(* the tick reads in one step and writes in a later one, and where takes   *)
(* the record only at the table's revision and the stats record's stamp.   *)
(* The model stamps with the record's version; the code's stamp names the  *)
(* fields the record caches (the streams' tidy and the mark), the rows'    *)
(* done being counted at every read.                                       *)
(*                                                                         *)
(* Invariants: ShownIsSinceMark (with no tidy in flight, every shown       *)
(* figure is the cards since the mark still on the row: the epoch's less   *)
(* the mark's, never below zero), RecordShowsShown (a record where takes   *)
(* shows exactly the figures), UnknownReadsAsBefore. Between a tidy's move *)
(* and its end the mark is not yet rebased: the row reads low for that     *)
(* one step, which ShownIsSinceMark leaves out.                            *)
(*                                                                         *)
(* Reversed witnesses (Broken): "norebase", the tidy keeps the mark's      *)
(* counter; "bycount", the tidy lowers it by one per moved card, finished  *)
(* before the mark or not (the first cut); "nolock", the reset neither     *)
(* waits for a tidy in flight nor writes by compare-and-set, its mark      *)
(* counted before a tidy's move written after it, and the tidy rebasing a  *)
(* mark counted after its move; each breaks ShownIsSinceMark. "nostamp",   *)
(* where takes a record by its revision alone, breaks RecordShowsShown.    *)
(***************************************************************************)
EXTENDS Integers, FiniteSets

CONSTANTS Figures,     \* the rows and streams, each one figure here
          MaxFig,      \* a figure's bound
          MaxRev,      \* the table's revision bound (every table write)
          MaxVer,      \* the stats record's version bound (every write of it)
          Broken       \* "none", "norebase", "bycount", "nolock" or "nostamp"

VARIABLES live, fig, after, pend,
          marked, mark, known, markId,
          ver, rev, rec, reading,
          rr,       \* a reset's read: active, the version read, the figures, the live set
          tidying, tseen, tpre, tall, tmoved

vars == <<live, fig, after, pend, marked, mark, known, markId, ver, rev, rec, reading,
          rr, tidying, tseen, tpre, tall, tmoved>>

Zero == [f \in Figures |-> 0]
NoRecord == [rev |-> -1, stamp |-> -1, vals |-> Zero]
NoRead == [active |-> FALSE, ver |-> -1, vals |-> Zero, live |-> {}]

Less(a, b) == IF a > b THEN a - b ELSE 0

\* the figure shown: the epoch's less the mark's when the mark knows it
Shown(f) == IF marked /\ f \in known THEN Less(fig[f], mark[f]) ELSE fig[f]

TypeOK ==
    /\ live \subseteq Figures
    /\ fig \in [Figures -> 0..MaxFig]
    /\ after \in [Figures -> 0..MaxFig] /\ pend \in [Figures -> 0..MaxFig]
    /\ \A f \in Figures : pend[f] <= after[f] /\ after[f] <= fig[f]
    /\ marked \in BOOLEAN /\ mark \in [Figures -> 0..MaxFig] /\ known \subseteq Figures
    /\ ver \in 0..MaxVer /\ rev \in 0..MaxRev
    /\ tidying \in BOOLEAN /\ tmoved \in BOOLEAN
    /\ tpre \in [Figures -> 0..MaxFig] /\ tall \in [Figures -> 0..MaxFig]

Init ==
    /\ live = {} /\ fig = Zero /\ after = Zero /\ pend = Zero
    /\ marked = FALSE /\ mark = Zero /\ known = {} /\ markId = 0
    /\ ver = 0 /\ rev = 0 /\ rec = NoRecord /\ reading = NoRecord
    /\ rr = NoRead
    /\ tidying = FALSE /\ tseen = 0 /\ tpre = Zero /\ tall = Zero /\ tmoved = FALSE

\* a table write: the revision moves
Write == rev < MaxRev /\ rev' = rev + 1
\* a write of the stats record: its version moves
Rewrite == ver < MaxVer /\ ver' = ver + 1

Appear(f) ==
    /\ f \notin live
    /\ Write
    /\ live' = live \cup {f}
    /\ UNCHANGED <<fig, after, pend, marked, mark, known, markId, ver, rec, reading, rr, tidying, tseen, tpre, tall, tmoved>>

\* a card finishes on the row: since the mark, and since a reset's read in flight
Grow(f) ==
    /\ f \in live
    /\ fig[f] < MaxFig
    /\ Write
    /\ fig' = [fig EXCEPT ![f] = @ + 1]
    /\ after' = [after EXCEPT ![f] = @ + 1]
    /\ pend' = IF rr.active THEN [pend EXCEPT ![f] = @ + 1] ELSE pend
    /\ UNCHANGED <<live, marked, mark, known, markId, ver, rec, reading, rr, tidying, tseen, tpre, tall, tmoved>>

\* ---- the reset, two steps ----
ResetRead ==
    /\ ~rr.active
    /\ (Broken = "nolock" \/ ~tidying)
    /\ rr' = [active |-> TRUE, ver |-> ver, vals |-> fig, live |-> live]
    /\ pend' = Zero
    /\ UNCHANGED <<live, fig, after, marked, mark, known, markId, ver, rev, rec, reading, tidying, tseen, tpre, tall, tmoved>>

\* the compare-and-set found the record changed: the reset reads again
ResetRetry ==
    /\ rr.active
    /\ Broken # "nolock"
    /\ rr.ver # ver
    /\ rr' = NoRead
    /\ pend' = Zero
    /\ UNCHANGED <<live, fig, after, marked, mark, known, markId, ver, rev, rec, reading, tidying, tseen, tpre, tall, tmoved>>

ResetWrite ==
    /\ rr.active
    /\ Broken = "nolock" \/ (rr.ver = ver /\ ~tidying)
    /\ Rewrite
    /\ marked' = TRUE
    /\ mark' = [f \in Figures |-> IF f \in rr.live THEN rr.vals[f] ELSE 0]
    /\ known' = rr.live
    /\ after' = [f \in Figures |-> IF f \in rr.live THEN pend[f] ELSE after[f]]
    /\ markId' = markId + 1
    /\ rec' = NoRecord
    /\ rr' = NoRead
    /\ pend' = Zero
    /\ UNCHANGED <<live, fig, rev, reading, tidying, tseen, tpre, tall, tmoved>>

\* ---- the tidy, three steps ----
TidyStart ==
    /\ ~tidying
    /\ Rewrite
    /\ tidying' = TRUE
    /\ tseen' = markId
    /\ tpre' = Zero /\ tall' = Zero /\ tmoved' = FALSE
    /\ UNCHANGED <<live, fig, after, pend, marked, mark, known, markId, rev, rec, reading, rr>>

\* any subset of the row's finished cards: k0 from before the mark, k1 since the mark and
\* before a reset's read in flight, k2 since that read
TidyMove(f) ==
    /\ tidying /\ ~tmoved
    /\ f \in live
    /\ \E k0 \in 0..(fig[f] - after[f]), k1 \in 0..(after[f] - pend[f]), k2 \in 0..pend[f] :
        /\ k0 + k1 + k2 > 0
        /\ Write
        /\ fig' = [fig EXCEPT ![f] = @ - k0 - k1 - k2]
        /\ after' = [after EXCEPT ![f] = @ - k1 - k2]
        /\ pend' = [pend EXCEPT ![f] = @ - k2]
        /\ tpre' = [tpre EXCEPT ![f] = k0]
        /\ tall' = [tall EXCEPT ![f] = k0 + k1 + k2]
    /\ tmoved' = TRUE
    /\ UNCHANGED <<live, marked, mark, known, markId, ver, rec, reading, rr, tidying, tseen>>

\* the end: the marker cleared, the mark it saw rebased by the moved cards before it
TidyEnd ==
    /\ tidying
    /\ Rewrite
    /\ tidying' = FALSE
    /\ mark' = CASE Broken = "norebase" -> mark
               [] Broken = "bycount" -> [f \in Figures |-> Less(mark[f], tall[f])]
               [] Broken = "nolock" -> [f \in Figures |-> Less(mark[f], tpre[f])]
               [] OTHER -> IF tseen = markId THEN [f \in Figures |-> Less(mark[f], tpre[f])] ELSE mark
    /\ tpre' = Zero /\ tall' = Zero /\ tmoved' = FALSE
    /\ UNCHANGED <<live, fig, after, pend, marked, known, markId, rev, rec, reading, rr, tseen>>

\* ---- the tick's where record, two steps ----
CountRead ==
    /\ reading' = [rev |-> rev, stamp |-> ver, vals |-> [f \in Figures |-> IF f \in live THEN Shown(f) ELSE 0]]
    /\ UNCHANGED <<live, fig, after, pend, marked, mark, known, markId, ver, rev, rec, rr, tidying, tseen, tpre, tall, tmoved>>

CountWrite ==
    /\ reading.rev >= 0
    /\ rec' = reading
    /\ reading' = NoRecord
    /\ UNCHANGED <<live, fig, after, pend, marked, mark, known, markId, ver, rev, rr, tidying, tseen, tpre, tall, tmoved>>

Next ==
    \/ \E f \in Figures : Appear(f) \/ Grow(f) \/ TidyMove(f)
    \/ ResetRead \/ ResetRetry \/ ResetWrite
    \/ TidyStart \/ TidyEnd
    \/ CountRead \/ CountWrite

Spec == Init /\ [][Next]_vars

\* where takes the record only at the table's revision and the stats record's stamp
Taken == rec.rev = rev /\ (Broken = "nostamp" \/ rec.stamp = ver)

\* with no tidy in flight, every shown figure is the cards since the mark still on the row
ShownIsSinceMark ==
    ~tidying => \A f \in live : Shown(f) = after[f]

\* a record where takes shows exactly the figures
RecordShowsShown ==
    Taken => \A f \in live : rec.vals[f] = Shown(f)

\* a figure the mark does not know reads as before
UnknownReadsAsBefore ==
    \A f \in live \ known : Shown(f) = fig[f]
=============================================================================
