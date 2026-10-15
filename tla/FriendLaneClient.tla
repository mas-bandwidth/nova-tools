-------------------------- MODULE FriendLaneClient --------------------------
(***************************************************************************)
(* The friend lane is a client of the server with no local state (v1.3,    *)
(* pulled forward from v1.5 by the owner on 2026-10-14; the card           *)
(* the-friend-lane-is-a-client-with-no-local-state). Code: the sprint      *)
(* server's lane records (nova-sprint internal/sprint/lane_hold.go,        *)
(* steps_work.go takeOne) and the client inside the friend's supervised    *)
(* hook service (nova-tools internal/friend/client.go, cmd/nova-hook       *)
(* lanes.go). FriendLane.tla is the lane protocol itself; this module is   *)
(* who holds the state, and what a restart, a dead client and a dead       *)
(* model do to it.                                                         *)
(*                                                                         *)
(* The server owns every friend lane's record: the card it holds (srv),    *)
(* the card's attempt generation (gen), the lease (lease, renewed by an    *)
(* ask or a progress, counted down while nobody renews), and the finish.   *)
(* The client holds a cache (cache) it rebuilds from the server's answer   *)
(* and nothing else: a restart starts it empty. The model (the friend's    *)
(* session) works what one notice told it (told) and finishes through the *)
(* server's verb, which applies only when the server's record for that     *)
(* lane holds that card at that generation.                                *)
(*                                                                         *)
(* Liveness evidence is the hook record's alone: the client asks, and so   *)
(* renews, only while the model is proven (the per-minute challenge, only  *)
(* the model answers). A dead client renews nothing, and neither does a    *)
(* live client whose model stopped proving: either way the lease runs out  *)
(* and the server returns the card ready at its next generation.           *)
(*                                                                         *)
(* Time is abstract: a renewing client renews within the bound, so a lease *)
(* ages only while nobody renews (Age).                                    *)
(*                                                                         *)
(* Reversed witnesses (MCFriendLaneClientBroken*.cfg):                     *)
(*   TrustCache     the daemon before: lanes.json survives the restart and *)
(*                  decides a finish (the local file, not the server):     *)
(*                  FinishedOnce fails.                                    *)
(*   RenewUnproven  the client renews whether or not the model proves (a   *)
(*                  service that keeps the lease for a dead session):      *)
(*                  DeadModelReleases fails.                               *)
(*   TickUnstarts   the tick puts a card the friend took back ready while  *)
(*                  its lease is live (2026-10-14 20:30:21 EDT: a friend   *)
(*                  took a card; the tick's friendUnstartedWorking moved it*)
(*                  working -> ready the same second): TakenStaysTaken     *)
(*                  fails.                                                 *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS Cards, Lanes, Bound, MaxRestarts, MaxDeaths,
          TrustCache, RenewUnproven, TickUnstarts

None == "none"
NoHold == <<None, 0>>  \* a cache entry naming no card
ASSUME None \notin Cards /\ Bound > 0
ASSUME TrustCache \in BOOLEAN /\ RenewUnproven \in BOOLEAN /\ TickUnstarts \in BOOLEAN

VARIABLES srv,       \* server: the card each lane holds, or None
          col,       \* server: each card ready, held or done
          gen,       \* server: each card's generation (a returned card's next is one more)
          lease,     \* server: each lane's lease left, 0..Bound
          up,        \* the client process runs
          proven,    \* the model proves (the hook record reads proven)
          cache,     \* the client: each lane's copy of the server's answer, or NoHold
          told,      \* the model: the <<card, gen, lane>> one notice has told it
          finished,  \* ghost: finishes the server applied, per card
          returned,  \* ghost: a hold ended with its lease live, by neither finish nor expiry
          restarts,  \* client crashes so far
          deaths     \* model deaths so far

vars == <<srv, col, gen, lease, up, proven, cache, told, finished, returned, restarts, deaths>>

MaxGen == 2 + MaxRestarts + MaxDeaths
Gens == 1..MaxGen
Hold == (Cards \X Gens)

TypeOK ==
  /\ srv \in [Lanes -> Cards \cup {None}]
  /\ col \in [Cards -> {"ready", "held", "done"}]
  /\ gen \in [Cards -> Gens]
  /\ lease \in [Lanes -> 0..Bound]
  /\ up \in BOOLEAN /\ proven \in BOOLEAN
  /\ cache \in [Lanes -> Hold \cup {NoHold}]
  /\ told \subseteq (Cards \X Gens \X Lanes)
  /\ finished \in [Cards -> 0..2]
  /\ returned \in BOOLEAN
  /\ restarts \in 0..MaxRestarts /\ deaths \in 0..MaxDeaths

Init ==
  /\ srv = [l \in Lanes |-> None]
  /\ col = [c \in Cards |-> "ready"]
  /\ gen = [c \in Cards |-> 1]
  /\ lease = [l \in Lanes |-> 0]
  /\ up = TRUE /\ proven = TRUE
  /\ cache = [l \in Lanes |-> NoHold]
  /\ told = {}
  /\ finished = [c \in Cards |-> 0]
  /\ returned = FALSE
  /\ restarts = 0 /\ deaths = 0

\* the client renews (asks) only while the model proves; the witness renews regardless
Renewing == up /\ (proven \/ RenewUnproven)

\* Ask(l): take --as friend.<name> --lane l. The server answers the card the lane holds
\* (and that answer is the lane heard: its lease renewed), else takes it a ready card, else
\* nothing. The client's cache is whatever the server said.
Ask(l) ==
  /\ Renewing
  /\ IF srv[l] # None
       THEN /\ lease' = [lease EXCEPT ![l] = Bound]
            /\ cache' = [cache EXCEPT ![l] = <<srv[l], gen[srv[l]]>>]
            /\ UNCHANGED <<srv, col>>
       ELSE IF \E c \in Cards : col[c] = "ready"
              THEN \E c \in Cards :
                     /\ col[c] = "ready"
                     /\ srv' = [srv EXCEPT ![l] = c]
                     /\ col' = [col EXCEPT ![c] = "held"]
                     /\ lease' = [lease EXCEPT ![l] = Bound]
                     /\ cache' = [cache EXCEPT ![l] = <<c, gen[c]>>]
              ELSE /\ cache' = [cache EXCEPT ![l] = NoHold]
                   /\ UNCHANGED <<srv, col, lease>>
  /\ UNCHANGED <<gen, up, proven, told, finished, returned, restarts, deaths>>

\* Notify: the one notice ("N cards taken; briefs at ...") tells the model every hold the
\* client's cache names. A restarted client tells again what the server says it holds.
Notify ==
  /\ up /\ proven
  /\ \E l \in Lanes : cache[l] # NoHold /\ <<cache[l][1], cache[l][2], l>> \notin told
  /\ told' = told \cup {<<cache[l][1], cache[l][2], l>> : l \in {k \in Lanes : cache[k] # NoHold}}
  /\ UNCHANGED <<srv, col, gen, lease, up, proven, cache, finished, returned, restarts, deaths>>

\* the server's record for lane l holds card c at generation g
ServerSays(c, g, l) == srv[l] = c /\ gen[c] = g

\* Finish: the model finishes a card it was told, through the server's verb. The design
\* applies it only on the server's record; the witness TrustCache lets the client's local
\* lanes.json decide.
Finish(c, g, l) ==
  /\ proven
  /\ <<c, g, l>> \in told
  /\ IF TrustCache THEN cache[l] = <<c, g>> ELSE ServerSays(c, g, l)
  /\ col' = [col EXCEPT ![c] = "done"]
  /\ srv' = [k \in Lanes |-> IF srv[k] = c THEN None ELSE srv[k]]
  /\ lease' = [k \in Lanes |-> IF srv[k] = c THEN 0 ELSE lease[k]]
  /\ finished' = [finished EXCEPT ![c] = IF @ < 2 THEN @ + 1 ELSE 2]
  /\ cache' = [cache EXCEPT ![l] = NoHold]
  /\ UNCHANGED <<gen, up, proven, told, returned, restarts, deaths>>

\* Progress: the model says the run is alive; applied only on the server's record
Progress(c, g, l) ==
  /\ proven
  /\ <<c, g, l>> \in told
  /\ ServerSays(c, g, l)
  /\ lease' = [lease EXCEPT ![l] = Bound]
  /\ UNCHANGED <<srv, col, gen, up, proven, cache, told, finished, returned, restarts, deaths>>

\* Age(l): time passes for a lease nobody renews
Age(l) ==
  /\ srv[l] # None /\ lease[l] > 0 /\ ~Renewing
  /\ lease' = [lease EXCEPT ![l] = @ - 1]
  /\ UNCHANGED <<srv, col, gen, up, proven, cache, told, finished, returned, restarts, deaths>>

\* Expire(l): the tick returns a card whose lease ran out, ready at its next generation
Expire(l) ==
  /\ srv[l] # None /\ lease[l] = 0
  /\ gen[srv[l]] < MaxGen
  /\ col' = [col EXCEPT ![srv[l]] = "ready"]
  /\ gen' = [gen EXCEPT ![srv[l]] = @ + 1]
  /\ srv' = [srv EXCEPT ![l] = None]
  /\ UNCHANGED <<lease, up, proven, cache, told, finished, returned, restarts, deaths>>

\* TickUnstart(l), the witness only: the tick puts a taken card back ready while its lease
\* is live (friendUnstartedWorking before the fix: a take that was no "start")
TickUnstart(l) ==
  /\ TickUnstarts
  /\ srv[l] # None /\ lease[l] > 0
  /\ col' = [col EXCEPT ![srv[l]] = "ready"]
  /\ srv' = [srv EXCEPT ![l] = None]
  /\ lease' = [lease EXCEPT ![l] = 0]
  /\ returned' = TRUE
  /\ UNCHANGED <<gen, up, proven, cache, told, finished, restarts, deaths>>

\* Crash: the client process dies. Restart: it starts again in an empty working directory,
\* holding nothing; the witness TrustCache keeps its lanes.json.
Crash ==
  /\ up /\ restarts < MaxRestarts
  /\ up' = FALSE /\ restarts' = restarts + 1
  /\ UNCHANGED <<srv, col, gen, lease, proven, cache, told, finished, returned, deaths>>
Restart ==
  /\ ~up
  /\ up' = TRUE
  /\ cache' = IF TrustCache THEN cache ELSE [l \in Lanes |-> NoHold]
  /\ UNCHANGED <<srv, col, gen, lease, proven, told, finished, returned, restarts, deaths>>

\* the model stops proving (its session gone), and comes back
Die ==
  /\ proven /\ deaths < MaxDeaths
  /\ proven' = FALSE /\ deaths' = deaths + 1
  /\ UNCHANGED <<srv, col, gen, lease, up, cache, told, finished, returned, restarts>>
Revive ==
  /\ ~proven
  /\ proven' = TRUE
  /\ UNCHANGED <<srv, col, gen, lease, up, cache, told, finished, returned, restarts, deaths>>

Next ==
  \/ \E l \in Lanes : Ask(l) \/ Age(l) \/ Expire(l) \/ TickUnstart(l)
  \/ Notify
  \/ \E c \in Cards, g \in Gens, l \in Lanes : Finish(c, g, l) \/ Progress(c, g, l)
  \/ Crash \/ Restart \/ Die \/ Revive

Spec == Init /\ [][Next]_vars
        /\ \A l \in Lanes : WF_vars(Ask(l)) /\ WF_vars(Age(l)) /\ WF_vars(Expire(l))
        /\ WF_vars(Notify) /\ WF_vars(Restart)

-----------------------------------------------------------------------------
\* No card is held by two lanes: the server's record is the one owner.
OneLanePerCard ==
  \A l1, l2 \in Lanes : (l1 # l2 /\ srv[l1] # None) => srv[l1] # srv[l2]

\* A card is held exactly when a lane's server record holds it.
HeldIsTheServersRecord ==
  \A c \in Cards : (col[c] = "held") <=> (\E l \in Lanes : srv[l] = c)

\* A card is finished at most once, whatever restarts and returns came between: a restarted
\* client never duplicates a card.
FinishedOnce == \A c \in Cards : finished[c] <= 1

\* A hold ends only by a finish or by its lease running out.
TakenStaysTaken == ~returned

\* A restarted client never loses a card: whatever the server holds for a lane, a renewing
\* client learns it again from the server alone and the model is told it, or the hold ends.
RestartResumes ==
  \A l \in Lanes, c \in Cards :
    (srv[l] = c /\ up /\ proven) ~> (srv[l] # c \/ <<c, gen[c], l>> \in told)

\* The lease bounds a dead client and a dead model: when nobody renews for good, every hold
\* is returned.
DeadClientReleases == (<>[](~up)) => <>[](\A l \in Lanes : srv[l] = None)
DeadModelReleases  == (<>[](~proven)) => <>[](\A l \in Lanes : srv[l] = None)
=============================================================================
