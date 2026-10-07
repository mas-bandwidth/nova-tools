---------------------------- MODULE WorkImport ----------------------------
\* nova-work v1: the states of an issue under import and of a repository's
\* sync, from docs/SPEC-WORK-V1.md section 1 (the tree, layer 1) and
\* section 1.7 (the modes above it, specified here and not built: the
\* destructive close after import, the export that re-opens).
\*
\* An issue's contents are abstracted to a version number: GitHub's copy
\* moves on every outside edit (ExternalEdit), the tree holds the version it
\* fetched. Equal versions stand for SPEC-WORK-V1's field-for-field equality
\* (cmd/nova-work verify, internal/workfile.Diff); the model does not look
\* inside an issue.
\*
\* An issue's import states (phase):
\*   absent            not read yet
\*   fetched           read from GitHub (workgh.Fetcher.Issues), not in the tree
\*   intree            in the tree file (import wrote it; nova-work import)
\*   mirrored          a verify found it equal to GitHub (nova-work verify)
\*   closed            closed on GitHub by a destructive import (not built)
\*   reopened          re-opened on GitHub by export (not built)
\* A repository's sync states (sync):
\*   idle -> importing -> imported -> verified -> closing -> exporting
\*   imported -> importing (a verify found drift; import again)
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* one guard removed, and the invariant its comment names fails:
\*   "noreceipt"  close without a verify receipt       -> DestructiveOnlyWithReceipt
\*   "stale"      close on a receipt older than an edit -> Lossless
\*   "external"   close an external issue               -> ExternalStaysOpen
\*   "noreopen"   export that does not re-open          -> ExportRestores
\*   "partial"    write the tree before every issue is fetched -> TreeIsWhole

EXTENDS Naturals, FiniteSets

CONSTANTS Internal, External, MaxEdits, Broken

Issues == Internal \cup External
None == 0 - 1   \* no version: not fetched, no receipt

VARIABLES
  ghVer,        \* GitHub's version of each issue's contents
  ghOpen,       \* whether the issue is open on GitHub
  fetched,      \* the version the running import read, or None
  treeVer,      \* the version the tree holds, or None
  treeOpen,     \* the state the tree holds
  receipt,      \* the version a zero-difference verify saw, or None
  closedByImport, \* closed on GitHub by the destructive mode
  closedVer,    \* ghost: GitHub's version when the import closed it
  closedWithReceipt, \* ghost: a receipt was held when it was closed
  phase,
  sync

vars == <<ghVer, ghOpen, fetched, treeVer, treeOpen, receipt, closedByImport, closedVer, closedWithReceipt, phase, sync>>

Phases == {"absent", "fetched", "intree", "mirrored", "closed", "reopened"}
Syncs == {"idle", "importing", "imported", "verified", "closing", "exporting"}
Vers == 0..MaxEdits \cup {None}

TypeOK ==
  /\ ghVer \in [Issues -> 0..MaxEdits]
  /\ ghOpen \in [Issues -> BOOLEAN]
  /\ fetched \in [Issues -> Vers]
  /\ treeVer \in [Issues -> Vers]
  /\ treeOpen \in [Issues -> BOOLEAN]
  /\ receipt \in [Issues -> Vers]
  /\ closedByImport \in [Issues -> BOOLEAN]
  /\ closedVer \in [Issues -> Vers]
  /\ closedWithReceipt \in [Issues -> BOOLEAN]
  /\ phase \in [Issues -> Phases]
  /\ sync \in Syncs

Init ==
  /\ ghVer = [i \in Issues |-> 0]
  /\ ghOpen = [i \in Issues |-> TRUE]
  /\ fetched = [i \in Issues |-> None]
  /\ treeVer = [i \in Issues |-> None]
  /\ treeOpen = [i \in Issues |-> TRUE]
  /\ receipt = [i \in Issues |-> None]
  /\ closedByImport = [i \in Issues |-> FALSE]
  /\ closedVer = [i \in Issues |-> None]
  /\ closedWithReceipt = [i \in Issues |-> FALSE]
  /\ phase = [i \in Issues |-> "absent"]
  /\ sync = "idle"

\* An outside event: someone edits the issue on GitHub (a comment, a label,
\* a title). Any time, whatever the tree holds.
ExternalEdit(i) ==
  /\ ghVer[i] < MaxEdits
  /\ ghVer' = [ghVer EXCEPT ![i] = @ + 1]
  /\ UNCHANGED <<ghOpen, fetched, treeVer, treeOpen, receipt, closedByImport, closedVer, closedWithReceipt, phase, sync>>

\* nova-work import begins a pass over the repository (non-destructive).
StartImport ==
  /\ sync \in {"idle", "imported"}
  /\ sync' = "importing"
  /\ fetched' = [i \in Issues |-> None]
  /\ phase' = [i \in Issues |-> IF phase[i] \in {"closed", "reopened"} THEN phase[i] ELSE "absent"]
  /\ receipt' = [i \in Issues |-> None]
  /\ UNCHANGED <<ghVer, ghOpen, treeVer, treeOpen, closedByImport, closedVer, closedWithReceipt>>

\* workgh.Fetcher.Issues reads one issue's full contents.
Fetch(i) ==
  /\ sync = "importing"
  /\ phase[i] = "absent"
  /\ fetched' = [fetched EXCEPT ![i] = ghVer[i]]
  /\ phase' = [phase EXCEPT ![i] = "fetched"]
  /\ UNCHANGED <<ghVer, ghOpen, treeVer, treeOpen, receipt, closedByImport, closedVer, closedWithReceipt, sync>>

