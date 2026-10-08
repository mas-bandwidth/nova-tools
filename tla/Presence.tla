------------------------------ MODULE Presence ------------------------------
(***************************************************************************)
(* A friend's presence as two facts (every-friend-daemon-beats-every-second, *)
(* 2026-10-06; docs/SPEC-FRIEND.md, The beat; docs/SPEC-SPRINT.md, Friend   *)
(* presence: the up rule).                                                  *)
(*                                                                         *)
(* The native heartbeat is a separate one-second task, with each send     *)
(* bounded below one second. Session work and bus reads run independently *)
(* and can block without preventing Heartbeat. Tick waits only for that  *)
(* heartbeat task, never for SessionStep. A reachable server is assumed:  *)
(* store below is the bus store, independent of the beat recipient.       *)
(* HoldBeat reverses the old session gate; BeatAloneUp reverses an up rule *)
(* that treats daemon liveness as session evidence.                      *)
(***************************************************************************)
EXTENDS Integers

CONSTANTS
    MaxT,         \* the last tick the model runs to
    BeatLive,     \* sprint.FriendBeatLive, in ticks
    Window,       \* sprint.FriendPongWindow, in ticks
    HoldBeat,     \* reversed witness: the beat held back while the session is deaf
    BeatAloneUp   \* reversed witness: up on the beat alone

ASSUME MaxT \in Nat /\ BeatLive \in Nat /\ BeatLive >= 2 /\ Window \in Nat \ {0}
ASSUME HoldBeat \in BOOLEAN /\ BeatAloneUp \in BOOLEAN

Never == -1  \* a time that has not happened

VARIABLES
    clock,     \* the time, in ticks
    daemon,    \* "stopped" or "running"
    started,   \* when the daemon last started, Never before the first
    stopped,   \* when it last stopped, Never while it never has
    stepped,   \* the running daemon has stepped its loop since the last tick
    lastBeat,  \* the sprint's record of her last beat, Never before the first
    carried,   \* last evidence the worker knew, distinct from the server-verified heard
    known,     \* the session evidence the daemon knows (SessionCheck.Evidence)
    session,   \* "answering" or "deaf"
    heard,     \* the sprint's record of the session's last evidence (pong, finish)
    store      \* the bus store: "up" or "down"

vars == <<clock, daemon, started, stopped, stepped, lastBeat, carried, known, session, heard, store>>

Time == Never..MaxT
Max(a, b) == IF a >= b THEN a ELSE b

TypeOK ==
    /\ clock \in 0..MaxT
    /\ daemon \in {"stopped", "running"}
    /\ started \in Time /\ stopped \in Time
    /\ stepped \in BOOLEAN
    /\ lastBeat \in Time /\ carried \in Time /\ known \in Time
    /\ session \in {"answering", "deaf"}
    /\ heard \in Time
    /\ store \in {"up", "down"}

\* The sprint's two facts, and its word (sprint.FriendDaemon,
\* sprint.FriendSessionHeard, sprint.FriendStatus).
DaemonUp == lastBeat # Never /\ clock - lastBeat <= BeatLive
SessionUp == heard # Never /\ clock - heard < Window
Up == IF BeatAloneUp THEN DaemonUp ELSE DaemonUp /\ SessionUp

Init ==
    /\ clock = 0
    /\ daemon = "stopped"
    /\ started = Never /\ stopped = Never
    /\ stepped = FALSE
    /\ lastBeat = Never /\ carried = Never /\ known = Never
    /\ session \in {"answering", "deaf"}
    /\ heard = Never
    /\ store \in {"up", "down"}

Start ==
    /\ daemon = "stopped"
    /\ daemon' = "running"
    /\ started' = clock
    /\ stepped' = FALSE
    /\ known' = Never  \* a daemon that starts knows nothing of the session
    /\ UNCHANGED <<clock, stopped, lastBeat, carried, session, heard, store>>

Stop ==
    /\ daemon = "running"
    /\ daemon' = "stopped"
    /\ stopped' = clock
    /\ UNCHANGED <<clock, started, stepped, lastBeat, carried, known, session, heard, store>>

\* Independent bounded heartbeat, carrying only what the worker already knows.
Heartbeat ==
    /\ daemon = "running"
    /\ ~stepped
    /\ stepped' = TRUE
    /\ LET beats == ~HoldBeat \/ (known # Never /\ clock - known < Window)
       IN /\ lastBeat' = IF beats THEN clock ELSE lastBeat
          /\ carried' = IF beats THEN known ELSE carried
    /\ UNCHANGED <<clock, daemon, started, stopped, known, session, heard, store>>

\* Session work can make progress only when the bus answers; no heartbeat waits for it.
SessionStep ==
    /\ daemon = "running"
    /\ store = "up"
    /\ known' = IF session = "answering" THEN clock ELSE known
    /\ UNCHANGED <<clock, daemon, started, stopped, stepped, lastBeat, carried, session, heard, store>>

\* The session gives the sprint evidence (a wake ping answered, a card finished).
SessionEvidence ==
    /\ session = "answering"
    /\ heard' = clock
    /\ UNCHANGED <<clock, daemon, started, stopped, stepped, lastBeat, carried, known, session, store>>

SessionFlips ==
    /\ session' = IF session = "answering" THEN "deaf" ELSE "answering"
    /\ UNCHANGED <<clock, daemon, started, stopped, stepped, lastBeat, carried, known, heard, store>>

StoreFlips ==
    /\ store' = IF store = "up" THEN "down" ELSE "up"
    /\ UNCHANGED <<clock, daemon, started, stopped, stepped, lastBeat, carried, known, session, heard>>

\* A second passes, once the independent heartbeat has run in it.
Tick ==
    /\ clock < MaxT
    /\ daemon = "running" => stepped
    /\ clock' = clock + 1
    /\ stepped' = FALSE
    /\ UNCHANGED <<daemon, started, stopped, lastBeat, carried, known, session, heard, store>>

Next == Start \/ Stop \/ Heartbeat \/ SessionStep \/ SessionEvidence \/ SessionFlips \/ StoreFlips \/ Tick

Spec == Init /\ [][Next]_vars

---------------------------------------------------------------------------
\* The card's invariant: a running daemon's last beat is never older than
\* two ticks (two seconds), counted from its start before its first beat.
BeatFresh == daemon = "running" => clock - Max(lastBeat, started) <= 2

\* While it runs, its row reads the daemon up, whatever the session says: a
\* deaf session is "daemon up, session deaf", never "down" with nothing named.
RunningReadsDaemonUp ==
    (daemon = "running" /\ lastBeat >= started) => DaemonUp

\* Up is both facts: never on the beat alone, never on the session alone.
UpHasSessionEvidence == Up => SessionUp
UpHasDaemon == Up => (daemon = "running" \/ (stopped # Never /\ clock - stopped <= BeatLive))

\* A beat carries the session evidence the daemon knew when it beat, never
\* a later one.
CarriedNeverAheadOfTheBeat == carried <= lastBeat
=============================================================================
