---------------------------- MODULE UpdateApply ----------------------------
\* internal/update: the states of the version inventory's invocations, from
\* docs/SPEC-UPDATE.md (rules 7, 9, 10, 13, 13a, 22, 24, 25, 26) and the code
\* those rules name: the manifest's entries (internal/update/manifest.go
\* `Load`), the verdict of one entry's two reads (internal/update/cli.go
\* `verdict`), the apply of one named entry and its dry run
\* (internal/update/cli.go `apply`, `applyDryRun`), the report's state file
\* with the deliveries it records SENT (internal/update/report.go `report`,
\* `deliver`, `sendOK`; internal/update/snapshot.go `readSnapshot`,
\* `writeSnapshotWith`), and the state file's writer lock
\* (internal/update/snapshot_unix.go `lockSnapshot`).
\*
\* The manifest's entries are the model's constants and not its state: one
\* invocation loads them once from the file and nothing this module does
\* writes them. What the file holds per entry -- its name, its kind, the argv
\* that reads its installed version, the source its latest is published at,
\* the argv that applies an update -- is abstracted to two constants: whether
\* the entry's kind is one whose verdict compares by equality (`Pins`: the
\* kinds pin and model, cli.go `verdict`), and the version its source
\* publishes (`Published`, one version for every entry, below `MaxVer` so an
\* installed version can be ahead of it). An entry whose apply column is
\* `none` is refused before any process starts, so every entry here has an
\* apply argv.
\*
\* Two updaters run at once: Updaters is the set of invocations in flight,
\* each with its own reads, verdicts, named entry and --dry-run flag, over
\* the one installed world and the one state file. An apply takes no lock, so
\* two updaters may install at once, each for the entry its own invocation
\* named. The state file is shared and its writes are not: the updater that
\* writes it holds the file's sibling lock until the report it left is
\* delivered or its process dies, and a second writer's write is refused busy
\* while the lock is held (SPEC-UPDATE rule 25: the lock serializes writers
\* across the atomic renames, and process death releases it;
\* snapshot_unix.go `lockSnapshot`).
\*
\* One version read is one number: `None` (this run has not read it),
\* `Unknown` (the read ran and did not answer: SPEC-UPDATE rule 7), or the
\* version; the two markers are below every version, so a read is never read
\* as one. The same markers stand for the world an entry's installed argv
\* reads: `Unknown` there is a path that holds no binary, which no read
\* answers with a version. The state file's observed set holds the same value
\* per entry: report.go keeps the raw identity beside the status, and the two
\* move together, so one value stands for both. `report` reads a latest only
\* for a `local:` source while `check` reads every one, so this module's
\* Read, which reads both, covers the reads of both verbs.
\*
\* One updater runs one invocation at a time, and its `phase` says which:
\*   idle       no invocation for that updater
\*   reading    its run is reading the entries (cli.go `readEntries`)
\*   written    the state file holds its run's reads and a report is pending
\*   crashed    its process died between the write and the send
\*   named      an apply invocation named one entry
\*
\* An install is the world's to land, and it can die at its replace: where
\* the host refuses to rename over a running image, the running file is
\* moved aside and the new binary is renamed in, and a crash between the move
\* aside and the rename leaves no binary at the entry's path. What the
\* entry's installed argv reads next is UNKNOWN, never a version (SPEC-UPDATE
\* rules 7 and 22).
\*
\* WHAT THE LIVENESS DOES NOT COVER. CrashedReportDelivers is proved once the
\* events that compete with a run go quiet: one counter, `faults`, bounds the
\* outside changes, the crashes, the busy refusals, the refused deliveries
\* and the apply invocations at MaxFaults, so the property says nothing of a
\* machine that keeps changing a tool's version, crashing a run or an
\* install, refusing a delivery or a write forever (Land.tla bounds its
\* outside events the same way, and its Recovers covers the same ground).
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* one guard removed, and the invariant its comment names fails:
\*   "installonverdict"  a STALE verdict installs by itself -> ApplyOnlyWhatIsNamed
\*   "dryruninstalls"    a dry run starts the install       -> DryRunInstallsNothing
\*   "sentonrefusal"     a refusal is recorded as SENT      -> SentOnlyOnOK
\*   "nolock"            a write skips the state file's lock -> WriterHoldsTheLock
\*   "asideisversion"    the crashed replace reads as landed -> AsideIsUnknown

