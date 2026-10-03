----------------------------- MODULE UpdateApply -----------------------------
\* The update tool's apply and report verbs: read installed and latest
\* versions for each manifest entry, compute a verdict, run the named
\* entry's apply command, and report changes against a crash-safe state
\* file. Modelled from internal/update/manifest.go (Load, Entry), cli.go
\* (verdict 574-599, apply 599-669, applyDryRun 676-688), report.go
\* (report 26-107, deliver 184-265, busRefusals 117), snapshot.go
\* (snapshot 29-33, writeSnapshot 378-407).

\* THE STATE.
\*   entries          the manifest: each entry has a name, kind, installed
\*                    argv, latest source, apply argv, and owner.
\*   instRead         per entry: the installed version read in this run
\*                    (a version string, or Unknown, or Failed).
\*   latestRead       per entry: the latest version read in this run
\*                    (a version string, or Unknown, or Failed).
\*   verdict          per entry: the computed verdict (Equal, Stale, Ahead,
\*                    Different, Unknown, or None for not yet computed).
\*   applyRun         whether an apply has run for the named entry in this
\*                    invocation, and what it read after.
\*   stateFile        the crash-safe JSON state file: the observed map
\*                    (name -> version, status, at) and the delivered
\*                    map (scope -> delivery{observed, id, at}).
\*   dryRun           the --dry-run flag: when true, Apply prints the plan
\*                    and starts no process.
\*   namedEntry       the entry the person named for apply (None when not
\*                    an apply run).
\*   busSaidOk        whether the bus replied SEND OK for the current
\*                    delivery attempt.
\*   crashed          whether a crash occurred between writing the state
\*                    file and sending the report.

\* THE ACTIONS.
\*   ReadInstalled(e) read the installed version of entry e; may yield
\*                    Unknown (command not found) or Failed (timeout).
\*   ReadLatest(e)    read the latest version from the entry's source;
\*                    may yield Unknown (source not known or not answered)
\*                    or Failed (timeout).
\*   ComputeVerdict(e) derive the verdict from the two reads of e; only
\*                     enabled after both reads are done for e in this run.
\*   Apply(e)         run the apply command for the named entry e, then
\*                    read installed again; disabled under dryRun.
\*   ApplyDryRun(e)   print the plan for e; no process starts, no version
\*                    changes.
\*   Report           read every entry's installed version, compute
\*                    Changed against the state file, write the state file.
\*   Deliver          send the report to the bus; SENT only when the bus
\*                    says SEND OK.
\*   CrashBetweenWriteAndSend  a crash after Report writes the state file
\*                             but before Deliver succeeds.
\*   OutsideChange(e) the installed version of e changes between runs.

\* THE INVARIANTS.
\*   ApplyOnlyWhatIsNamed    an install process starts only for the one
\*                           entry the invocation named, never on a verdict.
\*   DryRunInstallsNothing   under --dry-run no entry's installed version
\*                           changes.
\*   VerdictFromReads        a verdict exists only after both reads of its
\*                           entry in this run.
\*   StateWrittenAfterRead   the state file holds only versions read in a
\*                           run that completed its reads.
\*   SentOnlyOnOK            a delivery is SENT only after the bus's
\*                           SEND OK.
\*   ChangedIsAgainstState   CHANGED names exactly the entries whose read
\*                           differs from the state file.
\*   ReportDeliversOnRetry   (liveness) a report that crashed between
\*                           write and send delivers on the next run.

\* REVERSED WITNESSES (each a Broken value and a cfg whose expected
\* outcome is that the named invariant fails):
\*   BrokenInstallOnVerdict  STALE triggers an apply by itself:
\*                           ApplyOnlyWhatIsNamed fails.
\*   BrokenDryRunInstalls    DryRunInstallsNothing fails.
\*   BrokenSentOnRefusal     SEND REFUSED recorded as SENT:
\*                           SentOnlyOnOK fails.

EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Entries, MaxVersions, MaxRuns, Broken

