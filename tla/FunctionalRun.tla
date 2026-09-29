---------------------------- MODULE FunctionalRun ----------------------------
\* The life of one functional-test run inside a container, from the request
\* to the receipt, whatever happens to the client that asked for it. The
\* design of the runner verb in future/SPEC-FUNCTIONAL-RUN.md: the functional
\* tier runs inside ONE container per run, and the runtime, never the client,
\* owns and removes every process, namespace, shared memory segment and temp
\* file of the run.
\*
\* The rule the design stands on is what a hard kill of the client showed:
\* the container of a run whose client died runs on until a bound enforced
\* by the runtime or inside the container ends it. So the owner of cleanup is
\* THE RUNTIME'S OWN BOUND (podman run --timeout, enforced by the runtime's
\* monitor, conmon) together with --rm, plus A REAPER that selects by label
\* and age, and never the client process.
\*
\* The state, per run r:
\*   phase     none, requested (the verb wrote its intent), created, running,
\*             finished, timedout, killed (the container exited), removed
\*   how       how the container ended: pass, fail, inner (the in-container
\*             timeout), deadline (the runtime's), killed (a person), lost
\*             (the machine rebooted or the user session ended without
\*             lingering), reaped
\*   ctr       the container exists in the runtime (its record and storage)
\*   held      the run's own resources, which exist iff held: "vol" is what
\*             dies with the container's processes (the processes, the
\*             network and IPC namespaces with their shared memory, the
\*             tmpfs temp directories); "disk" is what lives until the
\*             container is removed with its volumes (the writable layer
\*             and any anonymous volume)
\*   outside   resources of the run that live outside its container (a
\*             dependency daemonised by a fixture outside it)
\*   client    the verb and its podman client: none, alive, dead, done
\*   age       time since the intent, against the run's deadline D and the
\*             reaper's grace G: young (< D), late (>= D), overdue (>= D+G)
\*   mon       the runtime's monitor of the container lives (conmon); it
\*             enforces --timeout and runs the --rm removal
\*   receipt   the first receipt written, and writers who wrote one
\*   notified  the reaper reported the run to a person
\* Shared: caches (the cache volumes: the module cache, filled by a separate
\* networked step and mounted read-only, and the build caches; they persist
\* by design and are not a run's), losses (reboots so far), trespass (history:
\* something removed a run's container before it was overdue, other than
\* that run's own lifecycle or a person).
\*
\* The reaper's view is the labels on the containers: the run id and the
\* intent's start time. It sees {r : ctr[r]}, reads age[r] from the label and
\* the clock, and reads the intents without a receipt. age stands for the
\* label's start time and the clock together.
\*
\* The design, Broken = {}:
\*   request  the verb writes the run's intent (id, start time, deadline)
\*   create   podman run --rm --timeout <what is left of D> with the labels;
\*            only while young: a run that could not start in time is not
\*            started (a --timeout of 0 is no timeout at all)
\*   exit     the tests pass, fail or hang; the inner timeout may end them
\*            (not guaranteed: a test can defeat it); the runtime's deadline
\*            ends them at D; a person may kill the container
\*   remove   the runtime's --rm, run by the monitor, removes the exited
\*            container with its anonymous volumes
\*   receipt  the verb writes it after the removal, from a leftover check,
\*            by exclusive create: the first writer wins
\*   reaper   removes (rm --force --volumes) every labelled container past
\*            D+G, in any state, and for every intent past D+G without a
\*            receipt writes the receipt itself, outcome reaped, and reports
\*            it to a person
\* The outside: the client dies at any moment; a person kills the container;
\* the monitor dies (a person, or a job-end sweep of the CI runner that kills
\* by environment); the machine reboots or the user session ends without
\* lingering (runtime loss: every process of the user dies, containers
\* persist as exited or configured records); after a loss, podman's first
\* command refreshes its state and removes the --rm containers that had run
\* (libpod refresh at v5.7.0), or does not (modelled as optional); time
\* passes.
\*
\* Every other value of Broken is a reversed witness:
\*   "norm"        no --rm: the client removes the container after it exits
\*   "reportonly"  the reaper reports but never removes
\*                 {"norm", "reportonly"} is cleanup owned by the client;
\*                 {"reportonly"} alone is --rm and --timeout without a reaper
\*   "nodeadline"  no --timeout: --rm alone
\*   "name"        the reaper selects by a name pattern, not by label and age
\*   "caches"      the reaper removes volumes (a volume prune), caches too
\*   "countcaches" the leftover check counts the cache volumes
\*   "early"       the receipt is written when the container exits, before
\*                 the removal, and says clean without looking
\*   "outside"     a fixture starts a dependency outside the container
\*   "keepvol"     the reaper removes without --volumes
\*   "twice"       the receipt is written without an exclusive create
\*   "silent"      the reaper removes and writes no receipt and no report
\*
\* Not here: podman's refusal after an unhandled reboot (a runtime directory
\* not on tmpfs; the verb and the reaper refuse, and the runner's check
\* holds it); a create killed half way, leaving storage without a record; a
\* create the runtime refuses (the verb writes its receipt, nothing made);
\* the output of the tests; how the verb tells a deadline from a kill. Those
\* are tests of the verb.
EXTENDS Naturals, FiniteSets
CONSTANTS Runs, Caches, MaxLoss, Broken
Faults == {"norm", "reportonly", "nodeadline", "name", "caches", "countcaches",
           "early", "outside", "keepvol", "twice", "silent"}
