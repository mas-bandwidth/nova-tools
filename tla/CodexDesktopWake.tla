------------------------ MODULE CodexDesktopWake ------------------------
\* The one-call native desktop wake adapter. The bus remains event truth;
\* the desktop's accepted turn id is turn truth. A write whose outcome is not
\* known is never retried here.

EXTENDS Naturals

CONSTANT Broken

VARIABLES phase, writes, appTurns, turnID, promptSource, inheritsSettings

vars == <<phase, writes, appTurns, turnID, promptSource, inheritsSettings>>

Phases == {"new", "initialized", "owner", "waiting", "accepted",
           "rejected", "pre-send-failed", "uncertain"}
Terminal == {"accepted", "rejected", "pre-send-failed", "uncertain"}

Init ==
    /\ phase = "new"
    /\ writes = 0
    /\ appTurns = 0
    /\ turnID = 0
    /\ promptSource = "none"
    /\ inheritsSettings = FALSE

Initialize ==
    /\ phase = "new"
    /\ phase' = "initialized"
    /\ UNCHANGED <<writes, appTurns, turnID, promptSource, inheritsSettings>>

DiscoverOwner ==
    /\ phase = "initialized"
    /\ phase' = "owner"
    /\ UNCHANGED <<writes, appTurns, turnID, promptSource, inheritsSettings>>

PreSendFail ==
    /\ phase \in {"new", "initialized", "owner"}
    /\ phase' = "pre-send-failed"
    /\ UNCHANGED <<writes, appTurns, turnID, promptSource, inheritsSettings>>

StartTurn(applied) ==
    /\ phase = "owner"
    /\ writes' = writes + 1
    /\ appTurns' = IF applied THEN appTurns + 1 ELSE appTurns
    /\ promptSource' = IF Broken = "external-body" THEN "external" ELSE "local"
    /\ inheritsSettings' = (Broken # "drop-settings")
    /\ phase' = "waiting"
    /\ UNCHANGED turnID

Accept ==
    /\ phase = "waiting"
    /\ appTurns = 1
    /\ phase' = "accepted"
    /\ turnID' = 1
    /\ UNCHANGED <<writes, appTurns, promptSource, inheritsSettings>>

Reject ==
    /\ phase = "waiting"
    /\ appTurns = 0
    /\ phase' = "rejected"
    /\ UNCHANGED <<writes, appTurns, turnID, promptSource, inheritsSettings>>

LoseOutcome ==
    /\ phase = "waiting"
    /\ phase' = "uncertain"
    /\ UNCHANGED <<writes, appTurns, turnID, promptSource, inheritsSettings>>

\* A qualitative deadline action. The concrete transport enforces 15 seconds;
\* this model proves only that a fair deadline eventually ends a stuck call.
Timeout ==
    /\ phase \in {"new", "initialized", "owner", "waiting"}
    /\ phase' = IF phase = "waiting" THEN "uncertain" ELSE "pre-send-failed"
    /\ UNCHANGED <<writes, appTurns, turnID, promptSource, inheritsSettings>>

BlindRetry ==
    /\ Broken = "retry"
    /\ phase = "uncertain"
    /\ writes' = writes + 1
    /\ appTurns' = appTurns + 1
    /\ UNCHANGED <<phase, turnID, promptSource, inheritsSettings>>

Next ==
    \/ Initialize
    \/ DiscoverOwner
    \/ PreSendFail
    \/ \E applied \in BOOLEAN : StartTurn(applied)
    \/ Accept
    \/ Reject
    \/ LoseOutcome
    \/ Timeout
    \/ BlindRetry

Spec == Init /\ [][Next]_vars /\ WF_vars(Timeout)

TypeOK ==
    /\ phase \in Phases
    /\ writes \in Nat
    /\ appTurns \in Nat
    /\ turnID \in {0, 1}
    /\ promptSource \in {"none", "local", "external"}
    /\ inheritsSettings \in BOOLEAN

OneMutationAttempt == writes <= 1
PreSendFailureIsCertain == phase = "pre-send-failed" => writes = 0
UnknownOnlyAfterSend == phase = "uncertain" => writes = 1
AcceptedIsAppTruth == phase = "accepted" => appTurns = 1 /\ turnID = 1
ExternalBodyIsNeverPrompt == promptSource # "external"
TurnKeepsSettings == appTurns > 0 => inheritsSettings
EventuallyTerminal == <> (phase \in Terminal)
=============================================================================
