---------------------------- MODULE WorkImport ----------------------------
\* nova-work v1: the states of an issue under import and of a repository's
\* sync, from docs/SPEC-WORK-V1.md section 1 (the tree, layer 1) and
\* section 1.7 (the modes above it, specified here and not built: the
\* destructive close after import, the export that re-opens, and the receipt
\* that licenses the close -- all design-only: the code's verify prints facts
\* and stores none).
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
\* Import and Verify each take a scope, a subset of the repositories. The
\* import writes only its scope's issues and the tree records the scope it was
\* imported with (treeScope, the tree's :scope; a whole-organization import
\* records the whole organization: internal/workfile/tree.go, cmd/nova-work/
\* import.go). The verify reads the tree's scope by default and refuses a
\* --repo outside it before any GitHub call (cmd/nova-work/verify.go), so a
\* verify's scope is always the tree's scope or a narrowing of it, and a verify
\* never reports done outside the import's scope. That refusal is the one
\* guard of the property, so VerifyNeverPassesOutsideItsScope has one reversed
\* witness (Broken "scoped").
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* one guard removed, and the invariant its comment names fails:
\*   "noreceipt"  close without a verify receipt        -> DestructiveOnlyWithReceipt
\*   "stale"      close on a receipt older than an edit -> Lossless
\*   "external"   close an external issue               -> ExternalStaysOpen
\*   "noreopen"   export that does not re-open          -> ExportRestores
\*   "partial"    write the tree before every issue of the scope is fetched
\*                -> TreeIsWholeForItsScope
\*   "scoped"     verify ignores the tree's scope, so a --repo outside it is
\*                not refused and a wider verify reports done
\*                -> VerifyNeverPassesOutsideItsScope

EXTENDS Naturals, FiniteSets

CONSTANTS Internal, External, MaxEdits, Broken, Repos, repo, ImportScope, VerifyScope

Issues == Internal \cup External
None == 0 - 1   \* no version: not fetched, no receipt

VARIABLES
  ghVer,        \* GitHub's version of each issue's contents
  ghOpen,       \* whether the issue is open on GitHub
  fetched,      \* the version the running import read, or None
  treeVer,      \* the version the tree holds, or None
  treeOpen,     \* the state the tree holds
  treeScope,    \* the scope the tree records (its :scope), or {} before any import
  receipt,      \* design-only: the version a zero-difference verify saw, or None
  closedByImport, \* closed on GitHub by the destructive mode
  closedVer,    \* ghost: GitHub's version when the import closed it
  closedWithReceipt, \* ghost: a receipt was held when it was closed
  phase,
  sync

vars == <<ghVer, ghOpen, fetched, treeVer, treeOpen, treeScope, receipt, closedByImport, closedVer, closedWithReceipt, phase, sync>>

Phases == {"absent", "fetched", "intree", "mirrored", "closed", "reopened"}
Syncs == {"idle", "importing", "imported", "verified", "closing", "exporting"}
Vers == 0..MaxEdits \cup {None}

TypeOK ==
  /\ ghVer \in [Issues -> 0..MaxEdits]
  /\ ghOpen \in [Issues -> BOOLEAN]
  /\ fetched \in [Issues -> Vers]
  /\ treeVer \in [Issues -> Vers]
  /\ treeOpen \in [Issues -> BOOLEAN]
  /\ treeScope \in SUBSET Repos
  /\ receipt \in [Issues -> Vers]
  /\ closedByImport \in [Issues -> BOOLEAN]
  /\ closedVer \in [Issues -> Vers]
  /\ closedWithReceipt \in [Issues -> BOOLEAN]
  /\ phase \in [Issues -> Phases]
  /\ sync \in Syncs
  /\ ImportScope \subseteq Repos
  /\ VerifyScope \subseteq Repos

Init ==
  /\ ghVer = [i \in Issues |-> 0]
  /\ ghOpen = [i \in Issues |-> TRUE]
  /\ fetched = [i \in Issues |-> None]
  /\ treeVer = [i \in Issues |-> None]
  /\ treeOpen = [i \in Issues |-> TRUE]
  /\ treeScope = {}
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
  /\ UNCHANGED <<ghOpen, fetched, treeVer, treeOpen, treeScope, receipt, closedByImport, closedVer, closedWithReceipt, phase, sync>>

\* nova-work import begins a pass over its scope (non-destructive); the
\* issues outside the scope are left as they are.
StartImport ==
  /\ sync \in {"idle", "imported"}
  /\ sync' = "importing"
  /\ fetched' = [i \in Issues |-> IF repo[i] \in ImportScope THEN None ELSE fetched[i]]
  /\ phase' = [i \in Issues |-> IF repo[i] \in ImportScope /\ phase[i] \notin {"closed", "reopened"} THEN "absent" ELSE phase[i]]
  /\ receipt' = [i \in Issues |-> IF repo[i] \in ImportScope THEN None ELSE receipt[i]]
  /\ UNCHANGED <<ghVer, ghOpen, treeVer, treeOpen, treeScope, closedByImport, closedVer, closedWithReceipt>>

\* workgh.Fetcher.Issues reads one in-scope issue's full contents.
Fetch(i) ==
  /\ sync = "importing"
  /\ phase[i] = "absent"
  /\ repo[i] \in ImportScope
  /\ fetched' = [fetched EXCEPT ![i] = ghVer[i]]
  /\ phase' = [phase EXCEPT ![i] = "fetched"]
  /\ UNCHANGED <<ghVer, ghOpen, treeVer, treeOpen, treeScope, receipt, closedByImport, closedVer, closedWithReceipt, sync>>