ASSUME Broken \subseteq Faults /\ MaxLoss \in Nat

Exited == {"finished", "timedout", "killed"}
Phases == {"none", "requested", "created", "running", "removed"} \cup Exited
Hows == {"-", "pass", "fail", "inner", "deadline", "killed", "lost", "reaped"}
Ages == {"young", "late", "overdue"}
Clients == {"none", "alive", "dead", "done"}
NoReceipt == [by |-> "-", how |-> "-", clean |-> FALSE, left |-> 0,
              truth |-> 0, ctrAt |-> FALSE]

VARIABLES phase, how, ctr, held, outside, client, age, mon,
          receipt, writers, notified, caches, losses, trespass
vars == <<phase, how, ctr, held, outside, client, age, mon,
          receipt, writers, notified, caches, losses, trespass>>
paper == <<receipt, writers, notified>>

Init ==
 /\ phase = [r \in Runs |-> "none"] /\ how = [r \in Runs |-> "-"]
 /\ ctr = [r \in Runs |-> FALSE] /\ held = [r \in Runs |-> {}]
 /\ outside = [r \in Runs |-> {}] /\ client = [r \in Runs |-> "none"]
 /\ age = [r \in Runs |-> "young"] /\ mon = [r \in Runs |-> FALSE]
 /\ receipt = [r \in Runs |-> NoReceipt] /\ writers = [r \in Runs |-> {}]
 /\ notified = [r \in Runs |-> FALSE]
 /\ caches = Caches /\ losses = 0 /\ trespass = FALSE

Set(f, r, v) == [f EXCEPT ![r] = v]

\* What the leftover check finds of the run: its own resources wherever
\* they are, and its container. Caches are not the run's.
Leftovers(r) == Cardinality(held[r]) + Cardinality(outside[r])
                + (IF ctr[r] THEN 1 ELSE 0)
Counted(r) == Leftovers(r)
              + (IF "countcaches" \in Broken THEN Cardinality(caches) ELSE 0)
\* One writer's receipt. Exclusive create: the first receipt stays.
MayWrite(r, by) == IF "twice" \in Broken THEN by \notin writers[r]
                   ELSE writers[r] = {}
Write(r, by, h, blind) ==
 LET left == IF blind THEN 0 ELSE Counted(r)
 IN /\ receipt' = IF writers[r] = {}
                  THEN Set(receipt, r, [by |-> by, how |-> h,
                         clean |-> (left = 0), left |-> left,
                         truth |-> Leftovers(r), ctrAt |-> ctr[r]])
                  ELSE receipt
    /\ writers' = Set(writers, r, writers[r] \cup {by})

