------------------------------ MODULE NetWatch ------------------------------
(* The network self-heal ladder of cmd/nova-fleet/netwatch.yml (roles/netwatch/files/nova-netwatch), one     *)
(* interface's instance. Once per tick the watch reads its inputs: the LAN (the gateway answers, or packets   *)
(* still arrive) and, on the tailnet's interface while the LAN works, the tailnet, which the script cannot see *)
(* directly: it sees an observation (obs: tailscale's state, its own node online, a peer answering, the       *)
(* coordination server answering) and Classify turns that into "ok", "local" or "remote", as the script does. *)
(* A tick with either down is a miss. After                                                                   *)
(* Threshold consecutive misses, waiting Backoff(r) ticks after rung r:                                       *)
(*   1 restart tailscaled, for any miss                                                                      *)
(*   2 renegotiate   3 link bounce   4 driver reload: only while the LAN misses                               *)
(*   5 reboot, after rung 4 for a LAN miss, or after rung 1 for a local tailnet miss; never while a protected *)
(*     job runs, never for a remote tailnet miss (that is only the alert: a coordination outage must not      *)
(*     reboot every machine at once).                                                                         *)
(* The reboot budget: at most MaxReboots reboots counted in the window (reboots; a reboot leaves the window   *)
(* after its age, the Age step), and at most MaxReboots in one outage (outage, kept across reboots, reset by  *)
(* a healthy tick). When the next reboot would pass either, the watch raises the alert and halts: no rung and *)
(* no reboot until a healthy tick (a person can also resume it by removing the alert, outside this model).    *)
(* The alert is also raised, without halting, when the reboot is deferred BusyWaitLimit times or the tailnet  *)
(* miss is remote; a healthy tick clears it.                                                                  *)
(*                                                                                                            *)
(* Step is the watch's record update as a function of the record and its inputs; the script's branches are   *)
(* its branches, and NetWatchTrace.tla checks the ticks the script took (NetWatchLog.tla) against it.         *)
(*                                                                                                            *)
(* The environment: the LAN breaks with a depth in Depths (2..6), the lowest rung that clears it (6: nothing  *)
(* on the machine does), and rung r clears it iff r >= depth. The tailnet breaks locally with a depth in      *)
(* TailDepths: 1 is a wedged tailscaled (its restart or the reboot clears it), 5 needs the reboot, 6 nothing  *)
(* clears; or, when RemoteFaults, remotely, which nothing on the machine clears. Each fault shows one of the  *)
(* observations it can produce (LocalObs, RemoteObs), and may change to another while it lasts. A local fault  *)
(* is assumed to leave the coordination server reachable and to show either tailscale not answering or not   *)
(* Running, or the own node offline with the peers silent (a machine the tailnet has lost); a local fault that *)
(* looks like a remote one (own node online, peers silent) is not modelled: it gets the alert, not a reboot.  *)
(* At most MaxFaults faults;                                                                                  *)
(* each may clear by itself; protected jobs start and stop (when BusyJobs); reboots age out of the window.    *)
(* None of these is fair. The watch's Tick is weakly fair (a systemd timer).                                  *)
(*                                                                                                            *)
(* Reversed witnesses: GuardBusy FALSE, AlertOnBusy FALSE, RestartTailscaled FALSE, TailLikeLan TRUE (the      *)
(* tailnet miss climbs the card's rungs and reboots like a LAN miss), Latch FALSE (the budget counts only the  *)
(* window and is tried again after it, so a fault that never clears reboots the machine forever), OldClassify  *)
(* TRUE (silent peers with the own node online taken as local: a relay outage reboots every machine).          *)
EXTENDS Naturals

CONSTANTS Threshold, BackoffStep, BusyBackoff, MaxFaults, MaxReboots, BusyWaitLimit,
          Depths, TailDepths, RemoteFaults, BusyJobs,
          GuardBusy, AlertOnBusy, RestartTailscaled, TailLikeLan, Latch, OldClassify

ASSUME Threshold \in Nat \ {0} /\ BackoffStep \in Nat \ {0} /\ BusyBackoff \in Nat \ {0}
ASSUME MaxFaults \in Nat /\ MaxReboots \in Nat /\ BusyWaitLimit \in Nat \ {0}
ASSUME Depths \subseteq 2..6 /\ TailDepths \subseteq {1, 5, 6}
ASSUME \A c \in {RemoteFaults, BusyJobs, GuardBusy, AlertOnBusy, RestartTailscaled, TailLikeLan, Latch, OldClassify} : c \in BOOLEAN

Rungs == 4               \* the rungs before the reboot
RebootRung == Rungs + 1
Backoff(r) == BackoffStep * r
Min(a, b) == IF a < b THEN a ELSE b
Max(a, b) == IF a > b THEN a ELSE b

VARIABLES
    depth,      \* 0 while the LAN works; else the lowest rung that clears its fault
    tkind,      \* the tailnet: "none", "local" or "remote" fault
    tdepth,     \* a local tailnet fault's depth (0 otherwise)
    faults,     \* faults so far (bounds the model)
    busy,       \* a protected job runs on the machine
    obs,        \* what the script can see of the tailnet
    w           \* the watch: [misses, rung, wait, busyWaits, reboots, outage, alerted, halted]

vars == <<depth, tkind, tdepth, faults, busy, obs, w>>

Down == depth > 0 \/ tkind # "none"

\* An observation of the tailnet: backend is "running", "stopped" (tailscale down by hand), "login" (NeedsLogin or NeedsMachineAuth), "other" (another
\* state) or "none" (the status does not answer); self, the own node online; peers, a peer answering tailscale ping
\* (read only while running); control, the coordination server answering over the plain internet.
Obs == [backend: {"running", "stopped", "login", "other", "none"}, self: BOOLEAN, peers: BOOLEAN, control: BOOLEAN]
Healthy == [backend |-> "running", self |-> TRUE, peers |-> TRUE, control |-> TRUE]

\* the script's read_tailnet
Classify(o) ==
    IF o.backend = "stopped" THEN "ok"
    ELSE IF o.backend = "login" THEN "remote"
    ELSE IF o.backend = "running" /\ o.peers THEN (IF o.self THEN "ok" ELSE "remote")
    ELSE IF o.backend = "running" /\ o.self /\ ~OldClassify THEN "remote"
    ELSE IF o.control THEN "local"
    ELSE "remote"

\* what a fault can look like: a local one leaves the coordination server reachable (see above); a remote one is
\* anything else that is not healthy: the coordination server silent, a new login, the own node offline while
\* peers answer, or silent peers while the own node is online (a relay outage, the peers down)
LocalObs ==
    {[backend |-> b, self |-> FALSE, peers |-> FALSE, control |-> TRUE] : b \in {"none", "other"}}
    \cup {[backend |-> "running", self |-> FALSE, peers |-> FALSE, control |-> TRUE]}
RemoteObs ==
    {o \in Obs : /\ o.backend # "stopped"
                 /\ o.backend \in {"none", "other"} => (~o.self /\ ~o.peers)
                 /\ o.backend = "login" => (~o.self /\ ~o.peers)
                 /\ \/ o.backend = "login"
                    \/ ~o.control /\ ~(o.backend = "running" /\ o.self /\ o.peers)
                    \/ o.backend = "running" /\ ~o.self /\ o.peers
                    \/ o.backend = "running" /\ o.self /\ ~o.peers}
ObsOf(k) == IF k = "local" THEN LocalObs ELSE IF k = "remote" THEN RemoteObs ELSE {Healthy}

\* the inputs of a tick, as the script reads them
LanIn == depth = 0
TailIn == IF ~LanIn THEN "skip" ELSE Classify(obs)

W == [misses: 0..Threshold, rung: 0..Rungs, wait: 0..Max(Backoff(Rungs), BusyBackoff),
      busyWaits: 0..BusyWaitLimit, reboots: 0..MaxReboots, outage: 0..MaxReboots,
      alerted: BOOLEAN, halted: BOOLEAN]

TypeOK ==
    /\ depth \in {0} \cup Depths
    /\ tkind \in {"none", "local", "remote"}
    /\ tdepth \in {0} \cup TailDepths
    /\ faults \in 0..MaxFaults
    /\ busy \in BOOLEAN
    /\ obs \in ObsOf(tkind)
    /\ w \in W

\* One tick of the watch: the record after, from the record before, the LAN signal (lan), the tailnet signal
\* (tail: "ok", "skip" when not read, "local" or "remote") and whether a protected job runs (b). Each IF branch
\* is one branch of the script.
Step(s, lan, tail, b) ==
    IF lan /\ tail \in {"ok", "skip"}
    THEN [s EXCEPT !.misses = 0, !.rung = 0, !.wait = 0, !.busyWaits = 0, !.outage = 0,
                   !.alerted = FALSE, !.halted = FALSE]
    ELSE IF s.halted
    THEN s
    ELSE IF s.misses < Threshold
    THEN [s EXCEPT !.misses = s.misses + 1]
    ELSE IF s.wait > 0
    THEN [s EXCEPT !.wait = s.wait - 1]
    ELSE IF s.rung = 0 \/ ((~lan \/ TailLikeLan) /\ s.rung < Rungs)
    THEN [s EXCEPT !.rung = s.rung + 1, !.wait = Backoff(s.rung + 1)]
    ELSE IF lan /\ tail = "remote" /\ ~TailLikeLan
    THEN [s EXCEPT !.alerted = TRUE]
    ELSE IF s.reboots >= MaxReboots \/ (Latch /\ s.outage >= MaxReboots)
    THEN [s EXCEPT !.alerted = TRUE, !.halted = Latch]
    ELSE IF b /\ GuardBusy
    THEN [s EXCEPT !.busyWaits = Min(s.busyWaits + 1, BusyWaitLimit), !.wait = BusyBackoff,
                   !.alerted = s.alerted \/ (AlertOnBusy /\ s.busyWaits + 1 >= BusyWaitLimit)]
    ELSE [s EXCEPT !.misses = 0, !.rung = 0, !.wait = 0, !.busyWaits = 0,   \* the reboot: volatile state is lost,
                   !.reboots = s.reboots + 1, !.outage = s.outage + 1]     \* the counts and the alert persist

\* What the rung r taken (RebootRung for the reboot) does to each fault.
LanClears(r) == r >= depth
TailClears(r) ==
    tkind = "local" /\ ((tdepth = 1 /\ r = 1 /\ RestartTailscaled) \/ (r = RebootRung /\ tdepth <= 5))

Init ==
    /\ depth = 0 /\ tkind = "none" /\ tdepth = 0 /\ faults = 0
    /\ busy \in IF BusyJobs THEN BOOLEAN ELSE {FALSE}
    /\ obs = Healthy
    /\ w = [misses |-> 0, rung |-> 0, wait |-> 0, busyWaits |-> 0, reboots |-> 0, outage |-> 0,
            alerted |-> FALSE, halted |-> FALSE]

\* ---- the environment (no fairness) ----
Fault ==
    /\ depth = 0 /\ faults < MaxFaults
    /\ depth' \in Depths /\ faults' = faults + 1
    /\ UNCHANGED <<tkind, tdepth, busy, obs, w>>

TailFault ==
    /\ tkind = "none" /\ faults < MaxFaults /\ faults' = faults + 1
    /\ \/ \E d \in TailDepths : tkind' = "local" /\ tdepth' = d
       \/ RemoteFaults /\ tkind' = "remote" /\ tdepth' = 0
    /\ obs' \in ObsOf(tkind')
    /\ UNCHANGED <<depth, busy, w>>

SelfHeal ==
    /\ depth > 0 /\ depth' = 0
    /\ UNCHANGED <<tkind, tdepth, faults, busy, obs, w>>

TailSelfHeal ==
    /\ tkind # "none" /\ tkind' = "none" /\ tdepth' = 0 /\ obs' = Healthy
    /\ UNCHANGED <<depth, faults, busy, w>>

\* a tailnet fault shows another of its observations
Observe ==
    /\ obs' \in ObsOf(tkind) /\ obs' # obs
    /\ UNCHANGED <<depth, tkind, tdepth, faults, busy, w>>

Busy ==
    /\ BusyJobs /\ busy' = ~busy
    /\ UNCHANGED <<depth, tkind, tdepth, faults, obs, w>>

\* a reboot leaves the window (the script counts the reboot times younger than the window)
Age ==
    /\ w.reboots > 0 /\ w' = [w EXCEPT !.reboots = w.reboots - 1]
    /\ UNCHANGED <<depth, tkind, tdepth, faults, busy, obs>>

\* ---- the watch: one tick of the timer ----
Tick ==
    /\ UNCHANGED <<faults, busy>>
    /\ LET s == Step(w, LanIn, TailIn, busy)
           r == IF s.reboots > w.reboots THEN RebootRung
                ELSE IF s.rung = w.rung + 1 THEN s.rung
                ELSE 0
       IN /\ w' = s
          /\ depth' = IF r > 0 /\ depth > 0 /\ LanClears(r) THEN 0 ELSE depth
          /\ IF r > 0 /\ TailClears(r)
             THEN tkind' = "none" /\ tdepth' = 0 /\ obs' = Healthy
             ELSE UNCHANGED <<tkind, tdepth, obs>>

Next == Fault \/ TailFault \/ SelfHeal \/ TailSelfHeal \/ Observe \/ Busy \/ Age \/ Tick

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick)

\* ---- safety ----
RebootsBounded == w.reboots <= MaxReboots

\* a fault that never clears costs at most MaxReboots reboots: no slow reboot loop as reboots age out
OutageRebootsBounded == w.outage <= MaxReboots

\* never reboots while a protected job runs
NoRebootWhileBusy == [][w'.reboots > w.reboots => ~busy]_vars

\* the card's rungs (2..4) only while the LAN misses
NoCardRungForTheTailnet == [][(w'.rung = w.rung + 1 /\ w'.rung >= 2) => depth > 0]_vars

\* a remote tailnet fault with a working LAN never reboots the machine
NoRebootForARemoteTailnet == [][w'.reboots > w.reboots => ~(depth = 0 /\ tkind = "remote")]_vars

\* halted, the watch takes no rung and no reboot
NothingWhileHalted == [][w.halted => (w'.reboots <= w.reboots /\ w'.rung \in {0, w.rung})]_vars

\* the ladder climbs one rung at a time, or starts over (a healthy tick or a reboot)
LadderInOrder == [][w'.rung \in {0, w.rung, w.rung + 1}]_vars

\* a rung or a reboot only after Threshold misses and the backoff has run out, and never while both signals are up
BackoffHonoured ==
    [][(w'.rung = w.rung + 1 \/ w'.reboots > w.reboots) => (Down /\ w.misses >= Threshold /\ w.wait = 0)]_vars

\* ---- liveness ----
\* every outage, of the LAN or the tailnet, ends in recovery or an alert
RecoverOrAlert == [](Down ~> (~Down \/ w.alerted))

\* a local tailnet fault the restart or the reboot clears is recovered by the watch itself (NetWatchTail.cfg)
TailRecovers == [](tkind = "local" ~> tkind = "none")
=============================================================================
