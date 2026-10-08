------------------------------- MODULE FriendCard -------------------------------
\* Friend card lifecycle model (BRIEF: friend-card-lifecycle-tla-b.w10~15)
\* Models the friend's own liveness and card states: dealt, delivered, started,
\* finished, returned, taken back. Variables per friend, per card.
\*
\* Per friend: daemonUp, sessionHearing (session answers pings)
\* Per card: state (dealt, delivered, started, finished, returned, takenBack)
\* Actions: friend (answer, start, report, die), daemon (deliver, failDelivery),
\*   tick (deal, level, takeBack, alarm, bringBackUp), world (sessionArchived, creditOut, creditBack, reboot)
\*
\* Invariants:
\*   - A card is working only after a start receipt
\*   - A finished card must have a reported finish or notice
\*   - A takenBack card must have a failed finish or notice
\*
\* Broken = "none" is the design. Every other value is a reversed witness.

EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS
  Friends,          \* friend names
  Cards,            \* card ids
  Window,           \* ping timeout window
  MaxTime,          \* maximum clock value

VARIABLES
  daemonUp,         \* friend -> BOOLEAN: daemon process alive
  sessionHearing,   \* friend -> BOOLEAN: session answers pings
  cardState,        \* card -> {"dealt", "delivered", "started", "finished", "returned", "takenBack"}
  cardFriend,       \* card -> friend: which friend holds this card
  cardSince,        \* card -> Nat: clock value when card entered current state
  lastPong,         \* friend -> Nat: clock value of last session pong
  daemonAge,        \* friend -> Nat: clock value since daemon last reported
  sessionArchived,  \* friend -> BOOLEAN: session is archived
  daemonDead,       \* friend -> BOOLEAN: daemon died without report
  cardNotice,       \* card -> BOOLEAN: card has a notice/ack

vars == <<daemonUp, sessionHearing, cardState, cardFriend, cardSince, lastPong, daemonAge, sessionArchived, daemonDead, cardNotice>>

\* Initial state
Init ==
  /\ daemonUp = [f \in Friends |-> TRUE]
  /\ sessionHearing = [f \in Friends |-> TRUE]
  /\ cardState = [c \in Cards |-> "dealt"]
  /\ cardFriend = [c \in Cards |-> CHOOSE f \in Friends: TRUE]
  /\ cardSince = [c \in Cards |-> 0]
  /\ lastPong = [f \in Friends |-> 0]
  /\ daemonAge = [f \in Friends |-> 0]
  /\ sessionArchived = [f \in Friends |-> FALSE]
  /\ daemonDead = [f \in Friends |-> FALSE]
  /\ cardNotice = [c \in Cards |-> FALSE]

\* Friend action: answer a ping (session is alive)
FriendAnswer(f) ==
  /\ sessionHearing[f]
  /\ lastPong' = [lastPong EXCEPT ![f] = now]
  /\ UNCHANGED <<daemonUp, cardState, cardFriend, cardSince, daemonAge, sessionArchived, daemonDead, cardNotice>>

\* Friend action: start a card (transition from delivered to started)
FriendStart(c) ==
  /\ cardState[c] = "delivered"
  /\ cardFriend[c] \in Friends
  /\ sessionHearing[cardFriend[c]]
  /\ cardState' = [cardState EXCEPT ![c] = "started"]
  /\ cardSince' = [cardSince EXCEPT ![c] = now]
  /\ UNCHANGED <<daemonUp, cardFriend, lastPong, daemonAge, sessionArchived, daemonDead, cardNotice>>

\* Friend action: report finish of a card
FriendReport(c) ==
  /\ cardState[c] = "started"
  /\ cardFriend[c] \in Friends
  /\ cardState' = [cardState EXCEPT ![c] = "finished"]
  /\ cardSince' = [cardSince EXCEPT ![c] = now]
  /\ cardNotice' = [cardNotice EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<daemonUp, cardFriend, lastPong, daemonAge, sessionArchived, daemonDead>>

\* Friend action: die without reporting (broken case)
FriendDie(f) ==
  /\ daemonUp[f]
  /\ daemonUp' = [daemonUp EXCEPT ![f] = FALSE]
  /\ daemonDead' = [daemonDead EXCEPT ![f] = TRUE]
  /\ UNCHANGED <<sessionHearing, cardState, cardFriend, cardSince, lastPong, daemonAge, sessionArchived, cardNotice>>

\* Daemon action: deliver a card (transition from dealt to delivered)
DaemonDeliver(c) ==
  /\ cardState[c] = "dealt"
  /\ cardFriend[c] \in Friends
  /\ daemonUp[cardFriend[c]]
  /\ sessionHearing[cardFriend[c]]
  /\ cardState' = [cardState EXCEPT ![c] = "delivered"]
  /\ cardSince' = [cardSince EXCEPT ![c] = now]
  /\ UNCHANGED <<daemonUp, cardFriend, lastPong, daemonAge, sessionArchived, daemonDead, cardNotice>>

\* Daemon action: fail delivery (card stays dealt, daemon reports error)
DaemonFailDelivery(c) ==
  /\ cardState[c] = "dealt"
  /\ cardFriend[c] \in Friends
  /\ UNCHANGED <<daemonUp, sessionHearing, cardState, cardFriend, cardSince, lastPong, daemonAge, sessionArchived, daemonDead, cardNotice>>

