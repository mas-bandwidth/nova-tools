---------------------------- MODULE MCDirtyTick ----------------------------
\* The small instances of DirtyTick. Every configuration names its cards,
\* machines and readers, the scenario it starts from (Scn), the repairs it
\* runs with (Fixes; {"seefleet", "pendingroom"} is the shape with both of
\* the holes this model found closed, {} or one of them is the shape as the
\* owner stated it), and the witness it turns on (Broken; "none" for none).
\* Card names: c1, c3 (s1), c2 (s2); g1 (s1) and g2 (s2) sentinels at the
\* heads of their streams; a1 to a3 (s1) and b1, b2 (s2) for the turns.
EXTENDS DirtyTick

MCCardOrder == <<"g1", "g2", "a1", "b1", "a2", "b2", "a3", "c1", "c2", "c3">>
MCStreamOf == [c \in {MCCardOrder[i] : i \in 1..Len(MCCardOrder)} |->
                 IF c \in {"c2", "g2", "b1", "b2"} THEN "s2" ELSE "s1"]
MCPos == [c \in {MCCardOrder[i] : i \in 1..Len(MCCardOrder)} |->
            CASE c \in {"g1", "g2", "a1", "b1"} -> 1
              [] c \in {"a2", "b2"} -> 2
              [] c = "a3" -> 3
              [] c = "c1" -> 2 [] c = "c2" -> 2 [] c = "c3" -> 3]
MCMOrder == <<"m1", "m2">>
MCROrder == <<"r1", "r2">>
MCSOrder == <<"s1", "s2">>
MCWidth == [m \in {"m1", "m2"} |-> 1]
MCHost == [r \in {"r1", "r2"} |-> IF r = "r1" THEN "m1" ELSE "m2"]

InOrder == SelectSeq(MCCardOrder, LAMBDA c : c \in Cards)
Adds == [i \in 1..Len(InOrder) |-> E("add", InOrder[i], "-")]
Queues(w, r, g, f) == [t \in Tables |-> CASE t = "work" -> w [] t = "readers" -> r
                                          [] t = "merge" -> g [] t = "fleet" -> f]
NoReads == [c \in Cards |-> NoR]
Empty == [m \in Machines |-> {}]
Base == [col |-> [c \in Cards |-> "none"], rd |-> NoReads, mq |-> {}, up |-> Machines \cup Readers,
         live |-> Machines \cup Readers, away |-> FALSE, misses |-> FALSE, hand |-> FALSE,
         mc |-> Empty, mr |-> Empty, q |-> Queues(Adds, <<>>, <<>>, <<>>), served |-> Cards]

\* Every card added (its add queued), every machine up: the whole life.
ScnBase == Base
\* m1 down and silent, one card added: "no fleet member is up", then a beat.
ScnCold == [Base EXCEPT !.up = {}, !.live = {}]
\* c1 reviewed on r1 (host m1), m1 down: a card waiting for a reader.
ScnBlind == [Base EXCEPT !.col = [c \in Cards |-> "review"], !.up = {}, !.live = {},
                         !.q = Queues(<<>>, <<E("ask", "c1", "-")>>, <<>>, <<>>)]
\* c1 ready, c2 finished and asking for a read; one machine of width 1 that
\* hosts the only reader.
ScnWidth == [Base EXCEPT !.col = [c \in Cards |-> IF c = "c1" THEN "ready" ELSE "review"],
                         !.q = Queues(<<>>, <<E("ask", "c2", "-")>>, <<>>, <<>>)]
\* c1 merging, its merge recorded.
ScnLand == [Base EXCEPT !.col = [c \in Cards |-> "merging"], !.mq = {"c1"},
                        !.q = Queues(<<>>, <<>>, <<E("merged", "c1", "-")>>, <<>>)]
\* c1 in review, its read on r1 (host m1) reported broken.
ScnRework == [Base EXCEPT !.col = [c \in Cards |-> "review"], !.rd = [c \in Cards |-> "r1"],
                          !.mr = [m \in Machines |-> IF m = "m1" THEN {"c1"} ELSE {}],
                          !.q = Queues(<<>>, <<E("rep", "c1", <<"r1", "broken">>)>>, <<>>, <<>>)]
