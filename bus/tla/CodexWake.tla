------------------------------ MODULE CodexWake ------------------------------
\* nova-bus hook --codex-thread (bus/codex.go codexWake): a hook that prints
\* every message and wakes a Codex chat with codex queue only while it is
\* idle, a chat that runs turns, and an app that drains a queued row into a
\* new turn once the chat is idle. A row queued into a running turn is a
\* steer, which can hang the chat; a second row behind one not yet drained
\* is counted as one too.
\*
\* The state: chat ("idle" or "busy", as the rollout shows it); rows, queued
\* and not drained; sent, the messages printed (each saved to its file
\* before the wake reads it); woken, the last id named in a wake (dir/woken,
\* kept across a crash); awaiting, a queued row whose turn has not started
\* (dir/woken's text, kept across a crash); up, crashes, fails. Waiting,
\* the saved ids after woken, is what a restarted hook reads from the files.
\* The outside: named, every id in a queued row; tried, every id a wake was
\* attempted for (queued, failed, or lost to a crash between the mark and the
\* queue); steer and twice, set when a row is queued into a busy chat or
\* behind an undrained row, or names an id already named.
\*
\* The idle read and the queue are one step here; in the code they are
\* milliseconds apart, and a turn started in that window is not modelled.
\*
\* Broken = "none" is the design. Reversed witnesses:
\*   "nocheck"   the wake does not wait for idle: NoSteer fails
\*   "noawait"   the wake does not wait for the last row's turn: a second
\*               row queues behind it, NoSteer fails
\*   "nomark"    woken is not advanced: a later wake names the id again,
\*               NoTwice fails
\*   "nocatchup" a message printed while the chat is busy is passed over,
\*               never named: EveryTried fails
EXTENDS Naturals, FiniteSets

CONSTANTS N, MaxCrash, MaxFail, Broken

VARIABLES chat, rows, sent, woken, awaiting, up, crashes, fails, named, tried, steer, twice

vars == <<chat, rows, sent, woken, awaiting, up, crashes, fails, named, tried, steer, twice>>
Ids == 1..N

Waiting == {m \in 1..sent : m > woken}

TypeOK ==
  /\ chat \in {"idle", "busy"} /\ rows \in 0..N /\ sent \in 0..N /\ woken \in 0..N
  /\ awaiting \in BOOLEAN /\ up \in BOOLEAN /\ crashes \in 0..MaxCrash /\ fails \in 0..MaxFail
  /\ named \subseteq Ids /\ tried \subseteq Ids /\ steer \in BOOLEAN /\ twice \in BOOLEAN

Init ==
  /\ chat = "idle" /\ rows = 0 /\ sent = 0 /\ woken = 0 /\ awaiting = FALSE
  /\ up = TRUE /\ crashes = 0 /\ fails = 0
  /\ named = {} /\ tried = {} /\ steer = FALSE /\ twice = FALSE

\* A message is printed and saved ("nocatchup": one printed while the chat
\* is busy is marked woken without a wake).
Deliver ==
  /\ up /\ sent < N
  /\ sent' = sent + 1
  /\ woken' = IF Broken = "nocatchup" /\ chat = "busy" THEN sent + 1 ELSE woken
  /\ UNCHANGED <<chat, rows, awaiting, up, crashes, fails, named, tried, steer, twice>>

\* poll: one wake names every waiting id, marked woken first; it is queued,
\* or codex queue fails (reported, never retried), or the hook crashes
\* between the mark and the queue (the wake is lost, the id never repeated).
Wake ==
  /\ up /\ Waiting /= {}
  /\ (Broken /= "nocheck" => chat = "idle")
  /\ (Broken /= "noawait" => ~awaiting)
  /\ woken' = IF Broken = "nomark" THEN woken ELSE sent
  /\ tried' = tried \cup Waiting
  /\ \/ /\ rows' = rows + 1 /\ named' = named \cup Waiting /\ awaiting' = TRUE
        /\ steer' = (steer \/ chat = "busy" \/ rows > 0)
        /\ twice' = (twice \/ Waiting \cap named /= {})
        /\ UNCHANGED <<up, crashes, fails>>
     \/ /\ fails < MaxFail /\ fails' = fails + 1
        /\ UNCHANGED <<rows, named, awaiting, steer, twice, up, crashes>>
     \/ /\ crashes < MaxCrash /\ crashes' = crashes + 1 /\ up' = FALSE
        /\ UNCHANGED <<rows, named, awaiting, steer, twice, fails>>
  /\ UNCHANGED <<chat, sent>>

\* Someone starts a turn; a turn ends.
UserTurn == chat = "idle" /\ chat' = "busy" /\ UNCHANGED <<rows, sent, woken, awaiting, up, crashes, fails, named, tried, steer, twice>>
TurnEnd == chat = "busy" /\ chat' = "idle" /\ UNCHANGED <<rows, sent, woken, awaiting, up, crashes, fails, named, tried, steer, twice>>

\* The app drains a row into a turn: its text is in the rollout.
Drain ==
  /\ chat = "idle" /\ rows > 0
  /\ rows' = rows - 1 /\ chat' = "busy" /\ awaiting' = FALSE
  /\ UNCHANGED <<sent, woken, up, crashes, fails, named, tried, steer, twice>>

Crash ==
  /\ up /\ crashes < MaxCrash
  /\ up' = FALSE /\ crashes' = crashes + 1
  /\ UNCHANGED <<chat, rows, sent, woken, awaiting, fails, named, tried, steer, twice>>

Restart == ~up /\ up' = TRUE /\ UNCHANGED <<chat, rows, sent, woken, awaiting, crashes, fails, named, tried, steer, twice>>

Next == Deliver \/ Wake \/ UserTurn \/ TurnEnd \/ Drain \/ Crash \/ Restart

\* A turn always ends and the app drains its rows; the hook keeps polling,
\* and a chat that is idle again and again is woken (strong fairness: a
\* person may keep starting turns).
Spec == Init /\ [][Next]_vars
          /\ WF_vars(Deliver) /\ WF_vars(TurnEnd) /\ WF_vars(Drain) /\ WF_vars(Restart)
          /\ SF_vars(Wake)

\* No row is queued into a running turn, or behind one not yet drained.
NoSteer == ~steer

\* No id is named in two rows.
NoTwice == ~twice

\* Every message printed is named in a wake, or its wake was attempted.
EveryTried == \A m \in Ids : <>(m \in tried)
=============================================================================
