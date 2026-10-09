--------------------------- MODULE DSHAccountRoute ---------------------------
(* Companion to FriendSetupLifecycle: public account routing and current RUN/
   PROFILE proof only. Authentication stays unknown; no provider turn occurs.
   Native init/schema actions assume the existing owned, durable native runner.
   Route epochs abstract existing RUN identities, not a second receipt/index. *)
EXTENDS Naturals
CONSTANTS InitialCertificate, InitialStatic, InitialPatch, Fault
VARIABLES staticPresent, patch, overlay, phase, currentRun, runState, schemaState, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, authState, providerState, usedImplicit, usedKey, badPublish, badInspect, badReuse, reused, readyReached, failedNewerHeld
vars == <<staticPresent, patch, overlay, phase, currentRun, runState, schemaState, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, authState, providerState, usedImplicit, usedKey, badPublish, badInspect, badReuse, reused, readyReached, failedNewerHeld>>
ExactEffectiveRoute == patch = "account" /\ overlay = "none"
CurrentProof == /\ profileKind = "account" /\ profileRun = currentRun
                /\ runState = "ended" /\ schemaState = "passed"
                /\ schemaRun = currentRun /\ schemaRoute = "account" /\ schemaClean
CanReuse == CurrentProof /\ staticPresent /\ ExactEffectiveRoute
ReuseCandidate == CanReuse \/ (Fault = "static-reuse" /\ staticPresent /\ ExactEffectiveRoute)
RouteCheck == ExactEffectiveRoute \/ (Fault \in {"implicit", "key-fallback"} /\ overlay = "none") \/ (Fault = "overlay-accept" /\ patch = "account")
Init == staticPresent = (InitialStatic) /\ patch = (InitialPatch) /\ overlay = ("none") /\ phase = ("idle") /\ currentRun = (0) /\ runState = (IF InitialCertificate = "none" THEN "none" ELSE "ended") /\ schemaState = (IF InitialCertificate = "account" THEN "passed" ELSE "unknown") /\ schemaRun = (0) /\ schemaRoute = (IF InitialCertificate = "account" THEN "account" ELSE "none") /\ schemaClean = (InitialCertificate = "account") /\ profileKind = (InitialCertificate) /\ profileRun = (0) /\ authState = ("unknown") /\ providerState = ("not_checked") /\ usedImplicit = (FALSE) /\ usedKey = (FALSE) /\ badPublish = (FALSE) /\ badInspect = (FALSE) /\ badReuse = (FALSE) /\ reused = (FALSE) /\ readyReached = (FALSE) /\ failedNewerHeld = (FALSE)
Plan == phase \in {"idle", "published", "failed", "accepted", "refused"}
    /\ phase' = ("planned")
    /\ UNCHANGED <<staticPresent, patch, overlay, currentRun, runState, schemaState, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, authState, providerState, usedImplicit, usedKey, badPublish, badInspect, badReuse, reused, readyReached, failedNewerHeld>>

ApplyFresh == phase = "planned"
    /\ overlay = "none"
    /\ ~ReuseCandidate
    /\ currentRun < 2
    /\ phase' = (IF staticPresent THEN "writing-route" ELSE "initializing")
    /\ currentRun' = (currentRun + 1)
    /\ runState' = ("prepared")
    /\ schemaState' = ("unknown")
    /\ UNCHANGED <<staticPresent, patch, overlay, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, authState, providerState, usedImplicit, usedKey, badPublish, badInspect, badReuse, reused, readyReached, failedNewerHeld>>

NativeStaticInit == phase = "initializing"
    /\ overlay = "none"
    /\ staticPresent' = (TRUE)
    /\ phase' = ("writing-route")
    /\ UNCHANGED <<patch, overlay, currentRun, runState, schemaState, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, authState, providerState, usedImplicit, usedKey, badPublish, badInspect, badReuse, reused, readyReached, failedNewerHeld>>

WritePublicRoute == phase = "writing-route"
    /\ overlay = "none"
    /\ patch' = (CASE Fault = "implicit" -> "implicit" [] Fault = "key-fallback" -> "api-key" [] OTHER -> "account")
    /\ phase' = ("schema")
    /\ usedImplicit' = ((usedImplicit \/ Fault = "implicit"))
    /\ usedKey' = ((usedKey \/ Fault = "key-fallback"))
    /\ authState' = (IF Fault = "auth-from-route" THEN "ready" ELSE authState)
    /\ UNCHANGED <<staticPresent, overlay, currentRun, runState, schemaState, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, providerState, badPublish, badInspect, badReuse, reused, readyReached, failedNewerHeld>>

NativeSchemaPass == phase = "schema"
    /\ RouteCheck
    /\ phase' = ("validated")
    /\ runState' = ("ended")
    /\ schemaState' = ("passed")
    /\ schemaRun' = (currentRun)
    /\ schemaRoute' = (patch)
    /\ schemaClean' = ((overlay = "none"))
    /\ UNCHANGED <<staticPresent, patch, overlay, currentRun, profileKind, profileRun, authState, providerState, usedImplicit, usedKey, badPublish, badInspect, badReuse, reused, readyReached, failedNewerHeld>>

NativeSchemaFail == phase = "schema"
    /\ phase' = ("failed")
    /\ runState' = ("failed")
    /\ schemaState' = ("failed")
    /\ UNCHANGED <<staticPresent, patch, overlay, currentRun, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, authState, providerState, usedImplicit, usedKey, badPublish, badInspect, badReuse, reused, readyReached, failedNewerHeld>>

