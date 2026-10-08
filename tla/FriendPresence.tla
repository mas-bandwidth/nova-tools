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
\* takenFrom the friend a card was last taken back from). Ghosts: closedAge
\* (how long the harness has been closed), events (the disruptions spent).
\*
\* Bound is the longest a friend stays up with no proof from the session:
\* in internal/friend/presence.go, SessionQuiet (the quiet before a check
\* goes in) plus SessionBound (the wait for its answer).
\*
\* Time is ages, not a clock: every age counts up one a tick and stops at
\* Bound, which means "Bound or older". So the clock never ends, Tick is
\* always enabled, and the liveness properties are checked on an infinite
\* clock over finitely many states.
\*
\* The table's word is derived at every read (Status): held is the hold
\* alone; up is an answer to a nonce younger than Bound; down is the rest.
\* The daemon's beat is not read. A friend leaves up in exactly two steps,
\* the coordinator's hold and the tick (an answer ageing to Bound), and each
\* takes back every card the friend holds in the same step: the hold's withdrawal
\* and the tick's rebalance. So a held or down friend holds no card at any
\* state, not "soon after".
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
\*   "enginesession" an engine friend is judged by the session's rule: a
\*                   check is asked of a friend with no session, nothing
\*                   answers, and she reads down while her engine beats (the
\*                   finding of 2026-10-07): EngineUpOnHerBeat
\*   "enginenostop"  an engine friend is up on her row's word alone, her
\*                   engine silent or never beating: EngineSilentIsDown
\*
\* Engines (docs/SPEC-FRIEND.md, "Presence per mode", 2026-10-08): the friends
\* whose presence is their engine's beat, not a session's answer. Such a
\* friend has no session: a runner of headless turns beats for her, every few
\* seconds, carrying her lanes (friend beat --working --width --running;
\* sprint.Beat.FromEngine), and the table reads her up while that beat is
\* younger than the bound and down, "engine silent for <t>", past it
\* (internal/sprint/presence.go, FriendEngineSilent). No check is ever asked
\* of her (Ping and Answer skip Engines). Per engine friend the world holds
\* engine (running, stopped: her runner alive or gone) and beatAge (her beat's
\* age, Bound before the first); a friend outside Engines has beatAge fixed at
\* Bound and engine running, both unread, so the cases without engines keep
\* their state spaces. Her harness, session and limit still move in the world
\* and are unread by her rule, as the app is by every rule.
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

CONSTANTS Friends, Cards, Bound, MaxEvents, Broken, Watched, Engines

ASSUME Bound >= 1 /\ MaxEvents \in Nat /\ Watched \subseteq Friends /\ Engines \subseteq Friends

VARIABLES harness, closedAge, session, limit, daemon, app, engine, beatAge,
          held, answered, answerAge, pending, holder, takenFrom, taken, events
vars == <<harness, closedAge, session, limit, daemon, app, engine, beatAge,
          held, answered, answerAge, pending, holder, takenFrom, taken, events>>
world == <<harness, closedAge, session, limit, daemon, app, engine, beatAge>>

Pool == "pool"
NoOne == "none"

TypeOK ==
  /\ harness \in [Friends -> {"running", "closed"}]
  /\ closedAge \in [Friends -> 0..Bound]
  /\ session \in [Friends -> {"answering", "silent"}]
  /\ limit \in [Friends -> {"none", "limited"}]
  /\ daemon \in [Friends -> {"beating"}]
  /\ app \in [Friends -> {"seen", "notseen"}]
  /\ engine \in [Friends -> {"running", "stopped"}]
  /\ beatAge \in [Friends -> 0..Bound]
  /\ held \in [Friends -> BOOLEAN]
  /\ answered \in [Friends -> BOOLEAN]
  /\ answerAge \in [Friends -> 0..Bound]
  /\ pending \in [Friends -> BOOLEAN]
  /\ holder \in [Cards -> Friends \cup {Pool}]
  /\ takenFrom \in [Cards -> Friends \cup {NoOne}]
  /\ taken \in [Cards -> BOOLEAN]
  /\ events \in 0..MaxEvents

