----------------------------- MODULE UpdateApply -----------------------------
\* TLA+ model of the update state machine (internal/update: manifest.go, cli.go, report.go, snapshot.go, read.go, latest.go) and docs/SPEC-UPDATE.md rules 9-14,20-26. The manifest's entries (name, kind, how installed is read, where latest is published, the apply command); per entry the installed version read, the latest read, the verdict (EQUAL, STALE, AHEAD, DIFFERENT, UNKNOWN), whether an apply ran and what it read after; the report's state file holding the observed set and the deliveries SENT; the dry-run flag. ACTIONS: Read (installed and latest for one entry; a read may fail: UNKNOWN); Verdict (from the two reads); Apply (only on an entry a person named: runs the entry's command, then reads installed again; --dry-run prints the plan and starts no install process); Report (reads every entry, computes CHANGED against the state file, writes the state file); Deliver (sends the report to the bus; SENT only when the bus said SEND OK); CrashBetweenWriteAndSend; OutsideChange (a tool's installed version changes between runs). INVARIANTS: ApplyOnlyWhatIsNamed: an install process starts only for the one entry the invocation named, never on a verdict; DryRunInstallsNothing: under --dry-run no entry's installed version changes; VerdictFromReads: a verdict exists only after both reads of its entry in this run; StateWrittenAfterRead: the state file holds only versions read in a run that completed its reads; SentOnlyOnOK: a delivery is SENT only after the bus's SEND OK; ChangedIsAgainstState: CHANGED names exactly the entries whose read differs from the state file; liveness: a report that crashed between write and send delivers on the next run. Libraries considered: the TLA+ standard modules the jar bundles (Naturals, Sequences, FiniteSets, TLC) for the module; tools/tlacheck and internal/tlc, in the tree, for the run and the records; no Go helper is written.
EXTENDS Naturals, Sequences, FiniteSets, TLC

CONSTANTS Entries, Kinds, MaxVersions, Broken
ASSUME Entries # {} /\ Kinds # {} /\ MaxVersions \in Nat /\ MaxVersions > 0

None == ""
ReadResult == (0..MaxVersions) \cup {None}
VerdictSet == {"EQUAL", "STALE", "AHEAD", "DIFFERENT", "UNKNOWN"}
Faults == {"installonverdict", "dryruninstalls", "sentonrefusal"}
ASSUME Broken \subseteq Faults

VARIABLES installed, latest, verdict, applied, after, observed, sentDeliveries, dryRun, crashed, outsideChanged, entryKind, entryHasApply

vars == <<installed, latest, verdict, applied, after, observed, sentDeliveries, dryRun, crashed, outsideChanged, entryKind, entryHasApply>>

Init ==
  /\ entryKind = [e \in Entries |-> CHOOSE k \in Kinds : TRUE]
  /\ entryHasApply = [e \in Entries |-> TRUE]
  /\ installed = [e \in Entries |-> None]
  /\ latest = [e \in Entries |-> None]
  /\ verdict = [e \in Entries |-> ""]
  /\ applied = [e \in Entries |-> FALSE]
  /\ after = [e \in Entries |-> None]
  /\ observed = [e \in Entries |-> None]
  /\ sentDeliveries = [e \in Entries |-> FALSE]
  /\ dryRun = FALSE
  /\ crashed = FALSE
  /\ outsideChanged = FALSE