EXTENDS Naturals

CONSTANTS Entries, Updaters, Pins, Published, MaxVer, MaxFaults, Broken

None == 0 - 1     \* this run has not read it
Unknown == 0 - 2  \* the read ran and did not answer; a path with no binary
Vers == {None, Unknown} \cup 0..MaxVer
Worlds == {Unknown} \cup 0..MaxVer
Verdicts == {"none", "EQUAL", "STALE", "AHEAD", "DIFFERENT", "UNKNOWN"}
Phases == {"idle", "reading", "written", "crashed", "named"}
Starts == {"none", "named", "unnamed", "dryrun"}

VARIABLES
  installed,      \* the version each entry's installed argv reads now
  aside,          \* ghost: the running binary sits aside, none renamed in
  instRead,       \* this run's installed read, per updater and entry
  latRead,        \* this run's latest read, per updater and entry
  verdictOf,      \* the verdict of each entry's two reads ("none": none yet),
                  \* per updater
  installs,       \* ghost: how an install process started, per entry
  applyRan,       \* whether an apply ran on the entry
  afterVer,       \* what the last apply read after its install
  dryRun,         \* the invocation's --dry-run flag, per updater
  named,          \* the entry the invocation named, per updater, as a set of
                  \* at most one: empty when no invocation named one
  phase,          \* which invocation each updater runs
  lockBy,         \* the updater that holds the state file's lock, if one does
  stateObserved,  \* the state file's observed set
  changedSet,     \* the CHANGED names the last run printed
  changedAgrees,  \* ghost: CHANGED named exactly the entries that moved
  delivered,      \* the state file records a delivery SENT
  sentOK,         \* ghost: the bus said SEND OK when that delivery was recorded
  pending,        \* a report is composed and held in the state file, not sent
  faults          \* the events that compete with a run so far, bounded by MaxFaults

vars == <<installed, aside, instRead, latRead, verdictOf, installs, applyRan,
          afterVer, dryRun, named, phase, lockBy, stateObserved, changedSet,
          changedAgrees, delivered, sentOK, pending, faults>>

world == <<installed, aside, installs, applyRan, afterVer>>
runOf == <<instRead, latRead, verdictOf, dryRun, named, phase>>
fileOf == <<stateObserved, changedSet, changedAgrees, delivered, sentOK, pending>>

TypeOK ==
  /\ installed \in [Entries -> Worlds]
  /\ aside \in [Entries -> BOOLEAN]
  /\ instRead \in [Updaters -> [Entries -> Vers]]
  /\ latRead \in [Updaters -> [Entries -> Vers]]
  /\ verdictOf \in [Updaters -> [Entries -> Verdicts]]
  /\ installs \in [Entries -> Starts]
  /\ applyRan \in [Entries -> BOOLEAN]
  /\ afterVer \in Vers
  /\ dryRun \in [Updaters -> BOOLEAN]
  /\ named \in [Updaters -> SUBSET Entries]
  /\ phase \in [Updaters -> Phases]
  /\ lockBy \in Updaters \cup {None}
  /\ stateObserved \in [Entries -> Vers]
  /\ changedSet \in SUBSET Entries
  /\ changedAgrees \in BOOLEAN
  /\ delivered \in BOOLEAN
  /\ sentOK \in BOOLEAN
  /\ pending \in BOOLEAN
  /\ faults \in 0..MaxFaults

