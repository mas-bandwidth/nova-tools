---------------------------- MODULE HookBeat ----------------------------
\* The v1.3 friend system, one record per fact (docs/SPEC-HOOK.md, v1.3):
\* liveness is nova-hook's records only (each name's record and the server
\* row), the hold is nova-sprint's only, and the dashboard's friend row is a
\* function of those records and nothing else.
\*
\* The hook's heartbeat (hook/redis.go beatLua and answerLua, cmd/nova-hook
\* serve.go; docs/SPEC-HOOK.md, the heartbeat). The server clock is intervals:
\* Tick ends one (Redis TIME). Every server passes at most once an interval and
\* the pass is taken once between them (the server row's at is the once-key);
\* a pass challenges each hooked name, counts a challenge still standing as a
\* miss, and at Limit misses in a row unhooks the name, logging it once. Only
\* the live model answers (nova-hook prove inside its turn), inside the
\* interval of its challenge; a model may go silent, come back and hook in.
\*
\* Telling (serve.go tell): what the friends see is the unhooked set and the
\* coordinator (a hooked name of the coordinator role, CoordRole, chosen
\* the same way every time, as the first in the code).
\* A pass that finds it changed since the row's told prepares one tell; the
\* tell is sent under a once-key (the told version), and the row's told is
\* marked by that same atomic write, so a failed send marks nothing and is
\* sent again by a later pass, a crash leaves no half-told state, and a send
\* repeated is never delivered twice. A tell with friends down or back raises a judgment to the
\* coordinator; the coordinator itself gone with no other raises COORDINATOR
\* DOWN to every live name. A judgment is open until a recipient answers it,
\* and past its due it is pushed again as OVERDUE once (Judge).
\*
\* Down is derived from deadlines in server time, so it holds however the
\* servers run: the dashboard shows a friend down when its record is
\* unhooked, or its last answer (answered_at) is older than UpBound
\* intervals, and every friend down when the server row is older than two
\* intervals (no server, no liveness).
\*
\* Server stamps and deadlines are Redis TIME. A client cannot read Redis TIME
\* when the server or the network is unreachable, so its detector (no
\* challenge for two intervals) runs on its own monotonic clock, driving only
\* the client's own alarm and never a record anyone else reads. A client may
\* lose all it holds at any step (NoClientState). Asks carry server deadlines
\* and the expiry delivers each timeout once (pkg/bus ExpireAsks, AddOnce).
\* The sprint's hold is set and cleared at any time.
\*
\* The clock is not a counter: every count here is bounded by the hooks and
\* asks, so the model has finitely many states, and the liveness properties
\* are checked under fairness of the tick, the pass and the sender.
EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Names, Servers, Limit, Broken, CoordRole, NoCoord, Parts, UpBound

QCap == UpBound + 1

\* Parts: what a case turns on, to keep each instance small:
\*   "tell" (the tell and its sends), "judge", "hold", "client", "ask", "bootstrap".
\* Broken (reversed witnesses, each must fail):
\*   "noonce"      no once-key: every server's pass is taken
\*   "heldfirst"   BadHeldWhileDown: the dashboard reads the hold before the hook
\*   "daemon"      BadDaemonAnswers: a process answers a challenge without the model
\*   "nokey"       BadTwoServersDoubleAnnounce: a tell sent with no once-key
\*   "clientclock" BadShownReadsClientClock: the dashboard shows down on a client's alarm
\*   "markfirst"   BadMarkBeforeSend: the pass marks told before the send (37e261311)
\*   "markafter"   BadMarkAfterSend: told marked in a step after the send, not in its write
\*   "nostale"     BadUpWithServerGone: the dashboard ignores the server row's age
\*   "nodeadline"  BadUpAfterOutage: the dashboard ignores the answer's age
\*   "bootstrapoverwrite" client DEL/HSET replaces a delivered server nonce
\* BadUnhookAtThree is Limit = 3.

None == <<>>

VARIABLES
    hooked,    \* [Names -> BOOLEAN]: the hook record's state is hooked
    chal,      \* challenge kind: none, server open/late, bootstrap, or server nonce clobbered
    beatOwned, \* [Names -> BOOLEAN]: beat_at stands in this epoch, even after Answer
    misses,    \* [Names -> Nat]: challenges in a row left standing
    events,    \* [Names -> Nat]: heartbeat unhooks logged on hook:events
    hooks,     \* [Names -> Nat]: times the name hooked in (its word)
    taken,     \* the pass of this interval is taken (the once-key)
    passed,    \* the servers that passed in this interval
    rowAge,    \* 0..3: intervals since the server row's at (a pass taken)
    told,      \* the row's told: <<unhooked set, coordinator>> last marked told
    tv,        \* the row's told version: marks so far
    out,       \* [Servers -> tell in hand]: None, or [v, from, to, sent]
    sent,      \* the once-keys of tells delivered: <<version, what>>
    dups,      \* deliveries of a once-key already delivered
    heard,     \* what the friends last heard: <<unhooked set, coordinator>>
    jst,       \* the judgment: "none", "open", "due", "overdue", "closed"
    jkind,     \* "change" (to the coordinator) or "coorddown" (to every live name)
    jto,       \* its recipients
    jresent,   \* OVERDUE pushes of the current judgment
    alive,     \* [Names -> BOOLEAN]: the model is there and answers (the world, not a record)
    quiet,     \* [Names -> 0..3]: passes since the model itself last answered or hooked in (ghost)
    qtime,     \* [Names -> 0..QCap]: the hook record's answered_at, as its age in intervals of server time
    elapsed,   \* [Names -> 0..2]: the client's local monotonic intervals since its last valid contact
    alarm,     \* [Names -> BOOLEAN]: the client's own alarm: the server is lost
    held,      \* [Names -> BOOLEAN]: nova-sprint's hold
    ask,       \* [Names -> {"none", "open", "due", "answered", "timedout"}]: one ask to the name
    expiries   \* [Names -> Nat]: timeouts delivered for the name's ask

tellv == <<told, tv, out, sent, dups, heard>>
judv == <<jst, jkind, jto, jresent>>
server == <<beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, tellv, judv, held, ask, expiries>>
client == <<elapsed, alarm>>
vars == <<beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, told, tv, out, sent, dups, heard,
          jst, jkind, jto, jresent, alive, quiet, qtime, elapsed, alarm, held, ask, expiries>>

Cap3(x) == IF x < 3 THEN x ELSE 3

\* What the friends are to hear, of a hook state h.
UnhookedOf(h) == {n \in Names : ~h[n]}
CoordOf(h) == IF \E n \in CoordRole : h[n] THEN CHOOSE n \in CoordRole : h[n] ELSE NoCoord
StateOf(h) == <<UnhookedOf(h), CoordOf(h)>>
Cur == StateOf(hooked)
States == (SUBSET Names) \X (Names \cup {NoCoord})

\* The server row is older than two intervals: no server is passing.
Stale == rowAge > 2 /\ "nostale" \notin Broken

\* nova-hook's up: hooked, answered by its deadline in server time.
HookUp(n) == hooked[n] /\ (qtime[n] <= UpBound \/ "nodeadline" \in Broken)

\* The dashboard's friend row (BLANK-SLATE.md's table), under a live server row.
Table == [h \in BOOLEAN |-> [d \in BOOLEAN |-> IF ~h THEN "down" ELSE IF d THEN "held" ELSE "up"]]
Shown(n) ==
    IF "clientclock" \in Broken /\ alarm[n] THEN "down" ELSE
    IF Stale THEN "down" ELSE
    IF "heldfirst" \in Broken
        THEN IF held[n] THEN "held" ELSE IF ~hooked[n] THEN "down" ELSE "up"
        ELSE Table[HookUp(n)][held[n]]

