--------------------------- MODULE MCSprintEvents ---------------------------
\* The small instances of SprintEvents: 2 streams (s1, s2), 2 members (m1,
\* m2), 1 reader (r1; 2 in the review cases), 1 tick process (2 where two
\* loops race), 1 verb process, 1 or 2 op names for drop --stream, and some of
\* the cards p1 (s1), p2 (s2), p3 (s1) and the sentinel g1 (s1). Each configuration picks its cards, its initial state (a
\* scenario below), the verbs its verb process may run (Menu), its bounds,
\* the members that beat (Beaters may stop and beat again; Steady never
\* stop), the repairs it runs with (Fixes, {} for the design as written),
\* and the witness it turns on (Broken). Scores: even numbers are the
\* integers add allocates (the counter next is the next one); rank and
\* insertion choose odd ones below the counter (U2).
EXTENDS SprintEvents

MCStreams == {"s1", "s2"}
MCMembers == {"m1", "m2"}
MCReaders == {"r1"}
MCReaders2 == {"r1", "r2"}
MCTicks1 == {"t1"}
MCTicks2 == {"t1", "t2"}
MCVerbs == {"v1"}
MCOps == {"o1"}
MCOps2 == {"o1", "o2"}
MCAll == {"p1", "p2", "p3", "g1"}
MCStreamOf == [c \in MCAll |-> IF c = "p2" THEN "s2" ELSE "s1"]
MCOrd == [c \in MCAll |-> CASE c = "p1" -> 1 [] c = "p2" -> 2 [] c = "p3" -> 3 [] OTHER -> 4]
MCFromCounter == [c \in MCAll |-> -1]
\* An insertion: p1 at 3 and the sentinel g1 at 5, odd scores below the counter.
MCAddOdd == [c \in MCAll |-> CASE c = "p1" -> 3 [] c = "g1" -> 5 [] OTHER -> -1]
MCNoNeeds == [c \in MCAll |-> {}]
MCP2NeedsP1 == [c \in MCAll |-> IF c = "p2" THEN {"p1"} ELSE {}]
MCP2NeedsP1P3 == [c \in MCAll |-> IF c = "p2" THEN {"p1", "p3"} ELSE {}]
MCP3NeedsP2 == [c \in MCAll |-> IF c = "p3" THEN {"p2"} ELSE {}]
MCNone == {}
MCRank3 == {3}
MCRank5 == {5}
MCRank6 == {6}
MCRank7 == {7}

Cols4(a, b, c, d) == [x \in MCAll |-> CASE x = "p1" -> a [] x = "p2" -> b [] x = "p3" -> c [] OTHER -> d]
NoCard == [x \in MCAll |-> {}]
AtM(m, cl) == {<<m, cl>>}
\* A scenario: every card not yet created, both members up, RUNNING, the keys
\* of every rule that is not quiet queued.
Base == [col |-> Cols4("none", "none", "none", "none"), score |-> Cols4(0, 0, 0, 0),
         result |-> Cols4("ok", "ok", "ok", "ok"), wpl |-> NoCard,
         status |-> [m \in MCMembers |-> "up"], running |-> TRUE,
         next |-> 10, goal |-> FALSE, log |-> <<>>, owe |-> {}, oweall |-> TRUE]
UpDown == [m \in MCMembers |-> IF m = "m1" THEN "up" ELSE "down"]

\* The instance of the task: p1 ready (s1, 2), g1 waiting (s1, 4), p2
\* waiting on p1 (s2, 2).
ScnMain == [Base EXCEPT !.col = Cols4("ready", "waiting", "none", "waiting"), !.score = Cols4(2, 2, 0, 4), !.next = 6]
\* p1 merging at the head of s1 (2), g1 behind it (4), p2 waiting on p1 (s2).
ScnLand == [Base EXCEPT !.col = Cols4("merging", "waiting", "none", "waiting"), !.score = Cols4(2, 2, 0, 4), !.next = 6]
\* p1 and p3 merging in s1 (2, 4), p2 waiting on both (s2).
ScnTwoNeeds == [Base EXCEPT !.col = Cols4("merging", "waiting", "merging", "none"), !.score = Cols4(2, 2, 4, 0), !.next = 6]
\* p1 dealt to m1 and not taken (s1, 2); m2 down.
ScnDealt == [Base EXCEPT !.col = Cols4("working", "none", "none", "none"), !.score = Cols4(2, 0, 0, 0),
                         !.wpl = [NoCard EXCEPT !["p1"] = AtM("m1", "ready")], !.status = UpDown, !.next = 4]
