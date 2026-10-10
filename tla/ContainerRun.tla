---------------------------- MODULE ContainerRun ----------------------------
\* The life of the containers of `nova-ci functional --in-container`, on podman
\* and on docker: one container per run, started by a client, bounded by a
\* deadline, removed whatever happens to the client, and the reaper that
\* removes what the run left. docs/SPEC-CI.md ("functional-container"); the code
\* is internal/ci/functionalrun (run.go: runContainer, leftovers; reap.go:
\* judge, reap; args.go: testArgs, removeArgs). It carries forward PR 4708's
\* FunctionalRun model (draft, superseded by this one) down to the state the
\* shipped code owns, and PR 4723 (functionalrun-src-symlink) is folded in with
\* the move of the engine into the package.
\*
\* The state, per container c:
\*   state    none, running, exited, removed
\*   eng      the engine that runs it: podman or docker
\*   startAt  the clock when it started (the label nova.functional.start; the
\*            deadline label is startAt + D)
\*   client   the verb and its runtime client: none, alive, dead, done
\*   mon      podman's monitor of the container lives (conmon: it enforces
\*            `run --timeout` and the --rm removal, outside the container and
\*            outside the client); docker has none, its daemon does both
\*   vols     the container's anonymous volumes (--fresh-gocache's build cache,
\*            the writable layer): they exist until the container is removed
\*            with --volumes
\* Shared: caches (the named cache volumes: the module cache and the build
\* cache; they persist by design and are not a run's), now (the clock), and two
\* histories of what the reaper took: a foreign container, a young container.
\*
\* The bound, per engine. podman: `run --timeout` ends the container at the
\* deadline from the monitor, which holds when the client is killed. docker has
\* no `run --timeout`: the in-container `timeout -k` under --init ends the
\* command at the deadline (and --init reaps whatever it leaves), the client
\* itself force-removes the container at the deadline plus its own grace, and the
\* reaper removes it by the deadline label at the deadline plus the grace. In
\* both, the reaper is the bound that does not depend on the client or the monitor.
\*
\* The assumption the real-time bound stands on, as a guard on Tick: time does
\* not pass while the reaper has a container it must remove (a pass runs before
\* every run, by hand, and on the fleet's timer). With Broken = {"noreaper"}
\* the guard is gone and the invariant NoOutlive is what refutes it.
\*
\* Every other value of Broken is a reversed witness, each refuted by one
\* property of its own (tla/CASES.tsv):
\*   "noreaper"    no reaper: NoOutlive
\*   "byname"      the reaper selects by a name pattern, not by label and age:
\*                 NoForeignTouched
\*   "young"       the reaper does not check the deadline: NoYoungTouched
\*   "prune"       the reaper also prunes volumes: CachesKept
\*   "keepvol"     a removal without --volumes: RemovedHoldsNothing
\*   "keepexited"  the reaper leaves an exited container (the monitor died
\*                 before --rm): RemovedEventually
\* Not here: the output of the tests, how the verb tells a deadline from a kill,
\* the module-cache step (it is a second container of the same shape and the
\* same removal), a reboot (podman's refresh; the reaper covers what it leaves).
EXTENDS Naturals, FiniteSets
CONSTANTS Ctrs, Foreign, Caches, D, G, ClientGrace, StartBy, MaxNow, Broken
Faults == {"noreaper", "byname", "young", "prune", "keepvol", "keepexited"}
ASSUME /\ Broken \subseteq Faults /\ Foreign \subseteq Ctrs
       /\ D \in Nat /\ G \in Nat /\ ClientGrace \in Nat /\ ClientGrace < G
       /\ StartBy \in Nat /\ MaxNow \in Nat
Ours == Ctrs \ Foreign
Engines == {"podman", "docker"}
States == {"none", "running", "exited", "removed"}
Present == {"running", "exited"}

VARIABLES state, eng, startAt, client, mon, vols, caches, now,
          foreignTouched, youngTouched
vars == <<state, eng, startAt, client, mon, vols, caches, now,
          foreignTouched, youngTouched>>

Init ==
 /\ state = [c \in Ctrs |-> IF c \in Foreign THEN "running" ELSE "none"]
 /\ eng = [c \in Ctrs |-> IF c \in Foreign THEN "podman" ELSE "-"]
 /\ startAt = [c \in Ctrs |-> 0]
 /\ client = [c \in Ctrs |-> IF c \in Foreign THEN "none" ELSE "alive"]
 /\ mon = [c \in Ctrs |-> FALSE]
 /\ vols = [c \in Ctrs |-> c \in Foreign]
 /\ caches = Caches /\ now = 0
 /\ foreignTouched = FALSE /\ youngTouched = FALSE

Set(f, c, v) == [f EXCEPT ![c] = v]
\* The deadline label plus the grace has passed.
Overdue(c) == now >= startAt[c] + D + G
Held(c) == eng[c] = "docker" \/ mon[c]

\* ---- the client: the verb and its runtime client
\* docs/SPEC-CI.md: start the one container of the run, with its labels.
Start(c, e) ==
 /\ c \in Ours /\ state[c] = "none" /\ client[c] = "alive" /\ now <= StartBy
 /\ state' = Set(state, c, "running") /\ eng' = Set(eng, c, e)
 /\ startAt' = Set(startAt, c, now)
 /\ mon' = Set(mon, c, e = "podman") /\ vols' = Set(vols, c, TRUE)
 /\ UNCHANGED <<client, caches, now, foreignTouched, youngTouched>>

\* The client's own removal, `rm --force --volumes`: at the end of the run, at
\* its own deadline (the docker bound, the podman backstop), at an interrupt.
ClientRemove(c) ==
 /\ c \in Ours /\ client[c] = "alive" /\ state[c] \in Present
 /\ \/ state[c] = "exited"
    \/ now >= startAt[c] + D + ClientGrace
 /\ state' = Set(state, c, "removed") /\ vols' = Set(vols, c, FALSE)
 /\ client' = Set(client, c, "done") /\ mon' = Set(mon, c, FALSE)
 /\ UNCHANGED <<eng, startAt, caches, now, foreignTouched, youngTouched>>
Interrupt(c) ==
 /\ c \in Ours /\ client[c] = "alive" /\ state[c] = "running"
 /\ state' = Set(state, c, "removed") /\ vols' = Set(vols, c, FALSE)
 /\ client' = Set(client, c, "done") /\ mon' = Set(mon, c, FALSE)
 /\ UNCHANGED <<eng, startAt, caches, now, foreignTouched, youngTouched>>
ClientDone(c) ==
 /\ c \in Ours /\ client[c] = "alive" /\ state[c] = "removed"
 /\ client' = Set(client, c, "done")
 /\ UNCHANGED <<state, eng, startAt, mon, vols, caches, now, foreignTouched,
                youngTouched>>
ClientDies(c) ==
 /\ c \in Ours /\ client[c] = "alive" /\ client' = Set(client, c, "dead")
 /\ UNCHANGED <<state, eng, startAt, mon, vols, caches, now, foreignTouched,
                youngTouched>>

\* ---- inside the container, and the runtime
Finish(c) ==
 /\ c \in Ours /\ state[c] = "running" /\ state' = Set(state, c, "exited")
 /\ UNCHANGED <<eng, startAt, client, mon, vols, caches, now, foreignTouched,
                youngTouched>>
\* The bound: podman's --timeout from the monitor, docker's timeout -k inside.
RuntimeBound(c) ==
 /\ c \in Ours /\ state[c] = "running" /\ now >= startAt[c] + D /\ Held(c)
 /\ state' = Set(state, c, "exited")
 /\ UNCHANGED <<eng, startAt, client, mon, vols, caches, now, foreignTouched,
                youngTouched>>
\* --rm: the monitor (podman) or the daemon (docker) removes the exited
\* container with its anonymous volumes.
AutoRemove(c) ==
 /\ c \in Ours /\ state[c] = "exited" /\ Held(c)
 /\ state' = Set(state, c, "removed") /\ vols' = Set(vols, c, FALSE)
 /\ mon' = Set(mon, c, FALSE)
 /\ UNCHANGED <<eng, startAt, client, caches, now, foreignTouched, youngTouched>>
MonitorDies(c) ==
 /\ c \in Ours /\ mon[c] /\ mon' = Set(mon, c, FALSE)
 /\ UNCHANGED <<state, eng, startAt, client, vols, caches, now, foreignTouched,
                youngTouched>>

\* ---- the reaper: by label and deadline, any state, with volumes, never caches
Selects(c) ==
 /\ "noreaper" \notin Broken
 /\ state[c] \in Present
 /\ "keepexited" \notin Broken \/ state[c] = "running"
 /\ IF "byname" \in Broken THEN TRUE
    ELSE IF "young" \in Broken THEN c \in Ours
    ELSE c \in Ours /\ Overdue(c)
Reap(c) ==
 /\ Selects(c)
 /\ state' = Set(state, c, "removed")
 /\ vols' = Set(vols, c, IF "keepvol" \in Broken THEN vols[c] ELSE FALSE)
 /\ mon' = Set(mon, c, FALSE)
 /\ caches' = IF "prune" \in Broken THEN {} ELSE caches
 /\ foreignTouched' = (foreignTouched \/ c \in Foreign)
 /\ youngTouched' = (youngTouched \/ (c \in Ours /\ ~Overdue(c)))
 /\ UNCHANGED <<eng, startAt, client, now>>

\* Time passes, unless the reaper has work.
Pending(c) == c \in Ours /\ state[c] \in Present /\ Overdue(c)
Tick ==
 /\ now < MaxNow
 /\ "noreaper" \in Broken \/ ~\E c \in Ours : Pending(c)
 /\ now' = now + 1
 /\ UNCHANGED <<state, eng, startAt, client, mon, vols, caches, foreignTouched,
                youngTouched>>

Next ==
 \/ Tick
 \/ \E c \in Ctrs :
      \/ \E e \in Engines : Start(c, e)
      \/ ClientRemove(c) \/ Interrupt(c) \/ ClientDone(c) \/ ClientDies(c)
      \/ Finish(c) \/ RuntimeBound(c) \/ AutoRemove(c) \/ MonitorDies(c)
      \/ Reap(c)

\* Fairness only on what is not a person, a test or the client: time, the
\* runtime's bound and removal while its monitor lives, and the reaper.
\* Nothing is assumed of the client (it may die anywhere), of the tests (they
\* may hang), of the monitor's survival, or of an interrupt.
Fairness ==
 /\ WF_vars(Tick)
 /\ \A c \in Ctrs : WF_vars(RuntimeBound(c)) /\ WF_vars(AutoRemove(c))
                    /\ WF_vars(Reap(c))
Spec == Init /\ [][Next]_vars /\ Fairness

TypeOK ==
 /\ state \in [Ctrs -> States] /\ eng \in [Ctrs -> Engines \cup {"-"}]
 /\ startAt \in [Ctrs -> 0..MaxNow] /\ mon \in [Ctrs -> BOOLEAN]
 /\ client \in [Ctrs -> {"none", "alive", "dead", "done"}]
 /\ vols \in [Ctrs -> BOOLEAN] /\ caches \subseteq Caches /\ now \in 0..MaxNow
 /\ foreignTouched \in BOOLEAN /\ youngTouched \in BOOLEAN

\* S1: no container of ours outlives its deadline plus the grace.
NoOutlive == \A c \in Ours : state[c] \in Present => now <= startAt[c] + D + G
\* S2: the reaper never touches a foreign container.
NoForeignTouched == ~foreignTouched
\* S3: the reaper never touches a young one (before its deadline plus grace).
NoYoungTouched == ~youngTouched
\* S4: a cache volume is never removed.
CachesKept == caches = Caches
\* S5: a removed container holds nothing: its anonymous volumes went with it.
RemovedHoldsNothing == \A c \in Ctrs : state[c] = "removed" => ~vols[c]

\* L1: every started container is removed, whatever happens to its client.
RemovedEventually == \A c \in Ours : (state[c] = "running") ~> (state[c] = "removed")
=============================================================================
