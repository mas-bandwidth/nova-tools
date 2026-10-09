---------------------------- MODULE Judgments ---------------------------
\* A judgment, the coordinator's interrupt (docs/SPEC-SPRINT.md section 8,
\* "Notifications": a judgment needs the coordinator, each names the decisions
\* open to it; the overdue row of that section; section 11, "Handing over the
\* seat", the push loop). The machine one judgment lives:
\*   Raise       the tick raises it, its condition holding (the world decides
\*               which judgment and when: TLC walks every order)
\*   Push        the seat's push loop (inbox --wait --push seat,
\*               cmd/nova-sprint/inboxwait.go pushLoop) writes it once as
\*               <id>.md into the holder's inbox, the files there the cursor,
\*               an existing file never replaced; a group that closed since the
\*               look that found it is nothing to write
\*   Answer      the coordinator answers it, one of the judgment's own
\*               decisions as a command; the answer is recorded once and never
\*               taken back
\*   RuleAnswer  a rule answers it alone (internal/sprint/rules.go
\*               RuleAnswers, applied by the tick's rule parts): only a
\*               judgment whose type a rule names, and once. The rules
\*               themselves are tla/SprintRules.tla's, instanced here as the
\*               one constant Ruled, never duplicated
\*   Wait        the coordinator sets a review time on it (wait): the bound
\*               counts running time from when wait set it (internal/sprint/
\*               inbox.go due)
\*   Tick        one tick. Its overdue part (internal/sprint/steps_tick.go
\*               TickOverdue) marks each open judgment past its bound once,
\*               in running time: one overdue line, a hold stopping a second
\*               while it stays overdue, the hold closed by the tick when the
\*               judgment closes or is no longer overdue, so a judgment
\*               overdue again is marked again. The code marks at
\*               DeadlineJudgment, ten minutes; the acceptance bound is
\*               fifteen, no judgment older than it: Bound here is that bound
\*               in ticks, whatever its minutes.
\*
\* Two judgments: jr, whose type a rule names, and jm, a mind's. One episode
\* each: a type raised again later is a new judgment id, another episode of
\* this machine (the id carries the sprint's epoch; the alias j<n> is stable
\* for the judgment's life). One subject each, where the code holds the
\* judgment's open subjects and marks each.
\*
\* The design, Broken = "none". Every other value is a reversed witness, each
\* broken by one property:
\*   "doubleanswer"  an answer fires on a judgment already answered, writing a
\*                   second answer: AnsweredAtMostOnce
\*   "lostpush"      the push loop marks the judgment pushed and the file never
\*                   lands in the seat's inbox: NoLostPush
\*   "noalarm"       the overdue part never marks: NoUnalarmedStale
\*   "ruleunruled"   a rule answers a judgment no rule names:
\*                   RuleAnswersOnlyItsOwn
EXTENDS Integers

CONSTANTS J,        \* the judgments of the instance
          Ruled,    \* the judgments whose type a rule names (rules.go ruleByType)
          Bound,    \* the stale bound, in ticks of running time
          MaxClock, \* the clock's end; the machine stutters from there
          Broken

VARIABLES st,         \* each judgment: none, open, answered
          ans,        \* the answers written on it: at most one
          answeredBy, \* who answered: -, the coordinator, a rule
          pushed,     \* the push loop marked it pushed
          inbox,      \* its file is in the seat's inbox
          alarm,      \* its overdue line stands (the hold)
          at,         \* the tick that raised it, or the review time wait set; -1 while none
          clk         \* running time, one step a tick
vars == <<st, ans, answeredBy, pushed, inbox, alarm, at, clk>>

TypeOK ==
  /\ st \in [J -> {"none", "open", "answered"}]
  /\ ans \in [J -> 0..2]
  /\ answeredBy \in [J -> {"-", "coordinator", "rule"}]
  /\ pushed \in [J -> BOOLEAN]
  /\ inbox \in [J -> BOOLEAN]
  /\ alarm \in [J -> BOOLEAN]
  /\ at \in [J -> (0..MaxClock) \cup {-1}]
  /\ clk \in 0..MaxClock

Init ==
  /\ st = [j \in J |-> "none"]
  /\ ans = [j \in J |-> 0]
  /\ answeredBy = [j \in J |-> "-"]
  /\ pushed = [j \in J |-> FALSE]
  /\ inbox = [j \in J |-> FALSE]
  /\ alarm = [j \in J |-> FALSE]
  /\ at = [j \in J |-> -1]
  /\ clk = 0

\* The judgment is open past its bound of running time: past the tick that
\* raised it, or past the review time wait set.
Overdue(j, t) == st[j] = "open" /\ t - at[j] > Bound

\* The tick raises it: a judgment of a condition holding, written with the
\* tick's reading as its At.
Raise(j) ==
  /\ st[j] = "none"
  /\ st' = [st EXCEPT ![j] = "open"]
  /\ at' = [at EXCEPT ![j] = clk]
  /\ UNCHANGED <<ans, answeredBy, pushed, inbox, alarm, clk>>

\* The push loop writes it once, the file in the seat's inbox the cursor; a
\* judgment already pushed is never written again, and a closed one is nothing
\* to write.
Push(j) ==
  /\ st[j] = "open"
  /\ ~pushed[j]
  /\ pushed' = [pushed EXCEPT ![j] = TRUE]
  /\ inbox' = IF Broken = "lostpush" THEN inbox ELSE [inbox EXCEPT ![j] = TRUE]
  /\ UNCHANGED <<st, ans, answeredBy, alarm, at, clk>>

\* The coordinator answers it, one of its own decisions as a command.
Answer(j) ==
  /\ st[j] = "open" \/ (Broken = "doubleanswer" /\ st[j] = "answered")
  /\ st' = [st EXCEPT ![j] = "answered"]
  /\ ans' = [ans EXCEPT ![j] = ans[j] + 1]
  /\ answeredBy' = [answeredBy EXCEPT ![j] = "coordinator"]
  /\ UNCHANGED <<pushed, inbox, alarm, at, clk>>

\* A rule answers it alone: only where a rule names its type, and once, its
\* answer recorded "answered by rule <name>".
RuleAnswer(j) ==
  /\ st[j] = "open" \/ (Broken = "doubleanswer" /\ st[j] = "answered")
  /\ (j \in Ruled) \/ (Broken = "ruleunruled")
  /\ st' = [st EXCEPT ![j] = "answered"]
  /\ ans' = [ans EXCEPT ![j] = ans[j] + 1]
  /\ answeredBy' = [answeredBy EXCEPT ![j] = "rule"]
  /\ UNCHANGED <<pushed, inbox, alarm, at, clk>>

\* The coordinator sets a review time: the bound counts from it, so the
\* judgment is not overdue before it, and the tick closes the hold.
Wait(j) ==
  /\ st[j] = "open"
  /\ at' = [at EXCEPT ![j] = clk]
  /\ UNCHANGED <<st, ans, answeredBy, pushed, inbox, alarm, clk>>

\* One tick: the clock steps, and the overdue part marks each open judgment
\* past its bound, once, the hold standing while it stays overdue and closed
\* when it does not.
Tick ==
  /\ clk < MaxClock
  /\ clk' = clk + 1
  /\ alarm' = [j \in J |->
                 IF Broken = "noalarm" THEN alarm[j]
                 ELSE IF Overdue(j, clk + 1) THEN TRUE
                 ELSE FALSE]
  /\ UNCHANGED <<st, ans, answeredBy, pushed, inbox, at>>

Next ==
  \/ (\E j \in J: Raise(j))
  \/ (\E j \in J: Push(j))
  \/ (\E j \in J: Answer(j))
  \/ (\E j \in J: RuleAnswer(j))
  \/ (\E j \in J: Wait(j))
  \/ Tick

Spec == Init /\ [][Next]_vars

\* Each judgment is answered at most once: the second answer is never written.
AnsweredAtMostOnce == \A j \in J: ans[j] <= 1

\* An answer by rule only for a judgment a rule names.
RuleAnswersOnlyItsOwn == \A j \in J: answeredBy[j] = "rule" => j \in Ruled

\* A judgment marked pushed has its file in the seat's inbox: the mark never
\* stands for a push lost.
NoLostPush == \A j \in J: pushed[j] => inbox[j]

\* A judgment past its bound has its overdue line: the tick raises one alarm.
NoUnalarmedStale == \A j \in J: Overdue(j, clk) => alarm[j]

=============================================================================
