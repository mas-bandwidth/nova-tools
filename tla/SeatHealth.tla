---------------------------- MODULE SeatHealth ----------------------------
\* nova-sprint's seat generation and a friend's health (docs/SPEC-SPRINT.md
\* section 1, "A friend's health"; internal/sprint/seat.go MoveSeat,
\* friend_health.go NotHealth, ObserveFriend, ObservedStatus, FriendStatus).
\* The sprint server is the authority and the table: the seat (holder and
\* generation) moves by handover, the coordinator's daemon observes one
\* friend and writes what it saw, fenced by the seat it read, and the table's
\* word is derived at every read: up or down, held by the coordinator's own
\* hold alone. The keepalive and its nonces are the daemon's (Friend.tla).
\*
\* The state the server owns: holder, gen (the seat), hstate, hseen, hgen (the
\* friend's health record: the word, the proof's time, the generation it was
\* observed under; hstate = "none" until the first), held (friend down), beat
\* (her last raw beat). The outside: the daemons' observations in flight
\* (inflight: each carries the generation its daemon read the seat at and
\* the proof's time, delivered later in any order, so a delayed or
\* superseded one is possible), handovers, her beats, the hold, the clock.
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* each caught by one property below:
\*   "nofence"     any generation writes: AcceptedAtTheSeat
\*   "olderproof"  an older proof overwrites: ProofNeverGoesBack
\*   "beatwins"    her raw beat makes an observed friend up: ObservedNeverUpByBeat
\*   "noexpiry"    a proof never ages: UpOnlyOnFreshProof
\*   "samegen"     a handover keeps the generation: GenerationStepsWithTheSeat
\*                 (at the first handover; past it OldSeatNeverUp breaks too:
\*                 A->B->A, A's first seat's proof shows up under A's second)

EXTENDS Naturals, FiniteSets

CONSTANTS Holders, MaxTime, MaxObs, DownAfter, Broken

VARIABLES now, holder, gen, moves, hstate, hseen, hgen, hmoves, held, beat, inflight, sent, accepted
vars == <<now, holder, gen, moves, hstate, hseen, hgen, hmoves, held, beat, inflight, sent, accepted>>

Never == 0 \* a time before the clock: no beat, no proof

\* An observation: the word, the proof's time, the seat generation its daemon
\* read, and (a ghost) the handover count when it was sent.
Obs == [state: {"up", "asleep", "down"}, seen: 1..MaxTime, gen: 1..(MaxObs + 1), moves: 0..MaxObs]

TypeOK ==
  /\ now \in 0..MaxTime
  /\ holder \in Holders
  /\ gen \in 1..(MaxObs + 1)
  /\ moves \in 0..MaxObs
  /\ hstate \in {"none", "up", "asleep", "down"}
  /\ hseen \in 0..MaxTime
  /\ hgen \in 0..(MaxObs + 1)
  /\ hmoves \in 0..MaxObs
  /\ held \in BOOLEAN
  /\ beat \in 0..MaxTime
  /\ inflight \subseteq Obs
  /\ sent \in 0..MaxObs
  /\ accepted \in 0..MaxObs

Observed == hstate # "none"

\* The friends' rule (FriendStatus, ObservedStatus): held wins; once observed,
\* up only for an up word under the current generation with a fresh proof;
\* else her raw beat, while fresh. The witnesses let the beat decide an
\* observed friend, or let a proof never age.
BeatFresh == beat # Never /\ now - beat < DownAfter
ProofFresh == IF Broken = "noexpiry" THEN TRUE ELSE now - hseen < DownAfter
ObservedWord == IF hstate = "up" /\ hgen = gen /\ ProofFresh THEN "up" ELSE "down"
Status ==
  IF held THEN "held"
  ELSE IF Observed /\ ~(Broken = "beatwins" /\ BeatFresh) THEN ObservedWord
  ELSE IF BeatFresh THEN "up" ELSE "down"

Init ==
  /\ now = 0
  /\ holder \in Holders /\ gen = 1 /\ moves = 0
  /\ hstate = "none" /\ hseen = Never /\ hgen = 0 /\ hmoves = 0
  /\ held = FALSE /\ beat = Never
  /\ inflight = {} /\ sent = 0 /\ accepted = 0

Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ UNCHANGED <<holder, gen, moves, hstate, hseen, hgen, hmoves, held, beat, inflight, sent, accepted>>

\* The seat moves to another holder (MoveSeat): the next generation, every
\* time. The witness keeps the generation.
Handover(h) ==
  /\ h # holder
  /\ moves < MaxObs
  /\ holder' = h
  /\ gen' = IF Broken = "samegen" THEN gen ELSE gen + 1
  /\ moves' = moves + 1
  /\ UNCHANGED <<now, hstate, hseen, hgen, hmoves, held, beat, inflight, sent, accepted>>

\* A daemon sends an observation of a proof seen at some time up to now,
\* under the seat as it reads it now (holder and generation).
Send(st) ==
  /\ sent < MaxObs
  /\ now >= 1
  /\ \E t \in 1..now :
       inflight' = inflight \cup {[state |-> st, seen |-> t, gen |-> gen, moves |-> moves]}
  /\ sent' = sent + 1
  /\ UNCHANGED <<now, holder, gen, moves, hstate, hseen, hgen, hmoves, held, beat, accepted>>

\* An observation arrives at the server (ObserveFriend under the fence):
\* accepted only at the seat's generation now, with a proof newer than the
\* row's; refused otherwise, nothing written. The witnesses take any
\* generation, or any proof.
Fenced(o) == o.gen = gen \/ Broken = "nofence"
Newer(o) == o.seen > hseen \/ Broken = "olderproof"
Deliver(o) ==
  /\ o \in inflight
  /\ inflight' = inflight \ {o}
  /\ IF Fenced(o) /\ Newer(o)
       THEN /\ hstate' = o.state /\ hseen' = o.seen /\ hgen' = o.gen /\ hmoves' = o.moves
            /\ accepted' = accepted + 1
       ELSE UNCHANGED <<hstate, hseen, hgen, hmoves, accepted>>
  /\ UNCHANGED <<now, holder, gen, moves, held, beat, sent>>

\* Her own beat (friend beat), the coordinator's hold and its release.
Beat ==
  /\ now >= 1
  /\ beat' = now
  /\ UNCHANGED <<now, holder, gen, moves, hstate, hseen, hgen, hmoves, held, inflight, sent, accepted>>
Hold == held' = TRUE /\ UNCHANGED <<now, holder, gen, moves, hstate, hseen, hgen, hmoves, beat, inflight, sent, accepted>>
Release == held' = FALSE /\ UNCHANGED <<now, holder, gen, moves, hstate, hseen, hgen, hmoves, beat, inflight, sent, accepted>>

Next ==
  \/ Tick
  \/ \E h \in Holders : Handover(h)
  \/ \E st \in {"up", "asleep", "down"} : Send(st)
  \/ \E o \in inflight : Deliver(o)
  \/ Beat \/ Hold \/ Release

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick)