Init ==
  /\ installed = [e \in Entries |-> 0]
  /\ aside = [e \in Entries |-> FALSE]
  /\ instRead = [p \in Updaters |-> [e \in Entries |-> None]]
  /\ latRead = [p \in Updaters |-> [e \in Entries |-> None]]
  /\ verdictOf = [p \in Updaters |-> [e \in Entries |-> "none"]]
  /\ installs = [e \in Entries |-> "none"]
  /\ applyRan = [e \in Entries |-> FALSE]
  /\ afterVer = None
  /\ dryRun = [p \in Updaters |-> FALSE]
  /\ named = [p \in Updaters |-> {}]
  /\ phase = [p \in Updaters |-> "idle"]
  /\ lockBy = None
  /\ stateObserved = [e \in Entries |-> None]
  /\ changedSet = {}
  /\ changedAgrees = TRUE
  /\ delivered = FALSE
  /\ sentOK = FALSE
  /\ pending = FALSE
  /\ faults = 0

\* An outside event: a tool's installed version changes between runs, and a
\* binary a crashed replace left aside is back at its path.
OutsideChange(e) ==
  /\ faults < MaxFaults
  /\ faults' = faults + 1
  /\ \E v \in 0..MaxVer : installed' = [installed EXCEPT ![e] = v]
  /\ aside' = [aside EXCEPT ![e] = FALSE]
  /\ UNCHANGED <<installs, applyRan, afterVer, runOf, lockBy, fileOf>>

\* A run begins: this run's reads and verdicts start empty, and a report a
\* crashed run left pending stays pending in the state file.
StartRun(p) ==
  /\ phase[p] \in {"idle", "crashed"}
  /\ phase' = [phase EXCEPT ![p] = "reading"]
  /\ instRead' = [instRead EXCEPT ![p] = [e \in Entries |-> None]]
  /\ latRead' = [latRead EXCEPT ![p] = [e \in Entries |-> None]]
  /\ verdictOf' = [verdictOf EXCEPT ![p] = [e \in Entries |-> "none"]]
  /\ UNCHANGED <<world, dryRun, named, lockBy, fileOf, faults>>

\* Read: installed and latest for one entry, and either read may fail, which
\* is UNKNOWN and never a version (SPEC-UPDATE rule 7).
Read(p, e) ==
  /\ phase[p] = "reading"
  /\ instRead[p][e] = None
  /\ \E okI \in BOOLEAN, okL \in BOOLEAN :
       /\ instRead' = [instRead EXCEPT ![p][e] = IF okI THEN installed[e] ELSE Unknown]
       /\ latRead' = [latRead EXCEPT ![p][e] = IF okL THEN Published ELSE Unknown]
  /\ UNCHANGED <<world, verdictOf, dryRun, named, phase, lockBy, fileOf, faults>>

\* Verdict: from the two reads of one entry, and only once this run has both
\* (cli.go `verdict`).
Verdict(p, e) ==
  /\ phase[p] = "reading"
  /\ verdictOf[p][e] = "none"
  /\ instRead[p][e] # None
  /\ latRead[p][e] # None
  /\ verdictOf' = [verdictOf EXCEPT ![p][e] =
       IF instRead[p][e] = Unknown \/ latRead[p][e] = Unknown
         THEN "UNKNOWN"
         ELSE IF e \in Pins
           THEN IF instRead[p][e] = latRead[p][e] THEN "EQUAL" ELSE "DIFFERENT"
           ELSE IF instRead[p][e] > latRead[p][e] THEN "AHEAD"
           ELSE IF instRead[p][e] < latRead[p][e] THEN "STALE"
           ELSE "EQUAL"]
  /\ UNCHANGED <<world, instRead, latRead, dryRun, named, phase, lockBy,
                 fileOf, faults>>

