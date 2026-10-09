------------------------- MODULE CompletionBoundary -------------------------
(* One prior durable native claim across one daemon restart. A visible report
   is not a quiescence or settlement receipt. Three switches reproduce the
   pre-load finish, partial-load overwrite and batch-mode debt bypass gaps.
   Correct guards are a required boundary contract, not code refinement. *)
EXTENDS Naturals
CONSTANTS GuardUnknownFinish, GuardFailedSave, GuardBatchDebt, GuardReleaseBarrier
VARIABLES durableClaim, memoryClaim, ownership, mode, group, quietObserved,
          reportAvailable, canonicalReport, finishJournal, settlementCommitted,
          finishCalled, crashes, batchWorkStarted, badBatchWork, badFinish, badRelease, erasedOnFailedRead,
          beforeLoadFinish, batchDebtFinish, safeRestartFinish
vars == <<durableClaim, memoryClaim, ownership, mode, group, quietObserved,
          reportAvailable, canonicalReport, finishJournal, settlementCommitted,
          finishCalled, crashes, batchWorkStarted, badBatchWork, badFinish, badRelease, erasedOnFailedRead,
          beforeLoadFinish, batchDebtFinish, safeRestartFinish>>
OwnershipKnown == ownership = "known"
DurabilityMet == canonicalReport /\ finishJournal
QuietProof == quietObserved \/ settlementCommitted
ReleaseBarrier == OwnershipKnown /\ group = "quiet" /\ QuietProof /\ DurabilityMet
FinishBarrier == ReleaseBarrier /\ ~durableClaim /\ settlementCommitted

Init == /\ durableClaim = TRUE /\ memoryClaim = TRUE
        /\ ownership = "known" /\ mode = "oneshot" /\ group = "live"
        /\ quietObserved = FALSE /\ reportAvailable = FALSE
        /\ canonicalReport = FALSE /\ finishJournal = FALSE
        /\ settlementCommitted = FALSE /\ finishCalled = FALSE /\ crashes = 0
        /\ batchWorkStarted = FALSE /\ badBatchWork = FALSE
        /\ badFinish = FALSE /\ badRelease = FALSE
        /\ erasedOnFailedRead = FALSE /\ beforeLoadFinish = FALSE
        /\ batchDebtFinish = FALSE /\ safeRestartFinish = FALSE
ReportAppears == /\ ~reportAvailable /\ reportAvailable' = TRUE
                 /\ UNCHANGED <<durableClaim, memoryClaim, ownership, mode,
                      group, quietObserved, canonicalReport, finishJournal,
                      settlementCommitted, finishCalled, crashes, badFinish,
                      badRelease, erasedOnFailedRead, beforeLoadFinish,
                      batchDebtFinish, safeRestartFinish, batchWorkStarted, badBatchWork>>
NativeGroupQuiets == /\ group = "live" /\ group' = "quiet"
                     /\ UNCHANGED <<durableClaim, memoryClaim, ownership,
                          mode, quietObserved, reportAvailable, canonicalReport,
                          finishJournal, settlementCommitted, finishCalled,
                          crashes, badFinish, badRelease, erasedOnFailedRead,
                          beforeLoadFinish, batchDebtFinish, safeRestartFinish, batchWorkStarted, badBatchWork>>
Restart == /\ crashes = 0 /\ crashes' = 1 /\ ownership' = "not-loaded"
           /\ memoryClaim' = FALSE /\ quietObserved' = FALSE
           /\ mode' = "batch" /\ finishCalled' = FALSE
           /\ UNCHANGED <<durableClaim, group, reportAvailable, canonicalReport,
                finishJournal, settlementCommitted, badFinish, badRelease,
                erasedOnFailedRead, beforeLoadFinish, batchDebtFinish,
                safeRestartFinish, batchWorkStarted, badBatchWork>>
LoadKnown == /\ crashes = 1 /\ ~OwnershipKnown
             /\ ownership' = "known" /\ memoryClaim' = durableClaim
             /\ UNCHANGED <<durableClaim, mode, group, quietObserved,
                  reportAvailable, canonicalReport, finishJournal,
                  settlementCommitted, finishCalled, crashes, badFinish,
                  badRelease, erasedOnFailedRead, beforeLoadFinish,
                  batchDebtFinish, safeRestartFinish, batchWorkStarted, badBatchWork>>