\* c1 working on m1, its finish queued to the fleet.
ScnFin == [Base EXCEPT !.col = [c \in Cards |-> "working"],
                       !.mc = [m \in Machines |-> IF m = "m1" THEN {"c1"} ELSE {}],
                       !.q = Queues(<<>>, <<>>, <<>>, <<E("fin", "c1", "m1")>>)]
\* c1 in review, its read on r1 (host m1); m1 has stopped beating.
ScnLapse == [Base EXCEPT !.live = {}, !.col = [c \in Cards |-> "review"], !.rd = [c \in Cards |-> "r1"],
                         !.mr = [m \in Machines |-> IF m = "m1" THEN {"c1"} ELSE {}],
                         !.q = Queues(<<>>, <<>>, <<>>, <<E("lapse", "-", "m1")>>)]

\* c1 dealt to m1; m1 beats, may miss beat windows and beat again, and lapses
\* only after Misses of them in a row (the missed beats).
ScnMiss == [Base EXCEPT !.misses = TRUE, !.col = [c \in Cards |-> "working"],
                        !.mc = [m \in Machines |-> IF m = "m1" THEN {"c1"} ELSE {}],
                        !.q = Queues(<<>>, <<>>, <<>>, <<>>)]
\* c1 dealt to m1 and not taken; its worker may take it, and the machines
\* may lapse and beat again: the redeals.
ScnTake == [Base EXCEPT !.col = [c \in Cards |-> "working"],
                        !.mc = [m \in Machines |-> IF m = "m1" THEN {"c1"} ELSE {}],
                        !.q = Queues(<<>>, <<>>, <<>>, <<>>)]
\* c1 in review, its read on r1 (host m1) reported ok; the coordinator may
\* record its CI, return it, and accept it: the accept's holds.
ScnAccept == [Base EXCEPT !.col = [c \in Cards |-> "review"], !.rd = [c \in Cards |-> "r1"],
                          !.mr = [m \in Machines |-> IF m = "m1" THEN {"c1"} ELSE {}],
                          !.q = Queues(<<>>, <<E("rep", "c1", <<"r1", "ok">>)>>, <<>>, <<>>)]

\* Every card added, every machine up; no route serves c2's tier and it pins
\* no model (THE ROUTE): c1 lives its life, c2 stays ready.
ScnRoute == [Base EXCEPT !.served = Cards \ {"c2"}]

\* THE READERS' PRESENCE. c1 in review waiting for a read, r1 away: the read is
\* asked of r2, and r1 may come back or go away again.
ScnReader == [Base EXCEPT !.col = [c \in Cards |-> "review"], !.away = TRUE,
                          !.up = Machines \cup {"r2"}, !.live = Machines \cup {"r2"},
                          !.q = Queues(<<>>, <<E("ask", "c1", "-")>>, <<>>, <<>>)]
\* c1 in review, its read on r1 (host m1) asked: r1 may go away, and the read is
\* taken back and asked of r2.
ScnReaderAway == [Base EXCEPT !.col = [c \in Cards |-> "review"], !.away = TRUE,
                              !.rd = [c \in Cards |-> "r1"],
                              !.mr = [m \in Machines |-> IF m = "m1" THEN {"c1"} ELSE {}],
                              !.q = Queues(<<>>, <<>>, <<>>, <<>>)]

\* THE RETURN. c1 in review, its read on r1 (host m1): r1 may return it, and
\* it is asked of r2.
ScnHandBack == [Base EXCEPT !.col = [c \in Cards |-> "review"], !.hand = TRUE,
                            !.rd = [c \in Cards |-> "r1"],
                            !.mr = [m \in Machines |-> IF m = "m1" THEN {"c1"} ELSE {}],
                            !.q = Queues(<<>>, <<>>, <<>>, <<>>)]

\* Reachability probes, expected to fail: every card lands; a card reaches
\* its bound; a tick drains a queue after the first pass; a take ends at the
\* redeal bound; the pump holds a card the coordinator then accepts.
ProbeNotAllLanded == \E c \in Cards : col[c] # "landed"
ProbeNoBound == \A c \in Cards : ~bnd[c]
ProbeNoLateDrain == ~(act = "Drain" /\ phase = "drain")
ProbeNoRedealBound == ~\E c \in Cards : ended[c] /\ rdl[c] >= MaxRedeals
ProbeNoHeldAccept == ~\E c \in Cards : col[c] = "merging" /\ ret[c]
=============================================================================
