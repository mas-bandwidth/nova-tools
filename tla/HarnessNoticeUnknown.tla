------------------------ MODULE HarnessNoticeUnknown ------------------------
\* Rule 5 of pkg/harness/notice (the Core): a native push whose outcome is
\* unknown may have reached the session. The notice is pending as if
\* accepted, the route keeps running (its lease is renewed and the heartbeat's
\* challenges keep reaching the Core), and only rule 3a pushes it again: once a
\* newer challenge has waited one interval behind it. The 2026-10-14 fault:
\* an unknown outcome stopped the route, the heartbeat missed MissLimit
\* challenges with nobody to deliver them, and unhooked the name (a new epoch).
EXTENDS Naturals
CONSTANTS Broken, MissLimit, MaxNonce
VARIABLES route, pending, shown, held, waited, misses, epoch, hooked, pushes
vars == <<route,pending,shown,held,waited,misses,epoch,hooked,pushes>>

Init ==
 /\ route = "running" /\ pending = FALSE /\ shown = 0 /\ held = 1
 /\ waited = FALSE /\ misses = 0 /\ epoch = 1 /\ hooked = TRUE /\ pushes = 0

\* A push with no pending notice: accepted, or unknown. A certain failure is
\* outside this model (it stops the route by design and says so).
Push(outcome) ==
 /\ route = "running" /\ hooked /\ ~pending /\ held > shown
 /\ pushes' = pushes + 1
 /\ IF outcome = "unknown" /\ Broken = "stop-on-unknown"
       THEN /\ route' = "stopped" /\ pending' = FALSE
            /\ UNCHANGED <<shown,waited>>
       ELSE /\ route' = "running" /\ pending' = TRUE /\ shown' = held
            /\ waited' = FALSE
 /\ UNCHANGED <<held,misses,epoch,hooked>>

\* Rule 3a: a newer challenge held, one interval waited: the same notice again.
Repush(outcome) ==
 /\ route = "running" /\ hooked /\ pending /\ held > shown /\ waited
 /\ pushes' = pushes + 1 /\ shown' = held /\ waited' = FALSE
 /\ UNCHANGED <<route,pending,held,misses,epoch,hooked>>

\* The model acts on the one notice: proves the newest challenge it shows.
Consume ==
 /\ route = "running" /\ pending /\ hooked
 /\ pending' = FALSE /\ misses' = 0
 /\ UNCHANGED <<route,shown,held,waited,epoch,hooked,pushes>>

\* The heartbeat: a new challenge each interval; an unanswered one is a miss.
\* A running route delivers it (held moves); a stopped one delivers nothing.
Beat ==
 /\ hooked /\ held < MaxNonce
 /\ held' = held + 1 /\ waited' = TRUE
 /\ misses' = IF pending \/ route = "stopped" THEN misses + 1 ELSE misses
 /\ IF misses' >= MissLimit /\ route = "stopped"
       THEN /\ hooked' = FALSE /\ epoch' = epoch + 1
       ELSE UNCHANGED <<hooked,epoch>>
 /\ UNCHANGED <<route,pending,shown,pushes>>

Next == \E o \in {"accepted","unknown"} : Push(o) \/ Repush(o)
        \/ Consume \/ Beat
Spec == Init /\ [][Next]_vars

\* An unknown outcome never unhooks or re-hooks the name.
NeverUnhooked == hooked /\ epoch = 1
\* At most one pending notice, and it is pushed at most once per newer nonce.
PushesBounded == pushes <= held
RouteRuns == route = "running"
=============================================================================