Read(e, which, res) ==
  /\ e \in Entries
  /\ which \in {"installed", "latest"}
  /\ res \in ReadResult
  /\ (which = "installed" => installed' = [installed EXCEPT ![e] = res])
  /\ (which = "latest" => latest' = [latest EXCEPT ![e] = res])
  /\ UNCHANGED <<verdict, applied, after, observed, sentDeliveries, dryRun, crashed, outsideChanged, entryKind, entryHasApply>>

Verdict(e) ==
  /\ e \in Entries
  /\ installed[e] # None
  /\ latest[e] # None
  /\ verdict' = [verdict EXCEPT ![e] =
       IF installed[e] = latest[e] THEN "EQUAL"
       ELSE IF installed[e] < latest[e] THEN "STALE" ELSE "AHEAD"]
  /\ UNCHANGED <<installed, latest, applied, after, observed, sentDeliveries, dryRun, crashed, outsideChanged, entryKind, entryHasApply>>

Apply(e) ==
  /\ e \in Entries
  /\ entryHasApply[e]
  /\ ~ (verdict[e] # "" /\ "installonverdict" \in Broken)
  /\ IF dryRun THEN
       /\ after' = [after EXCEPT ![e] = installed[e]]
       /\ applied' = [applied EXCEPT ![e] = FALSE]
     ELSE
       /\ applied' = [applied EXCEPT ![e] = TRUE]
       /\ after' = [after EXCEPT ![e] = IF installed[e] # None THEN installed[e] + 1 ELSE 1]
  /\ UNCHANGED <<installed, latest, verdict, observed, sentDeliveries, dryRun, crashed, outsideChanged, entryKind, entryHasApply>>

Report ==
  /\ observed' = [e \in Entries |-> installed[e]]
  /\ crashed' = TRUE
  /\ UNCHANGED <<installed, latest, verdict, applied, after, sentDeliveries, dryRun, outsideChanged, entryKind, entryHasApply>>

Deliver ==
  /\ crashed
  /\ sentDeliveries' = [e \in Entries |-> IF "sentonrefusal" \in Broken THEN sentDeliveries[e] ELSE TRUE]
  /\ crashed' = FALSE
  /\ UNCHANGED <<installed, latest, verdict, applied, after, observed, dryRun, outsideChanged, entryKind, entryHasApply>>

CrashBetweenWriteAndSend ==
  /\ crashed
  /\ UNCHANGED vars

OutsideChange ==
  /\ \E e \in Entries : installed' = [installed EXCEPT ![e] = IF installed[e] # None THEN installed[e] + 1 ELSE 1]
  /\ outsideChanged' = TRUE
  /\ UNCHANGED <<latest, verdict, applied, after, observed, sentDeliveries, dryRun, crashed, entryKind, entryHasApply>>

Next ==
  \/ \E e \in Entries, w \in {"installed","latest"}, r \in ReadResult : Read(e, w, r)
  \/ \E e \in Entries : Verdict(e) \/ Apply(e)
  \/ Report \/ Deliver \/ CrashBetweenWriteAndSend \/ OutsideChange

Spec == Init /\ [][Next]_vars

ApplyOnlyWhatIsNamed ==
  \A e \in Entries : applied[e] => entryHasApply[e]

DryRunInstallsNothing ==
  dryRun => \A e \in Entries : after[e] = installed[e] \/ after[e] = None

VerdictFromReads ==
  \A e \in Entries : verdict[e] # "" => installed[e] # None /\ latest[e] # None

StateWrittenAfterRead ==
  \A e \in Entries : observed[e] # None => observed[e] = installed[e]

SentOnlyOnOK ==
  \A e \in Entries : sentDeliveries[e] => ~("sentonrefusal" \in Broken)

ChangedIsAgainstState ==
  \A e \in Entries : (observed[e] # installed[e]) => (observed[e] # None)

Liveness ==
  WF_vars( (crashed /\ ~crashed') => <> (\E e \in Entries : sentDeliveries[e]) )

TypeOK ==
  /\ installed \in [Entries -> ReadResult]
  /\ latest \in [Entries -> ReadResult]
  /\ verdict \in [Entries -> VerdictSet \cup {""}]
  /\ applied \in [Entries -> BOOLEAN]
  /\ after \in [Entries -> ReadResult]
  /\ observed \in [Entries -> ReadResult]
  /\ sentDeliveries \in [Entries -> BOOLEAN]
  /\ dryRun \in BOOLEAN
  /\ crashed \in BOOLEAN
  /\ outsideChanged \in BOOLEAN

Invariants == ApplyOnlyWhatIsNamed /\ DryRunInstallsNothing /\ VerdictFromReads /\ StateWrittenAfterRead /\ SentOnlyOnOK /\ ChangedIsAgainstState

=============================================================================
