------------------------------- MODULE SSHOff -------------------------------
(***************************************************************************)
(* The tailscale role's switch of Tailscale SSH off (roles/tailscale/tasks/ *)
(* ssh-off.yml), and a later run of the role (main.yml).                   *)
(*                                                                         *)
(* A detached root job turns Tailscale SSH off, holding the pending file   *)
(* while it runs. At its deadline, an instant the model leaves free (so a  *)
(* key login that lands after it is a behaviour), it decides: with an      *)
(* exclusive create of the decision file (Atomic) it reverts only if it    *)
(* created the file; with a check then a touch (the code this replaced) it *)
(* reverts if the file was not there when it looked. Each revert is a step *)
(* after the decision.                                                     *)
(*                                                                         *)
(* The play waits for the switch, reads RunSSH off (or, the job having     *)
(* already reverted, fails), logs in by key or fails to, decides the same  *)
(* way, re-reads RunSSH when Recheck, and reports kept or fails.           *)
(*                                                                         *)
(* A later run starts once the play has ended; with WaitPending it waits   *)
(* for the pending file to go before it reads RunSSH. Reading it off, it   *)
(* schedules no switch and reports green; reading it on, it switches       *)
(* anew (out of this model: the same machine again).                       *)
(***************************************************************************)
EXTENDS Naturals

CONSTANTS Atomic, WaitPending, Recheck

VARIABLES runssh,   \* Tailscale SSH on the node
          pending,  \* the job's pending file
          file,     \* the decision file: "none", "play" or "job" (who created it)
          job,      \* "start", "armed", "reverting", "done"
          play,     \* "wait", "checked", "in", "claimed", "kept", "failed"
          rerun     \* "none", "green", "reswitch"

vars == <<runssh, pending, file, job, play, rerun>>

Init == /\ runssh = TRUE
        /\ pending = FALSE
        /\ file = "none"
        /\ job = "start"
        /\ play = "wait"
        /\ rerun = "none"

\* ---- the job ----

JobSwitch == /\ job = "start"
             /\ job' = "armed" /\ runssh' = FALSE /\ pending' = TRUE
             /\ UNCHANGED <<file, play, rerun>>

\* the deadline: create the decision file exclusively, or (not Atomic) look for it
JobDecide == /\ job = "armed"
             /\ IF Atomic
                  THEN (IF file = "none"
                         THEN file' = "job" /\ job' = "reverting"
                         ELSE file' = file /\ job' = "done")
                  ELSE /\ file' = file
                       /\ job' = IF file = "none" THEN "reverting" ELSE "done"
             /\ pending' = (job' = "reverting")
             /\ UNCHANGED <<runssh, play, rerun>>

JobRevert == /\ job = "reverting"
             /\ runssh' = TRUE /\ job' = "done" /\ pending' = FALSE
             /\ UNCHANGED <<file, play, rerun>>

\* ---- the play ----

PlayReadsOff == /\ play = "wait" /\ job # "start" /\ ~runssh
                /\ play' = "checked"
                /\ UNCHANGED <<runssh, pending, file, job, rerun>>

\* RunSSH reads on again: the job reverted before the play's reconnect read it
PlayReadsOn == /\ play = "wait" /\ job = "done" /\ runssh
               /\ play' = "failed"
               /\ UNCHANGED <<runssh, pending, file, job, rerun>>

PlayLogin == /\ play = "checked"
             /\ play' \in {"in", "failed"}
             /\ UNCHANGED <<runssh, pending, file, job, rerun>>

PlayDecide == /\ play = "in"
              /\ IF Atomic
                   THEN (IF file = "none"
                          THEN file' = "play" /\ play' = "claimed"
                          ELSE file' = file /\ play' = "failed")
                   ELSE file' = "play" /\ play' = "claimed"
              /\ UNCHANGED <<runssh, pending, job, rerun>>

PlayReport == /\ play = "claimed"
              /\ play' = IF Recheck /\ runssh THEN "failed" ELSE "kept"
              /\ UNCHANGED <<runssh, pending, file, job, rerun>>

\* ---- a later run of the role ----

Rerun == /\ rerun = "none" /\ play \in {"kept", "failed"}
         /\ WaitPending => ~pending
         /\ rerun' = IF runssh THEN "reswitch" ELSE "green"
         /\ UNCHANGED <<runssh, pending, file, job, play>>

Next == JobSwitch \/ JobDecide \/ JobRevert
        \/ PlayReadsOff \/ PlayReadsOn \/ PlayLogin \/ PlayDecide \/ PlayReport
        \/ Rerun

Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

TypeOK == /\ runssh \in BOOLEAN /\ pending \in BOOLEAN
          /\ file \in {"none", "play", "job"}
          /\ job \in {"start", "armed", "reverting", "done"}
          /\ play \in {"wait", "checked", "in", "claimed", "kept", "failed"}
          /\ rerun \in {"none", "green", "reswitch"}

\* a play that reports kept leaves Tailscale SSH off, in that state and every later one
KeptMeansOff == play = "kept" => ~runssh

\* a later run that reads Tailscale SSH off (and so switches nothing) is not undone by an earlier job
RerunGreenMeansOff == rerun = "green" => ~runssh

\* the pending file is held exactly while the job may still change RunSSH
PendingWhileDeciding == pending <=> job \in {"armed", "reverting"}

\* every run ends: the job done, the play kept or failed, the later run decided
Terminates == <>(job = "done" /\ play \in {"kept", "failed"} /\ rerun # "none")
=============================================================================
