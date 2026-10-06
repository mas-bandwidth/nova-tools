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
\* With Kind = "empty" (an up friend has an empty row while cards wait, emptyConds) the
\* condition is holds read through a clock: the judgment is told only once holds has been
\* seen by every tick for Delay of running time (EmptyRowAfter).
\*   watchAt    the running clock of the acknowledgement NFriendRowEmpty, the empty row's
\*              clock: written by the first tick that sees holds, closed by the first tick
\*              that does not; -1 while none. The pass reads the one an earlier tick wrote.
\*   runStart   (ghost) the running clock from which every tick has seen holds; -1 while
\*              the last tick did not. Time down, held, busy or with nothing waiting does
\*              not count.
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
\*   "nowait"     (Kind = "empty") the pass tells an empty row the first tick it sees it,
\*                without its clock: ToldOnlyAfterDelay.
\*   "noreset"    (Kind = "empty") the clock is kept when the row stops being empty, so
\*                time down or busy counts toward the next episode: ToldOnlyAfterDelay.
\*   "nowatch"    (Kind = "empty") the clock is never written, so an empty row is never
\*                told (2026-10-05: a friend sat at zero for an hour, nothing told the
\*                coordinator): EmptyToldWhenDue.
EXTENDS Integers

CONSTANTS Every, MaxClock, Broken, Kind, Delay

VARIABLES holds, seen, open, acked, first, again, clk, written, pushes,
          late, markAt, lastPush, tickPushes, watchAt, runStart
vars == <<holds, seen, open, acked, first, again, clk, written, pushes,
          late, markAt, lastPush, tickPushes, watchAt, runStart>>

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
  /\ watchAt \in (0..MaxClock) \cup {-1}
  /\ runStart \in (0..MaxClock) \cup {-1}

Init ==
  /\ holds = FALSE /\ seen = FALSE /\ open = FALSE /\ acked = FALSE
  /\ first = -1 /\ again = 0 /\ clk = 0 /\ written = 0 /\ pushes = 0
  /\ late = FALSE /\ markAt = -1 /\ lastPush = -1 /\ tickPushes = 0
  /\ watchAt = -1 /\ runStart = -1

\* The world: the friend's session answers or not, her cards finish or not, the
\* coordinator answers the late judgments or not, her row empties or fills, she goes up
\* or down, cards she could do come and go.
Flip ==
  /\ IF Kind = "behind"
       THEN late' = ~late /\ UNCHANGED holds
       ELSE holds' = ~holds /\ UNCHANGED late
  /\ UNCHANGED <<seen, open, acked, first, again, clk, written, pushes, markAt, lastPush, tickPushes,
                 watchAt, runStart>>

\* The coordinator acknowledges the open judgment.
Ack ==
  /\ open /\ ~acked
  /\ open' = FALSE /\ acked' = TRUE
  /\ UNCHANGED <<holds, seen, first, again, clk, written, pushes, late, markAt, lastPush, tickPushes,
                 watchAt, runStart>>

\* The condition the tick's pass reads. Behind: the late judgment's overdue line,
\* written by an earlier tick (the pass reads the holds before this tick's), is Every
\* old. Empty: the row's clock, written by an earlier tick, is Delay old.
Cond ==
  CASE Kind = "behind" ->
         late /\ IF Broken = "doublepush" THEN TRUE ELSE markAt # -1 /\ clk + 1 - markAt >= Every
    [] Kind = "empty" ->
         holds /\ IF Broken = "nowait" THEN TRUE ELSE watchAt # -1 /\ clk + 1 - watchAt >= Delay
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
  /\ UNCHANGED <<holds, late>>
  /\ markAt' = IF Kind = "behind" /\ late THEN (IF markAt = -1 THEN clk + 1 ELSE markAt) ELSE -1
  /\ tickPushes' = (IF Line THEN 1 ELSE 0) + (IF PassPush THEN 1 ELSE 0)
  /\ watchAt' = IF Kind # "empty" \/ Broken = "nowatch" THEN -1
                ELSE IF holds THEN (IF watchAt = -1 THEN clk + 1 ELSE watchAt)
                ELSE IF Broken = "noreset" THEN watchAt ELSE -1
  /\ runStart' = IF Kind = "empty" /\ holds THEN (IF runStart = -1 THEN clk + 1 ELSE runStart) ELSE -1
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

\* Empty: an empty row is told only once every tick has seen it for Delay: never the first
\* tick, and time down, held or busy does not count.
ToldOnlyAfterDelay == (Kind = "empty" /\ open) => first - runStart >= Delay

\* Empty: once every tick has seen the row empty for Delay, its judgment is open or
\* acknowledged: an idle up friend while work waits is always told.
EmptyToldWhenDue ==
  (Kind = "empty" /\ runStart # -1 /\ clk - runStart >= Delay) => (open \/ acked)

=============================================================================
