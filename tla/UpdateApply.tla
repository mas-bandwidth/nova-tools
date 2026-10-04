---------------------------- MODULE UpdateApply ----------------------------
\* internal/update: the states of one invocation of the version inventory,
\* from docs/SPEC-UPDATE.md (rules 7, 9, 10, 13, 13a, 24, 25, 26) and the
\* code those rules name: the manifest's entries (internal/update/manifest.go
\* `Load`), the verdict of one entry's two reads (internal/update/cli.go
\* `verdict`), the apply of one named entry and its dry run
\* (internal/update/cli.go `apply`, `applyDryRun`), and the report's state
\* file with the deliveries it records SENT (internal/update/report.go
\* `report`, `deliver`, `confirmed`; internal/update/snapshot.go
\* `readSnapshot`, `writeSnapshot`).
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
\* One version read is one number: `None` (this run has not read it),
\* `Unknown` (the read ran and did not answer: SPEC-UPDATE rule 7), or the
\* version; the two markers are below every version, so a read is never read
\* as one. The state file's observed set holds the same value per entry:
\* report.go keeps the raw identity beside the status, and the two move
\* together, so one value stands for both. `report` reads a latest only for a
\* `local:` source while `check` reads every one, so this module's Read,
\* which reads both, covers the reads of both verbs.
\*
\* One invocation runs at a time, and `phase` says which:
\*   idle       no invocation
\*   reading    a run is reading its entries (cli.go `readEntries`)
\*   written    the state file holds this run's reads and a report is pending
\*   crashed    the process died between the write and the send
\*   named      an apply invocation named one entry
\*
\* WHAT THE LIVENESS DOES NOT COVER. CrashedReportDelivers is proved once the
\* events that compete with a run go quiet: one counter, `faults`, bounds the
\* outside changes, the crashes, the refused deliveries and the apply
\* invocations at MaxFaults, so the property says nothing of a machine that
\* keeps changing a tool's version, crashing between the write and the send,
\* or refusing a delivery forever (Land.tla bounds its outside events the
\* same way, and its Recovers covers the same ground).
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* one guard removed, and the invariant its comment names fails:
\*   "installonverdict"  a STALE verdict installs by itself -> ApplyOnlyWhatIsNamed
\*   "dryruninstalls"    a dry run starts the install       -> DryRunInstallsNothing
\*   "sentonrefusal"     a refusal is recorded as SENT      -> SentOnlyOnOK

EXTENDS Naturals

CONSTANTS Entries, Pins, Published, MaxVer, MaxFaults, Broken

None == 0 - 1     \* this run has not read it
Unknown == 0 - 2  \* the read ran and did not answer
Vers == {None, Unknown} \cup 0..MaxVer
Verdicts == {"none", "EQUAL", "STALE", "AHEAD", "DIFFERENT", "UNKNOWN"}
Phases == {"idle", "reading", "written", "crashed", "named"}
Starts == {"none", "named", "unnamed", "dryrun"}

VARIABLES
  installed,      \* the version each entry's installed argv reads now
  instRead,       \* this run's installed read per entry
  latRead,        \* this run's latest read per entry
  verdictOf,      \* the verdict of each entry's two reads ("none": none yet)
  installs,       \* ghost: how an install process started, per entry
  applyRan,       \* whether an apply ran on the entry
  afterVer,       \* what the last apply read after its install
  dryRun,         \* the invocation's --dry-run flag
  named,          \* the entry the invocation named, as a set of at most one:
                  \* empty when no invocation named one
  phase,
  stateObserved,  \* the state file's observed set
  changedSet,     \* the CHANGED names the last run printed
  changedAgrees,  \* ghost: CHANGED named exactly the entries that moved
  delivered,      \* the state file records a delivery SENT
  sentOK,         \* ghost: the bus said SEND OK when that delivery was recorded
  pending,        \* a report is composed and held in the state file, not sent
  faults          \* the events that compete with a run so far, bounded by MaxFaults

vars == <<installed, instRead, latRead, verdictOf, installs, applyRan, afterVer,
          dryRun, named, phase, stateObserved, changedSet, changedAgrees,
          delivered, sentOK, pending, faults>>

