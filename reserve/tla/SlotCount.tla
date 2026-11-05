------------------------------ MODULE SlotCount ------------------------------
\* nova-reserve's guarantee, with no files: at most N shared holders at once, an
\* exclusive holder runs alone, and a holder that ends or dies leaves. The
\* layout model (SlotFiles.tla) refines this one.
\*
\* Broken = "none" is the guarantee. Reversed witness:
\*   "nocount"  a shared holder is let in without counting: AtMostN fails
EXTENDS Naturals, FiniteSets

CONSTANTS Procs, N, Broken

VARIABLES shared, excl

vars == <<shared, excl>>

TypeOK == shared \subseteq Procs /\ excl \subseteq Procs

Init == shared = {} /\ excl = {}

AcquireShared(p) ==
  /\ p \notin shared \cup excl
  /\ excl = {}
  /\ Broken = "nocount" \/ Cardinality(shared) < N
  /\ shared' = shared \cup {p}
  /\ UNCHANGED excl

AcquireExclusive(p) ==
  /\ shared = {} /\ excl = {}
  /\ excl' = {p}
  /\ UNCHANGED shared

\* Release is the holder's end, whether it finished or was killed.
Release(p) ==
  /\ p \in shared \cup excl
  /\ shared' = shared \ {p} /\ excl' = excl \ {p}

Next == \E p \in Procs : AcquireShared(p) \/ AcquireExclusive(p) \/ Release(p)

Spec == Init /\ [][Next]_vars

AtMostN == Cardinality(shared) <= N
ExclusiveAlone == excl # {} => (shared = {} /\ Cardinality(excl) = 1)
=============================================================================
