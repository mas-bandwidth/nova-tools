------------------------ MODULE PriorityStopReturn ------------------------
(* One active consumer, one queued peer and one STOP/resume. Active lease
   metadata stays fixed; the gen+1 return copies current producer fields in the
   same store unit. Next admission reads queued copies, not an ideal live rank. *)
EXTENDS Naturals, Sequences
CONSTANTS Role, InitialUrgency, UpdatedUrgency, OmitRefresh
Children == {"earlier", "later"}
Producer(c) == IF c = "earlier" THEN "p" ELSE "q"
RoleRank(l) == IF Role = "read" THEN 4 ELSE IF Role = "fix" THEN 0 ELSE l
VARIABLE s
vars == <<s>>
Init == s = [producer |-> [p \in {"p", "q"} |-> IF p = "q" THEN InitialUrgency ELSE 5],
             state |-> [c \in Children |-> IF c = "later" THEN "running" ELSE "queued"],
             rank |-> [c \in Children |-> RoleRank(IF c = "later" THEN InitialUrgency ELSE 5)],
             urgency |-> [c \in Children |-> IF c = "later" THEN InitialUrgency ELSE 5],
             gen |-> 1, owner |-> "owner", branch |-> "owned-branch", attempt |-> 1,
             phase |-> "active", live |-> TRUE, promoted |-> FALSE,
             returned |-> FALSE, replayed |-> FALSE,
             returnedRank |-> RoleRank(InitialUrgency), returnedUrgency |-> InitialUrgency,
             returnProducer |-> InitialUrgency, starts |-> <<>>, orderBad |-> FALSE]

Promote == /\ ~s.promoted /\ s.state["later"] = "running" /\ ~s.returned
           /\ s' = [s EXCEPT !.producer["q"] = UpdatedUrgency, !.promoted = TRUE]
Stop == /\ s.phase = "active"
        /\ s' = [s EXCEPT !.phase = "stopped"]
CancelQuiet == /\ s.phase = "stopped" /\ s.live
               /\ s' = [s EXCEPT !.live = FALSE]
(* The stored Work producer is resolved through the consumer's primary link.
   Receipt/quiet and atomic store completion are source obligations/oracles. *)
Return == /\ s.phase = "stopped" /\ ~s.live /\ ~s.returned
          /\ s.state["later"] = "running" /\ s.gen = 1
          /\ LET l == s.producer[Producer("later")]
                 r == IF OmitRefresh THEN s.rank["later"] ELSE RoleRank(l)
                 u == IF OmitRefresh THEN s.urgency["later"] ELSE l
             IN s' = [s EXCEPT !.state["later"] = "queued", !.gen = 2,
                              !.rank["later"] = r, !.urgency["later"] = u,
                              !.returned = TRUE, !.returnedRank = r,
                              !.returnedUrgency = u, !.returnProducer = l]
ReplayReturn == /\ s.phase = "stopped" /\ s.returned /\ ~s.replayed
                /\ s' = [s EXCEPT !.replayed = TRUE]
Resume == /\ s.phase = "stopped" /\ s.returned /\ ~s.live
          /\ s' = [s EXCEPT !.phase = "resumed"]

(* Role rank wins; fixed-role peers then use producer urgency; score ties are
   stable earlier-before-later. This mirrors QueueOrder's stored-field input. *)
Before(a,b,ranks,urgencies) == ranks[a] < ranks[b] \/
    (ranks[a] = ranks[b] /\ Role \in {"read", "fix"} /\ urgencies[a] < urgencies[b]) \/
    (ranks[a] = ranks[b] /\ (Role = "work" \/ urgencies[a] = urgencies[b]) /\
      a = "earlier" /\ b = "later")
DesiredRank == [c \in Children |-> RoleRank(s.producer[Producer(c)])]
DesiredUrgency == [c \in Children |-> s.producer[Producer(c)]]
Selected(c) == \A k \in Children : s.state[k] = "queued" => ~Before(k,c,s.rank,s.urgency)
Desired(c) == \A k \in Children : s.state[k] = "queued" => ~Before(k,c,DesiredRank,DesiredUrgency)
Launch(c) == /\ s.phase = "resumed" /\ s.state[c] = "queued"
             /\ \A k \in Children : s.state[k] # "running"
             /\ Selected(c)
             /\ s' = [s EXCEPT !.state[c] = "running", !.starts = Append(@,c),
                              !.orderBad = (s.orderBad \/ ~Desired(c))]
Finish(c) == /\ s.phase = "resumed" /\ s.state[c] = "running"
             /\ s' = [s EXCEPT !.state[c] = "done"]
Next == Promote \/ Stop \/ CancelQuiet \/ Return \/ ReplayReturn \/ Resume \/
        (\E c \in Children : Launch(c) \/ Finish(c))
Spec == Init /\ [][Next]_vars
TypeOK == /\ Role \in {"work", "read", "fix"}
          /\ s.producer \in [{"p", "q"} -> {1,2,3,5,6}]
          /\ s.rank \in [Children -> 0..6] /\ s.urgency \in [Children -> {1,2,3,5,6}]
          /\ s.state \in [Children -> {"queued", "running", "done"}]
          /\ s.phase \in {"active", "stopped", "resumed"}
          /\ s.gen \in {1,2} /\ Len(s.starts) <= 2
ReturnedCurrentPriority == s.returned =>
    s.returnedRank = RoleRank(s.returnProducer) /\ s.returnedUrgency = s.returnProducer
OrderedResume == ~s.orderBad
ActiveLeaseUnchanged == ~s.returned =>
    s.rank["later"] = RoleRank(InitialUrgency) /\ s.urgency["later"] = InitialUrgency
RoleFixed == Role \in {"read", "fix"} =>
    \A c \in Children : s.rank[c] = RoleRank(5)
ReturnedIdentity == s.owner = "owner" /\ s.branch = "owned-branch" /\ s.attempt = 1
NoReturnBeforeQuiet == s.returned => ~s.live /\ s.gen = 2
WitnessReturnedOrder == ~(s.promoted /\ s.returned /\ s.replayed /\
    Len(s.starts) = 2 /\ s.state["earlier"] = "done" /\ s.state["later"] = "done" /\
    s.starts[1] = IF UpdatedUrgency < 5 THEN "later" ELSE "earlier")
=============================================================================
