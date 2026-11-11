------------------------------ MODULE CodexWake ------------------------------
\* nova-bus hook --codex-thread (cmd/nova-bus/codexwake.go): a hook that prints every
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
\* in the rollout), intent (dir/intent, 0 for none), fres and flo (dir/faults:
\* fres[h] is the result of the fault whose last id is h, "none" for no such
\* fault, and Range(flo[h], h) its ids), all kept across a crash; stage
\* ("none", "checked", "queued": a row is out, not yet marked), lo and hi
\* (the wake in hand names Range(lo, hi); dir/intent holds both, so they
\* outlive a crash), retry (the fault the wake in hand retries, 0 for none),
\* tries, up, crashes, resolves. The outside: named (ids in a queued row),
\* raced, twice (an id in two rows), extra (a second undrained row: only
\* after a crash left an unknown).
\*
\* A fault marked retry (by a start, for every definite failure, or by hook
\* --resolve <id>=retry) gets one more try. Not modelled: --resolve
\* <id>=woken, --resolve on an intent, an unknown fault woken by its text
\* showing in the rollout, and a retry fault whose saved events are gone
\* (every id here is saved; the code settles it with nothing to queue).
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
\*   "lostretry" a fault marked retry is taken while the chat is busy and
\*               queued by a later check that does not know it: the fault
\*               outlives its row and is queued again, NoTwice fails
EXTENDS Naturals, FiniteSets

CONSTANTS N, MaxCrash, MaxResolve, Limit, Broken

VARIABLES chat, rows, sent, woken, awaiting, intent, fres, flo, stage, lo, hi, retry, tries, up, crashes,
          resolves, named, raced, seenBusy, twice, unknowns

vars == <<chat, rows, sent, woken, awaiting, intent, fres, flo, stage, lo, hi, retry, tries, up, crashes,
          resolves, named, raced, seenBusy, twice, unknowns>>
Ids == 1..N
Range(a, b) == {m \in Ids : a < m /\ m <= b}
Waiting == Range(woken, sent)
Max(a, b) == IF a > b THEN a ELSE b
Faulted == {m \in Ids : \E h \in Ids : fres[h] /= "none" /\ m \in Range(flo[h], h)}

TypeOK ==
  /\ chat \in {"idle", "busy"} /\ rows \in 0..N+1 /\ sent \in 0..N /\ woken \in 0..N
  /\ awaiting \in BOOLEAN /\ intent \in 0..N
  /\ fres \in [Ids -> {"none", "failed", "unknown", "retry"}] /\ flo \in [Ids -> 0..N]
  /\ stage \in {"none", "taken", "checked", "queued"} /\ lo \in 0..N /\ hi \in 0..N /\ retry \in 0..N
  /\ tries \in 0..Limit /\ up \in BOOLEAN /\ crashes \in 0..MaxCrash /\ resolves \in 0..MaxResolve
  /\ named \subseteq Ids
  /\ raced \in BOOLEAN /\ seenBusy \in BOOLEAN /\ twice \in BOOLEAN /\ unknowns \in BOOLEAN

Init ==
  /\ chat = "idle" /\ rows = 0 /\ sent = 0 /\ woken = 0 /\ awaiting = FALSE /\ intent = 0
  /\ fres = [h \in Ids |-> "none"] /\ flo = [h \in Ids |-> 0]
  /\ stage = "none" /\ lo = 0 /\ hi = 0 /\ retry = 0 /\ tries = 0 /\ up = TRUE /\ crashes = 0 /\ resolves = 0
  /\ named = {} /\ raced = FALSE /\ seenBusy = FALSE /\ twice = FALSE /\ unknowns = FALSE

Hook == <<chat, rows, awaiting, intent, fres, flo, lo, hi, retry, tries, up, crashes, resolves, named, raced, seenBusy, twice, unknowns>>

