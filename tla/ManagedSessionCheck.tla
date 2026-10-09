------------------------- MODULE ManagedSessionCheck -------------------------
\* SessionCheck asks a nonce without a provider turn in one-shot mode;
\* messageTurn/controlContext delivers it through the owned wall lane.
\* Production waits for the down-beat row when no explicit mode overrides it.
\* Two checks pin the initial proof and a subsequent unanswered presence check.
\* Native tool authenticity, process-group cleanup and admission atomicity are
\* outside this routing model; their existing implementation probes remain separate.
EXTENDS Naturals
CONSTANTS RowMode, Override, Broken
VARIABLES mode, pending, delivered, completed, route, unsafe
vars == <<mode, pending, delivered, completed, route, unsafe>>
Init == /\ mode = IF Override = "" THEN "unknown" ELSE Override
        /\ pending = 0 /\ delivered = FALSE /\ completed = 0
        /\ route = "none" /\ unsafe = FALSE
DownBeat == /\ mode = "unknown"
            /\ mode' = IF RowMode \in {"batch", "one-shot"} THEN RowMode ELSE "unknown"
            /\ UNCHANGED <<pending, delivered, completed, route, unsafe>>
Ask == /\ mode # "unknown" /\ pending = 0 /\ completed < 2
       /\ pending' = completed + 1 /\ delivered' = FALSE /\ route' = "none"
       /\ UNCHANGED <<mode, completed, unsafe>>
Deliver == /\ pending # 0 /\ ~delivered
           /\ route' = IF mode = "one-shot" /\ ~Broken THEN "owned-wall" ELSE "standalone"
           /\ unsafe' = (unsafe \/ (mode = "one-shot" /\ Broken))
           /\ delivered' = TRUE
           /\ UNCHANGED <<mode, pending, completed>>
Answer == /\ delivered /\ pending # 0
          /\ completed' = pending /\ pending' = 0 /\ delivered' = FALSE
          /\ UNCHANGED <<mode, route, unsafe>>
Next == DownBeat \/ Ask \/ Deliver \/ Answer
Spec == Init /\ [][Next]_vars /\ WF_vars(DownBeat) /\ WF_vars(Ask)
        /\ WF_vars(Deliver) /\ WF_vars(Answer)
TypeOK == /\ mode \in {"unknown", "batch", "one-shot"}
          /\ pending \in 0..2 /\ completed \in 0..2
          /\ delivered \in BOOLEAN /\ unsafe \in BOOLEAN
          /\ route \in {"none", "owned-wall", "standalone"}
OneShotOwned == ~unsafe
UnknownHeld == mode = "unknown" => pending = 0 /\ ~delivered
BatchUsesAdapter == mode = "batch" /\ delivered => route = "standalone"
BothChecksComplete == <> (completed = 2)
ReachLaterManagedCheckWitness == ~(mode = "one-shot" /\ completed = 2 /\ route = "owned-wall")
=============================================================================
