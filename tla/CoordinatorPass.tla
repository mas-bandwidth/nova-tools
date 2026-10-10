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
\* The pass escalates it (internal/sprint/stops.go, BehindLevel; the owner, 2026-10-10: no
\* silent waits): every EscEvery of running time since the overdue line is a new level, and
\* the level is part of the condition, so a new level is a new episode: the judgment of the
\* level before, or the acknowledgement of it, closes and a new judgment is raised.
\*   lvl        the level the open (or acknowledged) judgment was raised at; 0 while none.
\*
\* With Kind = "stop" (internal/sprint/stops.go: an automatic stop the machine made, a fleet
\* member down that the coordinator never held, a hard pin waiting on a friend who is not up;
\* the owner, 2026-10-10: "i still dislike these silent stops/failures", and the night
\* before, "it should raise it to you as a thing to do, but not do it automatically") holds
\* is the stop in force. The machine makes it, and the coordinator's undo verb or the stop's
\* own end clears it, between two ticks (Flip); the pass reads it at the next tick.
\*   stoppedAt  the running clock the stop was made at; -1 while none holds.
\* In the code a stop's raise again is pushed in the tick's one digest of the stops
\* (stopsDigest, NStopsDigest), due every PassEvery while any stop has gone that long without a
\* push: for the one stop modelled here that is a push every window, as Tick writes it.
\*
\* With Kind = "empty" (an up friend has an empty row while cards wait) holds is the
\* conjunction the pass reads each tick: she is up and not held, her row is empty, and
\* cards she could do wait elsewhere. The judgment waits EmptyRowAfter (Every, in the
\* code both ten minutes) of that conjunction, kept by the empty-row clock:
\*   watchAt the running clock the clock (NFriendRowEmpty, an acknowledgement) was
\*           written at, the first tick that finds the conjunction; -1 while none. A
\*           tick that finds it gone closes the clock, so time down, held or busy does
\*           not count.
\*   streak  how many ticks in a row have found the conjunction (a ghost: the code
\*           keeps no such count).
\* With Kind = "pin" (a named pin dealt away from its friend) holds is the card ready or
\* working off her row, and the deal writes the judgment on the unit that places it
\* there (Deal); the pass keeps that one note and raises it again in place:
\*   dealt   the open judgment is the deal's, not yet read by a pass.
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
\*   "noreset"    (Kind = "empty") the empty-row clock is kept when the conjunction is
\*                gone, so time down counts and she is judged on her first tick back:
\*                EmptyAWholeWindow.
\*   "pinrekey"   (Kind = "pin") the pass does not know the deal's judgment as its own
\*                (the text it would write differs from the deal's) and writes a
\*                second: OneJudgmentAnEpisode.
\*   "silentstop" (Kind = "stop") the stop's type is not one the pass reads (the night of
\*                2026-10-09: two machine rows down since they never beat, and a hard pin
\*                to a friend who was down, each found by a person reading the
\*                dashboard): StopSignalled.
\*   "ackforever" (Kind = "behind") the condition carries no level, so one quieting (a
\*                wait to a far review time) quiets the judgments late on the coordinator
\*                for as long as any is late (2026-10-09: 111 judgments waiting, the oldest
\*                51 hours): EscalatedPastAck.
EXTENDS Integers

CONSTANTS Every, MaxClock, Broken, Kind

VARIABLES holds, seen, open, acked, first, again, clk, written, pushes,
          late, markAt, lastPush, tickPushes, watchAt, streak, dealt, stoppedAt, lvl
vars == <<holds, seen, open, acked, first, again, clk, written, pushes,
          late, markAt, lastPush, tickPushes, watchAt, streak, dealt, stoppedAt, lvl>>

\* The escalation step: three windows (the code: BehindEscalateEvery, three PassEvery).
EscEvery == 3 * Every

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
  /\ streak \in 0..MaxClock
  /\ dealt \in BOOLEAN
  /\ stoppedAt \in (0..MaxClock) \cup {-1}
  /\ lvl \in 0..MaxClock

