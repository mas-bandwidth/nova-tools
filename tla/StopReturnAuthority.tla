------------------------- MODULE StopReturnAuthority -------------------------
\* Companion to StopReturn: exact authority across two owners, epochs and
\* launches per epoch. One representative work/read card is selected in Init;
\* StopReturn separately checks the conjunction of work and read return debts.
EXTENDS Naturals, FiniteSets
CONSTANTS Fault, None
Owners == {"owner-a", "owner-b"}
Epochs == 1..2
Generations == 1..3
Tables == {"fleet", "readers"}
Token(o, t, g, e) == [owner |-> o, table |-> t, card |-> "card",
                       generation |-> g, epoch |-> e]
Tokens == {Token(o,t,g,e) : o \in Owners, t \in Tables,
                             g \in Generations, e \in Epochs}
VARIABLE a
avars == <<a>>
Live == Token(a.owner, a.table, a.gen, a.epoch)
DebtKey(d) == <<d.owner, d.table, d.card, d.epoch>>
Settled == IF a.debt = None THEN TRUE ELSE
  (DebtKey(a.debt) = DebtKey(Live) /\ a.place = "ready" /\
   a.from = a.debt.generation /\ a.gen = a.debt.generation + 1)

AuthorityInitial(o,t,g,e) ==
    a = [machine |-> "running", epoch |-> e, owner |-> o,
         table |-> t, gen |-> g, launches |-> 1, place |-> "active", from |-> 0,
         alive |-> {Token(o,t,g,e)},
         reports |-> {Token(o,t,g,e)}, ack |-> {}, debt |-> None,
         returned |-> None, reported |-> None, started |-> None]
AuthorityInit == \E t \in Tables : AuthorityInitial("owner-a",t,1,1)
AuthorityFairInit == \E o \in Owners, t \in Tables, g \in Generations, e \in Epochs :
  AuthorityInitial(o,t,g,e)

\* machine_transition.go activeStopLeases: epoch is the captured store
\* context, NOT a field of the serialized StopLease (Table/Row/ID/Gen).
AuthorityStop ==
  /\ a.machine = "running"
  /\ a' = [a EXCEPT !.machine = "stopped",
             !.debt = IF a.place = "active" THEN Live ELSE None]

\* Real owner process-group exit is an environment fact. The store trusts the
\* acknowledgement; it does not inspect the OS process. ack retains old exits.
AuthorityCancel ==
  /\ a.machine = "stopped" /\ Live \in a.alive
  /\ a' = [a EXCEPT !.alive = @ \ {Live}, !.ack = @ \cup {Live}]

\* Requests retain the child's generation and pinned operation epoch. --as
\* is caller-supplied, so wrong-owner requests are tried, not assumed away.
\* An old exited child's return remains deliverable after a new launch.
Requests == {Token(o,t.table,t.generation,t.epoch) :
               o \in Owners, t \in a.reports}
Exited(r) == \E t \in a.ack : t.table = r.table /\ t.card = r.card /\
                              t.generation = r.generation /\ t.epoch = r.epoch
AuthorityReturn(r) ==
  /\ a.machine = "stopped" /\ a.place = "active"
  /\ r.table = a.table /\ r.card = "card"
  /\ (r.owner = a.owner \/ Fault = "owner")
  /\ (r.generation = a.gen \/ Fault = "attempt")
  /\ (r.epoch = a.epoch \/ Fault = "epoch")
  /\ (Exited(r) \/ Fault = "premature")
  /\ a' = [a EXCEPT !.place = "ready", !.gen = r.generation + 1,
             !.from = r.generation,
             !.returned = [request |-> r, live |-> Live,
                           alive |-> a.alive, ack |-> a.ack]]

\* Idempotent already-returned receipts are stutters. StopReturnReq's source
\* planner checks old+1/from/ready first; no second generation bump occurs.
AuthorityReplay(r) ==
  /\ a.machine = "stopped" /\ a.place = "ready"
  /\ r.owner = a.owner /\ r.epoch = a.epoch /\ r.table = a.table
  /\ a.from = r.generation /\ a.gen = r.generation + 1
  /\ UNCHANGED a

\* ExplicitStart checks the captured tuple's same-owner ready/from/gen receipt,
\* then clears the durable debt. It cannot infer settlement from missing rows.
AuthorityStart ==
  /\ a.machine = "stopped" /\ (Settled \/ Fault = "start")
  /\ a' = [a EXCEPT !.machine = "running", !.debt = None,
             !.started = [debt |-> a.debt, live |-> Live, place |-> a.place,
                          from |-> a.from, alive |-> a.alive]]

AuthorityLaunch ==
  /\ a.machine = "running" /\ a.place = "ready" /\ a.gen \in Generations /\ a.launches < 2
  /\ a' = [a EXCEPT !.place = "active", !.launches = @ + 1, !.alive = @ \cup {Live},
             !.reports = @ \cup {Live}]

\* A row transfer is possible once START releases the debt; stopDebtMutation
\* blocks it even after a valid return while the captured debt remains.
AuthorityTransfer ==
  /\ a.place = "ready" /\ a.owner = "owner-a" /\ a.gen < 3
  /\ (a.debt = None \/ Fault = "orphan")
  /\ a' = [a EXCEPT !.owner = "owner-b", !.gen = @ + 1]

