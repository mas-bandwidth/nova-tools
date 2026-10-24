--------------------------- MODULE HookLastChance ---------------------------
\* The last chance before a heartbeat down (hook/redis.go beatLua; cmd/nova-hook
\* serve.go; 2026-10-24). One name's record as the heartbeat pass decides it.
\* HookBeat.tla holds the pass, the tell and the proof; this module holds only the
\* rule that refines its down: a miss in silence (no answer nor bus send since the
\* challenge) used to unhook at once, and a friend deep in one long turn was
\* unhooked at one missed challenge and back minutes later, her cards stopped
\* each time (one friend, three times on 2026-10-24). Now:
\*   - a name that is not the coordinator and still holds its lease, missing a
\*     challenge in silence, is not unhooked: the same pass sets a fresh challenge
\*     at once (a new nonce, a full answer window), its last chance;
\*   - only a miss of that last chance, in silence, unhooks it: down after two
\*     misses in a row, never at the first;
\*   - a name with no lease cannot hear a challenge, and the coordinator keeps
\*     its own rule, so either is unhooked at its first silent miss, as before;
\*   - a miss while the name talked is no down for anyone: the challenge expires
\*     and a later one is set (HookBeat.tla, Retry).
\* The clock is passes: a challenge is missed at the pass Window passes after it
\* was set (its answer bound). An answer inside the window clears the misses.
\*
\* Broken (reversed witnesses, each must fail):
\*   "onemiss"  the rule before: a first silent miss unhooks a name that holds
\*              its lease (NoDownWithoutLastChance)
\*   "always"   every silent miss is a last chance, never only the first: a name
\*              whose model is gone is never unhooked (GoneIsEventuallyDown)
EXTENDS Naturals

CONSTANTS
    Window,     \* passes from a challenge to its miss (the answer bound)
    IsCoord,    \* the name is the coordinator
    CanRevive,  \* a gone model may come back (FALSE in the liveness instance)
    Broken

VARIABLES
    hooked,     \* the record is hooked
    chal,       \* "none" or "open": a challenge stands
    age,        \* 0..Window: passes since the standing challenge was set
    final,      \* the standing challenge is a last chance (last_chance = beat_at)
    talked,     \* the name was heard (a bus send) since the standing challenge was set
    misses,     \* 0..2: challenges missed in a row since the last answer, counted to two
    lease,      \* the name's bus lease stands (its hook can hear a challenge)
    alive,      \* the model behind the name answers and talks
    downs,      \* ghost: "none", "ok" (every down so far was allowed), "early"
    saved       \* ghost: a last chance was answered and the name stayed hooked

vars == <<hooked, chal, age, final, talked, misses, lease, alive, downs, saved>>

CapM(x) == IF x < 2 THEN x ELSE 2

Init ==
    /\ hooked = TRUE
    /\ chal = "none"
    /\ age = 0
    /\ final = FALSE
    /\ talked = FALSE
    /\ misses = 0
    /\ lease = TRUE
    /\ alive = TRUE
    /\ downs = "none"
    /\ saved = FALSE

