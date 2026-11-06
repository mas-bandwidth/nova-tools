------------------------------ MODULE CodexWake ------------------------------
\* nova-bus hook --codex-thread (bus/codexwake.go): a hook that prints every
\* message and wakes a Codex chat with codex queue, a chat that runs turns,
\* and an app that drains a queued row into a new turn once the chat is idle.
\*
\* The hook's wake is two steps, as in poll: Check reads the rollout (idle,
\* and the last wake's turn started), then Exec writes the intent and runs
\* codex queue. A turn may start between them. The model shows what that
\* costs: the row lands in a busy chat (raced) and the app drains it as a
\* new turn once the chat is idle again (observed of the app; Drain needs
\* idle here). It does not show the race absent: it is a property of the
\* timing that the model lets happen.
\*
\* State: chat ("idle"/"busy"); rows, queued and not drained; sent, the ids
\* printed (each saved first); woken (dir/woken), awaiting (its text not yet
\* in the rollout), intent (dir/intent, 0 for none), all kept across a crash;
\* faults (dir/faults: ids whose wake failed or whose result is unknown);
\* stage ("none", "checked", "queued": a row is out, not yet marked), hi
\* (the last id of the wake in hand), tries, up, crashes. The outside:
\* named (ids in a queued row), raced, twice (an id in two rows), extra (a
\* second undrained row: only after a crash left an unknown).
\*
\* Broken = "none" is the design. Reversed witnesses:
\*   "nocheck"   Check does not wait for idle: a row is queued into a turn
\*               the hook saw running, SeenIdle fails
\*   "noawait"   Check does not wait for the last row's turn: a second row
\*               queues behind it with no crash, OneRow fails
\*   "requeue"   an intent found at start is queued again rather than
\*               faulted: an id in two rows, NoTwice fails
\*   "nocatchup" an id printed while the chat is busy is passed over:
\*               EveryHandled fails
EXTENDS Naturals, FiniteSets

CONSTANTS N, MaxCrash, Limit, Broken

VARIABLES chat, rows, sent, woken, awaiting, intent, faults, stage, hi, tries, up, crashes,
          named, raced, seenBusy, twice, unknowns

vars == <<chat, rows, sent, woken, awaiting, intent, faults, stage, hi, tries, up, crashes,
          named, raced, seenBusy, twice, unknowns>>
Ids == 1..N
Range(a, b) == {m \in Ids : a < m /\ m <= b}
Waiting == Range(woken, sent)

TypeOK ==
  /\ chat \in {"idle", "busy"} /\ rows \in 0..N+1 /\ sent \in 0..N /\ woken \in 0..N
  /\ awaiting \in BOOLEAN /\ intent \in 0..N /\ faults \subseteq Ids
  /\ stage \in {"none", "checked", "queued"} /\ hi \in 0..N /\ tries \in 0..Limit
  /\ up \in BOOLEAN /\ crashes \in 0..MaxCrash /\ named \subseteq Ids
  /\ raced \in BOOLEAN /\ seenBusy \in BOOLEAN /\ twice \in BOOLEAN /\ unknowns \in BOOLEAN

Init ==
  /\ chat = "idle" /\ rows = 0 /\ sent = 0 /\ woken = 0 /\ awaiting = FALSE /\ intent = 0
  /\ faults = {} /\ stage = "none" /\ hi = 0 /\ tries = 0 /\ up = TRUE /\ crashes = 0
  /\ named = {} /\ raced = FALSE /\ seenBusy = FALSE /\ twice = FALSE /\ unknowns = FALSE

Hook == <<chat, rows, awaiting, intent, faults, hi, tries, up, crashes, named, raced, seenBusy, twice, unknowns>>

Deliver ==
  /\ up /\ sent < N /\ stage = "none"
  /\ sent' = sent + 1
  /\ woken' = IF Broken = "nocatchup" /\ chat = "busy" THEN sent + 1 ELSE woken
  /\ UNCHANGED <<stage>> /\ UNCHANGED Hook

\* busy(): idle, and the last wake's turn has started.
Check ==
  /\ up /\ stage = "none" /\ Waiting /= {}
  /\ (Broken /= "nocheck" => chat = "idle")
  /\ (Broken /= "noawait" => ~awaiting)
  /\ stage' = "checked" /\ hi' = sent /\ seenBusy' = (seenBusy \/ chat = "busy")
  /\ UNCHANGED <<chat, rows, sent, woken, awaiting, intent, faults, tries, up, crashes, named, raced, twice, unknowns>>

\* intent, then codex queue (queueOutcome): exit 0 with its Queued message
\* line, the row is out (marked next); the program did not start, a definite
\* failure (tried again up to Limit, then a fault); anything else, unknown
\* (a fault, a row may or may not be out, never tried again by the hook). Each settles the ids
\* through hi: a fault holds back nothing after it.
ExecOk ==
  /\ up /\ stage = "checked"
  /\ rows' = rows + 1 /\ intent' = hi /\ stage' = "queued"
  /\ named' = named \cup Range(woken, hi) /\ twice' = (twice \/ Range(woken, hi) \cap named /= {})
  /\ raced' = (raced \/ chat = "busy")
  /\ UNCHANGED <<chat, sent, woken, awaiting, faults, hi, tries, up, crashes, seenBusy, unknowns>>

