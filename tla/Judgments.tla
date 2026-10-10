------------------------------ MODULE Judgments -----------------------------
\* nova-sprint judgments (docs/SPEC-SPRINT.md section 8; internal/sprint/inbox.go,
\* decide.go, rules.go): the judgment as the coordinator's interrupt: raised by
\* the tick, pushed to the seat's inbox (the push loop of verb-seat-install-pushe),
\* answered by the coordinator or by a rule (SprintRules.tla models five rules
\* alone: instance it, never duplicate it), and stale past a bound (the acceptance
\* check accept-self-feeding wants no judgment older than 15 minutes).
\*
\* The state:
\*   clk:        running time of the sprint (0..MaxClock)
\*   status:     each judgment's state ("unraised", "open", "answered")
\*   kind:       the judgment's cause/type (RuleKinds or CoordKinds)
\*   raisedAt:   the clock reading when it was raised
\*   pushed:     how many times the push loop has pushed it (0..1)
\*   seatInbox:  the judgments delivered to the seat's inbox
\*   answered:   how many times it was answered (0..1)
\*   answeredBy: who gave the answer ("none", "coordinator", "rule")
\*   alarms:     how many stale alarms were raised for it (0..1)
\*
\* Invariants:
\*   PushedOnce:           each judgment pushed once, and marked pushed implies delivered
\*   AnsweredAtMostOnce:   each judgment answered at most once
\*   RuleAnswersOnlyNamed: an answer by rule only for a judgment a rule names
\*   StaleRaisesAlarm:     a judgment past the bound raises one alarm
\*
\* Reversed witnesses:
\*   "double_answer":  a double answer (breaks AnsweredAtMostOnce)
\*   "push_lost":      a push lost with the judgment marked pushed (breaks PushedOnce)
\*   "stale_no_alarm": a stale judgment with no alarm (breaks StaleRaisesAlarm)

EXTENDS Integers, FiniteSets

CONSTANTS
  Judgments,      \* Set of judgments, e.g. {"j1", "j2"}
  RuleKinds,      \* Set of judgment kinds that rules name / can answer, e.g. {"failed"}
  CoordKinds,     \* Set of judgment kinds that only a coordinator can answer, e.g. {"defect"}
  StaleBound,     \* Clock ticks after which an open judgment is stale
  MaxClock,       \* Bound on clock
  Broken          \* "none", "double_answer", "push_lost", "stale_no_alarm"

Kinds == RuleKinds \cup CoordKinds

VARIABLES
  clk,
  status,
  kind,
  raisedAt,
  pushed,
  seatInbox,
  answered,
  answeredBy,
  alarms

vars == <<clk, status, kind, raisedAt, pushed, seatInbox, answered, answeredBy, alarms>>

TypeOK ==
  /\ clk \in 0..MaxClock
  /\ status \in [Judgments -> {"unraised", "open", "answered"}]
  /\ kind \in [Judgments -> Kinds \cup {"none"}]
  /\ raisedAt \in [Judgments -> (0..MaxClock) \cup {-1}]
  /\ pushed \in [Judgments -> 0..2]
  /\ seatInbox \subseteq Judgments
  /\ answered \in [Judgments -> 0..2]
  /\ answeredBy \in [Judgments -> {"none", "coordinator", "rule"}]
  /\ alarms \in [Judgments -> 0..2]

Init ==
  /\ clk = 0
  /\ status = [j \in Judgments |-> "unraised"]
  /\ kind = [j \in Judgments |-> "none"]
  /\ raisedAt = [j \in Judgments |-> -1]
  /\ pushed = [j \in Judgments |-> 0]
  /\ seatInbox = {}
  /\ answered = [j \in Judgments |-> 0]
  /\ answeredBy = [j \in Judgments |-> "none"]
  /\ alarms = [j \in Judgments |-> 0]

