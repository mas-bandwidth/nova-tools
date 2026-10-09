------------------------- MODULE NativeOwnershipDebt -------------------------
(* One already-admitted card, its native primary and one auxiliary group.
   External server completion/reassignment does not prove local group exit.
   A callback may return while descendants or inspection identity remain owed.
   This module isolates the competing-end path omitted by the start models. *)
EXTENDS Naturals

CONSTANTS EarlyDrop, IgnoreRemovalSave
VARIABLE s
vars == <<s>>

Init == s = [server |-> "working", primary |-> "live",
             primaryQuietObserved |-> FALSE,
             aux |-> "none", auxPhysical |-> FALSE,
             returned |-> FALSE, externalEnded |-> FALSE,
             cancellationRequested |-> FALSE,
             startedPresent |-> TRUE, localClaim |-> TRUE,
             capacityHeld |-> TRUE, dropped |-> FALSE,
             durableRemoval |-> FALSE, removalSaveFailed |-> FALSE,
             hadLiveReturn |-> FALSE, hadUnknownAux |-> FALSE,
             reused |-> FALSE]

AuxQuiet == ~s.auxPhysical /\ s.aux \in {"none", "quiet"}
QuietProof == s.primary = "quiet" /\ s.primaryQuietObserved /\ AuxQuiet

(* These are server events, independent of this daemon's native process. *)
ExternalDone == /\ s.server = "working"
                /\ s' = [s EXCEPT !.server = "done"]
ExternalReassign == /\ s.server = "working"
                    /\ s' = [s EXCEPT !.server = "reassigned"]
NoticeExternalEnd == /\ s.server # "working" /\ ~s.externalEnded
                     /\ s.localClaim /\ s.startedPresent
                     /\ s' = [s EXCEPT !.externalEnded = TRUE,
                                      !.cancellationRequested = TRUE]

(* A returned Go turn/Wait result is not proof that all descendants exited. *)
NativeReturn == /\ ~s.returned /\ s.localClaim
                /\ s' = [s EXCEPT !.returned = TRUE,
                                 !.hadLiveReturn = (s.primary = "live")]
PrimaryExit == /\ s.primary = "live"
               /\ s' = [s EXCEPT !.primary = "quiet"]
ObservePrimaryQuiet == /\ s.primary = "quiet"
                       /\ ~s.primaryQuietObserved
                       /\ s' = [s EXCEPT !.primaryQuietObserved = TRUE]

LaunchAux == /\ s.aux = "none" /\ s.localClaim
             /\ s' = [s EXCEPT !.aux = "live", !.auxPhysical = TRUE]
LoseAuxIdentity == /\ s.aux = "live"
                   /\ s' = [s EXCEPT !.aux = "unknown",
                                    !.hadUnknownAux = TRUE]
AuxExit == /\ s.auxPhysical
           /\ s' = [s EXCEPT !.auxPhysical = FALSE]
(* This is a positive recovered identity/exit proof, not absence of a PID.
   The kernel and identity verification that produce it are outside scope. *)
ObserveAuxQuiet == /\ ~s.auxPhysical /\ s.aux \in {"live", "unknown"}
                   /\ s' = [s EXCEPT !.aux = "quiet"]

DropEligible == s.externalEnded /\ s.returned /\
                s.localClaim /\ s.startedPresent /\
                (EarlyDrop \/ QuietProof)
(* Source baseline: laneDone's ended branch calls setDown before native proof.
   Corrected contract: proof precedes durable removal and freeing the lane. *)
DropClaim == /\ DropEligible
             /\ s' = [s EXCEPT !.startedPresent = FALSE,
                              !.localClaim = FALSE, !.capacityHeld = FALSE,
                              !.dropped = TRUE, !.durableRemoval = TRUE]
RemovalSaveFails == /\ DropEligible /\ ~s.removalSaveFailed
                    /\ s' = [s EXCEPT !.removalSaveFailed = TRUE,
                                     !.startedPresent = IF IgnoreRemovalSave THEN FALSE ELSE @,
                                     !.localClaim = IF IgnoreRemovalSave THEN FALSE ELSE @,
                                     !.capacityHeld = IF IgnoreRemovalSave THEN FALSE ELSE @,
                                     !.dropped = IF IgnoreRemovalSave THEN TRUE ELSE @]
ReuseCapacity == /\ s.dropped /\ ~s.capacityHeld /\ ~s.reused
                 /\ s' = [s EXCEPT !.reused = TRUE]

Next == \/ ExternalDone \/ ExternalReassign \/ NoticeExternalEnd
        \/ NativeReturn \/ PrimaryExit \/ ObservePrimaryQuiet
        \/ LaunchAux \/ LoseAuxIdentity \/ AuxExit \/ ObserveAuxQuiet
        \/ DropClaim \/ RemovalSaveFails \/ ReuseCapacity
Spec == Init /\ [][Next]_vars

TypeOK == /\ s.server \in {"working", "done", "reassigned"}
          /\ s.primary \in {"live", "quiet"}
          /\ s.aux \in {"none", "live", "unknown", "quiet"}
NoCapacityBeforeQuiet == ~s.capacityHeld => QuietProof
NoDropWithoutDurableRemoval == s.dropped => s.durableRemoval
LocalDebtRetained == (~QuietProof /\ ~EarlyDrop) =>
                     (s.startedPresent /\ s.localClaim /\ s.capacityHeld)
ExternalEndNeverCompletesNativeWork == s.dropped =>
                                      (s.externalEnded /\ s.returned)

(* Negated witnesses: each dedicated case must find the named transition. *)
WitnessExternalDoneLiveHeld == ~(s.server = "done" /\ s.externalEnded /\
                                s.primary = "live" /\ s.capacityHeld /\
                                s.startedPresent /\ s.localClaim)
WitnessReassignedUnknownHeld == ~(s.server = "reassigned" /\ s.externalEnded /\
                                 s.aux = "unknown" /\ s.auxPhysical /\
                                 s.capacityHeld /\ s.startedPresent)
WitnessReturnedLiveHeld == ~(s.externalEnded /\ s.returned /\
                            s.primary = "live" /\ s.hadLiveReturn /\
                            s.capacityHeld /\ s.startedPresent)
WitnessUnknownAuxAfterExitHeld == ~(s.externalEnded /\ s.returned /\
                                  s.primaryQuietObserved /\
                                  s.aux = "unknown" /\ ~s.auxPhysical /\
                                  s.capacityHeld /\ s.startedPresent)
WitnessReleaseAfterProof == ~(s.dropped /\ s.reused /\ s.hadUnknownAux /\
                             s.hadLiveReturn /\ QuietProof /\ s.durableRemoval)
WitnessRemovalSaveRefused == ~(s.removalSaveFailed /\ QuietProof /\
                              s.capacityHeld /\ s.localClaim /\
                              s.startedPresent /\ ~s.dropped)
WitnessSaveRetryReleases == ~(s.removalSaveFailed /\ s.dropped /\
                             s.reused /\ QuietProof /\ s.durableRemoval)
=============================================================================
