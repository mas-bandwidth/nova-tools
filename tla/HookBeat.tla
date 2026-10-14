---------------------------- MODULE HookBeat ----------------------------
\* The v1.3 friend system, one record per fact (docs/SPEC-HOOK.md, v1.3):
\* liveness is nova-hook's
\* record only, the hold is nova-sprint's only, and the dashboard's friend row
\* is a function of those two records and nothing else.
\*
\* The hook's heartbeat (hook/redis.go beatLua and answerLua, cmd/nova-hook
\* serve.go; docs/SPEC-HOOK.md, the heartbeat). The server clock is intervals:
\* Tick ends one (Redis TIME). Every server passes at most once an interval and
\* the pass is taken once between them (the server row's at is the once-key);
\* a pass challenges each hooked name, counts a challenge still standing as a
\* miss, and at Limit misses in a row unhooks the name, logging it once, and
\* tells the set of unhooked names once per change (the row's told). Only the
\* live model answers (nova-hook prove inside its turn), inside the interval
\* of its challenge; a model may go silent and come back and hook in again.
\* Server stamps and deadlines are Redis TIME. A client cannot read Redis TIME
\* when the server or the network is unreachable, so its detector (no
\* challenge for two intervals) runs on its own monotonic clock: elapsed time
\* since its last valid contact, advancing on its own, driving only the
\* client's own alarm and never a record anyone else reads. A client may lose
\* all it holds at any step (NoClientState). Asks carry server
\* deadlines and the expiry delivers each timeout once (pkg/bus ExpireAsks,
\* AddOnce). The sprint's hold is set and cleared at any time.
\*
\* The clock is not a counter: every count here is bounded by the hooks and
\* asks, so the model has finitely many states with no end of time, and the liveness
\* SilentFriendGoesDown is checked under fairness of the tick and the pass.
EXTENDS Naturals, FiniteSets

CONSTANTS Names, Servers, Limit, Broken

\* Broken (reversed witnesses, each must fail):
\*   "noonce"    no once-key: every server's pass is taken
\*   "tellall"   the unhooked set is told every pass
\*   "heldfirst" BadHeldWhileDown: the dashboard reads the hold before the hook
\*   "daemon"    BadDaemonAnswers: a process answers a challenge without the model
\*   "clientclock" BadShownReadsClientClock: the dashboard shows down on a client's alarm
\*   "localtold" BadTwoServersDoubleAnnounce: each server keeps the set it told in its own memory
\* BadUnhookAtThree is Limit = 3 (MCHookBeatBrokenUnhookAtThree.cfg).

VARIABLES
    hooked,    \* [Names -> BOOLEAN]: the hook record's state is hooked
    chal,      \* [Names -> {"none", "open", "late"}]: the standing challenge; late is past its interval
    misses,    \* [Names -> Nat]: challenges in a row left standing
    events,    \* [Names -> Nat]: heartbeat unhooks logged on hook:events
    hooks,     \* [Names -> Nat]: times the name hooked in (its word)
    taken,     \* the pass of this interval is taken (the once-key)
    passed,    \* the servers that passed in this interval
    told,      \* [Servers -> SUBSET Names]: the unhooked set last told, as server s reads it
    tells,     \* how many times a set was told
    changes,   \* passes at which the unhooked set differed from the one told
    ann,       \* [Names -> Nat]: tells that announced the name newly unhooked
    alive,     \* [Names -> BOOLEAN]: the model is there and answers (the world, not a record)
    quiet,     \* [Names -> 0..3]: passes since the model itself last answered or hooked in (ghost)
    elapsed,   \* [Names -> 0..2]: the client's local monotonic intervals since its last valid contact
    alarm,     \* [Names -> BOOLEAN]: the client's own alarm: the server is lost
    held,      \* [Names -> BOOLEAN]: nova-sprint's hold
    ask,       \* [Names -> {"none", "open", "due", "answered", "timedout"}]: one ask to the name
    expiries   \* [Names -> Nat]: timeouts delivered for the name's ask

server == <<hooked, chal, misses, events, hooks, taken, passed, told, tells, changes, ann, held, ask, expiries>>
client == <<elapsed, alarm>>
vars == <<hooked, chal, misses, events, hooks, taken, passed, told, tells, changes, ann, alive, quiet, elapsed, alarm, held, ask, expiries>>

Unhooked == {n \in Names : ~hooked[n]}

\* The dashboard's friend row (BLANK-SLATE.md's table).
Table == [h \in BOOLEAN |-> [d \in BOOLEAN |-> IF ~h THEN "down" ELSE IF d THEN "held" ELSE "up"]]
Shown(n) ==
    IF "clientclock" \in Broken /\ alarm[n] THEN "down" ELSE
    IF "heldfirst" \in Broken
        THEN IF held[n] THEN "held" ELSE IF ~hooked[n] THEN "down" ELSE "up"
        ELSE IF ~hooked[n] THEN "down" ELSE IF held[n] THEN "held" ELSE "up"

Init ==
    /\ hooked = [n \in Names |-> TRUE]
    /\ chal = [n \in Names |-> "none"]
    /\ misses = [n \in Names |-> 0]
    /\ events = [n \in Names |-> 0]
    /\ hooks = [n \in Names |-> 1]
    /\ taken = FALSE
    /\ passed = {}
    /\ told = [s \in Servers |-> {}]
    /\ tells = 0
    /\ changes = 0
    /\ ann = [n \in Names |-> 0]
    /\ alive = [n \in Names |-> TRUE]
    /\ quiet = [n \in Names |-> 0]
    /\ elapsed = [n \in Names |-> 0]
    /\ alarm = [n \in Names |-> FALSE]
    /\ held = [n \in Names |-> FALSE]
    /\ ask = [n \in Names |-> "none"]
    /\ expiries = [n \in Names |-> 0]

Cap3(x) == IF x < 3 THEN x ELSE 3

\* The interval ends: a standing challenge is late (answerLua refuses it, -4),
\* an open ask is past its deadline.
Tick ==
    /\ taken' = FALSE
    /\ passed' = {}
    /\ chal' = [n \in Names |-> IF chal[n] = "open" THEN "late" ELSE chal[n]]
    /\ ask' = [n \in Names |-> IF ask[n] = "open" THEN "due" ELSE ask[n]]
    /\ UNCHANGED <<hooked, misses, events, hooks, told, tells, changes, ann, alive, quiet, client, held, expiries>>

\* One server's pass (beatLua), atomic. Without the once-key ("noonce") every
\* server's pass is taken. Under "localtold" the comparison is with what this
\* server told, held in its own memory; else with the row's told, one for all.
Pass(s) ==
    /\ s \notin passed
    /\ passed' = passed \cup {s}
    /\ ~taken \/ "noonce" \in Broken
    /\ taken' = TRUE
    /\ LET m == [n \in Names |-> IF hooked[n] /\ chal[n] # "none" THEN misses[n] + 1 ELSE misses[n]]
           gone == {n \in Names : hooked[n] /\ m[n] >= Limit}
           h2 == [n \in Names |-> hooked[n] /\ n \notin gone]
           set == {n \in Names : ~h2[n]}
           tell == set # told[s] \/ "tellall" \in Broken
       IN /\ hooked' = h2
          /\ misses' = [n \in Names |-> IF hooked[n] THEN m[n] ELSE misses[n]]
          /\ events' = [n \in Names |-> IF n \in gone THEN events[n] + 1 ELSE events[n]]
          /\ chal' = [n \in Names |-> IF h2[n] THEN "open" ELSE IF n \in gone THEN "none" ELSE chal[n]]
          /\ elapsed' = [n \in Names |-> IF h2[n] THEN 0 ELSE elapsed[n]]
          /\ alarm' = [n \in Names |-> IF h2[n] THEN FALSE ELSE alarm[n]]
          /\ quiet' = [n \in Names |-> IF hooked[n] THEN Cap3(quiet[n] + 1) ELSE quiet[n]]
          /\ changes' = IF set # told[s] THEN changes + 1 ELSE changes
          /\ IF tell
                THEN /\ told' = IF "localtold" \in Broken
                                   THEN [told EXCEPT ![s] = set]
                                   ELSE [x \in Servers |-> set]
                     /\ tells' = tells + 1
                     /\ ann' = [n \in Names |-> IF n \in set \ told[s] THEN ann[n] + 1 ELSE ann[n]]
                ELSE UNCHANGED <<told, tells, ann>>
    /\ UNCHANGED <<hooks, alive, held, ask, expiries>>

\* The model answers its open challenge (answerLua); a late one is refused.
Answer(n) ==
    /\ alive[n]
    /\ hooked[n]
    /\ chal[n] = "open"
    /\ chal' = [chal EXCEPT ![n] = "none"]
    /\ misses' = [misses EXCEPT ![n] = 0]
    /\ quiet' = [quiet EXCEPT ![n] = 0]
    /\ UNCHANGED <<hooked, events, hooks, taken, passed, told, tells, changes, ann, alive, client, held, ask, expiries>>

\* BadDaemonAnswers: a process answers with no model behind it.
DaemonAnswer(n) ==
    /\ "daemon" \in Broken
    /\ hooked[n]
    /\ chal[n] = "open"
    /\ chal' = [chal EXCEPT ![n] = "none"]
    /\ misses' = [misses EXCEPT ![n] = 0]
    /\ UNCHANGED <<hooked, events, hooks, taken, passed, told, tells, changes, ann, alive, quiet, client, held, ask, expiries>>

\* The model's own word hooks it in again (armLua: a new epoch, misses cleared).
Hook(n) ==
    /\ alive[n]
    /\ ~hooked[n]
    /\ hooks[n] < 2
    /\ hooked' = [hooked EXCEPT ![n] = TRUE]
    /\ hooks' = [hooks EXCEPT ![n] = hooks[n] + 1]
    /\ misses' = [misses EXCEPT ![n] = 0]
    /\ chal' = [chal EXCEPT ![n] = "none"]
    /\ elapsed' = [elapsed EXCEPT ![n] = 0]
    /\ alarm' = [alarm EXCEPT ![n] = FALSE]
    /\ quiet' = [quiet EXCEPT ![n] = 0]
    /\ UNCHANGED <<events, taken, passed, told, tells, changes, ann, alive, held, ask, expiries>>

\* The world: the model goes silent (a closed app, a limit, a stuck turn) or comes back.
GoSilent(n) == /\ alive[n] /\ alive' = [alive EXCEPT ![n] = FALSE]
               /\ UNCHANGED <<server, quiet, client>>
ComeBack(n) == /\ ~alive[n] /\ alive' = [alive EXCEPT ![n] = TRUE]
               /\ UNCHANGED <<server, quiet, client>>

\* The sprint sets or clears its hold, any time.
SetHold(n, v) == /\ held[n] # v /\ held' = [held EXCEPT ![n] = v]
                 /\ UNCHANGED <<hooked, chal, misses, events, hooks, taken, passed, told, tells, changes, ann, alive, quiet, client, ask, expiries>>

\* A client loses everything it holds (a restart): the server reads nothing of it.
ClientLose(n) == /\ elapsed' = [elapsed EXCEPT ![n] = 0]
                 /\ alarm' = [alarm EXCEPT ![n] = FALSE]
                 /\ UNCHANGED <<server, alive, quiet>>

\* The client's own clock advances on its own, whatever the server's does.
ClientElapse(n) == /\ elapsed[n] < 2
                   /\ elapsed' = [elapsed EXCEPT ![n] = elapsed[n] + 1]
                   /\ UNCHANGED <<server, alive, quiet, alarm>>

\* Two intervals with no valid contact, by the client's clock: its own alarm.
ClientAlarm(n) == /\ ~alarm[n] /\ elapsed[n] >= 2
                  /\ alarm' = [alarm EXCEPT ![n] = TRUE]
                  /\ UNCHANGED <<server, alive, quiet, elapsed>>

\* An ask to the name with a server deadline; the model answers it by then, or
\* any expiry delivers its timeout once (AddOnce's once-key).
Ask(n) == /\ ask[n] = "none" /\ ask' = [ask EXCEPT ![n] = "open"]
          /\ UNCHANGED <<hooked, chal, misses, events, hooks, taken, passed, told, tells, changes, ann, alive, quiet, client, held, expiries>>
Reply(n) == /\ alive[n] /\ ask[n] = "open" /\ ask' = [ask EXCEPT ![n] = "answered"]
            /\ UNCHANGED <<hooked, chal, misses, events, hooks, taken, passed, told, tells, changes, ann, alive, quiet, client, held, expiries>>
Expire(s, n) == /\ ask[n] = "due"
                /\ ask' = [ask EXCEPT ![n] = "timedout"]
                /\ expiries' = [expiries EXCEPT ![n] = expiries[n] + 1]
                /\ UNCHANGED <<hooked, chal, misses, events, hooks, taken, passed, told, tells, changes, ann, alive, quiet, client, held>>

Next ==
    \/ Tick
    \/ \E s \in Servers : Pass(s)
    \/ \E s \in Servers, n \in Names : Expire(s, n)
    \/ \E n \in Names : \/ Answer(n) \/ DaemonAnswer(n) \/ Hook(n)
                        \/ GoSilent(n) \/ ComeBack(n)
                        \/ ClientLose(n) \/ ClientElapse(n) \/ ClientAlarm(n)
                        \/ Ask(n) \/ Reply(n)
                        \/ \E v \in BOOLEAN : SetHold(n, v)

Spec == Init /\ [][Next]_vars

\* The server clock ticks and the servers pass: the environment the liveness needs.
LiveSpec == Spec /\ WF_vars(Tick) /\ WF_vars(\E s \in Servers : Pass(s))

TypeOK ==
    /\ hooked \in [Names -> BOOLEAN]
    /\ chal \in [Names -> {"none", "open", "late"}]
    /\ misses \in [Names -> 0..Limit]
    /\ quiet \in [Names -> 0..3]
    /\ elapsed \in [Names -> 0..2]
    /\ alarm \in [Names -> BOOLEAN]
    /\ held \in [Names -> BOOLEAN]
    /\ told \in [Servers -> SUBSET Names]

\* The dashboard row is the table applied to the hook record and the hold, and nothing else.
ShownIsFunctionOfTwoRecords == \A n \in Names : Shown(n) = Table[hooked[n]][held[n]]

\* Down trumps held: an unhooked friend is shown down whatever the hold.
DownTrumpsHeld == \A n \in Names : ~hooked[n] => Shown(n) = "down"

\* Shown up (or held, which is up) means the model itself answered a challenge,
\* or hooked in, within the last two intervals of the server clock.
UpMeansAnsweredRecently == \A n \in Names : Shown(n) \in {"up", "held"} => quiet[n] <= 2

\* A friend whose model stops answering is down within two intervals plus one tick.
SilentFriendGoesDown == \A n \in Names : (hooked[n] /\ ~alive[n]) ~> (~hooked[n] \/ alive[n])

\* Each heartbeat unhook is logged once per hooking-in and announced once.
UnhookAnnouncedOnce == \A n \in Names : events[n] <= hooks[n] /\ ann[n] = events[n]

\* Clients hold nothing the server needs: losing a client changes no server
\* record (and every invariant holds with ClientLose possible at any step).
NoClientState == [][\A n \in Names : ClientLose(n) \/ ClientElapse(n) \/ ClientAlarm(n) => UNCHANGED server]_vars

\* Shown never depends on a client clock: no step that leaves the hook record
\* and the hold as they are (a client's clock or alarm moving, say) changes a row.
ShownIgnoresClientClock == [][\A n \in Names : UNCHANGED <<hooked[n], held[n]>> => Shown(n)' = Shown(n)]_vars

\* An ask's timeout is delivered once.
AskExpiresOnce == \A n \in Names : expiries[n] <= 1 /\ (expiries[n] = 1 <=> ask[n] = "timedout")

\* A hooked name has missed fewer than Limit in a row.
UnderLimit == \A n \in Names : hooked[n] => misses[n] < Limit

\* One pass an interval, however many servers pass (Broken "noonce" breaks it).
OnePassPerInterval == Cardinality(passed) <= 1

\* The set told is told once per change (Broken "tellall" breaks it).
ToldOncePerChange == tells = changes

\* The client's timeout is its own: its alarm sounds only after two intervals of
\* its own clock with no valid contact, and a contact silences it.
ClientAlarmOnlyAfterTwo == \A n \in Names : alarm[n] => elapsed[n] >= 2

\* Reversed witness: the heartbeat unhooks someone. TLC's counterexample shows it is reached.
WitnessUnhook == \A n \in Names : events[n] = 0

\* Reversed witness: a friend is shown held. Reached.
WitnessHeld == \A n \in Names : Shown(n) # "held"
=============================================================================