Init ==
    /\ hooked = [n \in Names |-> TRUE]
    /\ chal = [n \in Names |-> "none"]
    /\ beatOwned = [n \in Names |-> FALSE]
    /\ misses = [n \in Names |-> 0]
    /\ events = [n \in Names |-> 0]
    /\ hooks = [n \in Names |-> 1]
    /\ taken = FALSE
    /\ passed = {}
    /\ rowAge = 0
    /\ told = Cur
    /\ tv = 0
    /\ out = [s \in Servers |-> None]
    /\ sent = {}
    /\ dups = 0
    /\ heard = Cur
    /\ jst = "none"
    /\ jkind = "change"
    /\ jto = {}
    /\ jresent = 0
    /\ alive = [n \in Names |-> TRUE]
    /\ quiet = [n \in Names |-> 0]
    /\ qtime = [n \in Names |-> 0]
    /\ elapsed = [n \in Names |-> 0]
    /\ alarm = [n \in Names |-> FALSE]
    /\ held = [n \in Names |-> FALSE]
    /\ ask = [n \in Names |-> "none"]
    /\ expiries = [n \in Names |-> 0]

\* The interval ends: a standing challenge is late (answerLua refuses it, -4),
\* an open ask and an open judgment are past their due, the row ages.
Tick ==
    /\ taken' = FALSE
    /\ passed' = {}
    /\ rowAge' = Cap3(rowAge + 1)
    /\ chal' = [n \in Names |-> IF chal[n] = "open" THEN "late" ELSE IF chal[n] = "bootstrap" THEN "none" ELSE chal[n]]
    /\ ask' = [n \in Names |-> IF ask[n] = "open" THEN "due" ELSE ask[n]]
    /\ jst' = IF jst = "open" THEN "due" ELSE jst
    /\ qtime' = [n \in Names |-> IF qtime[n] < QCap THEN qtime[n] + 1 ELSE QCap]
    /\ UNCHANGED <<beatOwned, hooked, misses, events, hooks, tellv, jkind, jto, jresent, alive, quiet, client, held, expiries>>

