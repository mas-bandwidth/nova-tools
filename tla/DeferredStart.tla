--------------------------- MODULE DeferredStart ---------------------------
(* One staged work brief, two bounded delivery attempts, and one native start.
   Delivery is not native admission. "atomic" is the required launch contract,
   not a claim that daemon delivery already implements it. "cached" exposes
   the gap between reading permission and releasing a queued native launch. *)
EXTENDS Naturals
CONSTANTS GuardRetryDelivery, NativeGuard, MaxAttempts
VARIABLES running, paused, known, phase, attempts, deferredOnce, deferredPaused,
          permit, badRetry, badStart, delayedStart, queuedStart, staleStart, retryAfterPause, queuedBeforePause,
          drainedPaused, resumedDeferred
vars == <<running, paused, known, phase, attempts, deferredOnce, deferredPaused,
          permit, badRetry, badStart, delayedStart, queuedStart, staleStart, retryAfterPause, queuedBeforePause,
          drainedPaused, resumedDeferred>>
MayLaunch == running /\ ~paused /\ known
Init == /\ running = TRUE /\ paused = FALSE /\ known = TRUE
        /\ phase = "unstaged" /\ attempts = 0
        /\ deferredOnce = FALSE /\ deferredPaused = FALSE /\ permit = FALSE
        /\ badRetry = FALSE /\ badStart = FALSE /\ delayedStart = FALSE
        /\ queuedStart = FALSE /\ staleStart = FALSE
        /\ retryAfterPause = FALSE /\ queuedBeforePause = FALSE
        /\ drainedPaused = FALSE /\ resumedDeferred = FALSE

StageBrief == /\ phase = "unstaged" /\ phase' = "staged"
              /\ UNCHANGED <<running, paused, known, attempts, deferredOnce,
                   deferredPaused, permit, badRetry, badStart, delayedStart,
                   queuedStart, staleStart, drainedPaused, resumedDeferred, retryAfterPause, queuedBeforePause>>
QueueStart == /\ phase = "staged" /\ phase' = "queued"
              /\ UNCHANGED <<running, paused, known, attempts, deferredOnce,
                   deferredPaused, permit, badRetry, badStart, delayedStart,
                   queuedStart, staleStart, drainedPaused, resumedDeferred, retryAfterPause, queuedBeforePause>>
BeginDelivery == /\ phase \in {"queued", "deferred"}
                 /\ attempts < MaxAttempts
                 /\ (IF phase = "queued" THEN MayLaunch
                     ELSE (~GuardRetryDelivery \/ MayLaunch))
                 /\ phase' = "delivering" /\ attempts' = attempts + 1
                 /\ badRetry' = (badRetry \/ (phase = "deferred" /\ ~MayLaunch))
                 /\ retryAfterPause' = (retryAfterPause \/ (phase = "deferred" /\ paused))
                 /\ UNCHANGED <<running, paused, known, deferredOnce, deferredPaused,
                      permit, badStart, delayedStart, queuedStart, staleStart,
                      drainedPaused, resumedDeferred, queuedBeforePause>>
DeliveryDeferred == /\ phase = "delivering" /\ phase' = "deferred"
                    /\ deferredOnce' = TRUE
                    /\ deferredPaused' = (deferredPaused \/ paused)
                    /\ UNCHANGED <<running, paused, known, attempts,
                         permit, badRetry, badStart, delayedStart, queuedStart,
                         staleStart, drainedPaused, resumedDeferred, retryAfterPause, queuedBeforePause>>
DeliveryAccepted == /\ phase = "delivering" /\ phase' = "native-queued"
                    /\ UNCHANGED <<running, paused, known, attempts,
                         deferredOnce, deferredPaused, permit, badRetry, badStart,
                         delayedStart, queuedStart, staleStart, drainedPaused,
                         resumedDeferred, retryAfterPause, queuedBeforePause>>
ReadNativePermit == /\ NativeGuard = "cached"
                    /\ phase = "native-queued" /\ MayLaunch
                    /\ permit' = TRUE /\ phase' = "native-checked"
                    /\ UNCHANGED <<running, paused, known, attempts,
                         deferredOnce, deferredPaused, badRetry, badStart,
                         delayedStart, queuedStart, staleStart, drainedPaused,
                         resumedDeferred, retryAfterPause, queuedBeforePause>>