Init ==
  /\ holds = FALSE /\ seen = FALSE /\ open = FALSE /\ acked = FALSE
  /\ first = -1 /\ again = 0 /\ clk = 0 /\ written = 0 /\ pushes = 0
  /\ late = FALSE /\ markAt = -1 /\ lastPush = -1 /\ tickPushes = 0
  /\ watchAt = -1 /\ streak = 0 /\ dealt = FALSE /\ stoppedAt = -1 /\ lvl = 0

\* The world: the friend's session answers or not, her cards finish or not, the
\* coordinator answers the late judgments or not, the friend comes up or goes down, her
\* row fills or empties, the cards she could do come and go, the pinned card moves; for a
\* stop, the machine makes it, and the coordinator's undo (or its own end) clears it.
Flip ==
  /\ IF Kind = "behind"
       THEN late' = ~late /\ UNCHANGED holds
       ELSE holds' = ~holds /\ UNCHANGED late
  /\ stoppedAt' = IF Kind = "stop" /\ ~holds THEN clk ELSE -1
  /\ UNCHANGED <<seen, open, acked, first, again, clk, written, pushes, markAt, lastPush, tickPushes,
                 watchAt, streak, dealt, lvl>>

\* Pin: the deal places the pinned card on another row and writes the judgment on that
\* unit (friendDeal, pinIgnoredNote). The deal is a part of the tick, which leaves the
\* condition holding; the next pass reads the deal's note.
Deal ==
  /\ Kind = "pin"
  /\ ~holds /\ ~open /\ ~acked
  /\ holds' = TRUE /\ seen' = TRUE
  /\ open' = TRUE /\ first' = clk /\ again' = 0 /\ dealt' = TRUE
  /\ written' = written + 1 /\ pushes' = pushes + 1
  /\ UNCHANGED <<acked, clk, late, markAt, lastPush, tickPushes, watchAt, streak, stoppedAt, lvl>>

\* The coordinator acknowledges the open judgment.
Ack ==
  /\ open /\ ~acked
  /\ open' = FALSE /\ acked' = TRUE
  /\ UNCHANGED <<holds, seen, first, again, clk, written, pushes, late, markAt, lastPush, tickPushes,
                 watchAt, streak, dealt, stoppedAt, lvl>>

