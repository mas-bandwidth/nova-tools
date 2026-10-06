----------------------------- MODULE FriendCard -----------------------------
\* A friend's card from the deal to its return, and the friend's own liveness
\* under it (docs/SPEC-SPRINT.md, "A friend's card's lifecycle"; card
\* friend-card-lifecycle-tla). The owner, 2026-10-05: "we need to make sure that
\* friends are up, they are not deaf, they are actually doing work, when they
\* finish work they notify you, when the work is finished it is returned to
\* you. And the whole time the dashboard needs to be accurate". The chain,
\* bottom up, is the layers this module holds:
\*   1 up          the friend's daemon process alive (her beat)
\*   2 hears       her session, not the daemon, answers a wake ping
\*   3 delivered   a dealt card's delivery accepted by the session
\*   4 started     her start receipt; only then is the card working
\*   5 progressing a live run with recent activity, else stalled
\*   6 finished    her report becomes a finish; a run that dies still finishes
\*   7 returned    the finish reaches review or merge
\*   8 balanced    the deal and the level read who hears and has room;
\*                 a friend coming back is brought up
\*   9 shown       the dashboard row is the lowest failing layer
\* Friend.tla is the daemon's own machine (the challenge and its nonce),
\* FriendPresence.tla the table's up/held/down and its take-back, and
\* StallLadder.tla the stall ladder's rungs; this module is the card's life
\* across all of them, at the grain of evidence and bounds.
\*
\* Time is ages, as in FriendPresence: every evidence clock counts up one a
\* tick and stops at its bound, which means "the bound or older". The tick is
\* always enabled, so the clock never ends and the liveness is checked on an
\* infinite clock over finitely many states. Bound is one bound for every
\* evidence clock of the card; the table in docs/SPEC-SPRINT.md names the
\* code's own bound for each transition.
\*
\* Gaps is the set of the code's gaps, each read in the code by hand and each
\* the counterexample of one property; Gaps = {} is the design. Read again on
\* 2026-10-06 at base sprint/mechanical-2026-10-02 ba3d867c0 (card
\* friend-card-lifecycle-tla-b; first read 2026-10-05 at 3cac64dbb). Each gap
\* names the card that closes it:
\*   "beatup"         presence-from-session-only, LANDED (c5ceb5257): a friend
\*                    never observed was up on her daemon's beat alone; today
\*                    she is up only on her session's evidence (internal/
\*                    sprint/presence.go FriendEvidence). Kept as a reversed
\*                    witness: DealOnlyToHearing
\*   "finishup"       wake-ping-every: a card of hers finished keeps her up
\*                    for FriendFinishWindow (30m), past the pong's
\*                    FriendPongWindow (10m) (internal/sprint/presence.go
\*                    FriendEvidence), and a wake pass that finds her deaf
\*                    sends one bus note and writes nothing to the table
\*                    (cmd/nova-friend/wakeping.go): a session that can take
\*                    no turn after its last finish counts as hearing Linger
\*                    ticks past the bound: DeafShownWithinBound
\*   "pingsuponly"    wake-ping-every: the wake loop pings only the friends
\*                    the table shows up (internal/friend/wakeping.go
\*                    WakeTargets), so a friend down (never observed, or her
\*                    evidence aged out) is never asked again; and no code
\*                    writes an answered pong as friend health --state up (the
\*                    coordinator writes it by hand): AbleFriendUp, under
\*                    SpecLive
\*   "fileisdelivery" friend-delivery-visible: a card is delivered when friend
\*                    sync writes inbox/<job>/BRIEF.md (cmd/nova-sprint/
\*                    friendcards.go friendCardsOf), whatever the session; a
\*                    session the provider refuses is said on the bus once
\*                    (internal/friend/daemon.go tellBroken) and never on the
\*                    table: DeliveredOnlyIntoLiveSession
\*   "columnworking"  friend-working-means-started: the deal moves a card
\*                    ready -> working on her row while she has a lane free
\*                    (internal/sprint/friend_deal.go friendDeal) and the
\*                    friends table counts that column as working
\*                    (cmd/nova-sprint/reads.go): WorkingOnlyAfterStart
\*   "downkeeps"      pr-friend-stall-complete: a friend down keeps her cards
\*                    on her row (cmd/nova-sprint/friends.go, "her cards stay
\*                    on her row"); only the hold and the stall ladder's rung 4
\*                    (internal/sprint/friend_stall.go) take them back, the
\*                    ladder at its own bound: NoUnstartedCardOffUp
\*   "lanes"          lane-end-finishes-the-card, LANDED for a daemon that
\*                    runs (462846817, internal/friend/lane_end.go): a lane's
\*                    end, or a daemon starting up over a started card, sends
\*                    the failed finish. The rest is open: only her own daemon
\*                    finishes a dead run, so while her daemon is down the card
\*                    stays working (the coordinator raises the WorkDeadline
\*                    judgment, internal/sprint/steps_tick.go, and finishes
\*                    nothing): DeadRunFinishedWithinBound
\*   "nobringup"      friend-back-up-automatic: a session the provider refused
\*                    BrokenAfter turns in a row is broken until a person renews
\*                    it and restarts the daemon (internal/friend/daemon.go):
\*                    AbleFriendUp, under SpecLive
\*   "dashupdown"     dashboard-friend-lowest-layer: the friends table's word is
\*                    up, held or down (sprint.FriendEvidence, its evidence
\*                    beside it) and up says every layer holds:
\*                    DashNeverOkWhileFailing

EXTENDS Naturals, FiniteSets

CONSTANTS Friends, Cards, Width, Bound, Linger, MaxEvents, Gaps

ASSUME Width >= 1 /\ Bound >= 1 /\ Linger \in Nat /\ MaxEvents \in Nat

Gap(g) == g \in Gaps

None == "none"

\* The card's states. pool is ready on the sprint with no friend: never dealt,
\* or taken back. gone is a started card whose run ended with no report.
CardStates == {"pool", "dealt", "delivered", "started", "gone", "finished", "returned"}
Unstarted == {"dealt", "delivered"}
OnFriend == {"dealt", "delivered", "started", "gone"}
Aging == {"dealt", "delivered", "started", "gone", "finished"}

\* The longest the session's evidence counts as hearing: Bound, or under the
\* gap "finishup" Bound + Linger when the evidence was a finish (the finish
\* window past the pong window); the range of heardAge.
HearBound == IF Gap("finishup") THEN Bound + Linger ELSE Bound

VARIABLES
  \* the world, per friend
  daemon,    \* "up" or "down": her nova-friend daemon process
  session,   \* "live" or "archived": her session can take a turn, or not
  credit,    \* "ok" or "out": her provider's allowance
  \* the machine's evidence, per friend
  heardAge,  \* ticks since her session's last evidence, a wake ping answered or a finish (HearBound: never, or too long)
  byFinish,  \* that evidence was a finish (set only under the gap "finishup"; FALSE otherwise)
  observed,  \* the coordinator has had one answer from her session (friend health)
  refused,   \* the daemon's delivery into her session was refused (visible)
  held,      \* the coordinator's hold (friend down)
  \* per card
  st,        \* the card's state, CardStates
  who,       \* the friend it is on, None in the pool
  cage,      \* the card's evidence clock: ticks since its last state change or activity
  \* ghosts
  deafAge,   \* ticks her session has been unable to answer since its last answer
  events     \* the disruptions spent (reboot, archive, credit out, hold)

friendVars == <<daemon, session, credit, heardAge, byFinish, observed, refused, held, deafAge>>
cardVars == <<st, who, cage>>
vars == <<daemon, session, credit, heardAge, byFinish, observed, refused, held, st, who, cage, deafAge, events>>

TypeOK ==
  /\ daemon \in [Friends -> {"up", "down"}]
  /\ session \in [Friends -> {"live", "archived"}]
  /\ credit \in [Friends -> {"ok", "out"}]
  /\ heardAge \in [Friends -> 0..HearBound]
  /\ byFinish \in [Friends -> BOOLEAN]
  /\ observed \in [Friends -> BOOLEAN]
  /\ refused \in [Friends -> BOOLEAN]
  /\ held \in [Friends -> BOOLEAN]
  /\ st \in [Cards -> CardStates]
  /\ who \in [Cards -> Friends \cup {None}]
  /\ cage \in [Cards -> 0..Bound]
  /\ deafAge \in [Friends -> 0..Bound]
  /\ events \in 0..MaxEvents

Up1(n, b) == IF n < b THEN n + 1 ELSE b

\* ------------------------------------------------------------ derived words

\* Her session can take a turn: the daemon that hands it in runs, the session
\* is live and the provider has allowance.
CanTakeTurn(f) == daemon[f] = "up" /\ session[f] = "live" /\ credit[f] = "ok"

\* Layer 2: her session's evidence (a wake ping answered, or a finish) is
\* within the bound. Under the gap "finishup" a finish counts Linger ticks
\* longer.
HearsIn(f, ha) ==
  \/ ha[f] < Bound
  \/ Gap("finishup") /\ byFinish[f] /\ ha[f] < HearBound
Hears(f) == HearsIn(f, heardAge)

\* The table's word over a given heardAge, so a step can read its own result:
\* held is the hold alone; up is a session that hears (and, under the gap
\* "beatup", a daemon's beat for a friend never observed); down is the rest.
StatusIn(f, ha) ==
  IF held[f] THEN "held"
  ELSE IF HearsIn(f, ha) \/ (Gap("beatup") /\ ~observed[f] /\ daemon[f] = "up") THEN "up"
  ELSE "down"
Status(f) == StatusIn(f, heardAge)

CardsOf(f) == {c \in Cards : who[c] = f}
Load(f) == Cardinality({c \in CardsOf(f) : st[c] \in OnFriend})

\* A card the friends table counts working: started (the design), or every
\* card on her row's Working column under the gap "columnworking".
ShownWorking(c) ==
  IF Gap("columnworking") THEN st[c] \in OnFriend ELSE st[c] = "started"

\* The alarm the machine raises for a card past its bound in a state it must
\* leave: stalled (started with no activity), or finished and not returned.
\* Dealt, delivered and gone cards are never past their bound in the design:
\* the tick takes back or finishes them in the step that ages them there.
Alarm(c) == cage[c] = Bound /\ st[c] \in Aging

\* Layer 9: the lowest failing layer of the friend, "ok" when none fails.
Lowest(f) ==
  CASE held[f]                                                   -> "held"
    [] daemon[f] = "down"                                        -> "up"
    [] ~Hears(f)                                                 -> "hears"
    [] refused[f] \/ \E c \in CardsOf(f) : st[c] = "dealt" /\ Alarm(c)  -> "delivered"
    [] \E c \in CardsOf(f) : st[c] = "delivered" /\ Alarm(c)     -> "started"
    [] \E c \in CardsOf(f) : st[c] = "started" /\ Alarm(c)       -> "progressing"
    [] \E c \in CardsOf(f) : st[c] = "gone" /\ Alarm(c)          -> "finished"
    [] \E c \in CardsOf(f) : st[c] = "finished" /\ Alarm(c)      -> "returned"
    [] OTHER                                                     -> "ok"

\* The dashboard's word for the friend: the lowest failing layer, or under
\* the gap "dashupdown" the table's up/held/down, up drawn as all well.
Dash(f) ==
  IF Gap("dashupdown")
    THEN CASE Status(f) = "held" -> "held"
           [] Status(f) = "up"   -> "ok"
           [] OTHER              -> "down"
    ELSE Lowest(f)

\* ------------------------------------------------------------------- init

Init ==
  /\ daemon = [f \in Friends |-> "up"]
  /\ session = [f \in Friends |-> "live"]
  /\ credit = [f \in Friends |-> "ok"]
  /\ heardAge = [f \in Friends |-> HearBound]
  /\ byFinish = [f \in Friends |-> FALSE]
  /\ observed = [f \in Friends |-> FALSE]
  /\ refused = [f \in Friends |-> FALSE]
  /\ held = [f \in Friends |-> FALSE]
  /\ st = [c \in Cards |-> "pool"]
  /\ who = [c \in Cards |-> None]
  /\ cage = [c \in Cards |-> 0]
  /\ deafAge = [f \in Friends |-> 0]
  /\ events = 0

\* The started cards of f end their runs: a reboot, an archived session or a
\* provider out of allowance ends every live run of hers, with no report.
EndRuns(f) ==
  /\ st' = [c \in Cards |-> IF who[c] = f /\ st[c] = "started" THEN "gone" ELSE st[c]]
  /\ cage' = [c \in Cards |-> IF who[c] = f /\ st[c] = "started" THEN 0 ELSE cage[c]]

\* --------------------------------------------------------------- the friend

\* Her session answers the wake ping (evidence: the session pong with the
\* ping's nonce, written as friend health --state up, FriendHealth.Seen;
\* bound FriendPongWindow). The design pings every friend every pass; under
\* the gap "pingsuponly" only a friend the table shows up is pinged.
Answer(f) ==
  /\ CanTakeTurn(f)
  /\ ~Gap("pingsuponly") \/ Status(f) = "up"
  /\ heardAge[f] # 0 \/ ~observed[f] \/ deafAge[f] # 0
  /\ heardAge' = [heardAge EXCEPT ![f] = 0]
  /\ byFinish' = [byFinish EXCEPT ![f] = FALSE]
  /\ observed' = [observed EXCEPT ![f] = TRUE]
  /\ deafAge' = [deafAge EXCEPT ![f] = 0]
  /\ UNCHANGED <<daemon, session, credit, refused, held, st, who, cage, events>>

\* Her start receipt (evidence: today her beat's Running naming the card or a
\* progress stamp, friendStarted in internal/sprint/friend_deal.go).
Start(c) ==
  /\ st[c] = "delivered" /\ CanTakeTurn(who[c])
  /\ st' = [st EXCEPT ![c] = "started"]
  /\ cage' = [cage EXCEPT ![c] = 0]
  /\ UNCHANGED <<friendVars, who, events>>

\* Activity on a started card (evidence: FriendReport.Active, the newest write
\* under her working directory, or a progress stamp FieldProgress).
Progress(c) ==
  /\ st[c] = "started" /\ cage[c] # 0 /\ CanTakeTurn(who[c])
  /\ cage' = [cage EXCEPT ![c] = 0]
  /\ UNCHANGED <<friendVars, st, who, events>>

\* Her report becomes a finish (evidence: outbox/<job>/REPORT.md collected by
\* friend sync, friendFinish, with its bus note to the coordinator). The
\* finish is her session's evidence too (FriendFinishWindow).
Report(c) ==
  /\ st[c] = "started" /\ CanTakeTurn(who[c])
  /\ st' = [st EXCEPT ![c] = "finished"]
  /\ cage' = [cage EXCEPT ![c] = 0]
  /\ heardAge' = [heardAge EXCEPT ![who[c]] = 0]
  /\ byFinish' = [byFinish EXCEPT ![who[c]] = Gap("finishup")]
  /\ observed' = [observed EXCEPT ![who[c]] = TRUE]
  /\ deafAge' = [deafAge EXCEPT ![who[c]] = 0]
  /\ UNCHANGED <<daemon, session, credit, refused, held, who, events>>

\* Her run of the card dies with no report (evidence: the lane's turn ends
\* with no RESULT.md, internal/friend/lanes.go).
Die(c) ==
  /\ st[c] = "started"
  /\ st' = [st EXCEPT ![c] = "gone"]
  /\ cage' = [cage EXCEPT ![c] = 0]
  /\ UNCHANGED <<friendVars, who, events>>

\* --------------------------------------------------------------- the daemon

\* The session accepts the delivery (evidence: the turn that hands the brief
\* in exits 0 and its messages are acked, the daemon's status Delivered).
\* Under the gap "fileisdelivery" the brief written into her inbox is the
\* delivery, whatever her session can do.
Deliver(c) ==
  /\ st[c] = "dealt"
  /\ CanTakeTurn(who[c]) \/ Gap("fileisdelivery")
  /\ st' = [st EXCEPT ![c] = "delivered"]
  /\ cage' = [cage EXCEPT ![c] = 0]
  /\ refused' = [refused EXCEPT ![who[c]] = FALSE]
  /\ UNCHANGED <<daemon, session, credit, heardAge, byFinish, observed, held, deafAge, who, events>>

\* The session refuses the delivery (evidence: the provider's refusal streak
\* the daemon keeps, Session broken after BrokenAfter). The design shows it on
\* her row; under the gap "fileisdelivery" it is said nowhere the table reads.
FailDelivery(c) ==
  /\ st[c] = "dealt" /\ daemon[who[c]] = "up" /\ ~CanTakeTurn(who[c])
  /\ ~Gap("fileisdelivery")
  /\ ~refused[who[c]]
  /\ refused' = [refused EXCEPT ![who[c]] = TRUE]
  /\ UNCHANGED <<daemon, session, credit, heardAge, byFinish, observed, held, deafAge, cardVars, events>>

\* ----------------------------------------------------------------- the tick

\* Time passes, and the tick acts on what the ages say in the same step:
\* - every evidence clock one older, stopping at its bound;
\* - a gone card at the bound is finished, failed (lane-end-finishes-the-card;
\*   under the gap "lanes" only while her daemon runs);
\* - an unstarted card is taken back to the pool when its friend is not up
\*   after the step (not under the gap "downkeeps"), or when it has sat
\*   undelivered or unstarted to the bound (today: the stall ladder's rung 4,
\*   and friend take).
\* The alarms are Alarm(c), read off the same ages.
Tick ==
  LET ha == [f \in Friends |-> Up1(heardAge[f], HearBound)]
      da == [f \in Friends |-> IF CanTakeTurn(f) THEN 0 ELSE Up1(deafAge[f], Bound)]
      ca == [c \in Cards |-> IF st[c] \in Aging THEN Up1(cage[c], Bound) ELSE cage[c]]
      fin(c) == st[c] = "gone" /\ ca[c] = Bound /\ (~Gap("lanes") \/ daemon[who[c]] = "up")
      back(c) == /\ st[c] \in Unstarted
                 /\ \/ ~Gap("downkeeps") /\ StatusIn(who[c], ha) # "up"
                    \/ ca[c] = Bound
  IN /\ heardAge' = ha
     /\ deafAge' = da
     /\ st' = [c \in Cards |-> IF fin(c) THEN "finished" ELSE IF back(c) THEN "pool" ELSE st[c]]
     /\ who' = [c \in Cards |-> IF back(c) THEN None ELSE who[c]]
     /\ cage' = [c \in Cards |-> IF fin(c) \/ back(c) THEN 0 ELSE ca[c]]
     /\ UNCHANGED <<daemon, session, credit, byFinish, observed, refused, held, events>>

\* The deal: a card in the pool to a friend up with room (evidence: her
\* status, her load against her width; friendDeal).
Deal(c, f) ==
  /\ st[c] = "pool" /\ Status(f) = "up" /\ Load(f) < Width
  /\ st' = [st EXCEPT ![c] = "dealt"]
  /\ who' = [who EXCEPT ![c] = f]
  /\ cage' = [cage EXCEPT ![c] = 0]
  /\ UNCHANGED <<friendVars, events>>

\* The level: an undelivered card moves to a friend up with more room
\* (FriendLevel; a started card never moves). Its clock is not restarted: the
\* dealt bound counts from untaken_since, the first deal since its last take,
\* which no later redeal rewrites (WorkDeadline, internal/sprint/steps_tick.go).
\* A level that restarted it lets a card pass between two friends unstarted
\* for ever (TLC's counterexample to DealtEnds while this was written).
Level(c, g) ==
  /\ st[c] = "dealt" /\ who[c] # g
  /\ Status(g) = "up" /\ Load(g) + 1 < Load(who[c])
  /\ who' = [who EXCEPT ![c] = g]
  /\ UNCHANGED <<friendVars, st, cage, events>>

\* The finish reaches review or merge (evidence: the work card in review at
\* the reported Head, the coordinator's bus note).
Return(c) ==
  /\ st[c] = "finished"
  /\ st' = [st EXCEPT ![c] = "returned"]
  /\ who' = [who EXCEPT ![c] = None]
  /\ cage' = [cage EXCEPT ![c] = 0]
  /\ UNCHANGED <<friendVars, events>>

\* Bring back up: the daemon restarted (launchd or systemd brings it back),
\* and an archived session renewed (not under the gap "nobringup").
BringUp(f) ==
  /\ daemon[f] = "down"
  /\ daemon' = [daemon EXCEPT ![f] = "up"]
  /\ UNCHANGED <<session, credit, heardAge, byFinish, observed, refused, held, deafAge, cardVars, events>>
Renew(f) ==
  /\ session[f] = "archived" /\ ~Gap("nobringup")
  /\ session' = [session EXCEPT ![f] = "live"]
  /\ refused' = [refused EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<daemon, credit, heardAge, byFinish, observed, held, deafAge, cardVars, events>>

\* ---------------------------------------------------------- the coordinator

\* The hold takes back every unstarted card of hers in the same step
\* (FriendTake with Hold); started cards stay and finish.
Hold(f) ==
  /\ ~held[f] /\ events < MaxEvents
  /\ held' = [held EXCEPT ![f] = TRUE]
  /\ st' = [c \in Cards |-> IF who[c] = f /\ st[c] \in Unstarted THEN "pool" ELSE st[c]]
  /\ who' = [c \in Cards |-> IF who[c] = f /\ st[c] \in Unstarted THEN None ELSE who[c]]
  /\ cage' = [c \in Cards |-> IF who[c] = f /\ st[c] \in Unstarted THEN 0 ELSE cage[c]]
  /\ events' = events + 1
  /\ UNCHANGED <<daemon, session, credit, heardAge, byFinish, observed, refused, deafAge>>
Release(f) ==
  /\ held[f]
  /\ held' = [held EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<daemon, session, credit, heardAge, byFinish, observed, refused, deafAge, cardVars, events>>

\* ---------------------------------------------------------------- the world

Reboot(f) ==
  /\ daemon[f] = "up" /\ events < MaxEvents
  /\ daemon' = [daemon EXCEPT ![f] = "down"]
  /\ EndRuns(f)
  /\ events' = events + 1
  /\ UNCHANGED <<session, credit, heardAge, byFinish, observed, refused, held, deafAge, who>>
Archive(f) ==
  /\ session[f] = "live" /\ events < MaxEvents
  /\ session' = [session EXCEPT ![f] = "archived"]
  /\ EndRuns(f)
  /\ events' = events + 1
  /\ UNCHANGED <<daemon, credit, heardAge, byFinish, observed, refused, held, deafAge, who>>
CreditOut(f) ==
  /\ credit[f] = "ok" /\ events < MaxEvents
  /\ credit' = [credit EXCEPT ![f] = "out"]
  /\ EndRuns(f)
  /\ events' = events + 1
  /\ UNCHANGED <<daemon, session, heardAge, byFinish, observed, refused, held, deafAge, who>>
CreditBack(f) ==
  /\ credit[f] = "out"
  /\ credit' = [credit EXCEPT ![f] = "ok"]
  /\ UNCHANGED <<daemon, session, heardAge, byFinish, observed, refused, held, deafAge, cardVars, events>>

Next ==
  \/ Tick
  \/ \E f \in Friends :
       \/ Answer(f) \/ BringUp(f) \/ Renew(f) \/ Hold(f) \/ Release(f)
       \/ Reboot(f) \/ Archive(f) \/ CreditOut(f) \/ CreditBack(f)
  \/ \E c \in Cards :
       \/ Start(c) \/ Progress(c) \/ Report(c) \/ Die(c)
       \/ Deliver(c) \/ FailDelivery(c) \/ Return(c)
       \/ \E f \in Friends : Deal(c, f) \/ Level(c, f)

\* The safety specification: no fairness.
Spec == Init /\ [][Next]_vars

\* The environment assumption for the liveness: disruptions are finite
\* (MaxEvents); the clock ticks; a daemon down is restarted and an archived
\* session renewed; a session able to answer does; a finish is returned; and
\* every run ends, by a report or by dying.
SpecLive ==
  /\ Spec /\ WF_vars(Tick)
  /\ \A f \in Friends : WF_vars(BringUp(f)) /\ WF_vars(Renew(f)) /\ WF_vars(Answer(f))
  /\ \A c \in Cards : WF_vars(Return(c)) /\ WF_vars(Report(c) \/ Die(c))

\* ---------------------------------------------------------------- the rules

\* A card is working only after a start receipt.
WorkingOnlyAfterStart == \A c \in Cards : ShownWorking(c) => st[c] = "started"

\* A card whose run is gone is finished within the bound.
DeadRunFinishedWithinBound == \A c \in Cards : st[c] = "gone" => cage[c] < Bound

\* A friend not hearing is dealt nothing: every deal and every level lands on
\* a friend whose session answers (an action property).
DealOnlyToHearing ==
  [][\A c \in Cards : (st'[c] = "dealt" /\ who'[c] # who[c]) => Hears(who'[c])]_vars

\* A delivery is accepted only by a session able to take it (an action property).
DeliveredOnlyIntoLiveSession ==
  [][\A c \in Cards : (st[c] = "dealt" /\ st'[c] = "delivered") => CanTakeTurn(who[c])]_vars

\* No unstarted card is held by a held or down friend; a started card stays
\* with her and is held to DeadRunFinishedWithinBound.
NoUnstartedCardOffUp == \A c \in Cards : st[c] \in Unstarted => Status(who[c]) = "up"

\* A session deaf for the bound is not counted as hearing.
DeafShownWithinBound == \A f \in Friends : deafAge[f] = Bound => ~Hears(f)

\* The dashboard's word is a function of these variables, the lowest failing
\* layer, and it never says all well while a layer fails.
DashIsLowestLayer == \A f \in Friends : Dash(f) = Lowest(f)
DashNeverOkWhileFailing == \A f \in Friends : Dash(f) = "ok" => Lowest(f) = "ok"

\* A card on a friend has a friend; one in the pool or returned has none.
CardHasItsFriend == \A c \in Cards : (st[c] \in OnFriend \cup {"finished"}) <=> who[c] # None
\* No friend holds more than her width.
WithinWidth == \A f \in Friends : Load(f) <= Width

\* The liveness. Every dealt card is eventually finished or taken back.
DealtEnds == \A c \in Cards : st[c] \in OnFriend ~> st[c] \in {"pool", "finished", "returned"}
\* Every finish is returned.
FinishedReturned == \A c \in Cards : st[c] = "finished" ~> st[c] = "returned"
\* Every friend able to work (her provider has allowance) is eventually up:
\* her session hears.
AbleFriendUp == \A f \in Friends : credit[f] = "ok" ~> (Hears(f) \/ credit[f] = "out")

=============================================================================