AuthorityRemove ==
  /\ a.place # "missing" /\ (a.place # "active" \/ Fault = "orphan")
  /\ (a.debt = None \/ Fault = "orphan")
  /\ a' = [a EXCEPT !.place = "missing"]

\* clear.go stops first, refuses any captured debt, and advances only when
\* empty. Recreate one card in the new epoch; creation is compressed here.
AuthorityClear ==
  /\ a.epoch = 1 /\ a.debt = None /\ a.place # "active"
  /\ a' = [a EXCEPT !.machine = "stopped", !.epoch = 2,
             !.owner = "owner-a", !.place = "ready", !.gen = 1, !.from = 0,
             !.launches = 0]

\* Reports are immutable child tokens, never overwritten by Launch. The
\* epoch guard abstracts required Step.Epoch and the pinned store fence;
\* direct library callers with nil Step.Epoch are outside this guarantee.
\* gen and owner are the
\* report planner's lease authority. No process-running assumption is needed.
AuthorityReport(r) ==
  /\ a.machine = "running" /\ a.place = "active"
  /\ r.owner = a.owner /\ r.table = a.table
  /\ (r.generation = a.gen \/ Fault = "report")
  /\ r.epoch = a.epoch
  /\ a' = [a EXCEPT !.place = "done", !.alive = @ \ {r},
             !.reported = [request |-> r, live |-> Live]]

AuthorityNext == AuthorityStop \/ AuthorityCancel \/ AuthorityStart \/
  AuthorityLaunch \/ AuthorityTransfer \/ AuthorityRemove \/ AuthorityClear \/
  (\E r \in Requests : AuthorityReturn(r) \/ AuthorityReplay(r)) \/
  (\E r \in a.reports : AuthorityReport(r))
AuthoritySpec == AuthorityInit /\ [][AuthorityNext]_avars

AuthorityType ==
  /\ a.machine \in {"running", "stopped"} /\ a.epoch \in Epochs
  /\ a.owner \in Owners /\ a.table \in Tables /\ a.gen \in 1..4 /\ a.launches \in 0..2
  /\ a.place \in {"active", "ready", "done", "missing"} /\ a.from \in 0..3
  /\ a.alive \subseteq Tokens /\ a.reports \subseteq Tokens /\ a.ack \subseteq Tokens
  /\ a.debt \in Tokens \cup {None}
ExactReturnOwner == IF a.returned = None THEN TRUE ELSE
  a.returned.request.owner = a.returned.live.owner
ExactReturnAttempt == IF a.returned = None THEN TRUE ELSE
  a.returned.request.generation = a.returned.live.generation
ExactReturnEpoch == IF a.returned = None THEN TRUE ELSE
  a.returned.request.epoch = a.returned.live.epoch
AuthorityNoPrematureReturn == IF a.returned = None THEN TRUE ELSE
  (a.returned.live \notin a.returned.alive /\ a.returned.live \in a.returned.ack)
AuthorityNoPrematureStart == IF a.started = None THEN TRUE ELSE
  IF a.started.debt = None THEN TRUE ELSE
  (a.started.place = "ready" /\
   DebtKey(a.started.debt) = DebtKey(a.started.live) /\
   a.started.from = a.started.debt.generation /\
   a.started.live.generation = a.started.debt.generation + 1 /\
   a.started.debt \notin a.started.alive)
AuthorityNoOrphanDebt == IF a.debt = None THEN TRUE ELSE
  (DebtKey(a.debt) = DebtKey(Live) /\
   ((a.place = "active" /\ a.gen = a.debt.generation) \/ Settled))
AuthorityNoStaleReport == IF a.reported = None THEN TRUE ELSE
  a.reported.request = a.reported.live

\* These intentionally-failing reachability checks are witnesses, not safety
\* claims. Each describes real state and retained input, not a latched boolean.
WitnessRelaunch == ~(a.place = "active" /\ a.gen = 2 /\
  \E r \in a.reports : r.epoch = a.epoch /\ r.generation = 1)
WitnessTransfer == ~(a.place = "active" /\ a.owner = "owner-b" /\
  \E r \in a.reports : r.epoch = a.epoch /\ r.owner = "owner-a")
WitnessEpoch == ~(a.place = "active" /\ a.epoch = 2 /\
  \E r \in a.reports : r.epoch = 1 /\ r.generation = a.gen)
WitnessReturnStart == IF a.started = None THEN TRUE ELSE
  ~(a.returned # None /\ a.machine = "running" /\ a.started.debt # None)

\* Progress is intentionally narrower than AuthoritySpec. With no clear,
\* transfer or new work, a requested STOP is eventually served; each live
\* child eventually exits and a valid same-owner store operation eventually
\* commits. Unfair environment controls below demonstrate these assumptions.
AuthorityFairNext == AuthorityStop \/ AuthorityCancel \/ AuthorityReturn(Live)
AuthorityFairSpec == AuthorityFairInit /\ [][AuthorityFairNext]_avars /\
  WF_avars(AuthorityStop) /\
  (Fault = "cancel-starve" \/ WF_avars(AuthorityCancel)) /\
  (Fault = "store-starve" \/ WF_avars(AuthorityReturn(Live)))
AuthorityEventuallyReturned == (a.machine = "stopped" /\ a.debt # None) ~> Settled
=============================================================================
