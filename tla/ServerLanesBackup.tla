----------------------------- MODULE ServerLanesBackup -----------------------------
(* The two backup predicates as invariant-free observers of the three counts.
   They are not invariants: a state may be backed up or not. Tick writes one
   judgment (wroteR, wroteM) at each edge that flips and none while the
   predicate holds. WaitExpires is an acknowledgement whose wait has run out:
   it may mark the open episode reminded, and it writes no judgment and moves
   no count. TLC checks BackupTracked and OneJudgmentAtEachEdge, the step's
   bookkeeping, not the observers. docs/SPEC-SPRINT.md, the backup state.

   The configuration is not a file beside this module. TLC on a bench, one
   worker, reads it from this comment:

   SPECIFICATION Spec
   CONSTANT Max = 2
   INVARIANT TypeOK
   INVARIANT BackupTracked
   PROPERTY OneJudgmentAtEachEdge
*)
EXTENDS Naturals
CONSTANT Max
ASSUME Max \in Nat /\ Max >= 1
VARIABLES working, review, merging, openR, openM, wroteR, wroteM, remindedR, remindedM
vars == <<working, review, merging, openR, openM, wroteR, wroteM, remindedR, remindedM>>
ReadsBackedUp(w, r) == r > w
MergesBackedUp(w, r, m) == m > (r + w)
ReadsNow == ReadsBackedUp(working, review)
MergesNow == MergesBackedUp(working, review, merging)
TypeOK == /\ working \in 0..Max /\ review \in 0..Max /\ merging \in 0..Max
          /\ openR \in BOOLEAN /\ openM \in BOOLEAN
          /\ wroteR \in BOOLEAN /\ wroteM \in BOOLEAN
          /\ remindedR \in BOOLEAN /\ remindedM \in BOOLEAN
Init == /\ working = 0 /\ review = 0 /\ merging = 0
        /\ openR = FALSE /\ openM = FALSE
        /\ wroteR = FALSE /\ wroteM = FALSE
        /\ remindedR = FALSE /\ remindedM = FALSE
Tick == \E w, r, m \in 0..Max :
          /\ working' = w /\ review' = r /\ merging' = m
          /\ openR' = ReadsBackedUp(w, r)
          /\ openM' = MergesBackedUp(w, r, m)
          /\ wroteR' = (openR' # openR)
          /\ wroteM' = (openM' # openM)
          /\ remindedR' = IF openR' = openR THEN remindedR ELSE FALSE
          /\ remindedM' = IF openM' = openM THEN remindedM ELSE FALSE
\* A wait running out while the predicate still holds. One side at a step.
WaitExpires ==
  /\ UNCHANGED <<working, review, merging, openR, openM>>
  /\ wroteR' = FALSE /\ wroteM' = FALSE
  /\ \/ /\ ReadsNow /\ openR /\ ~remindedR
        /\ remindedR' = TRUE /\ UNCHANGED remindedM
     \/ /\ MergesNow /\ openM /\ ~remindedM
        /\ remindedM' = TRUE /\ UNCHANGED remindedR
Next == Tick \/ WaitExpires
Spec == Init /\ [][Next]_vars
BackupTracked == openR = ReadsNow /\ openM = MergesNow
OneJudgmentAtEachEdge ==
  [][ /\ openR' = ReadsBackedUp(working', review')
      /\ openM' = MergesBackedUp(working', review', merging')
      /\ wroteR' <=> (openR' # openR)
      /\ wroteM' <=> (openM' # openM)
      /\ remindedR' => (openR' = openR /\ ~wroteR')
      /\ remindedM' => (openM' = openM /\ ~wroteM')
    ]_vars
====