\* nova-work import writes the tree once every issue is fetched and the
\* encoded tree read back equals what was fetched (cmd/nova-work/import.go).
WriteTree ==
  /\ sync = "importing"
  /\ \/ \A i \in Issues : phase[i] \in {"fetched", "closed", "reopened"}
     \/ Broken = "partial" /\ \E i \in Issues : phase[i] = "fetched"
  /\ treeVer' = [i \in Issues |-> IF phase[i] = "fetched" THEN fetched[i] ELSE treeVer[i]]
  /\ treeOpen' = [i \in Issues |-> IF phase[i] = "fetched" THEN ghOpen[i] ELSE treeOpen[i]]
  /\ phase' = [i \in Issues |-> IF phase[i] = "fetched" THEN "intree" ELSE phase[i]]
  /\ sync' = "imported"
  /\ UNCHANGED <<ghVer, ghOpen, fetched, receipt, closedByImport, closedVer, closedWithReceipt>>

\* nova-work verify: a fresh read compared field for field. Zero
\* differences over the repository is the receipt; any difference leaves
\* the repository imported, to be imported again.
Verify ==
  /\ sync \in {"imported", "verified"}
  /\ \A i \in Issues : phase[i] \in {"intree", "mirrored"}
  /\ IF \A i \in Issues : treeVer[i] = ghVer[i] /\ treeOpen[i] = ghOpen[i]
       THEN /\ receipt' = [i \in Issues |-> ghVer[i]]
            /\ phase' = [i \in Issues |-> "mirrored"]
            /\ sync' = "verified"
       ELSE /\ receipt' = [i \in Issues |-> None]
            /\ phase' = [i \in Issues |-> "intree"]
            /\ sync' = "imported"
  /\ UNCHANGED <<ghVer, ghOpen, fetched, treeVer, treeOpen, closedByImport, closedVer, closedWithReceipt>>

\* The destructive mode (specified, not built): close an internal, open
\* issue on GitHub after the import, only on a receipt taken at GitHub's
\* current version (the close is conditional on the issue unchanged since
\* the verify).
ReceiptHolds(i) ==
  CASE Broken = "noreceipt" -> TRUE
    [] Broken = "stale" -> receipt[i] # None
    [] OTHER -> receipt[i] = ghVer[i]

Close(i) ==
  /\ sync \in {"verified", "closing"} \/ (Broken = "noreceipt" /\ sync = "imported")
  /\ phase[i] \in {"mirrored", "intree"}
  /\ i \in Internal \/ Broken = "external"
  /\ ghOpen[i]
  /\ ReceiptHolds(i)
  /\ ghOpen' = [ghOpen EXCEPT ![i] = FALSE]
  /\ closedByImport' = [closedByImport EXCEPT ![i] = TRUE]
  /\ closedVer' = [closedVer EXCEPT ![i] = ghVer[i]]
  /\ closedWithReceipt' = [closedWithReceipt EXCEPT ![i] = receipt[i] # None]
  /\ phase' = [phase EXCEPT ![i] = "closed"]
  /\ sync' = "closing"
  /\ UNCHANGED <<ghVer, fetched, treeVer, treeOpen, receipt>>

\* Export (specified, not built): re-open an issue the import closed.
Export(i) ==
  /\ sync \in {"closing", "exporting"}
  /\ phase[i] = "closed"
  /\ closedByImport[i]
  /\ ghOpen' = [ghOpen EXCEPT ![i] = Broken # "noreopen"]
  /\ phase' = [phase EXCEPT ![i] = "reopened"]
  /\ sync' = "exporting"
  /\ UNCHANGED <<ghVer, fetched, treeVer, treeOpen, receipt, closedByImport, closedVer, closedWithReceipt>>

Next ==
  \/ \E i \in Issues : ExternalEdit(i) \/ Fetch(i) \/ Close(i) \/ Export(i)
  \/ StartImport \/ WriteTree \/ Verify

Spec == Init /\ [][Next]_vars /\ WF_vars(WriteTree) /\ WF_vars(Verify) /\ \A i \in Issues : WF_vars(Fetch(i))

----------------------------------------------------------------------------
\* The invariants.

\* A tree is written only whole: every issue of the repository is in it.
TreeIsWhole ==
  sync \in {"imported", "verified", "closing", "exporting"} => \A i \in Issues : treeVer[i] # None

\* Only a verify receipt licenses a close.
DestructiveOnlyWithReceipt ==
  \A i \in Issues : closedByImport[i] => closedWithReceipt[i]

\* Lossless: an issue the import closed is held by the tree exactly as GitHub
\* held it when it was closed, and the tree says it was open.
Lossless ==
  \A i \in Issues : closedByImport[i] => closedVer[i] = treeVer[i] /\ treeOpen[i]

\* An external issue is never closed by the import; it stays open until its
\* fix closes it.
ExternalStaysOpen ==
  \A i \in External : ~closedByImport[i]

\* The round trip: an issue export re-opened, not edited since it was
\* closed, is on GitHub exactly as the tree holds it.
ExportRestores ==
  \A i \in Issues : phase[i] = "reopened" /\ ghVer[i] = closedVer[i] => ghVer[i] = treeVer[i] /\ ghOpen[i] = treeOpen[i]

\* A receipt names what GitHub held when it was taken; the tree held it too.
ReceiptIsTheTree ==
  \A i \in Issues : receipt[i] # None => receipt[i] = treeVer[i]

\* Liveness, under fairness: an import that starts writes a whole tree.
ImportEnds == sync = "importing" ~> sync \in {"imported", "verified"}
=============================================================================