\* A pass sets a challenge on a hooked name with none standing (after its quiet,
\* or a retry after an expiry: when is HookBeat.tla's; here any pass may).
Issue ==
    /\ hooked /\ chal = "none"
    /\ chal' = "open" /\ age' = 0 /\ final' = FALSE /\ talked' = FALSE
    /\ UNCHANGED <<hooked, misses, lease, alive, downs, saved>>

\* A pass inside the window: the challenge stands one pass older.
Tick ==
    /\ hooked /\ chal = "open" /\ age < Window
    /\ age' = age + 1
    /\ UNCHANGED <<hooked, chal, final, talked, misses, lease, alive, downs, saved>>

\* The model answers the standing challenge inside its window (nova-hook prove).
Answer ==
    /\ hooked /\ alive /\ lease /\ chal = "open" /\ age < Window
    /\ chal' = "none" /\ misses' = 0 /\ final' = FALSE
    /\ saved' = (saved \/ final)
    /\ UNCHANGED <<hooked, age, talked, lease, alive, downs>>

\* The model sends on the bus: heard, so a miss now is no silence.
Talk ==
    /\ hooked /\ alive /\ chal = "open" /\ ~talked
    /\ talked' = TRUE
    /\ UNCHANGED <<hooked, chal, age, final, misses, lease, alive, downs, saved>>

\* The pass at the window's end: the standing challenge is missed (beatLua's miss).
Miss ==
    /\ hooked /\ chal = "open" /\ age = Window
    /\ LET m == CapM(misses + 1)
           silent == ~talked
           \* the last chance: a first silent miss of a name that holds its lease and
           \* is not the coordinator; under "always" any silent miss, under "onemiss" none
           last == /\ silent /\ ~IsCoord /\ lease /\ "onemiss" \notin Broken
                   /\ (~final \/ "always" \in Broken)
           down == silent /\ ~last
       IN /\ misses' = m
          /\ IF down
                THEN /\ hooked' = FALSE /\ chal' = "none" /\ age' = 0 /\ final' = FALSE
                     /\ talked' = FALSE
                     /\ downs' = IF final \/ ~lease \/ IsCoord
                                    THEN (IF downs = "early" THEN "early" ELSE "ok")
                                    ELSE "early"
                ELSE IF last
                THEN /\ chal' = "open" /\ age' = 0 /\ final' = TRUE /\ talked' = FALSE
                     /\ UNCHANGED <<hooked, downs>>
                \* heard: the challenge expires; a later pass sets another (Issue)
                ELSE /\ chal' = "none" /\ age' = 0 /\ final' = FALSE /\ talked' = FALSE
                     /\ UNCHANGED <<hooked, downs>>
    /\ UNCHANGED <<lease, alive, saved>>

\* The holder's lease lapses or is renewed (its service stops or starts).
Lapse == /\ lease /\ lease' = FALSE /\ UNCHANGED <<hooked, chal, age, final, talked, misses, alive, downs, saved>>
Renew == /\ ~lease /\ lease' = TRUE /\ UNCHANGED <<hooked, chal, age, final, talked, misses, alive, downs, saved>>

\* The model goes (a lapsed Monitor, a closed session) or comes back.
Die == /\ alive /\ alive' = FALSE /\ UNCHANGED <<hooked, chal, age, final, talked, misses, lease, downs, saved>>
Revive == /\ CanRevive /\ ~alive /\ alive' = TRUE /\ UNCHANGED <<hooked, chal, age, final, talked, misses, lease, downs, saved>>

\* An unhooked name hooks in again (armLua: misses and last_chance cleared).
Rearm ==
    /\ ~hooked /\ CanRevive
    /\ hooked' = TRUE /\ chal' = "none" /\ age' = 0 /\ final' = FALSE /\ talked' = FALSE /\ misses' = 0
    /\ UNCHANGED <<lease, alive, downs, saved>>

Next == Issue \/ Tick \/ Answer \/ Talk \/ Miss \/ Lapse \/ Renew \/ Die \/ Revive \/ Rearm

\* The server passes for ever: a challenge is set, ages and is missed.
Spec == Init /\ [][Next]_vars /\ WF_vars(Issue) /\ WF_vars(Tick) /\ WF_vars(Miss)

TypeOK ==
    /\ hooked \in BOOLEAN /\ chal \in {"none", "open"} /\ age \in 0..Window
    /\ final \in BOOLEAN /\ talked \in BOOLEAN /\ misses \in 0..2
    /\ lease \in BOOLEAN /\ alive \in BOOLEAN /\ downs \in {"none", "ok", "early"} /\ saved \in BOOLEAN

\* A name that holds its lease and is not the coordinator is unhooked by the
\* heartbeat only at a miss of its last chance: never at its first silent miss.
NoDownWithoutLastChance == downs # "early"

\* A last chance stands only after a miss.
LastChanceAfterAMiss == final => (chal = "open" /\ misses >= 1)

\* Liveness: a name whose model is gone for good is unhooked (one last chance,
\* never an endless run of them).
GoneIsEventuallyDown == [](hooked /\ ~alive => <>~hooked)

\* Reach witnesses (each must fail, showing the case is reached):
\* a last chance answered keeps the name hooked;
WitnessLastChanceSaves == ~saved
\* the name is unhooked with two misses in the reason.
WitnessDownAtTwo == ~(~hooked /\ misses = 2)
=============================================================================