\* The tick raises a judgment with a specific cause/kind.
Raise(j, k) ==
  /\ status[j] = "unraised"
  /\ k \in Kinds
  /\ status' = [status EXCEPT ![j] = "open"]
  /\ kind' = [kind EXCEPT ![j] = k]
  /\ raisedAt' = [raisedAt EXCEPT ![j] = clk]
  /\ UNCHANGED <<clk, pushed, seatInbox, answered, answeredBy, alarms>>

\* The push loop delivers an open judgment to the seat's inbox.
\* The witness "push_lost" marks the judgment pushed without delivering to seatInbox.
Push(j) ==
  /\ status[j] = "open"
  /\ pushed[j] = 0
  /\ pushed' = [pushed EXCEPT ![j] = pushed[j] + 1]
  /\ seatInbox' = IF Broken = "push_lost" THEN seatInbox ELSE seatInbox \cup {j}
  /\ UNCHANGED <<clk, status, kind, raisedAt, answered, answeredBy, alarms>>

\* The coordinator answers a judgment from the seat's inbox.
\* The witness "double_answer" allows answering an already answered judgment.
AnswerCoordinator(j) ==
  /\ j \in seatInbox
  /\ IF Broken = "double_answer"
     THEN status[j] \in {"open", "answered"}
     ELSE status[j] = "open" /\ answered[j] = 0
  /\ status' = [status EXCEPT ![j] = "answered"]
  /\ answered' = [answered EXCEPT ![j] = answered[j] + 1]
  /\ answeredBy' = [answeredBy EXCEPT ![j] = "coordinator"]
  /\ UNCHANGED <<clk, kind, raisedAt, pushed, seatInbox, alarms>>

\* A rule answers an open judgment for which a rule exists (kind[j] \in RuleKinds).
AnswerRule(j) ==
  /\ IF Broken = "double_answer"
     THEN status[j] \in {"open", "answered"}
     ELSE status[j] = "open" /\ answered[j] = 0
  /\ kind[j] \in RuleKinds
  /\ status' = [status EXCEPT ![j] = "answered"]
  /\ answered' = [answered EXCEPT ![j] = answered[j] + 1]
  /\ answeredBy' = [answeredBy EXCEPT ![j] = "rule"]
  /\ UNCHANGED <<clk, kind, raisedAt, pushed, seatInbox, alarms>>

\* The clock advances. An open judgment past the stale bound raises one alarm.
\* The witness "stale_no_alarm" omits raising the alarm when stale.
Tick ==
  /\ clk < MaxClock
  /\ clk' = clk + 1
  /\ alarms' = [j \in Judgments |->
       IF status[j] = "open" /\ (clk + 1 - raisedAt[j] > StaleBound)
       THEN IF Broken = "stale_no_alarm" THEN alarms[j] ELSE 1
       ELSE alarms[j]]
  /\ UNCHANGED <<status, kind, raisedAt, pushed, seatInbox, answered, answeredBy>>

Next ==
  \/ \E j \in Judgments, k \in Kinds : Raise(j, k)
  \/ \E j \in Judgments : Push(j)
  \/ \E j \in Judgments : AnswerCoordinator(j)
  \/ \E j \in Judgments : AnswerRule(j)
  \/ Tick

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
\* INVARIANTS

\* Each judgment is pushed at most once, and a push is not lost.
PushedOnce ==
  \A j \in Judgments:
    /\ pushed[j] <= 1
    /\ (pushed[j] = 1 <=> j \in seatInbox)

\* Each judgment is answered at most once.
AnsweredAtMostOnce ==
  \A j \in Judgments:
    answered[j] <= 1

\* An answer by rule only for a judgment a rule names.
RuleAnswersOnlyNamed ==
  \A j \in Judgments:
    answeredBy[j] = "rule" => kind[j] \in RuleKinds

\* A judgment past the bound raises one alarm.
StaleRaisesAlarm ==
  \A j \in Judgments:
    (status[j] = "open" /\ clk - raisedAt[j] > StaleBound) => (alarms[j] = 1)

=============================================================================
