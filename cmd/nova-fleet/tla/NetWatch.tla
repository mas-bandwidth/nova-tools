------------------------------ MODULE NetWatch ------------------------------
(* The network self-heal ladder of cmd/nova-fleet/netwatch.yml (roles/netwatch/files/nova-netwatch), one     *)
(* interface's instance. Once per tick the watch reads two signals: the LAN (the gateway answers, or packets  *)
(* still arrive) and, on the tailnet's interface, the tailnet (tailscale Running, its own node online, a peer *)
(* answers a ping). A tick with either down is a miss. After Threshold consecutive misses it climbs one rung *)
(* at a time, waiting Backoff(r) ticks after rung r:                                                          *)
(*   1 restart tailscaled   2 renegotiate   3 link bounce   4 driver reload                                   *)
(*   5 reboot: never while a protected job (a process pattern, or the hold.d manual hold) runs, at most       *)
(*     MaxReboots times per window (the counter persists across the reboot).                                  *)
(* A healthy tick resets the ladder and clears the alert. The alert is raised when the reboot budget is spent, *)
(* or when the reboot has been deferred BusyWaitLimit times for a protected job.                              *)
(*                                                                                                            *)
(* Step is the watch's record update as a function of the record and its inputs; the script's branches are   *)
(* its branches, and NetWatchTrace.tla checks ticks the script took (NetWatchLog.tla) against it.             *)
(*                                                                                                            *)
(* The environment: the LAN breaks with a depth in Depths (2..6), the lowest rung that clears it (6: nothing  *)
(* on the machine does, a dead card or cable), and a rung r clears it iff r >= depth. The tailnet breaks with *)
(* a depth in TailDepths (1..6): depth 1 is tailscaled wedged, cleared only by its restart or the reboot (the *)
(* card's rungs do not restart it); a deeper one sits below tailscaled and clears as the LAN's does. At most   *)
(* MaxFaults faults in all; each may also clear by itself; protected jobs start and stop at any time. None of *)
(* these is fair. The watch's Tick is weakly fair (a systemd timer).                                           *)
(*                                                                                                            *)
(* Reversed witnesses: GuardBusy FALSE reboots under a protected job (NetWatchBrokenNoGuard.cfg), AlertOnBusy  *)
(* FALSE defers forever in silence (NetWatchBrokenNoAlert.cfg), RestartTailscaled FALSE leaves a wedged       *)
(* tailscaled for a person once the reboot budget is spent (NetWatchBrokenNoRestart.cfg).                     *)
EXTENDS Naturals

CONSTANTS Threshold, BackoffStep, BusyBackoff, MaxFaults, MaxReboots, BusyWaitLimit,
          Depths, TailDepths, GuardBusy, AlertOnBusy, RestartTailscaled

ASSUME Threshold \in Nat \ {0} /\ BackoffStep \in Nat \ {0} /\ BusyBackoff \in Nat \ {0}
ASSUME MaxFaults \in Nat /\ MaxReboots \in Nat /\ BusyWaitLimit \in Nat \ {0}
ASSUME Depths \subseteq 2..6 /\ TailDepths \subseteq 1..6
ASSUME GuardBusy \in BOOLEAN /\ AlertOnBusy \in BOOLEAN /\ RestartTailscaled \in BOOLEAN

Rungs == 4               \* the rungs before the reboot
RebootRung == Rungs + 1
Backoff(r) == BackoffStep * r
Min(a, b) == IF a < b THEN a ELSE b
Max(a, b) == IF a > b THEN a ELSE b

VARIABLES
    depth,      \* 0 while the LAN works; else the lowest rung that clears its fault
    tdepth,     \* 0 while the tailnet works; else the depth of its fault
    faults,     \* faults so far (bounds the model)
    busy,       \* a protected job runs on the machine
    w           \* the watch: [misses, rung, wait, busyWaits, reboots, alerted]

vars == <<depth, tdepth, faults, busy, w>>

Down == depth > 0 \/ tdepth > 0

W == [misses: 0..Threshold, rung: 0..Rungs, wait: 0..Max(Backoff(Rungs), BusyBackoff),
      busyWaits: 0..BusyWaitLimit, reboots: 0..MaxReboots, alerted: BOOLEAN]

TypeOK ==
    /\ depth \in {0} \cup Depths
    /\ tdepth \in {0} \cup TailDepths
    /\ faults \in 0..MaxFaults
    /\ busy \in BOOLEAN
    /\ w \in W

\* One tick of the watch: the record after, from the record before, whether both signals were up and whether a
\* protected job runs. Each IF branch is one branch of the script.
Step(s, healthy, b) ==
    IF healthy
    THEN [s EXCEPT !.misses = 0, !.rung = 0, !.wait = 0, !.busyWaits = 0, !.alerted = FALSE]
    ELSE IF s.misses < Threshold
    THEN [s EXCEPT !.misses = s.misses + 1]
    ELSE IF s.wait > 0
    THEN [s EXCEPT !.wait = s.wait - 1]
    ELSE IF s.rung < Rungs
    THEN [s EXCEPT !.rung = s.rung + 1, !.wait = Backoff(s.rung + 1)]
    ELSE IF s.reboots >= MaxReboots
    THEN [s EXCEPT !.alerted = TRUE]
    ELSE IF b /\ GuardBusy
    THEN [s EXCEPT !.busyWaits = Min(s.busyWaits + 1, BusyWaitLimit), !.wait = BusyBackoff,
                   !.alerted = s.alerted \/ (AlertOnBusy /\ s.busyWaits + 1 >= BusyWaitLimit)]
    ELSE [misses |-> 0, rung |-> 0, wait |-> 0, busyWaits |-> 0,       \* the reboot: volatile state is lost,
          reboots |-> s.reboots + 1, alerted |-> s.alerted]           \* the count and the alert persist

\* What the rung r taken (RebootRung for the reboot) does to each fault.
LanClears(r) == r >= depth
TailClears(r) ==
    IF tdepth = 1 THEN (r = 1 /\ RestartTailscaled) \/ r = RebootRung
    ELSE r >= tdepth

Init ==
    /\ depth = 0 /\ tdepth = 0 /\ faults = 0 /\ busy \in BOOLEAN
    /\ w = [misses |-> 0, rung |-> 0, wait |-> 0, busyWaits |-> 0, reboots |-> 0, alerted |-> FALSE]

\* ---- the environment (no fairness) ----
Fault ==
    /\ depth = 0 /\ faults < MaxFaults
    /\ depth' \in Depths /\ faults' = faults + 1
    /\ UNCHANGED <<tdepth, busy, w>>

TailFault ==
    /\ tdepth = 0 /\ faults < MaxFaults
    /\ tdepth' \in TailDepths /\ faults' = faults + 1
    /\ UNCHANGED <<depth, busy, w>>

SelfHeal ==
    /\ depth > 0 /\ depth' = 0
    /\ UNCHANGED <<tdepth, faults, busy, w>>

TailSelfHeal ==
    /\ tdepth > 0 /\ tdepth' = 0
    /\ UNCHANGED <<depth, faults, busy, w>>

Busy ==
    /\ busy' = ~busy
    /\ UNCHANGED <<depth, tdepth, faults, w>>

\* ---- the watch: one tick of the timer ----
Tick ==
    /\ UNCHANGED <<faults, busy>>
    /\ LET s == Step(w, ~Down, busy)
           r == IF s.reboots > w.reboots THEN RebootRung
                ELSE IF s.rung = w.rung + 1 THEN s.rung
                ELSE 0
       IN /\ w' = s
          /\ depth' = IF r > 0 /\ depth > 0 /\ LanClears(r) THEN 0 ELSE depth
          /\ tdepth' = IF r > 0 /\ tdepth > 0 /\ TailClears(r) THEN 0 ELSE tdepth

Next == Fault \/ TailFault \/ SelfHeal \/ TailSelfHeal \/ Busy \/ Tick

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick)

\* ---- safety ----
RebootsBounded == w.reboots <= MaxReboots

\* never reboots while a protected job runs
NoRebootWhileBusy == [][w'.reboots > w.reboots => ~busy]_vars

\* the ladder climbs one rung at a time, or starts over (a healthy tick or a reboot)
LadderInOrder == [][w'.rung \in {0, w.rung, w.rung + 1}]_vars

\* a rung or a reboot only after Threshold misses and the backoff has run out, and never while both signals are up
BackoffHonoured ==
    [][(w'.rung = w.rung + 1 \/ w'.reboots > w.reboots) => (Down /\ w.misses >= Threshold /\ w.wait = 0)]_vars

\* ---- liveness ----
\* every outage, of the LAN or the tailnet, ends in recovery or an alert
RecoverOrAlert == [](Down ~> (~Down \/ w.alerted))

\* a wedged tailscaled is recovered by the ladder itself (checked with no reboot budget: NetWatchTail.cfg)
TailWedgeRecovers == [](tdepth = 1 ~> tdepth = 0)
=============================================================================
