------------------------------ MODULE FuseBox ------------------------------
\* nova-fuse's box, as state. nova-tools cmd/nova-fuse/main.go and
\* internal/fuse/fuse.go: ReadBox (ErrNoBox and CANNOT TELL), CreateBox (init),
\* cmdCheck (the gate), cmdLockdown, cmdQuarantine, liftQuarantine, and
\* parseBoxWith's one-value rule for --box.
\*
\* The state the code owns, per box file b: st[b], what a read of the path
\* finds ("absent", "unreadable", "ok"); lock[b], the one hard fuse; quar[b],
\* the quarantined surfaces. The verbs are the actions: Init, Lockdown,
\* Quarantine, Lift (lift quarantine; lift lockdown is refused before any read
\* and changes nothing, so it is no action). The outside events: a box is
\* deleted or moved (Delete), a box goes unreadable (Corrupt), and your person
\* replaces a box by hand (Hand), the only lockdown replacement there is.
\* check is not a state change; the gate is the operator Answer, evaluated for
\* every argument list and surface in every reachable state. Args are the
\* --box lists a caller can write: one box, or --box given twice.
\*
\* pst, plock, pquar are the state before the last step and last names it,
\* so a rule can say which step made a surface clear.
\*
\* Broken = "none" is the design. Every other value is a reversed witness:
\*   "absentclear"      a path with no box reads as an empty box (ReadBox
\*                      returning an empty Box for fs.ErrNotExist): a
\*                      deleted blown box, or a caller's second --box
\*                      naming an empty place, answers clear
\*   "lastwins"         --box given twice is taken, the last one answering
\*                      (package flag's default): check answers for a box
\*                      the caller named second, over a blown first
\*   "quarantinemakes"  quarantine on a path with no box makes a box
\*                      holding only that quarantine: every other surface,
\*                      refused a step before, is clear
\*   "initreplaces"     init writes an empty box over whatever is there:
\*                      a blown lockdown is reset by a verb
\*   "lockdownneedsbox" lockdown refuses a path with no box: a fuse that
\*                      cannot be blown
\* Each is caught by one invariant below; the design passes all four.

EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Boxes, Surfaces, Broken

NoSurface == "(none)"   \* a bare check: no surface named
Asked == Surfaces \cup {NoSurface}
Args == {<<b>> : b \in Boxes} \cup {<<b1, b2>> : b1 \in Boxes, b2 \in Boxes}

VARIABLES st, lock, quar, pst, plock, pquar, last
vars == <<st, lock, quar, pst, plock, pquar, last>>

TypeOK ==
  /\ st \in [Boxes -> {"absent", "unreadable", "ok"}]
  /\ lock \in [Boxes -> BOOLEAN]
  /\ quar \in [Boxes -> SUBSET Surfaces]

\* What a read of b finds, as the gate sees it.
Readable(s0, b) == s0[b] = "ok" \/ (Broken = "absentclear" /\ s0[b] = "absent")

\* The gate, over a given state: "clear" only when the one box named was
\* read and holds no fuse for s.
AnswerIn(s0, l0, q0, args, s) ==
  IF Len(args) # 1 /\ Broken # "lastwins" THEN "refused"
  ELSE LET b == args[Len(args)] IN
       IF ~Readable(s0, b) THEN "refused"
       ELSE IF l0[b] THEN "blown"
       ELSE IF s0[b] = "ok" /\ s \in q0[b] THEN "blown"
       ELSE "clear"

Answer(args, s) == AnswerIn(st, lock, quar, args, s)
ClearNow(b, s) == Answer(<<b>>, s) = "clear"
ClearBefore(b, s) == AnswerIn(pst, plock, pquar, <<b>>, s) = "clear"

Init ==
  /\ st = [b \in Boxes |-> "absent"]
  /\ lock = [b \in Boxes |-> FALSE]
  /\ quar = [b \in Boxes |-> {}]
  /\ pst = st /\ plock = lock /\ pquar = quar
  /\ last = <<"start">>

Remember == pst' = st /\ plock' = lock /\ pquar' = quar

\* nova-fuse init: an empty box, only where none is.
InitBox(b) ==
  /\ (st[b] = "absent" \/ Broken = "initreplaces")
  /\ st' = [st EXCEPT ![b] = "ok"]
  /\ lock' = [lock EXCEPT ![b] = FALSE]
  /\ quar' = [quar EXCEPT ![b] = {}]
  /\ Remember /\ last' = <<"init", b>>

\* nova-fuse lockdown: works on any box. An unreadable box's quarantines are
\* not carried forward (its bytes are kept aside); a box that is not there is made.
Lockdown(b) ==
  /\ Remember /\ last' = <<"lockdown", b>>
  /\ IF st[b] = "absent" /\ Broken = "lockdownneedsbox"
       THEN UNCHANGED <<st, lock, quar>>
       ELSE /\ st' = [st EXCEPT ![b] = "ok"]
            /\ lock' = [lock EXCEPT ![b] = TRUE]
            /\ quar' = [quar EXCEPT ![b] = IF st[b] = "ok" THEN @ ELSE {}]

\* nova-fuse quarantine: only into a box it has read.
Quarantine(b, s) ==
  /\ \/ /\ st[b] = "ok"
        /\ quar' = [quar EXCEPT ![b] = @ \cup {s}]
        /\ UNCHANGED <<st, lock>>
     \/ /\ st[b] = "absent" /\ Broken = "quarantinemakes"
        /\ st' = [st EXCEPT ![b] = "ok"]
        /\ quar' = [quar EXCEPT ![b] = {s}]
        /\ UNCHANGED lock
  /\ Remember /\ last' = <<"quarantine", b, s>>

\* nova-fuse lift quarantine: only from a box it has read.
Lift(b, s) ==
  /\ st[b] = "ok" /\ s \in quar[b]
  /\ quar' = [quar EXCEPT ![b] = @ \ {s}]
  /\ UNCHANGED <<st, lock>>
  /\ Remember /\ last' = <<"lift", b, s>>

\* Outside events.
Delete(b) ==
  /\ st[b] # "absent"
  /\ st' = [st EXCEPT ![b] = "absent"]
  /\ lock' = [lock EXCEPT ![b] = FALSE]
  /\ quar' = [quar EXCEPT ![b] = {}]
  /\ Remember /\ last' = <<"delete", b>>

Corrupt(b) ==
  /\ st[b] = "ok"
  /\ st' = [st EXCEPT ![b] = "unreadable"]
  /\ UNCHANGED <<lock, quar>>
  /\ Remember /\ last' = <<"corrupt", b>>

Hand(b, q) ==
  /\ st' = [st EXCEPT ![b] = "ok"]
  /\ lock' = [lock EXCEPT ![b] = FALSE]
  /\ quar' = [quar EXCEPT ![b] = q]
  /\ Remember /\ last' = <<"hand", b>>

Next ==
  \E b \in Boxes :
    \/ InitBox(b) \/ Lockdown(b) \/ Delete(b) \/ Corrupt(b)
    \/ \E s \in Surfaces : Quarantine(b, s) \/ Lift(b, s)
    \/ \E q \in SUBSET Surfaces : Hand(b, q)

Spec == Init /\ [][Next]_vars

\* ---------------------------------------------------------------- the rules

\* The gate answers clear only for a box it read, and for every box the
\* caller named: a second --box never answers for the first.
GateAnswersOnlyFromEveryNamedBox ==
  \A args \in Args, s \in Asked :
    Answer(args, s) = "clear" =>
      \A i \in 1..Len(args) :
        /\ st[args[i]] = "ok"
        /\ ~lock[args[i]]
        /\ s \notin quar[args[i]]

\* No step makes a surface clear that was not, but the three that mean to: a
\* lift of that surface, init of a box that was not there, your person's hand.
OnlyALiftInitOrHandClears ==
  \A b \in Boxes, s \in Asked :
    (ClearNow(b, s) /\ ~ClearBefore(b, s)) =>
      \/ last = <<"lift", b, s>>
      \/ last = <<"init", b>>
      \/ last = <<"hand", b>>

\* init never replaces a box.
InitOnlyWhereNoBox ==
  last[1] = "init" => pst[last[2]] = "absent"

\* A lockdown always blows.
LockdownAlwaysBlows ==
  last[1] = "lockdown" => (st[last[2]] = "ok" /\ lock[last[2]])

\* Every cooperating tool mutation keeps the hard fuse. Outside Hand and
\* Delete remain permitted; this rule does not extend to those events.
ToolWritesPreserveLockdown ==
  last[1] \in {"init", "lockdown", "quarantine", "lift"} =>
    (\A b \in Boxes : plock[b] => lock[b])

=============================================================================
