------------------------------ MODULE Presence ------------------------------
\* nova-bus presence: the key bus:presence:<name> and hook processes that
\* compete for it (bus/main.go runHook, hook; bus/redis.go claim, release,
\* Presence). Each process in Procs starts at most once, with its own token:
\* owner is the process whose token the key holds.
\*
\* Time is counted in ticks and kept relative, so the state space is finite:
\* keyLeft is the key's remaining TTL, sinceSet the ticks since the key was
\* last set, since[p] the ticks since p last set it. The key is present, as a
\* GET or who sees it, while it exists and its TTL has not run out; Expire is
\* Redis removing it once it has.
\*
\* Claim is the claim script: set the key unless it is present with another
\* token. A hook claims at start; finding another token it waits, polling,
\* and claims once the key is absent (a supervised restart after a crash
\* waits out the dead hook's key); if the key is still held after W ticks
\* (TTL plus a refresh: a live duplicate), it gives up, exits 1 and releases
\* nothing. It claims again when its refresh is due; a refresh that finds
\* another token ends the hook the same way. The code claims when presenceRefresh has passed and never blocks
\* a read past that, so a running hook claims at the latest R ticks after its
\* last: Tick may not pass that. R stands for the refresh interval plus the
\* time to print and ack one batch.
\*
\* A hook whose stdout blocks (the reader stopped reading) is stuck: it does
\* not refresh, and Tick is not held for it. When the write returns it claims
\* at once (the atomic Unstick stands for the write returning and the next
\* refresh; the code may print the rest of its batch first).
\*
\* Exit is a clean exit (SIGINT, SIGTERM, stdout failing): the release script
\* deletes the key only if it holds this token, and the DEL may fail (the
\* connection is down), leaving the key to its TTL. Crash is a kill or a lost
\* machine: the key is left to its TTL.
\*
\* Broken = "none" is the design. Reversed witnesses:
\*   "noexpire"     the key is set without a TTL (refresh left on): a crashed
\*                  hook stays present (PresentOnlyIfFresh fails)
\*   "norefresh"    the hook never refreshes: a running hook drops out
\*                  (LiveIsPresent fails)
\*   "noclaimcheck" the claim sets the key whatever it holds: a second hook
\*                  starts while the first is live (StartOnlyWhenFree fails)
\*   "nodelcheck"   the release deletes whatever the key holds: a hook that
\*                  exits deletes its successor's key (OnlyOwnerDeletes fails)
\*   "nowait"       a hook that finds another token refuses at once: a
\*                  restart after a crash never claims (WaitClaims fails)
\* LiveIsPresentStrict, which counts a stuck hook too, is the property that
\* is allowed to fail: PresenceStuckStrict.cfg shows it does.
EXTENDS Naturals

CONSTANTS TTL, R, Procs, Broken

ASSUME R < TTL /\ 0 \notin Procs

VARIABLES hs, stuck, since, waited, keyOn, owner, keyTTL, keyLeft, sinceSet, badStart, badDel

vars == <<hs, stuck, since, waited, keyOn, owner, keyTTL, keyLeft, sinceSet, badStart, badDel>>
W == TTL + R
keyVars == <<keyOn, owner, keyTTL, keyLeft, sinceSet>>

Min(a, b) == IF a < b THEN a ELSE b

TypeOK ==
  /\ hs \in [Procs -> {"none", "waiting", "up", "refused", "exited", "crashed"}]
  /\ stuck \in [Procs -> BOOLEAN] /\ since \in [Procs -> 0..R] /\ waited \in [Procs -> 0..W]
  /\ keyOn \in BOOLEAN /\ owner \in Procs \cup {0} /\ keyTTL \in BOOLEAN
  /\ keyLeft \in 0..TTL /\ sinceSet \in 0..TTL
  /\ badStart \in BOOLEAN /\ badDel \in BOOLEAN

Present == keyOn /\ (~keyTTL \/ keyLeft > 0)
Free(p) == ~Present \/ owner = p

Init ==
  /\ hs = [p \in Procs |-> "none"] /\ stuck = [p \in Procs |-> FALSE]
  /\ since = [p \in Procs |-> 0] /\ waited = [p \in Procs |-> 0]
  /\ keyOn = FALSE /\ owner = 0 /\ keyTTL = TRUE /\ keyLeft = 0 /\ sinceSet = TTL
  /\ badStart = FALSE /\ badDel = FALSE

\* The claim script: SET ... PX unless another token is present.
Claim(p) ==
  IF Free(p) \/ Broken = "noclaimcheck"
    THEN /\ keyOn' = TRUE /\ owner' = p /\ keyTTL' = (Broken /= "noexpire")
         /\ keyLeft' = TTL /\ sinceSet' = 0 /\ since' = [since EXCEPT ![p] = 0]
         /\ hs' = [hs EXCEPT ![p] = "up"]
         /\ badStart' = (badStart \/ (hs[p] \in {"none", "waiting"} /\ ~Free(p)))
    ELSE /\ hs' = [hs EXCEPT ![p] = "refused"]
         /\ UNCHANGED <<keyVars, since, badStart>>

Start(p) ==
  /\ hs[p] = "none"
  /\ IF Free(p) \/ Broken = "noclaimcheck" THEN Claim(p)
     ELSE IF Broken = "nowait" THEN hs' = [hs EXCEPT ![p] = "refused"] /\ UNCHANGED <<keyVars, since, badStart>>
     ELSE hs' = [hs EXCEPT ![p] = "waiting"] /\ UNCHANGED <<keyVars, since, badStart>>
  /\ UNCHANGED <<stuck, waited, badDel>>

\* A waiting hook's poll (every second) finds the key absent and claims.
Poll(p) ==
  /\ hs[p] = "waiting" /\ Free(p)
  /\ Claim(p)
  /\ UNCHANGED <<stuck, waited, badDel>>

\* W ticks of waiting and the key is still held: a live duplicate.
GiveUp(p) ==
  /\ hs[p] = "waiting" /\ waited[p] = W /\ ~Free(p)
  /\ hs' = [hs EXCEPT ![p] = "refused"]
  /\ UNCHANGED <<stuck, since, waited, keyVars, badStart, badDel>>

Refresh(p) ==
  /\ hs[p] = "up" /\ ~stuck[p] /\ Broken /= "norefresh"
  /\ Claim(p)
  /\ UNCHANGED <<stuck, waited, badDel>>

Stick(p) ==
  /\ hs[p] = "up" /\ ~stuck[p]
  /\ stuck' = [stuck EXCEPT ![p] = TRUE]
  /\ UNCHANGED <<hs, since, waited, keyVars, badStart, badDel>>

Unstick(p) ==
  /\ hs[p] = "up" /\ stuck[p]
  /\ stuck' = [stuck EXCEPT ![p] = FALSE]
  /\ IF Broken = "norefresh" THEN UNCHANGED <<hs, since, keyVars, badStart>> ELSE Claim(p)
  /\ UNCHANGED <<waited, badDel>>

\* The release script, or a DEL that fails.
Exit(p) ==
  /\ hs[p] = "up"
  /\ hs' = [hs EXCEPT ![p] = "exited"]
  /\ \/ UNCHANGED <<keyVars, badDel>>
     \/ /\ IF Present /\ (owner = p \/ Broken = "nodelcheck")
             THEN keyOn' = FALSE /\ owner' = 0 /\ badDel' = (badDel \/ owner /= p)
             ELSE UNCHANGED <<keyOn, owner, badDel>>
        /\ UNCHANGED <<keyTTL, keyLeft, sinceSet>>
  /\ UNCHANGED <<stuck, since, waited, badStart>>

Crash(p) ==
  /\ hs[p] = "up"
  /\ hs' = [hs EXCEPT ![p] = "crashed"]
  /\ UNCHANGED <<stuck, since, waited, keyVars, badStart, badDel>>

Expire ==
  /\ keyOn /\ keyTTL /\ keyLeft = 0
  /\ keyOn' = FALSE /\ owner' = 0
  /\ UNCHANGED <<hs, stuck, since, waited, keyTTL, keyLeft, sinceSet, badStart, badDel>>

\* Time passes, but not past a running hook's refresh (unless it never
\* refreshes).
Tick ==
  /\ ~\E p \in Procs : hs[p] = "up" /\ ~stuck[p] /\ since[p] = R /\ Broken /= "norefresh"
  /\ keyLeft' = IF keyOn /\ keyTTL /\ keyLeft > 0 THEN keyLeft - 1 ELSE keyLeft
  /\ sinceSet' = Min(sinceSet + 1, TTL)
  /\ since' = [p \in Procs |-> IF hs[p] = "up" THEN Min(since[p] + 1, R) ELSE since[p]]
  /\ waited' = [p \in Procs |-> IF hs[p] = "waiting" THEN Min(waited[p] + 1, W) ELSE waited[p]]
  /\ UNCHANGED <<hs, stuck, keyOn, owner, keyTTL, badStart, badDel>>