\* One server's pass (beatLua), atomic: the beat, the row stamped, and a tell
\* prepared when what the friends are to hear differs from the row's told.
\* Under "markfirst" the pass marks told then and there (the code at 37e261311).
Pass(s) ==
    /\ s \notin passed
    /\ passed' = passed \cup {s}
    /\ ~taken \/ "noonce" \in Broken
    /\ beatOwned' = [n \in Names |-> beatOwned[n] \/ hooked[n]]
    /\ taken' = TRUE
    /\ rowAge' = 0
    /\ LET m == [n \in Names |-> IF hooked[n] /\ beatOwned[n] /\ chal[n] # "none" THEN misses[n] + 1 ELSE misses[n]]
           gone == {n \in Names : hooked[n] /\ m[n] >= Limit}
           h2 == [n \in Names |-> hooked[n] /\ n \notin gone]
           now == StateOf(h2)
       IN /\ hooked' = h2
          /\ misses' = [n \in Names |-> IF hooked[n] THEN m[n] ELSE misses[n]]
          /\ events' = [n \in Names |-> IF n \in gone THEN events[n] + 1 ELSE events[n]]
          /\ chal' = [n \in Names |-> IF h2[n] THEN "open" ELSE IF n \in gone THEN "none" ELSE chal[n]]
          /\ elapsed' = [n \in Names |-> IF h2[n] THEN 0 ELSE elapsed[n]]
          /\ alarm' = [n \in Names |-> IF h2[n] THEN FALSE ELSE alarm[n]]
          /\ quiet' = [n \in Names |-> IF hooked[n] THEN Cap3(quiet[n] + 1) ELSE quiet[n]]
          /\ IF now # told /\ "tell" \in Parts
                THEN /\ out' = [out EXCEPT ![s] = [v |-> tv, to |-> now, sent |-> FALSE]]
                     /\ IF "markfirst" \in Broken
                           THEN told' = now /\ tv' = tv + 1
                           ELSE UNCHANGED <<told, tv>>
                ELSE UNCHANGED <<out, told, tv>>
    /\ UNCHANGED <<hooks, sent, dups, heard, judv, alive, qtime, held, ask, expiries>>

\* The judgment a tell raises (serve.go tell): friends down or back go to the
\* coordinator; the coordinator gone with none after it, to every live name.
Raise(from, to) ==
    LET down == to[1] \ from[1]
        back == from[1] \ to[1]
        live == Names \ to[1]
    IN IF to[2] # NoCoord /\ down \cup back # {}
          THEN /\ jst' = "open" /\ jkind' = "change" /\ jto' = {to[2]} /\ jresent' = 0
       ELSE IF from[2] # NoCoord /\ to[2] = NoCoord /\ from[2] \in down /\ live \ back # {}
          THEN /\ jst' = "open" /\ jkind' = "coorddown" /\ jto' = live \ back /\ jresent' = 0
       ELSE UNCHANGED judv

\* The tell is sent under its once-key, the told version (Tell), and the
\* row's told is marked in the same atomic write (AddOnce's marks): a key
\* already written is not delivered again (under "nokey" it is). Under
\* "markafter" the mark is a step of its own after the send (Mark), and a
\* crash between the two loses it.
Tell(s) ==
    /\ out[s] # None
    /\ ~out[s].sent
    /\ LET t == out[s]
       IN IF t.v \in sent /\ "nokey" \notin Broken
             THEN /\ out' = [out EXCEPT ![s] = None]
                  /\ UNCHANGED <<told, tv, sent, dups, heard, judv>>
             ELSE /\ sent' = sent \cup {t.v}
                  /\ dups' = IF t.v \in sent THEN dups + 1 ELSE dups
                  /\ heard' = t.to
                  /\ IF "judge" \in Parts THEN Raise(told, t.to) ELSE UNCHANGED judv
                  /\ IF "markafter" \in Broken
                        THEN /\ out' = [out EXCEPT ![s].sent = TRUE]
                             /\ UNCHANGED <<told, tv>>
                        ELSE /\ out' = [out EXCEPT ![s] = None]
                             /\ told' = t.to
                             /\ tv' = t.v + 1
    /\ UNCHANGED <<beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, alive, quiet, qtime, client, held, ask, expiries>>

