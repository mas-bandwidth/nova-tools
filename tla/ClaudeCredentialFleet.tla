---------------------------- MODULE ClaudeCredentialFleet ----------------------------
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS Broken

NoToken == <<"no-token", 0>>
NoBinding == <<"no-binding">>

Accounts == {"work", "personal"}
Generations == 0..2
MaxGeneration == 2
PrimarySeat == "primary-seat"
SourceMachine == "source-host"
TargetMachineA == "target-a"
TargetMachineB == "target-b"

\* The source/target tagged union in internal/fleet/credential_seats.go.
\* Both source seats share one source host. The work profile is deployed to
\* two target hosts; one target host also holds a second account profile.
SourceBindings == {
  <<"work", SourceMachine, "work-source">>,
  <<"personal", SourceMachine, "personal-source">>
}
TargetBindings == {
  <<"work", TargetMachineA, "work-profile", "work-target-a-seat">>,
  <<"work", TargetMachineB, "work-profile", "work-target-b-seat">>,
  <<"personal", TargetMachineA, "personal-profile", "personal-target-a-seat">>
}

AuthKind == [a \in Accounts |-> IF a = "work" THEN "local-oauth" ELSE "subscription-token"]
\* A local OAuth refresh remains on its owner machine. The subscription token
\* mode has no shared refresh owner: a new value is an explicit owner action.
RefreshOwner == [a \in Accounts |-> IF a = "work" THEN SourceMachine ELSE NoBinding]
SourceRoute(a) == CHOOSE b \in SourceBindings : b[1] = a
Token(a, g) == <<a, g>>
TokenSet == {Token(a, g) : a \in Accounts, g \in Generations}
SourceSeatNames == {b[3] : b \in SourceBindings}
TargetSeatNames == {b[4] : b \in TargetBindings}

VARIABLES latest, sourceToken, storeToken, targetToken, processToken,
          sourceUsed, placedAt, proof, revoked, providerValid, revocationEvidence
vars == <<latest, sourceToken, storeToken, targetToken, processToken,
          sourceUsed, placedAt, proof, revoked, providerValid, revocationEvidence>>

TypeOK ==
  /\ latest \in [Accounts -> Generations]
  /\ sourceToken \in [Accounts -> TokenSet]
  /\ storeToken \in [Accounts -> {NoToken} \cup TokenSet]
  /\ targetToken \in [TargetBindings -> {NoToken} \cup TokenSet]
  /\ processToken \in [TargetBindings -> {NoToken} \cup TokenSet]
  /\ sourceUsed \in [TargetBindings -> {NoBinding} \cup SourceBindings]
  /\ placedAt \in [TargetBindings -> {NoBinding} \cup TargetBindings]
  /\ proof \in [TargetBindings -> {NoToken} \cup TokenSet]
  /\ revoked \subseteq TokenSet
  /\ providerValid \subseteq TokenSet
  /\ revocationEvidence \in [TokenSet -> SUBSET TargetBindings]

RegistryBindingsValid ==
  /\ \A a \in Accounts : \E b \in SourceBindings : b[1] = a
  /\ \A b1, b2 \in SourceBindings : b1[1] = b2[1] => b1 = b2
  /\ \A t1, t2 \in TargetBindings : t1[3] = t2[3] => t1[1] = t2[1]
  /\ \A t1, t2 \in TargetBindings :
       (t1[1] = t2[1] /\ t1[2] = t2[2] /\ t1[3] = t2[3]) => t1 = t2
  /\ Cardinality(SourceSeatNames) = Cardinality(SourceBindings)
  /\ Cardinality(TargetSeatNames) = Cardinality(TargetBindings)
  /\ SourceSeatNames \cap TargetSeatNames = {}
  /\ PrimarySeat \notin SourceSeatNames \cup TargetSeatNames

