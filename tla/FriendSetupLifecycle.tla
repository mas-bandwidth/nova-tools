------------------------- MODULE FriendSetupLifecycle -------------------------
\* Bounded generic harness setup lifecycle, SPEC-FRIEND.md "Harness setup".
\* Plan is observation only. Apply owns one kernel-locked private profile root.
\* RUN is the durable run id/command hash and then PID/birth receipt. The native
\* gate never releases before that second write. RealExec owns bounded cleanup;
\* Apply advances only after verified group exit, receipt save and validation.
\* A crashed caller releases the kernel lock. Closing a still-gated child's pipe
\* aborts that child; an already launched group survives and holds every retry.
\* Run numbers abstract unique nonces, PID/birth pairs and command hashes; there
\* are no credential values, provider turns, daemon activation or presence claims.
EXTENDS Naturals, FiniteSets
CONSTANTS Actors, Selections, Selection, Steps, Commands, MaxRuns, MaxApplies, Broken
None == 0
Runs == 1..MaxRuns
Faults == {"plan-write", "no-lock", "owner-overwrite", "early-launch",
           "advance-live", "ignore-failure", "blind-retry", "publish-early"}
ASSUME /\ None \notin Actors /\ None \notin Selections
       /\ Selection \in [Actors -> Selections]
       /\ Steps > 0 /\ MaxRuns > 0 /\ MaxApplies > 0
       /\ Commands \in [1..Steps -> 1..Steps]
       /\ Broken \subseteq Faults
VARIABLES pc, lock, owner, step, applies, run, used, groups, receipt,
          runActor, runStep, result, bound, launched, validated, cleaned, published,
          planWrote, lockLost, ownerChanged, advancedLive, advancedFailed,
          retriedLive, crashedAfterLaunch, refusedLive, adoptedDead
vars == <<pc, lock, owner, step, applies, run, used, groups, receipt,
          runActor, runStep, result, bound, launched, validated, cleaned, published,
          planWrote, lockLost, ownerChanged, advancedLive, advancedFailed,
          retriedLive, crashedAfterLaunch, refusedLive, adoptedDead>>
Phases == {"idle", "planned", "locked", "ready", "prepared", "gated", "bound",
           "running", "cleanup", "verified", "ended", "published", "done", "failed", "crashed"}
Holds == {"locked", "ready", "prepared", "gated", "bound", "running", "cleanup",
          "verified", "ended", "published"}
GroupStates == {"absent", "gated", "live", "draining", "unknown", "dead"}
Results == {"pending", "ok", "error", "overflow", "cancel", "save-error", "invalid"}
EmptyReceipt == [stage |-> "absent", id |-> None, hash |-> None,
                 pid |-> None, birth |-> None]
Busy(r) == r # None /\ groups[r] \in {"gated", "live", "draining", "unknown"}
Active == {r \in Runs : groups[r] \in {"live", "draining", "unknown"}}
Go(p, to) == pc' = [pc EXCEPT ![p] = to]

Init ==
 /\ pc = [p \in Actors |-> "idle"] /\ lock = None /\ owner = None
 /\ step = [p \in Actors |-> 1] /\ applies = [p \in Actors |-> 0]
 /\ run = None /\ used = 0 /\ receipt = EmptyReceipt
 /\ groups = [r \in Runs |-> "absent"]
 /\ runActor = [r \in Runs |-> None] /\ runStep = [r \in Runs |-> 0]
 /\ result = [r \in Runs |-> "pending"]
 /\ bound = {} /\ launched = {} /\ validated = {} /\ cleaned = {} /\ published = {}
 /\ planWrote = FALSE /\ lockLost = FALSE /\ ownerChanged = FALSE
 /\ advancedLive = FALSE /\ advancedFailed = FALSE /\ retriedLive = FALSE
 /\ crashedAfterLaunch = {} /\ refusedLive = {} /\ adoptedDead = {}

Plan(p) ==
 /\ pc[p] = "idle" /\ Go(p, "planned")
 /\ planWrote' = (planWrote \/ "plan-write" \in Broken)
 /\ UNCHANGED <<lock, owner, step, applies, run, used, groups, receipt,
      runActor, runStep, result, bound, launched, validated, cleaned, published,
      lockLost, ownerChanged, advancedLive, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

Apply(p) ==
 /\ pc[p] = "planned" /\ applies[p] < MaxApplies
 /\ applies' = [applies EXCEPT ![p] = @ + 1]
 /\ IF lock = None \/ "no-lock" \in Broken
    THEN /\ Go(p, "locked") /\ lock' = p
         /\ lockLost' = (lockLost \/ lock # None)
    ELSE /\ Go(p, "failed") /\ UNCHANGED <<lock, lockLost>>
 /\ UNCHANGED <<owner, step, run, used, groups, receipt, runActor, runStep,
      result, bound, launched, validated, cleaned, published, planWrote, ownerChanged,
      advancedLive, advancedFailed, retriedLive, crashedAfterLaunch,
      refusedLive, adoptedDead>>

