------------------------------ MODULE LandBisect ------------------------------
\* nova-sprint land, the blame of a red batch (cmd/nova-sprint/landpass.go, bisect;
\* 2026-10-10, fault item 4: "landings refused on go build, streams stall in merging").
\* LandPass.tla holds the pass over the streams at the grain of one batch; this module
\* holds what happens inside one batch when its one gate is red.
\*
\* BEFORE. A red batch gate gated every head again, one after another from the base
\* (build with gateEach): N gates for N heads. v1-4's batch of 99 cards ran past
\* LandDeadline on that walk, was abandoned with nothing blamed, and was cut again the
\* next pass the same way: the stream sat in merging for hours, no card returned. And a
\* bench that could not run the gate at all (`sh: go: not found`, `disk quota exceeded`)
\* answered red, so the walk blamed whichever head it was gating.
\*
\* THE WORLD. The batch is N heads merged in order onto a base whose tree passed the gate;
\* tip(i) is the tree after head i. Whether each tip is red is the oracle red, chosen at
\* Init over every assignment and then fixed (a gate answers what the tree is); red[N] is
\* TRUE, the batch's own gate. Red need not be monotone: a later head may fix what an
\* earlier one broke. A probe of a tip may instead meet a bench fault (Faults): the bench
\* did not run the gate, which says nothing about the tree.
\*
\* THE STATE.
\*   lo, hi    the search: tip(lo - 1) passed (or is the base, lo = 1) and tip(hi) is red
\*   probes    the gates the search ran
\*   state     search, blamed (head blamed returned with its finding; heads 1..blamed-1,
\*             tip(blamed - 1) gated green, go on to land), refused (a bench fault: the
\*             batch is refused for the pass, no head blamed, its cards still queued)
\*
\* THE RULES.
\*   BlameSound: a blamed head is the one whose merge turned a passing tree red: tip(k)
\*     red and tip(k - 1) passing (or the base). The prefix that lands passed a gate.
\*   ProbesBounded: at most ceil(log2 N) gates after the batch's own: 99 heads, 7 gates.
\*   Ends (liveness, under fairness): the search ends, blamed or refused.
\*
\* Broken: "none" is the design.
\*   "each"         the walk of every head in order (probe lo, not the middle): the gate
\*                  per head of before (ProbesBounded fails).
\*   "faultblames"  a bench fault answers red (BlameSound fails: a head blamed whose tree
\*                  never failed).
\*   "last"         the last head merged is blamed with the batch's finding, no search
\*                  (BlameSound fails when the tree was red before it).
\*
\* WHAT IS NOT MODELLED. The merges and checks (a head that does not merge ends the batch
\* before any gate, as before), the base's gate and cure, the deadline (LandPass.tla), the
\* push and the report (Land.tla).
\*
\* TLC, 2026-10-10, hetzner2, tla2tools.jar as tla/tla2tools.sha256 pins it: MCLandBisect
\* (N = 6, faults on) passes TypeOK, BlameSound, ProbesBounded and Ends; the three reversed
\* witnesses each fail the property their configuration names. The records are tla/RUNS.tsv.
EXTENDS Integers, FiniteSets

CONSTANTS N, Faults, Broken

ASSUME N \in Nat \ {0}
ASSUME Faults \in BOOLEAN

VARIABLES red, lo, hi, probes, state

vars == <<red, lo, hi, probes, state>>

Heads == 1..N

\* The least k with 2^k >= n.
RECURSIVE Pow2(_)
Pow2(k) == IF k = 0 THEN 1 ELSE 2 * Pow2(k - 1)
Log2Ceil(n) == CHOOSE k \in 0..n : Pow2(k) >= n /\ (k = 0 \/ Pow2(k - 1) < n)

TypeOK ==
  /\ red \in [Heads -> BOOLEAN]
  /\ lo \in Heads /\ hi \in Heads
  /\ probes \in Nat
  /\ state \in {"search", "blamed", "refused"}

Init ==
  /\ red \in {r \in [Heads -> BOOLEAN] : r[N]}
  /\ lo = 1 /\ hi = N
  /\ probes = 0
  /\ state = "search"

\* The tip probed: the middle of the span, or (each) its first.
Mid == IF Broken = "each" THEN lo ELSE (lo + hi) \div 2

\* One gate of tip(Mid): red moves hi down to it, passing moves lo past it.
Probe ==
  /\ state = "search" /\ lo < hi /\ Broken # "last"
  /\ IF red[Mid] THEN hi' = Mid /\ lo' = lo ELSE lo' = Mid + 1 /\ hi' = hi
  /\ probes' = probes + 1
  /\ UNCHANGED <<red, state>>

\* The bench did not run the gate: the batch is refused for the pass, nothing blamed
\* (faultblames: the fault is read as a red tree).
Fault ==
  /\ Faults /\ state = "search" /\ lo < hi /\ Broken # "last"
  /\ IF Broken = "faultblames"
       THEN hi' = Mid /\ lo' = lo /\ UNCHANGED state
       ELSE state' = "refused" /\ UNCHANGED <<lo, hi>>
  /\ probes' = probes + 1
  /\ UNCHANGED red

\* The span is one head: it is blamed (last: the last head, whatever the span).
Blame ==
  /\ state = "search" /\ (lo = hi \/ Broken = "last")
  /\ state' = "blamed"
  /\ IF Broken = "last" THEN lo' = N /\ hi' = N ELSE UNCHANGED <<lo, hi>>
  /\ UNCHANGED <<red, probes>>

Next == Probe \/ Fault \/ Blame

Spec == Init /\ [][Next]_vars

FairSpec == Spec /\ WF_vars(Probe) /\ WF_vars(Blame)

\* ---- the rules ----

BlameSound == state = "blamed" => red[lo] /\ (lo = 1 \/ ~red[lo - 1])

ProbesBounded == probes <= Log2Ceil(N)

Ends == <>(state # "search")

=============================================================================