\* nova-work import writes the tree once every issue of its scope is fetched
\* and the encoded tree read back equals what was fetched
\* (cmd/nova-work/import.go). The tree records the scope it was imported with
\* (:scope), and a scoped import that lost its :scope would verify against the
\* whole organization, so import checks it survives the file (import.go).
WriteTree ==
  /\ sync = "importing"
  /\ \/ \A i \in Issues : repo[i] \in ImportScope => phase[i] \in {"fetched", "closed", "reopened"}
     \/ Broken = "partial" /\ \E i \in Issues : repo[i] \in ImportScope /\ phase[i] = "fetched"
  /\ treeVer' = [i \in Issues |-> IF repo[i] \in ImportScope /\ phase[i] = "fetched" THEN fetched[i] ELSE treeVer[i]]
  /\ treeOpen' = [i \in Issues |-> IF repo[i] \in ImportScope /\ phase[i] = "fetched" THEN ghOpen[i] ELSE treeOpen[i]]
  /\ treeScope' = ImportScope
  /\ phase' = [i \in Issues |-> IF repo[i] \in ImportScope /\ phase[i] = "fetched" THEN "intree" ELSE phase[i]]
  /\ sync' = "imported"
  /\ UNCHANGED <<ghVer, ghOpen, fetched, receipt, closedByImport, closedVer, closedWithReceipt>>

\* The scope a verify reads: the --repo scope named, or, with none, the tree's
\* own recorded scope (cmd/nova-work/verify.go reads exactly tree.Scope or the
\* narrowing --repo). A verify is refused a --repo outside the tree's scope
\* before any GitHub call, so VerifyReads is always the tree's scope or a
\* narrowing of it; the refusal is the guard Verify removes below.
VerifyReads == IF VerifyScope # {} THEN VerifyScope ELSE treeScope

\* The issues a verify compares: those of the repositories it reads that the
\* tree holds. A scoped tree holds only its scope's issues, so a repository the
\* import did not read contributes nothing (treeVer is None); the refusal keeps
\* VerifyReads within the tree's scope, so in the design every issue VerifyReads
\* names is held by the tree.
VerifyCompared(i) ==
  repo[i] \in VerifyReads /\ treeVer[i] # None

\* nova-work verify: a fresh read of its scope, compared field for field. The
\* scope defaults to the tree's own, and a --repo outside the tree's scope is
\* refused before any GitHub call (the guard Broken "scoped" removes), so a
\* verify's scope is the tree's scope or a narrowing of it and a wider verify
\* never reports done. Zero differences over the scope is the receipt; any
\* difference leaves the repository imported, to be imported again. The receipt
\* the verify holds is design-only (the code's verify prints facts and stores
\* none).
Verify ==
  /\ sync \in {"imported", "verified"}
  /\ \A i \in Issues : repo[i] \in ImportScope => phase[i] \in {"intree", "mirrored"}
  /\ Broken = "scoped" \/ VerifyScope \subseteq treeScope
  /\ IF \A i \in Issues : VerifyCompared(i) => treeVer[i] = ghVer[i] /\ treeOpen[i] = ghOpen[i]
       THEN /\ receipt' = [i \in Issues |-> IF VerifyCompared(i) THEN ghVer[i] ELSE receipt[i]]
            /\ phase' = [i \in Issues |-> IF VerifyCompared(i) THEN "mirrored" ELSE phase[i]]
            /\ sync' = "verified"
       ELSE /\ receipt' = [i \in Issues |-> IF VerifyCompared(i) THEN None ELSE receipt[i]]
            /\ phase' = [i \in Issues |-> IF VerifyCompared(i) THEN "intree" ELSE phase[i]]
            /\ sync' = "imported"
  /\ UNCHANGED <<ghVer, ghOpen, fetched, treeVer, treeOpen, treeScope, closedByImport, closedVer, closedWithReceipt>>

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
  /\ UNCHANGED <<ghVer, fetched, treeVer, treeOpen, treeScope, receipt>>

\* Export (specified, not built): re-open an issue the import closed.
Export(i) ==
  /\ sync \in {"closing", "exporting"}
  /\ phase[i] = "closed"
  /\ closedByImport[i]
  /\ ghOpen' = [ghOpen EXCEPT ![i] = Broken # "noreopen"]
  /\ phase' = [phase EXCEPT ![i] = "reopened"]
  /\ sync' = "exporting"
  /\ UNCHANGED <<ghVer, fetched, treeVer, treeOpen, treeScope, receipt, closedByImport, closedVer, closedWithReceipt>>

Next ==
  \/ \E i \in Issues : ExternalEdit(i) \/ Fetch(i) \/ Close(i) \/ Export(i)
  \/ StartImport \/ WriteTree \/ Verify

Spec == Init /\ [][Next]_vars /\ WF_vars(WriteTree) /\ WF_vars(Verify) /\ \A i \in Issues : WF_vars(Fetch(i))

----------------------------------------------------------------------------
\* The invariants.

\* A tree is written only whole: every issue of the import's scope is in it.
TreeIsWholeForItsScope ==
  sync \in {"imported", "verified", "closing", "exporting"} => \A i \in Issues : repo[i] \in ImportScope => treeVer[i] # None

\* A verify never reports done over a scope wider than the import's: the tree
\* records the scope it was imported with, the verify reads that scope (or a
\* narrowing --repo) and refuses a --repo outside it before any GitHub call, so
\* a verify's scope is always the tree's scope or a narrowing of it. The
\* refusal is the one guard, so Broken "scoped" (which ignores the tree's scope
\* and lets a wider verify report done) is its reversed witness.
VerifyNeverPassesOutsideItsScope ==
  sync = "verified" => VerifyScope \subseteq treeScope

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