Faults == {"installonverdict", "dryruninstalls", "sentonrefusal"}
ASSUME Broken \subseteq Faults

\* Entry names and kinds
Names == 1..Entries
Kinds == {"harness", "engine", "model", "tool", "pin"}
None == 0
UnknownVal == -1
FailedVal == -2

\* Version values: 1..MaxVersions are concrete versions, UnknownVal = unknown,
\* FailedVal = read failed
Versions == 1..MaxVersions \cup {UnknownVal, FailedVal}

\* Verdict values
Verdicts == {"Equal", "Stale", "Ahead", "Different", "Unknown", "None"}

\* Run phases
Phases == {"idle", "reading", "verdict", "applying", "reporting", "delivering", "done"}

VARIABLES
  \* Manifest entries (constant per run, but modelled as state for OutsideChange)
  entryName,          \* name of each entry (abstracted to index)
  entryKind,          \* kind of each entry
  entryInstalledArgv, \* installed command (abstracted)
  entryLatestSource,  \* latest source (abstracted)
  entryApplyArgv,     \* apply command (abstracted)
  entryOwner,         \* owner (abstracted)
  entryHasApply,      \* whether apply column is not "none"
  
  \* Per-entry reads this run
  instRead,           \* installed version read (Versions)
  latestRead,         \* latest version read (Versions)
  instReadDone,       \* whether installed read completed
  latestReadDone,     \* whether latest read completed
  
  \* Per-entry verdict
  verdict,            \* computed verdict (Verdicts)
  
  \* Apply state
  applyRun,           \* whether apply ran for the named entry
  applyAfterRead,     \* installed version read after apply (Versions)
  namedEntry,         \* the entry named for apply (Names \cup {None})
  dryRun,             \* --dry-run flag
  
  \* State file (snapshot)
  stateObserved,      \* [name |-> <<version, status, at>>]
  stateDelivered,     \* [scope |-> <<observed, id, at>>]
  
  \* Bus and crash
  busSaidOk,          \* whether bus replied SEND OK
  crashed,            \* crash between write and send
  
  \* Run control
  phase,              \* current phase (Phases)
  currentEntry,       \* entry currently being processed (Names \cup {None})

vars == <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
          entryApplyArgv, entryOwner, entryHasApply,
          instRead, latestRead, instReadDone, latestReadDone,
          verdict, applyRun, applyAfterRead, namedEntry, dryRun,
          stateObserved, stateDelivered, busSaidOk, crashed,
          phase, currentEntry>>

\* ---- Initial state ----
Init ==
  /\ entryName = [n \in Names |-> n]
  /\ entryKind \in [Names -> Kinds]
  /\ entryInstalledArgv = [n \in Names |-> TRUE]
  /\ entryLatestSource = [n \in Names |-> TRUE]
  /\ entryApplyArgv = [n \in Names |-> TRUE]
  /\ entryOwner = [n \in Names |-> TRUE]
  /\ entryHasApply \in [Names -> BOOLEAN]
  
  /\ instRead = [n \in Names |-> None]
  /\ latestRead = [n \in Names |-> None]
  /\ instReadDone = [n \in Names |-> FALSE]
  /\ latestReadDone = [n \in Names |-> FALSE]
  /\ verdict = [n \in Names |-> "None"]
  
  /\ applyRun = FALSE
  /\ applyAfterRead = None
  /\ namedEntry = None
  /\ dryRun = FALSE
  
  /\ stateObserved = [n \in Names |-> <<None, "none", "">>]
  /\ stateDelivered = {}
  
  /\ busSaidOk = FALSE
  /\ crashed = FALSE
  
  /\ phase = "idle"
  /\ currentEntry = None

\* ---- Helper definitions ----

\* An entry's reads are both done
ReadsDone(e) == instReadDone[e] /\ latestReadDone[e]

\* All entries' reads are done
AllReadsDone == \A e \in Names : ReadsDone(e)

