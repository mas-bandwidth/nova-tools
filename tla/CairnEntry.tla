----------------------------- MODULE CairnEntry -----------------------------
\* Nested cairn entry publication, not flat records or a metadata transaction.
\* Truth is disk[id], a complete immutable text/metadata tuple. The atomicfile
\* contract is assumed: publish an absent name atomically, or report existence.
\* JSON validity and equality of exact prose are lower-layer test obligations.
\* Pointer repair and log append are distinct transitions. Their sets below
\* abstract presence only: neither physical-line uniqueness nor log completeness
\* is claimed. Uncooperative deletion/replacement and session Open are excluded.
\* Source map: internal/cairn/cairn.go appendEntry initial read/duplicate branch,
\* final WriteFile, ensurePointer, appendLog and result; proposed NoReplace plus
\* winner reread in fuse-cairn-concurrency-design.md section 3 (not implemented).
\* Metadata tokens stand for timestamp, source and policy together.
EXTENDS Naturals, FiniteSets
CONSTANTS Procs, IDs, Texts, Metas, MaxRetries, AllowCrash, Broken
None == 0
Entry == [text : Texts, meta : Metas]
PCs == {"read", "prepare", "publish", "winner", "decide", "pointer",
        "log", "ack", "done", "conflict", "error", "crashed"}
ASSUME /\ Procs # {} /\ IDs # {} /\ Texts # {} /\ Metas # {}
       /\ None \notin Entry /\ MaxRetries \in Nat
       /\ Broken \in {"none", "replace", "trustloser", "retrystamp"}
VARIABLE s
vars == <<s>>

Init == \E ids \in [Procs -> IDs], texts \in [Procs -> Texts],
           metas \in [Procs -> Metas] :
  s = [disk |-> [i \in IDs |-> None], first |-> [i \in IDs |-> None],
       id |-> ids, text |-> texts, meta |-> metas,
       pc |-> [p \in Procs |-> "read"], seen |-> [p \in Procs |-> None],
       selected |-> [p \in Procs |-> None], dup |-> [p \in Procs |-> FALSE],
       retries |-> [p \in Procs |-> 0], crashes |-> [p \in Procs |-> 0],
       pointerDone |-> [p \in Procs |-> FALSE],
       logDone |-> [p \in Procs |-> FALSE],
       pointers |-> {}, logs |-> {}, accepted |-> {},
       crashPublished |-> {}, healed |-> {}, conflictSafe |-> TRUE]

Requested(p) == [text |-> s.text[p], meta |-> s.meta[p]]

\* appendEntry's initial lookup. A present entry enters the existing comparison
\* branch; a missing entry prepares bytes, retaining the stale-absence race.
ReadEntry(p) ==
  /\ s.pc[p] = "read"
  /\ s' = [s EXCEPT !.seen[p] = s.disk[s.id[p]],
             !.selected[p] = s.disk[s.id[p]],
             !.dup[p] = s.disk[s.id[p]] # None,
             !.pc[p] = IF s.disk[s.id[p]] = None THEN "prepare" ELSE "decide"]
Prepare(p) ==
  /\ s.pc[p] = "prepare"
  /\ s' = [s EXCEPT !.pc[p] = "publish"]

\* Only this transition publishes. The first ghost never changes, including
\* when publication is followed by a crash or an independent cleanup error.
PublishAbsent(p) ==
  /\ s.pc[p] = "publish"
  /\ IF s.disk[s.id[p]] = None \/ Broken = "replace"
       THEN s' = [s EXCEPT !.disk[s.id[p]] = Requested(p),
                   !.first[s.id[p]] = IF @ = None THEN Requested(p) ELSE @,
                   !.selected[p] = Requested(p), !.dup[p] = FALSE,
                   !.pc[p] = "pointer"]
       ELSE IF Broken = "trustloser"
         THEN s' = [s EXCEPT !.selected[p] = Requested(p),
                     !.dup[p] = TRUE, !.pc[p] = "pointer"]
         ELSE s' = [s EXCEPT !.pc[p] = "winner"]

\* Existence is not success: reread complete stored bytes and reuse the same
\* prose-only conflict/duplicate decision as the initial existing-entry branch.
ReadWinner(p) ==
  /\ s.pc[p] = "winner"
  /\ s.disk[s.id[p]] # None
  /\ s' = [s EXCEPT !.seen[p] = s.disk[s.id[p]],
             !.selected[p] = s.disk[s.id[p]], !.dup[p] = TRUE,
             !.pc[p] = "decide"]
DecideDuplicateOrConflict(p) ==
  /\ s.pc[p] = "decide"
  /\ s.selected[p] # None
  /\ IF s.selected[p].text = s.text[p]
       THEN s' = [s EXCEPT !.pc[p] = "pointer",
                   !.selected[p] = IF Broken = "retrystamp" /\ s.retries[p] > 0
                                    THEN Requested(p) ELSE @]
       ELSE s' = [s EXCEPT !.pc[p] = "conflict",
                   !.conflictSafe = @ /\ ~s.pointerDone[p] /\ ~s.logDone[p]]

\* ensurePointer uses the selected stored stamp on duplicates. No atomic
\* coupling with entry or log; a crash can happen between all these operations.
HealPointer(p) ==
  /\ s.pc[p] = "pointer"
  /\ s' = [s EXCEPT !.pointers = @ \cup {<<s.id[p], s.selected[p].meta>>},
             !.pointerDone[p] = TRUE,
             !.pc[p] = IF s.dup[p] THEN "ack" ELSE "log"]