\* Check OWNER and old RUN under the lock. Dead receipts are observations to
\* replan; they never count as a completed native step of this invocation.
Inspect(p) ==
 /\ pc[p] = "locked"
 /\ IF owner # None /\ owner # Selection[p] /\ ~("owner-overwrite" \in Broken)
    THEN /\ Go(p, "failed") /\ lock' = None
         /\ UNCHANGED <<owner, ownerChanged, retriedLive, refusedLive, adoptedDead>>
    ELSE IF Busy(run) /\ ~("blind-retry" \in Broken)
         THEN /\ Go(p, "failed") /\ lock' = None /\ refusedLive' = refusedLive \cup {p}
              /\ UNCHANGED <<owner, ownerChanged, retriedLive, adoptedDead>>
         ELSE /\ Go(p, "ready") /\ UNCHANGED lock
              /\ owner' = Selection[p]
              /\ ownerChanged' = (ownerChanged \/ (owner # None /\ owner # Selection[p]))
              /\ retriedLive' = (retriedLive \/ Busy(run))
              /\ adoptedDead' = IF run # None /\ groups[run] = "dead" THEN adoptedDead \cup {p} ELSE adoptedDead
              /\ UNCHANGED refusedLive
 /\ UNCHANGED <<step, applies, run, used, groups, receipt, runActor, runStep,
      result, bound, launched, validated, cleaned, published, planWrote, lockLost,
      advancedLive, advancedFailed, crashedAfterLaunch>>

Prepare(p) ==
 /\ pc[p] = "ready" /\ step[p] <= Steps /\ used < MaxRuns
 /\ LET r == used + 1 IN
    /\ run' = r /\ used' = r
    /\ receipt' = [stage |-> "prepared", id |-> r, hash |-> Commands[step[p]],
                   pid |-> None, birth |-> None]
    /\ runActor' = [runActor EXCEPT ![r] = p]
    /\ runStep' = [runStep EXCEPT ![r] = step[p]]
 /\ Go(p, "prepared")
 /\ UNCHANGED <<lock, owner, step, applies, groups, result, bound, launched,
      validated, cleaned, published, planWrote, lockLost, ownerChanged, advancedLive,
      advancedFailed, retriedLive, crashedAfterLaunch, refusedLive, adoptedDead>>

SpawnGated(p) ==
 /\ pc[p] = "prepared" /\ Go(p, "gated")
 /\ groups' = [groups EXCEPT ![run] = "gated"]
 /\ UNCHANGED <<lock, owner, step, applies, run, used, receipt, runActor,
      runStep, result, bound, launched, validated, cleaned, published, planWrote, lockLost,
      ownerChanged, advancedLive, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

BindReceipt(p) ==
 /\ pc[p] = "gated" /\ Go(p, "bound")
 /\ receipt' = [receipt EXCEPT !.stage = "bound", !.pid = run, !.birth = run]
 /\ bound' = bound \cup {run}
 /\ UNCHANGED <<lock, owner, step, applies, run, used, groups, runActor,
      runStep, result, launched, validated, cleaned, published, planWrote, lockLost,
      ownerChanged, advancedLive, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

Launch(p) ==
 /\ pc[p] = "bound" \/ (pc[p] = "gated" /\ "early-launch" \in Broken)
 /\ Go(p, "running") /\ groups' = [groups EXCEPT ![run] = "live"]
 /\ launched' = launched \cup {run}
 /\ UNCHANGED <<lock, owner, step, applies, run, used, receipt, runActor,
      runStep, result, bound, validated, cleaned, published, planWrote, lockLost,
      ownerChanged, advancedLive, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

\* Leader exit/cancellation/output cap enter the executor's cleanup phase.
LeaderExit(p, outcome) ==
 /\ pc[p] = "running" /\ outcome \in Results \ {"pending"}
 /\ Go(p, "cleanup")
 /\ groups' = IF groups[run] = "dead" THEN groups ELSE [groups EXCEPT ![run] = "draining"]
 /\ result' = [result EXCEPT ![run] = outcome]
 /\ UNCHANGED <<lock, owner, step, applies, run, used, receipt, runActor,
      runStep, bound, launched, validated, cleaned, published, planWrote, lockLost,
      ownerChanged, advancedLive, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

