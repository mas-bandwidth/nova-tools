----------------------------- MODULE MachineStop -----------------------------
\* The timed hand-stop transition of docs/SPEC-SPRINT.md section 14. Each
\* action is one serialized server transition; provider-read failures are
\* covered by the twin-store test, not modeled here.
EXTENDS Naturals, FiniteSets

CONSTANT MaxTime
ASSUME MaxTime \in Nat

VARIABLES running, cause, reason, until, now, funded,
          latestStop, scheduledStop, startedFrom
vars == <<running, cause, reason, until, now, funded,
          latestStop, scheduledStop, startedFrom>>

Reasons == {"bench", "longer bench"}
Causes == {"", "funds"}

TypeOK ==
  /\ running \in BOOLEAN
  /\ cause \in Causes
  /\ reason \in Reasons \cup {""}
  /\ until \in 0..MaxTime
  /\ now \in 0..MaxTime
  /\ funded \in BOOLEAN
  /\ latestStop \in 0..2
  /\ scheduledStop \in 0..2
  /\ startedFrom \in 0..2

Init ==
  /\ running = TRUE
  /\ cause = ""
  /\ reason = ""
  /\ until = 0
  /\ now = 0
  /\ funded = TRUE
  /\ latestStop = 0
  /\ scheduledStop = 0
  /\ startedFrom = 0

HandStop(r, at) ==
  /\ r \in Reasons
  /\ at \in (now + 1)..MaxTime
  /\ latestStop < 2
  /\ running' = FALSE
  /\ cause' = ""
  /\ reason' = r
  /\ until' = at
  /\ latestStop' = latestStop + 1
  /\ scheduledStop' = latestStop + 1
  /\ UNCHANGED <<now, funded, startedFrom>>

Clear ==
  /\ running' = FALSE
  /\ cause' = ""
  /\ reason' = ""
  /\ until' = 0
  /\ scheduledStop' = 0
  /\ UNCHANGED <<now, funded, latestStop, startedFrom>>

Advance ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ UNCHANGED <<running, cause, reason, until, funded,
                  latestStop, scheduledStop, startedFrom>>

SetFunding ==
  /\ funded' = ~funded
  /\ UNCHANGED <<running, cause, reason, until, now,
                  latestStop, scheduledStop, startedFrom>>

Tick ==
  /\ ~running
  /\ cause = ""
  /\ reason # ""
  /\ until > 0
  /\ now >= until
  /\ IF funded
        THEN /\ running' = TRUE
             /\ cause' = ""
             /\ startedFrom' = scheduledStop
        ELSE /\ running' = FALSE
             /\ cause' = "funds"
             /\ startedFrom' = startedFrom
  /\ reason' = ""
  /\ until' = 0
  /\ scheduledStop' = 0
  /\ UNCHANGED <<now, funded, latestStop>>

Next ==
  \/ \E r \in Reasons, at \in 1..MaxTime : HandStop(r, at)
  \/ Clear
  \/ Advance
  \/ SetFunding
  \/ Tick

Spec == Init /\ [][Next]_vars

HandStopIsLatest == reason # "" => scheduledStop = latestStop
CauseHasNoDeadline == cause = "funds" => reason = "" /\ until = 0
NoStaleExpiry == running => startedFrom = latestStop
NoHandStopRunsEarly == running => reason = "" /\ until = 0
=============================================================================
