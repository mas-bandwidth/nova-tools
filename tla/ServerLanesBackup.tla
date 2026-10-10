----------------------------- MODULE ServerLanesBackup -----------------------------
(* The two backup predicates as invariant-free observers of the three counts.
   They are not invariants: a state may be backed up or not. The step raises
   one judgment at each edge that flips and none while the predicate holds.
   TLC checks BackupTracked and OneJudgmentAtEachEdge, the step's bookkeeping,
   not the observers. docs/SPEC-SPRINT.md, the backup state. *)
EXTENDS Naturals
CONSTANT Max
ASSUME Max \in Nat /\ Max >= 1
VARIABLES working, review, merging, openR, openM
vars == <<working, review, merging, openR, openM>>
ReadsBackedUp(w, r) == r > w
MergesBackedUp(w, r, m) == m > (r + w)
ReadsNow == ReadsBackedUp(working, review)
MergesNow == MergesBackedUp(working, review, merging)
TypeOK == /\ working \in 0..Max /\ review \in 0..Max /\ merging \in 0..Max
          /\ openR \in BOOLEAN /\ openM \in BOOLEAN
Init == /\ working = 0 /\ review = 0 /\ merging = 0
        /\ openR = FALSE /\ openM = FALSE
Tick == \E w, r, m \in 0..Max :
          /\ working' = w /\ review' = r /\ merging' = m
          /\ openR' = ReadsBackedUp(w, r)
          /\ openM' = MergesBackedUp(w, r, m)
Next == Tick
Spec == Init /\ [][Next]_vars
BackupTracked == openR = ReadsNow /\ openM = MergesNow
OneJudgmentAtEachEdge ==
  [][ /\ openR' = ReadsBackedUp(working', review')
      /\ openM' = MergesBackedUp(working', review', merging')
      /\ (openR' # openR) <=> (ReadsBackedUp(working', review') # ReadsNow)
      /\ (openM' # openM) <=> (MergesBackedUp(working', review', merging') # MergesNow)
    ]_vars
====
