------------------------------ MODULE ServerInstall ------------------------------
\* nova-sprint's install as state (cmd/nova-sprint/server_switch.go, run.go;
\* docs/SPEC-SPRINT.md section 14, install-rollback-on-missed-ticks-b.w6).
\*
\* server switch shadows a candidate and swaps it in, keeping the previous
\* binary. The new server is on probation for its first Probation ticks: a tick
\* that misses its deadline (TickMissed) or the process exiting (Exit) puts the
\* previous binary back, restarts it, logs the tick that failed and pushes the
\* seat one note; Probation good ticks (TickOK) end the probation, which
\* ProbationEnd records as kept. A binary rolled back is refused by switch
\* (Shadow requires it not in refused) until a new binary is named.
\*
\* The state: phase is where the seat is (running old, canary, swapped on
\* probation, kept, rolled back); serving is the set of binaries serving now;
\* ticks counts the probation ticks seen; cand is the candidate; refused is
\* every binary a rollback put back; plog and ppush say the rollback was logged
\* and the seat pushed; pphase is the phase before the last step, and last names
\* the step, so a rule can say what a step did.
\*
\* Broken = "none" is the design. Every other value is a reversed witness:
\*   "twoservers"       swap starts the new server before the old is stopped:
\*                      both serve at once
\*   "noroollbackexit"  an exit during probation leaves the probation standing,
\*                      rolling nothing back
\*   "laterollback"     a rollback after the probation ended takes the kept
\*                      binary out
\*   "latemissed"       TickMissed remains enabled after all probation ticks
\*                      passed, rolling back the candidate on tick N+1
\*   "reserverefused"   switch shadows a binary it already rolled back
\* Each is caught by one invariant below; the design passes all.

EXTENDS Naturals, FiniteSets

CONSTANTS Candidates, Probation, Broken

Old == "old"
Binaries == Candidates \cup {Old}
Phases == {"old", "canary", "probation", "kept", "rolledback"}

VARIABLES phase, serving, ticks, cand, refused, plog, ppush, pphase, last
vars == <<phase, serving, ticks, cand, refused, plog, ppush, pphase, last>>

TypeOK ==
  /\ phase \in Phases
  /\ serving \in SUBSET Binaries
  /\ ticks \in 0..Probation
  /\ cand \in Binaries
  /\ refused \in SUBSET Binaries
  /\ plog \in BOOLEAN
  /\ ppush \in BOOLEAN
  /\ pphase \in Phases

\* pphase keeps the phase before the last step, for the invariants below.
Remember == pphase' = phase

Init ==
  /\ phase = "old"
  /\ serving = {Old}
  /\ ticks = 0
  /\ cand = Old
  /\ refused = {}
  /\ plog = FALSE
  /\ ppush = FALSE
  /\ pphase = "old"
  /\ last = <<"start">>

\* server switch names a candidate and shadows it. It never names a binary a
\* rollback refused: a rollback never loops.
Shadow(b) ==
  /\ b \in Candidates
  /\ b \notin refused
  /\ phase \in {"old", "rolledback"}
  /\ phase' = "canary"
  /\ cand' = b
  /\ UNCHANGED <<serving, ticks, refused, plog, ppush>>
  /\ Remember
  /\ last' = <<"shadow", b>>

\* the shadow passed: the seat is swapped to the candidate, which is on
\* probation for its first ticks. The old server is stopped as the new one
\* starts; the reversed witness "twoservers" starts the new one beside it.
Swap ==
  /\ phase = "canary"
  /\ serving' = IF Broken = "twoservers" THEN serving \cup {cand} ELSE {cand}
  /\ phase' = "probation"
  /\ ticks' = 0
  /\ UNCHANGED <<cand, refused, plog, ppush>>
  /\ Remember
  /\ last' = <<"swap", cand>>

\* a probation tick met its deadline.
TickOK ==
  /\ phase = "probation"
  /\ ticks < Probation
  /\ ticks' = ticks + 1
  /\ phase' = "probation"
  /\ UNCHANGED <<serving, cand, refused, plog, ppush>>
  /\ Remember
  /\ last' = <<"tickok", ticks + 1>>

