--------------------------- MODULE FriendPresence ---------------------------
\* A friend's presence on the sprint's table, and the cards the table deals
\* the friend (docs/SPEC-FRIEND.md, "Presence"; written before the code that
\* implements it, card fr-presence-model). The daemon's own machine, the
\* connection and the challenge, is Friend.tla; the seat fence and the
\* observation record are SeatHealth.tla. This module is the layer above
\* both: what the friend really is (the harness, the session, the provider's
\* limit, the daemon), what the table shows (up, held or down), and where
\* the friend's cards are.
\*
\* The harness here is whatever runs the session: the app, or a harness run
\* from its command line (dsh headless, codex exec, claude -p), a session
\* like any other. What the daemon's process-table look sees is app (seen,
\* notseen), a separate word that moves on its own: a headless session runs
\* with no app seen, and an app can be open with nothing answering in it.
\* app is advisory: no rule of the table and no step of the beat reads it
\* (docs/SPEC-FRIEND.md, "The harness check"; the finding of 2026-10-05,
\* card liveness-is-the-session-pong-not-an-app). Watched is the friends whose
\* app look can move; every other friend's app is never seen, a headless
\* session for ever (a constant only to keep the run in budget).
\*
\* What the world holds, per friend: harness (running, closed), session
\* (answering, silent), limit (none, limited until a reset), daemon (beating,
\* always: the beat is in the model so that a rule which reads it can be
\* caught). What the table holds: held (the coordinator's hold), the age of
\* the session's last answer to a nonce (answered, answerAge), an open nonce
\* (pending), and holder, where each card is (a friend or the pool, with
\* takenFrom the friend a card was last taken back from). The daemon's own
\* batch turn: turn (none, gate, session), the gate a check's delivery holds
\* (checkHolds), and, per friend, the unanswered check waiting behind a turn
\* (behind), the age since it first went behind (behindAge), and whether the
\* BehindCap is reached and said (capped). Ghosts: closedAge (how long the
\* harness has been closed), events (the disruptions spent).
\*
\* Bound is the longest a friend stays up with no proof from the session:
\* in internal/friend/presence.go, SessionQuiet (the quiet before a check
\* goes in) plus SessionBound (the wait for its answer). BehindCap is the
\* most wall clock an unanswered check is held off by the daemon's own batch
\* turns, from the first time it went behind one until an answer
\* (Presence.BehindSince): above the 20 to 40 minute turns measured
\* 2026-10-10, so a working batch session is never called down mid-turn, and
\* bounded, so back-to-back turns never hold a deaf session up for ever.
\*
\* Time is ages, not a clock: every age counts up one a tick and stops at
\* Bound (or BehindCap), which means "Bound or older". So the clock never
\* ends, Tick is always enabled, and the liveness properties are checked on
\* an infinite clock over finitely many states.
\*
\* The table's word is derived at every read (Status): held is the hold
\* alone; up is an answer to a nonce younger than Bound or, the session
\* cannot answer while it runs, a batch turn in the session with an
\* unanswered check behind it less than BehindCap old; down is the rest.
\* The daemon's beat is not read. A friend leaves up in exactly three steps,
\* the coordinator's hold, the tick (an answer ageing to Bound, or the
\* BehindCap reached) and the harness close (a closed harness runs no batch
\* turn), and each takes back every card the friend holds in the same step:
\* the hold's withdrawal, the tick's rebalance and the close's. So a held or
\* down friend holds no card at any state, not "soon after".
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* each caught by one property below:
\*   "beatup"        the daemon's beat makes the friend up: BeatAloneNeverUp
\*   "noexpiry"      an answer never ages: UpHasFreshAnswer, and on SpecClosed
\*                   ClosedShownDown (a closed harness shown up for ever)
\*   "closedanswers" an answer is counted with the harness closed (the daemon
\*                   answering in the session's place): UpHasRunningHarness
\*   "dealdown"      the dealer deals to any friend not held: NoCardOffUp
\*   "lazywithdraw"  the hold and the tick leave the cards, a later step takes
\*                   them back: NoCardOffUp
\*   "backtoheld"    the deal gives a card back to the friend it was taken
\*                   from: HeldCardDealtElsewhere
\*   "appholds"      the app not seen holds the beat back, so no check goes
\*                   in (the finding of 2026-10-05): SessionShownUp
\*   "appup"         the app seen makes the friend up: BeatAloneNeverUp
\*   "ctlstatus"     a take for a friend reads a control card status, which
\*                   her row never carries (only a machine's does): her
\*                   ready card is never taken while she is up,
\*                   ReadyTakenWhileUp
\*   "nocap"         no BehindCap: back-to-back turns hold a deaf session up
\*                   for ever: DeafShownDownWithinCap
\*   "gateturn"      a turn told running counts while it waits at the gate,
\*                   not only once it is in the session: the hung check and
\*                   the turn behind it hold each other (the cold read of
\*                   7077ed131): UpHasFreshAnswer
\*
\* The take (internal/sprint/steps_work.go takeSeat; the card
\* take-by-id-reads-the-friends-presence.w1): a card on a friend's row is
\* ready until it is taken into working (taken), by her own take or the
\* daemon's through the server. The take is admitted by the table's word,
\* Status (FriendStatus), and by nothing else: up takes, held and down are
\* refused (TakeOnlyWhenUp). The night of 2026-10-05 the take read the row's
\* control card status, which a friend's row has none of, and refused every
\* friend up ("member friend.<f> is -"): the witness "ctlstatus".

EXTENDS Naturals, FiniteSets

CONSTANTS Friends, Cards, Bound, BehindCap, MaxEvents, Broken, Watched

ASSUME Bound >= 1 /\ BehindCap >= 1 /\ MaxEvents \in Nat /\ Watched \subseteq Friends

VARIABLES harness, closedAge, session, limit, daemon, app,
          held, answered, answerAge, pending, holder, takenFrom, taken, events,
          turn, checkHolds, behind, behindAge, capped
vars == <<harness, closedAge, session, limit, daemon, app,
          held, answered, answerAge, pending, holder, takenFrom, taken, events,
          turn, checkHolds, behind, behindAge, capped>>
world == <<harness, closedAge, session, limit, daemon, app>>

Pool == "pool"
NoOne == "none"

TypeOK ==
  /\ harness \in [Friends -> {"running", "closed"}]
  /\ closedAge \in [Friends -> 0..Bound]
  /\ session \in [Friends -> {"answering", "silent"}]
  /\ limit \in [Friends -> {"none", "limited"}]
  /\ daemon \in [Friends -> {"beating"}]
  /\ app \in [Friends -> {"seen", "notseen"}]
  /\ held \in [Friends -> BOOLEAN]
  /\ answered \in [Friends -> BOOLEAN]
  /\ answerAge \in [Friends -> 0..Bound]
  /\ pending \in [Friends -> BOOLEAN]
  /\ holder \in [Cards -> Friends \cup {Pool}]
  /\ takenFrom \in [Cards -> Friends \cup {NoOne}]
  /\ taken \in [Cards -> BOOLEAN]
  /\ events \in 0..MaxEvents
  /\ turn \in [Friends -> {"none", "gate", "session"}]
  /\ checkHolds \in [Friends -> BOOLEAN]
  /\ behind \in [Friends -> BOOLEAN]
  /\ behindAge \in [Friends -> 0..BehindCap]
  /\ capped \in [Friends -> BOOLEAN]

\* The daemon's batch turn over any turn state, so a step can read the turn
\* its own result shows. Running is the turn in the session (the design), or,
\* under "gateturn", any turn told running: the cold read of 7077ed131, where
\* a turn waiting at the gate behind a hung check held that check's bound off
\* too, so neither ever ended.
Running(f) ==
  /\ harness[f] = "running"
  /\ IF Broken = "gateturn" THEN turn[f] # "none" ELSE turn[f] = "session"

\* The table's word over any held/answered/answerAge, so a step can read the
\* word its own result shows. The witnesses let the beat or a stale answer
\* say up. HeldOffIn is the batch turn's exemption: an unanswered check behind
\* a turn in the session, under BehindCap. The witness "nocap" drops the cap
\* (back-to-back turns hold the bound off for ever); "gateturn" counts a turn
\* told running while it waits at the gate.
HeldOffIn(f, tn, bh, bAge) ==
  /\ pending[f]
  /\ harness[f] = "running"
  /\ CASE Broken = "nocap"    -> tn[f] = "session" /\ bh[f]
       [] Broken = "gateturn" -> tn[f] # "none"   /\ bh[f] /\ bAge[f] < BehindCap
       [] OTHER               -> tn[f] = "session" /\ bh[f] /\ bAge[f] < BehindCap

UpIn(f, tn, ans, age, bh, bAge) ==
  CASE Broken = "beatup"   -> daemon[f] = "beating"
    [] Broken = "appup"    -> app[f] = "seen"
    [] Broken = "noexpiry" -> ans[f]
    [] OTHER               -> ans[f] /\ (age[f] < Bound \/ HeldOffIn(f, tn, bh, bAge))
StatusIn(f, hd, tn, ans, age, bh, bAge) ==
  IF hd[f] THEN "held" ELSE IF UpIn(f, tn, ans, age, bh, bAge) THEN "up" ELSE "down"
Status(f) == StatusIn(f, held, turn, answered, answerAge, behind, behindAge)

CardsOf(f) == {c \in Cards : holder[c] = f}

\* Take back every card of a friend not up in the step's result: the hold's
\* withdrawal and the tick's rebalance. The witness "lazywithdraw" takes back
\* nothing here.
Lazy == Broken = "lazywithdraw"
TakeBack(hd, tn, ans, age, bh, bAge) ==
  /\ holder' = [c \in Cards |->
                  IF ~Lazy /\ holder[c] \in Friends /\ StatusIn(holder[c], hd, tn, ans, age, bh, bAge) # "up"
                    THEN Pool ELSE holder[c]]
  /\ takenFrom' = [c \in Cards |->
                     IF ~Lazy /\ holder[c] \in Friends /\ StatusIn(holder[c], hd, tn, ans, age, bh, bAge) # "up"
                       THEN holder[c] ELSE takenFrom[c]]
  /\ taken' = [c \in Cards |->
                 IF ~Lazy /\ holder[c] \in Friends /\ StatusIn(holder[c], hd, tn, ans, age, bh, bAge) # "up"
                   THEN FALSE ELSE taken[c]]

