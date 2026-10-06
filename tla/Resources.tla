------------------------------ MODULE Resources ------------------------------
EXTENDS Naturals, Sequences, FiniteSets
\* docs/SPEC-SPRINT.md section 11, Resources (card coordinator-managed-resources.w1;
\* internal/sprint/resource.go, internal/sprint/store/resource.go). A shared
\* resource (a bench, a branch, a port, a provider account) is a row of the
\* coordinator's resources table with a capacity. A member holds it only by a
\* lease the coordinator's verbs grant: resource claim is granted while the row
\* has room and no one waits, else the claimant joins the row's line, first in,
\* first out; resource release gives a lease back. A lease has an expiry: renew
\* moves it on, and time passing it makes the lease stale (Lapse). The tick
\* releases a stale lease (Expire) and every lease and place in line of a member
\* it reads as down, held or out of credit (MemberDown), and in the same write
\* grants the room it freed to the line's head (Fill). Grant is the tick's own
\* fill of a row with room and a line, which the correct spec never leaves.
\*
\* What is abstracted: the times (a lease is live or stale; renew only moves a
\* live lease's expiry, so it changes no variable here), the record's encoding,
\* the fence the verbs and the tick write under (each action is one locked
\* read-modify-write of the record), and the coordinator's judgment of a line
\* that waits with no room past a bound. A member down is the tick's reading of
\* its liveness: the release is in the same write as that reading, so the
\* invariant DownHoldsNothing is over the members the tick has read down.
CONSTANTS Members, Res, Cap, BadOverCap, BadDownKeeps, BadNoFill, BadNoExpire

VARIABLES holders, live, line, up
vars == <<holders, live, line, up>>

InLine(r) == {line[r][i] : i \in 1..Len(line[r])}
Room(r) == Cardinality(holders[r]) < Cap

\* Drop is the line without m, order kept.
Drop(s, m) == SelectSeq(s, LAMBDA x : x # m)

\* Fill grants the line's head while the row has room, in order. Members is
\* small, so the recursion is bounded by Cap.
RECURSIVE FillH(_, _), FillL(_, _)
FillH(h, l) == IF l # <<>> /\ Cardinality(h) < Cap THEN FillH(h \cup {Head(l)}, Tail(l)) ELSE h
FillL(h, l) == IF l # <<>> /\ Cardinality(h) < Cap THEN FillL(h \cup {Head(l)}, Tail(l)) ELSE l

\* After is the row after a release or expiry left holders h and line l: filled
\* from the line at once, unless the broken witness leaves the room idle.
AfterH(h, l) == IF BadNoFill THEN h ELSE FillH(h, l)
AfterL(h, l) == IF BadNoFill THEN l ELSE FillL(h, l)

TypeOK ==
    /\ holders \in [Res -> SUBSET Members]
    /\ live \in [Res -> SUBSET Members]
    /\ \A r \in Res : live[r] \subseteq holders[r]
    /\ line \in [Res -> Seq(Members)]
    /\ up \subseteq Members

Init ==
    /\ holders = [r \in Res |-> {}]
    /\ live = [r \in Res |-> {}]
    /\ line = [r \in Res |-> <<>>]
    /\ up = Members

\* resource claim --as m: granted at once while there is room and no line,
\* else m joins the line (never polls: the tick grants it).
Claim(m, r) ==
    /\ m \in up
    /\ m \notin holders[r] /\ m \notin InLine(r)
    /\ IF (Room(r) /\ line[r] = <<>>) \/ BadOverCap
         THEN /\ holders' = [holders EXCEPT ![r] = @ \cup {m}]
              /\ live' = [live EXCEPT ![r] = @ \cup {m}]
              /\ UNCHANGED line
         ELSE /\ line' = [line EXCEPT ![r] = Append(@, m)]
              /\ UNCHANGED <<holders, live>>
    /\ UNCHANGED up

\* resource renew --as m: a live lease's expiry moves on. The abstraction holds
\* no times, so it changes nothing here; a stale lease is not renewed (the verb
\* refuses it: claim again).
Renew(m, r) ==
    /\ m \in up /\ m \in live[r]
    /\ UNCHANGED vars

\* Time passes a live lease's expiry with no renew.
Lapse(m, r) ==
    /\ m \in live[r]
    /\ live' = [live EXCEPT ![r] = @ \ {m}]
    /\ UNCHANGED <<holders, line, up>>

\* resource release --as m: the lease given back, its room granted at once.
Release(m, r) ==
    /\ m \in up /\ m \in holders[r]
    /\ holders' = [holders EXCEPT ![r] = AfterH(@ \ {m}, line[r])]
    /\ live' = [live EXCEPT ![r] = (@ \ {m}) \cup (AfterH(holders[r] \ {m}, line[r]) \ holders[r])]
    /\ line' = [line EXCEPT ![r] = AfterL(holders[r] \ {m}, @)]
    /\ UNCHANGED up

\* The tick: a lease past its expiry released, its room granted at once.
Expire(m, r) ==
    /\ ~BadNoExpire
    /\ m \in holders[r] \ live[r]
    /\ holders' = [holders EXCEPT ![r] = AfterH(@ \ {m}, line[r])]
    /\ live' = [live EXCEPT ![r] = @ \cup (AfterH(holders[r] \ {m}, line[r]) \ holders[r])]
    /\ line' = [line EXCEPT ![r] = AfterL(holders[r] \ {m}, @)]
    /\ UNCHANGED up

\* The tick reads m down, held or out of credit: every lease and every place in
\* line of m released in the same write, each row's room granted at once.
MemberDown(m) ==
    /\ m \in up
    /\ up' = up \ {m}
    /\ IF BadDownKeeps
         THEN UNCHANGED <<holders, live, line>>
         ELSE LET h(r) == holders[r] \ {m}
                  l(r) == Drop(line[r], m)
              IN /\ holders' = [r \in Res |-> AfterH(h(r), l(r))]
                 /\ live' = [r \in Res |-> (live[r] \ {m}) \cup (AfterH(h(r), l(r)) \ holders[r])]
                 /\ line' = [r \in Res |-> AfterL(h(r), l(r))]

MemberUp(m) ==
    /\ m \notin up
    /\ up' = up \cup {m}
    /\ UNCHANGED <<holders, live, line>>

\* The tick's fill of a row with room and a line: the head granted.
Grant(r) ==
    /\ ~BadNoFill
    /\ Room(r) /\ line[r] # <<>>
    /\ holders' = [holders EXCEPT ![r] = @ \cup {Head(line[r])}]
    /\ live' = [live EXCEPT ![r] = @ \cup {Head(line[r])}]
    /\ line' = [line EXCEPT ![r] = Tail(@)]
    /\ UNCHANGED up

Next ==
    \/ \E m \in Members, r \in Res : Claim(m, r) \/ Renew(m, r) \/ Lapse(m, r) \/ Release(m, r) \/ Expire(m, r)
    \/ \E m \in Members : MemberDown(m) \/ MemberUp(m)
    \/ \E r \in Res : Grant(r)

\* Fairness is the card's premise: holders keep releasing or expiring. Every
\* live lease's expiry comes (a renew moves it, never past every bound: the
\* coordinator's judgment of a line waiting with no room is that bound), and
\* the tick runs.
Fairness ==
    /\ \A m \in Members, r \in Res : WF_vars(Lapse(m, r)) /\ WF_vars(Expire(m, r))
    /\ \A r \in Res : WF_vars(Grant(r))

Spec == Init /\ [][Next]_vars /\ Fairness

\* Never more holders than capacity.
CapacityHeld == \A r \in Res : Cardinality(holders[r]) <= Cap

\* A member the tick has read down holds nothing and waits for nothing.
DownHoldsNothing == \A r \in Res : holders[r] \subseteq up /\ InLine(r) \subseteq up

\* A waiter is granted when room exists: no row has room and a line at once.
NoWaitWithRoom == \A r \in Res : line[r] # <<>> => ~Room(r)

\* No member is in a line twice, or in the line of a row it holds.
LineClean == \A r \in Res :
    /\ Len(line[r]) = Cardinality(InLine(r))
    /\ InLine(r) \cap holders[r] = {}

\* Every waiter is eventually granted, or leaves the line by going down.
WaiterGranted == \A m \in Members, r \in Res :
    (m \in InLine(r)) ~> (m \in holders[r] \/ m \notin up)
=============================================================================