\* the last probation tick passed: the binary is kept.
ProbationEnd ==
  /\ phase = "probation"
  /\ ticks = Probation
  /\ phase' = "kept"
  /\ UNCHANGED <<serving, ticks, cand, refused, plog, ppush>>
  /\ Remember
  /\ last' = <<"probationend">>

\* a probation tick missed its deadline: the previous binary is rolled back,
\* the candidate is refused, the rollback is logged and the seat pushed.
TickMissed ==
  /\ phase = "probation"
  /\ (Broken = "latemissed" \/ ticks < Probation)
  /\ phase' = "rolledback"
  /\ refused' = refused \cup {cand}
  /\ serving' = {Old}
  /\ plog' = TRUE
  /\ ppush' = TRUE
  /\ ticks' = 0
  /\ UNCHANGED cand
  /\ Remember
  /\ last' = <<"rollback", "missed", ticks + 1>>

\* the probation server exited. The design rolls back; the reversed witness
\* "noroollbackexit" leaves the probation standing and rolls nothing back.
Exit ==
  /\ phase = "probation"
  /\ phase' = IF Broken = "noroollbackexit" THEN "probation" ELSE "rolledback"
  /\ refused' = IF Broken = "noroollbackexit" THEN refused ELSE refused \cup {cand}
  /\ serving' = IF Broken = "noroollbackexit" THEN serving ELSE {Old}
  /\ plog' = IF Broken = "noroollbackexit" THEN plog ELSE TRUE
  /\ ppush' = IF Broken = "noroollbackexit" THEN ppush ELSE TRUE
  /\ ticks' = IF Broken = "noroollbackexit" THEN ticks ELSE 0
  /\ UNCHANGED cand
  /\ Remember
  /\ last' = <<"exit">>

\* the reversed witness "laterollback": a rollback after the probation ended.
LateRollback ==
  /\ Broken = "laterollback"
  /\ phase = "kept"
  /\ phase' = "rolledback"
  /\ refused' = refused \cup {cand}
  /\ serving' = {Old}
  /\ plog' = TRUE
  /\ ppush' = TRUE
  /\ ticks' = 0
  /\ UNCHANGED cand
  /\ Remember
  /\ last' = <<"laterollback">>

\* the reversed witness "reserverefused": switch shadows a binary already
\* rolled back, which the design refuses.
ReServe(b) ==
  /\ Broken = "reserverefused"
  /\ b \in refused
  /\ phase \in {"old", "rolledback"}
  /\ phase' = "canary"
  /\ cand' = b
  /\ UNCHANGED <<serving, ticks, refused, plog, ppush>>
  /\ Remember
  /\ last' = <<"shadow", b>>

Next ==
  \/ \E b \in Candidates : Shadow(b)
  \/ Swap
  \/ TickOK
  \/ ProbationEnd
  \/ TickMissed
  \/ Exit
  \/ LateRollback
  \/ \E b \in Binaries : ReServe(b)

Spec == Init /\ [][Next]_vars

\* ---------------------------------------------------------------- the rules

\* Exactly one server serves at any time.
ExactlyOneServer ==
  Cardinality(serving) = 1

\* A binary a rollback put back is never served again by switch: a rollback
\* never loops.
RolledBackNeverServed ==
  \A b \in refused : b \notin serving

\* An exit during probation is rolled back.
ExitRollsBack ==
  last[1] = "exit" => phase = "rolledback"

\* A rollback never happens after the probation ended: a kept binary stays.
NoLateRollback ==
  last[1] = "laterollback" => pphase = "probation"

\* A missed tick cannot roll a candidate back after all N probation ticks
\* passed; last[3] records the tick number that caused a rollback.
NoRollbackAfterProbation ==
  last[1] = "rollback" => last[3] <= Probation

\* A rollback logs the tick that failed and pushes the seat one note.
RollbackLoggedAndPushed ==
  last[1] \in {"rollback", "laterollback"} => plog /\ ppush

=============================================================================