CleanupUnknown(p) ==
 /\ pc[p] = "cleanup" /\ groups[run] = "draining"
 /\ groups' = [groups EXCEPT ![run] = "unknown"]
 /\ Go(p, "failed") /\ lock' = None
 /\ UNCHANGED <<owner, step, applies, run, used, receipt, runActor, runStep,
      result, bound, launched, validated, cleaned, published, planWrote, lockLost,
      ownerChanged, advancedLive, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

\* A fresh observation proves no runnable member remains. It can arrive after
\* a refused/uncertain cleanup or an interrupted caller, without signalling.
GroupDead(r) ==
 /\ r <= used /\ groups[r] \in {"live", "draining", "unknown"}
 /\ groups' = [groups EXCEPT ![r] = "dead"] /\ cleaned' = cleaned \cup {r}
 /\ UNCHANGED <<pc, lock, owner, step, applies, run, used, receipt, runActor,
      runStep, result, bound, launched, validated, published, planWrote, lockLost,
      ownerChanged, advancedLive, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

CleanupVerified(p) ==
 /\ pc[p] = "cleanup"
 /\ groups[run] = "dead" \/ "advance-live" \in Broken
 /\ Go(p, "verified")
 /\ advancedLive' = (advancedLive \/ groups[run] # "dead")
 /\ UNCHANGED <<lock, owner, step, applies, run, used, groups, receipt,
      runActor, runStep, result, bound, launched, validated, cleaned, published, planWrote,
      lockLost, ownerChanged, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

SaveEnded(p) ==
 /\ pc[p] = "verified"
 /\ IF result[run] = "save-error"
    THEN /\ Go(p, "failed") /\ lock' = None /\ UNCHANGED receipt
    ELSE /\ Go(p, "ended") /\ UNCHANGED lock
         /\ receipt' = [receipt EXCEPT !.stage = "ended"]
 /\ UNCHANGED <<owner, step, applies, run, used, groups, runActor, runStep,
      result, bound, launched, validated, cleaned, published, planWrote, lockLost,
      ownerChanged, advancedLive, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

Validate(p) ==
 /\ pc[p] = "ended"
 /\ IF result[run] = "ok" \/ "ignore-failure" \in Broken
    THEN /\ Go(p, "ready") /\ step' = [step EXCEPT ![p] = @ + 1]
         /\ validated' = validated \cup {run}
         /\ advancedFailed' = (advancedFailed \/ result[run] # "ok")
         /\ UNCHANGED lock
    ELSE /\ Go(p, "failed") /\ lock' = None
         /\ UNCHANGED <<step, validated, advancedFailed>>
 /\ UNCHANGED <<owner, applies, run, used, groups, receipt, runActor, runStep,
      result, bound, launched, cleaned, published, planWrote, lockLost, ownerChanged,
      advancedLive, retriedLive, crashedAfterLaunch, refusedLive, adoptedDead>>

\* Publish is the final atomic PROFILE write and exact readback. A failed
\* write/readback never reports a complete apply; installation is a later layer.
Publish(p) ==
 /\ pc[p] = "ready"
 /\ step[p] = Steps + 1 \/ "publish-early" \in Broken
 /\ Go(p, "published") /\ published' = published \cup {<<p, applies[p]>>}
 /\ UNCHANGED <<lock, owner, step, applies, run, used, groups, receipt,
      runActor, runStep, result, bound, launched, validated, cleaned, planWrote,
      lockLost, ownerChanged, advancedLive, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

PublishFailure(p) ==
 /\ pc[p] = "ready" /\ step[p] = Steps + 1
 /\ Go(p, "failed") /\ lock' = None
 /\ UNCHANGED <<owner, step, applies, run, used, groups, receipt, runActor,
      runStep, result, bound, launched, validated, cleaned, published, planWrote, lockLost,
      ownerChanged, advancedLive, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

Finish(p) ==
 /\ pc[p] = "published" /\ step[p] = Steps + 1
 /\ <<p, applies[p]>> \in published
 /\ Go(p, "done") /\ lock' = None
 /\ UNCHANGED <<owner, step, applies, run, used, groups, receipt, runActor,
      runStep, result, bound, launched, validated, cleaned, published, planWrote, lockLost,
      ownerChanged, advancedLive, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

\* A failed prelaunch write/identity/anchor setup never opens the native gate.
AbortBeforeLaunch(p) ==
 /\ pc[p] \in {"prepared", "gated", "bound"}
 /\ Go(p, "failed") /\ lock' = None
 /\ groups' = [groups EXCEPT ![run] = "dead"]
 /\ UNCHANGED <<owner, step, applies, run, used, receipt, runActor, runStep,
      result, bound, launched, validated, cleaned, published, planWrote, lockLost,
      ownerChanged, advancedLive, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

