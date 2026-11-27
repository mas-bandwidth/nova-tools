---------------------------- MODULE WslWatchTrace ----------------------------
(* The code bound to the model: every tick NovaWslWatch (roles/github-runner/files/wsl-watchdog.ps1) took in  *)
(* wsl_watchdog_functional_test.go (WslWatchLog.tla) is one Step of WslWatch.tla under the test's numbers,    *)
(* and its tailnet signal is Classify of what it observed. Reversed witnesses: SkipRung1Log (a trace whose    *)
(* rung 0 -> 1 step skips to rung 2), BlockDeadWslWorker TRUE (wedged Runner.Worker deferring dead-WSL        *)
(* recovery), OldClassify TRUE, and AnyReply TRUE; all must fail TraceMatches.                                *)
EXTENDS WslWatch, WslWatchLog, Sequences

CONSTANT Log

TraceMatches ==
    /\ worker \in BOOLEAN
    /\ \A k \in 1..Len(Log) :
          /\ Step(Log[k].pre, Log[k].wsl, Log[k].tail, Log[k].worker, Log[k].hold) = Log[k].post
          /\ Classify(Log[k].obs) = Log[k].tail

SkipRung1Log ==
    [k \in 1..Len(TickLog) |->
        IF TickLog[k].pre.rung = 0 /\ TickLog[k].post.rung = 1
        THEN [TickLog[k] EXCEPT !.post.rung = 0, !.post.reboots = TickLog[k].pre.reboots + 1]
        ELSE TickLog[k]]

TraceNext == UNCHANGED vars
=============================================================================