\* The send fails: nothing is marked, a later pass prepares it again.
TellFails(s) ==
    /\ out[s] # None
    /\ ~out[s].sent
    /\ out' = [out EXCEPT ![s] = None]
    /\ UNCHANGED <<beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, told, tv, sent, dups, heard, judv, alive, quiet, qtime, client, held, ask, expiries>>

\* The server crashes holding a tell, before or after its send: the tell in
\* hand is lost; a later pass prepares it again (under "markafter", a crash
\* after the send loses the mark).
Crash(s) ==
    /\ out[s] # None
    /\ out' = [out EXCEPT ![s] = None]
    /\ UNCHANGED <<beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, told, tv, sent, dups, heard, judv, alive, quiet, qtime, client, held, ask, expiries>>

\* BadMarkAfterSend: after the send, the row's told is marked, if no one marked it since.
Mark(s) ==
    /\ out[s] # None
    /\ out[s].sent
    /\ IF tv = out[s].v
          THEN told' = out[s].to /\ tv' = tv + 1
          ELSE UNCHANGED <<told, tv>>
    /\ out' = [out EXCEPT ![s] = None]
    /\ UNCHANGED <<beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, sent, dups, heard, judv, alive, quiet, qtime, client, held, ask, expiries>>

\* A judgment past its due is pushed again as OVERDUE, once (Judge).
Judge ==
    /\ jst = "due"
    /\ jresent = 0
    /\ jst' = "overdue"
    /\ jresent' = jresent + 1
    /\ UNCHANGED <<beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, tellv, jkind, jto, alive, quiet, qtime, client, held, ask, expiries>>

\* A live recipient answers the judgment: closed.
JudgeAnswered ==
    /\ jst \in {"open", "due", "overdue"}
    /\ \E n \in jto : alive[n]
    /\ jst' = "closed"
    /\ UNCHANGED <<beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, tellv, jkind, jto, jresent, alive, quiet, qtime, client, held, ask, expiries>>

