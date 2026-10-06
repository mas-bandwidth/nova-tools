--------------------------- MODULE CoordinatorPass ---------------------------
\* The coordinator's pass (docs/SPEC-SPRINT.md section 8, "The coordinator's pass";
\* internal/sprint/coordinator_pass.go, TickCoordinatorPass). The owner, 2026-10-05:
\* "everything I described above needs to be mechanical, so you have a reminder to do it
\* (notification) coming from the machine every 10 minutes. Otherwise, you will eventually
\* drift and forget."
\*
\* One condition on one subject (a friend's session deaf, a friend holding working cards
\* that finishes none, judgments late on the coordinator: each is this machine), and its
\* judgment:
\*   holds   the condition as the world has it now: an outside event, it comes and goes
\*           at any time (Flip).
\*   seen    the condition as the last tick saw it.
\*   open    the judgment is open (the tick's notify wrote it, and has not closed it).
\*   acked   the coordinator acknowledged it (ack): kept on the subject as an
\*           acknowledgement, never raised again, until the condition clears.
\*   first   the running clock it was written at (its At); -1 while none.
\*   again   how many times it was raised again (its Before): the k-th raise again is
\*           due k times Every after first.
\*   clk     running time, one step a tick (PassEvery is Every steps).
\*   written the judgments written this episode (the notes of the type on the subject).
\*   pushes  the pushes to the coordinator this episode: the first raise, then each
\*           NRaisedAgain note.
\*
\* With Kind = "behind" the condition is not an outside event: it is read off one judgment
\* late on the coordinator, and the overdue part's line is the first reminder of it.
\*   late       the judgment is open past its deadline (an outside event: the deadline
\*              passes, or the coordinator answers it).
\*   markAt     the running clock the overdue part named it at (its NOverdue hold), -1
\*              while unmarked; the line is written the first tick that finds it late.
\*   lastPush   the running clock of the last push about it: its overdue line, or the
\*              pass's raise or raise again; -1 while none.
\*   tickPushes the pushes about it the last tick wrote.
\* The condition holds once Every of running time has run since the overdue line
\* (behindConds): the line at the deadline, then the pass Every on, then every Every.
\*
\* With Kind = "starved" the condition is a friend up whose row has been empty for Window
\* of running time while cards she could do wait elsewhere (starvedConds; the owner,
\* 2026-10-05: "How is it that you missed Zhi having zero cards? Seems bad.").
\*   holds      her row is empty while she is up (an outside event).
\*   waits      cards she could do wait in the pool or unstarted on another friend's row
\*              (an outside event).
\*   emptyAt    the fleet property PropFriendEmptySince: the running clock of the tick that
\*              first saw her row empty, -1 while it is not; that tick only writes it.
\*   emptyFrom  the same as the world has it (a ghost): the empty row's true start.
\* The condition holds once Window has run since emptyAt, while cards wait.
\*
\* The design, Broken = "none":
\*   Tick  the clock steps; when the condition holds, a judgment is written if none is
\*         open or acknowledged, else the open one is raised again in place (again counts
\*         the raises due, one push) once its next raise is due; when it does not hold,
\*         the open judgment (or the acknowledgement) is closed and the episode ends.
\*   Ack   the coordinator acknowledges an open judgment.
\* Reversed witnesses, each broken by one property:
\*   "noreraise"  the judgment is raised once and never again (the tick's judgments before
\*                the pass): PushedEveryWindow.
\*   "reopen"     raising again writes a second judgment instead of rewriting the open one:
\*                OneJudgmentAnEpisode.
\*   "noclose"    the judgment stays open when the condition clears: ClosedWhenCleared.
\*   "doublepush" (Kind = "behind") the pass counts a late judgment from its deadline, not
\*                from its overdue line: the line and the pass push in one tick:
\*                OnePushATick.
\*   "nowindow"   (Kind = "starved") she is told the tick her row is first seen empty:
\*                TellsOnlyAfterWindow.
\*   "noreset"    (Kind = "starved") the empty row's start is not cleared when her row
\*                holds cards again, so a later empty row is told at once:
\*                TellsOnlyAfterWindow.
EXTENDS Integers

CONSTANTS Every, MaxClock, Broken, Kind, Window

VARIABLES holds, seen, open, acked, first, again, clk, written, pushes,
          late, markAt, lastPush, tickPushes, waits, emptyAt, emptyFrom
vars == <<holds, seen, open, acked, first, again, clk, written, pushes,
          late, markAt, lastPush, tickPushes, waits, emptyAt, emptyFrom>>

TypeOK ==
  /\ holds \in BOOLEAN
  /\ seen \in BOOLEAN
  /\ open \in BOOLEAN
  /\ acked \in BOOLEAN
  /\ first \in (0..MaxClock) \cup {-1}
  /\ again \in 0..MaxClock
  /\ clk \in 0..MaxClock
  /\ written \in 0..MaxClock
  /\ pushes \in 0..MaxClock
  /\ late \in BOOLEAN
  /\ markAt \in (0..MaxClock) \cup {-1}
  /\ lastPush \in (0..MaxClock) \cup {-1}
  /\ tickPushes \in 0..2
  /\ waits \in BOOLEAN
  /\ emptyAt \in (0..MaxClock) \cup {-1}
  /\ emptyFrom \in (0..MaxClock) \cup {-1}

Init ==
  /\ holds = FALSE /\ seen = FALSE /\ open = FALSE /\ acked = FALSE
  /\ first = -1 /\ again = 0 /\ clk = 0 /\ written = 0 /\ pushes = 0
  /\ late = FALSE /\ markAt = -1 /\ lastPush = -1 /\ tickPushes = 0
  /\ waits = FALSE /\ emptyAt = -1 /\ emptyFrom = -1

\* The world: the friend's session answers or not, her cards finish or not, the
\* coordinator answers the late judgments or not, her row empties or fills, cards she
\* could do come to wait elsewhere or stop waiting.
Flip ==
  /\ CASE Kind = "behind" -> late' = ~late /\ UNCHANGED <<holds, waits>>
       [] Kind = "starved" -> \/ holds' = ~holds /\ UNCHANGED <<late, waits>>
                              \/ waits' = ~waits /\ UNCHANGED <<late, holds>>
       [] OTHER -> holds' = ~holds /\ UNCHANGED <<late, waits>>
  /\ UNCHANGED <<seen, open, acked, first, again, clk, written, pushes, markAt, lastPush, tickPushes, emptyAt, emptyFrom>>

\* The coordinator acknowledges the open judgment.
Ack ==
  /\ open /\ ~acked
  /\ open' = FALSE /\ acked' = TRUE
  /\ UNCHANGED <<holds, seen, first, again, clk, written, pushes, late, markAt, lastPush, tickPushes, waits, emptyAt, emptyFrom>>

\* The condition the tick's pass reads. Behind: the late judgment's overdue line,
\* written by an earlier tick (the pass reads the holds before this tick's), is Every
\* old.
Cond ==
  CASE Kind = "behind" ->
         late /\ IF Broken = "doublepush" THEN TRUE ELSE markAt # -1 /\ clk + 1 - markAt >= Every
    [] Kind = "starved" ->
         holds /\ waits /\ IF Broken = "nowindow" THEN TRUE ELSE emptyAt # -1 /\ clk + 1 - emptyAt >= Window
    [] OTHER -> holds

\* This tick's overdue line about the late judgment: the first tick that finds it late.
Line == Kind = "behind" /\ late /\ markAt = -1

\* This tick's pass pushes: it raises the judgment, or raises it again.
PassPush ==
  /\ Cond
  /\ \/ ~open /\ ~acked
     \/ open /\ (clk + 1 - first) \div Every > again /\ Broken # "noreraise"

\* One tick: the overdue part's line, then the pass (TickCoordinatorPass: notify, then
\* reraise).
Tick ==
  /\ clk < MaxClock
  /\ clk' = clk + 1
  /\ seen' = Cond
  /\ UNCHANGED <<holds, late, waits>>
  \* the empty row's start: written the tick that first sees it, cleared when it fills
  /\ emptyAt' = IF Kind # "starved" THEN -1
                ELSE IF holds THEN (IF emptyAt = -1 THEN clk + 1 ELSE emptyAt)
                ELSE IF Broken = "noreset" THEN emptyAt ELSE -1
  /\ emptyFrom' = IF Kind = "starved" /\ holds THEN (IF emptyFrom = -1 THEN clk + 1 ELSE emptyFrom) ELSE -1
  /\ markAt' = IF Kind = "behind" /\ late THEN (IF markAt = -1 THEN clk + 1 ELSE markAt) ELSE -1
  /\ tickPushes' = (IF Line THEN 1 ELSE 0) + (IF PassPush THEN 1 ELSE 0)
  /\ lastPush' = IF ~(Kind = "behind" /\ late) THEN -1
                 ELSE IF tickPushes' > 0 THEN clk + 1 ELSE lastPush
  /\ IF Cond
       THEN IF ~open /\ ~acked
              THEN \* raised: the judgment, once an episode
                   /\ open' = TRUE /\ first' = clk + 1 /\ again' = 0
                   /\ written' = written + 1 /\ pushes' = pushes + 1
                   /\ UNCHANGED acked
            ELSE IF open /\ (clk + 1 - first) \div Every > again /\ Broken # "noreraise"
              THEN \* raised again: in place, every raise due counted, one push
                   /\ again' = (clk + 1 - first) \div Every /\ pushes' = pushes + 1
                   /\ written' = IF Broken = "reopen" THEN written + 1 ELSE written
                   /\ UNCHANGED <<open, acked, first>>
            ELSE UNCHANGED <<open, acked, first, again, written, pushes>>
       ELSE IF Broken = "noclose"
              THEN UNCHANGED <<open, acked, first, again, written, pushes>>
            ELSE \* closed: the episode ends
                 /\ open' = FALSE /\ acked' = FALSE /\ first' = -1 /\ again' = 0
                 /\ written' = 0 /\ pushes' = 0

Next == Flip \/ Ack \/ Tick

Spec == Init /\ [][Next]_vars

\* One judgment an episode: raising again rewrites it, never writes a second.
OneJudgmentAnEpisode == written <= 1

\* An open judgment the coordinator has not acknowledged is never left unraised for a
\* whole window: each tick that finds its next raise due raises it.
PushedEveryWindow == (open /\ ~acked) => clk - first < (again + 1) * Every

\* A judgment is open only while the last tick saw its condition hold.
ClosedWhenCleared == (open \/ acked) => seen

\* The last tick saw the condition hold: its judgment is open, or acknowledged.
RaisedWhileItHolds == (seen /\ clk > 0) => (open \/ acked)

\* Every push of an episode is of its one judgment: the first raise and one a raise again.
PushedOnlyWhileRaised == pushes > 0 => (written >= 1 /\ pushes <= again + 1)

\* Behind: one late judgment is pushed at most once a tick, by its overdue line or by
\* the pass, never both.
OnePushATick == tickPushes <= 1

\* Behind: a late judgment the coordinator has not acknowledged is never a whole window
\* without a push once its overdue line is written: the line, then the pass every Every.
LateRemindedEveryWindow ==
  (Kind = "behind" /\ late /\ markAt # -1 /\ ~acked) => clk - lastPush <= Every

\* Starved: a friend is told only once her row has truly been empty for Window, never on
\* a row that just emptied, nor on an earlier empty row's start.
TellsOnlyAfterWindow ==
  (Kind = "starved" /\ (open \/ acked)) => (emptyFrom # -1 /\ first - emptyFrom >= Window)

=============================================================================