PublishProfile == ((phase = "validated" /\ ExactEffectiveRoute) \/ (Fault = "drift-publish" /\ phase = "validated") \/ (Fault = "early-publish" /\ phase = "schema") \/ (Fault = "failed-schema-publish" /\ phase = "failed"))
    /\ profileKind' = ("account")
    /\ profileRun' = (currentRun)
    /\ phase' = ("published")
    /\ badPublish' = ((badPublish \/ ~(runState = "ended" /\ schemaState = "passed" /\ schemaRun = currentRun /\ schemaRoute = "account" /\ schemaClean /\ ExactEffectiveRoute)))
    /\ UNCHANGED <<staticPresent, patch, overlay, currentRun, runState, schemaState, schemaRun, schemaRoute, schemaClean, authState, providerState, usedImplicit, usedKey, badInspect, badReuse, reused, readyReached, failedNewerHeld>>

ReuseCertifiedProfile == phase = "planned"
    /\ ReuseCandidate
    /\ phase' = ("published")
    /\ reused' = (TRUE)
    /\ badReuse' = ((badReuse \/ ~CanReuse))
    /\ UNCHANGED <<staticPresent, patch, overlay, currentRun, runState, schemaState, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, authState, providerState, usedImplicit, usedKey, badPublish, badInspect, readyReached, failedNewerHeld>>

NewerProofAttempt == phase \in {"published", "accepted"}
    /\ currentRun < 2
    /\ currentRun' = (currentRun + 1)
    /\ runState' = ("prepared")
    /\ schemaState' = ("unknown")
    /\ phase' = ("writing-route")
    /\ UNCHANGED <<staticPresent, patch, overlay, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, authState, providerState, usedImplicit, usedKey, badPublish, badInspect, badReuse, reused, readyReached, failedNewerHeld>>

EffectiveRouteDrifts == phase \in {"validated", "published", "accepted"}
    /\ patch = "account"
    /\ patch' = ("drift")
    /\ UNCHANGED <<staticPresent, overlay, phase, currentRun, runState, schemaState, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, authState, providerState, usedImplicit, usedKey, badPublish, badInspect, badReuse, reused, readyReached, failedNewerHeld>>

ExtraOverlayAppears == phase \in {"idle", "planned", "writing-route", "schema", "validated", "published", "accepted"}
    /\ overlay = "none"
    /\ overlay' = ("extra")
    /\ UNCHANGED <<staticPresent, patch, phase, currentRun, runState, schemaState, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, authState, providerState, usedImplicit, usedKey, badPublish, badInspect, badReuse, reused, readyReached, failedNewerHeld>>

InspectForActivation == phase \in {"published", "failed"}
    /\ (CanReuse \/ (Fault = "stale-inspection" /\ CurrentProof))
    /\ phase' = ("accepted")
    /\ readyReached' = (TRUE)
    /\ badInspect' = ((badInspect \/ ~CanReuse))
    /\ UNCHANGED <<staticPresent, patch, overlay, currentRun, runState, schemaState, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, authState, providerState, usedImplicit, usedKey, badPublish, badReuse, reused, failedNewerHeld>>

RefuseInspection == phase \in {"published", "failed"}
    /\ ~CanReuse
    /\ phase' = ("refused")
    /\ failedNewerHeld' = ((failedNewerHeld \/ (schemaState = "failed" /\ profileKind = "account" /\ profileRun # currentRun)))
    /\ UNCHANGED <<staticPresent, patch, overlay, currentRun, runState, schemaState, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, authState, providerState, usedImplicit, usedKey, badPublish, badInspect, badReuse, reused, readyReached>>

Next == Plan \/ ApplyFresh \/ NativeStaticInit \/ WritePublicRoute
        \/ NativeSchemaPass \/ NativeSchemaFail \/ PublishProfile
        \/ ReuseCertifiedProfile \/ NewerProofAttempt \/ EffectiveRouteDrifts
        \/ ExtraOverlayAppears \/ InspectForActivation \/ RefuseInspection
Spec == Init /\ [][Next]_vars
TypeOK == /\ staticPresent \in BOOLEAN
          /\ patch \in {"none", "account", "implicit", "api-key", "drift"}
          /\ overlay \in {"none", "extra"}
          /\ phase \in {"idle", "planned", "initializing", "writing-route", "schema", "validated", "published", "failed", "accepted", "refused"}
          /\ currentRun \in 0..2 /\ profileRun \in 0..2 /\ schemaRun \in 0..2
          /\ runState \in {"none", "prepared", "ended", "failed"}
          /\ schemaState \in {"unknown", "passed", "failed"} /\ schemaClean \in BOOLEAN
          /\ schemaRoute \in {"none", "account", "implicit", "api-key", "drift"}
          /\ profileKind \in {"none", "static", "account"}
          /\ authState \in {"unknown", "ready"} /\ providerState = "not_checked"
          /\ {usedImplicit, usedKey, badPublish, badInspect, badReuse, reused, readyReached, failedNewerHeld} \subseteq BOOLEAN
PlanReadOnly == [][Plan => UNCHANGED <<staticPresent, patch, overlay, currentRun, runState, schemaState, schemaRun, schemaRoute, schemaClean, profileKind, profileRun, authState, providerState>>]_vars
NoImplicitRoute == ~usedImplicit
NoKeyFallback == ~usedKey
PublishAfterRouteSchema == ~badPublish
SchemaProofHasNoOverlay == (schemaState = "passed") => schemaClean
FailedSchemaCannotBindCurrentProfile == (schemaState = "failed") => ~(profileKind = "account" /\ profileRun = currentRun)
CurrentProofRequiredForReuse == ~badReuse
FreshEffectiveRouteInspection == ~badInspect
RouteDoesNotProveAuth == authState = "unknown" /\ providerState = "not_checked"
WitnessRouteInspected == ~readyReached
WitnessCertifiedReuse == ~reused
WitnessFailedNewerHeld == ~failedNewerHeld
=============================================================================