Init ==
  /\ harness = [f \in Friends |-> "running"]
  /\ closedAge = [f \in Friends |-> 0]
  /\ session = [f \in Friends |-> "answering"]
  /\ limit = [f \in Friends |-> "none"]
  /\ daemon = [f \in Friends |-> "beating"]
  /\ app \in {a \in [Friends -> {"seen", "notseen"}] : \A f \in Friends \ Watched : a[f] = "notseen"}
  /\ held = [f \in Friends |-> FALSE]
  /\ answered = [f \in Friends |-> FALSE]
  /\ answerAge = [f \in Friends |-> Bound]
  /\ pending = [f \in Friends |-> FALSE]
  /\ holder = [c \in Cards |-> Pool]
  /\ takenFrom = [c \in Cards |-> NoOne]
  /\ taken = [c \in Cards |-> FALSE]
  /\ events = 0
  /\ turn = [f \in Friends |-> "none"]
  /\ checkHolds = [f \in Friends |-> FALSE]
  /\ behind = [f \in Friends |-> FALSE]
  /\ behindAge = [f \in Friends |-> 0]
  /\ capped = [f \in Friends |-> FALSE]

Up1(n) == IF n < Bound THEN n + 1 ELSE Bound
UpCap(n) == IF n < BehindCap THEN n + 1 ELSE BehindCap