\* Report: every entry read, CHANGED computed against the state file as it
\* stood, the state file written with what this run read, and, under --send
\* alone, the report composed and held pending in that file (SPEC-UPDATE
\* rules 24 and 25; report.go writes it through snapshot.go's rename). The
\* write takes the file's lock, which must be free here, and the writer
\* holds it until the report it left is delivered or it dies (SPEC-UPDATE
\* rule 25; snapshot_unix.go `lockSnapshot`).
WriteState(p) ==
  /\ phase[p] = "reading"
  /\ \A e \in Entries : instRead[p][e] # None /\ latRead[p][e] # None
  /\ lockBy = None \/ Broken = "nolock"
  /\ \E sends \in BOOLEAN :
       /\ changedSet' = {e \in Entries : stateObserved[e] # instRead[p][e]}
       /\ changedAgrees' = \A e \in Entries :
            (e \in changedSet') = (stateObserved[e] # instRead[p][e])
       /\ stateObserved' = instRead[p]
       /\ pending' = (pending \/ sends)
       /\ phase' = [phase EXCEPT ![p] = IF pending \/ sends THEN "written" ELSE "idle"]
       /\ lockBy' = IF Broken = "nolock" THEN lockBy
                    ELSE IF pending \/ sends THEN p ELSE None
  /\ UNCHANGED <<world, instRead, latRead, verdictOf, dryRun, named,
                 delivered, sentOK, faults>>

\* The bounded wait for the lock ends: the write is refused busy and nothing
\* is written (snapshot_unix.go `lockSnapshot`; SPEC-UPDATE rule 8, the
\* bounded run).
BusyRefused(p) ==
  /\ phase[p] = "reading"
  /\ \A e \in Entries : instRead[p][e] # None /\ latRead[p][e] # None
  /\ lockBy # None /\ lockBy # p
  /\ Broken # "nolock"
  /\ faults < MaxFaults
  /\ faults' = faults + 1
  /\ phase' = [phase EXCEPT ![p] = "idle"]
  /\ UNCHANGED <<world, instRead, latRead, verdictOf, dryRun, named, lockBy,
                 fileOf>>

\* CrashBetweenWriteAndSend: the process dies once the state file is renamed
\* and before the send, so the file keeps the run's reads and its pending
\* report and the run's own memory is gone; the lock goes with the process
\* (SPEC-UPDATE rule 25: process death releases ownership).
Crash(p) ==
  /\ phase[p] = "written"
  /\ faults < MaxFaults
  /\ faults' = faults + 1
  /\ phase' = [phase EXCEPT ![p] = "crashed"]
  /\ lockBy' = IF lockBy = p THEN None ELSE lockBy
  /\ instRead' = [instRead EXCEPT ![p] = [e \in Entries |-> None]]
  /\ latRead' = [latRead EXCEPT ![p] = [e \in Entries |-> None]]
  /\ verdictOf' = [verdictOf EXCEPT ![p] = [e \in Entries |-> "none"]]
  /\ UNCHANGED <<world, dryRun, named, fileOf>>

\* Deliver: the bus said `SEND OK id=<id> pushed=true` for this report, so
\* the state file records the delivery SENT and nothing is pending
\* (report.go `deliver`, `sendOK`; SPEC-UPDATE rule 24).
DeliverOK(p) ==
  /\ phase[p] = "written"
  /\ pending
  /\ delivered' = TRUE
  /\ sentOK' = TRUE
  /\ pending' = FALSE
  /\ phase' = [phase EXCEPT ![p] = "idle"]
  /\ lockBy' = IF lockBy = p THEN None ELSE lockBy
  /\ UNCHANGED <<world, instRead, latRead, verdictOf, dryRun, named,
                 stateObserved, changedSet, changedAgrees, faults>>

\* Deliver refused or failed: the bus's own line is relayed, the report exits
\* 1, and the pending report stays in the state file for the next --send
\* (report.go `busSaid`).
DeliverRefused(p) ==
  /\ phase[p] = "written"
  /\ pending
  /\ faults < MaxFaults
  /\ faults' = faults + 1
  /\ delivered' = (delivered \/ Broken = "sentonrefusal")
  /\ sentOK' = (sentOK /\ Broken # "sentonrefusal")
  /\ phase' = [phase EXCEPT ![p] = "idle"]
  /\ lockBy' = IF lockBy = p THEN None ELSE lockBy
  /\ UNCHANGED <<world, instRead, latRead, verdictOf, dryRun, named,
                 stateObserved, changedSet, changedAgrees, pending>>

\* An apply invocation names one entry, from a person, with or without
\* --dry-run (SPEC-UPDATE rules 10 and 13a).
StartApply(p, e) ==
  /\ phase[p] = "idle"
  /\ faults < MaxFaults
  /\ faults' = faults + 1
  /\ phase' = [phase EXCEPT ![p] = "named"]
  /\ named' = [named EXCEPT ![p] = {e}]
  /\ \E d \in BOOLEAN : dryRun' = [dryRun EXCEPT ![p] = d]
  /\ UNCHANGED <<world, instRead, latRead, verdictOf, lockBy, fileOf>>

\* Where an install process may start: for the entry the invocation named and
\* never under --dry-run, which prints the plan and starts no process
\* (cli.go `apply`, `applyDryRun`). A witness removes one of those guards.
InstallStarts(p, e) ==
  \/ phase[p] = "named" /\ e \in named[p] /\ ~dryRun[p]
  \/ Broken = "installonverdict" /\ verdictOf[p][e] = "STALE" /\ ~dryRun[p]
  \/ Broken = "dryruninstalls" /\ phase[p] = "named" /\ e \in named[p] /\ dryRun[p]

\* Apply: the entry's apply argv runs for the target version, then installed
\* is read again, and after must equal the target (SPEC-UPDATE rules 13 and
\* 14). The install is the world's to land, so the version it leaves behind
\* is chosen here -- and the replace has a crash window: where the rename is
\* refused over a running image, the running file is moved aside and the new
\* binary is renamed in, and a crash between the move aside and the rename
\* leaves no binary at the entry's path, which its next read answers UNKNOWN,
\* never a version (SPEC-UPDATE rules 7 and 22).
ApplyInstall(p, e) ==
  /\ InstallStarts(p, e)
  /\ installs' = [installs EXCEPT ![e] =
       IF e \in named[p] /\ ~dryRun[p] THEN "named"
       ELSE IF dryRun[p] THEN "dryrun" ELSE "unnamed"]
  /\ applyRan' = [applyRan EXCEPT ![e] = TRUE]
  /\ \/ \* the replace completes: the version it leaves is the world's
        /\ faults' = faults
        /\ aside' = [aside EXCEPT ![e] = FALSE]
        /\ \E v \in 0..MaxVer :
             /\ installed' = [installed EXCEPT ![e] = v]
             /\ afterVer' = v
     \/ \* the crash window: the process dies between the move aside and the
        \* rename; the witness "asideisversion" lets the window read as the
        \* install having landed
        /\ faults < MaxFaults
        /\ faults' = faults + 1
        /\ aside' = [aside EXCEPT ![e] = TRUE]
        /\ \E v \in Worlds :
             installed' = [installed EXCEPT ![e] =
                 IF Broken = "asideisversion" THEN v ELSE Unknown]
        /\ afterVer' = afterVer
  /\ phase' = [phase EXCEPT ![p] = IF phase[p] = "named" THEN "idle" ELSE phase[p]]
  /\ named' = [named EXCEPT ![p] = IF phase[p] = "named" THEN {} ELSE named[p]]
  /\ dryRun' = [dryRun EXCEPT ![p] = IF phase[p] = "named" THEN FALSE ELSE dryRun[p]]
  /\ UNCHANGED <<instRead, latRead, verdictOf, lockBy, fileOf>>