Crash(p) ==
 /\ pc[p] \in Holds /\ Go(p, "crashed") /\ lock' = None
 /\ groups' = IF run # None /\ groups[run] = "gated"
              THEN [groups EXCEPT ![run] = "dead"] ELSE groups
 /\ crashedAfterLaunch' = IF pc[p] \in {"running", "cleanup"} /\ run # None /\ runActor[run] = p /\ run \in launched /\ Busy(run) THEN crashedAfterLaunch \cup {p} ELSE crashedAfterLaunch
 /\ UNCHANGED <<owner, step, applies, run, used, receipt, runActor, runStep,
      result, bound, launched, validated, cleaned, published, planWrote, lockLost,
      ownerChanged, advancedLive, advancedFailed, retriedLive,
      refusedLive, adoptedDead>>

Replan(p) ==
 /\ pc[p] \in {"failed", "crashed"} /\ applies[p] < MaxApplies
 /\ Go(p, "planned") /\ step' = [step EXCEPT ![p] = 1]
 /\ UNCHANGED <<lock, owner, applies, run, used, groups, receipt, runActor,
      runStep, result, bound, launched, validated, cleaned, published, planWrote, lockLost,
      ownerChanged, advancedLive, advancedFailed, retriedLive,
      crashedAfterLaunch, refusedLive, adoptedDead>>

Next ==
 \/ \E p \in Actors : Plan(p) \/ Apply(p) \/ Inspect(p) \/ Prepare(p)
       \/ SpawnGated(p) \/ BindReceipt(p) \/ Launch(p) \/ CleanupUnknown(p)
       \/ CleanupVerified(p) \/ SaveEnded(p) \/ Validate(p)
       \/ Publish(p) \/ PublishFailure(p) \/ Finish(p)
       \/ AbortBeforeLaunch(p) \/ Crash(p) \/ Replan(p)
       \/ (\E outcome \in Results \ {"pending"} : LeaderExit(p, outcome))
 \/ \E r \in Runs : GroupDead(r)
Spec == Init /\ [][Next]_vars

TypeOK ==
 /\ pc \in [Actors -> Phases] /\ lock \in Actors \cup {None}
 /\ owner \in Selections \cup {None} /\ step \in [Actors -> 1..(Steps + 1)]
 /\ applies \in [Actors -> 0..MaxApplies] /\ run \in Runs \cup {None}
 /\ used \in 0..MaxRuns /\ groups \in [Runs -> GroupStates]
 /\ receipt.stage \in {"absent", "prepared", "bound", "ended"}
 /\ receipt.id \in Runs \cup {None} /\ receipt.hash \in (1..Steps) \cup {None}
 /\ receipt.pid \in Runs \cup {None} /\ receipt.birth \in Runs \cup {None}
 /\ runActor \in [Runs -> Actors \cup {None}] /\ runStep \in [Runs -> 0..Steps]
 /\ result \in [Runs -> Results]
 /\ bound \subseteq Runs /\ launched \subseteq Runs
 /\ validated \subseteq Runs /\ cleaned \subseteq Runs
 /\ published \subseteq (Actors \X (1..MaxApplies))
 /\ crashedAfterLaunch \subseteq Actors /\ refusedLive \subseteq Actors /\ adoptedDead \subseteq Actors
PlanReadOnly == ~planWrote
ProfileMutualExclusion == ~lockLost /\ Cardinality({p \in Actors : pc[p] \in Holds}) <= 1
OwnerNeverChanges == ~ownerChanged
LaunchAfterDurableBirth == launched \subseteq bound
ReceiptIdentity == receipt.stage \in {"bound", "ended"} =>
                   receipt.hash = Commands[runStep[receipt.id]] /\ receipt.id = receipt.pid /\ receipt.id = receipt.birth
AdvanceAfterGroupExit == ~advancedLive /\ validated \subseteq cleaned
FailureStopsSequence == ~advancedFailed
RetryAfterGroupExit == ~retriedLive /\ Cardinality(Active) <= 1
PublishAfterNative == \A p \in Actors : pc[p] \in {"published", "done"} =>
                      step[p] = Steps + 1 /\ <<p, applies[p]>> \in published
DoneHasEveryStep == \A p \in Actors : pc[p] = "done" =>
                   Cardinality({r \in validated : runActor[r] = p}) >= Steps
\* Reversed witnesses fail only when the nontrivial required state is reached.
WitnessApplied == ~\E p \in Actors : pc[p] = "done" /\ step[p] = Steps + 1
WitnessCleanup == ~(cleaned # {} /\ validated # {})
WitnessInterruptedRetry == ~\E p \in Actors :
 /\ p \in crashedAfterLaunch \cap refusedLive \cap adoptedDead
 /\ pc[p] = "done" /\ step[p] = Steps + 1
=============================================================================