AppendLog(p) ==
  /\ s.pc[p] = "log"
  /\ s' = [s EXCEPT !.logs = @ \cup {<<s.id[p], s.selected[p].meta>>},
             !.logDone[p] = TRUE, !.pc[p] = "ack"]
Acknowledge(p) ==
  /\ s.pc[p] = "ack"
  /\ s' = [s EXCEPT !.pc[p] = "done",
             !.accepted = @ \cup {[writer |-> p, id |-> s.id[p],
                 text |-> s.selected[p].text, meta |-> s.selected[p].meta,
                 duplicate |-> s.dup[p]]},
             !.healed = IF s.retries[p] > 0 /\ p \in s.crashPublished
                           THEN @ \cup {p} ELSE @]

\* Errors never become successful acknowledgments or erase a published entry.
\* Post-publication failure represents pointer/log or publication-cleanup error;
\* existence joined with an independent error must not take a success path.
Fail(p) ==
  /\ s.pc[p] \in {"read", "prepare", "publish", "winner", "decide",
                      "pointer", "log", "ack"}
  /\ s' = [s EXCEPT !.pc[p] = "error"]
Crash(p) ==
  /\ AllowCrash
  /\ s.crashes[p] < MaxRetries
  /\ s.pc[p] \in {"read", "prepare", "publish", "winner", "decide",
                      "pointer", "log", "ack"}
  /\ s' = [s EXCEPT !.pc[p] = "crashed", !.crashes[p] = @ + 1,
             !.crashPublished = IF s.pc[p] = "pointer" /\ ~s.dup[p]
                                 THEN @ \cup {p} ELSE @]
\* An explicit retry keeps ID/prose, but may supply a new timestamp/source/policy.
Retry(p, m) ==
  /\ s.pc[p] \in {"crashed", "error"}
  /\ s.retries[p] < MaxRetries
  /\ m \in Metas
  /\ s' = [s EXCEPT !.pc[p] = "read", !.meta[p] = m,
             !.retries[p] = @ + 1, !.seen[p] = None, !.selected[p] = None,
             !.dup[p] = FALSE, !.pointerDone[p] = FALSE, !.logDone[p] = FALSE]

Next == \E p \in Procs : ReadEntry(p) \/ Prepare(p) \/ PublishAbsent(p)
        \/ ReadWinner(p) \/ DecideDuplicateOrConflict(p) \/ HealPointer(p)
        \/ AppendLog(p) \/ Acknowledge(p) \/ Fail(p) \/ Crash(p)
        \/ (\E m \in Metas : Retry(p, m))
Spec == Init /\ [][Next]_vars

TypeOK == /\ s.disk \in [IDs -> Entry \cup {None}]
          /\ s.first \in [IDs -> Entry \cup {None}]
          /\ s.id \in [Procs -> IDs] /\ s.text \in [Procs -> Texts]
          /\ s.meta \in [Procs -> Metas] /\ s.pc \in [Procs -> PCs]
          /\ s.seen \in [Procs -> Entry \cup {None}]
          /\ s.selected \in [Procs -> Entry \cup {None}]
          /\ s.dup \in [Procs -> BOOLEAN]
          /\ s.retries \in [Procs -> 0..MaxRetries]
          /\ s.crashes \in [Procs -> 0..MaxRetries]
          /\ s.pointerDone \in [Procs -> BOOLEAN]
          /\ s.logDone \in [Procs -> BOOLEAN]
          /\ s.pointers \subseteq IDs \X Metas /\ s.logs \subseteq IDs \X Metas
          /\ s.accepted \subseteq [writer : Procs, id : IDs, text : Texts,
                                    meta : Metas, duplicate : BOOLEAN]
          /\ s.crashPublished \subseteq Procs /\ s.healed \subseteq Procs
          /\ s.conflictSafe \in BOOLEAN
EntryNeverReplaced == \A i \in IDs : s.first[i] # None => s.disk[i] = s.first[i]
OneAcceptedTextPerID == \A a, b \in s.accepted : a.id = b.id => a.text = b.text
SuccessHasPublishedEntry == \A a \in s.accepted :
  s.disk[a.id] # None /\ a.text = s.disk[a.id].text
DuplicateUsesStoredMetadata == \A a \in s.accepted :
  a.duplicate => s.disk[a.id] # None /\ a.meta = s.disk[a.id].meta
ConflictWritesNoEntry ==
  /\ s.conflictSafe
  /\ \A p \in Procs : s.pc[p] = "conflict" =>
       s.disk[s.id[p]] = s.seen[p] /\ ~s.pointerDone[p] /\ ~s.logDone[p]
PointerNamesPublishedEntry == \A pair \in s.pointers :
  s.disk[pair[1]] # None /\ pair[2] = s.disk[pair[1]].meta

\* Reversed invariants prove reachability only when TLC finds the named failure.
ConflictNeverReached == ~((\E p \in Procs : s.pc[p] = "conflict") /\ s.accepted # {})
RetryNeverHealed == s.healed = {}
BothNeverSucceeded == Cardinality({a.writer : a \in s.accepted}) < Cardinality(Procs)
=============================================================================
