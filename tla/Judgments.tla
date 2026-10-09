----------------------------- MODULE Judgments -----------------------------
\* The coordinator's interrupt: the tick raises a judgment, the seat push loop
\* delivers it once, the coordinator or a named rule answers it, and the tick
\* alarms once when it reaches the running-time bound. The state is the note,
\* its delivery receipt, and its answer receipt (internal/sprint/inbox.go,
\* internal/sprint/decide.go, internal/sprint/rules.go; docs/SPEC-SPRINT.md).
\* SprintRules is instantiated rather than copied: its five rule machines remain
\* the source for the rule names used by this delivery model.

EXTENDS Integers, FiniteSets

CONSTANTS Bound, Broken

RuleNames == {"failed", "bound", "late", "read-late", "hold-need"}
RuleFor == [x \in {"j1"} |-> "late"]

VARIABLES raised, open, pushed, delivered, answered, answerCount, ruleAnswered,
          answerer, age, alarm, alarms

SprintRuleModel == INSTANCE SprintRules WITH
  Part <- "rules", Broken <- "none", Cap <- 2, MaxFails <- 2,
  MaxGen <- 2, Window <- 2, MaxClock <- 4,
  col <- raised, need <- raised, blocked <- raised, twin <- raised,
  tier <- raised, fails <- raised, st <- raised, attempts <- raised, envFails <- raised,
  gen <- raised, waited <- raised, progress <- raised, late <- raised, waits <- raised,
  lst <- raised, stamped <- raised, badReturn <- raised,
  rdst <- raised, rdatt <- raised, rdlast <- raised, rdfind <- raised,
  rdpaths <- raised, rdtwins <- raised, rdtotal <- raised,
  gfails <- raised, stopped <- raised,
  idle <- raised, since <- raised, said <- raised, alarms <- raised, clk <- raised,
  nalarm <- raised, nclear <- raised,
  anst <- raised, anatt <- raised, anreworks <- raised, anhead <- raised,
  ancarry <- raised, anfind <- raised, anfail <- raised, anlast <- raised,
  anland <- raised, anlost <- raised, anfixless <- raised

vars == <<raised, open, pushed, delivered, answered, answerCount, ruleAnswered,
          answerer, age, alarm, alarms>>

TypeOK ==
  /\ raised \in BOOLEAN /\ open \in BOOLEAN /\ pushed \in BOOLEAN
  /\ delivered \in BOOLEAN /\ answered \in BOOLEAN /\ answerCount \in 0..2
  /\ ruleAnswered \in BOOLEAN
  /\ answerer \in {"none", "coordinator"} \cup RuleNames
  /\ age \in 0..Bound /\ alarm \in BOOLEAN /\ alarms \in 0..1

Init ==
  /\ raised = FALSE /\ open = FALSE /\ pushed = FALSE /\ delivered = FALSE
  /\ answered = FALSE /\ answerCount = 0 /\ ruleAnswered = FALSE
  /\ answerer = "none" /\ age = 0 /\ alarm = FALSE /\ alarms = 0

Tick ==
  /\ ~raised
  /\ raised' = TRUE /\ open' = TRUE /\ pushed' = FALSE /\ delivered' = FALSE
  /\ answered' = FALSE /\ answerCount' = 0 /\ ruleAnswered' = FALSE
  /\ answerer' = "none" /\ age' = 0 /\ alarm' = FALSE /\ alarms' = 0

Push ==
  /\ raised /\ open /\ ~pushed
  /\ pushed' = TRUE
  /\ delivered' = IF Broken = "lost-push" THEN FALSE ELSE TRUE
  /\ UNCHANGED <<raised, open, answered, answerCount, ruleAnswered, answerer, age, alarm, alarms>>

CoordinatorAnswer ==
  /\ open /\ delivered /\ (~answered \/ Broken = "double-answer")
  /\ answered' = TRUE /\ answerCount' = answerCount + 1
  /\ open' = IF Broken = "double-answer" THEN TRUE ELSE FALSE
  /\ answerer' = "coordinator"
  /\ UNCHANGED <<raised, pushed, delivered, ruleAnswered, age, alarm, alarms>>

RuleAnswer ==
  /\ open /\ delivered /\ ~answered
  /\ answered' = TRUE /\ answerCount' = answerCount + 1 /\ open' = FALSE
  /\ ruleAnswered' = TRUE
  /\ answerer' = IF Broken = "un-named-rule" THEN "failed" ELSE RuleFor["j1"]
  /\ UNCHANGED <<raised, pushed, delivered, age, alarm, alarms>>

Advance ==
  /\ open /\ age < Bound /\ age' = age + 1
  /\ alarm' = IF age + 1 = Bound
                THEN IF Broken = "stale-no-alarm" THEN FALSE ELSE TRUE
                ELSE alarm
  /\ alarms' = IF age + 1 = Bound
                 THEN IF Broken = "stale-no-alarm" THEN 0 ELSE 1
                 ELSE alarms
  /\ UNCHANGED <<raised, open, pushed, delivered, answered, answerCount,
                 ruleAnswered, answerer>>

Alarm ==
  /\ open /\ age = Bound /\ ~alarm
  /\ alarm' = IF Broken = "stale-no-alarm" THEN FALSE ELSE TRUE
  /\ alarms' = IF Broken = "stale-no-alarm" THEN 0 ELSE 1
  /\ UNCHANGED <<raised, open, pushed, delivered, answered, answerCount,
                 ruleAnswered, answerer, age>>

Next == Tick \/ Push \/ CoordinatorAnswer \/ RuleAnswer \/ Advance \/ Alarm
Spec == Init /\ [][Next]_vars

NoDoubleAnswer == answerCount <= 1
PushDelivered == pushed => delivered
RuleNamed == ruleAnswered => answerer = RuleFor["j1"]
StaleAlarm == (open /\ age = Bound) => alarm /\ alarms = 1

=============================================================================