\* ------------------------------------------------------------- the world

\* The app closes or opens. A disruption (close, silence, limit, hold) spends
\* one of MaxEvents; a recovery spends nothing. A closed harness runs no batch
\* turn: the turn ends, the check is no longer behind, and the close takes back
\* the friend's cards in the same step, as the tick does.
Close(f) ==
  /\ harness[f] = "running" /\ events < MaxEvents
  /\ harness' = [harness EXCEPT ![f] = "closed"]
  /\ closedAge' = [closedAge EXCEPT ![f] = 0]
  /\ turn' = [turn EXCEPT ![f] = "none"]
  /\ behind' = [behind EXCEPT ![f] = FALSE]
  /\ TakeBack(held, turn', answered, answerAge, behind', behindAge)
  /\ events' = events + 1
  /\ UNCHANGED <<session, limit, daemon, app, held, answered, answerAge, pending, checkHolds, behindAge, capped>>
Open(f) ==
  /\ harness[f] = "closed"
  /\ harness' = [harness EXCEPT ![f] = "running"]
  /\ closedAge' = [closedAge EXCEPT ![f] = 0]
  /\ UNCHANGED <<session, limit, daemon, app, held, answered, answerAge, pending, holder, takenFrom, taken, turn, checkHolds, behind, behindAge, capped, events>>