\* The table's word over any held/answered/answerAge/beatAge, so a step can
\* read the word its own result shows. The witnesses let the beat or a stale
\* answer say up. An engine friend's word is her engine's beat under the
\* bound (the design); the witnesses judge her by the session's rule, or by
\* her row's word alone.
UpIn(f, ans, age, bage) ==
  IF f \in Engines THEN
    CASE Broken = "enginesession" -> ans[f] /\ age[f] < Bound
      [] Broken = "enginenostop"  -> TRUE
      [] OTHER                    -> bage[f] < Bound
  ELSE
    CASE Broken = "beatup"   -> daemon[f] = "beating"
      [] Broken = "appup"    -> app[f] = "seen"
      [] Broken = "noexpiry" -> ans[f]
      [] OTHER               -> ans[f] /\ age[f] < Bound
StatusIn(f, hd, ans, age, bage) ==
  IF hd[f] THEN "held" ELSE IF UpIn(f, ans, age, bage) THEN "up" ELSE "down"
Status(f) == StatusIn(f, held, answered, answerAge, beatAge)

CardsOf(f) == {c \in Cards : holder[c] = f}

\* Take back every card of a friend not up in the step's result: the hold's
\* withdrawal and the tick's rebalance. The witness "lazywithdraw" takes back
\* nothing here.
Lazy == Broken = "lazywithdraw"
TakeBack(hd, ans, age, bage) ==
  /\ holder' = [c \in Cards |->
                  IF ~Lazy /\ holder[c] \in Friends /\ StatusIn(holder[c], hd, ans, age, bage) # "up"
                    THEN Pool ELSE holder[c]]
  /\ takenFrom' = [c \in Cards |->
                     IF ~Lazy /\ holder[c] \in Friends /\ StatusIn(holder[c], hd, ans, age, bage) # "up"
                       THEN holder[c] ELSE takenFrom[c]]
  /\ taken' = [c \in Cards |->
                 IF ~Lazy /\ holder[c] \in Friends /\ StatusIn(holder[c], hd, ans, age, bage) # "up"
                   THEN FALSE ELSE taken[c]]

Init ==
  /\ harness = [f \in Friends |-> "running"]
  /\ closedAge = [f \in Friends |-> 0]
  /\ session = [f \in Friends |-> "answering"]
  /\ limit = [f \in Friends |-> "none"]
  /\ daemon = [f \in Friends |-> "beating"]
  /\ app \in {a \in [Friends -> {"seen", "notseen"}] : \A f \in Friends \ Watched : a[f] = "notseen"}
  /\ engine = [f \in Friends |-> "running"]
  /\ beatAge = [f \in Friends |-> Bound]
  /\ held = [f \in Friends |-> FALSE]
  /\ answered = [f \in Friends |-> FALSE]
  /\ answerAge = [f \in Friends |-> Bound]
  /\ pending = [f \in Friends |-> FALSE]
  /\ holder = [c \in Cards |-> Pool]
  /\ takenFrom = [c \in Cards |-> NoOne]
  /\ taken = [c \in Cards |-> FALSE]
  /\ events = 0

Up1(n) == IF n < Bound THEN n + 1 ELSE Bound

\* ------------------------------------------------------------- the world

\* The app closes or opens. A disruption (close, silence, limit, hold) spends
\* one of MaxEvents; a recovery spends nothing.
Close(f) ==
  /\ harness[f] = "running" /\ events < MaxEvents
  /\ harness' = [harness EXCEPT ![f] = "closed"]
  /\ closedAge' = [closedAge EXCEPT ![f] = 0]
  /\ events' = events + 1
  /\ UNCHANGED <<session, limit, daemon, app, engine, beatAge, held, answered, answerAge, pending, holder, takenFrom, taken>>
