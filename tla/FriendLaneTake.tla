--------------------------- MODULE FriendLaneTake ---------------------------
(***************************************************************************)
(* A one-shot friend's lanes against the sprint server, the one source of  *)
(* truth for what her lanes work (v1.2.6; the fault of 2026-10-10 20:14Z    *)
(* on a friend host: the sprint started a second after her daemon staged  *)
(* its dealt cards, the lanes ran cards still ready on her row, every      *)
(* finish was refused "not working (it is friend.<name>:ready)", and the   *)
(* cards were set aside, stranded).                                        *)
(*                                                                         *)
(* The server: each card on her row is ready, working or done, and the     *)
(* machine RUNNING or STOPPED. A take (by id) moves ready -> working and   *)
(* is refused while STOPPED (store/engine.go, StartsWork); a finish moves  *)
(* working -> done and is refused while STOPPED or when the card is not    *)
(* working; a stop-return moves working -> ready.                          *)
(*                                                                         *)
(* The daemon: each lane holds a card or none; "given" is the cards it set *)
(* aside, "report" the cards whose outbox holds the lane's own REPORT.md,  *)
(* "ended" the cards whose job carries an ended lane mark (endCard writes *)
(* it; one_lane.go laneHolder refuses every lane a card so marked).       *)
(* pkg/friend/lane_take.go: laneTake (LaneTake, LaneResume), reclaimRefused *)
(* (Reclaim); lanes.go laneDone/endCard (Finish, FinishRefused); stop.go   *)
(* (StopReturn); outbox.go outboxStep (OutboxFinish); a restart forgets    *)
(* the lanes and keeps given and report (Restart).                         *)
(*                                                                         *)
(* Reversed witnesses (MCFriendLaneTakeBroken*.cfg):                       *)
(*   RunReady = TRUE: the code before the fix, a lane started on any card   *)
(*     on her row (inbox.go nextCard), ready or working: NoLaneWithoutTake *)
(*     fails.                                                              *)
(*   ReclaimOn = FALSE: a set-aside card the server still has ready stays  *)
(*     aside: NothingStrandedAcrossStopStart fails.                        *)
(*   ReclaimClearsMark = FALSE: the reclaim leaves the ended lane mark (the *)
(*     first cut of this fix, found by both cold readers): the card is     *)
(*     never taken again, NothingStrandedAcrossStopStart fails.            *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS Cards, Lanes, MaxStops, RunReady, ReclaimOn, ReclaimClearsMark

None == "none"

VARIABLES
    col,      \* the server: card -> "ready" | "working" | "done"
    machine,  \* the server: "RUNNING" | "STOPPED"
    stops,    \* how many times the machine has stopped (bounded: the owner starts it again)
    lane,     \* the daemon: lane -> card | None
    given,    \* the daemon: cards set aside
    report,   \* the daemon: cards whose outbox holds the lane's REPORT.md
    ended     \* the daemon: cards whose job carries an ended lane mark

vars == <<col, machine, stops, lane, given, report, ended>>

Held == {lane[l] : l \in Lanes} \ {None}

Init ==
    /\ col = [c \in Cards |-> "ready"]   \* dealt ready on her row
    /\ machine = "STOPPED"               \* the incident: dealt and staged before the start
    /\ stops = 0
    /\ lane = [l \in Lanes |-> None]
    \* any of them may be the incident's leftovers: set aside with the lane's REPORT.md
    \* while the server still has them ready (what the adopted daemon starts from)
    /\ given \in SUBSET Cards
    /\ report = given
    /\ ended = given

Free(l, c) ==
    /\ lane[l] = None
    /\ c \notin Held
    /\ c \notin given
    /\ c \notin report
    /\ c \notin ended

\* A free lane's take by id: the server moves the card to working while RUNNING; a refused
\* take (STOPPED) changes nothing and is asked again on a later pass (it stays enabled).
LaneTake(l, c) ==
    /\ Free(l, c)
    /\ col[c] = "ready"
    /\ machine = "RUNNING"
    /\ col' = [col EXCEPT ![c] = "working"]
    /\ lane' = [lane EXCEPT ![l] = c]
    /\ UNCHANGED <<machine, stops, given, report, ended>>

\* A card the server already has working on her row and no lane holds: resumed, no take.
LaneResume(l, c) ==
    /\ Free(l, c)
    /\ col[c] = "working"
    /\ lane' = [lane EXCEPT ![l] = c]
    /\ UNCHANGED <<col, machine, stops, given, report, ended>>

\* The code before the fix: a lane starts a card that is ready on her row, untaken.
LaneRunReady(l, c) ==
    /\ RunReady
    /\ Free(l, c)
    /\ col[c] = "ready"
    /\ lane' = [lane EXCEPT ![l] = c]
    /\ UNCHANGED <<col, machine, stops, given, report, ended>>

\* The lane's run ends and its finish is taken.
Finish(l) ==
    /\ lane[l] # None
    /\ machine = "RUNNING"
    /\ col[lane[l]] = "working"
    /\ col' = [col EXCEPT ![lane[l]] = "done"]
    /\ lane' = [lane EXCEPT ![l] = None]
    /\ UNCHANGED <<machine, stops, given, report, ended>>

\* The lane's run ends and its finish is refused (STOPPED, or the card not working): the
\* lane writes its REPORT.md and sets the card aside (laneDone, set_aside).
FinishRefused(l) ==
    /\ lane[l] # None
    /\ \/ machine = "STOPPED"
       \/ col[lane[l]] # "working"
    /\ given' = given \cup {lane[l]}
    /\ report' = report \cup {lane[l]}
    /\ ended' = ended \cup {lane[l]}
    /\ lane' = [lane EXCEPT ![l] = None]
    /\ UNCHANGED <<col, machine, stops>>

\* STOPPED cancels a lane's run and hands its card back (stop-return).
StopReturn(l) ==
    /\ machine = "STOPPED"
    /\ lane[l] # None
    /\ col' = [col EXCEPT ![lane[l]] = IF col[lane[l]] = "working" THEN "ready" ELSE col[lane[l]]]
    /\ lane' = [lane EXCEPT ![l] = None]
    /\ UNCHANGED <<machine, stops, given, report, ended>>

\* The outbox step: a REPORT.md of a card working on her row that no lane runs is finished.
OutboxFinish(c) ==
    /\ c \in report
    /\ c \notin Held
    /\ col[c] = "working"
    /\ machine = "RUNNING"
    /\ col' = [col EXCEPT ![c] = "done"]
    /\ UNCHANGED <<machine, stops, lane, given, report, ended>>

\* reclaimRefused: a set-aside card the server still has ready is held aside no longer, its
\* lane-written REPORT.md moved off and its ended lane mark removed.
Reclaim(c) ==
    /\ ReclaimOn
    /\ c \in given
    /\ col[c] = "ready"
    /\ given' = given \ {c}
    /\ report' = report \ {c}
    /\ ended' = IF ReclaimClearsMark THEN ended \ {c} ELSE ended
    /\ UNCHANGED <<col, machine, stops, lane>>

Stop ==
    /\ machine = "RUNNING"
    /\ stops < MaxStops
    /\ machine' = "STOPPED"
    /\ stops' = stops + 1
    /\ UNCHANGED <<col, lane, given, report, ended>>

Start ==
    /\ machine = "STOPPED"
    /\ machine' = "RUNNING"
    /\ UNCHANGED <<col, stops, lane, given, report, ended>>

\* The daemon restarts: its lanes are gone (the server keeps each card where it is); what it
\* set aside and the reports it wrote are on disk.
Restart ==
    /\ Held # {}
    /\ lane' = [l \in Lanes |-> None]
    /\ UNCHANGED <<col, machine, stops, given, report, ended>>

Next ==
    \/ \E l \in Lanes, c \in Cards : LaneTake(l, c) \/ LaneResume(l, c) \/ LaneRunReady(l, c)
    \/ \E l \in Lanes : Finish(l) \/ FinishRefused(l) \/ StopReturn(l)
    \/ \E c \in Cards : OutboxFinish(c) \/ Reclaim(c)
    \/ Stop \/ Start \/ Restart

Fairness ==
    /\ WF_vars(Start)
    /\ \A l \in Lanes : WF_vars(Finish(l)) /\ WF_vars(StopReturn(l))
    /\ \A l \in Lanes : \A c \in Cards : WF_vars(LaneTake(l, c)) /\ WF_vars(LaneResume(l, c))
    /\ \A c \in Cards : WF_vars(OutboxFinish(c)) /\ WF_vars(Reclaim(c))
    \* her lanes work while the machine runs: a lane holding a working card ends its run
    \* before the next stop (the run is not cut forever by stops)
    /\ \A l \in Lanes : SF_vars(Finish(l))

Spec == Init /\ [][Next]_vars /\ Fairness

TypeOK ==
    /\ col \in [Cards -> {"ready", "working", "done"}]
    /\ machine \in {"RUNNING", "STOPPED"}
    /\ lane \in [Lanes -> Cards \cup {None}]
    /\ given \subseteq Cards
    /\ report \subseteq Cards
    /\ ended \subseteq Cards

\* No lane holds a card the server does not have working on her row.
NoLaneWithoutTake ==
    \A l \in Lanes : lane[l] # None => col[lane[l]] = "working"

\* One lane per card.
OneLanePerCard ==
    \A l1, l2 \in Lanes : (l1 # l2 /\ lane[l1] # None) => lane[l1] # lane[l2]

\* Across any stops and starts, every card dealt to her is eventually done: a refused take
\* is taken again, and nothing set aside on a refusal is stranded.
NothingStrandedAcrossStopStart ==
    \A c \in Cards : <>(col[c] = "done")

\* A card ready on her row while the machine runs is eventually taken (or done).
RefusedTakeIsEventuallyRetaken ==
    \A c \in Cards : (col[c] = "ready") ~> (col[c] \in {"working", "done"})
=============================================================================
