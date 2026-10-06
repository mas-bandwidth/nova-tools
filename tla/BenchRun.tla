---- MODULE BenchRun ----
\* BenchRun is one `nova-ci bench run` (internal/bench/bench.go Run): the
\* hosts in order, the make, copy, exec and remove steps on the first host
\* that answers, and an interrupt that may land at any step. Every host's
\* answer and every step's outcome is chosen by TLC, so the model covers each
\* order of failure the code can meet.
\*
\* The rules the code keeps, as invariants:
\*   OnlyTheMadeDirIsRemoved  no remove names a directory this run did not make
\*                            (a bogus mktemp answer makes nothing nameable);
\*   AtMostOneHostAnswers     the run never moves past a host that answered;
\*   FallbackOnlyOnNoAnswer   a host is passed over only when it did not answer;
\*   ExitIsTheCommands        a run that reached the command ends with its code;
\*   NothingLeftBehind        a finished run that made a directory has asked
\*                            for its removal, whatever happened after the make.
EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS
  NHosts,     \* the host and its fallbacks
  Codes,      \* exit codes the command may return
  RemoveBuggy \* TRUE: the broken twin, which skips the remove after a red copy

Hosts == 1..NHosts
Dirs  == {"run1", "bogus"} \* what mktemp may print: a run directory, or not
None    == 100 \* the verb has no exit yet
Refused == 200 \* the run never reached the command (the code's exit 2 REFUSED)

VARIABLES
  phase,    \* "make" | "copy" | "exec" | "remove" | "done"
  host,     \* the host being tried
  answered, \* hosts that answered
  passed,   \* hosts passed over
  made,     \* the directory this run made and can name ("" for none)
  removed,  \* directories a remove step named
  ranCmd,   \* the command ran
  cmdCode,  \* its exit code (0 until it ran)
  exit,     \* the verb's exit: a code, None or Refused
  outcome   \* "" until done

vars == <<phase, host, answered, passed, made, removed, ranCmd, cmdCode, exit, outcome>>

TypeOK ==
  /\ phase \in {"make", "copy", "exec", "remove", "done"}
  /\ host \in Hosts \cup {NHosts + 1}
  /\ answered \subseteq Hosts /\ passed \subseteq Hosts
  /\ made \in {"", "run1"}
  /\ removed \subseteq Dirs
  /\ ranCmd \in BOOLEAN /\ cmdCode \in Codes \cup {0}
  /\ exit \in Codes \cup {0, None, Refused}

Init ==
  /\ phase = "make" /\ host = 1
  /\ answered = {} /\ passed = {}
  /\ made = "" /\ removed = {}
  /\ ranCmd = FALSE /\ cmdCode = 0
  /\ exit = None /\ outcome = ""

Finish(e) == /\ phase' = "done" /\ exit' = e /\ outcome' = "done"

\* makeRunDir: ssh's own 255 passes the host over; a refusal, or a mktemp
\* answer that is not <root>/run.<x>, ends the run with nothing to remove.
MakeNoAnswer ==
  /\ phase = "make"
  /\ passed' = passed \cup {host}
  /\ host' = host + 1
  /\ IF host + 1 > NHosts
       THEN Finish(Refused)
       ELSE UNCHANGED <<phase, exit, outcome>>
  /\ UNCHANGED <<answered, made, removed, ranCmd, cmdCode>>

MakeRefused ==
  /\ phase = "make"
  /\ answered' = answered \cup {host}
  /\ Finish(Refused)
  /\ UNCHANGED <<host, passed, made, removed, ranCmd, cmdCode>>

MakeAnswers(d) ==
  /\ phase = "make"
  /\ answered' = answered \cup {host}
  /\ IF d = "run1"
       THEN /\ made' = d /\ phase' = "copy" /\ UNCHANGED <<exit, outcome>>
       ELSE /\ made' = "" /\ Finish(Refused)
  /\ UNCHANGED <<host, passed, removed, ranCmd, cmdCode>>

\* runIn: the deferred remove runs after a failed copy, a red command, or an
\* interrupt (context.WithoutCancel), so every way out of copy and exec goes
\* through remove.
CopyOk ==
  /\ phase = "copy" /\ phase' = "exec"
  /\ UNCHANGED <<host, answered, passed, made, removed, ranCmd, cmdCode, exit, outcome>>

CopyFails ==
  /\ phase = "copy"
  /\ IF RemoveBuggy THEN Finish(Refused) ELSE (phase' = "remove" /\ exit' = Refused /\ UNCHANGED outcome)
  /\ UNCHANGED <<host, answered, passed, made, removed, ranCmd, cmdCode>>

Exec(c) ==
  /\ phase = "exec" /\ phase' = "remove"
  /\ ranCmd' = TRUE /\ cmdCode' = c /\ exit' = c
  /\ UNCHANGED <<host, answered, passed, made, removed, outcome>>

Interrupt ==
  /\ phase \in {"copy", "exec"} /\ phase' = "remove"
  /\ exit' = Refused
  /\ UNCHANGED <<host, answered, passed, made, removed, ranCmd, cmdCode, outcome>>

\* remove: rm -rf of the one directory made; its own failure is reported
\* (removed=no) and does not change the exit.
Remove(ok) ==
  /\ phase = "remove"
  /\ removed' = IF ok THEN removed \cup {made} ELSE removed
  /\ phase' = "done" /\ outcome' = IF ok THEN "removed" ELSE "kept"
  /\ UNCHANGED <<host, answered, passed, made, ranCmd, cmdCode, exit>>

Next ==
  \/ MakeNoAnswer \/ MakeRefused \/ \E d \in Dirs : MakeAnswers(d)
  \/ CopyOk \/ CopyFails \/ Interrupt
  \/ \E c \in Codes : Exec(c)
  \/ \E ok \in BOOLEAN : Remove(ok)

Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

OnlyTheMadeDirIsRemoved == removed \subseteq ({made} \ {""})
AtMostOneHostAnswers   == Cardinality(answered) <= 1
FallbackOnlyOnNoAnswer == passed \cap answered = {} /\ \A h \in passed : h < host
ExitIsTheCommands      == (phase = "done" /\ ranCmd /\ exit # Refused) => exit = cmdCode
NothingLeftBehind      == (phase = "done" /\ made # "") => outcome \in {"removed", "kept"}

Terminates == <>(phase = "done")
====
