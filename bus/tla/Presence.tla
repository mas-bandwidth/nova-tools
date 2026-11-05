------------------------------ MODULE Presence ------------------------------
\* nova-bus presence: the key bus:presence:<name> and the hook processes that
\* hold it, one at a time (bus/main.go hook, runHook; bus/redis.go
\* SetPresence, DelPresence, Presence).
\*
\* Time is counted in ticks and kept relative, so the state space is finite:
\* keyLeft is the key's remaining TTL (SET EX), sinceSet the ticks since a hook
\* last set it, sinceRefresh the ticks since the running hook last set it. The
\* key is present, as a GET or who sees it, while it exists and its TTL has not
\* run out; Expire is Redis removing it once it has.
\*
\* The code refreshes when presenceRefresh has passed and never blocks in a
\* read past that, so a running hook sets the key at the latest R ticks after
\* the last set: Tick may not pass a running hook's refresh. R stands for the
\* refresh interval plus the time to print and ack one batch; the code keeps
\* that under the TTL (10 s refresh, 30 s TTL).
\*
\* Start is the hook's first SET (a process not yet at it is not a hook here),
\* Exit a clean exit (SIGINT, SIGTERM, a stdout error: DEL), Crash a kill or a
\* lost machine (the key is left to its TTL).
\*
\* Broken = "none" is the design. Reversed witnesses:
\*   "noexpire"   the key is set without a TTL and the hook forgets to
\*                refresh: it still shows present long after its last set,
\*                and forever after a crash (PresentOnlyIfFresh fails)
\*   "norefresh"  the hook forgets to refresh: a running hook drops out
\*                (LiveIsPresent fails)
EXTENDS Naturals

CONSTANTS TTL, R, MaxStarts, Broken

ASSUME R < TTL

VARIABLES hook, starts, keyOn, keyTTL, keyLeft, sinceSet, sinceRefresh

vars == <<hook, starts, keyOn, keyTTL, keyLeft, sinceSet, sinceRefresh>>

Min(a, b) == IF a < b THEN a ELSE b

TypeOK ==
  /\ hook \in {"none", "up", "exited", "crashed"} /\ starts \in 0..MaxStarts
  /\ keyOn \in BOOLEAN /\ keyTTL \in BOOLEAN /\ keyLeft \in 0..TTL
  /\ sinceSet \in 0..TTL /\ sinceRefresh \in 0..R

Present == keyOn /\ (~keyTTL \/ keyLeft > 0)

Init ==
  /\ hook = "none" /\ starts = 0
  /\ keyOn = FALSE /\ keyTTL = TRUE /\ keyLeft = 0
  /\ sinceSet = TTL /\ sinceRefresh = 0

\* SET bus:presence:<name> <start> EX TTL; under "noexpire", no EX.
Set ==
  /\ keyOn' = TRUE /\ keyTTL' = (Broken /= "noexpire") /\ keyLeft' = TTL
  /\ sinceSet' = 0 /\ sinceRefresh' = 0

Start ==
  /\ hook /= "up" /\ starts < MaxStarts
  /\ hook' = "up" /\ starts' = starts + 1
  /\ Set

Refresh ==
  /\ hook = "up" /\ Broken = "none"
  /\ Set
  /\ UNCHANGED <<hook, starts>>

Exit ==
  /\ hook = "up"
  /\ hook' = "exited" /\ keyOn' = FALSE
  /\ UNCHANGED <<starts, keyTTL, keyLeft, sinceSet, sinceRefresh>>

Crash ==
  /\ hook = "up"
  /\ hook' = "crashed"
  /\ UNCHANGED <<starts, keyOn, keyTTL, keyLeft, sinceSet, sinceRefresh>>

Expire ==
  /\ keyOn /\ keyTTL /\ keyLeft = 0
  /\ keyOn' = FALSE
  /\ UNCHANGED <<hook, starts, keyTTL, keyLeft, sinceSet, sinceRefresh>>

\* Time passes, but not past a running hook's refresh (the design only).
Tick ==
  /\ ~(hook = "up" /\ Broken = "none" /\ sinceRefresh = R)
  /\ keyLeft' = IF keyOn /\ keyTTL /\ keyLeft > 0 THEN keyLeft - 1 ELSE keyLeft
  /\ sinceSet' = Min(sinceSet + 1, TTL)
  /\ sinceRefresh' = IF hook = "up" THEN Min(sinceRefresh + 1, R) ELSE sinceRefresh
  /\ UNCHANGED <<hook, starts, keyOn, keyTTL>>

Next == Start \/ Refresh \/ Exit \/ Crash \/ Expire \/ Tick

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick) /\ WF_vars(Expire)

\* A name is present only if a hook started and set the key within the TTL.
PresentOnlyIfFresh == Present => starts > 0 /\ sinceSet < TTL

\* A running hook is always present.
LiveIsPresent == hook = "up" => Present

\* A clean exit leaves the name absent.
ExitedIsAbsent == hook = "exited" => ~Present

\* A crashed hook's presence goes away, unless a new hook takes the name.
CrashLapses == (hook = "crashed") ~> (~Present \/ hook = "up")
=============================================================================
