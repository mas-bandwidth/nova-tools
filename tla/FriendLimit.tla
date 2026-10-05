----------------------------- MODULE FriendLimit -----------------------------
\* A harness usage-limit refusal (internal/friend/limit.go, Limits).
\* The state this machine owns: phase (up, down until a parsed reset, waking
\* once that reset has passed and the session has not answered, held when the
\* refusal named no readable reset), until (the instant she is down until),
\* stated (the reset the message named), judged (1 once the unreadable
\* refusal has been judged). now is the clock.
\*
\* Actions are the refusal and the clock, not the parser:
\*   RefuseParsed(t)  a refusal whose reset reads as t, still in the future.
\*                    She is down until t. A hold, if there was one, ends.
\*                    This is not a judgment.
\*   RefuseUnreadable a refusal whose reset does not read, and she was up.
\*                    She is held and judged once. No hour is guessed.
\*   RefuseAgain      the same unreadable refusal while she is held. No
\*                    second judgment, and still no reset.
\*   Tick             the clock. When it reaches the stated reset, down
\*                    becomes waking. A hold has no reset, so a tick leaves
\*                    her held.
\*   Answer           the session answers the wake. Only waking becomes up.
\*
\* ResetOrOneJudgment is the rule: down only until the reset the message
\* stated, and still before it; a hold is one judgment and no until; waking
\* is at or after that reset; up has no judgment open.

EXTENDS Naturals

CONSTANTS MaxTime

VARIABLES now, phase, until, stated, judged

NoTime == 0
vars == <<now, phase, until, stated, judged>>

TypeOK ==
  /\ now \in 0..MaxTime
  /\ phase \in {"up", "down", "waking", "held"}
  /\ until \in 0..MaxTime
  /\ stated \in 0..MaxTime
  /\ judged \in 0..1

Init ==
  /\ now = 0
  /\ phase = "up"
  /\ until = NoTime
  /\ stated = NoTime
  /\ judged = 0

RefuseParsed(t) ==
  /\ t \in 1..MaxTime
  /\ t > now
  /\ phase' = "down"
  /\ until' = t
  /\ stated' = t
  /\ judged' = 0
  /\ now' = now

RefuseUnreadable ==
  /\ phase = "up"
  /\ phase' = "held"
  /\ until' = NoTime
  /\ stated' = NoTime
  /\ judged' = 1
  /\ now' = now

RefuseAgain ==
  /\ phase = "held"
  /\ UNCHANGED vars

Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ IF phase = "down" /\ now' >= until
     THEN /\ phase' = "waking"
          /\ UNCHANGED <<until, stated, judged>>
     ELSE UNCHANGED <<phase, until, stated, judged>>

Answer ==
  /\ phase = "waking"
  /\ phase' = "up"
  /\ until' = NoTime
  /\ stated' = NoTime
  /\ judged' = 0
  /\ now' = now

Next ==
  \/ \E t \in 1..MaxTime : RefuseParsed(t)
  \/ RefuseUnreadable
  \/ RefuseAgain
  \/ Tick
  \/ Answer

Spec == Init /\ [][Next]_vars

\* Down only until the stated reset; an unreadable refusal is one judgment.
ResetOrOneJudgment ==
  /\ (phase = "down" => until = stated /\ stated > now /\ judged = 0)
  /\ (phase = "held" => judged = 1 /\ until = NoTime /\ stated = NoTime)
  /\ (phase = "waking" => now >= stated /\ stated > NoTime /\ judged = 0)
  /\ (phase = "up" => judged = 0 /\ until = NoTime)

=============================================================================