\* Apply under --dry-run: the plan the real run would take is printed and no
\* process starts, so the invocation ends having changed nothing
\* (cli.go `applyDryRun`; SPEC-UPDATE rule 13a).
ApplyPlan(p, e) ==
  /\ phase[p] = "named"
  /\ e \in named[p]
  /\ dryRun[p]
  /\ Broken # "dryruninstalls"
  /\ phase' = [phase EXCEPT ![p] = "idle"]
  /\ named' = [named EXCEPT ![p] = {}]
  /\ dryRun' = [dryRun EXCEPT ![p] = FALSE]
  /\ UNCHANGED <<world, instRead, latRead, verdictOf, lockBy, fileOf, faults>>

Next ==
  \/ \E e \in Entries : OutsideChange(e)
  \/ \E p \in Updaters :
       StartRun(p) \/ WriteState(p) \/ BusyRefused(p)
       \/ Crash(p) \/ DeliverOK(p) \/ DeliverRefused(p)
  \/ \E p \in Updaters, e \in Entries :
       Read(p, e) \/ Verdict(p, e) \/ StartApply(p, e)
       \/ ApplyInstall(p, e) \/ ApplyPlan(p, e)

\* Fairness is the bounded invocation: a run reads its entries and writes its
\* state file, a pending report is sent, a write that waits on the lock is
\* refused busy, and an apply installs or prints its plan, because every
\* child here is bounded in time (SPEC-UPDATE rules 8 and 23).
Spec == Init /\ [][Next]_vars
        /\ \A p \in Updaters :
             /\ WF_vars(StartRun(p)) /\ WF_vars(WriteState(p))
             /\ WF_vars(BusyRefused(p)) /\ WF_vars(DeliverOK(p))
         /\ \A q \in Updaters, e \in Entries :
              /\ WF_vars(Read(q, e))
              /\ WF_vars(ApplyInstall(q, e))
              /\ WF_vars(ApplyPlan(q, e))