TypeOK ==
  /\ installed \in [Entries -> 0..MaxVer]
  /\ instRead \in [Entries -> Vers]
  /\ latRead \in [Entries -> Vers]
  /\ verdictOf \in [Entries -> Verdicts]
  /\ installs \in [Entries -> Starts]
  /\ applyRan \in [Entries -> BOOLEAN]
  /\ afterVer \in Vers
  /\ dryRun \in BOOLEAN
  /\ named \in SUBSET Entries
  /\ phase \in Phases
  /\ stateObserved \in [Entries -> Vers]
  /\ changedSet \in SUBSET Entries
  /\ changedAgrees \in BOOLEAN
  /\ delivered \in BOOLEAN
  /\ sentOK \in BOOLEAN
  /\ pending \in BOOLEAN
  /\ faults \in 0..MaxFaults

Init ==
  /\ installed = [e \in Entries |-> 0]
  /\ instRead = [e \in Entries |-> None]
  /\ latRead = [e \in Entries |-> None]
  /\ verdictOf = [e \in Entries |-> "none"]
  /\ installs = [e \in Entries |-> "none"]
  /\ applyRan = [e \in Entries |-> FALSE]
  /\ afterVer = None
  /\ dryRun = FALSE
  /\ named = {}
  /\ phase = "idle"
  /\ stateObserved = [e \in Entries |-> None]
  /\ changedSet = {}
  /\ changedAgrees = TRUE
  /\ delivered = FALSE
  /\ sentOK = FALSE
  /\ pending = FALSE
  /\ faults = 0

\* An outside event: a tool's installed version changes between runs.
OutsideChange(e) ==
  /\ faults < MaxFaults
  /\ faults' = faults + 1
  /\ \E v \in 0..MaxVer : installed' = [installed EXCEPT ![e] = v]
  /\ UNCHANGED <<instRead, latRead, verdictOf, installs, applyRan, afterVer,
                 dryRun, named, phase, stateObserved, changedSet, changedAgrees,
                 delivered, sentOK, pending>>

\* A run begins: this run's reads and verdicts start empty, and a report a
\* crashed run left pending stays pending in the state file.
StartRun ==
  /\ phase \in {"idle", "crashed"}
  /\ phase' = "reading"
  /\ instRead' = [e \in Entries |-> None]
  /\ latRead' = [e \in Entries |-> None]
  /\ verdictOf' = [e \in Entries |-> "none"]
  /\ UNCHANGED <<installed, installs, applyRan, afterVer, dryRun, named,
                 stateObserved, changedSet, changedAgrees, delivered, sentOK, pending, faults>>

\* Read: installed and latest for one entry, and either read may fail, which
\* is UNKNOWN and never a version (SPEC-UPDATE rule 7).
Read(e) ==
  /\ phase = "reading"
  /\ instRead[e] = None
  /\ \E okI \in BOOLEAN, okL \in BOOLEAN :
       /\ instRead' = [instRead EXCEPT ![e] = IF okI THEN installed[e] ELSE Unknown]
       /\ latRead' = [latRead EXCEPT ![e] = IF okL THEN Published ELSE Unknown]
  /\ UNCHANGED <<installed, verdictOf, installs, applyRan, afterVer, dryRun,
                 named, phase, stateObserved, changedSet, changedAgrees,
                 delivered, sentOK, pending, faults>>

\* Verdict: from the two reads of one entry, and only once this run has both
\* (cli.go `verdict`).
Verdict(e) ==
  /\ phase = "reading"
  /\ verdictOf[e] = "none"
  /\ instRead[e] # None
  /\ latRead[e] # None
  /\ verdictOf' = [verdictOf EXCEPT ![e] =
       IF instRead[e] = Unknown \/ latRead[e] = Unknown
         THEN "UNKNOWN"
         ELSE IF e \in Pins
           THEN IF instRead[e] = latRead[e] THEN "EQUAL" ELSE "DIFFERENT"
           ELSE IF instRead[e] > latRead[e] THEN "AHEAD"
           ELSE IF instRead[e] < latRead[e] THEN "STALE"
           ELSE "EQUAL"]
  /\ UNCHANGED <<installed, instRead, latRead, installs, applyRan, afterVer,
                 dryRun, named, phase, stateObserved, changedSet, changedAgrees,
                 delivered, sentOK, pending, faults>>