\* ---- the client: the verb and its podman client
Request(r) ==
 /\ phase[r] = "none"
 /\ phase' = Set(phase, r, "requested") /\ client' = Set(client, r, "alive")
 /\ UNCHANGED <<how, ctr, held, outside, age, mon, paper, caches, losses,
                trespass>>

Create(r) ==
 /\ client[r] = "alive" /\ phase[r] = "requested" /\ age[r] = "young"
 /\ phase' = Set(phase, r, "created") /\ ctr' = Set(ctr, r, TRUE)
 /\ held' = Set(held, r, {"disk"})
 /\ UNCHANGED <<how, outside, client, age, mon, paper, caches, losses,
                trespass>>

Start(r) ==
 /\ client[r] = "alive" /\ phase[r] = "created" /\ age[r] = "young"
 /\ phase' = Set(phase, r, "running") /\ held' = Set(held, r, {"disk", "vol"})
 /\ mon' = Set(mon, r, TRUE)
 /\ UNCHANGED <<how, ctr, outside, client, age, paper, caches, losses,
                trespass>>

\* "norm": without --rm the client removes what exited.
ClientRemove(r) ==
 /\ "norm" \in Broken
 /\ client[r] = "alive" /\ phase[r] \in Exited /\ ctr[r]
 /\ phase' = Set(phase, r, "removed") /\ ctr' = Set(ctr, r, FALSE)
 /\ held' = Set(held, r, {}) /\ mon' = Set(mon, r, FALSE)
 /\ UNCHANGED <<how, outside, client, age, paper, caches, losses, trespass>>

\* After the removal, from the leftover check, and then the verb exits.
WriteReceipt(r) ==
 /\ client[r] = "alive" /\ MayWrite(r, "client")
 /\ IF "early" \in Broken THEN phase[r] \in Exited \cup {"removed"}
    ELSE phase[r] = "removed"
 /\ Write(r, "client", how[r], "early" \in Broken)
 /\ client' = Set(client, r, "done")
 /\ UNCHANGED <<phase, how, ctr, held, outside, age, mon, notified, caches,
                losses, trespass>>

\* ---- inside the container
Leave(r, to, h) ==
 /\ phase[r] = "running"
 /\ phase' = Set(phase, r, to) /\ how' = Set(how, r, h)
 /\ held' = Set(held, r, held[r] \ {"vol"})
 /\ UNCHANGED <<ctr, outside, client, age, mon, paper, caches, losses,
                trespass>>
Finish(r) == \E h \in {"pass", "fail"} : Leave(r, "finished", h)
InnerTimeout(r) == Leave(r, "timedout", "inner")
\* "outside": a fixture daemonises its dependency where the container is not.
Daemonise(r) ==
 /\ "outside" \in Broken /\ phase[r] = "running" /\ outside[r] = {}
 /\ outside' = Set(outside, r, {"dep"})
 /\ UNCHANGED <<phase, how, ctr, held, client, age, mon, paper, caches,
                losses, trespass>>

\* ---- the runtime
Bounded == "nodeadline" \notin Broken
RuntimeDeadline(r) ==
 /\ Bounded /\ mon[r] /\ age[r] # "young"
 /\ Leave(r, "timedout", "deadline")
\* --rm: the monitor removes the exited container with its volumes.
AutoRemove(r) ==
 /\ "norm" \notin Broken
 /\ phase[r] \in Exited /\ ctr[r] /\ mon[r]
 /\ phase' = Set(phase, r, "removed") /\ ctr' = Set(ctr, r, FALSE)
 /\ held' = Set(held, r, {}) /\ mon' = Set(mon, r, FALSE)
 /\ UNCHANGED <<how, outside, client, age, paper, caches, losses, trespass>>
\* After a loss podman's first command removes the --rm containers that had
\* run (libpod refresh: every container that ran is reset to exited, and an
\* exited auto-remove container is removed with its volumes); a created one
\* is reset to configured and kept. It may never happen: optional.
Refresh(r) ==
 /\ losses > 0 /\ "norm" \notin Broken
 /\ phase[r] \in Exited /\ ctr[r] /\ ~mon[r]
 /\ phase' = Set(phase, r, "removed") /\ ctr' = Set(ctr, r, FALSE)
 /\ held' = Set(held, r, {})
 /\ UNCHANGED <<how, outside, client, age, mon, paper, caches, losses,
                trespass>>