\* The condition the tick's pass reads. Behind: the late judgment's overdue line,
\* written by an earlier tick (the pass reads the holds before this tick's), is Every
\* old. Empty: the empty-row clock, written by an earlier tick, is Every old (or the
\* judgment is already open: emptyConds keeps it while the conjunction holds). Stop: the
\* stop is in force, unless the pass does not read its type (silentstop).
Cond ==
  CASE Kind = "behind" ->
         late /\ IF Broken = "doublepush" THEN TRUE ELSE markAt # -1 /\ clk + 1 - markAt >= Every
    [] Kind = "empty" ->
         holds /\ (open \/ acked \/ (watchAt # -1 /\ clk + 1 - watchAt >= Every))
    [] Kind = "stop" ->
         holds /\ Broken # "silentstop"
    [] OTHER -> holds

\* This tick's overdue line about the late judgment: the first tick that finds it late.
Line == Kind = "behind" /\ late /\ markAt = -1

\* The overdue mark after this tick, and the escalation level this tick's pass reads off it
\* (behind only: every other kind is at level 0).
NextMark == IF Kind = "behind" /\ late THEN (IF markAt = -1 THEN clk + 1 ELSE markAt) ELSE -1
NextLevel ==
  IF Kind = "behind" /\ NextMark # -1 THEN (clk + 1 - NextMark) \div EscEvery ELSE 0

\* The level of the judgments late on the coordinator now: what a judgment of the condition
\* raised now would carry.
Level ==
  IF Kind = "behind" /\ markAt # -1 THEN (clk - markAt) \div EscEvery ELSE 0

\* This tick's pass meets a judgment (or an acknowledgement) of a level below the
\* condition's: a new episode. With ackforever the condition's key carries no level, so the
\* pass never sees one.
Escalate == Cond /\ (open \/ acked) /\ lvl # NextLevel /\ Broken # "ackforever"

\* This tick's pass pushes: it raises the judgment, raises it at a new level, or raises it
\* again.
PassPush ==
  /\ Cond
  /\ \/ ~open /\ ~acked
     \/ Escalate
     \/ open /\ (clk + 1 - first) \div Every > again /\ Broken # "noreraise"

\* One tick: the overdue part's line, then the pass (TickCoordinatorPass: notify, then
\* reraise).
Tick ==
  /\ clk < MaxClock
  /\ clk' = clk + 1
  /\ seen' = Cond
  /\ UNCHANGED <<holds, late, stoppedAt>>
  /\ markAt' = NextMark
  /\ watchAt' = IF Kind # "empty" THEN -1
               ELSE IF holds THEN (IF watchAt = -1 THEN clk + 1 ELSE watchAt)
               ELSE IF Broken = "noreset" THEN watchAt ELSE -1
  /\ streak' = IF Kind = "empty" /\ holds THEN streak + 1 ELSE 0
  /\ dealt' = FALSE
  /\ tickPushes' = (IF Line THEN 1 ELSE 0) + (IF PassPush THEN 1 ELSE 0)
  /\ lastPush' = IF ~(Kind = "behind" /\ late) THEN -1
                 ELSE IF tickPushes' > 0 THEN clk + 1 ELSE lastPush
  /\ IF Cond
       THEN IF ~open /\ ~acked
              THEN \* raised: the judgment, once an episode
                   /\ open' = TRUE /\ first' = clk + 1 /\ again' = 0 /\ lvl' = NextLevel
                   /\ written' = written + 1 /\ pushes' = pushes + 1
                   /\ UNCHANGED acked
            ELSE IF Escalate
              THEN \* a new level: the judgment (or acknowledgement) of the level before
                   \* closes and the new level's is raised, a new episode
                   /\ open' = TRUE /\ acked' = FALSE /\ first' = clk + 1 /\ again' = 0
                   /\ lvl' = NextLevel /\ written' = 1 /\ pushes' = 1
            ELSE IF open /\ (clk + 1 - first) \div Every > again /\ Broken # "noreraise"
              THEN \* raised again: in place, every raise due counted, one push
                   /\ again' = (clk + 1 - first) \div Every /\ pushes' = pushes + 1
                   /\ written' = IF Broken = "reopen" THEN written + 1 ELSE written
                   /\ UNCHANGED <<open, acked, first, lvl>>
            ELSE IF open /\ dealt /\ Broken = "pinrekey"
              THEN \* the deal's note not known as the pass's: a second judgment
                   /\ written' = written + 1
                   /\ UNCHANGED <<open, acked, first, again, pushes, lvl>>
            ELSE UNCHANGED <<open, acked, first, again, written, pushes, lvl>>
       ELSE IF Broken = "noclose"
              THEN UNCHANGED <<open, acked, first, again, written, pushes, lvl>>
            ELSE \* closed: the episode ends
                 /\ open' = FALSE /\ acked' = FALSE /\ first' = -1 /\ again' = 0
                 /\ written' = 0 /\ pushes' = 0 /\ lvl' = 0

Next == Flip \/ Ack \/ Deal \/ Tick

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

\* Empty: a friend is judged only after a whole window of ticks has found her up and not
\* held, her row empty and cards she could do waiting; time down, held or busy restarts it.
EmptyAWholeWindow == (Kind = "empty" /\ (open \/ acked)) => streak > Every

\* Stop: every automatic stop has a raised signal while it holds. Once a tick has run since
\* the machine made the stop, its judgment is open (pushed, and pushed again every Every by
\* PushedEveryWindow) or the coordinator acknowledged it; it is never only in a table cell.
StopSignalled ==
  (Kind = "stop" /\ holds /\ stoppedAt # -1 /\ clk > stoppedAt) => (open \/ acked)

\* Behind: the judgment (or acknowledgement) of the judgments late on the coordinator is of
\* the level they have reached: an acknowledgement never outlives its level, so a wait that
\* grows is pushed again however it was quieted.
EscalatedPastAck ==
  (Kind = "behind" /\ (open \/ acked)) => lvl = Level

=============================================================================