\* The fault for the wake in hand; it replaces one of the same last id.
SetFault(r) == fres' = [fres EXCEPT ![hi] = r] /\ flo' = [flo EXCEPT ![hi] = lo]

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
  /\ stage' = "checked" /\ lo' = woken /\ hi' = sent /\ seenBusy' = (seenBusy \/ chat = "busy")
  /\ UNCHANGED <<chat, rows, sent, woken, awaiting, intent, fres, flo, retry, tries, up, crashes, resolves, named, raced, twice, unknowns>>

\* With nothing waiting, the oldest fault marked retry is taken for one more
\* try (tries starts one short of Limit), by the check that finds the chat
\* idle: the poll that takes a fault is the one that queues and settles it.
\* ("lostretry": it is taken while the chat is busy, and Taken checks it
\* later with the fault forgotten.)
Retry ==
  /\ up /\ stage = "none" /\ Waiting = {}
  /\ \E h \in Ids :
       /\ fres[h] = "retry" /\ \A g \in Ids : fres[g] = "retry" => h <= g
       /\ lo' = flo[h] /\ hi' = h
       /\ IF chat = "idle" /\ ~awaiting
            THEN stage' = "checked" /\ retry' = h
            ELSE Broken = "lostretry" /\ stage' = "taken" /\ retry' = 0
  /\ tries' = Limit - 1
  /\ UNCHANGED <<chat, rows, sent, woken, awaiting, intent, fres, flo, up, crashes, resolves, named, raced, seenBusy, twice, unknowns>>

Taken ==
  /\ up /\ stage = "taken" /\ chat = "idle" /\ ~awaiting
  /\ stage' = "checked"
  /\ UNCHANGED <<chat, rows, sent, woken, awaiting, intent, fres, flo, lo, hi, retry, tries, up, crashes, resolves, named, raced, seenBusy, twice, unknowns>>

\* intent, then codex queue (queueOutcome): exit 0 with its Queued message
\* line, the row is out (marked next); the program did not start, a definite
\* failure (tried again up to Limit, then a fault); anything else, unknown
\* (a fault, a row may or may not be out, never tried again by the hook). Each settles the ids
\* through hi: a fault holds back nothing after it.
ExecOk ==
  /\ up /\ stage = "checked"
  /\ rows' = rows + 1 /\ intent' = hi /\ stage' = "queued"
  /\ named' = named \cup Range(lo, hi) /\ twice' = (twice \/ Range(lo, hi) \cap named /= {})
  /\ raced' = (raced \/ chat = "busy")
  /\ UNCHANGED <<chat, sent, woken, awaiting, fres, flo, lo, hi, retry, tries, up, crashes, resolves, seenBusy, unknowns>>

ExecFail ==
  /\ up /\ stage = "checked"
  /\ IF tries + 1 < Limit
       THEN tries' = tries + 1 /\ UNCHANGED <<woken, fres, flo, lo>>
       ELSE tries' = 0 /\ woken' = Max(woken, hi) /\ SetFault("failed") /\ lo' = 0
  /\ stage' = "none" /\ retry' = 0
  /\ UNCHANGED <<chat, rows, sent, awaiting, intent, hi, up, crashes, resolves, named, raced, seenBusy, twice, unknowns>>

ExecCutOff ==
  /\ up /\ stage = "checked"
  /\ \/ /\ rows' = rows + 1 /\ named' = named \cup Range(lo, hi) /\ twice' = (twice \/ Range(lo, hi) \cap named /= {})
     \/ UNCHANGED <<rows, named, twice>>
  /\ woken' = Max(woken, hi) /\ SetFault("unknown") /\ stage' = "none" /\ lo' = 0 /\ retry' = 0 /\ tries' = 0
  /\ unknowns' = TRUE
  /\ UNCHANGED <<chat, sent, awaiting, intent, hi, up, crashes, resolves, raced, seenBusy>>

\* mark: woken takes the intent's record, the fault the wake retried goes,
\* and the hook waits for the row's turn, unless it has already started (its
\* text is after the size the check read, so a row drained before the mark
\* is seen as drained).
Mark ==
  /\ up /\ stage = "queued"
  /\ woken' = Max(woken, hi) /\ awaiting' = (rows > 0) /\ intent' = 0 /\ stage' = "none" /\ tries' = 0
  /\ IF retry = 0 THEN UNCHANGED <<fres, flo>>
     ELSE fres' = [fres EXCEPT ![retry] = "none"] /\ flo' = [flo EXCEPT ![retry] = 0]
  /\ lo' = 0 /\ retry' = 0
  /\ UNCHANGED <<chat, rows, sent, hi, up, crashes, resolves, named, raced, seenBusy, twice, unknowns>>

