----------------------------- MODULE ProviderRetry -----------------------------
\* A provider's transient refusal is sent again, never handed to the harness first,
\* and a request is sent at most MaxAttempts times (pkg/swarm/providerretry.go;
\* pkg/swarm/providerbudget.go serveRetried and forward; the owner 2026-10-19, "fix
\* ultramarine so it stops failing").
\*
\* The state the proxy owns for one harness request: sends, how many times it was sent
\* upstream; phase, sending, waiting out a backoff, or done; handed, what the harness
\* was given (nothing yet, the provider's success or other answer, the transient
\* refusal, or the run's end). The outside: the provider, answering each send ok, with
\* another answer the harness is to see (a 400, an auth refusal, out of funds), or a
\* transient refusal (a 429, a 503, a 529, overloaded); and the run, which may end at
\* any time, ending the request.
\*
\* A limited route's limit refusals are not modelled here: they stall without a bound by
\* design (the owner, 2026-10-11) and are nova-sprint's tla/RateBudget.tla. Their sends
\* do not count against the bound, so the bound here is the one this code adds.
\*
\* Broken = "none" is the design. Every other value is a reversed witness:
\*   "handfirst" the refusal is handed to the harness on the first send (the code
\*               before v1.6.7, which ended the Ultramarine reads): NoRefusalBeforeTheCap
\*   "unbounded" the last send is never final, so a provider that never recovers is
\*               sent the request forever: SendsBounded

EXTENDS Naturals

CONSTANTS MaxAttempts, MaxSends, Broken

VARIABLES sends, phase, handed

vars == <<sends, phase, handed>>

Answers == {"ok", "other", "transient"}

TypeOK ==
    /\ sends \in 0..MaxSends
    /\ phase \in {"send", "wait", "done"}
    /\ handed \in {"none", "ok", "other", "transient", "ended"}

Init ==
    /\ sends = 0
    /\ phase = "send"
    /\ handed = "none"

\* The last send is final: its transient refusal is handed on whole.
Final(n) == IF Broken = "unbounded" THEN FALSE ELSE n >= MaxAttempts

Send(a) ==
    /\ phase = "send"
    /\ sends < MaxSends
    /\ sends' = sends + 1
    /\ IF a = "transient" /\ Broken # "handfirst" /\ ~Final(sends + 1)
          THEN /\ phase' = "wait"
               /\ UNCHANGED handed
          ELSE /\ phase' = "done"
               /\ handed' = a

\* The backoff ends and the same request is sent again.
Wake ==
    /\ phase = "wait"
    /\ phase' = "send"
    /\ UNCHANGED <<sends, handed>>

\* The run ends while the request is sent or waits.
End ==
    /\ phase # "done"
    /\ phase' = "done"
    /\ handed' = "ended"
    /\ UNCHANGED sends

Next == (\E a \in Answers : Send(a)) \/ Wake \/ End

Spec == Init /\ [][Next]_vars /\ WF_vars(\E a \in Answers : Send(a)) /\ WF_vars(Wake)

\* The bound: a request is sent at most MaxAttempts times.
SendsBounded == sends <= MaxAttempts

\* A transient refusal reaches the harness only from the last send.
NoRefusalBeforeTheCap == handed = "transient" => sends = MaxAttempts

\* Each request ends: it is answered, handed its last refusal, or ended with its run.
EveryRequestEnds == <>(phase = "done")

\* Once handed, the harness's answer never changes.
HandedOnce == [][handed # "none" => handed' = handed]_vars
=============================================================================
