---------------------------- MODULE LandBisect ----------------------------
\* The tree gate of a batch (cmd/nova-sprint/landbatch.go, gateBatch and
\* bisectRed; docs/SPEC-SPRINT.md section 7, the tree gate). land merges a
\* batch's N heads, then gates the batch's tip once. A green tip lands the
\* batch. A red tip is bisected over the prefixes of the batch to the head whose
\* merge turned a green prefix red: that head is blamed (the conflict fact) and
\* the green prefix before it is what is pushed. The base (prefix 0) is green:
\* land gates it before any merge and refuses the batch when it is red.
\*
\* THE STATE.
\*   red     the gate's verdict on each prefix 1..N of the batch (TRUE: red),
\*           any mix, chosen once: a red head may be followed by one that makes
\*           the tree green again (the verdicts need not be monotone); the
\*           regeneration of the generated ledgers at a prefix's tip is part of
\*           its verdict (settleLedgers)
\*   phase   gate (the batch's tip not yet gated), bisect, done
\*   lo, hi  the longest prefix known green and the shortest known red
\*   probes  the prefixes gated after the batch's tip
\*   landed  the prefix pushed when done (0: nothing)
\*   blamed  the head blamed when done (0: none)
\*
\* THE ACTIONS. Gate (the batch's tip, once), Probe (one prefix strictly
\* between lo and hi, its middle), Finish (lo and hi one head apart).
\*
\* THE RULES.
\*   GreenLanded: every prefix pushed is green (the base, or a prefix gated
\*     green): every tip land pushes passed the gate on the tree it is.
\*   BlameIsTheTurn: the head blamed is red at its prefix and the prefix before
\*     it, the one landed, is green: a head is blamed only for turning a green
\*     tree red.
\*   AllOrBlame: a batch lands whole exactly when no head is blamed.
\*   FirstRedWhenMonotone: where the verdicts are monotone (every prefix past
\*     the first red one red), the head blamed is the first red one, the head
\*     the gate of every tip blamed.
\*   FewProbes: at most ceil(log2 N) probes.
\*   Terminates: under fairness the gate ends.
\* Reversed witnesses: ReachBlameEarly (a head before the last is blamed, so
\* the bisection is not vacuous) and ReachWhole (a green batch lands whole).
\*
\* Broken: "none" is the design.
\*   "tiponly" blames the last head of a red tip and lands the rest unprobed
\*     (GreenLanded fails: an earlier head may be the red one).
\*   "lowside" moves hi, not lo, on a green probe (BlameIsTheTurn fails: a
\*     green head is blamed).
EXTENDS Integers, TLC

CONSTANTS N, Broken

VARIABLES red, phase, lo, hi, probes, landed, blamed

vars == <<red, phase, lo, hi, probes, landed, blamed>>

Green(k) == k = 0 \/ ~red[k]

Log2Ceil(n) == CHOOSE k \in 0..n : 2^k >= n /\ (k = 0 \/ 2^(k - 1) < n)

TypeOK ==
  /\ red \in [1..N -> BOOLEAN]
  /\ phase \in {"gate", "bisect", "done"}
  /\ lo \in 0..N /\ hi \in 0..N
  /\ probes \in 0..N
  /\ landed \in 0..N /\ blamed \in 0..N

Init ==
  /\ red \in [1..N -> BOOLEAN]
  /\ phase = "gate" /\ lo = 0 /\ hi = N /\ probes = 0 /\ landed = 0 /\ blamed = 0

Gate ==
  /\ phase = "gate"
  /\ CASE ~red[N] -> phase' = "done" /\ landed' = N /\ UNCHANGED blamed
       [] Broken = "tiponly" -> phase' = "done" /\ landed' = N - 1 /\ blamed' = N
       [] OTHER -> phase' = "bisect" /\ UNCHANGED <<landed, blamed>>
  /\ UNCHANGED <<red, lo, hi, probes>>

Probe ==
  /\ phase = "bisect" /\ hi - lo > 1
  /\ LET mid == lo + (hi - lo) \div 2 IN
       IF Green(mid) /\ Broken # "lowside"
       THEN lo' = mid /\ UNCHANGED hi
       ELSE hi' = mid /\ UNCHANGED lo
  /\ probes' = probes + 1
  /\ UNCHANGED <<red, phase, landed, blamed>>

Finish ==
  /\ phase = "bisect" /\ hi - lo = 1
  /\ phase' = "done" /\ landed' = lo /\ blamed' = hi
  /\ UNCHANGED <<red, lo, hi, probes>>

Next == Gate \/ Probe \/ Finish

Spec == Init /\ [][Next]_vars

FairSpec == Spec /\ WF_vars(Next)

\* ---- the rules ----

GreenLanded == phase = "done" => Green(landed)

BlameIsTheTurn == phase = "done" /\ blamed # 0 => blamed = landed + 1 /\ red[blamed] /\ Green(landed)

AllOrBlame == phase = "done" => (blamed = 0 <=> landed = N)

FirstRed == CHOOSE k \in 1..N : red[k] /\ \A j \in 1..(k - 1) : ~red[j]

Monotone == \A k \in 1..N : red[k] => \A j \in k..N : red[j]

FirstRedWhenMonotone == phase = "done" /\ blamed # 0 /\ Monotone => blamed = FirstRed

FewProbes == probes <= Log2Ceil(N)

\* the bisection's bounds: lo green, hi red, lo before hi
Bounds == phase = "bisect" => Green(lo) /\ red[hi] /\ lo < hi

Terminates == <>(phase = "done")

\* ---- reversed witnesses ----

ReachBlameEarly == ~(phase = "done" /\ blamed # 0 /\ blamed < N)

ReachWhole == ~(phase = "done" /\ landed = N)
=============================================================================
