------------------------------- MODULE FriendFollow -------------------------------
\* The daemon follows its row's release (internal/friend/follow.go, docs/SPEC-FRIEND.md
\* "The row is followed"; the owner, 2026-10-07: "nova-config is the thing the runners
\* AUTOMATICALLY FOLLOW"). One daemon runs build `build`; its row names release `row`
\* (set from outside, nova-config friend set --release); its lanes hold cards or not. When
\* row # build the daemon holds (no lane takes a card), fetches the release from a source
\* that may be down, verifies what it fetched (a fetch may answer a wrong binary), stages
\* it, and once every lane is idle swaps it in and restarts under it; a failed fetch is
\* tried again after a tick, never in a tight loop.
\*
\* Mapped to the code:
\*   SetRow      = the beat's answer carries another row_release= (Follower.Want)
\*   Begin       = Follower.Step with want # Build: hold, and the fetch starts
\*   FetchGood   = Fetch and Verify answer the release: staged
\*   FetchBad    = Fetch answers a binary whose version is not the release: dropped, failed
\*   FetchFail   = Fetch fails (the source down): failed
\*   Retry       = the backoff passed: the fetch again
\*   Restart     = idle: Swap then Exec; the daemon comes up under the staged build
\*   LaneTake    = a lane takes a card (laneStep), never while the follow holds
\*   LaneEnd     = a lane's card finishes (laneDone)
\*   SourceDown/Up = the release's host unreachable, reachable (outside)
\*
\* Broken names a reversed witness:
\*   "unverified" -- a bad binary staged and run (no Verify): NeverRunsUnverified fails
\*   "killlanes"  -- the restart does not wait for the lanes: LanesSurvive fails
\*   "noretry"    -- a failed fetch is never tried again: Follows fails
EXTENDS Naturals, FiniteSets

CONSTANTS Builds,     \* the builds a row may name and a daemon may run
          Lanes,      \* her lanes
          Initial,    \* the build the daemon starts on, in Builds
          MaxEvents,  \* a bound on the outside: row changes, source outages, cards taken
          Broken

ASSUME Initial \in Builds
ASSUME Broken \in {"none", "unverified", "killlanes", "noretry"}

VARIABLES build,    \* the build running
          row,      \* the row's release
          lanes,    \* [Lanes -> {"idle", "busy"}]
          phase,    \* "idle" (not following), "fetching", "failed", "staged"
          staged,   \* the build staged, or "none"
          good,     \* the builds verified: what may ever run
          sourceUp, \* the release's host answers
          events,   \* outside events so far
          died      \* lanes whose card died under a restart

vars == <<build, row, lanes, phase, staged, good, sourceUp, events, died>>

Following == row # build
AllIdle == \A l \in Lanes : lanes[l] = "idle"
Busy == {l \in Lanes : lanes[l] = "busy"}

TypeOK ==
  /\ build \in Builds
  /\ row \in Builds
  /\ lanes \in [Lanes -> {"idle", "busy"}]
  /\ phase \in {"idle", "fetching", "failed", "staged"}
  /\ staged \in Builds \cup {"none"}
  /\ good \subseteq Builds
  /\ sourceUp \in BOOLEAN
  /\ events \in 0..MaxEvents
  /\ died \in 0..Cardinality(Lanes)

Init ==
  /\ build = Initial
  /\ row = Initial
  /\ lanes = [l \in Lanes |-> "idle"]
  /\ phase = "idle"
  /\ staged = "none"
  /\ good = {Initial}
  /\ sourceUp = TRUE
  /\ events = 0
  /\ died = 0

\* The row names another release: whatever was staged for the old one is dropped and the
\* follow starts over (Follower.Want).
SetRow(b) ==
  /\ b # row
  /\ events < MaxEvents
  /\ row' = b
  /\ phase' = "idle"
  /\ staged' = "none"
  /\ events' = events + 1
  /\ UNCHANGED <<build, lanes, good, sourceUp, died>>

Begin ==
  /\ Following
  /\ phase = "idle"
  /\ phase' = "fetching"
  /\ UNCHANGED <<build, row, lanes, staged, good, sourceUp, events, died>>

