---------------------------- MODULE Resources ----------------------------
(***************************************************************************)
(* The resources table (docs/SPEC-SPRINT.md section 19;                    *)
(* internal/sprint/resource.go, internal/sprint/store/resource.go): one    *)
(* shared resource (a bench machine, a branch, a port, a provider account) *)
(* of capacity Cap, held by leases and asked for by a line of waiters, and *)
(* the members' liveness as the coordinator observes it.                   *)
(*                                                                         *)
(* On 2026-10-05 a friend held a bench by a bus message to another friend, *)
(* went down out of credit, and the bench stayed held for the morning. The *)
(* owner: "Dining philosophers." and "If we have any sort of resources     *)
(* being managed in future, they should be managed by the coordinator     *)
(* here, as formal verbs." Here every hold is a lease with an expiry, the  *)
(* line is served in order, and a member down holds nothing.               *)
(*                                                                         *)
(* Time is a countdown per lease: a lease starts at Lease ticks and the    *)
(* tick takes one off; at zero the lease has expired and the tick releases *)
(* it. A holder renews at most MaxRenew times per lease, which is how the  *)
(* model says "while holders keep releasing or expiring": the liveness     *)
(* property is checked under that bound and weak fairness of the tick.    *)
(*                                                                         *)
(* Broken selects a reversed witness (none, down-keeps, release-no-grant,  *)
(* over-capacity), each of which TLC must refute by the invariant named    *)
(* beside it in tla/CASES.tsv.                                              *)
(***************************************************************************)
EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS Members,   \* the members that may claim: friends, fleet members
          Cap,       \* the resource's capacity: how many hold it at once
          Lease,     \* a lease's length, in ticks
          MaxRenew,  \* how many times one lease may be renewed
          Broken     \* "none", or the reversed witness

ASSUME Cap \in Nat /\ Cap >= 1
ASSUME Lease \in Nat /\ Lease >= 1
ASSUME MaxRenew \in Nat
ASSUME Broken \in {"none", "down-keeps", "release-no-grant", "over-capacity"}

VARIABLES holders,  \* the members that hold a lease
          left,     \* [Members -> 0..Lease]: the ticks left on a holder's lease
          renews,   \* [Members -> 0..MaxRenew]: how often a holder renewed this lease
          waiters,  \* the line, a sequence of members, the head is next
          up        \* the members that are up (not down, held or out of credit)

vars == <<holders, left, renews, waiters, up>>

Range(s) == {s[i] : i \in 1..Len(s)}

RECURSIVE Without(_, _)
Without(s, m) == IF s = <<>> THEN <<>>
                 ELSE IF Head(s) = m THEN Without(Tail(s), m)
                 ELSE <<Head(s)>> \o Without(Tail(s), m)

RECURSIVE Pos(_, _)
Pos(s, m) == IF s = <<>> THEN 0
             ELSE IF Head(s) = m THEN 1
             ELSE 1 + Pos(Tail(s), m)

(***************************************************************************)
(* Settle grants the head of the line into every free place, each grant a  *)
(* fresh lease; it is what every release, expiry and claim ends with, so a *)
(* place is never free while someone waits (NoRoomWasted).                 *)
(***************************************************************************)
RECURSIVE Settle(_, _, _, _)
Settle(hs, ws, lf, rn) ==
    IF Len(ws) > 0 /\ Cardinality(hs) < Cap
    THEN Settle(hs \cup {Head(ws)}, Tail(ws),
                [lf EXCEPT ![Head(ws)] = Lease], [rn EXCEPT ![Head(ws)] = 0])
    ELSE <<hs, ws, lf, rn>>

Apply(t) == /\ holders' = t[1]
            /\ waiters' = t[2]
            /\ left' = t[3]
            /\ renews' = t[4]

Init == /\ holders = {}
        /\ left = [m \in Members |-> 0]
        /\ renews = [m \in Members |-> 0]
        /\ waiters = <<>>
        /\ up = Members

(***************************************************************************)
(* Claim: a member up that holds nothing and waits nowhere joins the back  *)
(* of the line, and is granted at once when the line is empty and there is *)
(* room (it is then the head). The over-capacity witness grants it a place *)
(* whatever the holders.                                                   *)
(***************************************************************************)
Claim(m) ==
    /\ m \in up
    /\ m \notin holders
    /\ m \notin Range(waiters)
    /\ IF Broken = "over-capacity"
       THEN /\ holders' = holders \cup {m}
            /\ left' = [left EXCEPT ![m] = Lease]
            /\ renews' = [renews EXCEPT ![m] = 0]
            /\ UNCHANGED waiters
       ELSE Apply(Settle(holders, Append(waiters, m), left, renews))
    /\ UNCHANGED up

(***************************************************************************)
(* Renew: a holder up starts its lease again, at most MaxRenew times.      *)
(***************************************************************************)
Renew(m) ==
    /\ m \in up
    /\ m \in holders
    /\ renews[m] < MaxRenew
    /\ left' = [left EXCEPT ![m] = Lease]
    /\ renews' = [renews EXCEPT ![m] = renews[m] + 1]
    /\ UNCHANGED <<holders, waiters, up>>

(***************************************************************************)
(* Release: a holder gives its lease back, or a waiter leaves the line;    *)
(* the head is granted into the room made. The release-no-grant witness    *)
(* leaves the room empty until the next tick.                              *)
(***************************************************************************)
Release(m) ==
    /\ m \in up
    /\ m \in holders \/ m \in Range(waiters)
    /\ IF Broken = "release-no-grant"
       THEN /\ holders' = holders \ {m}
            /\ waiters' = Without(waiters, m)
            /\ left' = [left EXCEPT ![m] = 0]
            /\ UNCHANGED renews
       ELSE Apply(Settle(holders \ {m}, Without(waiters, m),
                         [left EXCEPT ![m] = 0], renews))
    /\ UNCHANGED up

(***************************************************************************)
(* Tick: the coordinator's tick takes one off every lease; a lease at zero *)
(* has expired and is released, and the line is served.                    *)
(***************************************************************************)
Expired == {m \in holders : left[m] <= 1}

Tick ==
    /\ holders /= {}
    /\ LET lf == [m \in Members |-> IF m \in holders /\ left[m] > 0 THEN left[m] - 1 ELSE 0]
       IN Apply(Settle(holders \ Expired, waiters, lf, renews))
    /\ UNCHANGED up

(***************************************************************************)
(* MemberDown: the coordinator observes a member down (held, out of credit, *)
(* no session evidence): its lease is released at once and its place in   *)
(* the line is given up; the head is granted. The down-keeps witness is the *)
(* night of 2026-10-05: the hold outlives the holder.                      *)
(***************************************************************************)
MemberDown(m) ==
    /\ m \in up
    /\ up' = up \ {m}
    /\ IF Broken = "down-keeps"
       THEN UNCHANGED <<holders, left, renews, waiters>>
       ELSE Apply(Settle(holders \ {m}, Without(waiters, m),
                         [left EXCEPT ![m] = 0], renews))

MemberUp(m) ==
    /\ m \notin up
    /\ up' = up \cup {m}
    /\ UNCHANGED <<holders, left, renews, waiters>>

Next == \/ \E m \in Members : Claim(m) \/ Renew(m) \/ Release(m)
                               \/ MemberDown(m) \/ MemberUp(m)
        \/ Tick

Spec == Init /\ [][Next]_vars

(* The live spec: the tick keeps coming, and a member down comes back. *)
LiveSpec == Spec /\ WF_vars(Tick) /\ \A m \in Members : WF_vars(MemberUp(m))

-----------------------------------------------------------------------------
(* Invariants *)

TypeOK ==
    /\ holders \subseteq Members
    /\ left \in [Members -> 0..Lease]
    /\ renews \in [Members -> 0..MaxRenew]
    /\ waiters \in Seq(Members)
    /\ up \subseteq Members
    /\ \A i, j \in 1..Len(waiters) : i /= j => waiters[i] /= waiters[j]

\* Never more holders than capacity.
CapacityKept == Cardinality(holders) <= Cap

\* A member down holds nothing and waits nowhere.
DownHoldsNothing == \A m \in Members : m \notin up => m \notin holders /\ m \notin Range(waiters)

\* A holder's lease has time left: the tick released every expired one.
HoldersHaveTime == \A m \in holders : left[m] >= 1

\* A waiter is granted when room exists: nobody waits while a place is free.
NoRoomWasted == Len(waiters) > 0 => Cardinality(holders) = Cap

\* A holder is in the line nowhere.
HoldOrWait == \A m \in holders : m \notin Range(waiters)

-----------------------------------------------------------------------------
(* Action properties *)

\* No overtaking: when a waiter is granted, nobody that was ahead of it in the
\* line is still waiting (each was granted first, released, or went down).
NoOvertaking ==
    \A m \in Members :
        (m \in holders' /\ m \notin holders /\ m \in Range(waiters)) =>
            \A i \in 1..(Pos(waiters, m) - 1) : waiters[i] \notin Range(waiters')

NoOvertakingProp == [][NoOvertaking]_vars

-----------------------------------------------------------------------------
(* Liveness, on LiveSpec: no wait lasts for ever. A waiter's wait ends by a   *)
(* grant, by its own release of its place, or by going down; while holders   *)
(* keep releasing or expiring (MaxRenew bounds a lease) and the tick keeps   *)
(* coming, a waiter that stays in the line is granted. TLC checks every      *)
(* behaviour, the ones where it stays included, so the disjunction has teeth: *)
(* the release-no-grant witness leaves a waiter in an empty resource for ever. *)

EveryWaiterIsServed ==
    \A m \in Members :
        (m \in Range(waiters)) ~> (m \in holders \/ m \notin up \/ m \notin Range(waiters))

=============================================================================
