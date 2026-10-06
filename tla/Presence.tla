------------------------------ MODULE Presence ------------------------------
(***************************************************************************)
(* A friend's presence as two facts (every-friend-daemon-beats-every-second, *)
(* 2026-10-06; docs/SPEC-FRIEND.md, The beat; docs/SPEC-SPRINT.md, Friend   *)
(* presence: the up rule).                                                  *)
(*                                                                         *)
(* The daemon (internal/friend Daemon.Run) steps its loop once a second     *)
(* and beats to the sprint server at every step, whatever the session and  *)
(* the bus store say (SessionCheck.Beat steps the check and never holds    *)
(* the beat). The session, separately, gives evidence or does not: a wake  *)
(* ping it answered, or a card it finished, which the sprint records      *)
(* (friend health, friend-finish). The sprint's up rule (sprint.FriendStatus)*)
(* is both facts together: her last beat at most BeatLive old, and her     *)
(* session's evidence under Window old.                                     *)
(*                                                                         *)
(* One unit of the clock is one second. The loop's step takes under a      *)
(* second (the read blocks at most BeatEvery), so a running daemon steps    *)
(* between two ticks: Tick waits for the step. That is the timing the code *)
(* promises; what the model checks is that the step beats unconditionally. *)
(*                                                                         *)
(* HoldBeat is the code before this card: the step beats only while the   *)
(* session's evidence is fresh ("no beat until a session check answers"),  *)
(* and BeatFresh fails. BeatAloneUp is an up rule that reads the beat alone *)
(* (the finding of 2026-10-04, friends up for hours on beats a loop sent), *)
(* and UpHasSessionEvidence fails.                                          *)
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
    carried,   \* the session evidence her last beat carried (friend beat --pong)
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

\* One step of the loop: the bus store read (the session's word reaches the
\* daemon only through it), the session check stepped, then the beat,
\* carrying what the daemon knows. Under HoldBeat the beat goes only while
\* the session's evidence the daemon knows is fresh.
Step ==
    /\ daemon = "running"
    /\ ~stepped
    /\ stepped' = TRUE
    /\ LET k == IF store = "up" /\ session = "answering" THEN clock ELSE known
           beats == ~HoldBeat \/ (k # Never /\ clock - k < Window)
       IN /\ known' = k
          /\ lastBeat' = IF beats THEN clock ELSE lastBeat
          /\ carried' = IF beats THEN k ELSE carried
    /\ UNCHANGED <<clock, daemon, started, stopped, session, heard, store>>

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

\* A second passes, once a running daemon has stepped in it.
Tick ==
    /\ clock < MaxT
    /\ daemon = "running" => stepped
    /\ clock' = clock + 1
    /\ stepped' = FALSE
    /\ UNCHANGED <<daemon, started, stopped, lastBeat, carried, known, session, heard, store>>

Next == Start \/ Stop \/ Step \/ SessionEvidence \/ SessionFlips \/ StoreFlips \/ Tick

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