\* The session goes silent, or takes turns again.
Silence(f) ==
  /\ session[f] = "answering" /\ events < MaxEvents
  /\ session' = [session EXCEPT ![f] = "silent"]
  /\ events' = events + 1
  /\ UNCHANGED <<harness, closedAge, limit, daemon, app, held, answered, answerAge, pending, holder, takenFrom, taken, turn, checkHolds, behind, behindAge, capped>>
Resume(f) ==
  /\ session[f] = "silent"
  /\ session' = [session EXCEPT ![f] = "answering"]
  /\ UNCHANGED <<harness, closedAge, limit, daemon, app, held, answered, answerAge, pending, holder, takenFrom, taken, turn, checkHolds, behind, behindAge, capped, events>>

\* The process-table look moves on its own: an app opens or closes with no
\* tie to the session (a headless run, an app left open). Neither a
\* disruption nor a recovery: it spends nothing and nothing waits for it.
AppMoves(f) ==
  /\ f \in Watched
  /\ app' = [app EXCEPT ![f] = IF app[f] = "seen" THEN "notseen" ELSE "seen"]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, held, answered, answerAge, pending, holder, takenFrom, taken, turn, checkHolds, behind, behindAge, capped, events>>

\* The provider's limit hits, and later resets.
LimitHit(f) ==
  /\ limit[f] = "none" /\ events < MaxEvents
  /\ limit' = [limit EXCEPT ![f] = "limited"]
  /\ events' = events + 1
  /\ UNCHANGED <<harness, closedAge, session, daemon, app, held, answered, answerAge, pending, holder, takenFrom, taken, turn, checkHolds, behind, behindAge, capped>>
LimitReset(f) ==
  /\ limit[f] = "limited"
  /\ limit' = [limit EXCEPT ![f] = "none"]
  /\ UNCHANGED <<harness, closedAge, session, daemon, app, held, answered, answerAge, pending, holder, takenFrom, taken, turn, checkHolds, behind, behindAge, capped, events>>

\* ------------------------------------------------------------- the table