LoadFailed == /\ ownership = "not-loaded" /\ ownership' = "failed"
              /\ memoryClaim' = FALSE
              /\ UNCHANGED <<durableClaim, mode, group, quietObserved,
                   reportAvailable, canonicalReport, finishJournal,
                   settlementCommitted, finishCalled, crashes, badFinish,
                   badRelease, erasedOnFailedRead, beforeLoadFinish,
                   batchDebtFinish, safeRestartFinish, batchWorkStarted, badBatchWork>>
LoadPartialError == /\ ownership = "not-loaded" /\ ownership' = "partial"
                    /\ memoryClaim' = FALSE
                    /\ UNCHANGED <<durableClaim, mode, group, quietObserved,
                         reportAvailable, canonicalReport, finishJournal,
                         settlementCommitted, finishCalled, crashes, badFinish,
                         badRelease, erasedOnFailedRead, beforeLoadFinish,
                         batchDebtFinish, safeRestartFinish, batchWorkStarted, badBatchWork>>
OverwritePartial == /\ ownership = "partial" /\ durableClaim
                    /\ ~GuardFailedSave /\ durableClaim' = memoryClaim
                    /\ erasedOnFailedRead' = TRUE
                    /\ UNCHANGED <<memoryClaim, ownership, mode, group,
                         quietObserved, reportAvailable, canonicalReport,
                         finishJournal, settlementCommitted, finishCalled,
                         crashes, badFinish, badRelease, beforeLoadFinish,
                         batchDebtFinish, safeRestartFinish, batchWorkStarted, badBatchWork>>
SelectMode(m) == /\ crashes = 1 /\ mode # m /\ mode' = m
                /\ UNCHANGED <<durableClaim, memoryClaim, ownership, group,
                     quietObserved, reportAvailable, canonicalReport,
                     finishJournal, settlementCommitted, finishCalled, crashes,
                     badFinish, badRelease, erasedOnFailedRead,
                     beforeLoadFinish, batchDebtFinish, safeRestartFinish, batchWorkStarted, badBatchWork>>
VerifyQuiet == /\ crashes = 1 /\ OwnershipKnown /\ group = "quiet"
               /\ ~quietObserved /\ quietObserved' = TRUE
               /\ UNCHANGED <<durableClaim, memoryClaim, ownership, mode,
                    group, reportAvailable, canonicalReport, finishJournal,
                    settlementCommitted, finishCalled, crashes, badFinish,
                    badRelease, erasedOnFailedRead, beforeLoadFinish,
                    batchDebtFinish, safeRestartFinish, batchWorkStarted, badBatchWork>>
PublishCanonicalReport == /\ crashes = 1 /\ OwnershipKnown /\ quietObserved
                          /\ ~canonicalReport /\ canonicalReport' = TRUE
                          /\ reportAvailable' = TRUE
                          /\ UNCHANGED <<durableClaim, memoryClaim, ownership,
                               mode, group, quietObserved, finishJournal,
                               settlementCommitted, finishCalled, crashes,
                               badFinish, badRelease, erasedOnFailedRead,
                               beforeLoadFinish, batchDebtFinish,
                               safeRestartFinish, batchWorkStarted, badBatchWork>>
SaveFinishJournal == /\ OwnershipKnown /\ canonicalReport /\ ~finishJournal
                     /\ finishJournal' = TRUE
                     /\ UNCHANGED <<durableClaim, memoryClaim, ownership, mode,
                          group, quietObserved, reportAvailable, canonicalReport,
                          settlementCommitted, finishCalled, crashes, badFinish,
                          badRelease, erasedOnFailedRead, beforeLoadFinish,
                          batchDebtFinish, safeRestartFinish, batchWorkStarted, badBatchWork>>