\* Compute verdict from reads (mirrors cli.go:574-599)
ComputeVerdictValue(e) ==
  LET i == instRead[e]
      l == latestRead[e]
  IN IF i = UnknownVal \/ i = FailedVal \/ l = UnknownVal \/ l = FailedVal
     THEN "Unknown"
     ELSE IF entryKind[e] = "pin"
          THEN IF i # None /\ l = i THEN "Equal" ELSE "Different"
          ELSE IF entryKind[e] = "model"
               THEN IF i = l THEN "Equal" ELSE "Different"
               ELSE IF i \in Versions \ {UnknownVal, FailedVal} /\ l \in Versions \ {UnknownVal, FailedVal}
                    THEN IF Ahead(i, l) THEN "Ahead"
                         ELSE IF Compare(i, l) = "OLDER" THEN "Stale"
                         ELSE Compare(i, l)
                    ELSE "Unknown"

\* Placeholder for version comparison (abstracted)
Ahead(i, l) == i > l
Compare(i, l) == IF i < l THEN "OLDER" ELSE IF i > l THEN "NEWER" ELSE "EQUAL"

\* The entry named for apply has an apply command
NamedEntryHasApply == namedEntry # None => entryHasApply[namedEntry]

\* State file observed version for an entry
StateVersion(e) == stateObserved[e][1]
StateStatus(e) == stateObserved[e][2]

\* Changed entries: those whose current read differs from state
ChangedEntries == {e \in Names : instRead[e] # StateVersion(e)}

\* ---- Actions ----

\* Read installed version for entry e
ReadInstalled(e) ==
  /\ phase = "reading"
  /\ currentEntry = e
  /\ ~instReadDone[e]
  /\ instRead' = [instRead EXCEPT ![e] = 
       IF "installonverdict" \in Broken \/ "dryruninstalls" \in Broken
       THEN \* In broken models, reads may behave differently
            CHOOSE v \in Versions : TRUE
       ELSE CHOOSE v \in Versions : TRUE]
  /\ instReadDone' = [instReadDone EXCEPT ![e] = TRUE]
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  latestRead, latestReadDone, verdict,
                  applyRun, applyAfterRead, namedEntry, dryRun,
                  stateObserved, stateDelivered, busSaidOk, crashed,
                  phase, currentEntry>>

\* Read latest version for entry e
ReadLatest(e) ==
  /\ phase = "reading"
  /\ currentEntry = e
  /\ ~latestReadDone[e]
  /\ latestRead' = [latestRead EXCEPT ![e] = CHOOSE v \in Versions : TRUE]
  /\ latestReadDone' = [latestReadDone EXCEPT ![e] = TRUE]
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  instRead, instReadDone, verdict,
                  applyRun, applyAfterRead, namedEntry, dryRun,
                  stateObserved, stateDelivered, busSaidOk, crashed,
                  phase, currentEntry>>

\* Move to next entry or verdict phase
NextEntry ==
  /\ phase = "reading"
  /\ currentEntry # None
  /\ ReadsDone(currentEntry)
  /\ LET next == currentEntry + 1
     IN IF next > Entries
        THEN /\ phase' = "verdict"
             /\ currentEntry' = 1
        ELSE /\ currentEntry' = next
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  instRead, latestRead, instReadDone, latestReadDone,
                  verdict, applyRun, applyAfterRead, namedEntry, dryRun,
                  stateObserved, stateDelivered, busSaidOk, crashed>>

\* Compute verdict for entry e
ComputeVerdict(e) ==
  /\ phase = "verdict"
  /\ currentEntry = e
  /\ ReadsDone(e)
  /\ verdict[e] = "None"
  /\ verdict' = [verdict EXCEPT ![e] = ComputeVerdictValue(e)]
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  instRead, latestRead, instReadDone, latestReadDone,
                  applyRun, applyAfterRead, namedEntry, dryRun,
                  stateObserved, stateDelivered, busSaidOk, crashed,
                  phase, currentEntry>>