\* ---- the outside
ClientDies(r) ==
 /\ client[r] = "alive" /\ client' = Set(client, r, "dead")
 /\ UNCHANGED <<phase, how, ctr, held, outside, age, mon, paper, caches,
                losses, trespass>>
OperatorKill(r) == Leave(r, "killed", "killed")
\* The monitor dies with the container still there: no --timeout, no --rm.
MonitorDies(r) ==
 /\ mon[r] /\ mon' = Set(mon, r, FALSE)
 /\ UNCHANGED <<phase, how, ctr, held, outside, client, age, paper, caches,
                losses, trespass>>
\* A reboot, or the user session ending without lingering: every process of
\* the user dies (clients, monitors, containers, anything outside); records
\* and storage stay; the runtime forgets its clients.
RuntimeLoss ==
 /\ losses < MaxLoss /\ losses' = losses + 1
 /\ phase' = [r \in Runs |-> IF phase[r] = "running" THEN "killed" ELSE phase[r]]
 /\ how' = [r \in Runs |-> IF phase[r] = "running" THEN "lost" ELSE how[r]]
 /\ held' = [r \in Runs |-> held[r] \ {"vol"}]
 /\ outside' = [r \in Runs |-> {}]
 /\ mon' = [r \in Runs |-> FALSE]
 /\ client' = [r \in Runs |-> IF client[r] = "alive" THEN "dead" ELSE client[r]]
 /\ UNCHANGED <<ctr, age, paper, caches, trespass>>
\* Time passes. The runtime's deadline fires by D: time does not pass D+G
\* while a bounded container runs under a live monitor.
TickLate(r) ==
 /\ phase[r] # "none" /\ age[r] = "young" /\ age' = Set(age, r, "late")
 /\ UNCHANGED <<phase, how, ctr, held, outside, client, mon, paper, caches,
                losses, trespass>>
TickOverdue(r) ==
 /\ age[r] = "late"
 /\ ~(Bounded /\ phase[r] = "running" /\ mon[r])
 /\ age' = Set(age, r, "overdue")
 /\ UNCHANGED <<phase, how, ctr, held, outside, client, mon, paper, caches,
                losses, trespass>>

\* ---- the reaper: by label and age; any state; with volumes; never caches
Selects(r) == ctr[r] /\ ("name" \in Broken \/ age[r] = "overdue")
ReapRemove(r) ==
 /\ "reportonly" \notin Broken /\ Selects(r)
 /\ phase' = Set(phase, r, "removed") /\ ctr' = Set(ctr, r, FALSE)
 /\ how' = Set(how, r, IF phase[r] \in {"created", "running"} THEN "reaped"
                       ELSE how[r])
 /\ held' = Set(held, r, IF "keepvol" \in Broken THEN held[r] \cap {"disk"}
                         ELSE {})
 /\ mon' = Set(mon, r, FALSE)
 /\ caches' = IF "caches" \in Broken THEN {} ELSE caches
 /\ trespass' = (trespass \/ age[r] # "overdue")
 /\ UNCHANGED <<outside, client, age, paper, losses>>
\* An intent past D+G without a receipt: the reaper writes it and tells a
\* person. It reports only what it could remove, unless it never removes.
ReapReport(r) ==
 /\ "silent" \notin Broken
 /\ age[r] = "overdue" /\ phase[r] # "none" /\ MayWrite(r, "reaper")
 /\ (~ctr[r] \/ "reportonly" \in Broken)
 /\ Write(r, "reaper", "reaped", FALSE)
 /\ notified' = Set(notified, r, TRUE)
 /\ UNCHANGED <<phase, how, ctr, held, outside, client, age, mon, caches,
                losses, trespass>>

Step(r) ==
 \/ Request(r) \/ Create(r) \/ Start(r) \/ ClientRemove(r) \/ WriteReceipt(r)
 \/ Finish(r) \/ InnerTimeout(r) \/ Daemonise(r)
 \/ RuntimeDeadline(r) \/ AutoRemove(r) \/ Refresh(r)
 \/ ClientDies(r) \/ OperatorKill(r) \/ MonitorDies(r)
 \/ TickLate(r) \/ TickOverdue(r)
 \/ ReapRemove(r) \/ ReapReport(r)