ClearStartedDurably == /\ crashes = 1 /\ durableClaim
                       /\ (ReleaseBarrier \/ ~GuardReleaseBarrier)
                       /\ durableClaim' = FALSE /\ memoryClaim' = FALSE
                       /\ settlementCommitted' = TRUE
                       /\ badRelease' = (badRelease \/ ~ReleaseBarrier)
                       /\ UNCHANGED <<ownership, mode, group, quietObserved,
                            reportAvailable, canonicalReport, finishJournal,
                            finishCalled, crashes, badFinish, erasedOnFailedRead,
                            beforeLoadFinish, batchDebtFinish, safeRestartFinish, batchWorkStarted, badBatchWork>>
OuterFinish == /\ crashes = 1 /\ reportAvailable /\ ~finishCalled
               /\ (FinishBarrier \/ (~GuardUnknownFinish /\ ~OwnershipKnown)
                    \/ (~GuardBatchDebt /\ mode = "batch"))
               /\ finishCalled' = TRUE
               /\ badFinish' = (badFinish \/ ~FinishBarrier)
               /\ beforeLoadFinish' = (beforeLoadFinish \/
                    (ownership = "not-loaded" /\ mode = "oneshot"))
               /\ batchDebtFinish' = (batchDebtFinish \/
                    (OwnershipKnown /\ mode = "batch" /\ durableClaim))
               /\ safeRestartFinish' = (safeRestartFinish \/ FinishBarrier)
               /\ UNCHANGED <<durableClaim, memoryClaim, ownership, mode,
                    group, quietObserved, reportAvailable, canonicalReport,
                    finishJournal, settlementCommitted, crashes, badRelease,
                    erasedOnFailedRead, batchWorkStarted, badBatchWork>>
BatchWorkStart == /\ crashes = 1 /\ mode = "batch" /\ ~batchWorkStarted
                  /\ (FinishBarrier \/ ~GuardBatchDebt)
                  /\ batchWorkStarted' = TRUE
                  /\ badBatchWork' = (badBatchWork \/ ~FinishBarrier)
                  /\ UNCHANGED <<durableClaim, memoryClaim, ownership, mode,
                       group, quietObserved, reportAvailable, canonicalReport,
                       finishJournal, settlementCommitted, finishCalled, crashes,
                       badFinish, badRelease, erasedOnFailedRead,
                       beforeLoadFinish, batchDebtFinish, safeRestartFinish>>
Next == ReportAppears \/ NativeGroupQuiets \/ Restart \/ LoadKnown
        \/ LoadFailed \/ LoadPartialError \/ OverwritePartial \/ VerifyQuiet
        \/ PublishCanonicalReport \/ SaveFinishJournal \/ ClearStartedDurably
        \/ OuterFinish \/ BatchWorkStart \/ (\E m \in {"oneshot", "batch"}: SelectMode(m))
Spec == Init /\ [][Next]_vars
TypeOK == /\ {durableClaim, memoryClaim, quietObserved, reportAvailable,
               canonicalReport, finishJournal, settlementCommitted, finishCalled,
               badFinish, badRelease, erasedOnFailedRead, beforeLoadFinish,
               batchDebtFinish, safeRestartFinish, batchWorkStarted, badBatchWork} \subseteq BOOLEAN
          /\ ownership \in {"not-loaded", "known", "failed", "partial"}
          /\ mode \in {"oneshot", "batch"} /\ group \in {"live", "quiet"}
          /\ crashes \in 0..1
KnownMemoryMatchesDisk == OwnershipKnown => memoryClaim = durableClaim
NoFinishBeforeBarrier == ~badFinish
NoReleaseBeforeBarrier == ~badRelease
FailedReadPreservesClaim == ~erasedOnFailedRead
ModeCannotBypassDebt == ~batchDebtFinish
NoBatchWorkWithOldDebt == ~badBatchWork
ModeKeepsClaim == [][(\E m \in {"oneshot", "batch"}: SelectMode(m))
                   => UNCHANGED durableClaim]_vars
WitnessOutboxBeforeLoad == ~beforeLoadFinish
WitnessPartialErrorOverwrite == ~erasedOnFailedRead
WitnessBatchDebtBypass == ~batchDebtFinish
WitnessSafeRestartFinish == ~safeRestartFinish
=============================================================================