----------------------------------------------------------------------------
\* The invariants.

\* An install process starts only for the one entry the invocation named,
\* never on a verdict: the module never discovers homes or installs on a
\* verdict (manifest.go's package comment; SPEC-UPDATE rules 9 and 10).
ApplyOnlyWhatIsNamed == \A e \in Entries : installs[e] # "unnamed"

\* Under --dry-run no entry's installed version changes: the plan is printed
\* and no process starts (cli.go `applyDryRun`; SPEC-UPDATE rule 13a).
DryRunInstallsNothing == \A e \in Entries : installs[e] # "dryrun"

\* A verdict exists only after both reads of its entry in this run, and a
\* read that did not answer is UNKNOWN, never a version (cli.go `verdict`;
\* SPEC-UPDATE rule 7).
VerdictFromReads ==
  \A p \in Updaters, e \in Entries :
    verdictOf[p][e] # "none" => instRead[p][e] # None /\ latRead[p][e] # None

\* The state file is written only by the updater that holds its lock: the
\* lock serializes writers across the atomic renames, so a second writer
\* waits and is refused busy, and a run whose write is in flight or whose
\* report is still pending is the one that holds it (SPEC-UPDATE rule 25;
\* snapshot_unix.go `lockSnapshot`).
WriterHoldsTheLock == \A p \in Updaters : phase[p] = "written" => lockBy = p

\* The state file holds only versions read in a run that completed its reads:
\* the write follows the reads of every entry (report.go; SPEC-UPDATE
\* rule 25).
StateWrittenAfterRead ==
  \A p \in Updaters :
    phase[p] = "written" => \A e \in Entries :
      stateObserved[e] = instRead[p][e] /\ instRead[p][e] # None

\* A delivery is recorded SENT only after the bus's SEND OK: an unconfirmed
\* send is uncertain and stays pending (report.go `deliver`, `sendOK`;
\* SPEC-UPDATE rule 24).
SentOnlyOnOK == delivered => sentOK

\* CHANGED names exactly the entries whose read differs from the state file,
\* the first run's baseline included (report.go; SPEC-UPDATE rule 25).
ChangedIsAgainstState == changedAgrees

\* A binary the crashed replace left aside is no version: the path holds no
\* binary, and what the entry's installed argv reads next is UNKNOWN, never
\* the version the install was on its way to leave (SPEC-UPDATE rules 7 and
\* 22).
AsideIsUnknown == \A e \in Entries : aside[e] => installed[e] = Unknown

\* Liveness: a report that crashed between the write and the send is
\* delivered by a later run, because the pending report survives in the state
\* file and only a confirmed send clears it (report.go `deliver`).
CrashedReportDelivers == pending ~> ~pending
=============================================================================