ExecFail ==
  /\ up /\ stage = "checked"
  /\ IF tries + 1 < Limit
       THEN tries' = tries + 1 /\ UNCHANGED <<woken, faults>>
       ELSE tries' = 0 /\ woken' = hi /\ faults' = faults \cup Range(woken, hi)
  /\ stage' = "none"
  /\ UNCHANGED <<chat, rows, sent, awaiting, intent, hi, up, crashes, named, raced, seenBusy, twice, unknowns>>

ExecCutOff ==
  /\ up /\ stage = "checked"
  /\ \/ /\ rows' = rows + 1 /\ named' = named \cup Range(woken, hi) /\ twice' = (twice \/ Range(woken, hi) \cap named /= {})
     \/ UNCHANGED <<rows, named, twice>>
  /\ woken' = hi /\ faults' = faults \cup Range(woken, hi) /\ stage' = "none" /\ tries' = 0
  /\ unknowns' = TRUE
  /\ UNCHANGED <<chat, sent, awaiting, intent, hi, up, crashes, raced, seenBusy>>

\* mark: woken takes the intent's record, and the hook waits for the row's
\* turn, unless it has already started (its text is after the size the
\* check read, so a row drained before the mark is seen as drained).
Mark ==
  /\ up /\ stage = "queued"
  /\ woken' = hi /\ awaiting' = (rows > 0) /\ intent' = 0 /\ stage' = "none" /\ tries' = 0
  /\ UNCHANGED <<chat, rows, sent, faults, hi, up, crashes, named, raced, seenBusy, twice, unknowns>>

UserTurn == chat = "idle" /\ chat' = "busy" /\ UNCHANGED <<rows, sent, woken, awaiting, intent, faults, stage, hi, tries, up, crashes, named, raced, seenBusy, twice, unknowns>>
TurnEnd == chat = "busy" /\ chat' = "idle" /\ UNCHANGED <<rows, sent, woken, awaiting, intent, faults, stage, hi, tries, up, crashes, named, raced, seenBusy, twice, unknowns>>

\* The app drains a row into a new turn once the chat is idle; the turn's
\* text is in the rollout, so the hook's wait for it ends.
Drain ==
  /\ chat = "idle" /\ rows > 0
  /\ rows' = rows - 1 /\ chat' = "busy" /\ awaiting' = FALSE
  /\ UNCHANGED <<sent, woken, intent, faults, stage, hi, tries, up, crashes, named, raced, seenBusy, twice, unknowns>>

Crash ==
  /\ up /\ crashes < MaxCrash
  /\ up' = FALSE /\ crashes' = crashes + 1 /\ stage' = "none" /\ tries' = 0
  /\ UNCHANGED <<chat, rows, sent, woken, awaiting, intent, faults, hi, named, raced, seenBusy, twice, unknowns>>

\* A start reconciles an intent: its text in the rollout (the row drained)
\* is evidence, and it is woken; else a fault, result unknown, never queued
\* again ("requeue": its ids wait again). A row still undrained then may
\* have a second behind it: unknowns says so.
Restart ==
  /\ ~up /\ up' = TRUE
  /\ IF intent = 0 THEN UNCHANGED <<woken, faults, unknowns>>
     ELSE IF Broken = "requeue" THEN UNCHANGED <<woken, faults, unknowns>>
     ELSE /\ woken' = intent
          /\ faults' = IF rows = 0 THEN faults ELSE faults \cup Range(woken, intent)
          /\ unknowns' = (unknowns \/ rows > 0)
  /\ intent' = 0 /\ awaiting' = (awaiting /\ intent = 0)
  /\ UNCHANGED <<chat, rows, sent, stage, hi, tries, crashes, named, raced, seenBusy, twice>>

Next == Deliver \/ Check \/ ExecOk \/ ExecFail \/ ExecCutOff \/ Mark \/ UserTurn \/ TurnEnd \/ Drain \/ Crash \/ Restart

Spec == Init /\ [][Next]_vars
          /\ WF_vars(Deliver) /\ WF_vars(TurnEnd) /\ WF_vars(Restart) /\ WF_vars(Mark)
          /\ WF_vars(ExecOk \/ ExecFail \/ ExecCutOff)
          /\ SF_vars(Drain) /\ SF_vars(Check)

\* The hook never queues for a chat it saw busy.
SeenIdle == ~seenBusy

\* No id is in two rows.
NoTwice == ~twice

\* At most one undrained row, unless a crash left a result unknown.
OneRow == rows <= 1 \/ unknowns

\* Every printed id is named in a row or recorded as a fault.
EveryHandled == \A m \in Ids : <>(m \in named \cup faults)

\* Every row is drained.
Drained == <>[](rows = 0)
=============================================================================
