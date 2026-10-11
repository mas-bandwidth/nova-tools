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
\* Two halves. The daemon half (the session check: Heard, Answer, Ping, the
\* cadence ProveEvery/ReaskAfter/Read and Proven, and Finish) models
\* pkg/friend/presence.go. The table half (Hold, TakeBack, Deal, Take) models
\* nova-sprint code now (internal/sprint/presence.go FriendStatus,
\* internal/sprint/steps_work.go takeSeat) and should move beside SeatHealth
\* in nova-sprint; it is kept here as a follow-up, not moved by this card.
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
\* the session's last line (heard, heardAge), the age of the last finish
\* (finish, finishAge), the push proof (proven) and whether the session read
\* the open check (read), an open nonce (pending), and holder, where each
\* card is (a friend or the pool, with takenFrom the friend a card was last
\* taken back from). Ghosts: closedAge (how long the harness has been
\* closed), events (the disruptions spent).
\*
\* Bound is the longest a friend stays up with no proof from the session:
\* in pkg/friend/presence.go, SessionQuiet (the quiet before a check goes in)
\* plus SessionBound (the wait for its answer). The check cadence in the code
\* (ProveEvery while up, SessionQuiet while down, ReaskAfter while an unread
\* check is open) is below this module's single time unit; a check goes in
\* whenever none is open, and Read names the pile-up guard.
\*
\* Time is ages, not a clock: every age counts up one a tick and stops at
\* Bound, which means "Bound or older". So the clock never ends, Tick is
\* always enabled, and the liveness properties are checked on an infinite
\* clock over finitely many states.
\*
\* The table's word is derived at every read (Status): held is the hold
\* alone; up is fresh evidence, a session line (Heard or an Answer) or a
\* Finish younger than Bound; down is the rest. The daemon's beat is not
\* read. A friend leaves up when her evidence ages to Bound, and the hold and
\* the tick take back every card the friend holds in the same step: the hold's
\* withdrawal and the tick's rebalance. So a held or down friend holds no card
\* at any state, not "soon after".
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* each caught by one property below:
\*   "beatup"        the daemon's beat makes the friend up: BeatAloneNeverUp
\*   "noexpiry"      a session line never ages: UpHasFreshEvidence, and on
\*                   SpecClosed ClosedShownDown (a closed harness shown up
\*                   for ever)
\*   "finishforever" a finish never ages: UpHasFreshEvidence
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
\*   "oneshotsession" a one-shot friend answers only from her session (the
\*                   batch rule), so a silent or full session keeps her down:
\*                   OneShotNeverDownBySession
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

CONSTANTS Friends, Cards, Bound, MaxEvents, Broken, Watched, OneShot

ASSUME Bound >= 1 /\ MaxEvents \in Nat /\ Watched \subseteq Friends
ASSUME OneShot \subseteq Friends

VARIABLES harness, closedAge, session, limit, daemon, app,
          held, heard, heardAge, finish, finishAge, proven, read,
          pending, holder, takenFrom, taken, events
vars == <<harness, closedAge, session, limit, daemon, app,
          held, heard, heardAge, finish, finishAge, proven, read,
          pending, holder, takenFrom, taken, events>>
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
  /\ heard \in [Friends -> BOOLEAN]
  /\ heardAge \in [Friends -> 0..Bound]
  /\ finish \in [Friends -> BOOLEAN]
  /\ finishAge \in [Friends -> 0..Bound]
  /\ proven \in [Friends -> BOOLEAN]
  /\ read \in [Friends -> BOOLEAN]
  /\ pending \in [Friends -> BOOLEAN]
  /\ holder \in [Cards -> Friends \cup {Pool}]
  /\ takenFrom \in [Cards -> Friends \cup {NoOne}]
  /\ taken \in [Cards -> BOOLEAN]
  /\ events \in 0..MaxEvents

\* The table's word over any held/evidence, so a step can read the word its
\* own result shows. The witnesses let the beat, the app, a stale session
\* line or a stale finish say up.
UpIn(f, h, ha, fin, fa) ==
  CASE Broken = "beatup"        -> daemon[f] = "beating"
    [] Broken = "appup"         -> app[f] = "seen"
    [] Broken = "noexpiry"      -> h[f]
    [] Broken = "finishforever" -> fin[f]
    [] OTHER                    -> (h[f] /\ ha[f] < Bound) \/ (fin[f] /\ fa[f] < Bound)