Open(f) ==
  /\ harness[f] = "closed"
  /\ harness' = [harness EXCEPT ![f] = "running"]
  /\ closedAge' = [closedAge EXCEPT ![f] = 0]
  /\ UNCHANGED <<session, limit, daemon, app, engine, beatAge, held, answered, answerAge, pending, holder, takenFrom, taken, events>>

\* The session goes silent, or takes turns again.
Silence(f) ==
  /\ session[f] = "answering" /\ events < MaxEvents
  /\ session' = [session EXCEPT ![f] = "silent"]
  /\ events' = events + 1
  /\ UNCHANGED <<harness, closedAge, limit, daemon, app, engine, beatAge, held, answered, answerAge, pending, holder, takenFrom, taken>>
Resume(f) ==
  /\ session[f] = "silent"
  /\ session' = [session EXCEPT ![f] = "answering"]
  /\ UNCHANGED <<harness, closedAge, limit, daemon, app, engine, beatAge, held, answered, answerAge, pending, holder, takenFrom, taken, events>>

\* The process-table look moves on its own: an app opens or closes with no
\* tie to the session (a headless run, an app left open). Neither a
\* disruption nor a recovery: it spends nothing and nothing waits for it.
AppMoves(f) ==
  /\ f \in Watched
  /\ app' = [app EXCEPT ![f] = IF app[f] = "seen" THEN "notseen" ELSE "seen"]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, engine, beatAge, held, answered, answerAge, pending, holder, takenFrom, taken, events>>

\* The provider's limit hits, and later resets.
LimitHit(f) ==
  /\ limit[f] = "none" /\ events < MaxEvents
  /\ limit' = [limit EXCEPT ![f] = "limited"]
  /\ events' = events + 1
  /\ UNCHANGED <<harness, closedAge, session, daemon, app, engine, beatAge, held, answered, answerAge, pending, holder, takenFrom, taken>>
LimitReset(f) ==
  /\ limit[f] = "limited"
  /\ limit' = [limit EXCEPT ![f] = "none"]
  /\ UNCHANGED <<harness, closedAge, session, daemon, app, engine, beatAge, held, answered, answerAge, pending, holder, takenFrom, taken, events>>

\* ------------------------------------------------------------- the table

