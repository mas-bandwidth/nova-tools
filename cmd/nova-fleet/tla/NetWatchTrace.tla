---------------------------- MODULE NetWatchTrace ----------------------------
(* The code bound to the model: every tick the script took in netwatch_functional_test.go (NetWatchLog.tla,   *)
(* written by that test and held equal to what the script does) is one Step of NetWatch.tla under the test's *)
(* numbers, and its tailnet signal is Classify of what the script saw. Reversed witnesses: SkipRestart, the    *)
(* same ticks with a ladder that goes from no rung straight to the renegotiation; and the same ticks checked  *)
(* with OldClassify TRUE (NetWatchTraceBrokenOldClassify.cfg), where silent peers with the own node online   *)
(* would be local, or with AnyReply TRUE (NetWatchTraceBrokenAnyReply.cfg), where a coordination server       *)
(* giving a 5xx would be answering: all must be refused.                                                      *)
EXTENDS NetWatch, NetWatchLog, Sequences

CONSTANT Log

\* busy \in BOOLEAN makes it a state predicate, which TLC checks as an invariant and reports when violated
TraceMatches ==
    /\ busy \in BOOLEAN
    /\ \A k \in 1..Len(Log) :
          /\ Step(Log[k].pre, Log[k].lan, Log[k].tail, Log[k].busy) = Log[k].post
          /\ Log[k].tail = "skip" \/ Classify(Log[k].obs) = Log[k].tail

SkipRestart ==
    [k \in 1..Len(TickLog) |->
        IF TickLog[k].pre.rung = 0 /\ TickLog[k].post.rung = 1
        THEN [TickLog[k] EXCEPT !.post.rung = 2, !.post.wait = Backoff(2)]
        ELSE TickLog[k]]

TraceNext == UNCHANGED vars
=============================================================================