\* Report: every entry read, CHANGED computed against the state file as it
\* stood, the state file written with what this run read, and, under --send
\* alone, the report composed and held pending in that file (SPEC-UPDATE
\* rules 24 and 25; report.go writes it through snapshot.go's rename).
WriteState ==
  /\ phase = "reading"
  /\ \A e \in Entries : instRead[e] # None /\ latRead[e] # None
  /\ changedSet' = {e \in Entries : stateObserved[e] # instRead[e]}
  /\ changedAgrees' = \A e \in Entries : (e \in changedSet') = (stateObserved[e] # instRead[e])
  /\ stateObserved' = instRead
  /\ \E sends \in BOOLEAN :
       /\ pending' = (pending \/ sends)
       /\ phase' = IF pending \/ sends THEN "written" ELSE "idle"
  /\ UNCHANGED <<installed, instRead, latRead, verdictOf, installs, applyRan,
                 afterVer, dryRun, named, delivered, sentOK, faults>>

\* CrashBetweenWriteAndSend: the process dies once the state file is renamed
\* and before the send, so the file keeps the run's reads and its pending
\* report and the run's own memory is gone.
Crash ==
  /\ phase = "written"
  /\ faults < MaxFaults
  /\ faults' = faults + 1
  /\ phase' = "crashed"
  /\ instRead' = [e \in Entries |-> None]
  /\ latRead' = [e \in Entries |-> None]
  /\ verdictOf' = [e \in Entries |-> "none"]
  /\ UNCHANGED <<installed, installs, applyRan, afterVer, dryRun, named,
                 stateObserved, changedSet, changedAgrees, delivered, sentOK, pending>>

\* Deliver: the bus said `SEND OK id=<id> pushed=true` for this report, so
\* the state file records the delivery SENT and nothing is pending
\* (report.go `deliver`, `confirmed`; SPEC-UPDATE rule 24).
DeliverOK ==
  /\ phase = "written"
  /\ pending
  /\ delivered' = TRUE
  /\ sentOK' = TRUE
  /\ pending' = FALSE
  /\ phase' = "idle"
  /\ UNCHANGED <<installed, instRead, latRead, verdictOf, installs, applyRan,
                 afterVer, dryRun, named, stateObserved, changedSet, changedAgrees, faults>>

\* Deliver refused or failed: the bus's own line is relayed, the report exits
\* 1, and the pending report stays in the state file for the next --send
\* (report.go `busSaid`).
DeliverRefused ==
  /\ phase = "written"
  /\ pending
  /\ faults < MaxFaults
  /\ faults' = faults + 1
  /\ delivered' = (delivered \/ Broken = "sentonrefusal")
  /\ sentOK' = (sentOK /\ Broken # "sentonrefusal")
  /\ phase' = "idle"
  /\ UNCHANGED <<installed, instRead, latRead, verdictOf, installs, applyRan,
                 afterVer, dryRun, named, stateObserved, changedSet, changedAgrees, pending>>

\* An apply invocation names one entry, from a person, with or without
\* --dry-run (SPEC-UPDATE rules 10 and 13a).
StartApply(e) ==
  /\ phase = "idle"
  /\ faults < MaxFaults
  /\ faults' = faults + 1
  /\ phase' = "named"
  /\ named' = {e}
  /\ \E d \in BOOLEAN : dryRun' = d
  /\ UNCHANGED <<installed, instRead, latRead, verdictOf, installs, applyRan,
                 afterVer, stateObserved, changedSet, changedAgrees,
                 delivered, sentOK, pending>>

\* Where an install process may start: for the entry the invocation named and
\* never under --dry-run, which prints the plan and starts no process
\* (cli.go `apply`, `applyDryRun`). A witness removes one of those guards.
InstallStarts(e) ==
  \/ phase = "named" /\ e \in named /\ ~dryRun
  \/ Broken = "installonverdict" /\ verdictOf[e] = "STALE" /\ ~dryRun
  \/ Broken = "dryruninstalls" /\ phase = "named" /\ e \in named /\ dryRun

\* Apply: the entry's apply argv runs for the target version, then installed
\* is read again, and after must equal the target (SPEC-UPDATE rules 13 and
\* 14). The install is the world's to land, so the version it leaves behind
\* is chosen here.
ApplyInstall(e) ==
  /\ InstallStarts(e)
  /\ installs' = [installs EXCEPT ![e] =
       IF e \in named /\ ~dryRun THEN "named" ELSE IF dryRun THEN "dryrun" ELSE "unnamed"]
  /\ applyRan' = [applyRan EXCEPT ![e] = TRUE]
  /\ \E v \in 0..MaxVer :
       /\ installed' = [installed EXCEPT ![e] = v]
       /\ afterVer' = v
  /\ phase' = IF phase = "named" THEN "idle" ELSE phase
  /\ named' = IF phase = "named" THEN {} ELSE named
  /\ dryRun' = IF phase = "named" THEN FALSE ELSE dryRun
  /\ UNCHANGED <<instRead, latRead, verdictOf, stateObserved, changedSet,
                 changedAgrees, delivered, sentOK, pending, faults>>