\* The coordinator pings: a fresh nonce is open for the friend
\* (the daemon's session check, docs/SPEC-FRIEND.md "Presence").
\* The process table is never read here; the witness "appholds" lets the
\* app not seen hold the beat, and with it the check, back. An engine friend
\* has no session and is never asked; the witness "enginesession" asks her.
Ping(f) ==
  /\ ~pending[f]
  /\ f \notin Engines \/ Broken = "enginesession"
  /\ Broken # "appholds" \/ app[f] = "seen"
  /\ pending' = [pending EXCEPT ![f] = TRUE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, engine, beatAge, held, answered, answerAge, holder, takenFrom, taken, events>>

\* The session answers the open nonce: only a running harness, a session
\* that takes turns and a provider not limiting it can (the witness
\* "closedanswers" counts an answer from a closed harness). The answer's age
\* starts again.
CanAnswer(f) ==
  /\ f \notin Engines
  /\ harness[f] = "running" \/ Broken = "closedanswers"
  /\ session[f] = "answering"
  /\ limit[f] = "none"
Answer(f) ==
  /\ pending[f] /\ CanAnswer(f)
  /\ pending' = [pending EXCEPT ![f] = FALSE]
  /\ answered' = [answered EXCEPT ![f] = TRUE]
  /\ answerAge' = [answerAge EXCEPT ![f] = 0]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, engine, beatAge, held, holder, takenFrom, taken, events>>

\* The coordinator holds a friend, and the hold takes back the friend's cards
\* in the same step; or releases the friend.
Hold(f) ==
  /\ ~held[f] /\ events < MaxEvents
  /\ held' = [held EXCEPT ![f] = TRUE]
  /\ TakeBack(held', answered, answerAge, beatAge)
  /\ events' = events + 1
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, engine, beatAge, answered, answerAge, pending>>
Release(f) ==
  /\ held[f]
  /\ held' = [held EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, engine, beatAge, answered, answerAge, pending, holder, takenFrom, taken, events>>

\* Time passes: every age one older (an engine friend's beat too), and the
\* tick's rebalance takes back the cards of every friend no longer up, in the
\* same step.
Tick ==
  /\ answerAge' = [f \in Friends |-> Up1(answerAge[f])]
  /\ closedAge' = [f \in Friends |-> IF harness[f] = "closed" THEN Up1(closedAge[f]) ELSE 0]
  /\ beatAge' = [f \in Friends |-> IF f \in Engines THEN Up1(beatAge[f]) ELSE beatAge[f]]
  /\ TakeBack(held, answered, answerAge', beatAge')
  /\ UNCHANGED <<harness, session, limit, daemon, app, engine, held, answered, pending, events>>

\* ------------------------------------------------------------- the engine

\* An engine friend's engine beats (her runner's friend beat, carrying her
\* lanes), while it runs: her beat's age starts again, and the table reads
\* her up on it. Only Engines have one.
EngineBeats(f) ==
  /\ f \in Engines /\ engine[f] = "running"
  /\ beatAge' = [beatAge EXCEPT ![f] = 0]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, engine, held, answered, answerAge, pending, holder, takenFrom, taken, events>>

\* Her engine stops (a disruption) and beats no more; later it starts again.
\* Nothing but the ticks moves her word: she is down once her beat is Bound
\* old (EngineSilentIsDown), never at the stop itself, which the table cannot
\* see.
EngineStop(f) ==
  /\ f \in Engines /\ engine[f] = "running" /\ events < MaxEvents
  /\ engine' = [engine EXCEPT ![f] = "stopped"]
  /\ events' = events + 1
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, beatAge, held, answered, answerAge, pending, holder, takenFrom, taken>>
EngineStart(f) ==
  /\ f \in Engines /\ engine[f] = "stopped"
  /\ engine' = [engine EXCEPT ![f] = "running"]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, beatAge, held, answered, answerAge, pending, holder, takenFrom, taken, events>>

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
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, engine, beatAge, held, answered, answerAge, pending, takenFrom, taken, events>>

\* A card ready on a friend's row is taken into working: her take, or the
\* daemon's through the server. Admitted by Status alone; the witness reads a
\* control card status her row never carries, so admits nothing.
TakeAdmits(f) == IF Broken = "ctlstatus" THEN FALSE ELSE Status(f) = "up"
Take(c) ==
  /\ holder[c] \in Friends /\ ~taken[c]
  /\ TakeAdmits(holder[c])
  /\ taken' = [taken EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, engine, beatAge, held, answered, answerAge, pending, holder, takenFrom, events>>

\* Only under "lazywithdraw": the take-back as a step of its own, after the
\* hold or the tick that made the friend not up.
Withdraw(f) ==
  /\ Lazy
  /\ Status(f) # "up" /\ CardsOf(f) # {}
  /\ holder' = [c \in Cards |-> IF holder[c] = f THEN Pool ELSE holder[c]]
  /\ takenFrom' = [c \in Cards |-> IF holder[c] = f THEN f ELSE takenFrom[c]]
  /\ taken' = [c \in Cards |-> IF holder[c] = f THEN FALSE ELSE taken[c]]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, engine, beatAge, held, answered, answerAge, pending, events>>

Next ==
  \/ Tick
  \/ \E f \in Friends :
       \/ Close(f) \/ Open(f) \/ AppMoves(f) \/ Silence(f) \/ Resume(f) \/ LimitHit(f) \/ LimitReset(f)
       \/ Ping(f) \/ Answer(f) \/ Hold(f) \/ Release(f) \/ Withdraw(f)
       \/ EngineBeats(f) \/ EngineStop(f) \/ EngineStart(f)
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
\* keeps pinging and a session able to answer does; and a card dealable
\* infinitely often is dealt (strong fairness: a friend's up comes and goes
\* with the ticks).
SpecLive ==
  /\ Spec /\ WF_vars(Tick)
  /\ \A f \in Friends :
       /\ WF_vars(Open(f)) /\ WF_vars(Resume(f)) /\ WF_vars(LimitReset(f)) /\ WF_vars(Release(f))
       /\ WF_vars(Ping(f)) /\ WF_vars(Answer(f))
       /\ WF_vars(EngineBeats(f)) /\ WF_vars(EngineStart(f))
  /\ \A c \in Cards : SF_vars(\E g \in Friends : Deal(c, g)) /\ WF_vars(Take(c))

\* ---------------------------------------------------------------- the rules

\* A friend shown up has a session answer younger than the bound. The rules
\* of the session's evidence read Friends \ Engines: an engine friend has no
\* session, and her rules follow at the end.
UpHasFreshAnswer == \A f \in Friends \ Engines : Status(f) = "up" => answered[f] /\ answerAge[f] < Bound

\* A friend shown up has a running harness, or one closed less than the bound
\* ago: the table cannot see the app close, only the answers stop, so this
\* is the most a table can promise (UpHasRunningHarnessNow, the rule read
\* literally, fails: the finding case MCFriendPresenceFindingRunningNow).
UpHasRunningHarness == \A f \in Friends \ Engines : Status(f) = "up" => harness[f] = "running" \/ closedAge[f] < Bound
UpHasRunningHarnessNow == \A f \in Friends \ Engines : Status(f) = "up" => harness[f] = "running"

\* A closed harness is shown down within the bound: the bounded form of
\* ClosedShownDown, a state invariant on the ages.
ClosedDownWithinBound == \A f \in Friends \ Engines : harness[f] = "closed" /\ closedAge[f] = Bound => Status(f) # "up"

\* A held or down friend holds no card.
NoCardOffUp == \A f \in Friends : Status(f) # "up" => CardsOf(f) = {}

\* The daemon's beat alone never makes a friend up: a friend whose session
\* never answered is not up, however the daemon beats. An engine friend is
\* the one exception, and her beat is her engine's, not a daemon's
\* (EngineUpOnHerBeat).
BeatAloneNeverUp == \A f \in Friends \ Engines : ~answered[f] => Status(f) # "up"

\* The liveness. A closed harness is shown down (or opens again) on a clock
\* that keeps ticking (SpecClosed or SpecLive).
ClosedShownDown == \A f \in Friends \ Engines : harness[f] = "closed" ~> (Status(f) # "up" \/ harness[f] = "running")

\* A session that answers is shown up again and again, whatever the process
\* table says: the app may stay unseen for ever (AppMoves has no fairness; a friend outside Watched never has it)
\* and the friend still comes up on her session's answers (SpecLive).
SessionShownUp == \A f \in Friends \ Engines : []<>(Status(f) = "up")

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

\* ------------------------------------------------ the engine friends' rules

\* An engine friend not held is up on her engine's beat younger than the
\* bound: no session answer is asked of her (the witness "enginesession"
\* judges her by one, and she reads down while her engine beats).
EngineUpOnHerBeat == \A f \in Engines : ~held[f] /\ beatAge[f] < Bound => Status(f) = "up"

\* An engine friend whose engine has been silent for the bound, or never
\* beat, is not up (the witness "enginenostop" reads her row's word alone).
EngineSilentIsDown == \A f \in Engines : beatAge[f] = Bound => Status(f) # "up"

\* No session check is ever open for an engine friend.
EngineNeverAsked == \A f \in Engines : ~pending[f]

\* The liveness. An engine that runs is shown up again and again (SpecLive:
\* the engine beats, the hold is released, a stopped engine starts again).
EngineShownUp == \A f \in Engines : []<>(Status(f) = "up")

\* A stopped engine is shown down, or starts again, on a clock that keeps
\* ticking (SpecClosed or SpecLive).
EngineStoppedShownDown == \A f \in Engines : engine[f] = "stopped" ~> (Status(f) # "up" \/ engine[f] = "running")

=============================================================================