\* The same, both members up.
ScnDealtUp == [ScnDealt EXCEPT !.status = [m \in MCMembers |-> "up"]]
\* The same, m1 held by fleet down.
ScnDealtHeld == [ScnDealt EXCEPT !.status = [m \in MCMembers |-> IF m = "m1" THEN "held" ELSE "up"]]
\* p1 ready (s1, 4) with g1 waiting behind it (6).
ScnSentBehind == [Base EXCEPT !.col = Cols4("ready", "none", "none", "waiting"), !.score = Cols4(4, 0, 0, 6), !.next = 8]
\* p1 waiting (s1, 4) with g1 behind it (6).
ScnSentBehindW == [ScnSentBehind EXCEPT !.col = Cols4("waiting", "none", "none", "waiting")]
\* g1 waiting alone at the head of s1 (4), p3 ready behind it (6).
ScnReachRace == [Base EXCEPT !.col = Cols4("none", "none", "ready", "waiting"), !.score = Cols4(0, 0, 6, 4), !.next = 8]
\* p1 ready (s1, 2); m1 up, m2 down.
ScnReady1 == [Base EXCEPT !.col = Cols4("ready", "none", "none", "none"), !.score = Cols4(2, 0, 0, 0), !.status = UpDown, !.next = 4]
\* p1 and p3 ready and never dealt (s1, 2 and 4).
ScnReady2 == [Base EXCEPT !.col = Cols4("ready", "none", "ready", "none"), !.score = Cols4(2, 0, 4, 0), !.next = 6]
\* s1: p1 and p3 ready (2, 4); s2: p2 ready (6): both members up with room for
\* two, so the deal takes two of the three. The whole table's order (2, 4, 6)
\* takes both of s1's; the stream turns take p1 and p2 (W28).
ScnTurns == [Base EXCEPT !.col = Cols4("ready", "ready", "ready", "none"), !.score = Cols4(2, 6, 4, 0), !.next = 8]
\* The same, m2 down.
ScnReady2One == [ScnReady2 EXCEPT !.status = UpDown]
\* p1 in review, its work ok, never asked (s1, 2).
ScnMainOne == [ScnMain EXCEPT !.status = UpDown]
\* p1 in review, its work ok, never asked (s1, 2).
ScnReview == [Base EXCEPT !.col = Cols4("review", "none", "none", "none"), !.score = Cols4(2, 0, 0, 0), !.next = 4]
\* p1 ready behind g1 (s1: g1 at 2, p1 at 4).
ScnBehind == [Base EXCEPT !.col = Cols4("ready", "none", "none", "waiting"), !.score = Cols4(4, 0, 0, 2), !.next = 6]
\* STOPPED, p1 waiting and free to go (s1, 2): moves are due.
ScnStoppedDue == [Base EXCEPT !.col = Cols4("waiting", "none", "none", "none"), !.score = Cols4(2, 0, 0, 0),
                              !.running = FALSE, !.next = 4]
\* STOPPED, p1 dealt to m1 and not taken: no move is due.
ScnStoppedDealt == [ScnDealt EXCEPT !.running = FALSE]
\* s1: p1 working at m1 (2), p3 working at m2 (4).
ScnTwoWorking == [Base EXCEPT !.col = Cols4("working", "none", "working", "none"), !.score = Cols4(2, 0, 4, 0),
                              !.wpl = [NoCard EXCEPT !["p1"] = AtM("m1", "working"), !["p3"] = AtM("m2", "working")], !.next = 6]