Init ==
  /\ latest = [a \in Accounts |-> 0]
  /\ sourceToken = [a \in Accounts |-> Token(a, 0)]
  /\ storeToken = [a \in Accounts |-> Token(a, 0)]
  /\ targetToken = [t \in TargetBindings |-> Token(t[1], 0)]
  /\ processToken = [t \in TargetBindings |-> Token(t[1], 0)]
  /\ sourceUsed = [t \in TargetBindings |-> SourceRoute(t[1])]
  /\ placedAt = [t \in TargetBindings |-> t]
  /\ proof = [t \in TargetBindings |-> NoToken]
  /\ revoked = {}
  /\ providerValid \in SUBSET {Token(a, 0) : a \in Accounts}
  /\ revocationEvidence = [token \in TokenSet |-> {}]

IssueGeneration(a) ==
  /\ latest[a] < MaxGeneration
  /\ latest' = [latest EXCEPT ![a] = @ + 1]
  /\ sourceToken' = [sourceToken EXCEPT ![a] = Token(a, latest[a] + 1)]
  /\ proof' = [t \in TargetBindings |-> IF t[1] = a THEN NoToken ELSE proof[t]]
  /\ UNCHANGED <<storeToken, targetToken, processToken, sourceUsed, placedAt,
                 revoked, providerValid, revocationEvidence>>

OAuthRefreshAtSource(a) ==
  /\ a = "work"
  /\ RefreshOwner[a] = SourceRoute(a)[2]
  /\ Token(a, latest[a] + 1) \notin revoked
  /\ IssueGeneration(a)

OwnerIssuesSubscriptionToken(a) ==
  /\ a = "personal"
  /\ RefreshOwner[a] = NoBinding
  /\ Token(a, latest[a] + 1) \notin revoked
  /\ IssueGeneration(a)

SealAtSource(a) ==
  /\ storeToken[a] # sourceToken[a]
  /\ sourceToken[a] \notin revoked
  /\ storeToken' = [storeToken EXCEPT ![a] = sourceToken[a]]
  /\ UNCHANGED <<latest, sourceToken, targetToken, processToken, sourceUsed,
                 placedAt, proof, revoked, providerValid, revocationEvidence>>

Delivery(t, source, destination, token) ==
  /\ targetToken' = [targetToken EXCEPT ![t] = token]
  /\ sourceUsed' = [sourceUsed EXCEPT ![t] = source]
  /\ placedAt' = [placedAt EXCEPT ![t] = destination]
  /\ proof' = [proof EXCEPT ![t] = NoToken]
  /\ UNCHANGED <<latest, sourceToken, storeToken, processToken, revoked,
                 providerValid, revocationEvidence>>

DeliverExact(t) ==
  /\ SourceRoute(t[1]) \in SourceBindings
  /\ storeToken[t[1]] # targetToken[t]
  /\ storeToken[t[1]] # NoToken
  /\ storeToken[t[1]] \notin revoked
  /\ Delivery(t, SourceRoute(t[1]), t, storeToken[t[1]])

RestartAtTarget(t) ==
  /\ targetToken[t] # NoToken
  /\ targetToken[t] \notin revoked
  /\ processToken' = [processToken EXCEPT ![t] = targetToken[t]]
  /\ proof' = [proof EXCEPT ![t] = NoToken]
  /\ UNCHANGED <<latest, sourceToken, storeToken, targetToken, sourceUsed,
                 placedAt, revoked, providerValid, revocationEvidence>>

NativeTurnSucceeds(t) ==
  /\ targetToken[t] # NoToken
  /\ processToken[t] = targetToken[t]
  /\ targetToken[t] = Token(t[1], latest[t[1]])
  /\ targetToken[t] \notin revoked
  /\ targetToken[t] \in providerValid
  /\ proof' = [proof EXCEPT ![t] = targetToken[t]]
  /\ UNCHANGED <<latest, sourceToken, storeToken, targetToken, processToken,
                 sourceUsed, placedAt, revoked, providerValid, revocationEvidence>>

ProviderAccepts(token) ==
  /\ token \notin revoked
  /\ token = sourceToken[token[1]]
  /\ providerValid' = providerValid \cup {token}
  /\ UNCHANGED <<latest, sourceToken, storeToken, targetToken, processToken,
                 sourceUsed, placedAt, proof, revoked, revocationEvidence>>

