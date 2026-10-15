----------------------- MODULE CodexHookRecovery -----------------------
\* Tiny startup selection model, separate from the one-call wire model.
\* No delivery ledger: bus messages and per-ID answers are the only truth.
EXTENDS Naturals, FiniteSets
CONSTANT Broken
VARIABLES phase, events, answers, window, boundary, cursor, selected, live,
          lease, challenge, attempts, process, explicitRecovery
vars == <<phase,events,answers,window,boundary,cursor,selected,live,lease,
          challenge,attempts,process,explicitRecovery>>

Init ==
 /\ phase = "new"
 /\ events = {1,2}
 /\ answers \in {{},{2}}
 /\ window \in {{1,2,3},{2,3}}
 /\ boundary = {} /\ cursor = {} /\ selected = {} /\ live = {}
 /\ lease = FALSE /\ challenge = TRUE
 /\ attempts = 0 /\ process = 1 /\ explicitRecovery = FALSE

Arrive ==
 /\ phase \in {"new","captured","published"} /\ 3 \notin events
 /\ events' = events \cup {3}
 /\ UNCHANGED <<phase,answers,window,boundary,cursor,selected,live,lease,
                 challenge,attempts,process,explicitRecovery>>

Capture ==
 /\ phase = "new" /\ Broken # "tail-after-renew"
 /\ phase' = "captured" /\ boundary' = events /\ cursor' = events
 /\ UNCHANGED <<events,answers,window,selected,live,lease,challenge,
                 attempts,process,explicitRecovery>>

Publish ==
 /\ phase = "captured"
 /\ phase' = "published" /\ lease' = TRUE
 /\ UNCHANGED <<events,answers,window,boundary,cursor,selected,live,
                 challenge,attempts,process,explicitRecovery>>

\* The reversed old ordering scans a boundary, publishes its lease, and only
\* then captures the live cursor. An arrival between them belongs to neither.
PublishEarly ==
 /\ phase = "new" /\ Broken = "tail-after-renew"
 /\ phase' = "early" /\ lease' = TRUE /\ boundary' = events
 /\ UNCHANGED <<events,answers,window,cursor,selected,live,challenge,
                 attempts,process,explicitRecovery>>
EarlyArrival ==
 /\ phase = "early" /\ events' = events \cup {3}
 /\ phase' = "late-capture"
 /\ UNCHANGED <<answers,window,boundary,cursor,selected,live,lease,
                 challenge,attempts,process,explicitRecovery>>
LateCapture ==
 /\ phase = "late-capture" /\ phase' = "published" /\ cursor' = events
 /\ UNCHANGED <<events,answers,window,boundary,selected,live,lease,
                 challenge,attempts,process,explicitRecovery>>

Select ==
 /\ phase = "published" /\ phase' = "selected"
 /\ selected' = IF Broken = "latest-answer" /\ 2 \in answers
                 THEN {m \in boundary \cap window : m > 2} \cup {4}
                 ELSE ((boundary \cap window) \ answers) \cup {4}
 /\ live' = events \ cursor
 /\ UNCHANGED <<events,answers,window,boundary,cursor,lease,challenge,
                 attempts,process,explicitRecovery>>
Deliver ==
 /\ phase = "selected" /\ phase' = "stopped" /\ attempts' = attempts + 1
 /\ UNCHANGED <<events,answers,window,boundary,cursor,selected,live,lease,
                 challenge,process,explicitRecovery>>
\* A certain native failure ends this process. An unknown outcome leaves the
\* notice pending in the Core (HarnessNoticeUnknown, rule 5): the route runs on
\* and only rule 3a pushes it again. Neither is retried blindly in place.
Restart ==
 /\ phase = "stopped" /\ process = 1
 /\ phase' = "new" /\ lease' = FALSE /\ attempts' = 0
 /\ process' = 2 /\ explicitRecovery' = TRUE
 /\ boundary' = {} /\ cursor' = {} /\ selected' = {} /\ live' = {}
 /\ UNCHANGED <<events,answers,window,challenge>>
BlindRetry ==
 /\ Broken = "blind-retry" /\ phase = "stopped"
 /\ attempts' = attempts + 1
 /\ UNCHANGED <<phase,events,answers,window,boundary,cursor,selected,live,
                 lease,challenge,process,explicitRecovery>>
Next == Capture \/ Publish \/ Arrive \/ PublishEarly \/ EarlyArrival \/
        LateCapture \/ Select \/ Deliver \/ Restart \/ BlindRetry
Spec == Init /\ [][Next]_vars
NoUnansweredGap == phase \in {"selected","stopped"} =>
                   ((events \cap window) \ answers) \subseteq (selected \cup live)
ServerChallengeIncluded == phase \in {"selected","stopped"} => 4 \in selected
OneAttemptPerProcess == attempts <= 1
ExplicitRestart == process = 2 => explicitRecovery
BoundedBatch == Cardinality(selected) <= 4
=============================================================================