\* s1: p1 and p3 waiting and free to go (2, 4).
ScnTwoWaiting == [Base EXCEPT !.col = Cols4("waiting", "none", "waiting", "none"), !.score = Cols4(2, 0, 4, 0), !.next = 6]
\* p2 waiting on p1 (s2); p1 waiting (s1).
ScnNeedOpen == [Base EXCEPT !.col = Cols4("waiting", "waiting", "none", "none"), !.score = Cols4(2, 2, 0, 0), !.next = 4]
\* p2 waiting on p1, which has no record yet ("blocked on something missing").
ScnNeedMissing == [Base EXCEPT !.col = Cols4("none", "waiting", "none", "none"), !.score = Cols4(0, 2, 0, 0), !.next = 4]
\* p1 ready (s1, 2); nothing else.
ScnRank == [Base EXCEPT !.col = Cols4("ready", "none", "none", "none"), !.score = Cols4(2, 0, 0, 0), !.next = 6]
\* A goal with its remind entry; p1 ready.
ScnGoal == [ScnRank EXCEPT !.goal = TRUE]
\* A deal key queued with nothing to deal: p1 in review.
ScnQuietKey == [ScnReview EXCEPT !.owe = {DealK}]
\* A line after the cursor that queues deal, the key nowhere else; p1 ready.
ScnDealLine == [ScnRank EXCEPT !.log = << [keys |-> {DealK}, ing |-> 0] >>, !.oweall = FALSE]
\* p1 dealt to m1 and not taken (s1, 2), p3 waiting and free to go (s1, 4), m2 down.
ScnDownDeal == [ScnDealt EXCEPT !.col = Cols4("working", "none", "waiting", "none"), !.score = Cols4(2, 0, 4, 0), !.next = 6]
\* The bench instance: p1 ready (s1, 2), g1 waiting (s1, 4), p3 waiting
\* behind g1 (s1, 6), p2 waiting on p1 (s2, 2).
ScnFull == [Base EXCEPT !.col = Cols4("ready", "waiting", "waiting", "waiting"), !.score = Cols4(2, 2, 6, 4), !.next = 8]
\* s1: p3 waiting (2), p1 in review (4), its work ok.
ScnDropReview == [Base EXCEPT !.col = Cols4("review", "none", "waiting", "none"), !.score = Cols4(4, 0, 2, 0), !.next = 6]
\* p1 dealt to m1 and p3 to m2, neither taken (s1, 2 and 4): every ready cell full.
ScnDealtBoth == [Base EXCEPT !.col = Cols4("working", "none", "working", "none"), !.score = Cols4(2, 0, 4, 0),
                             !.wpl = [NoCard EXCEPT !["p1"] = AtM("m1", "ready"), !["p3"] = AtM("m2", "ready")], !.next = 6]
\* s1: p1 waiting and free to go (2), p3 waiting (4) on p2, which has no record:
\* "a primary is blocked on something missing" on p3.
ScnDropBlocked == [Base EXCEPT !.col = Cols4("waiting", "none", "waiting", "none"), !.score = Cols4(2, 0, 4, 0), !.next = 6]
\* s1: p3 ready and never dealt (6); p1 and g1 not yet added (insertions at 3 and 5).
ScnInsert == [Base EXCEPT !.col = Cols4("none", "none", "ready", "none"), !.score = Cols4(0, 0, 6, 0), !.next = 8]
\* s1: p1 and p3 waiting and free to go (2, 4); s2: p2 waiting and free to go (2).
ScnDropTwo == [ScnTwoWaiting EXCEPT !.col = Cols4("waiting", "waiting", "waiting", "none"), !.score = Cols4(2, 2, 4, 0)]
\* s1: p1 and p3 waiting and free to go (2, 4), g1 waiting behind them (6).
ScnMulti == [Base EXCEPT !.col = Cols4("waiting", "none", "waiting", "waiting"), !.score = Cols4(2, 0, 4, 6), !.next = 8]
\* s2: p2 waiting (2) on p1 (s1), which has no record: "a primary is blocked
\* on something missing: p1" on p2; s1: p3 waiting and free to go (2), g1
\* waiting behind it (4), so that a drop of s1 takes more than one part.
ScnDropMissing == [Base EXCEPT !.col = Cols4("none", "waiting", "waiting", "waiting"), !.score = Cols4(0, 2, 2, 4), !.next = 6]
\* STOPPED, p1 ready and never dealt (s1, 2), m1 up, m2 down: a dry deal would place it.
ScnStoppedReady == [ScnReady1 EXCEPT !.running = FALSE]

\* Reachability probes (expected to fail: each names a state a configuration
\* must reach for its case to mean anything).
\* Two drops in one behaviour, each to its last part (MCSprintEventsDrop2).
ProbeTwoDrops == Cardinality({r \in receipts : r.k > 0 /\ r.final}) < 2
\* One step that moves two cards: a request of two units applied (a release
\* of two, a deal of two; no verb here moves two cards) (MCSprintEventsMulti).
ProbeTwoMoves == [][~\E p, q \in Prims : p # q /\ col'[p] # col[p] /\ col'[q] # col[q]]_vars
\* A part of drop --stream applied after the verb read again on a
\* freezefirst refusal (MCSprintEventsFreezeReach).
ProbeReread == [][~\E v \in VerbProcs : vk[v].rr > 0 /\ \E r \in receipts' \ receipts : r.op = vk[v].op /\ r.k > 0]_vars

=============================================================================