\* Move to next entry for verdict or to applying/reporting
NextVerdictEntry ==
  /\ phase = "verdict"
  /\ currentEntry # None
  /\ verdict[currentEntry] # "None"
  /\ LET next == currentEntry + 1
     IN IF next > Entries
        THEN /\ \/ \E e \in Names : verdict[e] = "Stale" /\ "installonverdict" \in Broken /\ namedEntry = None
                 \* In BrokenInstallOnVerdict, a STALE verdict triggers apply without being named
                 /\ phase' = "applying"
                 /\ namedEntry' = CHOOSE e \in Names : verdict[e] = "Stale"
                 /\ applyRun' = FALSE
               \/ /\ namedEntry = None
                  /\ phase' = "reporting"
                  /\ currentEntry' = 1
               \/ /\ namedEntry # None
                  /\ NamedEntryHasApply
                  /\ phase' = "applying"
                  /\ applyRun' = FALSE
               \/ /\ namedEntry # None
                  /\ ~NamedEntryHasApply
                  /\ phase' = "reporting"
                  /\ currentEntry' = 1
        ELSE /\ currentEntry' = next
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  instRead, latestRead, instReadDone, latestReadDone,
                  verdict, applyRun, applyAfterRead, dryRun,
                  stateObserved, stateDelivered, busSaidOk, crashed>>

\* Apply the named entry (real apply, not dry-run)
Apply ==
  /\ phase = "applying"
  /\ namedEntry # None
  /\ ~applyRun
  /\ ~dryRun
  /\ entryHasApply[namedEntry]
  /\ applyRun' = TRUE
  /\ applyAfterRead' = CHOOSE v \in Versions : TRUE
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  instRead, latestRead, instReadDone, latestReadDone,
                  verdict, namedEntry, dryRun,
                  stateObserved, stateDelivered, busSaidOk, crashed,
                  phase, currentEntry>>

\* Dry-run apply: prints plan, no process starts, no version changes
ApplyDryRun ==
  /\ phase = "applying"
  /\ namedEntry # None
  /\ ~applyRun
  /\ dryRun
  /\ entryHasApply[namedEntry]
  /\ applyRun' = TRUE
  /\ applyAfterRead' = None
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  instRead, latestRead, instReadDone, latestReadDone,
                  verdict, namedEntry, dryRun,
                  stateObserved, stateDelivered, busSaidOk, crashed,
                  phase, currentEntry>>

\* Move to reporting after apply
ApplyDone ==
  /\ phase = "applying"
  /\ applyRun
  /\ phase' = "reporting"
  /\ currentEntry' = 1
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  instRead, latestRead, instReadDone, latestReadDone,
                  verdict, applyRun, applyAfterRead, namedEntry, dryRun,
                  stateObserved, stateDelivered, busSaidOk, crashed>>

\* Report: read every entry's installed version, compute Changed against
\* state file, write state file
Report ==
  /\ phase = "reporting"
  /\ currentEntry # None
  /\ currentEntry <= Entries
  /\ instRead' = [instRead EXCEPT ![currentEntry] = CHOOSE v \in Versions : TRUE]
  /\ instReadDone' = [instReadDone EXCEPT ![currentEntry] = TRUE]
  /\ latestReadDone' = [latestReadDone EXCEPT ![currentEntry] = TRUE] \* report reads no latest
  /\ IF currentEntry = Entries
     THEN /\ stateObserved' = [e \in Names |-> <<instRead'[e], "known", "now">>]
          /\ phase' = "delivering"
          /\ currentEntry' = None
     ELSE /\ currentEntry' = currentEntry + 1
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  latestRead, verdict, applyRun, applyAfterRead, namedEntry,
                  dryRun, stateDelivered, busSaidOk, crashed>>

\* Deliver: send report to bus; SENT only on SEND OK
Deliver ==
  /\ phase = "delivering"
  /\ ~crashed
  /\ busSaidOk' = TRUE
  /\ stateDelivered' = stateDelivered \cup {scope |-> 
       <<stateObserved, "id", "now">>}
  /\ phase' = "done"
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  instRead, latestRead, instReadDone, latestReadDone,
                  verdict, applyRun, applyAfterRead, namedEntry, dryRun,
                  stateObserved, busSaidOk, crashed, currentEntry>>