UserTurn == chat = "idle" /\ chat' = "busy" /\ UNCHANGED <<rows, sent, woken, awaiting, intent, fres, flo, stage, lo, hi, retry, tries, up, crashes, resolves, named, raced, seenBusy, twice, unknowns>>
TurnEnd == chat = "busy" /\ chat' = "idle" /\ UNCHANGED <<rows, sent, woken, awaiting, intent, fres, flo, stage, lo, hi, retry, tries, up, crashes, resolves, named, raced, seenBusy, twice, unknowns>>

\* The app drains a row into a new turn once the chat is idle; the turn's
\* text is in the rollout, so the hook's wait for it ends.
Drain ==
  /\ chat = "idle" /\ rows > 0
  /\ rows' = rows - 1 /\ chat' = "busy" /\ awaiting' = FALSE
  /\ UNCHANGED <<sent, woken, intent, fres, flo, stage, lo, hi, retry, tries, up, crashes, resolves, named, raced, seenBusy, twice, unknowns>>

Crash ==
  /\ up /\ crashes < MaxCrash
  /\ up' = FALSE /\ crashes' = crashes + 1 /\ stage' = "none" /\ retry' = 0 /\ tries' = 0
  /\ UNCHANGED <<chat, rows, sent, woken, awaiting, intent, fres, flo, lo, hi, resolves, named, raced, seenBusy, twice, unknowns>>

\* A start reconciles an intent: its text in the rollout (the row drained)
\* is evidence, and it is woken, the fault it retried gone; else a fault,
\* result unknown, never queued again by the hook ("requeue": its ids wait
\* again). A row still undrained then may have a second behind it: unknowns
\* says so. Then every definite failure is marked for one more try.
Restart ==
  /\ ~up /\ up' = TRUE
  /\ LET kept == intent = 0 \/ Broken = "requeue"
         res  == IF kept THEN fres ELSE [fres EXCEPT ![intent] = IF rows = 0 THEN "none" ELSE "unknown"]
     IN  /\ fres' = [h \in Ids |-> IF res[h] = "failed" THEN "retry" ELSE res[h]]
         /\ flo' = IF kept THEN flo ELSE [flo EXCEPT ![intent] = IF rows = 0 THEN 0 ELSE lo]
         /\ woken' = IF kept THEN woken ELSE Max(woken, intent)
         /\ unknowns' = (unknowns \/ (~kept /\ rows > 0))
  /\ intent' = 0 /\ awaiting' = (awaiting /\ intent = 0) /\ lo' = 0
  /\ UNCHANGED <<chat, rows, sent, stage, hi, retry, tries, crashes, resolves, named, raced, seenBusy, twice>>

\* hook --resolve <id>=retry, for a fault whose ids its operator has
\* confirmed are in no row: one more try.
Resolve ==
  /\ resolves < MaxResolve /\ resolves' = resolves + 1
  /\ \E h \in Ids :
       /\ fres[h] \in {"failed", "unknown"} /\ Range(flo[h], h) \cap named = {}
       /\ fres' = [fres EXCEPT ![h] = "retry"]
  /\ UNCHANGED <<chat, rows, sent, woken, awaiting, intent, flo, stage, lo, hi, retry, tries, up, crashes, named, raced, seenBusy, twice, unknowns>>

Next == Deliver \/ Check \/ Retry \/ Taken \/ ExecOk \/ ExecFail \/ ExecCutOff \/ Mark \/ UserTurn \/ TurnEnd \/ Drain \/ Crash \/ Restart \/ Resolve

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
EveryHandled == \A m \in Ids : <>(m \in named \cup Faulted)

\* Every row is drained.
Drained == <>[](rows = 0)
=============================================================================
