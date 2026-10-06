----------------------------- MODULE LaneCheck -----------------------------
\* A judgment checks the lane before it rises (docs/SPEC-SPRINT.md section 8;
\* internal/sprint/friend_deadline.go LaneChecked, LiveLane, Suppressed.Counted;
\* internal/sprint/store/tick.go tickRun.laneChecked and the heartbeat's count).
\*
\* One friend's card, dealt to her row. Its clock (ran, running time from its take)
\* advances; her beat names it running, or stops; the coordinator acknowledges an open
\* judgment. Each tick: once the card is past the finish window (Window) and no
\* judgment about it is open, the tick would raise one (a lateness, a stall or
\* finishes none). The lane check keeps it quiet while the lane is live: her beat
\* names the card and it has run less than its cap (Cap). A quiet the tick before
\* also kept is the same judgment still kept from rising and is not counted again;
\* a tick that keeps nothing quiet ends the span.
\*
\* Invariants:
\*   NoRiseOverLiveLane: no judgment rises while her lane is live.
\*   CountedOncePerSpan: the suppressed count is the number of spans of ticks that
\*     kept the judgment quiet (ghost spans), never one per tick.
\*   NoMissedRise: a tick that finds the card past the window, no judgment open and
\*     the lane not live raises it (past the cap or with no beat it rises as before).
\*
\* Broken values:
\*   "none"       the design
\*   "nocheck"    the tick raises without checking the lane (breaks NoRiseOverLiveLane)
\*   "everytick"  each tick's quiet is counted, the tick before not read (breaks CountedOncePerSpan)
\*   "nocap"      a beat naming the card keeps it quiet past its cap (breaks NoMissedRise)

EXTENDS Naturals

CONSTANTS Window, Cap, MaxTime, Broken

VARIABLES ran, beat, open, quiet, count, spans, roseLive, missed

vars == <<ran, beat, open, quiet, count, spans, roseLive, missed>>

Live == beat /\ (ran < Cap \/ Broken = "nocap")
TrueLive == beat /\ ran < Cap
Due == ran >= Window /\ ~open

TypeOK ==
    /\ ran \in 0..MaxTime
    /\ beat \in BOOLEAN
    /\ open \in BOOLEAN
    /\ quiet \in BOOLEAN
    /\ count \in Nat
    /\ spans \in Nat
    /\ roseLive \in BOOLEAN
    /\ missed \in BOOLEAN

Init ==
    /\ ran = 0
    /\ beat = TRUE
    /\ open = FALSE
    /\ quiet = FALSE
    /\ count = 0
    /\ spans = 0
    /\ roseLive = FALSE
    /\ missed = FALSE

\* Running time passes.
Advance ==
    /\ ran < MaxTime
    /\ ran' = ran + 1
    /\ UNCHANGED <<beat, open, quiet, count, spans, roseLive, missed>>

\* Her beat stops naming the card (the daemon stopped, or the lane let it go), or
\* names it again.
BeatFlip ==
    /\ beat' = ~beat
    /\ UNCHANGED <<ran, open, quiet, count, spans, roseLive, missed>>

\* The coordinator answers an open judgment.
Ack ==
    /\ open
    /\ open' = FALSE
    /\ UNCHANGED <<ran, beat, quiet, count, spans, roseLive, missed>>

\* A tick: the lane check before the judgment rises, and the heartbeat's count.
Tick ==
    IF Due /\ Live /\ Broken # "nocheck"
    THEN /\ quiet' = TRUE
         /\ count' = IF quiet /\ Broken # "everytick" THEN count ELSE count + 1
         /\ spans' = IF quiet THEN spans ELSE spans + 1
         /\ missed' = (missed \/ ~TrueLive)
         /\ UNCHANGED <<ran, beat, open, roseLive>>
    ELSE IF Due
    THEN /\ open' = TRUE
         /\ roseLive' = (roseLive \/ TrueLive)
         /\ quiet' = FALSE
         /\ UNCHANGED <<ran, beat, count, spans, missed>>
    ELSE /\ quiet' = FALSE
         /\ UNCHANGED <<ran, beat, open, count, spans, roseLive, missed>>

Next == Advance \/ BeatFlip \/ Ack \/ Tick

Spec == Init /\ [][Next]_vars

\* The bound of the model: spans stay finite on a small instance.
Bound == spans <= 3 /\ count <= 6

NoRiseOverLiveLane == ~roseLive
CountedOncePerSpan == count = spans
NoMissedRise == ~missed

=============================================================================