Next ==
  \/ Expire \/ Tick
  \/ \E p \in Procs : Start(p) \/ Poll(p) \/ GiveUp(p) \/ Refresh(p) \/ Stick(p) \/ Unstick(p) \/ Exit(p) \/ Crash(p)

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick) /\ WF_vars(Expire)
          /\ \A p \in Procs : WF_vars(Refresh(p)) /\ WF_vars(Poll(p)) /\ WF_vars(GiveUp(p))

\* A name is present only if a hook set the key within the TTL.
PresentOnlyIfFresh == Present => owner \in Procs /\ sinceSet < TTL

\* A running hook that is not stuck on stdout is present, with its token.
LiveIsPresent == \A p \in Procs : hs[p] = "up" /\ ~stuck[p] => Present /\ owner = p

\* The same for every running hook: fails once a stuck hook lapses.
LiveIsPresentStrict == \A p \in Procs : hs[p] = "up" => Present /\ owner = p

\* A hook never starts while another hook's token is present.
StartOnlyWhenFree == ~badStart

\* The key is only ever deleted by the hook whose token it holds.
OnlyOwnerDeletes == ~badDel

\* A hook that exited or crashed is absent within the TTL.
GoneAfterExit == \A p \in Procs : (hs[p] \in {"exited", "crashed", "refused"}) ~> ~(Present /\ owner = p)
\* A waiting hook whose rival crashed sees the name claimed: by itself, or by
\* another hook that got there first.
WaitClaims == \A p \in Procs :
  (hs[p] = "waiting" /\ owner \in Procs /\ hs[owner] = "crashed") ~> (\E q \in Procs : hs[q] = "up" /\ owner = q /\ Present)

\* Waiting ends: the hook claims or gives up.
WaitEnds == \A p \in Procs : (hs[p] = "waiting") ~> (hs[p] \in {"up", "refused"})
=============================================================================