FetchGood ==
  /\ phase = "fetching"
  /\ sourceUp
  /\ phase' = "staged"
  /\ staged' = row
  /\ good' = good \cup {row}
  /\ UNCHANGED <<build, row, lanes, sourceUp, events, died>>

\* The source answers a binary that is not the release (a wrong asset, a cut transfer):
\* verification drops it, and the follow is a failure; the witness stages it anyway.
FetchBad ==
  /\ phase = "fetching"
  /\ sourceUp
  /\ IF Broken = "unverified"
       THEN /\ phase' = "staged"
            /\ staged' = row
       ELSE /\ phase' = "failed"
            /\ staged' = "none"
  /\ UNCHANGED <<build, row, lanes, good, sourceUp, events, died>>

FetchFail ==
  /\ phase = "fetching"
  /\ ~sourceUp
  /\ phase' = "failed"
  /\ UNCHANGED <<build, row, lanes, staged, good, sourceUp, events, died>>

Retry ==
  /\ phase = "failed"
  /\ Broken # "noretry"
  /\ phase' = "fetching"
  /\ UNCHANGED <<build, row, lanes, staged, good, sourceUp, events, died>>

\* The swap and the restart: once every lane is idle (the witness does not wait). A lane
\* busy at the restart loses its card's run.
Restart ==
  /\ phase = "staged"
  /\ AllIdle \/ Broken = "killlanes"
  /\ build' = staged
  /\ phase' = "idle"
  /\ staged' = "none"
  /\ died' = died + Cardinality(Busy)
  /\ lanes' = [l \in Lanes |-> "idle"]
  /\ UNCHANGED <<row, good, sourceUp, events>>

\* A lane takes a card: never while the follow holds (the hold is the whole of the follow,
\* from Begin to Restart).
LaneTake(l) ==
  /\ lanes[l] = "idle"
  /\ ~Following
  /\ events < MaxEvents
  /\ lanes' = [lanes EXCEPT ![l] = "busy"]
  /\ events' = events + 1
  /\ UNCHANGED <<build, row, phase, staged, good, sourceUp, died>>

LaneEnd(l) ==
  /\ lanes[l] = "busy"
  /\ lanes' = [lanes EXCEPT ![l] = "idle"]
  /\ UNCHANGED <<build, row, phase, staged, good, sourceUp, events, died>>

SourceDown ==
  /\ sourceUp
  /\ events < MaxEvents
  /\ sourceUp' = FALSE
  /\ events' = events + 1
  /\ UNCHANGED <<build, row, lanes, phase, staged, good, died>>

SourceUp ==
  /\ ~sourceUp
  /\ sourceUp' = TRUE
  /\ UNCHANGED <<build, row, lanes, phase, staged, good, events, died>>

Next ==
  \/ \E b \in Builds : SetRow(b)
  \/ Begin \/ FetchGood \/ FetchBad \/ FetchFail \/ Retry \/ Restart
  \/ \E l \in Lanes : LaneTake(l) \/ LaneEnd(l)
  \/ SourceDown \/ SourceUp

\* The daemon's own steps are fair, the lanes finish their cards, and the source comes back;
\* the outside (row changes, outages, cards taken) is bounded by MaxEvents, so a changed row
\* is followed once it is quiet.
Fairness ==
  /\ WF_vars(Begin) /\ WF_vars(FetchGood) /\ WF_vars(Retry) /\ WF_vars(Restart)
  /\ WF_vars(SourceUp)
  /\ \A l \in Lanes : WF_vars(LaneEnd(l))

Spec == Init /\ [][Next]_vars /\ Fairness

\* The daemon never runs a build that did not pass verification.
NeverRunsUnverified == build \in good

\* No lane's card dies under a restart: the restart waits for the lanes.
LanesSurvive == died = 0

\* What is staged is for the release the row names now (a row change drops the old stage),
\* so a restart lands on the row's release.
StagedIsTheRow == phase = "staged" => staged = row

\* Once the outside is quiet, the running build is the row's release.
Follows == <>[](build = row)

====================================================================================