Next == RuntimeLoss \/ \E r \in Runs : Step(r)

\* Fairness, and only on what is not a person, a test or the client:
\*   time passes (TickLate, TickOverdue);
\*   the runtime's deadline fires while its monitor lives (RuntimeDeadline):
\*     conmon enforces --timeout outside the container;
\*   the monitor's --rm removal runs once the container exits (AutoRemove);
\*   the reaper runs (ReapRemove, ReapReport): a timer outside any run.
\* Nothing is assumed of the client (it may stall or die anywhere), of the
\* tests (they may hang, and defeat the inner timeout), of a person, of the
\* monitor's survival, of a reboot, or of podman's refresh.
Fairness ==
 \A r \in Runs :
   /\ WF_vars(TickLate(r)) /\ WF_vars(TickOverdue(r))
   /\ WF_vars(RuntimeDeadline(r)) /\ WF_vars(AutoRemove(r))
   /\ WF_vars(ReapRemove(r)) /\ WF_vars(ReapReport(r))
Spec == Init /\ [][Next]_vars /\ Fairness

TypeOK ==
 /\ phase \in [Runs -> Phases] /\ how \in [Runs -> Hows]
 /\ ctr \in [Runs -> BOOLEAN] /\ held \in [Runs -> SUBSET {"vol", "disk"}]
 /\ outside \in [Runs -> SUBSET {"dep"}] /\ client \in [Runs -> Clients]
 /\ age \in [Runs -> Ages] /\ mon \in [Runs -> BOOLEAN]
 /\ writers \in [Runs -> SUBSET {"client", "reaper"}]
 /\ notified \in [Runs -> BOOLEAN] /\ caches \subseteq Caches
 /\ losses \in 0..MaxLoss /\ trespass \in BOOLEAN
 /\ \A r \in Runs : receipt[r].by \in {"-", "client", "reaper"}
\* The container exists exactly between its creation and its removal.
Consistent ==
 \A r \in Runs :
   /\ ctr[r] <=> phase[r] \in {"created", "running"} \cup Exited
   /\ ("vol" \in held[r]) <=> phase[r] = "running"

\* S1: a removed run holds nothing.
RemovedHoldsNothing == \A r \in Runs : phase[r] = "removed" => held[r] = {}
\* S2: nothing of a run lives outside its container.
NothingOutside == \A r \in Runs : outside[r] = {}
\* S3: a receipt says clean only when the run left nothing and its
\* container was gone when it was written.
CleanIsTrue ==
 \A r \in Runs : receipt[r].clean => receipt[r].truth = 0 /\ ~receipt[r].ctrAt
\* S4: nothing takes a run's container before it is overdue, other than the
\* run's own lifecycle or a person.
NoTrespass == ~trespass
\* S5: the caches are never removed, and never counted as leftovers: a run
\* that left nothing of its own is reported with nothing left.
CachesKept ==
 /\ caches = Caches
 /\ \A r \in Runs : receipt[r].by # "-" /\ receipt[r].truth = 0
                      => receipt[r].left = 0
\* S6: at most one receipt per run.
OneReceipt == \A r \in Runs : Cardinality(writers[r]) <= 1
\* S7: no container runs past its deadline plus the grace while the
\* runtime's monitor of it lives. (A dead monitor leaves it to the reaper:
\* RemovedEventually.)
BoundHolds ==
 \A r \in Runs : (age[r] = "overdue" /\ phase[r] = "running") => ~mon[r]

\* L1: every run that is created is eventually removed, whatever happens to
\* its client.
RemovedEventually == \A r \in Runs : (phase[r] = "created") ~> (phase[r] = "removed")
\* L2: every run eventually has a receipt or is reported to a person.
ReceiptOrReport ==
 \A r \in Runs : (phase[r] = "requested") ~> (writers[r] # {} \/ notified[r])
=============================================================================
