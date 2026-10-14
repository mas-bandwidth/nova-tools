---------------------------- MODULE HookBeat ----------------------------
\* The hook's heartbeat (hook/redis.go beatLua and answerLua, cmd/nova-hook
\* serve.go; docs/SPEC-HOOK.md, the heartbeat). Time is intervals: Tick ends
\* one. Servers pass at most once an interval between them (the server row's
\* at is the once-key); a pass challenges each hooked name, counts a challenge
\* still standing as a miss, and at Limit misses in a row unhooks the name,
\* logging it once, and tells the set of unhooked names once per change. Only
\* the session answers, inside the interval of its challenge; the client sees
\* the server lost after two intervals with no challenge.
EXTENDS Naturals, FiniteSets

CONSTANTS Names, Servers, Limit, MaxTicks, Broken

VARIABLES
    hooked,    \* [Names -> BOOLEAN]: the record's state is hooked
    chal,      \* [Names -> {"none", "open", "late"}]: the standing challenge; late is past its interval
    misses,    \* [Names -> Nat]: challenges in a row left standing
    events,    \* [Names -> Nat]: heartbeat unhooks logged on hook:events
    hooks,     \* [Names -> Nat]: times the name hooked in (its word)
    taken,     \* the pass of this interval is taken
    told,      \* the unhooked set the last pass told
    tells,     \* how many times a set was told
    since,     \* [Names -> Nat]: intervals since the name's last challenge (or its hook)
    ticks,
    passes,    \* passes taken in this interval
    changes    \* passes at which the unhooked set differed from the one told

vars == <<hooked, chal, misses, events, hooks, taken, told, tells, since, ticks, passes, changes>>

Unhooked == {n \in Names : ~hooked[n]}

Init ==
    /\ hooked = [n \in Names |-> TRUE]
    /\ chal = [n \in Names |-> "none"]
    /\ misses = [n \in Names |-> 0]
    /\ events = [n \in Names |-> 0]
    /\ hooks = [n \in Names |-> 1]
    /\ taken = FALSE
    /\ told = {}
    /\ tells = 0
    /\ since = [n \in Names |-> 0]
    /\ ticks = 0
    /\ passes = 0
    /\ changes = 0

\* The interval ends: a standing challenge is late (answerLua refuses it, -4).
Tick ==
    /\ ticks < MaxTicks
    /\ ticks' = ticks + 1
    /\ taken' = FALSE
    /\ passes' = 0
    /\ chal' = [n \in Names |-> IF chal[n] = "open" THEN "late" ELSE chal[n]]
    /\ since' = [n \in Names |-> IF since[n] < 3 THEN since[n] + 1 ELSE 3]
    /\ UNCHANGED <<hooked, misses, events, hooks, told, tells, changes>>

\* One server's pass (beatLua), atomic. Broken "noonce": no once-key.
Pass(s) ==
    /\ ~taken \/ "noonce" \in Broken
    /\ taken' = TRUE
    /\ passes' = passes + 1
    /\ LET m == [n \in Names |-> IF hooked[n] /\ chal[n] # "none" THEN misses[n] + 1 ELSE misses[n]]
           gone == {n \in Names : hooked[n] /\ m[n] >= Limit}
           h2 == [n \in Names |-> hooked[n] /\ n \notin gone]
           set == {n \in Names : ~h2[n]}
       IN /\ hooked' = h2
          /\ misses' = [n \in Names |-> IF hooked[n] THEN m[n] ELSE misses[n]]
          /\ events' = [n \in Names |-> IF n \in gone THEN events[n] + 1 ELSE events[n]]
          /\ chal' = [n \in Names |-> IF h2[n] THEN "open" ELSE IF n \in gone THEN "none" ELSE chal[n]]
          /\ since' = [n \in Names |-> IF h2[n] THEN 0 ELSE since[n]]
          /\ changes' = IF set # told THEN changes + 1 ELSE changes
          /\ IF set # told \/ "tellall" \in Broken
                THEN told' = set /\ tells' = tells + 1
                ELSE UNCHANGED <<told, tells>>
    /\ UNCHANGED <<hooks, ticks>>

\* The session answers its open challenge (answerLua); a late one is refused.
Answer(n) ==
    /\ hooked[n]
    /\ chal[n] = "open"
    /\ chal' = [chal EXCEPT ![n] = "none"]
    /\ misses' = [misses EXCEPT ![n] = 0]
    /\ UNCHANGED <<hooked, events, hooks, taken, told, tells, since, ticks, passes, changes>>

\* The name's own word hooks it in again (armLua: a new epoch, misses cleared).
Hook(n) ==
    /\ ~hooked[n]
    /\ hooks[n] < 2
    /\ hooked' = [hooked EXCEPT ![n] = TRUE]
    /\ hooks' = [hooks EXCEPT ![n] = hooks[n] + 1]
    /\ misses' = [misses EXCEPT ![n] = 0]
    /\ chal' = [chal EXCEPT ![n] = "none"]
    /\ since' = [since EXCEPT ![n] = 0]
    /\ UNCHANGED <<events, taken, told, tells, ticks, passes, changes>>

Next ==
    \/ Tick
    \/ \E s \in Servers : Pass(s)
    \/ \E n \in Names : Answer(n) \/ Hook(n)

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ hooked \in [Names -> BOOLEAN]
    /\ chal \in [Names -> {"none", "open", "late"}]
    /\ misses \in [Names -> 0..Limit]
    /\ events \in [Names -> Nat]
    /\ since \in [Names -> 0..3]

\* A heartbeat unhook is logged once per hooking-in: never twice for one.
UnhookOnce == \A n \in Names : events[n] <= hooks[n]

\* A hooked name has missed fewer than Limit in a row.
UnderLimit == \A n \in Names : hooked[n] => misses[n] < Limit

\* One pass an interval, however many servers pass (Broken "noonce" breaks it).
OnePassPerInterval == passes <= 1

\* The set told is told once per change (Broken "tellall" breaks it).
ToldOncePerChange == tells = changes

\* The client's timeout: a hooked name with a server passing every interval
\* is never two intervals past its last challenge once the interval's pass is taken.
ClientSeesServer == \A n \in Names : hooked[n] /\ taken => since[n] <= 1

\* Reversed witness: the heartbeat unhooks someone. TLC's counterexample shows it is reached.
WitnessUnhook == \A n \in Names : events[n] = 0
=============================================================================
