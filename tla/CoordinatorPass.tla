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
\* With Kind = "empty" (emptyConds) holds is an up friend's row empty while cards she
\* could do wait elsewhere, and the condition is that conjunction held for After of
\* running time:
\*   watchAt the running clock of the empty-row clock (the acknowledgement
\*           NFriendRowEmpty), written the first tick that sees holds and closed the
\*           first tick that does not; -1 while none. The pass reads the clock an
\*           earlier tick wrote.
\*   since   (ghost) the running clock of the first tick of the current run of ticks
\*           that saw holds; -1 while none. Time down, held or busy is not in it.
\*
\* With Kind = "pin" (pinConds, friendDeal) holds is a named pin's card sitting ready or
\* working off its friend's row. Only the tick's deal puts it there; Flip only takes it
\* away (finished, or taken back to her row). The deal is a part of the tick before the
\* overdue part, so the pass reads the world after the deal:
\*   path    how the card came off her row: "friend" (the friends' deal, whose unit
\*           carries the judgment, pinIgnoredNote) or "fleet" (a machine's deal, which
\*           writes none: the pass raises it); "none" while it is on her row.
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
\*   "noclock"    (Kind = "empty") the friend is told the first tick her row is seen
\*                empty, not After later: EmptyToldOnlyAfterWindow.
\*   "keepclock"  (Kind = "empty") the clock is not closed when the conjunction breaks,
\*                so time down or busy counts toward the ten minutes:
\*                EmptyToldOnlyAfterWindow.
\*   "passbefore" (Kind = "pin") the pass reads the world before the tick's deal, so a
\*                pin placed off her row is silent until the next tick: PinNeverSilent.
\*   "nopass"     (Kind = "pin") only the friends' deal tells it, the pass does not: a
\*                pin a machine took is never told: PinNeverSilent.
EXTENDS Integers

CONSTANTS Every, MaxClock, Broken, Kind, After

VARIABLES holds, seen, open, acked, first, again, clk, written, pushes,
          late, markAt, lastPush, tickPushes, watchAt, since, path
vars == <<holds, seen, open, acked, first, again, clk, written, pushes,
          late, markAt, lastPush, tickPushes, watchAt, since, path>>

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
  /\ since \in (0..MaxClock) \cup {-1}
  /\ path \in {"none", "friend", "fleet"}

Init ==
  /\ holds = FALSE /\ seen = FALSE /\ open = FALSE /\ acked = FALSE
  /\ first = -1 /\ again = 0 /\ clk = 0 /\ written = 0 /\ pushes = 0
  /\ late = FALSE /\ markAt = -1 /\ lastPush = -1 /\ tickPushes = 0
  /\ watchAt = -1 /\ since = -1 /\ path = "none"

\* The world: the friend's session answers or not, her cards finish or not, the
\* coordinator answers the late judgments or not; her row fills or empties, she goes
\* down or up, the cards she could do come and go; a pinned card off her row is
\* finished or taken back to her (only the deal puts one off her row).
Flip ==
  /\ IF Kind = "behind"
       THEN late' = ~late /\ UNCHANGED holds
       ELSE /\ Kind = "pin" => holds
            /\ holds' = ~holds /\ UNCHANGED late
  /\ path' = IF Kind = "pin" THEN "none" ELSE path
  /\ UNCHANGED <<seen, open, acked, first, again, clk, written, pushes, markAt, lastPush, tickPushes,
                 watchAt, since>>

\* The coordinator acknowledges the open judgment.
Ack ==
  /\ open /\ ~acked
  /\ open' = FALSE /\ acked' = TRUE
  /\ UNCHANGED <<holds, seen, first, again, clk, written, pushes, late, markAt, lastPush, tickPushes,
                 watchAt, since, path>>

\* The condition the tick's pass reads. Behind: the late judgment's overdue line,
\* written by an earlier tick (the pass reads the holds before this tick's), is Every
\* old. Empty: the empty-row clock, written by an earlier tick, is After old. Pin: the
\* card is off her row after this tick's deal.
Cond ==
  CASE Kind = "behind" ->
         late /\ IF Broken = "doublepush" THEN TRUE ELSE markAt # -1 /\ clk + 1 - markAt >= Every
    [] Kind = "empty" ->
         holds /\ (Broken = "noclock" \/ (watchAt # -1 /\ clk + 1 - watchAt >= After))
    [] Kind = "pin" ->
         CASE Broken = "passbefore" -> holds
           [] Broken = "nopass"     -> holds' /\ path' = "friend"
           [] OTHER                 -> holds'
    [] OTHER -> holds

\* This tick's overdue line about the late judgment: the first tick that finds it late.
Line == Kind = "behind" /\ late /\ markAt = -1

\* This tick's pass pushes: it raises the judgment, or raises it again.
PassPush ==
  /\ Cond
  /\ \/ ~open /\ ~acked
     \/ open /\ (clk + 1 - first) \div Every > again /\ Broken # "noreraise"

\* The tick's deal (Kind = "pin"): it may place the pinned card off her row, by the
\* friends' deal or a machine's.
Deal ==
  IF Kind = "pin"
    THEN \E d \in BOOLEAN, how \in {"friend", "fleet"}:
           /\ holds' = (holds \/ d)
           /\ path' = IF ~holds /\ d THEN how ELSE path
    ELSE UNCHANGED <<holds, path>>

\* One tick: the deal, the overdue part's line, then the pass (TickCoordinatorPass:
\* notify, then reraise).
Tick ==
  /\ clk < MaxClock
  /\ clk' = clk + 1
  /\ Deal
  /\ seen' = Cond
  /\ UNCHANGED late
  /\ watchAt' = IF Kind # "empty" THEN -1
                ELSE IF holds THEN (IF watchAt = -1 THEN clk + 1 ELSE watchAt)
                ELSE IF Broken = "keepclock" THEN watchAt ELSE -1
  /\ since' = IF Kind = "empty" /\ holds THEN (IF since = -1 THEN clk + 1 ELSE since) ELSE -1
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

\* Empty: a friend is told only once her row has been empty, while cards she could do
\* wait, for After of running time without a break.
EmptyToldOnlyAfterWindow ==
  (Kind = "empty" /\ (open \/ acked)) => (since # -1 /\ first - since >= After)

\* Empty: once that has held for After, she is told (or the coordinator acknowledged it).
EmptyToldByWindow ==
  (Kind = "empty" /\ since # -1 /\ clk - since >= After) => (open \/ acked)

\* Pin: a pinned card off her row is never silent: the tick that puts it there tells it,
\* by the friends' deal's note or the pass.
PinNeverSilent == (Kind = "pin" /\ holds) => (open \/ acked)

=============================================================================
