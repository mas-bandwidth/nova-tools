---------------------------- MODULE FriendNotices ----------------------------
\* Coordinator notification delivery, pkg/friend/notices.go. Episode detectors
\* issue once per transition; Queue gives each occurrence a fresh token. A lost
\* reply leaves the same token queued; the bus commits once per token. Queue
\* capacity is fixed and a full queue holds the next event without dropping
\* an older one. This queue is volatile and shutdown reports its unsent count.
EXTENDS Naturals, Sequences, FiniteSets
CONSTANTS Events, Capacity, Broken
VARIABLES issued, pending, delivered, receipted
vars == <<issued, pending, delivered, receipted>>
E == 1..Events
Init == /\ issued = 0 /\ pending = <<>>
        /\ delivered = [e \in E |-> 0] /\ receipted = {}
Queue == /\ issued < Events /\ Len(pending) < Capacity
         /\ issued' = issued + 1
         /\ pending' = Append(pending, issued + 1)
         /\ UNCHANGED <<delivered, receipted>>
Send(i, reply) ==
  /\ i \in 1..Len(pending)
  /\ LET e == pending[i]
     IN /\ delivered' = [delivered EXCEPT ![e] = IF @ = 0 \/ Broken = "newtoken" THEN @ + 1 ELSE @]
        /\ receipted' = IF reply THEN receipted \cup {e} ELSE receipted
  /\ pending' = IF reply \/ Broken = "droplost" THEN SubSeq(pending, 1, i-1) \o SubSeq(pending, i+1, Len(pending)) ELSE pending
  /\ UNCHANGED issued
Next == Queue \/ (\E i \in 1..Len(pending), reply \in BOOLEAN : Send(i, reply))
Spec == Init /\ [][Next]_vars
TypeOK == /\ issued \in 0..Events /\ pending \in Seq(E)
          /\ Len(pending) <= Capacity /\ receipted \subseteq E
          /\ delivered \in [E -> Nat]
AtMostOnce == \A e \in E : delivered[e] <= 1
NoNoticeLost == (1..issued) = receipted \cup {pending[i] : i \in 1..Len(pending)}
ReceiptsAreReal == \A e \in receipted : delivered[e] = 1
=============================================================================