StatusIn(f, hd, h, ha, fin, fa) ==
  IF hd[f] THEN "held" ELSE IF UpIn(f, h, ha, fin, fa) THEN "up" ELSE "down"
Status(f) == StatusIn(f, held, heard, heardAge, finish, finishAge)

CardsOf(f) == {c \in Cards : holder[c] = f}

\* Take back every card of a friend not up in the step's result: the hold's
\* withdrawal and the tick's rebalance. The witness "lazywithdraw" takes back
\* nothing here.
Lazy == Broken = "lazywithdraw"
TakeBack(hd, h, ha, fin, fa) ==
  /\ holder' = [c \in Cards |->
                  IF ~Lazy /\ holder[c] \in Friends /\ StatusIn(holder[c], hd, h, ha, fin, fa) # "up"
                    THEN Pool ELSE holder[c]]
  /\ takenFrom' = [c \in Cards |->
                     IF ~Lazy /\ holder[c] \in Friends /\ StatusIn(holder[c], hd, h, ha, fin, fa) # "up"
                       THEN holder[c] ELSE takenFrom[c]]
  /\ taken' = [c \in Cards |->
                 IF ~Lazy /\ holder[c] \in Friends /\ StatusIn(holder[c], hd, h, ha, fin, fa) # "up"
                   THEN FALSE ELSE taken[c]]

Init ==
  /\ harness = [f \in Friends |-> "running"]
  /\ closedAge = [f \in Friends |-> 0]
  /\ session = [f \in Friends |-> "answering"]
  /\ limit = [f \in Friends |-> "none"]
  /\ daemon = [f \in Friends |-> "beating"]
  /\ app \in {a \in [Friends -> {"seen", "notseen"}] : \A f \in Friends \ Watched : a[f] = "notseen"}
  /\ held = [f \in Friends |-> FALSE]
  /\ heard = [f \in Friends |-> FALSE]
  /\ heardAge = [f \in Friends |-> Bound]
  /\ finish = [f \in Friends |-> FALSE]
  /\ finishAge = [f \in Friends |-> Bound]
  /\ proven = [f \in Friends |-> FALSE]
  /\ read = [f \in Friends |-> FALSE]
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
  /\ UNCHANGED <<session, limit, daemon, app, held, heard, heardAge, finish, finishAge, proven, read, pending, holder, takenFrom, taken>>
Open(f) ==
  /\ harness[f] = "closed"
  /\ harness' = [harness EXCEPT ![f] = "running"]
  /\ closedAge' = [closedAge EXCEPT ![f] = 0]
  /\ UNCHANGED <<session, limit, daemon, app, held, heard, heardAge, finish, finishAge, proven, read, pending, holder, takenFrom, taken, events>>

\* The session goes silent, or takes turns again.
Silence(f) ==
  /\ session[f] = "answering" /\ events < MaxEvents
  /\ session' = [session EXCEPT ![f] = "silent"]
  /\ events' = events + 1
  /\ UNCHANGED <<harness, closedAge, limit, daemon, app, held, heard, heardAge, finish, finishAge, proven, read, pending, holder, takenFrom, taken>>
Resume(f) ==
  /\ session[f] = "silent"
  /\ session' = [session EXCEPT ![f] = "answering"]
  /\ UNCHANGED <<harness, closedAge, limit, daemon, app, held, heard, heardAge, finish, finishAge, proven, read, pending, holder, takenFrom, taken, events>>

\* The process-table look moves on its own: an app opens or closes with no
\* tie to the session (a headless run, an app left open). Neither a
\* disruption nor a recovery: it spends nothing and nothing waits for it.
AppMoves(f) ==
  /\ f \in Watched
  /\ app' = [app EXCEPT ![f] = IF app[f] = "seen" THEN "notseen" ELSE "seen"]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, held, heard, heardAge, finish, finishAge, proven, read, pending, holder, takenFrom, taken, events>>

