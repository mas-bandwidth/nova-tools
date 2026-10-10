---------------------------- MODULE DiskWatermark ----------------------------
\* The disk watermark (internal/sprint/disk.go, docs/SPEC-SPRINT.md section 8).
\* One tick reads a volume's use and whether that reading is fresh, then:
\*   a fresh reading at or over the warn line opens the one judgment, and
\*   raises it again only once each re-raise interval while it holds;
\*   a fresh reading at or over the stop line quiets the machine, and no new
\*   lane starts while it is quiet;
\*   a reading under the warn line, or one that is not fresh, closes the
\*   judgment and lifts the quiet.
\* The lane decision uses the reading of this tick, as the deal does
\* (withDiskQuiets) before the plan writes the quiet.
\*
\* Reversed witnesses, Broken:
\*   "lane"   a quiet machine is still dealt a lane (NoLaneWhileQuiet).
\*   "stale"  a reading that is not over the warn line leaves the judgment
\*            open (JudgmentIffOver).
EXTENDS Naturals

CONSTANTS Warn, Stop, Reraise, MaxClock, Uses, Broken

VARIABLES use, fresh, open, quiet, lanes, clock, raised, raises, dealtWhileQuiet
vars == <<use, fresh, open, quiet, lanes, clock, raised, raises, dealtWhileQuiet>>

ASSUME Warn \in 1..100 /\ Stop \in 1..100 /\ Warn < Stop /\ Reraise \in 1..MaxClock /\ MaxClock \in 1..6 /\ Uses \subseteq 0..100 /\ Uses # {}

readingOver(u, f) == f /\ u >= Warn
readingStop(u, f) == f /\ u >= Stop

\* the quiet this tick, and the judgment. A stale witness keeps both.
quietOf(u, f) ==
    IF "stale" \in Broken THEN readingStop(u, f) \/ quiet ELSE readingStop(u, f)

openOf(u, f) ==
    IF readingOver(u, f) THEN TRUE
    ELSE IF "stale" \in Broken THEN open ELSE FALSE

\* a raise is due when the judgment is closed, or the re-raise interval has passed.
raiseDue == ~(open /\ clock < raised + Reraise)

TypeOK ==
    /\ use \in Uses \cup {0}
    /\ fresh \in BOOLEAN
    /\ open \in BOOLEAN
    /\ quiet \in BOOLEAN
    /\ lanes \in 0..3
    /\ clock \in 0..MaxClock
    /\ raised \in 0..MaxClock
    /\ raises \in 0..MaxClock
    /\ dealtWhileQuiet \in BOOLEAN

Init ==
    /\ use = 0
    /\ fresh = FALSE
    /\ open = FALSE
    /\ quiet = FALSE
    /\ lanes = 0
    /\ clock = 0
    /\ raised = 0
    /\ raises = 0
    /\ dealtWhileQuiet = FALSE

\* one tick: a reading arrives, the judgment and the quiet follow it, and a
\* lane starts only while the machine is not quiet.
Tick ==
    /\ clock < MaxClock
    /\ \E u \in Uses, f \in BOOLEAN:
        LET q == quietOf(u, f)
            deal == lanes < 3 /\ (~q \/ "lane" \in Broken)
            bump == readingOver(u, f) /\ raiseDue
        IN /\ use' = u
           /\ fresh' = f
           /\ quiet' = q
           /\ open' = openOf(u, f)
           /\ raised' = IF bump THEN clock ELSE raised
           /\ raises' = IF bump THEN raises + 1 ELSE raises
           /\ lanes' = IF deal THEN lanes + 1 ELSE lanes
           /\ dealtWhileQuiet' = (dealtWhileQuiet \/ (deal /\ q))
    /\ clock' = clock + 1

Next == Tick

Spec == Init /\ [][Next]_vars

\* the judgment is open exactly while the latest reading is fresh and at or
\* over the warn line.
JudgmentIffOver == open = (fresh /\ use >= Warn)

\* the quiet holds exactly while the latest reading is fresh and at or over
\* the stop line. The stale witness keeps a quiet, so this is not its failure.
QuietIffStop == "stale" \in Broken \/ quiet = (fresh /\ use >= Stop)

\* no lane was started on a tick whose reading held the machine quiet.
NoLaneWhileQuiet == dealtWhileQuiet = FALSE

\* while the volume stays over, the judgment is raised again at most once
\* each re-raise interval: the first raise, then one more per interval.
RaisesBounded == raises <= 1 + (clock \div Reraise)
=============================================================================