NativeStart == /\ phase \in {"native-queued", "native-checked"}
               /\ (CASE NativeGuard = "atomic" -> MayLaunch
                     [] NativeGuard = "cached" -> permit
                     [] OTHER -> TRUE)
               /\ phase' = "active"
               /\ badStart' = (badStart \/ ~MayLaunch)
               /\ delayedStart' = (delayedStart \/ (paused /\ deferredOnce))
               /\ queuedStart' = (queuedStart \/ (paused /\ ~deferredOnce))
               /\ staleStart' = (staleStart \/ (permit /\ ~MayLaunch))
               /\ resumedDeferred' = (resumedDeferred \/
                    (deferredOnce /\ deferredPaused /\ MayLaunch))
               /\ UNCHANGED <<running, paused, known, attempts, deferredOnce,
                    deferredPaused, permit, badRetry, drainedPaused, retryAfterPause, queuedBeforePause>>
FinishActive == /\ phase = "active" /\ phase' = "done"
                /\ drainedPaused' = (drainedPaused \/ paused)
                /\ UNCHANGED <<running, paused, known, attempts, deferredOnce,
                     deferredPaused, permit, badRetry, badStart, delayedStart,
                     queuedStart, staleStart, resumedDeferred, retryAfterPause, queuedBeforePause>>
Pause == /\ running /\ ~paused /\ paused' = TRUE
         /\ deferredPaused' = (deferredPaused \/ phase = "deferred")
         /\ queuedBeforePause' = (queuedBeforePause \/ phase = "native-queued")
         /\ UNCHANGED <<running, known, phase, attempts, deferredOnce, permit,
              badRetry, badStart, delayedStart, queuedStart, staleStart,
              drainedPaused, resumedDeferred, retryAfterPause>>
Unpause == /\ running /\ paused /\ paused' = FALSE
           /\ UNCHANGED <<running, known, phase, attempts, deferredOnce,
                deferredPaused, permit, badRetry, badStart, delayedStart, queuedStart,
                staleStart, drainedPaused, resumedDeferred, retryAfterPause, queuedBeforePause>>
Stop == /\ running /\ running' = FALSE /\ paused' = FALSE
        /\ UNCHANGED <<known, phase, attempts, deferredOnce, deferredPaused, permit,
             badRetry, badStart, delayedStart, queuedStart, staleStart,
             drainedPaused, resumedDeferred, retryAfterPause, queuedBeforePause>>
AuthorityUnknown == /\ known /\ known' = FALSE
                    /\ UNCHANGED <<running, paused, phase, attempts,
                         deferredOnce, deferredPaused, permit, badRetry, badStart,
                         delayedStart, queuedStart, staleStart, drainedPaused,
                         resumedDeferred, retryAfterPause, queuedBeforePause>>
AuthorityKnown == /\ ~known /\ known' = TRUE
                  /\ UNCHANGED <<running, paused, phase, attempts,
                       deferredOnce, deferredPaused, permit, badRetry, badStart,
                       delayedStart, queuedStart, staleStart, drainedPaused,
                       resumedDeferred, retryAfterPause, queuedBeforePause>>
Next == StageBrief \/ QueueStart \/ BeginDelivery \/ DeliveryDeferred
        \/ DeliveryAccepted \/ ReadNativePermit \/ NativeStart \/ FinishActive
        \/ Pause \/ Unpause \/ Stop \/ AuthorityUnknown \/ AuthorityKnown
Spec == Init /\ [][Next]_vars
TypeOK == /\ running \in BOOLEAN /\ paused \in BOOLEAN /\ known \in BOOLEAN
          /\ phase \in {"unstaged", "staged", "queued", "delivering",
               "deferred", "native-queued", "native-checked", "active", "done"}
          /\ attempts \in 0..MaxAttempts
          /\ {deferredOnce, deferredPaused, permit, badRetry, badStart, delayedStart,
               queuedStart, staleStart, drainedPaused, resumedDeferred, retryAfterPause,
               queuedBeforePause} \subseteq BOOLEAN
RetryDeliveryNeedsAuthority == ~badRetry
NoNewNativeStartWhileHeld == ~badStart
StoppedCannotUnpause == ~running => ~paused
PauseKeepsAssignment == [][Pause => UNCHANGED <<phase, attempts>>]_vars
ActiveCanDrainWhilePaused == (phase = "active" /\ paused) => ENABLED FinishActive
WitnessDeferredAfterPause == ~(delayedStart /\ retryAfterPause)
WitnessQueuedAfterPause == ~(queuedStart /\ queuedBeforePause)
WitnessStaleNativePermit == ~staleStart
WitnessPausedDrain == ~drainedPaused
WitnessUnpausedDeferredStart == ~resumedDeferred
=============================================================================
