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
\* a pass challenges each hooked name and counts a challenge still standing as a
\* miss; a miss when the name's last answer (or its hooking in) is DownAfter
\* intervals old unhooks the name, logging it once (2026-10-14: down
\* only after a friend has not talked to us for five minutes, not two missed
\* one-minute challenges; nova-hook serve --down-after). The interval is the
\* model's unit, so Every = 1 and DownAfter counts intervals. The code fires on
\* answered_at older than down-after by TIME, which is at qtime DownAfter or
\* DownAfter + 1 (an answer late in its interval); the model fires at the
\* first, and NoDownBeforeDownAfter holds for both. Only
\* the live model answers (nova-hook prove inside its turn), inside the
\* window of its challenge; a model may go silent, come back and hook in.
\*
\* The proof window (hook/redis.go answerLua and the record's recent, under
\* Parts "window"): a harness may deliver a challenge into the model's turn an
\* interval or more late, after the server issued a newer one. A proof answers
\* any challenge the server issued to that name, in its epoch, within the last
\* DownAfter intervals and not yet spent, not only the newest, and it counts
\* as an answer at the time of the proof. issued holds those challenges with
\* their age in intervals; one DownAfter intervals old has left the window
\* (the code's deadline is its issue time plus down-after by TIME, so an age
\* under DownAfter is inside it whatever the fraction of the interval).
\* Without "window" the challenges are one, answered while open: the cases
\* that check the tell, the client and liveness stay inside their budget, and
\* the window cases check the late answers.
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
\* unhooked, or its last answer (answered_at) is older than DownAfter
\* intervals (the server row's down_after: the same window the server unhooks
\* on), and every friend down when the server row is older than two
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

CONSTANTS Names, Servers, DownAfter, Broken, CoordRole, NoCoord, Parts

\* Silence cases model the production ordering: answer/down < friend quiet <
\* coordinator quiet. Other cases retain the faster scheduler as an
\* overapproximation for old issued nonces and the delivery protocol.
Silence == "silence" \in Parts
QuietAfter == 2 * DownAfter
CoordinatorEvery == 6 * DownAfter
ActivityCap == IF Silence THEN CoordinatorEvery + 1 ELSE DownAfter + 1
ProofLife == 8 * DownAfter
\* A fresh server may pass up to two intervals apart, including work time.
ProofDue == ProofLife - DownAfter - 2
QCap == IF Silence THEN ProofLife + 1 ELSE DownAfter + 1
\* quiet's cap: past the most passes an up name can have gone unanswered.
QuietCap == IF Silence THEN CoordinatorEvery + 3 ELSE DownAfter + 2

\* Parts: what a case turns on, to keep each instance small:
\*   "tell" (the tell and its sends), "judge", "hold", "client", "ask", "bootstrap",
\*   "quiet" (the ghost quiet is counted; else it stays 0), "misses" (misses are
\*   counted, for the old rule and its witness; else they stay 0). Neither decides
\*   a down now, so the cases that do not read them leave them out. "window"
\*   (the issued challenges a proof answers, and the ghost proof).
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
\*   "twomisses"   BadDownEarly: the old rule, down at two missed challenges in a
\*                 row whatever the time since the last answer (NoDownBeforeDownAfter)
\*   "oldnonce"    BadProveOldNonce: a proof answers a challenge older than the
\*                 window (ProofNamesIssuedInWindow)
\*   "othername"   BadProveOtherName: a proof answers another name's challenge
\*                 (ProofNamesIssuedInWindow)
\* BadUnhookAtThree (Limit = 3) is retired with Limit: the down rule is time.

None == <<>>