\* The provider's limit hits, and later resets.
LimitHit(f) ==
  /\ limit[f] = "none" /\ events < MaxEvents
  /\ limit' = [limit EXCEPT ![f] = "limited"]
  /\ events' = events + 1
  /\ UNCHANGED <<harness, closedAge, session, daemon, app, held, heard, heardAge, finish, finishAge, proven, read, pending, holder, takenFrom, taken>>
LimitReset(f) ==
  /\ limit[f] = "limited"
  /\ limit' = [limit EXCEPT ![f] = "none"]
  /\ UNCHANGED <<harness, closedAge, session, daemon, app, held, heard, heardAge, finish, finishAge, proven, read, pending, holder, takenFrom, taken, events>>

\* ------------------------------------------------------------- the table

\* The coordinator pings: a fresh nonce is open for the friend
\* (the daemon's session check, docs/SPEC-FRIEND.md "Presence").
\* The process table is never read here; the witness "appholds" lets the
\* app not seen hold the beat, and with it the check, back.
Ping(f) ==
  /\ ~pending[f]
  /\ Broken # "appholds" \/ app[f] = "seen"
  /\ pending' = [pending EXCEPT ![f] = TRUE]
  /\ read' = [read EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, heard, heardAge, finish, finishAge, proven, holder, takenFrom, taken, events>>

\* The session answers the open nonce: only a running harness, a session
\* that takes turns and a provider not limiting it can (the witness
\* "closedanswers" counts an answer from a closed harness). A one-shot friend
\* has no long-lived session by design (docs/SPEC-FRIEND.md, presence: her
\* check is a tiny fresh turn per check), so her answer does not depend on
\* session[f]; the witness "oneshotsession" makes it depend on the session
\* again, as a batch friend's does. The answer's age starts again.
SessionNeeded(f) == f \notin OneShot \/ Broken = "oneshotsession"
CanAnswer(f) ==
  /\ harness[f] = "running" \/ Broken = "closedanswers"
  /\ session[f] = "answering" \/ ~SessionNeeded(f)
  /\ limit[f] = "none"
Answer(f) ==
  /\ pending[f] /\ CanAnswer(f)
  /\ pending' = [pending EXCEPT ![f] = FALSE]
  /\ heard' = [heard EXCEPT ![f] = TRUE]
  /\ heardAge' = [heardAge EXCEPT ![f] = 0]
  /\ proven' = [proven EXCEPT ![f] = TRUE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, finish, finishAge, read, holder, takenFrom, taken, events>>

\* The session writes any bus line, not the answer to the open nonce: that
\* line is evidence, so the friend is up (pkg/friend/presence.go Heard). It
\* also proves the push, like an answer does. A silent or limited session
\* writes nothing; the line's age starts again.
Heard(f) ==
  /\ harness[f] = "running"
  /\ session[f] = "answering"
  /\ limit[f] = "none"
  /\ heard' = [heard EXCEPT ![f] = TRUE]
  /\ heardAge' = [heardAge EXCEPT ![f] = 0]
  /\ proven' = [proven EXCEPT ![f] = TRUE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, finish, finishAge, read, pending, holder, takenFrom, taken, events>>

\* The session read the open check (its headless turn ended, or the adapter
\* saw it take the check): a read check is asked again on the cadence, never
\* piled up (pkg/friend/presence.go Read).
ReadCheck(f) ==
  /\ pending[f] /\ harness[f] = "running"
  /\ read' = [read EXCEPT ![f] = TRUE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, heard, heardAge, finish, finishAge, proven, pending, holder, takenFrom, taken, events>>

\* The sprint server counts a recent finish as evidence the friend is up
\* (nova-sprint internal/sprint/presence.go FriendStatus): a finished card's
\* run. The finish's age starts again; a finish older than Bound proves
\* nothing.
Finish(f) ==
  /\ harness[f] = "running"
  /\ ~finish[f] \/ finishAge[f] # 0
  /\ finish' = [finish EXCEPT ![f] = TRUE]
  /\ finishAge' = [finishAge EXCEPT ![f] = 0]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, heard, heardAge, proven, read, pending, holder, takenFrom, taken, events>>

\* The coordinator holds a friend, and the hold takes back the friend's cards
\* in the same step; or releases the friend.
Hold(f) ==
  /\ ~held[f] /\ events < MaxEvents
  /\ held' = [held EXCEPT ![f] = TRUE]
  /\ TakeBack(held', heard, heardAge, finish, finishAge)
  /\ events' = events + 1
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, heard, heardAge, finish, finishAge, proven, read, pending>>
Release(f) ==
  /\ held[f]
  /\ held' = [held EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, heard, heardAge, finish, finishAge, proven, read, pending, holder, takenFrom, taken, events>>

\* Time passes: every age one older, and the tick's rebalance takes back the
\* cards of every friend no longer up, in the same step.
Tick ==
  /\ heardAge' = [f \in Friends |-> Up1(heardAge[f])]
  /\ finishAge' = [f \in Friends |-> Up1(finishAge[f])]
  /\ closedAge' = [f \in Friends |-> IF harness[f] = "closed" THEN Up1(closedAge[f]) ELSE 0]
  /\ TakeBack(held, heard, heardAge', finish, finishAge')
  /\ UNCHANGED <<harness, session, limit, daemon, app, held, heard, finish, proven, read, pending, events>>

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
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, heard, heardAge, finish, finishAge, proven, read, pending, takenFrom, taken, events>>

\* A card ready on a friend's row is taken into working: her take, or the
\* daemon's through the server. Admitted by Status alone; the witness reads a
\* control card status her row never carries, so admits nothing.
TakeAdmits(f) == IF Broken = "ctlstatus" THEN FALSE ELSE Status(f) = "up"
Take(c) ==
  /\ holder[c] \in Friends /\ ~taken[c]
  /\ TakeAdmits(holder[c])
  /\ taken' = [taken EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, heard, heardAge, finish, finishAge, proven, read, pending, holder, takenFrom, events>>

\* Only under "lazywithdraw": the take-back as a step of its own, after the
\* hold or the tick that made the friend not up.
Withdraw(f) ==
  /\ Lazy
  /\ Status(f) # "up" /\ CardsOf(f) # {}
  /\ holder' = [c \in Cards |-> IF holder[c] = f THEN Pool ELSE holder[c]]
  /\ takenFrom' = [c \in Cards |-> IF holder[c] = f THEN f ELSE takenFrom[c]]
  /\ taken' = [c \in Cards |-> IF holder[c] = f THEN FALSE ELSE taken[c]]
  /\ UNCHANGED <<harness, closedAge, session, limit, daemon, app, held, heard, heardAge, finish, finishAge, proven, read, pending, events>>

Next ==
  \/ Tick
  \/ \E f \in Friends :
       \/ Close(f) \/ Open(f) \/ AppMoves(f) \/ Silence(f) \/ Resume(f) \/ LimitHit(f) \/ LimitReset(f)
       \/ Ping(f) \/ Answer(f) \/ Heard(f) \/ ReadCheck(f) \/ Finish(f) \/ Hold(f) \/ Release(f) \/ Withdraw(f)
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
  /\ \A c \in Cards : SF_vars(\E g \in Friends : Deal(c, g)) /\ WF_vars(Take(c))

\* ---------------------------------------------------------------- the rules

\* A friend shown up has fresh evidence: a session line (Heard or Answer) or
\* a Finish younger than the bound.
UpHasFreshEvidence ==
  \A f \in Friends : Status(f) = "up" =>
    (heard[f] /\ heardAge[f] < Bound) \/ (finish[f] /\ finishAge[f] < Bound)

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

\* The daemon's beat alone never makes a friend up: a friend with no session
\* line and no finish is not up, however the daemon beats.
BeatAloneNeverUp == \A f \in Friends : ~heard[f] /\ ~finish[f] => Status(f) # "up"

\* A one-shot friend with a running daemon is never down because of her
\* session: when she is down, her daemon running, a check pending and her
\* session silent, the silence is not what blocks the answer (CanAnswer does
\* not read session[f] for her). The witness "oneshotsession" answers only
\* from the session, so a silent one-shot friend is down with no answer open
\* to her and this fails.
OneShotNeverDownBySession ==
  \A f \in OneShot : Status(f) = "down" =>
    ~(harness[f] = "running" /\ limit[f] = "none" /\ ~held[f]
      /\ session[f] = "silent" /\ pending[f] /\ ~CanAnswer(f))

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