\* The coordinator pings: a fresh nonce is open for the friend
\* (the daemon's session check, docs/SPEC-FRIEND.md "Presence"). The check's
\* delivery comes to the gate as a turn of its own, which is why it holds it
\* (checkHolds) until the session reads it (ReadCheck) or its bound cancels it
\* (CancelCheck). The process table is never read here; the witness "appholds"
\* lets the app not seen hold the beat, and with it the check, back.
Ping(f) ==
  /\ ~pending[f]
  /\ Broken # "appholds" \/ app[f] = "seen"
  /\ pending' = [pending EXCEPT ![f] = TRUE]
  /\ checkHolds' = [checkHolds EXCEPT ![f] = TRUE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, answered, answerAge, holder, takenFrom, taken, events, turn, behind, behindAge, capped>>

\* The session reads the open check: its turn came back from the gate, so the
\* gate is free (a batch turn may take it), but the check is unanswered still.
ReadCheck(f) ==
  /\ pending[f] /\ checkHolds[f]
  /\ checkHolds' = [checkHolds EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, answered, answerAge, pending, holder, takenFrom, taken, events, turn, behind, behindAge, capped>>

\* The session answers the open nonce: only a running harness, a session
\* that takes turns and a provider not limiting it can (the witness
\* "closedanswers" counts an answer from a closed harness). The answer's age
\* starts again, and it clears BehindSince and Capped: only an answer does.
CanAnswer(f) ==
  /\ harness[f] = "running" \/ Broken = "closedanswers"
  /\ session[f] = "answering"
  /\ limit[f] = "none"
Answer(f) ==
  /\ pending[f] /\ CanAnswer(f)
  /\ pending' = [pending EXCEPT ![f] = FALSE]
  /\ checkHolds' = [checkHolds EXCEPT ![f] = FALSE]
  /\ answered' = [answered EXCEPT ![f] = TRUE]
  /\ answerAge' = [answerAge EXCEPT ![f] = 0]
  /\ behind' = [behind EXCEPT ![f] = FALSE]
  /\ behindAge' = [behindAge EXCEPT ![f] = 0]
  /\ capped' = [capped EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, holder, takenFrom, taken, events, turn>>

\* The open check's bound has run against a friend already down (not held off
\* by a turn in the session): its delivery is cancelled and the gate freed, so
\* a batch turn may take it, but the nonce stays open for the session's late
\* answer (only an answer clears it). A turn waiting at the gate holds no bound
\* off (Running is the session), so the hung check of 7077ed131 goes down here,
\* its cancel frees the gate, and the daemon's turn gets through.
CancelCheck(f) ==
  /\ pending[f]
  /\ answerAge[f] = Bound
  /\ Status(f) # "up"
  /\ checkHolds[f] \/ behind[f]
  /\ checkHolds' = [checkHolds EXCEPT ![f] = FALSE]
  /\ behind' = [behind EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, answered, answerAge, pending, holder, takenFrom, taken, events, turn, behindAge, capped>>