\* The model answers its open challenge (answerLua); a late one is refused.
Answer(n) ==
    /\ alive[n]
    /\ hooked[n]
    /\ chal[n] \in {"open", "bootstrap"}
    /\ chal' = [chal EXCEPT ![n] = "none"]
    /\ misses' = [misses EXCEPT ![n] = 0]
    /\ quiet' = [quiet EXCEPT ![n] = 0]
    /\ qtime' = [qtime EXCEPT ![n] = 0]
    /\ UNCHANGED <<beatOwned, hooked, events, hooks, taken, passed, rowAge, tellv, judv, alive, client, held, ask, expiries>>

\* BadDaemonAnswers: a process answers with no model behind it; the record
\* cannot tell (answered_at is stamped), only the ghost quiet can.
DaemonAnswer(n) ==
    /\ "daemon" \in Broken
    /\ hooked[n]
    /\ chal[n] = "open"
    /\ chal' = [chal EXCEPT ![n] = "none"]
    /\ misses' = [misses EXCEPT ![n] = 0]
    /\ qtime' = [qtime EXCEPT ![n] = 0]
    /\ UNCHANGED <<beatOwned, hooked, events, hooks, taken, passed, rowAge, tellv, judv, alive, quiet, client, held, ask, expiries>>

\* The model's own word hooks it in again (armLua: a new epoch, misses cleared).
Hook(n) ==
    /\ alive[n]
    /\ ~hooked[n]
    /\ hooks[n] < 2
    /\ beatOwned' = [beatOwned EXCEPT ![n] = FALSE]
    /\ hooked' = [hooked EXCEPT ![n] = TRUE]
    /\ hooks' = [hooks EXCEPT ![n] = hooks[n] + 1]
    /\ misses' = [misses EXCEPT ![n] = 0]
    /\ chal' = [chal EXCEPT ![n] = "none"]
    /\ elapsed' = [elapsed EXCEPT ![n] = 0]
    /\ alarm' = [alarm EXCEPT ![n] = FALSE]
    /\ quiet' = [quiet EXCEPT ![n] = 0]
    /\ qtime' = [qtime EXCEPT ![n] = 0]
    /\ UNCHANGED <<events, taken, passed, rowAge, tellv, judv, alive, held, ask, expiries>>

\* Client startup/periodic challengeLua is one atomic write. beat_at belongs
\* to the epoch, so an answered or expired server challenge does not reopen
\* bootstrap. The broken DEL/HSET erases both the nonce sum and until.
Bootstrap(n) ==
    /\ "bootstrap" \in Parts
    /\ hooked[n]
    /\ IF "bootstrapoverwrite" \in Broken
          THEN chal' = [chal EXCEPT ![n] = IF chal[n] \in {"open", "late", "clobbered"}
                                             THEN "clobbered" ELSE "bootstrap"]
          ELSE /\ ~beatOwned[n] /\ chal[n] = "none"
               /\ chal' = [chal EXCEPT ![n] = "bootstrap"]
    /\ UNCHANGED <<beatOwned, hooked, misses, events, hooks, taken, passed, rowAge,
                   tellv, judv, alive, quiet, qtime, client, held, ask, expiries>>

\* The world: the model goes silent (a closed app, a limit, a stuck turn) or comes back.
GoSilent(n) == /\ alive[n] /\ alive' = [alive EXCEPT ![n] = FALSE]
               /\ UNCHANGED <<server, quiet, qtime, client>>
ComeBack(n) == /\ ~alive[n] /\ alive' = [alive EXCEPT ![n] = TRUE]
               /\ UNCHANGED <<server, quiet, qtime, client>>

\* The sprint sets or clears its hold, any time.
SetHold(n, v) == /\ "hold" \in Parts
                 /\ held[n] # v /\ held' = [held EXCEPT ![n] = v]
                 /\ UNCHANGED <<beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, tellv, judv, alive, quiet, qtime, client, ask, expiries>>