\* Apply under --dry-run: the plan the real run would take is printed and no
\* process starts, so the invocation ends having changed nothing
\* (cli.go `applyDryRun`; SPEC-UPDATE rule 13a).
ApplyPlan(e) ==
  /\ phase = "named"
  /\ e \in named
  /\ dryRun
  /\ Broken # "dryruninstalls"
  /\ phase' = "idle"
  /\ named' = {}
  /\ dryRun' = FALSE
  /\ UNCHANGED <<installed, instRead, latRead, verdictOf, installs, applyRan,
                 afterVer, stateObserved, changedSet, changedAgrees,
                 delivered, sentOK, pending, faults>>

Next ==
  \/ \E e \in Entries : OutsideChange(e) \/ Read(e) \/ Verdict(e)
                        \/ StartApply(e) \/ ApplyInstall(e) \/ ApplyPlan(e)
  \/ StartRun \/ WriteState \/ Crash \/ DeliverOK \/ DeliverRefused

\* Fairness is the bounded invocation: a run reads its entries and writes its
\* state file, a pending report is sent, and an apply installs or prints its
\* plan, because every child here is bounded in time (SPEC-UPDATE rules 8
\* and 23).
Spec == Init /\ [][Next]_vars
        /\ WF_vars(StartRun) /\ WF_vars(WriteState) /\ WF_vars(DeliverOK)
        /\ \A e \in Entries : /\ WF_vars(Read(e))
                              /\ WF_vars(ApplyInstall(e))
                              /\ WF_vars(ApplyPlan(e))

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
  \A e \in Entries : verdictOf[e] # "none" => instRead[e] # None /\ latRead[e] # None

\* The state file holds only versions read in a run that completed its reads:
\* the write follows the reads of every entry (report.go; SPEC-UPDATE
\* rule 25).
StateWrittenAfterRead ==
  phase = "written" => \A e \in Entries : stateObserved[e] = instRead[e] /\ instRead[e] # None

\* A delivery is recorded SENT only after the bus's SEND OK: an unconfirmed
\* send is uncertain and stays pending (report.go `deliver`, `confirmed`;
\* SPEC-UPDATE rule 24).
SentOnlyOnOK == delivered => sentOK

\* CHANGED names exactly the entries whose read differs from the state file,
\* the first run's baseline included (report.go; SPEC-UPDATE rule 25).
ChangedIsAgainstState == changedAgrees

\* Liveness: a report that crashed between the write and the send is
\* delivered by a later run, because the pending report survives in the state
\* file and only a confirmed send clears it (report.go `deliver`).
CrashedReportDelivers == pending ~> ~pending
=============================================================================
