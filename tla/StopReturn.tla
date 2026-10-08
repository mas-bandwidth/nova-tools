------------------------------ MODULE StopReturn ------------------------------
\* Bounded STOP contract for one work and one cold read card. The store is the
\* source of placement and generation; the owner runner is the source of a
\* child-cancel acknowledgement. STOP requests cancellation but cannot claim
\* ready until the owner confirms that its process group stopped.
EXTENDS Naturals, FiniteSets

CONSTANTS Work, Read, Owner, Branch, MaxGen, BadPrematureReady, BadLateRead, BadUnfencedStop
Cards == {Work, Read}
ReadyOf(c) == IF c = Work THEN "ready" ELSE "asked"
ActiveOf(c) == IF c = Work THEN "working" ELSE "reading"
DoneOf(c) == IF c = Work THEN "review" ELSE "read-done"

VARIABLES machine, place, row, branch, gen, child, childGen,
          cancel, ack, returned, staged, planned, staleCommitted, accepted
vars == <<machine, place, row, branch, gen, child, childGen,
          cancel, ack, returned, staged, planned, staleCommitted, accepted>>

Init ==
  /\ machine = "running"
  /\ place = [c \in Cards |-> ActiveOf(c)]
  /\ row = [c \in Cards |-> Owner]
  /\ branch = [c \in Cards |-> Branch]
  /\ gen = [c \in Cards |-> 1]
  /\ child = [c \in Cards |-> TRUE]
  /\ childGen = [c \in Cards |-> 1]
  /\ cancel = [c \in Cards |-> FALSE]
  /\ ack = [c \in Cards |-> FALSE]
  /\ returned = [c \in Cards |-> FALSE]
  /\ staged = [c \in Cards |-> TRUE]
  /\ planned = [c \in Cards |-> FALSE]
  /\ staleCommitted = FALSE
  /\ accepted = [c \in Cards |-> 0]

Stop ==
  /\ machine = "running"
  /\ machine' = "stopped"
  /\ cancel' = [c \in Cards |-> child[c]]
  /\ staged' = [c \in Cards |-> FALSE]
  /\ UNCHANGED <<place, row, branch, gen, child, childGen, ack, returned, planned, staleCommitted, accepted>>

StopAgain ==
  /\ machine = "stopped"
  /\ UNCHANGED vars

CancelAck(c) ==
  /\ machine = "stopped" /\ cancel[c] /\ child[c]
  /\ child' = [child EXCEPT ![c] = FALSE]
  /\ ack' = [ack EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<machine, place, row, branch, gen, childGen, cancel, returned, staged, planned, staleCommitted, accepted>>

Return(c) ==
  /\ machine = "stopped" /\ place[c] = ActiveOf(c) /\ gen[c] < MaxGen
  /\ IF BadPrematureReady THEN cancel[c] ELSE ack[c] /\ ~child[c]
  /\ place' = [place EXCEPT ![c] = ReadyOf(c)]
  /\ gen' = [gen EXCEPT ![c] = @ + 1]
  /\ returned' = [returned EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<machine, row, branch, child, childGen, cancel, ack, staged, planned, staleCommitted, accepted>>

ExplicitStart ==
  /\ machine = "stopped"
  /\ \A c \in Cards : cancel[c] => ack[c] /\ returned[c]
  /\ machine' = "running"
  /\ UNCHANGED <<place, row, branch, gen, child, childGen, cancel, ack, returned, staged, planned, staleCommitted, accepted>>

Stage(c) ==
  /\ machine = "running" /\ place[c] = ReadyOf(c) /\ ~staged[c]
  /\ staged' = [staged EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<machine, place, row, branch, gen, child, childGen, cancel, ack, returned, planned, staleCommitted, accepted>>

\* A worker can read RUNNING and prepare its launch before STOP. Acquiring the
\* store fence at commit time either sees the old generation while STOP waits,
\* or loses to STOP's newer generation and re-reads STOP. The broken variant
\* commits this old plan after STOP without that fence.
PlanLaunch(c) ==
  /\ machine = "running" /\ place[c] = ReadyOf(c) /\ staged[c] /\ ~child[c] /\ ~planned[c]
  /\ planned' = [planned EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<machine, place, row, branch, gen, child, childGen, cancel, ack, returned, staged, staleCommitted, accepted>>

Launch(c) ==
  /\ place[c] = ReadyOf(c) /\ planned[c] /\ ~child[c]
  /\ ((machine = "running" /\ staged[c]) \/ BadUnfencedStop)
  /\ place' = [place EXCEPT ![c] = ActiveOf(c)]
  /\ child' = [child EXCEPT ![c] = TRUE]
  /\ childGen' = [childGen EXCEPT ![c] = gen[c]]
  /\ cancel' = [cancel EXCEPT ![c] = FALSE]
  /\ ack' = [ack EXCEPT ![c] = FALSE]
  /\ returned' = [returned EXCEPT ![c] = FALSE]
  /\ planned' = [planned EXCEPT ![c] = FALSE]
  /\ staleCommitted' = (staleCommitted \/ machine = "stopped")
  /\ UNCHANGED <<machine, row, branch, gen, staged, accepted>>

\* A delayed report is delivered using the generation its child held. A read
\* in the broken variant ignores the fence, reproducing the old read API.
Report(c) ==
  /\ (IF c = Read /\ BadLateRead THEN place[c] \in {"asked", "reading"}
      ELSE machine = "running" /\ place[c] = ActiveOf(c) /\ childGen[c] = gen[c])
  /\ place' = [place EXCEPT ![c] = DoneOf(c)]
  /\ accepted' = [accepted EXCEPT ![c] = childGen[c]]
  /\ UNCHANGED <<machine, row, branch, gen, child, childGen, cancel, ack, returned, staged, planned, staleCommitted>>

Next == Stop \/ StopAgain \/ ExplicitStart
        \/ \E c \in Cards : CancelAck(c) \/ Return(c) \/ Stage(c) \/ PlanLaunch(c) \/ Launch(c) \/ Report(c)
Spec == Init /\ [][Next]_vars

\* The implementation requires an owner runner to deliver a real process
\* cancellation acknowledgement. This fair-stop case assumes each requested
\* cancellation and its return receipt eventually runs while STOP remains.
\* It does not assert progress if an owner runner is absent or refuses ack.
StopOnlyNext == Stop \/ StopAgain \/ \E c \in Cards : CancelAck(c) \/ Return(c)
FairStopSpec == Init /\ [][StopOnlyNext]_vars
                /\ WF_vars(Stop)
                /\ \A c \in Cards : WF_vars(CancelAck(c)) /\ WF_vars(Return(c))
EventuallyReturned == \A c \in Cards : (machine = "stopped" /\ cancel[c]) ~> returned[c]

SameOwnerAndBranch == \A c \in Cards : row[c] = Owner /\ branch[c] = Branch
NoReadyBeforeAck == \A c \in Cards : returned[c] => ack[c] /\ ~child[c]
NoStaleAcceptance == \A c \in Cards : accepted[c] = 0 \/ accepted[c] = gen[c]
NoActiveLaunchOnStop == machine = "stopped" => \A c \in Cards : ~staged[c]
NoLaunchCommittedOnStop == ~staleCommitted
NoRestartBeforeReturn == machine = "running" => \A c \in Cards : cancel[c] => returned[c]
TypeOK ==
  /\ machine \in {"running", "stopped"}
  /\ \A c \in Cards : gen[c] \in Nat /\ place[c] \in {ReadyOf(c), ActiveOf(c), DoneOf(c)}
=============================================================================