ProviderExpires(token) ==
  /\ token \in providerValid
  /\ providerValid' = providerValid \ {token}
  /\ proof' = [t \in TargetBindings |-> IF proof[t] = token THEN NoToken ELSE proof[t]]
  /\ UNCHANGED <<latest, sourceToken, storeToken, targetToken, processToken,
                 sourceUsed, placedAt, revoked, revocationEvidence>>

NativeReady(t) ==
  /\ t \in TargetBindings
  /\ targetToken[t] # NoToken
  /\ targetToken[t] = Token(t[1], latest[t[1]])
  /\ processToken[t] = targetToken[t]
  /\ proof[t] = targetToken[t]
  /\ targetToken[t] \in providerValid
  /\ targetToken[t] \notin revoked

FleetCurrent(a) ==
  \A t \in TargetBindings : t[1] = a => targetToken[t] = Token(a, latest[a])
FleetHasNativeSuccess(a) ==
  \A t \in TargetBindings : t[1] = a => NativeReady(t)
ProcessDrainedOf(a, token) ==
  \A t \in TargetBindings : t[1] = a => processToken[t] # token

RevokeOldGeneration(a, g) ==
  /\ g \in Generations
  /\ g < latest[a]
  /\ storeToken[a] = Token(a, latest[a])
  /\ FleetCurrent(a)
  /\ FleetHasNativeSuccess(a)
  /\ ProcessDrainedOf(a, Token(a, g))
  /\ Token(a, g) \notin revoked
  /\ revoked' = revoked \cup {Token(a, g)}
  /\ revocationEvidence' = [revocationEvidence EXCEPT ![Token(a, g)] = {t \in TargetBindings : t[1] = a}]
  /\ proof' = [t \in TargetBindings |-> IF t[1] = a THEN NoToken ELSE proof[t]]
  /\ UNCHANGED <<latest, sourceToken, storeToken, targetToken, processToken,
                 sourceUsed, placedAt, providerValid>>

WrongSourceRoute(a) ==
  IF a = "work"
  THEN SourceRoute("personal")
  ELSE SourceRoute("work")

BreakWrongSource(t) ==
  /\ storeToken[t[1]] # NoToken
  /\ storeToken[t[1]] \notin revoked
  /\ Delivery(t, WrongSourceRoute(t[1]), t, storeToken[t[1]])

BreakWrongTarget(t) ==
  /\ storeToken[t[1]] # NoToken
  /\ storeToken[t[1]] \notin revoked
  /\ \E u \in TargetBindings : u[1] = t[1] /\ u # t
  /\ LET u == CHOOSE x \in TargetBindings : x[1] = t[1] /\ x # t
     IN Delivery(t, SourceRoute(t[1]), u, storeToken[t[1]])

BreakCrossAccount(t) ==
  /\ LET other == IF t[1] = "work" THEN "personal" ELSE "work"
     IN /\ storeToken[other] # NoToken
        /\ Delivery(t, SourceRoute(other), t, storeToken[other])

BreakEarlyRevocation(a, g) ==
  /\ g \in Generations
  /\ g < latest[a]
  /\ Token(a, g) \notin revoked
  /\ revoked' = revoked \cup {Token(a, g)}
  /\ UNCHANGED <<latest, sourceToken, storeToken, targetToken, processToken,
                 sourceUsed, placedAt, proof, providerValid, revocationEvidence>>

BreakRevokeBeforeNative(a, g) ==
  /\ g \in Generations
  /\ g < latest[a]
  /\ storeToken[a] = Token(a, latest[a])
  /\ FleetCurrent(a)
  /\ ProcessDrainedOf(a, Token(a, g))
  /\ Token(a, g) \notin revoked
  /\ revoked' = revoked \cup {Token(a, g)}
  /\ UNCHANGED <<latest, sourceToken, storeToken, targetToken, processToken,
                 sourceUsed, placedAt, proof, providerValid, revocationEvidence>>

BreakReplayRevoked(t) ==
  /\ \E token \in revoked : token[1] = t[1]
  /\ LET old == CHOOSE token \in revoked : token[1] = t[1]
     IN Delivery(t, SourceRoute(t[1]), t, old)