\* ---------------------------------------------------------------- the rules

\* The table shows up, held or down and nothing else.
ThreeWords == Status \in {"up", "held", "down"}

\* Held is the coordinator's hold alone: no observation and no beat shows it.
HeldIsTheHold == (Status = "held") <=> held

\* A friend is up only on an up word under the current seat with a proof
\* under DownAfter old, or, never observed, on a fresh beat.
UpOnlyOnFreshProof ==
  Status = "up" => \/ (Observed /\ hstate = "up" /\ hgen = gen /\ now - hseen < DownAfter)
                   \/ (~Observed /\ BeatFresh)

\* An old seat's proof never looks up under a new seat: what the row holds
\* was accepted after the last handover (A->B->A rejects A's first seat).
OldSeatNeverUp == Observed => (hmoves = moves \/ Status # "up")

\* An accepted observation names the seat's generation now: the fence.
AcceptedAtTheSeat == [][accepted' > accepted => hgen' = gen']_vars

\* The row's proof never goes back: a delayed or repeated proof writes nothing.
ProofNeverGoesBack == [][hseen' >= hseen]_vars

\* Once observed, her raw beat never makes her up.
ObservedNeverUpByBeat == (Observed /\ ObservedWord = "down" /\ ~held) => Status = "down"

\* Every handover takes the next generation, and nothing else moves it.
GenerationStepsWithTheSeat == [][gen' = gen + moves' - moves]_vars

=============================================================================