\* The coordinator holds a friend, and the hold takes back the friend's cards
\* in the same step; or releases the friend.
Hold(f) ==
  /\ ~held[f] /\ events < MaxEvents
  /\ held' = [held EXCEPT ![f] = TRUE]
  /\ TakeBack(held', turn, answered, answerAge, behind, behindAge)
  /\ events' = events + 1
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, answered, answerAge, pending, turn, checkHolds, behind, behindAge, capped>>
Release(f) ==
  /\ held[f]
  /\ held' = [held EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, answered, answerAge, pending, holder, takenFrom, taken, events, turn, checkHolds, behind, behindAge, capped>>

\* The daemon's own batch turn. StartTurn is the daemon telling the turn
\* running (Daemon.BatchTurn), at the gate; TakeGate is taking it, so the turn
\* is in the session and holds an unanswered check's bound off. EndTurn ends
\* it: the check is no longer behind, but BehindSince persists (a new turn, or
\* a check asked again, never buys more time; only an answer clears it). A
\* turn never starts with the harness closed.
StartTurn(f) ==
  /\ harness[f] = "running"
  /\ turn[f] = "none"
  /\ turn' = [turn EXCEPT ![f] = "gate"]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, answered, answerAge, pending, holder, takenFrom, taken, events, checkHolds, behind, behindAge, capped>>
TakeGate(f) ==
  /\ harness[f] = "running"
  /\ turn[f] = "gate" /\ ~checkHolds[f]
  /\ turn' = [turn EXCEPT ![f] = "session"]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, answered, answerAge, pending, holder, takenFrom, taken, events, checkHolds, behind, behindAge, capped>>
EndTurn(f) ==
  /\ turn[f] # "none"
  /\ turn' = [turn EXCEPT ![f] = "none"]
  /\ behind' = [behind EXCEPT ![f] = FALSE]
  /\ TakeBack(held, turn', answered, answerAge, behind', behindAge)
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, answered, answerAge, pending, checkHolds, behindAge, capped, events>>

\* Time passes: every age one older, and the tick's rebalance takes back the
\* cards of every friend no longer up, in the same step. BehindSince is set
\* when an unanswered check first goes behind a turn in the session (Running),
\* and its age counts on from then whether or not a turn runs: a turn's start
\* or a check asked again never resets it, only an answer does. At BehindCap
\* the cap is said once (capped) and the bounds run through the turn.
Tick ==
  /\ answerAge' = [f \in Friends |-> Up1(answerAge[f])]
  /\ closedAge' = [f \in Friends |-> IF harness[f] = "closed" THEN Up1(closedAge[f]) ELSE 0]
  /\ behind' = [f \in Friends |-> behind[f] \/ (Running(f) /\ pending[f])]
  /\ behindAge' = [f \in Friends |->
                     IF behind[f] \/ (Running(f) /\ pending[f]) \/ behindAge[f] # 0
                       THEN UpCap(behindAge[f]) ELSE 0]
  /\ capped' = [f \in Friends |->
                   capped[f] \/ ((behind[f] \/ (Running(f) /\ pending[f]) \/ behindAge[f] # 0)
                                 /\ UpCap(behindAge[f]) = BehindCap)]
  /\ TakeBack(held, turn, answered, answerAge', behind', behindAge')
  /\ UNCHANGED <<harness, session, limit, daemon, app, held, answered, pending, turn, checkHolds, events>>

\* The dealer deals a card from the pool to a friend up, never back to the
\* friend it was last taken from. The witnesses deal to any friend not held,
\* or back to the one it was taken from.
Dealable(c, g) ==
  /\ holder[c] = Pool
  /\ IF Broken = "dealdown" THEN Status(g) # "held" ELSE Status(g) = "up"
  /\ g # takenFrom[c] \/ Broken = "backtoheld"
Deal(c, g) ==
  /\ Dealable(c, g)
  /\ holder' = [holder EXCEPT ![c] = g]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, answered, answerAge, pending, takenFrom, taken, events, turn, checkHolds, behind, behindAge, capped>>

\* A card ready on a friend's row is taken into working: her take, or the
\* daemon's through the server. Admitted by Status alone; the witness reads a
\* control card status her row never carries, so admits nothing.
TakeAdmits(f) == IF Broken = "ctlstatus" THEN FALSE ELSE Status(f) = "up"
Take(c) ==
  /\ holder[c] \in Friends /\ ~taken[c]
  /\ TakeAdmits(holder[c])
  /\ taken' = [taken EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, answered, answerAge, pending, holder, takenFrom, events, turn, checkHolds, behind, behindAge, capped>>

\* Only under "lazywithdraw": the take-back as a step of its own, after the
\* hold or the tick that made the friend not up.
Withdraw(f) ==
  /\ Lazy
  /\ Status(f) # "up" /\ CardsOf(f) # {}
  /\ holder' = [c \in Cards |-> IF holder[c] = f THEN Pool ELSE holder[c]]
  /\ takenFrom' = [c \in Cards |-> IF holder[c] = f THEN f ELSE takenFrom[c]]
  /\ taken' = [c \in Cards |-> IF holder[c] = f THEN FALSE ELSE taken[c]]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, answered, answerAge, pending, events, turn, checkHolds, behind, behindAge, capped>>

Next ==
  \/ Tick
  \/ \E f \in Friends :
       \/ Close(f) \/ Open(f) \/ AppMoves(f) \/ Silence(f) \/ Resume(f) \/ LimitHit(f) \/ LimitReset(f)
       \/ Ping(f) \/ Answer(f) \/ ReadCheck(f) \/ CancelCheck(f) \/ Hold(f) \/ Release(f) \/ Withdraw(f)
       \/ StartTurn(f) \/ TakeGate(f) \/ EndTurn(f)
  \/ \E c \in Cards, g \in Friends : Deal(c, g)
  \/ \E c \in Cards : Take(c)

\* The safety specification: no fairness.
Spec == Init /\ [][Next]_vars

\* The clock alone is fair: a harness may stay closed, a session silent, a
\* limit unreset for ever. Enough for ClosedShownDown.
SpecClosed == Spec /\ WF_vars(Tick)

\* The full environment assumption for the deal's liveness: disruptions are
\* finite (MaxEvents); every recovery comes (the app reopens, the session
\* answers again, the limit resets, the hold is released); the coordinator
\* keeps pinging and a session able to answer does; a batch turn it started
\* ends; and a card dealable infinitely often is dealt (strong fairness: a
\* friend's up comes and goes with the ticks).
SpecLive ==
  /\ Spec /\ WF_vars(Tick)
  /\ \A f \in Friends :
       /\ WF_vars(Open(f)) /\ WF_vars(Resume(f)) /\ WF_vars(LimitReset(f)) /\ WF_vars(Release(f))
       /\ WF_vars(Ping(f)) /\ WF_vars(Answer(f)) /\ WF_vars(EndTurn(f))
  /\ \A c \in Cards : SF_vars(\E g \in Friends : Deal(c, g)) /\ WF_vars(Take(c))

\* ---------------------------------------------------------------- the rules

\* A friend shown up has either a session answer younger than the bound, or a
\* batch turn in the session with an unanswered check behind it less than
\* BehindCap old: the turn's exemption (the cold read of 7077ed131). A turn
\* waiting at the gate is not in the session and holds no bound off; and the
\* cap is BehindCap, so back-to-back turns hold a deaf session up no longer.
UpHasFreshAnswer ==
  \A f \in Friends :
    Status(f) = "up" =>
      (answered[f] /\ answerAge[f] < Bound)
      \/ (turn[f] = "session" /\ behind[f] /\ behindAge[f] < BehindCap)

\* A deaf session is shown down within BehindCap + Bound: once its unanswered
\* check has waited behind turns for BehindCap (capped) and its answer is
\* Bound old, the table shows it down. The behind age is the turn exemption,
\* the answer age the bound after it; neither exceeds its cap.
DeafShownDownWithinCap ==
  \A f \in Friends :
    behind[f] /\ capped[f] /\ answerAge[f] = Bound => Status(f) # "up"

\* A friend shown up has a running harness, or one closed less than the bound
\* ago: the table cannot see the app close, only the answers stop, so this
\* is the most a table can promise (UpHasRunningHarnessNow, the rule read
\* literally, fails: the finding case MCFriendPresenceFindingRunningNow).
UpHasRunningHarness == \A f \in Friends : Status(f) = "up" => harness[f] = "running" \/ closedAge[f] < Bound
UpHasRunningHarnessNow == \A f \in Friends : Status(f) = "up" => harness[f] = "running"

\* A closed harness is shown down within the bound: the bounded form of
\* ClosedShownDown, a state invariant on the ages.
ClosedDownWithinBound == \A f \in Friends : harness[f] = "closed" /\ closedAge[f] = Bound => Status(f) # "up"

\* A held or down friend holds no card.
NoCardOffUp == \A f \in Friends : Status(f) # "up" => CardsOf(f) = {}

\* The daemon's beat alone never makes a friend up: a friend whose session
\* never answered is not up, however the daemon beats.
BeatAloneNeverUp == \A f \in Friends : ~answered[f] => Status(f) # "up"

\* The liveness. A closed harness is shown down (or opens again) on a clock
\* that keeps ticking (SpecClosed or SpecLive).
ClosedShownDown == \A f \in Friends : harness[f] = "closed" ~> (Status(f) # "up" \/ harness[f] = "running")

\* A session that answers is shown up again and again, whatever the process
\* table says: the app may stay unseen for ever (AppMoves has no fairness; a friend outside Watched never has it)
\* and the friend still comes up on her session's answers (SpecLive).
SessionShownUp == \A f \in Friends : []<>(Status(f) = "up")

\* A card is taken into working only for a friend up: never held, never
\* down (the take's admission is Status, the friends' rule).
TakeOnlyWhenUp ==
  [][\A c \in Cards : (~taken[c] /\ taken'[c]) => Status(holder[c]) = "up"]_vars

\* A card ready on a friend's row is taken while she is up, or leaves her
\* row (SpecLive): never left ready on a friend up for ever.
ReadyTakenWhileUp ==
  \A c \in Cards : (holder[c] \in Friends /\ ~taken[c]) ~> (taken[c] \/ holder[c] = Pool)

\* A card taken back from a friend (held, or down at a tick) is dealt to
\* another friend (SpecLive).
HeldCardDealtElsewhere ==
  \A c \in Cards, f \in Friends :
    (holder[c] = Pool /\ takenFrom[c] = f) ~> (holder[c] \in Friends \ {f})

=============================================================================