NoCrossAccountToken ==
  /\ \A a \in Accounts : sourceToken[a][1] = a
  /\ \A a \in Accounts : storeToken[a] = NoToken \/ storeToken[a][1] = a
  /\ \A t \in TargetBindings : targetToken[t] = NoToken \/ targetToken[t][1] = t[1]
  /\ \A t \in TargetBindings : processToken[t] = NoToken \/ processToken[t][1] = t[1]

SourceIsLatestGeneration ==
  \A a \in Accounts : sourceToken[a] = Token(a, latest[a])

TargetGenerationDoesNotLeadSource ==
  \A t \in TargetBindings : targetToken[t] = NoToken \/ targetToken[t][2] <= latest[t[1]]

DeliveryUsesExactSource ==
  \A t \in TargetBindings : sourceUsed[t] = NoBinding \/ sourceUsed[t] = SourceRoute(t[1])
DeliveryUsesExactTarget ==
  \A t \in TargetBindings : placedAt[t] = NoBinding \/ placedAt[t] = t

NoRevokedGenerationInUse ==
  /\ \A a \in Accounts : storeToken[a] = NoToken \/ storeToken[a] \notin revoked
  /\ \A t \in TargetBindings : targetToken[t] = NoToken \/ targetToken[t] \notin revoked
  /\ \A t \in TargetBindings : processToken[t] = NoToken \/ processToken[t] \notin revoked
  /\ \A t \in TargetBindings : proof[t] = NoToken \/ proof[t] \notin revoked

RevocationHasNativeEvidence ==
  \A token \in revoked :
    revocationEvidence[token] = {t \in TargetBindings : t[1] = token[1]}

ProofIsForCurrentInstalledGeneration ==
  \A t \in TargetBindings :
    proof[t] = NoToken \/ (proof[t] = targetToken[t]
      /\ proof[t] = processToken[t]
      /\ proof[t] = Token(t[1], latest[t[1]])
      /\ proof[t] \notin revoked)

RefreshCustodyPreserved ==
  /\ AuthKind["work"] = "local-oauth"
  /\ RefreshOwner["work"] = SourceRoute("work")[2]
  /\ AuthKind["personal"] = "subscription-token"
  /\ RefreshOwner["personal"] = NoBinding
  /\ \A t \in TargetBindings : t[1] = "personal" => t[2] # SourceMachine

PartialFleetDistribution ==
  \E a \in Accounts :
    /\ latest[a] > 0
    /\ storeToken[a] = Token(a, latest[a])
    /\ \E t \in TargetBindings : t[1] = a /\ targetToken[t] = Token(a, latest[a])
    /\ \E u \in TargetBindings : u[1] = a /\ targetToken[u] # Token(a, latest[a])

NoPartialFleet == ~PartialFleetDistribution

Next ==
  \/ \E a \in Accounts : OAuthRefreshAtSource(a)
  \/ \E a \in Accounts : OwnerIssuesSubscriptionToken(a)
  \/ \E a \in Accounts : SealAtSource(a)
  \/ \E t \in TargetBindings : DeliverExact(t)
  \/ \E t \in TargetBindings : RestartAtTarget(t)
  \/ \E t \in TargetBindings : NativeTurnSucceeds(t)
  \/ \E token \in TokenSet : ProviderAccepts(token)
  \/ \E token \in TokenSet : ProviderExpires(token)
  \/ \E a \in Accounts : \E g \in Generations : RevokeOldGeneration(a, g)
  \/ (Broken = "wrong-source" /\ \E t \in TargetBindings : BreakWrongSource(t))
  \/ (Broken = "wrong-target" /\ \E t \in TargetBindings : BreakWrongTarget(t))
  \/ (Broken = "cross-account" /\ \E t \in TargetBindings : BreakCrossAccount(t))
  \/ (Broken = "early-revoke" /\ \E a \in Accounts : \E g \in Generations : BreakEarlyRevocation(a, g))
  \/ (Broken = "revoke-before-native" /\ \E a \in Accounts : \E g \in Generations : BreakRevokeBeforeNative(a, g))
  \/ (Broken = "replay-revoked" /\ \E t \in TargetBindings : BreakReplayRevoked(t))

Spec == Init /\ [][Next]_vars

=============================================================================
