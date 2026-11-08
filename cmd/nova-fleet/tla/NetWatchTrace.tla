---------------------------- MODULE NetWatchTrace ----------------------------
(* The code bound to the model: every tick the script took in netwatch_functional_test.go (NetWatchLog.tla,   *)
(* written by that test and held equal to what the script does) is one Step of NetWatch.tla under the test's *)
(* numbers. SkipRestart is the reversed witness: the same ticks with a ladder that goes from no rung straight  *)
(* to the renegotiation, never restarting tailscaled, which the check must refuse.                            *)
EXTENDS NetWatch, NetWatchLog, Sequences

CONSTANT Log

\* busy \in BOOLEAN makes it a state predicate, which TLC checks as an invariant and reports when violated
TraceMatches ==
    /\ busy \in BOOLEAN
    /\ \A k \in 1..Len(Log) : Step(Log[k].pre, Log[k].healthy, Log[k].busy) = Log[k].post

SkipRestart ==
    [k \in 1..Len(TickLog) |->
        IF TickLog[k].pre.rung = 0 /\ TickLog[k].post.rung = 1
        THEN [TickLog[k] EXCEPT !.post.rung = 2, !.post.wait = Backoff(2)]
        ELSE TickLog[k]]

TraceNext == UNCHANGED vars
=============================================================================
