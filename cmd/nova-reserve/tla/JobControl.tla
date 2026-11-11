----------------------------- MODULE JobControl -----------------------------
\* A run started from an interactive shell (os_unix.go tie and wait,
\* proc_unix.go watchdog): who holds the terminal's foreground, the shell, the
\* job (nova-reserve's process group, which the shell started) or the
\* command's own group, and how Ctrl-Z, fg, bg, the command's end and a
\* SIGKILL of nova-reserve move it.
\*
\* term     the terminal's foreground group: "shell", "reserve" or "cmd"
\* shell    "waiting" on its foreground job, or at its "prompt"
\* res      nova-reserve: "running", "stopped", "exited" or "dead" (SIGKILL)
\* pc       where nova-reserve is in tie and wait
\* child    the command: "none", "forked" (in the job's group, signals blocked,
\*          before setpgid and TIOCSPGRP) or "started" (exec'd in its own group)
\* grp      the command's group still alive: its leader and a job it started
\* stopped  the command's group is stopped
\* pending  a SIGTSTP the forked child has blocked, taken once it is started
\* watch    the watchdog is running (started after the command, tie)
\* sigstop  the job's SIGSTOP to itself is sent and has not landed: the kernel
\*          gives it to one thread, and the others run on until the group stop
\*          reaches them, the one that sent it too
\* cont     a SIGCONT has reached nova-reserve's channel since it drained it
\*
\* Broken = "none" is the code. Reversed witnesses, each must fail:
\*   "norelay"    wait without WUNTRACED: a stopped command keeps the terminal
\*                and the shell waits on a job that never stops (NoHungShell)
\*   "noreclaim"  untie does not hand the terminal back (TerminalBackAtExit)
\*   "nowatchdog" Pdeathsig alone: the leader dies, its job runs on
\*                (WholeGroupDies)
\*   "fgonly"     the relay only for a command started in the foreground: one
\*                started after Ctrl-Z and bg (while waiting for a slot), then
\*                fg, stops on its first read and the shell hangs (NoHungShell)
\*   "nowaitcont" no wait for the SIGCONT after kill(0, SIGSTOP): the thread runs
\*                on before its own stop lands, continues the command in the
\*                background, and the stop lands later, after the shell's fg,
\*                as a stop nobody asked for (OwnStopLandsFirst)
\*
\* Gaps are instants the code does not cover, outside the model unless named:
\*   "ctrlz-at-start"  Ctrl-Z between fork and the child's TIOCSPGRP stops the
\*                     job, and the child takes the terminal from the shell's
\*                     prompt (PromptOwnsTerminal)
\*   "kill-at-start"   a SIGKILL of nova-reserve before the watchdog starts
\*                     leaves the command running (WholeGroupDies), and the
\*                     child takes the terminal from the shell's prompt
\*                     (PromptOwnsTerminal, which TLC reaches first)
\*   "cont-at-stop"    fg or bg between signal.Notify and kill(0, SIGSTOP): the
\*                     SIGCONT is taken for the one after the stop, and the
\*                     thread runs on before the stop lands (OwnStopLandsFirst)
\* Each is a case in CASES.tsv that must fail as stated. Not modelled: Ctrl-C
\* (the command's group ends: CmdExit), and fg of a job that bg left running,
\* which gives the terminal to nova-reserve, not the command, until the
\* command next stops (a second fg then hands it on).
EXTENDS FiniteSets

CONSTANTS Broken, Gaps

VARIABLES term, shell, res, pc, child, grp, stopped, pending, watch, fg, sigstop, cont

vars == <<term, shell, res, pc, child, grp, stopped, pending, watch, fg, sigstop, cont>>

TypeOK ==
  /\ term \in {"shell", "reserve", "cmd"}
  /\ shell \in {"waiting", "prompt"}
  /\ res \in {"running", "stopped", "exited", "dead"}
  /\ pc \in {"start", "starting", "wait", "seen", "stopping", "kill", "cont", "sigcont", "reclaim", "done"}
  /\ child \in {"none", "forked", "started"}
  /\ grp \subseteq {"leader", "job"}
  /\ stopped \in BOOLEAN /\ pending \in BOOLEAN /\ watch \in BOOLEAN /\ fg \in BOOLEAN
  /\ sigstop \in BOOLEAN /\ cont \in BOOLEAN

\* The shell has given the job the terminal (fg, or a command line).
Init ==
  /\ term = "reserve" /\ shell = "waiting" /\ res = "running" /\ pc = "start"
  /\ child = "none" /\ grp = {} /\ stopped = FALSE /\ pending = FALSE /\ watch = FALSE /\ fg = FALSE
  /\ sigstop = FALSE /\ cont = FALSE

Run == res = "running"

\* --- nova-reserve ------------------------------------------------------------

\* tie: Foreground only when the job has the terminal (it may have been put
\* in the background while it waited for a slot); cmd.Start forks.
Fork ==
  /\ Run /\ pc = "start"
  /\ child' = "forked" /\ pc' = "starting" /\ fg' = (term = "reserve")
  /\ UNCHANGED <<term, shell, res, grp, stopped, pending, watch, sigstop, cont>>

\* cmd.Start returns once the child has exec'd; then the watchdog starts.
Started ==
  /\ Run /\ pc = "starting" /\ child = "started"
  /\ pc' = "wait" /\ watch' = (Broken # "nowatchdog")
  /\ UNCHANGED <<term, shell, res, child, grp, stopped, pending, fg, sigstop, cont>>

\* wait: Wait4 with WUNTRACED reports the stop, whether or not the command
\* started in the foreground ("fgonly" relays only then).
SeeStop ==
  /\ Run /\ pc = "wait" /\ grp # {} /\ stopped /\ Broken # "norelay"
  /\ fg \/ Broken # "fgonly"
  /\ pc' = "seen"
  /\ UNCHANGED <<term, shell, res, child, grp, stopped, pending, watch, fg, sigstop, cont>>

\* passTerminal(pid, self): back from the command only if it has it.
TakeBack ==
  /\ Run /\ pc = "seen"
  /\ term' = IF term = "cmd" THEN "reserve" ELSE term
  /\ pc' = "stopping"
  /\ UNCHANGED <<shell, res, child, grp, stopped, pending, watch, fg, sigstop, cont>>

\* A new channel takes SIGCONT (signal.Notify): a SIGCONT before it (an
\* earlier bg or fg) is not the one that ends this stop.
Drain ==
  /\ Run /\ pc = "stopping"
  /\ cont' = FALSE /\ pc' = "kill"
  /\ UNCHANGED <<term, shell, res, child, grp, stopped, pending, watch, fg, sigstop>>

\* kill(0, SIGSTOP): sent; the job stops when it lands (StopLands).
StopSelf ==
  /\ Run /\ pc = "kill"
  /\ sigstop' = TRUE /\ pc' = "cont"
  /\ UNCHANGED <<term, shell, res, child, grp, stopped, pending, watch, fg, cont>>

\* Block on the channel until the shell's SIGCONT ("nowaitcont" does not);
\* then passTerminal(self, pid): on only if the shell gave the job the
\* terminal (fg).
PassOn ==
  /\ Run /\ pc = "cont" /\ (cont \/ Broken = "nowaitcont")
  /\ term' = IF term = "reserve" THEN "cmd" ELSE term
  /\ pc' = "sigcont"
  /\ UNCHANGED <<shell, res, child, grp, stopped, pending, watch, fg, sigstop, cont>>

\* kill(-pid, SIGCONT)
Cont ==
  /\ Run /\ pc = "sigcont"
  /\ stopped' = FALSE /\ pc' = "wait"
  /\ UNCHANGED <<term, shell, res, child, grp, pending, watch, fg, sigstop, cont>>

\* Wait4 reports the command's end.
SeeExit ==
  /\ Run /\ pc = "wait" /\ grp = {}
  /\ pc' = "reclaim"
  /\ UNCHANGED <<term, shell, res, child, grp, stopped, pending, watch, fg, sigstop, cont>>

\* untie: the watchdog ends, and passTerminal(pid, self).
Reclaim ==
  /\ Run /\ pc = "reclaim"
  /\ term' = IF term = "cmd" /\ Broken # "noreclaim" THEN "reserve" ELSE term
  /\ watch' = FALSE /\ pc' = "done"
  /\ UNCHANGED <<shell, res, child, grp, stopped, pending, fg, sigstop, cont>>

Exit ==
  /\ Run /\ pc = "done"
  /\ res' = "exited"
  /\ UNCHANGED <<term, shell, pc, child, grp, stopped, pending, watch, fg, sigstop, cont>>

\* --- the command ------------------------------------------------------------

\* The child, signals blocked: setpgid, TIOCSPGRP, unblock (a pending SIGTSTP
\* stops it), exec; the command starts a job. It runs whatever its parent does.
Exec ==
  /\ child = "forked"
  /\ child' = "started" /\ grp' = {"leader", "job"} /\ term' = IF fg THEN "cmd" ELSE term
  /\ stopped' = pending /\ pending' = FALSE
  /\ UNCHANGED <<shell, res, pc, watch, fg, sigstop, cont>>

\* The command reads the terminal it does not hold: SIGTTIN stops it.
Ttin ==
  /\ child = "started" /\ grp # {} /\ ~stopped /\ term # "cmd"
  /\ stopped' = TRUE
  /\ UNCHANGED <<term, shell, res, pc, child, grp, pending, watch, fg, sigstop, cont>>

\* The command's group ends, however.
CmdExit ==
  /\ child = "started" /\ grp # {} /\ ~stopped
  /\ grp' = {} /\ stopped' = FALSE
  /\ UNCHANGED <<term, shell, res, pc, child, pending, watch, fg, sigstop, cont>>

\* The watchdog sees nova-reserve gone and kills the command's group.
Watchdog ==
  /\ watch /\ res = "dead" /\ grp # {}
  /\ grp' = {} /\ stopped' = FALSE
  /\ UNCHANGED <<term, shell, res, pc, child, pending, watch, fg, sigstop, cont>>

\* --- the user and the shell ---------------------------------------------------

\* Ctrl-Z: SIGTSTP to the terminal's foreground group. The forked child is in
\* the job's group, and holds the signal pending ("ctrlz-at-start").
CtrlZ ==
  \/ /\ term = "cmd" /\ grp # {} /\ ~stopped
     /\ stopped' = TRUE
     /\ UNCHANGED <<term, shell, res, pc, child, grp, pending, watch, fg, sigstop, cont>>
  \/ /\ term = "reserve" /\ Run
     /\ child # "forked" \/ "ctrlz-at-start" \in Gaps
     /\ res' = "stopped" /\ pending' = (pending \/ child = "forked")
     /\ UNCHANGED <<term, shell, pc, child, grp, stopped, watch, fg, sigstop, cont>>

\* SIGKILL of nova-reserve. On Linux Pdeathsig kills the command's leader.
Kill ==
  /\ res \in {"running", "stopped"}
  /\ pc # "starting" \/ "kill-at-start" \in Gaps
  /\ res' = "dead"
  /\ grp' = IF Broken = "nowatchdog" THEN grp \ {"leader"} ELSE grp
  /\ UNCHANGED <<term, shell, pc, child, stopped, pending, watch, fg, sigstop, cont>>

\* The shell's waitpid sees its foreground job stop or end, and takes the terminal.
ShellSees ==
  /\ shell = "waiting" /\ res \in {"stopped", "exited", "dead"}
  /\ shell' = "prompt" /\ term' = "shell"
  /\ UNCHANGED <<res, pc, child, grp, stopped, pending, watch, fg, sigstop, cont>>

\* fg: the terminal to the job, SIGCONT to the job. A SIGCONT discards a
\* SIGSTOP still to land, and reaches nova-reserve's channel.
Fg ==
  /\ shell = "prompt" /\ res \in {"running", "stopped"}
  /\ pc # "kill" \/ "cont-at-stop" \in Gaps
  /\ term' = "reserve" /\ shell' = "waiting" /\ res' = "running"
  /\ sigstop' = FALSE /\ cont' = TRUE
  /\ UNCHANGED <<pc, child, grp, stopped, pending, watch, fg>>

\* bg: SIGCONT to the job; the shell keeps the terminal.
Bg ==
  /\ shell = "prompt" /\ res = "stopped"
  /\ pc # "kill" \/ "cont-at-stop" \in Gaps
  /\ res' = "running"
  /\ sigstop' = FALSE /\ cont' = TRUE
  /\ UNCHANGED <<term, shell, pc, child, grp, stopped, pending, watch, fg>>

\* --- the kernel ---------------------------------------------------------------

\* The group stop reaches every thread of nova-reserve: the job stops.
StopLands ==
  /\ sigstop /\ res \in {"running", "stopped"}
  /\ res' = "stopped" /\ sigstop' = FALSE
  /\ UNCHANGED <<term, shell, pc, child, grp, stopped, pending, watch, fg, cont>>

Reserve == Fork \/ Started \/ SeeStop \/ TakeBack \/ Drain \/ StopSelf \/ PassOn \/ Cont \/ SeeExit \/ Reclaim \/ Exit

Next == Reserve \/ StopLands \/ Exec \/ Ttin \/ CmdExit \/ Watchdog \/ CtrlZ \/ Kill \/ ShellSees \/ Fg \/ Bg

\* nova-reserve, its stop, the child's start, the watchdog and the shell make progress;
\* the user, the command's end and a SIGKILL may never come.
Spec == Init /\ [][Next]_vars /\ WF_vars(Reserve) /\ WF_vars(StopLands) /\ WF_vars(Exec) /\ WF_vars(Watchdog) /\ WF_vars(ShellSees)

\* --- properties -------------------------------------------------------------

\* At its prompt the shell holds the terminal: nothing takes it from under it.
PromptOwnsTerminal == shell = "prompt" => term = "shell"

\* While its own SIGSTOP is still to land, nova-reserve waits: it hands the
\* terminal to no one and continues nothing. Otherwise the stop lands later,
\* after the shell's fg, and the shell sees a stop nobody asked for.
OwnStopLandsFirst == sigstop => pc = "cont"

\* nova-reserve never exits leaving the terminal with the command's group.
TerminalBackAtExit == res = "exited" => term # "cmd"

\* A stopped command never leaves the shell waiting on its job: the shell gets
\* its prompt back, or the command goes on.
NoHungShell == (stopped /\ shell = "waiting") ~> (shell = "prompt" \/ ~stopped)

\* When nova-reserve dies, all of the command's group dies.
WholeGroupDies == (res = "dead") ~> (grp = {})

\* When nova-reserve ends, however, the shell gets the terminal back.
ShellGetsTerminal == (res \in {"exited", "dead"}) ~> (term = "shell")
=============================================================================