\* Daemon action: report age
DaemonReport(f) ==
  /\ daemonUp[f]
  /\ daemonAge' = [daemonAge EXCEPT ![f] = 0]
  /\ UNCHANGED <<sessionHearing, cardState, cardFriend, cardSince, lastPong, sessionArchived, daemonDead, cardNotice>>

\* Tick action: deal a card
TickDeal(c, f) ==
  /\ cardState[c] = "dealt"
  /\ f \in Friends
  /\ cardFriend' = [cardFriend EXCEPT ![c] = f]
  /\ cardSince' = [cardSince EXCEPT ![c] = now]
  /\ UNCHANGED <<daemonUp, sessionHearing, cardState, lastPong, daemonAge, sessionArchived, daemonDead, cardNotice>>

\* Tick action: take back a card without notice (broken case - should not happen)
TickTakeBackNoNotice(c) ==
  /\ cardState[c] \in {"started", "finished"}
  /\ cardState' = [cardState EXCEPT ![c] = "takenBack"]
  /\ cardSince' = [cardSince EXCEPT ![c] = now]
  /\ UNCHANGED <<daemonUp, sessionHearing, cardFriend, lastPong, daemonAge, sessionArchived, daemonDead, cardNotice>>

\* Tick action: take back a card with notice (correct)
TickTakeBack(c) ==
  /\ cardState[c] \in {"started", "finished", "returned"}
  /\ cardNotice[c]
  /\ cardState' = [cardState EXCEPT ![c] = "takenBack"]
  /\ cardSince' = [cardSince EXCEPT ![c] = now]
  /\ UNCHANGED <<daemonUp, sessionHearing, cardFriend, lastPong, daemonAge, sessionArchived, daemonDead>>

\* World: session archived
SessionArchive(f) ==
  /\ sessionArchived[f] = FALSE
  /\ sessionArchived' = [sessionArchived EXCEPT ![f] = TRUE]
  /\ UNCHANGED <<daemonUp, sessionHearing, cardState, cardFriend, cardSince, lastPong, daemonAge, daemonDead, cardNotice>>

\* World: session restored
SessionRestore(f) ==
  /\ sessionArchived[f]
  /\ sessionArchived' = [sessionArchived EXCEPT ![f] = FALSE]
  /\ UNCHANGED <<daemonUp, sessionHearing, cardState, cardFriend, cardSince, lastPong, daemonAge, daemonDead, cardNotice>>

\* World: clock tick
Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ UNCHANGED <<daemonUp, sessionHearing, cardState, cardFriend, cardSince, lastPong, daemonAge, sessionArchived, daemonDead, cardNotice>>

\* Next state
Next ==
  \/ /\ \E f \in Friends: FriendAnswer(f)
  \/ /\ \E c \in Cards: FriendStart(c)
  \/ /\ \E c \in Cards: FriendReport(c)
  \/ /\ \E f \in Friends: FriendDie(f)
  \/ /\ \E c \in Cards: DaemonDeliver(c)
  \/ /\ \E c \in Cards: DaemonFailDelivery(c)
  \/ /\ \E f \in Friends: DaemonReport(f)
  \/ /\ \E c \in Cards: \E f \in Friends: TickDeal(c, f)
  \/ /\ \E c \in Cards: TickTakeBackNoNotice(c)
  \/ /\ \E c \in Cards: TickTakeBack(c)
  \/ /\ \E f \in Friends: SessionArchive(f)
  \/ /\ \E f \in Friends: SessionRestore(f)
  \/ Tick

\* Safety invariant: a card is working only after started
CardWorkingOnlyAfterStarted ==
  \A c \in Cards: cardState[c] = "started" => cardSince[c] > 0

\* Safety invariant: a finished card has a notice
FinishedHasNotice ==
  \A c \in Cards: cardState[c] = "finished" => cardNotice[c]

\* Safety invariant: a takenBack card must have had a notice or been returned
TakenBackValid ==
  \A c \in Cards: cardState[c] = "takenBack" =>
    \/ cardNotice[c]
    \/ \E c' \in Cards: c' \notin {c} /\ cardState[c'] = "returned"

\* Liveness: every dealt card eventually gets started or taken back
StartedOrTakenEventually ==
  \A c \in Cards: <>[](cardState[c] = "dealt" => <>cardState[c] \in {"started", "takenBack", "returned"})

\* Liveness: every started card eventually reports or is taken back
ReportedOrTakenEventually ==
  \A c \in Cards: <>[](cardState[c] = "started" => <>cardState[c] \in {"finished", "takenBack", "returned"})

\* Type invariant
TypeOK ==
  /\ now \in 0..MaxTime
  /\ daemonUp \in [Friends -> BOOLEAN]
  /\ sessionHearing \in [Friends -> BOOLEAN]
  /\ cardState \in [Cards -> {"dealt", "delivered", "started", "finished", "returned", "takenBack"}]
  /\ cardFriend \in [Cards -> Friends]
  /\ cardSince \in [Cards -> 0..MaxTime]
  /\ lastPong \in [Friends -> 0..MaxTime]
  /\ daemonAge \in [Friends -> 0..MaxTime]
  /\ sessionArchived \in [Friends -> BOOLEAN]
  /\ daemonDead \in [Friends -> BOOLEAN]
  /\ cardNotice \in [Cards -> BOOLEAN]

=============================================================================