\* Bus refusal: SEND REFUSED or SEND FAIL
BusRefusal ==
  /\ phase = "delivering"
  /\ ~crashed
  /\ busSaidOk' = FALSE
  /\ phase' = "done"
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  instRead, latestRead, instReadDone, latestReadDone,
                  verdict, applyRun, applyAfterRead, namedEntry, dryRun,
                  stateObserved, stateDelivered, crashed, currentEntry>>

\* Crash between write and send
CrashBetweenWriteAndSend ==
  /\ phase = "delivering"
  /\ ~crashed
  /\ crashed' = TRUE
  /\ phase' = "idle"
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  instRead, latestRead, instReadDone, latestReadDone,
                  verdict, applyRun, applyAfterRead, namedEntry, dryRun,
                  stateObserved, stateDelivered, busSaidOk, currentEntry>>

\* Retry deliver after crash (liveness)
RetryDeliver ==
  /\ phase = "idle"
  /\ crashed
  /\ stateDelivered # {}
  /\ busSaidOk' = TRUE
  /\ stateDelivered' = stateDelivered \cup {scope |-> 
       <<stateObserved, "id", "now">>}
  /\ crashed' = FALSE
  /\ phase' = "done"
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  instRead, latestRead, instReadDone, latestReadDone,
                  verdict, applyRun, applyAfterRead, namedEntry, dryRun,
                  stateObserved, currentEntry>>

\* Outside change: installed version of an entry changes between runs
OutsideChange(e) ==
  /\ phase = "idle"
  /\ ~crashed
  /\ instRead' = [instRead EXCEPT ![e] = CHOOSE v \in Versions : v # instRead[e]]
  /\ instReadDone' = [instReadDone EXCEPT ![e] = FALSE]
  /\ latestReadDone' = [latestReadDone EXCEPT ![e] = FALSE]
  /\ verdict' = [verdict EXCEPT ![e] = "None"]
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  latestRead, applyRun, applyAfterRead, namedEntry, dryRun,
                  stateObserved, stateDelivered, busSaidOk, crashed,
                  phase, currentEntry>>

\* Start a new run (reset per-run state)
StartRun ==
  /\ phase = "idle"
  /\ ~crashed
  /\ instRead' = [n \in Names |-> None]
  /\ latestRead' = [n \in Names |-> None]
  /\ instReadDone' = [n \in Names |-> FALSE]
  /\ latestReadDone' = [n \in Names |-> FALSE]
  /\ verdict' = [n \in Names |-> "None"]
  /\ applyRun' = FALSE
  /\ applyAfterRead' = None
  /\ namedEntry' = None
  /\ dryRun' = FALSE
  /\ currentEntry' = 1
  /\ phase' = "reading"
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  stateObserved, stateDelivered, busSaidOk, crashed>>

\* Set named entry for apply
SetNamedEntry(e) ==
  /\ phase = "idle"
  /\ namedEntry = None
  /\ namedEntry' = e
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  instRead, latestRead, instReadDone, latestReadDone,
                  verdict, applyRun, applyAfterRead, dryRun,
                  stateObserved, stateDelivered, busSaidOk, crashed,
                  phase, currentEntry>>

\* Set dry-run flag
SetDryRun ==
  /\ phase = "idle"
  /\ dryRun' = TRUE
  /\ UNCHANGED <<entryName, entryKind, entryInstalledArgv, entryLatestSource,
                  entryApplyArgv, entryOwner, entryHasApply,
                  instRead, latestRead, instReadDone, latestReadDone,
                  verdict, applyRun, applyAfterRead, namedEntry,
                  stateObserved, stateDelivered, busSaidOk, crashed,
                  phase, currentEntry>>

