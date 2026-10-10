--------------------------- MODULE FriendBatch ----------------------------
\* A friend's presence while the daemon's own batch turn runs (a sibling of
\* FriendPresence.tla; internal/friend/presence.go, the batch turn of
\* nova-tools#5540 head 71c747f6ef). FriendPresence.tla models the world and
\* the table; this module models only the one rule the batch turn adds, so the
\* batch turn's state does not enlarge the presence model's reachable states.
\*
\* The finding of 2026-10-10 on two batch friends (dsh and opencode): a batch
\* session takes every waiting card in one turn of 20 to 40 minutes, the
\* session check cannot be answered until the turn ends, and the bound called
\* her down mid-turn, so the sprint took her unstarted cards back. Deaf is a
\* message to her not seen: while the daemon's own batch turn is in the
\* session, neither the open-check bound nor the quiet bound runs against an
\* unanswered check. The exemption is capped by wall clock, BehindCap, from
\* the first time the unanswered check went behind a turn (BehindSince);
\* neither a turn's start nor a check asked again resets it, only an answer
\* does. Back-to-back turns carry the check across turns, and at the cap the
\* bounds run through the turn.
\*
\* A batch turn is "told running" when the daemon announces it (BatchTurn),
\* and "in the session" only once a batch Deliver holds the gate
\* (SessionCheck.inSession, taken after the turn's RLock). A turn the daemon
\* started that still waits at the gate behind a check holding it alone is
\* told, not in the session, so it never holds a check's bound off. That is
\* the cold read of 7077ed131: told running before it took the gate, a hang
\* check and the turn behind it held each other, the session up for ever. This
\* module's `hung` is that check's delivery holding the gate; `turn` is the
\* daemon's batch turn (off, told, session).
\*
\* Time is ages, as in FriendPresence.tla: heldAge counts the ticks the bound
\* has been held off by a turn, saturating at BehindCap + Bound; behindAge
\* counts the ticks the unanswered check has been behind a turn in the
\* session, saturating the same way. The design (Broken = "none") holds the
\* bound off only while the turn is in the session and under the cap, so a
\* deaf session is shown down within BehindCap + Bound (the property below).
\*
\* Broken is the design ("none") or one reversed witness, each caught by the
\* deaf-session property:
\*   "nocap"   the cap is dropped: a turn (or back-to-back turns) in the
\*             session holds a deaf friend up for ever.
\*   "batter"  the bound is held from the turn being told rather than from its
\*             being in the session: a hung check holds the gate, the turn
\*             waits at the gate told, and neither ever ends, so the cap never
\*             starts and the friend is up for ever (7077ed131).

EXTENDS Naturals

CONSTANTS Bound, BehindCap, MaxTurns, Broken

ASSUME Bound >= 1 /\ BehindCap >= Bound /\ BehindCap \in Nat /\ MaxTurns \in Nat

VARIABLES answered, answerAge, pending, turn, hung, heldAge, behindAge, capped, events

vars == <<answered, answerAge, pending, turn, hung, heldAge, behindAge, capped, events>>

Up1(n) == IF n < Bound THEN n + 1 ELSE Bound

\* The held clock saturates at the deaf-session bound: BehindCap of wall clock
\* behind a turn, plus one bound for the run through the turn at the cap.
CapTotal == BehindCap + Bound
UpCap(n) == IF n < CapTotal THEN n + 1 ELSE CapTotal

\* The condition under which a running batch turn keeps the bound off: in the
\* session under the design, and (the witness "batter") merely told running.
TurnKeeps == IF Broken = "batter" THEN turn /= "off" ELSE turn = "session"

\* An unanswered check is held while a turn keeps it and the cap is not reached.
Held == pending /\ TurnKeeps /\ ~capped

\* The table's word: an answer younger than the bound, or a held unanswered
\* check (a turn in the session under the cap).
Status == IF (answered /\ answerAge < Bound) \/ Held THEN "up" ELSE "down"

TypeOK ==
  /\ answered \in BOOLEAN
  /\ answerAge \in 0..Bound
  /\ pending \in BOOLEAN
  /\ turn \in {"off", "told", "session"}
  /\ hung \in BOOLEAN
  /\ heldAge \in 0..CapTotal
  /\ behindAge \in 0..CapTotal
  /\ capped \in BOOLEAN
  /\ events \in 0..MaxTurns

Init ==
  /\ answered = FALSE
  /\ answerAge = Bound
  /\ pending = FALSE
  /\ turn = "off"
  /\ hung = FALSE
  /\ heldAge = 0
  /\ behindAge = 0
  /\ capped = FALSE
  /\ events = 0

\* The session answers the open check: up again, and only an answer clears the
\* behind clock and the cap (Presence.Answer).
Answer ==
  /\ pending
  /\ answered' = TRUE
  /\ answerAge' = 0
  /\ pending' = FALSE
  /\ hung' = FALSE
  /\ heldAge' = 0
  /\ behindAge' = 0
  /\ capped' = FALSE
  /\ UNCHANGED <<turn, events>>

\* A session check goes into the session (Presence.Ask): its delivery holds the
\* gate alone until the session reads it, and the bound runs from here.
Ask ==
  /\ ~pending
  /\ pending' = TRUE
  /\ hung' = TRUE
  /\ heldAge' = 0
  /\ behindAge' = 0
  /\ capped' = FALSE
  /\ UNCHANGED <<answered, answerAge, turn, events>>

\* The session takes the check (a headless ReadOnReturn turn, or the adapter saw
\* it): the delivery leaves the gate, the check is in the session unanswered.
Read ==
  /\ hung
  /\ hung' = FALSE
  /\ UNCHANGED <<answered, answerAge, pending, turn, heldAge, behindAge, capped, events>>

\* The daemon starts its batch turn: told running, it may wait at the gate.
TurnStart ==
  /\ turn = "off" /\ events < MaxTurns
  /\ turn' = "told"
  /\ events' = events + 1
  /\ UNCHANGED <<answered, answerAge, pending, hung, heldAge, behindAge, capped>>

\* The batch turn takes the gate and is in the session (SessionCheck.inSession).
TurnEnter ==
  /\ turn = "told" /\ ~hung
  /\ turn' = "session"
  /\ UNCHANGED <<answered, answerAge, pending, hung, heldAge, behindAge, capped, events>>

\* The batch turn ends.
TurnEnd ==
  /\ turn /= "off"
  /\ turn' = "off"
  /\ UNCHANGED <<answered, answerAge, pending, hung, heldAge, behindAge, capped, events>>

\* The check's bound runs while no turn holds it: unanswered past the bound, it
\* goes down and its delivery, if any, is cancelled (the gate frees).
Expire ==
  /\ pending /\ ~Held
  /\ pending' = FALSE
  /\ hung' = FALSE
  /\ UNCHANGED <<answered, answerAge, turn, heldAge, behindAge, capped, events>>

\* Time passes: every age one older (saturating). heldAge counts while the bound
\* is held; behindAge counts while the turn is in the session, and the cap is
\* reached when it gets to BehindCap (never, under "nocap").
Tick ==
  LET hd == Held
      a2 == Up1(answerAge)
      h2 == IF hd THEN UpCap(heldAge) ELSE heldAge
      b2 == IF hd /\ turn = "session" THEN UpCap(behindAge) ELSE behindAge
      c2 == capped \/ (Broken /= "nocap" /\ b2 >= BehindCap)
      gone == pending /\ ~hd /\ a2 >= Bound
  IN
  /\ answerAge' = a2
  /\ heldAge' = h2
  /\ behindAge' = b2
  /\ capped' = c2
  /\ pending' = IF gone THEN FALSE ELSE pending
  /\ hung' = IF gone THEN FALSE ELSE hung
  /\ UNCHANGED <<answered, turn, events>>

Next ==
  \/ Answer
  \/ Ask
  \/ Read
  \/ TurnStart
  \/ TurnEnter
  \/ TurnEnd
  \/ Expire
  \/ Tick

Spec == Init /\ [][Next]_vars

\* ---------------------------------------------------------------- the rules

\* A friend shown up has an answer younger than the bound, or a turn in the
\* session and under the cap.
UpHasFreshAnswer ==
  Status = "up" => (answered /\ answerAge < Bound) \/ (turn = "session" /\ ~capped)

\* A deaf session (an unanswered check) the bound is held behind turns for is
\* shown down within BehindCap + Bound: once the held clock reaches the bound,
\* the friend is not up.
DeafSessionShownDownWithinBoundPlusCap ==
  heldAge >= BehindCap + Bound => Status /= "up"

=============================================================================