VARIABLES
    hooked,    \* [Names -> BOOLEAN]: the hook record's state is hooked
    chal,      \* challenge kind: none, server open/late, bootstrap, or server nonce clobbered
    nonceVersion, \* bounded identity bit: a replacement flips it
    delivered, \* durable bus receipt for the current nonce
    beatOwned, \* [Names -> BOOLEAN]: beat_at stands in this epoch, even after Answer
    misses,    \* [Names -> 0..2]: challenges in a row left standing (the reason's count), counted to two
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
    quiet,     \* [Names -> 0..QuietCap]: passes since the model itself last answered or hooked in (ghost)
    qtime,     \* [Names -> 0..QCap]: the hook record's answered_at, as its age in intervals of server time
    elapsed,   \* [Names -> 0..2]: the client's local monotonic intervals since its last valid contact
    alarm,     \* [Names -> BOOLEAN]: the client's own alarm: the server is lost
    held,      \* [Names -> BOOLEAN]: nova-sprint's hold
    ask,       \* [Names -> {"none", "open", "due", "answered", "timedout"}]: one ask to the name
    expiries,  \* [Names -> Nat]: timeouts delivered for the name's ask
    issued,    \* the heartbeat challenges a proof may answer, [to, ep, age] (the record's recent)
    busy, \* authoritative sprint row contains ready or working cards
    lastActivity, \* age of max(last proof, last bus send, arm), in store TIME
    challengedWhileTalking, \* ghost: a pass violated the silence guard
    proof      \* ghost: the last proof, None or [by, to, ep, epNow, age, older]

QuietLimit(n) == IF n \in CoordRole THEN CoordinatorEvery ELSE IF busy[n] THEN DownAfter ELSE QuietAfter

activity == <<busy, lastActivity, challengedWhileTalking>>

tellv == <<told, tv, out, sent, dups, heard>>
judv == <<jst, jkind, jto, jresent>>
server == <<activity, nonceVersion, delivered, beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, tellv, judv, held, ask, expiries, issued, proof>>
client == <<elapsed, alarm>>
vars == <<activity, nonceVersion, delivered, beatOwned, hooked, chal, misses, events, hooks, taken, passed, rowAge, told, tv, out, sent, dups, heard,
          jst, jkind, jto, jresent, alive, quiet, qtime, elapsed, alarm, held, ask, expiries, issued, proof>>

Cap3(x) == IF x < 3 THEN x ELSE 3
CapQ(x) == IF x < QuietCap THEN x ELSE QuietCap
\* misses decide nothing now but the old rule's witness: counted to two.
CapM(x) == IF x < 2 THEN x ELSE 2

\* The oldest age an issued challenge is kept at: inside the window, under
\* DownAfter; under "oldnonce" one interval past it.
MaxAge == IF "oldnonce" \in Broken THEN DownAfter ELSE DownAfter - 1

\* What the friends are to hear, of a hook state h.
UnhookedOf(h) == {n \in Names : ~h[n]}
CoordOf(h) == IF \E n \in CoordRole : h[n] THEN CHOOSE n \in CoordRole : h[n] ELSE NoCoord
StateOf(h) == <<UnhookedOf(h), CoordOf(h)>>
Cur == StateOf(hooked)
States == (SUBSET Names) \X (Names \cup {NoCoord})

\* The server row is older than two intervals: no server is passing.
Stale == rowAge > 2 /\ "nostale" \notin Broken

\* nova-hook's up: hooked, answered by its deadline in server time.
ActivityUp(n) == ~(chal[n] = "late" /\ delivered[n] /\ lastActivity[n] > DownAfter)
HookUp(n) == hooked[n] /\ ((IF Silence THEN ActivityUp(n) ELSE qtime[n] <= DownAfter) \/ "nodeadline" \in Broken)

\* The dashboard's friend row (BLANK-SLATE.md's table), under a live server row.
Table == [h \in BOOLEAN |-> [d \in BOOLEAN |-> IF ~h THEN "down" ELSE IF d THEN "held" ELSE "up"]]
Shown(n) ==
    IF "clientclock" \in Broken /\ alarm[n] THEN "down" ELSE
    IF Stale THEN "down" ELSE
    IF "heldfirst" \in Broken
        THEN IF held[n] THEN "held" ELSE IF ~hooked[n] THEN "down" ELSE "up"
        ELSE Table[HookUp(n)][held[n]]

Init ==
    /\ nonceVersion = [n \in Names |-> FALSE]
    /\ delivered = [n \in Names |-> FALSE]
    /\ busy = [n \in Names |-> FALSE]
    /\ lastActivity = [n \in Names |-> 0]
    /\ challengedWhileTalking = FALSE
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
    /\ issued = {}
    /\ proof = None

\* The interval ends: a standing challenge is late (past its until, still
\* answered inside the window), an issued challenge ages and leaves the
\* window at DownAfter, an open ask and an open judgment are past their due,
\* the row ages.
Tick ==
    /\ "renewal" \notin Parts \/ (rowAge < 2 /\ \A n \in Names : chal[n] # "open")
    /\ lastActivity' = [n \in Names |-> IF "renewal" \in Parts THEN 0 ELSE IF lastActivity[n] < ActivityCap THEN lastActivity[n] + 1 ELSE ActivityCap]
    /\ taken' = FALSE
    /\ passed' = {}
    /\ rowAge' = Cap3(rowAge + 1)
    /\ chal' = [n \in Names |-> IF chal[n] = "open" THEN "late" ELSE IF chal[n] = "bootstrap" THEN "none" ELSE chal[n]]
    /\ ask' = [n \in Names |-> IF ask[n] = "open" THEN "due" ELSE ask[n]]
    /\ jst' = IF jst = "open" THEN "due" ELSE jst
    /\ qtime' = [n \in Names |-> IF qtime[n] < QCap THEN qtime[n] + 1 ELSE QCap]
    /\ issued' = {[c EXCEPT !.age = c.age + 1] : c \in {d \in issued : d.age < MaxAge}}
    /\ UNCHANGED <<nonceVersion, delivered, busy, challengedWhileTalking, beatOwned, hooked, misses, events, hooks, tellv, jkind, jto, jresent, alive, quiet, client, held, expiries, proof>>

\* One server's pass (beatLua), atomic: the beat, the row stamped, and a tell
\* prepared when what the friends are to hear differs from the row's told.
\* Under "markfirst" the pass marks told then and there (the code at 37e261311).
Pass(s) ==
    /\ s \notin passed
    /\ passed' = passed \cup {s}
    /\ ~taken \/ "noonce" \in Broken
    /\ taken' = TRUE
    /\ rowAge' = 0
    /\ LET miss == {n \in Names : hooked[n] /\ beatOwned[n] /\ (IF Silence THEN chal[n] = "late" /\ delivered[n] ELSE chal[n] # "none")}
           m == [n \in Names |-> IF n \in miss THEN CapM(misses[n] + 1) ELSE misses[n]]
           \* down: a miss, and no answer (nor hooking in) for DownAfter intervals;
           \* under "twomisses", the old rule: two misses in a row, whatever the time
           gone == {n \in Names : n \in miss /\ IF "twomisses" \in Broken THEN m[n] >= 2 ELSE ("nodown" \notin Broken /\ IF Silence THEN lastActivity[n] > DownAfter ELSE qtime[n] >= DownAfter)}
           h2 == [n \in Names |-> hooked[n] /\ n \notin gone]
           targets == {n \in Names : h2[n] /\
                       (~Silence \/ (chal[n] \in {"none", "late"} /\
                       (lastActivity[n] > QuietLimit(n) \/ (qtime[n] >= ProofDue /\ "ignoreexpiry" \notin Broken) \/ "talking" \in Broken)))}
           now == StateOf(h2)
       IN /\ hooked' = h2
          /\ beatOwned' = [n \in Names |-> beatOwned[n] \/ n \in targets]
          /\ challengedWhileTalking' = challengedWhileTalking \/
                  (Silence /\ \E n \in targets : lastActivity[n] <= QuietLimit(n) /\ qtime[n] < ProofDue)
          /\ misses' = IF "misses" \in Parts THEN [n \in Names |-> IF hooked[n] THEN m[n] ELSE misses[n]] ELSE misses
          /\ events' = [n \in Names |-> IF n \in gone THEN events[n] + 1 ELSE events[n]]
          /\ nonceVersion' = [n \in Names |-> IF Silence /\ n \in targets THEN ~nonceVersion[n] ELSE nonceVersion[n]]
          /\ delivered' = [n \in Names |-> IF n \in targets THEN "delivery" \notin Parts ELSE IF n \in gone \/ (Silence /\ chal[n] = "late") THEN FALSE ELSE delivered[n]]
          /\ chal' = [n \in Names |-> IF n \in targets THEN "open" ELSE IF n \in gone \/ (Silence /\ chal[n] = "late") THEN "none" ELSE chal[n]]
          \* each challenge set joins the name's recent; an unhook clears it
          /\ issued' = IF "window" \in Parts
                          THEN {c \in issued : c.to \notin gone}
                               \cup {[to |-> x, ep |-> hooks[x], age |-> 0] : x \in targets}
                          ELSE issued
          /\ elapsed' = [n \in Names |-> IF n \in targets THEN 0 ELSE elapsed[n]]
          /\ alarm' = [n \in Names |-> IF n \in targets THEN FALSE ELSE alarm[n]]
          /\ quiet' = IF "quiet" \in Parts THEN [n \in Names |-> IF hooked[n] THEN CapQ(quiet[n] + 1) ELSE quiet[n]] ELSE quiet
          /\ IF now # told /\ "tell" \in Parts
                THEN /\ out' = [out EXCEPT ![s] = [v |-> tv, to |-> now, sent |-> FALSE]]
                     /\ IF "markfirst" \in Broken
                           THEN told' = now /\ tv' = tv + 1
                           ELSE UNCHANGED <<busy, lastActivity, told, tv>>
                ELSE UNCHANGED <<busy, lastActivity, out, told, tv>>
    /\ UNCHANGED <<busy, lastActivity, hooks, sent, dups, heard, judv, alive, qtime, held, ask, expiries, proof>>

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
                  /\ UNCHANGED <<activity, told, tv, sent, dups, heard, judv>>
             ELSE /\ sent' = sent \cup {t.v}
                  /\ dups' = IF t.v \in sent THEN dups + 1 ELSE dups
                  /\ heard' = t.to
                  /\ IF "judge" \in Parts THEN Raise(told, t.to) ELSE UNCHANGED judv
                  /\ IF "markafter" \in Broken
                        THEN /\ out' = [out EXCEPT ![s].sent = TRUE]
                             /\ UNCHANGED <<activity, told, tv>>
                        ELSE /\ out' = [out EXCEPT ![s] = None]
                             /\ told' = t.to
                             /\ tv' = t.v + 1
    /\ UNCHANGED <<activity, beatOwned, hooked, nonceVersion, delivered, chal, misses, events, hooks, taken, passed, rowAge, alive, quiet, qtime, client, held, ask, expiries, issued, proof>>

\* The send fails: nothing is marked, a later pass prepares it again.
TellFails(s) ==
    /\ out[s] # None
    /\ ~out[s].sent
    /\ out' = [out EXCEPT ![s] = None]
    /\ UNCHANGED <<activity, beatOwned, hooked, nonceVersion, delivered, chal, misses, events, hooks, taken, passed, rowAge, told, tv, sent, dups, heard, judv, alive, quiet, qtime, client, held, ask, expiries, issued, proof>>

\* The server crashes holding a tell, before or after its send: the tell in
\* hand is lost; a later pass prepares it again (under "markafter", a crash
\* after the send loses the mark).
Crash(s) ==
    /\ out[s] # None
    /\ out' = [out EXCEPT ![s] = None]
    /\ UNCHANGED <<activity, beatOwned, hooked, nonceVersion, delivered, chal, misses, events, hooks, taken, passed, rowAge, told, tv, sent, dups, heard, judv, alive, quiet, qtime, client, held, ask, expiries, issued, proof>>

\* BadMarkAfterSend: after the send, the row's told is marked, if no one marked it since.
Mark(s) ==
    /\ out[s] # None
    /\ out[s].sent
    /\ IF tv = out[s].v
          THEN told' = out[s].to /\ tv' = tv + 1
          ELSE UNCHANGED <<activity, told, tv>>
    /\ out' = [out EXCEPT ![s] = None]
    /\ UNCHANGED <<activity, beatOwned, hooked, nonceVersion, delivered, chal, misses, events, hooks, taken, passed, rowAge, sent, dups, heard, judv, alive, quiet, qtime, client, held, ask, expiries, issued, proof>>

\* A judgment past its due is pushed again as OVERDUE, once (Judge).
Judge ==
    /\ jst = "due"
    /\ jresent = 0
    /\ jst' = "overdue"
    /\ jresent' = jresent + 1
    /\ UNCHANGED <<activity, beatOwned, hooked, nonceVersion, delivered, chal, misses, events, hooks, taken, passed, rowAge, tellv, jkind, jto, alive, quiet, qtime, client, held, ask, expiries, issued, proof>>

\* A live recipient answers the judgment: closed.
JudgeAnswered ==
    /\ jst \in {"open", "due", "overdue"}
    /\ \E n \in jto : alive[n]
    /\ jst' = "closed"
    /\ UNCHANGED <<activity, beatOwned, hooked, nonceVersion, delivered, chal, misses, events, hooks, taken, passed, rowAge, tellv, jkind, jto, jresent, alive, quiet, qtime, client, held, ask, expiries, issued, proof>>

\* The model answers its current challenge (answerLua): a bootstrap, or,
\* without "window", the heartbeat's while open (AnswerIssued, under
\* "window", answers it late too).
Answer(n) ==
    /\ UNCHANGED nonceVersion
    /\ delivered' = [delivered EXCEPT ![n] = FALSE]
    /\ lastActivity' = [lastActivity EXCEPT ![n] = 0]
    /\ alive[n]
    /\ hooked[n]
    /\ ~Silence \/ delivered[n] \/ chal[n] = "bootstrap"
    /\ chal[n] \in IF "window" \in Parts THEN {"bootstrap"} ELSE {"open", "bootstrap"}
    /\ chal' = [chal EXCEPT ![n] = "none"]
    /\ misses' = [misses EXCEPT ![n] = 0]
    /\ quiet' = [quiet EXCEPT ![n] = 0]
    /\ qtime' = [qtime EXCEPT ![n] = 0]
    /\ UNCHANGED <<busy, challengedWhileTalking, beatOwned, hooked, events, hooks, taken, passed, rowAge, tellv, judv, alive, client, held, ask, expiries, issued, proof>>

\* The youngest challenge issued to n: the one hook:chal holds while it stands.
Youngest(n, c) == \A d \in issued : d.to = n => d.age >= c.age

\* The model proves a heartbeat challenge it was handed (answerLua over the
\* record's recent): any challenge issued to it, in its epoch, inside the
\* window and not yet spent. The proof spends that challenge and counts as an
\* answer now; a newer challenge still standing stays standing (and is a miss
\* at the next pass, which the answer's time keeps from a down). Under
\* "othername" it answers another name's challenge; under "oldnonce" issued
\* keeps challenges one interval past the window.
AnswerIssued(n, c) ==
    /\ UNCHANGED nonceVersion
    /\ delivered' = [delivered EXCEPT ![n] = FALSE]
    /\ lastActivity' = [lastActivity EXCEPT ![n] = 0]
    /\ "window" \in Parts
    /\ alive[n]
    /\ hooked[n]
    /\ c \in issued
    /\ c.to = n \/ "othername" \in Broken
    /\ c.ep = hooks[c.to]
    /\ issued' = issued \ {c}
    /\ chal' = IF c.to = n /\ Youngest(n, c) /\ chal[n] \in {"open", "late"}
                 THEN [chal EXCEPT ![n] = "none"] ELSE chal
    /\ misses' = [misses EXCEPT ![n] = 0]
    /\ quiet' = [quiet EXCEPT ![n] = 0]
    /\ qtime' = [qtime EXCEPT ![n] = 0]
    /\ proof' = [by |-> n, to |-> c.to, ep |-> c.ep, epNow |-> hooks[n], age |-> c.age,
                 older |-> \E d \in issued : d.to = n /\ d.age < c.age]
    /\ UNCHANGED <<busy, challengedWhileTalking, beatOwned, hooked, events, hooks, taken, passed, rowAge, tellv, judv, alive, client, held, ask, expiries>>

\* BadDaemonAnswers: a process answers with no model behind it; the record
\* cannot tell (answered_at is stamped), only the ghost quiet can.
DaemonAnswer(n) ==
    /\ UNCHANGED nonceVersion
    /\ UNCHANGED nonceVersion
    /\ delivered' = [delivered EXCEPT ![n] = FALSE]
    /\ lastActivity' = [lastActivity EXCEPT ![n] = 0]
    /\ "daemon" \in Broken
    /\ hooked[n]
    /\ chal[n] = "open"
    /\ chal' = [chal EXCEPT ![n] = "none"]
    /\ misses' = [misses EXCEPT ![n] = 0]
    /\ qtime' = [qtime EXCEPT ![n] = 0]
    /\ UNCHANGED <<busy, challengedWhileTalking, beatOwned, hooked, events, hooks, taken, passed, rowAge, tellv, judv, alive, quiet, client, held, ask, expiries, issued, proof>>

\* The model's own word hooks it in again (armLua: a new epoch, misses and recent cleared).
Hook(n) ==
    /\ UNCHANGED nonceVersion
    /\ delivered' = [delivered EXCEPT ![n] = FALSE]
    /\ lastActivity' = [lastActivity EXCEPT ![n] = 0]
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
    /\ issued' = {c \in issued : c.to # n}
    /\ UNCHANGED <<busy, challengedWhileTalking, events, taken, passed, rowAge, tellv, judv, alive, held, ask, expiries, proof>>

\* Client startup/periodic challengeLua is one atomic write. beat_at belongs
\* to the epoch, so an answered or expired server challenge does not reopen
\* bootstrap. The broken DEL/HSET erases both the nonce sum and until.
Bootstrap(n) ==
    /\ UNCHANGED nonceVersion
    /\ delivered' = [delivered EXCEPT ![n] = FALSE]
    /\ "bootstrap" \in Parts
    /\ hooked[n]
    /\ IF "bootstrapoverwrite" \in Broken
          THEN chal' = [chal EXCEPT ![n] = IF chal[n] \in {"open", "late", "clobbered"}
                                             THEN "clobbered" ELSE "bootstrap"]
          ELSE /\ ~beatOwned[n] /\ chal[n] = "none"
               /\ chal' = [chal EXCEPT ![n] = "bootstrap"]
    /\ UNCHANGED <<activity, beatOwned, hooked, misses, events, hooks, taken, passed, rowAge,
                   tellv, judv, alive, quiet, qtime, client, held, ask, expiries, issued, proof>>

\* The outbox survives a failed send or crash. Deliver is idempotent for
\* this nonce and cannot move its original deadline. The lost-delivery
\* mutation discards every attempt; strong fairness exposes the lost wake.
Deliver(n) ==
    /\ UNCHANGED nonceVersion
    /\ Silence /\ "delivery" \in Parts /\ "lostdelivery" \notin Broken
    /\ chal[n] = "open" /\ ~delivered[n]
    /\ delivered' = [delivered EXCEPT ![n] = TRUE]
    /\ elapsed' = [elapsed EXCEPT ![n] = 0]
    /\ alarm' = [alarm EXCEPT ![n] = FALSE]
    /\ UNCHANGED <<activity, beatOwned, hooked, chal, misses, events, hooks,
                   taken, passed, rowAge, tellv, judv, alive, quiet, qtime,
                   held, ask, expiries, issued, proof>>

DeliveryFails(n) ==
    /\ Silence /\ "delivery" \in Parts /\ chal[n] = "open" /\ ~delivered[n]
    /\ UNCHANGED vars

\* The sprint row changes outside the hook. Only the server's answer sets
\* this policy input; it is neither a bus send nor a proof.
SetBusy(n) ==
    /\ Silence /\ alive[n] /\ n \notin CoordRole
    /\ busy' = [busy EXCEPT ![n] = ~@]
    /\ quiet' = [quiet EXCEPT ![n] = 0]
    /\ UNCHANGED <<lastActivity, challengedWhileTalking, beatOwned, hooked,
                   nonceVersion, delivered, chal, misses, events, hooks, taken, passed, rowAge, tellv,
                   judv, alive, qtime, client, held, ask, expiries, issued, proof>>

\* A committed send is activity at store TIME, never a proof or an answer.
BusSend(n) ==
    /\ Silence /\ alive[n] /\ hooked[n]
    /\ lastActivity' = [lastActivity EXCEPT ![n] = 0]
    /\ quiet' = [quiet EXCEPT ![n] = 0]
    /\ UNCHANGED <<busy, challengedWhileTalking, beatOwned, hooked, nonceVersion, delivered, chal, misses,
                   events, hooks, taken, passed, rowAge, tellv, judv, alive,
                   qtime, client, held, ask, expiries, issued, proof>>

\* Background traffic is not model activity. The mutation attributes a
\* daemon pong to the absent model and defeats silent detection.
DaemonSend(n) ==
    /\ Silence /\ ~alive[n]
    /\ IF "daemonactivity" \in Broken
          THEN /\ lastActivity' = [lastActivity EXCEPT ![n] = 0]
               /\ quiet' = [quiet EXCEPT ![n] = 0]
          ELSE UNCHANGED <<lastActivity, quiet>>
    /\ UNCHANGED <<busy, challengedWhileTalking, beatOwned, hooked, nonceVersion, delivered, chal,
                   misses, events, hooks, taken, passed, rowAge, tellv, judv,
                   alive, qtime, client, held, ask, expiries, issued, proof>>

\* The world: the model goes silent (a closed app, a limit, a stuck turn) or comes back.
GoSilent(n) == /\ "renewal" \notin Parts /\ alive[n] /\ alive' = [alive EXCEPT ![n] = FALSE]
               /\ UNCHANGED <<server, quiet, qtime, client>>
ComeBack(n) == /\ ~alive[n] /\ alive' = [alive EXCEPT ![n] = TRUE]
               /\ UNCHANGED <<server, quiet, qtime, client>>

\* The sprint sets or clears its hold, any time.
SetHold(n, v) == /\ "hold" \in Parts
                 /\ held[n] # v /\ held' = [held EXCEPT ![n] = v]
                 /\ UNCHANGED <<activity, beatOwned, hooked, nonceVersion, delivered, chal, misses, events, hooks, taken, passed, rowAge, tellv, judv, alive, quiet, qtime, client, ask, expiries, issued, proof>>

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
          /\ UNCHANGED <<activity, beatOwned, hooked, nonceVersion, delivered, chal, misses, events, hooks, taken, passed, rowAge, tellv, judv, alive, quiet, qtime, client, held, expiries, issued, proof>>
Reply(n) == /\ alive[n] /\ ask[n] = "open" /\ ask' = [ask EXCEPT ![n] = "answered"]
            /\ UNCHANGED <<activity, beatOwned, hooked, nonceVersion, delivered, chal, misses, events, hooks, taken, passed, rowAge, tellv, judv, alive, quiet, qtime, client, held, expiries, issued, proof>>
Expire(s, n) == /\ ask[n] = "due"
                /\ ask' = [ask EXCEPT ![n] = "timedout"]
                /\ expiries' = [expiries EXCEPT ![n] = expiries[n] + 1]
                /\ UNCHANGED <<activity, beatOwned, hooked, nonceVersion, delivered, chal, misses, events, hooks, taken, passed, rowAge, tellv, judv, alive, quiet, qtime, client, held, issued, proof>>

Next ==
    \/ Tick
    \/ \E s \in Servers : Pass(s) \/ Tell(s) \/ TellFails(s) \/ Crash(s) \/ Mark(s)
    \/ Judge \/ JudgeAnswered
    \/ \E s \in Servers, n \in Names : Expire(s, n)
    \/ \E n \in Names : \/ Answer(n) \/ DaemonAnswer(n) \/ Hook(n) \/ Bootstrap(n)
                        \/ \E c \in issued : AnswerIssued(n, c)
                        \/ Deliver(n) \/ DeliveryFails(n) \/ DaemonSend(n) \/ SetBusy(n) \/ BusSend(n) \/ GoSilent(n) \/ ComeBack(n)
                        \/ ClientLose(n) \/ ClientElapse(n) \/ ClientAlarm(n)
                        \/ Ask(n) \/ Reply(n)
                        \/ \E v \in BOOLEAN : SetHold(n, v)

Spec == Init /\ [][Next]_vars

\* The server clock ticks, the servers pass, and the sender is fair: a send
\* tried again and again is delivered at last, and a sent tell is marked.
LiveSpec == Spec /\ WF_vars(Tick) /\ WF_vars(\E s \in Servers : Pass(s))
                 /\ \A s \in Servers : SF_vars(Tell(s)) /\ WF_vars(Mark(s))
                 /\ \A n \in Names : SF_vars(Deliver(n))

\* Only the clock is fair: the servers may stop for good.
ClockSpec == Spec /\ WF_vars(Tick)

TypeOK ==
    /\ nonceVersion \in [Names -> BOOLEAN]
    /\ delivered \in [Names -> BOOLEAN]
    /\ busy \in [Names -> BOOLEAN]
    /\ lastActivity \in [Names -> 0..ActivityCap]
    /\ challengedWhileTalking \in BOOLEAN
    /\ hooked \in [Names -> BOOLEAN]
    /\ beatOwned \in [Names -> BOOLEAN]
    /\ chal \in [Names -> {"none", "open", "late", "bootstrap", "clobbered"}]
    /\ misses \in [Names -> 0..2]
    /\ quiet \in [Names -> 0..QuietCap]
    /\ elapsed \in [Names -> 0..2]
    /\ alarm \in [Names -> BOOLEAN]
    /\ held \in [Names -> BOOLEAN]
    /\ rowAge \in 0..3
    /\ told \in States /\ heard \in States
    /\ jst \in {"none", "open", "due", "overdue", "closed"}
    /\ issued \subseteq [to : Names, ep : 1..2, age : 0..DownAfter]

\* The dashboard row is the table applied to the hook record (its state and
\* answered_at) and the hold, under a live server row, and nothing else.
ShownIsFunctionOfTwoRecords ==
    \A n \in Names : Shown(n) = IF rowAge > 2 THEN "down" ELSE Table[hooked[n] /\ (IF Silence THEN ActivityUp(n) ELSE qtime[n] <= DownAfter)][held[n]]

\* Down trumps held: an unhooked friend is shown down whatever the hold.
DownTrumpsHeld == \A n \in Names : ~hooked[n] => Shown(n) = "down"

\* No server, no liveness: a server row older than two intervals shows everyone down.
StaleServerMeansDown == rowAge > 2 => \A n \in Names : Shown(n) = "down"

\* Shown up (or held, which is up) means the model itself answered a challenge,
\* or hooked in, within the last DownAfter intervals of the server clock: at
\* most one pass an interval, and one in the interval of the answer itself.
UpMeansAnsweredRecently == \A n \in Names : Shown(n) \in {"up", "held"} => quiet[n] <= (IF Silence THEN QuietLimit(n) + 2 ELSE DownAfter + 1)

\* The same in server time, whether or not any server passes (an outage, a
\* server coming back): shown up means the model answered within DownAfter
\* intervals by the clock.
UpMeansAnsweredInServerTime == \A n \in Names : Shown(n) \in {"up", "held"} => (IF Silence THEN ActivityUp(n) ELSE qtime[n] <= DownAfter)

\* A friend whose model stops answering is down at last (within DownAfter intervals and a pass).
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
ShownIgnoresClientClock == [][\A n \in Names : UNCHANGED <<activity, delivered[n], chal[n], hooked[n], qtime[n], held[n], rowAge>> => Shown(n)' = Shown(n)]_vars

\* An ask's timeout is delivered once.
AskExpiresOnce == \A n \in Names : expiries[n] <= 1 /\ (expiries[n] = 1 <=> ask[n] = "timedout")

\* The down-after rule (2026-10-14): a name goes down only once it has not answered
\* (nor hooked in) for DownAfter intervals, however many challenges it missed
\* meanwhile. Under "twomisses" (the old rule) it fails: two misses come at
\* qtime 2, under DownAfter = 3.
NoDownBeforeDownAfter ==
    [][\A n \in Names : (hooked[n] /\ ~hooked'[n]) => (IF Silence THEN lastActivity[n] > DownAfter ELSE qtime[n] >= DownAfter)]_vars

\* A hooked name that answers often enough misses challenges and is never
\* unhooked for it: the reversed witness that the new rule lets misses run
\* past the old limit (reached: misses of 2 on a hooked name).
WitnessMissesPastTwo == \A n \in Names : hooked[n] => misses[n] < 2

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

\* The proof window: a proof always names a challenge actually issued to the
\* name that proves, in its epoch, inside the window (BadProveOldNonce and
\* BadProveOtherName fail it).
ProofNamesIssuedInWindow ==
    proof = None \/ (proof.to = proof.by /\ proof.ep = proof.epNow /\ proof.age < DownAfter)

\* A proof counts as an answer at the time of the proof: the name's
\* answered_at is now, so it reads up and is not unhooked for that silence.
ProofRefreshesAnswer ==
    [][proof' # proof /\ proof' # None => qtime'[proof'.by] = 0 /\ hooked'[proof'.by]]_vars

\* Reached: a proof of a challenge older than one still issued to the name
\* (the late delivery the window is for).
WitnessProveOlder == proof = None \/ ~proof.older

\* Reversed witnesses: reached states. TLC's counterexample shows each is reached.
WitnessUnhook == \A n \in Names : events[n] = 0
WitnessHeld == \A n \in Names : Shown(n) # "held"
WitnessOverdue == jst # "overdue"
WitnessCoordDown == ~(jst # "none" /\ jkind = "coorddown")
\* A challenge snapshots the quiet guard at issue time, so a later bus send
\* cannot retroactively make a legitimate pending challenge a violation.
\* A quiet name is challenged early only when proof renewal is due.
NoChallengeWhileTalking == ~challengedWhileTalking

SingleOutstandingNonce ==
    [][\A n \in Names : Silence /\ chal[n] = "open" /\ chal'[n] = "open" => nonceVersion'[n] = nonceVersion[n]]_vars

\* A responsive talking model, under regular passes (renewal), is asked
\* before its existing proof lapses. Bus activity never renews that proof.
TalkingProofStaysFresh == \A n \in Names : qtime[n] <= ProofLife

\* With regular passes a silent name is down by its quiet threshold plus
\* one answer interval and the next pass. quiet counts passes, including
\* the pass in the activity interval; the "nodown" mutation breaks this.
SilentDetectionBound == \A n \in Names :
    Silence /\ ~alive[n] /\ hooked[n] => quiet[n] <= QuietLimit(n) + 2
=============================================================================