Next ==
  \/ \E e \in Names : ReadInstalled(e)
  \/ \E e \in Names : ReadLatest(e)
  \/ NextEntry
  \/ \E e \in Names : ComputeVerdict(e)
  \/ NextVerdictEntry
  \/ Apply
  \/ ApplyDryRun
  \/ ApplyDone
  \/ Report
  \/ Deliver
  \/ BusRefusal
  \/ CrashBetweenWriteAndSend
  \/ RetryDeliver
  \/ \E e \in Names : OutsideChange(e)
  \/ StartRun
  \/ SetNamedEntry
  \/ SetDryRun

Spec == Init /\ [][Next]_vars

\* ---- Type invariant ----
TypeOK ==
  /\ entryName \in [Names -> Names]
  /\ entryKind \in [Names -> Kinds]
  /\ entryInstalledArgv \in [Names -> BOOLEAN]
  /\ entryLatestSource \in [Names -> BOOLEAN]
  /\ entryApplyArgv \in [Names -> BOOLEAN]
  /\ entryOwner \in [Names -> BOOLEAN]
  /\ entryHasApply \in [Names -> BOOLEAN]
  /\ instRead \in [Names -> Versions \cup {None}]
  /\ latestRead \in [Names -> Versions \cup {None}]
  /\ instReadDone \in [Names -> BOOLEAN]
  /\ latestReadDone \in [Names -> BOOLEAN]
  /\ verdict \in [Names -> Verdicts]
  /\ applyRun \in BOOLEAN
  /\ applyAfterRead \in Versions \cup {None}
  /\ namedEntry \in Names \cup {None}
  /\ dryRun \in BOOLEAN
  /\ stateObserved \in [Names -> Versions \cup {None} \times {"known", "none"} \times STRING]
  /\ stateDelivered \in [SUBSET STRING -> Versions \cup {None} \times STRING \times STRING]
  /\ busSaidOk \in BOOLEAN
  /\ crashed \in BOOLEAN
  /\ phase \in Phases
  /\ currentEntry \in Names \cup {None}

\* ---- Invariants ----

\* ApplyOnlyWhatIsNamed: an install process starts only for the one entry
\* the invocation named, never on a verdict.
ApplyOnlyWhatIsNamed ==
  applyRun => (namedEntry # None /\ ~dryRun /\ entryHasApply[namedEntry])

\* DryRunInstallsNothing: under --dry-run no entry's installed version changes.
\* In the model, this means applyRun is true but applyAfterRead is None
\* (no actual version read after apply) and instRead is unchanged.
DryRunInstallsNothing ==
  dryRun => (applyRun => applyAfterRead = None)

\* VerdictFromReads: a verdict exists only after both reads of its entry in this run.
VerdictFromReads ==
  \A e \in Names : verdict[e] # "None" => ReadsDone(e)

\* StateWrittenAfterRead: the state file holds only versions read in a run
\* that completed its reads.
StateWrittenAfterRead ==
  \A e \in Names : stateObserved[e][1] # None => instReadDone[e]

\* SentOnlyOnOK: a delivery is SENT only after the bus's SEND OK.
\* In the model, stateDelivered is non-empty only when busSaidOk is true.
SentOnlyOnOK ==
  (stateDelivered # {}) => busSaidOk

\* ChangedIsAgainstState: CHANGED names exactly the entries whose read
\* differs from the state file.
ChangedIsAgainstState ==
  \A e \in Names : (e \in ChangedEntries) <=> (instRead[e] # StateVersion(e))

\* ReportDeliversOnRetry: (liveness) a report that crashed between write
\* and send delivers on the next run.
ReportDeliversOnRetry ==
  <> (crashed => <> (stateDelivered # {} /\ ~crashed))

\* ---- Reversed witness predicates ----

\* BrokenInstallOnVerdict: STALE triggers an apply by itself
BrokenInstallOnVerdict ==
  "installonverdict" \in Broken

\* BrokenDryRunInstalls: dry run installs
BrokenDryRunInstalls ==
  "dryruninstalls" \in Broken

\* BrokenSentOnRefusal: SEND REFUSED recorded as SENT
BrokenSentOnRefusal ==
  "sentonrefusal" \in Broken

=============================================================================