\* A client loses everything it holds (a restart): the server reads nothing of it.
ClientLose(n) == /\ "client" \in Parts
                 /\ elapsed' = [elapsed EXCEPT ![n] = 0]
                 /\ alarm' = [alarm EXCEPT ![n] = FALSE]
                 /\ UNCHANGED <<server, alive, quiet, qtime>>

\* The client's own clock advances on its own, whatever the server's does.
ClientElapse(n) == /\ "client" \in Parts
                   /\ elapsed[n] < 2
                   /\ elapsed' = [elapsed EXCEPT ![n] = elapsed[n] + 1]
                   /\ UNCHANGED <<server, alive, quiet, qtime, alarm>>

\* Two intervals with no valid contact, by the client's clock: its own alarm.
ClientAlarm(n) == /\ "client" \in Parts
                  /\ ~alarm[n] /\ elapsed[n] >= 2
                  /\ alarm' = [alarm EXCEPT ![n] = TRUE]
                  /\ UNCHANGED <<server, alive, quiet, qtime, elapsed>>

\* An ask to the name with a server deadline; the model answers it by then, or
\* any expiry delivers its timeout once (AddOnce's once-key).
Ask(n) == /\ "ask" \in Parts
          /\ ask[n] = "none" /\ ask' = [ask EXCEPT ![n] = "open"]
          /\ UNCHANGED <<beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, tellv, judv, alive, quiet, qtime, client, held, expiries>>
Reply(n) == /\ alive[n] /\ ask[n] = "open" /\ ask' = [ask EXCEPT ![n] = "answered"]
            /\ UNCHANGED <<beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, tellv, judv, alive, quiet, qtime, client, held, expiries>>
Expire(s, n) == /\ ask[n] = "due"
                /\ ask' = [ask EXCEPT ![n] = "timedout"]
                /\ expiries' = [expiries EXCEPT ![n] = expiries[n] + 1]
                /\ UNCHANGED <<beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, tellv, judv, alive, quiet, qtime, client, held>>

Next ==
    \/ Tick
    \/ \E s \in Servers : Pass(s) \/ Tell(s) \/ TellFails(s) \/ Crash(s) \/ Mark(s)
    \/ Judge \/ JudgeAnswered
    \/ \E s \in Servers, n \in Names : Expire(s, n)
    \/ \E n \in Names : \/ Answer(n) \/ DaemonAnswer(n) \/ Hook(n) \/ Bootstrap(n)
                        \/ GoSilent(n) \/ ComeBack(n)
                        \/ ClientLose(n) \/ ClientElapse(n) \/ ClientAlarm(n)
                        \/ Ask(n) \/ Reply(n)
                        \/ \E v \in BOOLEAN : SetHold(n, v)

Spec == Init /\ [][Next]_vars

\* The server clock ticks, the servers pass, and the sender is fair: a send
\* tried again and again is delivered at last, and a sent tell is marked.
LiveSpec == Spec /\ WF_vars(Tick) /\ WF_vars(\E s \in Servers : Pass(s))
                 /\ \A s \in Servers : SF_vars(Tell(s)) /\ WF_vars(Mark(s))

\* Only the clock is fair: the servers may stop for good.
ClockSpec == Spec /\ WF_vars(Tick)

TypeOK ==
    /\ hooked \in [Names -> BOOLEAN]
    /\ beatOwned \in [Names -> BOOLEAN]
    /\ chal \in [Names -> {"none", "open", "late", "bootstrap", "clobbered"}]
    /\ misses \in [Names -> 0..Limit]
    /\ quiet \in [Names -> 0..3]
    /\ elapsed \in [Names -> 0..2]
    /\ alarm \in [Names -> BOOLEAN]
    /\ held \in [Names -> BOOLEAN]
    /\ rowAge \in 0..3
    /\ told \in States /\ heard \in States
    /\ jst \in {"none", "open", "due", "overdue", "closed"}

\* The dashboard row is the table applied to the hook record (its state and
\* answered_at) and the hold, under a live server row, and nothing else.
ShownIsFunctionOfTwoRecords ==
    \A n \in Names : Shown(n) = IF rowAge > 2 THEN "down" ELSE Table[hooked[n] /\ qtime[n] <= UpBound][held[n]]

\* Down trumps held: an unhooked friend is shown down whatever the hold.
DownTrumpsHeld == \A n \in Names : ~hooked[n] => Shown(n) = "down"

\* No server, no liveness: a server row older than two intervals shows everyone down.
StaleServerMeansDown == rowAge > 2 => \A n \in Names : Shown(n) = "down"

\* Shown up (or held, which is up) means the model itself answered a challenge,
\* or hooked in, within the last two intervals of the server clock.
UpMeansAnsweredRecently == \A n \in Names : Shown(n) \in {"up", "held"} => quiet[n] <= 2

\* The same in server time, whether or not any server passes (an outage, a
\* server coming back): shown up means the model answered within UpBound
\* intervals by the clock.
UpMeansAnsweredInServerTime == \A n \in Names : Shown(n) \in {"up", "held"} => qtime[n] <= UpBound

\* A friend whose model stops answering is down within two intervals plus one tick.
SilentFriendGoesDown == \A n \in Names : (hooked[n] /\ ~alive[n]) ~> (~hooked[n] \/ alive[n])

\* With every server stopped (only the clock is fair), a silent friend is
\* still shown down: by the unhook, or by the stale server row.
SilentShownDown == \A n \in Names : (~alive[n]) ~> (Shown(n) = "down" \/ alive[n])

\* Each heartbeat unhook is logged once per hooking-in.
UnhookLoggedOnce == \A n \in Names : events[n] <= hooks[n]

\* No tell is delivered twice, however sends fail, marks are lost, or servers race.
NeverToldTwice == dups = 0

\* Every change of what the friends are to hear is heard, unless superseded first.
EveryTransitionToldAtLeastOnce == \A x \in States : (Cur = x /\ heard # x) ~> (heard = x \/ Cur # x)

\* A judgment is pushed OVERDUE at most once.
OverdueOnce == jresent <= 1

\* Clients hold nothing the server needs: losing a client changes no server
\* record (and every invariant holds with ClientLose possible at any step).
NoClientState == [][\A n \in Names : ClientLose(n) \/ ClientElapse(n) \/ ClientAlarm(n) => UNCHANGED server]_vars

\* Shown never depends on a client clock: no step that leaves the hook record,
\* the hold and the server row as they are changes a row.
ShownIgnoresClientClock == [][\A n \in Names : UNCHANGED <<hooked[n], qtime[n], held[n], rowAge>> => Shown(n)' = Shown(n)]_vars

\* An ask's timeout is delivered once.
AskExpiresOnce == \A n \in Names : expiries[n] <= 1 /\ (expiries[n] = 1 <=> ask[n] = "timedout")

\* A hooked name has missed fewer than Limit in a row.
UnderLimit == \A n \in Names : hooked[n] => misses[n] < Limit

\* One pass an interval, however many servers pass (Broken "noonce" breaks it).
OnePassPerInterval == Cardinality(passed) <= 1

\* The client's timeout is its own: its alarm sounds only after two intervals of
\* its own clock with no valid contact, and a contact silences it.
ClientAlarmOnlyAfterTwo == \A n \in Names : alarm[n] => elapsed[n] >= 2

\* A nonce the server delivered remains answerable through its deadline;
\* a client must not replace its sum or remove its deadline.
BootstrapPreservesServerNonce == \A n \in Names : chal[n] # "clobbered"
BootstrapOnlyBeforeHeartbeat == \A n \in Names : beatOwned[n] => chal[n] # "bootstrap"
WitnessBootstrap == \A n \in Names : chal[n] # "bootstrap"

\* Reversed witnesses: reached states. TLC's counterexample shows each is reached.
WitnessUnhook == \A n \in Names : events[n] = 0
WitnessHeld == \A n \in Names : Shown(n) # "held"
WitnessOverdue == jst # "overdue"
WitnessCoordDown == ~(jst # "none" /\ jkind = "coorddown")
=============================================================================
