------------------------------ MODULE WslWatch ------------------------------
(* The Windows-side WSL2 and tailnet self-heal ladder of cmd/nova-fleet/windows-runners.yml                 *)
(* (roles/github-runner/files/wsl-watchdog.ps1, task "WSL2 watchdog (<distro>)"). Once a minute, bounded    *)
(* by a 5-minute Task Scheduler ceiling and per-probe millisecond timeouts, the task reads its inputs:      *)
(*   - wsl: the keepalive task is running, wsl.exe --exec /bin/true exits 0, and WSL sshd answers TCP;      *)
(*   - tail: Windows Tailscale and the coordination server, turned by Classify into "ok", "local" or        *)
(*     "remote" (an HTTP 200-499 from the coordination server is answering; a 5xx or silence is not, so     *)
(*     an ISP, LAN or control-plane outage is "remote" and never reboots the machine);                      *)
(*   - worker: a Runner.Worker process is running;                                                          *)
(*   - hold: an operator hold file exists in hold.d.                                                        *)
(*                                                                                                          *)
(* A tick with ~wsl or tail # "ok" is a miss. After Threshold consecutive misses, waiting Backoff(1) ticks  *)
(* after rung 1:                                                                                            *)
(*   1 restart the broken local side without rebooting Windows: when ~wsl, stop the keepalive, run          *)
(*     wsl --shutdown, restart WslService and start the keepalive again (never touching a healthy WSL2      *)
(*     for a tailnet miss alone); when tail # "ok", restart the Windows Tailscale service;                  *)
(*   2 reboot Windows, after rung 1's backoff when ~wsl or tail = "local":                                  *)
(*     - never for a remote tailnet miss while WSL2 is up (raises the alert only);                          *)
(*     - never once MaxReboots in the sliding window or in this outage is spent (raises the alert, halts);  *)
(*     - deferred while an operator hold file is present, or while WSL2 is up and a Runner.Worker runs      *)
(*       (when WSL2 is down, Runner.Worker may be wedged waiting on dead WSL interop, so it does not        *)
(*       block WSL2 recovery or the reboot).                                                                *)
(*                                                                                                          *)
(* Step is the watch's record update; WslWatchTrace.tla binds the C# implementation's ticks to Step and     *)
(* Classify.                                                                                                *)
EXTENDS Naturals

CONSTANTS Threshold, BackoffStep, BusyBackoff, MaxFaults, MaxReboots, BusyWaitLimit,
          WslDepths, TailDepths, RemoteFaults, BusyJobs, HoldFiles,
          GuardBusy, AlertOnBusy, RestartWsl, RestartTailscale,
          BlockDeadWslWorker, ResetWslOnTail, SkipRung1, RemoteReboot, Latch, OldClassify, AnyReply

ASSUME Threshold \in Nat \ {0} /\ BackoffStep \in Nat \ {0} /\ BusyBackoff \in Nat \ {0}
ASSUME MaxFaults \in Nat /\ MaxReboots \in Nat /\ BusyWaitLimit \in Nat \ {0}
ASSUME WslDepths \subseteq {1, 2, 3} /\ TailDepths \subseteq {1, 2, 3}
ASSUME \A c \in {RemoteFaults, BusyJobs, HoldFiles, GuardBusy, AlertOnBusy, RestartWsl, RestartTailscale,
                 BlockDeadWslWorker, ResetWslOnTail, SkipRung1, RemoteReboot, Latch, OldClassify, AnyReply} : c \in BOOLEAN

RebootRung == 2
Backoff(r) == BackoffStep * r
Min(a, b) == IF a < b THEN a ELSE b
Max(a, b) == IF a > b THEN a ELSE b

VARIABLES
    wdepth,     \* 0 while WSL2 answers its probes; else 1 (rung 1 clears), 2 (reboot clears), 3 (neither clears)
    tkind,      \* the Windows tailnet: "none", "local" or "remote" fault
    tdepth,     \* a local tailnet fault's depth (0 otherwise): 1 (rung 1 clears), 2 (reboot clears), 3 (neither)
    faults,     \* faults injected so far (bounds the model)
    worker,     \* a Runner.Worker process runs
    hold,       \* an operator hold file in hold.d
    obs,        \* what the watch observes of Windows Tailscale and the coordination server
    w           \* [misses, rung, wait, busyWaits, reboots, outage, alerted, halted]

vars == <<wdepth, tkind, tdepth, faults, worker, hold, obs, w>>

WslIn == wdepth = 0
Down == wdepth > 0 \/ tkind # "none"

Obs == [backend: {"running", "stopped", "login", "other", "none"}, self: BOOLEAN, peers: BOOLEAN,
        control: {"answers", "error", "silent"}]
Healthy == [backend |-> "running", self |-> TRUE, peers |-> TRUE, control |-> "answers"]
Answers(o) == o.control = "answers" \/ (AnyReply /\ o.control = "error")

Classify(o) ==
    IF o.backend = "stopped" THEN "ok"
    ELSE IF o.backend = "login" THEN "remote"
    ELSE IF o.backend = "running" /\ o.peers THEN (IF o.self THEN "ok" ELSE "remote")
    ELSE IF o.backend = "running" /\ o.self /\ ~OldClassify THEN "remote"
    ELSE IF Answers(o) THEN "local"
    ELSE "remote"

LocalObs ==
    {[backend |-> "none", self |-> FALSE, peers |-> FALSE, control |-> "answers"]}
    \cup {[backend |-> "other", self |-> s, peers |-> FALSE, control |-> "answers"] : s \in BOOLEAN}
    \cup {[backend |-> "running", self |-> FALSE, peers |-> FALSE, control |-> "answers"]}
RemoteObs ==
    {o \in Obs : /\ o.backend # "stopped"
                 /\ o.backend = "none" => (~o.self /\ ~o.peers)
                 /\ o.backend # "running" => ~o.peers
                 /\ ~(o.backend = "running" /\ o.self /\ o.peers)
                 /\ o \notin LocalObs}
ObsOf(k) == IF k = "none" THEN {Healthy} ELSE IF k = "local" THEN LocalObs ELSE RemoteObs

TailIn == Classify(obs)

Protected(wsl, wrk, hld) ==
    hld \/ ((wsl \/ BlockDeadWslWorker) /\ wrk)

Rung1Wsl(wsl, tail) == ~wsl \/ (ResetWslOnTail /\ tail # "ok")
Rung1Tail(tail) == tail # "ok"

Watch == [misses: 0..Threshold, rung: 0..1, wait: 0..Max(Backoff(1), BusyBackoff),
          busyWaits: 0..BusyWaitLimit, reboots: 0..(MaxReboots + 1), outage: 0..(MaxFaults * (MaxReboots + 2)),
          alerted: BOOLEAN, halted: BOOLEAN]

TypeOK ==
    /\ wdepth \in {0} \cup WslDepths
    /\ tkind \in {"none", "local", "remote"}
    /\ tdepth \in {0} \cup TailDepths
    /\ (tkind = "local") <=> (tdepth > 0)
    /\ faults \in 0..MaxFaults
    /\ worker \in BOOLEAN /\ hold \in BOOLEAN
    /\ obs \in ObsOf(tkind)
    /\ w \in Watch

Step(s, wsl, tail, wrk, hld) ==
    LET prot == Protected(wsl, wrk, hld) IN
    IF wsl /\ tail = "ok"
    THEN [s EXCEPT !.misses = 0, !.rung = 0, !.wait = 0, !.busyWaits = 0, !.outage = 0,
                   !.alerted = FALSE, !.halted = FALSE]
    ELSE IF s.halted
    THEN s
    ELSE IF s.misses < Threshold
    THEN [s EXCEPT !.misses = s.misses + 1]
    ELSE IF s.wait > 0
    THEN [s EXCEPT !.wait = s.wait - 1]
    ELSE IF s.rung = 0 /\ ~SkipRung1
    THEN [s EXCEPT !.rung = 1, !.wait = Backoff(1)]
    ELSE IF wsl /\ tail = "remote" /\ ~RemoteReboot
    THEN [s EXCEPT !.alerted = TRUE]
    ELSE IF s.reboots >= MaxReboots \/ (Latch /\ s.outage >= MaxReboots)
    THEN [s EXCEPT !.alerted = TRUE, !.halted = Latch]
    ELSE IF prot /\ GuardBusy
    THEN [s EXCEPT !.busyWaits = Min(s.busyWaits + 1, BusyWaitLimit), !.wait = BusyBackoff,
                   !.alerted = s.alerted \/ (AlertOnBusy /\ s.busyWaits + 1 >= BusyWaitLimit)]
    ELSE [s EXCEPT !.misses = 0, !.rung = 0, !.wait = 0, !.busyWaits = 0,
                   !.reboots = s.reboots + 1, !.outage = s.outage + 1]

WslClears(r, r1wsl) ==
    (r = 1 /\ r1wsl /\ wdepth = 1 /\ RestartWsl) \/ (r = RebootRung /\ wdepth <= 2)

TailClears(r, r1tail) ==
    tkind = "local" /\ ((r = 1 /\ r1tail /\ tdepth = 1 /\ RestartTailscale) \/ (r = RebootRung /\ tdepth <= 2))

Init ==
    /\ wdepth = 0 /\ tkind = "none" /\ tdepth = 0 /\ faults = 0
    /\ worker \in IF BusyJobs THEN BOOLEAN ELSE {FALSE}
    /\ hold \in IF HoldFiles THEN BOOLEAN ELSE {FALSE}
    /\ obs = Healthy
    /\ w = [misses |-> 0, rung |-> 0, wait |-> 0, busyWaits |-> 0, reboots |-> 0, outage |-> 0,
            alerted |-> FALSE, halted |-> FALSE]

\* ---- the environment (no fairness) ----
WslFault ==
    /\ wdepth = 0 /\ faults < MaxFaults
    /\ wdepth' \in WslDepths /\ faults' = faults + 1
    /\ UNCHANGED <<tkind, tdepth, worker, hold, obs, w>>

TailFault ==
    /\ tkind = "none" /\ faults < MaxFaults /\ faults' = faults + 1
    /\ \/ \E d \in TailDepths : tkind' = "local" /\ tdepth' = d
       \/ RemoteFaults /\ tkind' = "remote" /\ tdepth' = 0
    /\ obs' \in ObsOf(tkind')
    /\ UNCHANGED <<wdepth, worker, hold, w>>

WslSelfHeal ==
    /\ wdepth > 0 /\ wdepth' = 0
    /\ UNCHANGED <<tkind, tdepth, faults, worker, hold, obs, w>>

TailSelfHeal ==
    /\ tkind # "none" /\ tkind' = "none" /\ tdepth' = 0 /\ obs' = Healthy
    /\ UNCHANGED <<wdepth, faults, worker, hold, w>>

Observe ==
    /\ obs' \in ObsOf(tkind) /\ obs' # obs
    /\ UNCHANGED <<wdepth, tkind, tdepth, faults, worker, hold, w>>

Worker ==
    /\ BusyJobs /\ worker' = ~worker
    /\ UNCHANGED <<wdepth, tkind, tdepth, faults, hold, obs, w>>

Hold ==
    /\ HoldFiles /\ hold' = ~hold
    /\ UNCHANGED <<wdepth, tkind, tdepth, faults, worker, obs, w>>

Age ==
    /\ w.reboots > 0 /\ w' = [w EXCEPT !.reboots = w.reboots - 1]
    /\ UNCHANGED <<wdepth, tkind, tdepth, faults, worker, hold, obs>>

\* ---- the watch: one tick of the 1-minute task ----
Tick ==
    /\ UNCHANGED <<faults, hold>>
    /\ LET s == Step(w, WslIn, TailIn, worker, hold)
           r == IF s.reboots > w.reboots THEN RebootRung
                ELSE IF s.rung = w.rung + 1 THEN 1
                ELSE 0
           r1wsl == r = 1 /\ Rung1Wsl(WslIn, TailIn)
           r1tail == r = 1 /\ Rung1Tail(TailIn)
       IN /\ w' = s
          /\ wdepth' = IF r > 0 /\ wdepth > 0 /\ WslClears(r, r1wsl) THEN 0 ELSE wdepth
          /\ worker' = IF r = RebootRung THEN FALSE ELSE worker
          /\ IF r > 0 /\ TailClears(r, r1tail)
             THEN tkind' = "none" /\ tdepth' = 0 /\ obs' = Healthy
             ELSE UNCHANGED <<tkind, tdepth, obs>>

Next == WslFault \/ TailFault \/ WslSelfHeal \/ TailSelfHeal \/ Observe \/ Worker \/ Hold \/ Age \/ Tick

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick)

\* ---- safety ----
RebootsBounded == w.reboots <= MaxReboots

OutageRebootsBounded == w.outage <= MaxReboots

\* never reboots under an operator hold, or while WSL2 is healthy and a Runner.Worker runs
NoRebootWhileProtected == [][w'.reboots > w.reboots => ~(hold \/ (WslIn /\ worker))]_vars

\* rung 1 never shuts down or restarts a healthy WSL2 instance when only the tailnet missed
NoWslResetWhileWslHealthy == [][(w'.rung = 1 /\ w.rung = 0 /\ Rung1Wsl(WslIn, TailIn)) => ~WslIn]_vars

\* a remote tailnet or ISP outage with WSL2 healthy never reboots Windows
NoRebootForARemoteTailnet == [][w'.reboots > w.reboots => ~(wdepth = 0 /\ tkind = "remote")]_vars

NothingWhileHalted == [][w.halted => (w'.reboots <= w.reboots /\ w'.rung \in {0, w.rung})]_vars

LadderInOrder ==
    [][/\ w'.rung \in {0, w.rung, w.rung + 1}
       /\ (w'.reboots > w.reboots => w.rung = 1)]_vars

BackoffHonoured ==
    [][(w'.rung = w.rung + 1 \/ w'.reboots > w.reboots) => (Down /\ w.misses >= Threshold /\ w.wait = 0)]_vars

\* ---- liveness ----
RecoverOrAlert == [](Down ~> (~Down \/ w.alerted))

WslRecovers == [](wdepth > 0 ~> wdepth = 0)

TailRecovers == [](tkind = "local" ~> tkind = "none")
=============================================================================
