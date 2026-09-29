----------------------------- MODULE CardManager -----------------------------
(***************************************************************************)
(* The card layer: cards are plain data, and one manager transforms the    *)
(* ARRAY of cards. Every variable below is a function from card ids to a   *)
(* property; there is no card object and no per-card action. Each action   *)
(* is one batch: one server call that reads one pre-state, checks every    *)
(* guard against it, and commits every change together with one table     *)
(* revision. A refused batch writes nothing (the Refuse stutter). A single *)
(* card is the degenerate batch of one.                                    *)
(*                                                                         *)
(* The contract is mas-bandwidth/ideas#825 with its rulings:               *)
(*  - identity is card:<id>; the member is created by admission and never  *)
(*    re-keyed; replacement is a NEW admission under a new id, the old     *)
(*    member ending done/replaced with a successor link;                   *)
(*  - an admitted definition (digest, kind, dependencies) never changes;   *)
(*  - a dependency is met when landed, or done/completed for a kind that   *)
(*    carries no PR; replaced, cancelled and dependency-failed never count;*)
(*  - evidence is bound to the head and digest it was taken at, and a new  *)
(*    head (a push, a new result, a rework) invalidates it.                *)
(*                                                                         *)
(* The table under the card array is abstracted to its one-place contract *)
(* (MemberTable.tla, EpochMemberTable.tla): cells hold member sets, place *)
(* is the member record's reverse index, and a card's row is its stream.   *)
(*                                                                         *)
(* Left out, and held elsewhere or by tests: the epoch's advance (the      *)
(* table layer's, EpochMemberTable; this layer observes one epoch and      *)
(* never writes it), operation records and replay of an operation id (the *)
(* receipt ledger; the table set read/write model owns it), no-op batches  *)
(* (a batch that changes no card is a stutter here), rejecting reads and   *)
(* red CI (absence of an accepting record stands for them), the reader    *)
(* roster and author exclusion, ground truth for external observations    *)
(* (the evidence verifier's), and dispatch: jobs, slots, leases, workers.  *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS
  Cards,      \* card ids (the member is card:<id>)
  Streams,    \* the table's rows: one per stream
  Stream,     \* [Cards -> Streams]: the stream a card's definition names
  Kind,       \* [Cards -> STRING]: the KIND of the committed definition
  NonPR,      \* the kinds that carry no PR (completion policy)
  Deps,       \* [Cards -> SUBSET Cards]: DEPENDS-ON of the committed definition
  Def,        \* [Cards -> STRING]: the digest of the committed definition of each id
  Reviewers,  \* recorded reader identities
  Quorum,     \* accepting reads required at the exact head
  MaxHead,    \* bound on a card's head (results and pushes)
  BatchMax,   \* bound on the entries of one batch (1..3)
  Broken      \* "none", or the misimplementation a reversed witness turns on

ASSUME BatchMax \in 1..3 /\ MaxHead \in Nat /\ Quorum \in Nat
ASSUME Broken \in {"none", "secondplace", "digest", "deps", "stale",
                   "sequential", "return", "resurrect"}

States   == {"waiting", "ready", "working", "review", "merging", "landed", "done"}
Open     == {"waiting", "ready", "working", "review", "merging"}
Line     == {"ready", "working", "review", "merging"}
Terminal == {"landed", "done"}
Outcomes == {"completed", "cancelled", "depfailed", "replaced"}
Cells    == Streams \X States
NoPlace  == <<"-", "-">>
None     == "-"
Issuers  == Reviewers \cup {"ci"}
Digests  == {Def[c] : c \in Cards}
Evidence == Issuers \X {"accept", "ok"} \X (0..MaxHead) \X Digests

VARIABLES
  cells,     \* [Cells -> SUBSET Cards]      the table's owned cells
  place,     \* [Cards -> Cells \cup {NoPlace}] the member record's reverse index
  digest,    \* [Cards -> Digests \cup {None}]  the pinned definition digest
  kind,      \* [Cards -> kinds \cup {None}]    KIND, pinned at admission
  deps,      \* [Cards -> SUBSET Cards]         DEPENDS-ON, pinned at admission
  rev,       \* [Cards -> Nat]                  the member revision
  outcome,   \* [Cards -> Outcomes \cup {None}] set on done
  succ,      \* [Cards -> Cards \cup {None}]    the successor of a replaced card
  head,      \* [Cards -> 0..MaxHead]           the head the card's result is at
  evidence,  \* [Cards -> SUBSET Evidence]      live evidence records
  tableRev,  \* the table revision: once per accepted batch
  epoch,     \* the observed table epoch: never written by this layer
  onePre     \* audit: every accepted batch's guards held in its one pre-state

vars == <<cells, place, digest, kind, deps, rev, outcome, succ, head,
          evidence, tableRev, epoch, onePre>>

TypeOK ==
  /\ cells \in [Cells -> SUBSET Cards]
  /\ place \in [Cards -> Cells \cup {NoPlace}]
  /\ digest \in [Cards -> Digests \cup {None}]
  /\ deps \in [Cards -> SUBSET Cards]
  /\ rev \in [Cards -> Nat]
  /\ outcome \in [Cards -> Outcomes \cup {None}]
  /\ succ \in [Cards -> Cards \cup {None}]
  /\ head \in [Cards -> 0..MaxHead]
  /\ evidence \in [Cards -> SUBSET Evidence]
  /\ tableRev \in Nat /\ epoch \in Nat /\ onePre \in BOOLEAN

Init ==
  /\ cells = [x \in Cells |-> {}]
  /\ place = [c \in Cards |-> NoPlace]
  /\ digest = [c \in Cards |-> None]
  /\ kind = [c \in Cards |-> None]
  /\ deps = [c \in Cards |-> {}]
  /\ rev = [c \in Cards |-> 0]
  /\ outcome = [c \in Cards |-> None]
  /\ succ = [c \in Cards |-> None]
  /\ head = [c \in Cards |-> 0]
  /\ evidence = [c \in Cards |-> {}]
  /\ tableRev = 0 /\ epoch = 1 /\ onePre = TRUE

----------------------------------------------------------------------------
(* Reading the array *)

Admitted(c) == place[c] /= NoPlace
St(c)       == IF place[c] = NoPlace THEN "none" ELSE place[c][2]
AdmittedIds == {c \in Cards : Admitted(c)}
Fresh       == {c \in Cards : ~Admitted(c) /\ digest[c] = None}

\* a snapshot of the array's placements and outcomes: the pre-state every
\* guard reads (Pre), or, in the one-at-a-time witness, a partial update
Pre == [st |-> [c \in Cards |-> St(c)], oc |-> outcome]
MetIn(S, d) == \/ S.st[d] = "landed"
               \/ S.st[d] = "done" /\ S.oc[d] = "completed" /\ kind[d] \in NonPR
DepsMetIn(S, c)   == \A d \in deps[c] : MetIn(S, d)
DepFailedIn(S, c) == \E d \in deps[c] : S.st[d] = "done" /\ S.oc[d] \in {"cancelled", "depfailed", "replaced"}
DepsMet(c) == DepsMetIn(Pre, c)

PRKind(c) == kind[c] \notin NonPR
Accepts(c) == {r \in Reviewers : <<r, "accept", head[c], digest[c]>> \in evidence[c]}
Accepted(c) == Cardinality(Accepts(c)) >= Quorum
CIOk(c) == <<"ci", "ok", head[c], digest[c]>> \in evidence[c]

\* at most BatchMax entries drawn from S (a batch is a set, never a loop)
Small(S) == {{p} : p \in S}
            \cup (IF BatchMax >= 2 THEN {{p, q} : p, q \in S} ELSE {})
            \cup (IF BatchMax >= 3 THEN {{p, q, r} : p, q, r \in S} ELSE {})
\* a set of <<card, x>> pairs names each card once
OnePerCard(B) == \A p, q \in B : p[1] = q[1] => p = q
Ids(B) == {p[1] : p \in B}
Of(B, c) == (CHOOSE p \in B : p[1] = c)[2]

\* one commit: every changed card's revision once, the table's once
Commit(D) ==
  /\ rev' = [c \in Cards |-> IF c \in D THEN rev[c] + 1 ELSE rev[c]]
  /\ tableRev' = tableRev + 1
\* cells follow the records in the same commit
Recell(D, np) == [x \in Cells |-> (cells[x] \ D) \cup {c \in D : np[c] = x}]

----------------------------------------------------------------------------
(* AdmitBatch: every selected committed definition is created in waiting   *)
(* together, record and placement in one commit; an id already admitted    *)
(* (record or placement) refuses the whole batch.                          *)

AdmitBatch(A) ==
  /\ A \subseteq (IF Broken = "secondplace" THEN Cards ELSE Fresh)
  /\ LET np == [c \in Cards |-> IF c \in A THEN <<Stream[c], "waiting">> ELSE place[c]] IN
     /\ place' = np
     /\ cells' = IF Broken = "secondplace"
                   \* the witness: create without the absence guard, the
                   \* old cell kept (a second place for an admitted card)
                   THEN [x \in Cells |-> cells[x] \cup {c \in A : np[c] = x}]
                   ELSE Recell(A, np)
  /\ digest' = [c \in Cards |-> IF c \in A THEN Def[c] ELSE digest[c]]
  /\ kind' = [c \in Cards |-> IF c \in A THEN Kind[c] ELSE kind[c]]
  /\ deps' = [c \in Cards |-> IF c \in A THEN Deps[c] ELSE deps[c]]
  /\ Commit(A)
  /\ UNCHANGED <<outcome, succ, head, evidence, epoch, onePre>>

----------------------------------------------------------------------------
(* ResolveSet: over a declared scope, the waiting cards whose dependencies *)
(* are met in the pre-state move to ready together. The complete scope is  *)
(* the set of every eligible card; a narrower scope is a subset of it.     *)

Eligible == {c \in AdmittedIds : St(c) = "waiting" /\ (Broken = "deps" \/ DepsMet(c))}

ResolveSet(E) ==
  /\ E /= {} /\ E \subseteq Eligible
  /\ LET np == [c \in Cards |-> IF c \in E THEN <<Stream[c], "ready">> ELSE place[c]] IN
     /\ place' = np /\ cells' = Recell(E, np)
  /\ Commit(E)
  /\ UNCHANGED <<digest, kind, deps, outcome, succ, head, evidence, epoch, onePre>>

ResolveAny == \E E \in Small(Eligible) : ResolveSet(E)

\* the witness for Ready means READY: a ready card with no taker is sent
\* back to waiting (the old deal-return)
ReturnSet(E) ==
  /\ Broken = "return"
  /\ E /= {} /\ \A c \in E : St(c) = "ready"
  /\ LET np == [c \in Cards |-> IF c \in E THEN <<Stream[c], "waiting">> ELSE place[c]] IN
     /\ place' = np /\ cells' = Recell(E, np)
  /\ Commit(E)
  /\ UNCHANGED <<digest, kind, deps, outcome, succ, head, evidence, epoch, onePre>>

----------------------------------------------------------------------------
(* ApplyEvents: a manifest of typed events, one per card, applied as one   *)
(* transaction. Every guard reads the one pre-state; events chain across   *)
(* batches, never inside one. Two events for one card refuse (OnePerCard). *)

Events == {"start", "result", "head", "merge", "rework", "land",
           "complete", "cancel", "depfail"}

To(c, e) ==
  CASE e = "start"    -> "working"
    [] e = "result"   -> "review"
    [] e = "head"     -> "review"
    [] e = "merge"    -> "merging"
    [] e = "rework"   -> "ready"
    [] e = "land"     -> "landed"
    [] e \in {"complete", "cancel", "depfail"} -> "done"
NewOutcome(c, e) ==
  CASE e = "complete" -> "completed"
    [] e = "cancel"   -> "cancelled"
    [] e = "depfail"  -> "depfailed"
    [] OTHER          -> outcome[c]

\* the card's own guards (its own pre-state fields)
OwnOK(c, e) ==
  CASE e = "start"    -> St(c) = "ready"
    [] e = "result"   -> St(c) = "working" /\ head[c] < MaxHead
    [] e = "head"     -> St(c) \in {"review", "merging"} /\ PRKind(c) /\ head[c] < MaxHead
    [] e = "merge"    -> St(c) = "review" /\ PRKind(c) /\ Accepted(c) /\ CIOk(c)
    [] e = "rework"   -> St(c) = "review" /\ head[c] < MaxHead
    [] e = "land"     -> \/ St(c) = "merging"
                         \/ St(c) \in {"waiting", "ready", "working"} /\ PRKind(c)
                         \/ Broken = "resurrect" /\ St(c) = "done" /\ PRKind(c)
    [] e = "complete" -> St(c) = "review" /\ ~PRKind(c) /\ Accepted(c)
    [] e = "cancel"   -> St(c) \in Open
    [] e = "depfail"  -> St(c) = "waiting"
\* the guard that reads other cards, in snapshot S
CrossOK(S, c, e) == e = "depfail" => DepFailedIn(S, c)

GuardsPre(B) == \A p \in B : OwnOK(p[1], p[2]) /\ CrossOK(Pre, p[1], p[2])

\* the witness: events applied one card at a time, each guard reading the
\* array as the earlier events of the same batch left it
Mix(B, Done) == [st |-> [c \in Cards |-> IF c \in Done THEN To(c, Of(B, c)) ELSE St(c)],
                 oc |-> [c \in Cards |-> IF c \in Done THEN NewOutcome(c, Of(B, c)) ELSE outcome[c]]]
Orders(D) == {s \in [1..Cardinality(D) -> D] : \A i, j \in DOMAIN s : i /= j => s[i] /= s[j]}
GuardsOneAtATime(B) ==
  \E s \in Orders(Ids(B)) :
    \A i \in DOMAIN s :
      OwnOK(s[i], Of(B, s[i])) /\ CrossOK(Mix(B, {s[j] : j \in 1..(i - 1)}), s[i], Of(B, s[i]))

ApplyEvents(B) ==
  /\ OnePerCard(B)
  /\ IF Broken = "sequential" THEN GuardsOneAtATime(B) ELSE GuardsPre(B)
  /\ onePre' = (onePre /\ GuardsPre(B))
  /\ LET D  == Ids(B)
         np == [c \in Cards |-> IF c \in D THEN <<Stream[c], To(c, Of(B, c))>> ELSE place[c]]
     IN /\ place' = np /\ cells' = Recell(D, np)
        /\ outcome' = [c \in Cards |-> IF c \in D THEN NewOutcome(c, Of(B, c)) ELSE outcome[c]]
        /\ head' = [c \in Cards |-> IF c \in D /\ Of(B, c) \in {"result", "head"}
                                      THEN head[c] + 1 ELSE head[c]]
        \* a new head or a rework invalidates the evidence taken before it
        /\ evidence' = [c \in Cards |-> IF c \in D /\ Of(B, c) \in {"result", "head", "rework"}
                                          THEN {} ELSE evidence[c]]
        /\ Commit(D)
  /\ UNCHANGED <<digest, kind, deps, succ, epoch>>

EventChoices == {<<c, e>> : c \in AdmittedIds, e \in Events}
ApplyAny == \E B \in Small({p \in EventChoices : OwnOK(p[1], p[2])}) : ApplyEvents(B)

----------------------------------------------------------------------------
(* RecordEvidence: an array of evidence records, each bound to the head    *)
(* and digest it was taken at, for cards in review. A record at another    *)
(* head or digest refuses the batch, as does a second record from one      *)
(* issuer for one card at one head. Evidence recorded in a batch cannot   *)
(* authorize a transition in that batch: a batch is one operation.        *)

Issue(c) == IF PRKind(c) THEN Issuers ELSE Reviewers
Disposition(i) == IF i = "ci" THEN "ok" ELSE "accept"
\* candidates at every head: the guard, not the enumeration, does the refusing
Offered == UNION {{<<c, <<i, Disposition(i), h, digest[c]>>>> : i \in Issue(c), h \in 1..MaxHead} :
                   c \in {x \in AdmittedIds : St(x) = "review"}}

EvidenceOK(c, ev) ==
  /\ St(c) = "review"
  /\ ev[4] = digest[c]
  /\ (Broken = "stale" \/ ev[3] = head[c])
  /\ ~\E old \in evidence[c] : old[1] = ev[1] /\ old[3] = ev[3]

RecordEvidence(B) ==
  /\ \A p, q \in B : p[1] = q[1] /\ p[2][1] = q[2][1] => p = q
  /\ \A p \in B : EvidenceOK(p[1], p[2])
  /\ LET D == Ids(B) IN
     /\ evidence' = [c \in Cards |-> evidence[c] \cup {p[2] : p \in {q \in B : q[1] = c}}]
     /\ Commit(D)
  /\ UNCHANGED <<cells, place, digest, kind, deps, outcome, succ, head, epoch, onePre>>

RecordAny == \E B \in Small(Offered) : RecordEvidence(B)

----------------------------------------------------------------------------
(* ReplaceDefinitions: pairs of an admitted non-working, non-terminal old  *)
(* card and a fresh id. In one commit every old card ends done/replaced    *)
(* with its successor named, and every successor is admitted in waiting.   *)
(* Dependants keep their declared ids: nothing follows the link.           *)

Replaceable == {c \in AdmittedIds : St(c) \in {"waiting", "ready", "review", "merging"}}

ReplaceDefinitions(P) ==
  /\ \A p, q \in P : (p[1] = q[1] \/ p[2] = q[2]) => p = q
  /\ LET Old == {p[1] : p \in P}
         New == {p[2] : p \in P}
     IN IF Broken = "digest"
        \* the witness: the successor's definition installed on the old member
        THEN /\ digest' = [c \in Cards |-> IF c \in Old THEN Def[Of(P, c)] ELSE digest[c]]
             /\ Commit(Old)
             /\ UNCHANGED <<cells, place, kind, deps, outcome, succ, head, evidence, epoch, onePre>>
        ELSE LET np == [c \in Cards |-> IF c \in Old THEN <<Stream[c], "done">>
                                        ELSE IF c \in New THEN <<Stream[c], "waiting">>
                                        ELSE place[c]]
             IN /\ place' = np /\ cells' = Recell(Old \cup New, np)
                /\ outcome' = [c \in Cards |-> IF c \in Old THEN "replaced" ELSE outcome[c]]
                /\ succ' = [c \in Cards |-> IF c \in Old THEN Of(P, c) ELSE succ[c]]
                /\ digest' = [c \in Cards |-> IF c \in New THEN Def[c] ELSE digest[c]]
                /\ kind' = [c \in Cards |-> IF c \in New THEN Kind[c] ELSE kind[c]]
                /\ deps' = [c \in Cards |-> IF c \in New THEN Deps[c] ELSE deps[c]]
                /\ Commit(Old \cup New)
                /\ UNCHANGED <<head, evidence, epoch, onePre>>

ReplaceAny == \E P \in Small(Replaceable \X Fresh) : ReplaceDefinitions(P)

----------------------------------------------------------------------------
(* Refuse: a batch whose epoch, table revision, card revisions, places,    *)
(* identities, bounds or guards fail against the pre-state writes nothing. *)
Refuse == UNCHANGED vars

Next ==
  \/ \E A \in Small(IF Broken = "secondplace" THEN Cards ELSE Fresh) : AdmitBatch(A)
  \/ ResolveAny
  \/ ApplyAny
  \/ RecordAny
  \/ ReplaceAny
  \/ \E E \in Small({c \in AdmittedIds : St(c) = "ready"}) : ReturnSet(E)
  \/ Refuse

\* the coordinator invokes resolve over the complete scope fairly; nothing
\* else is promised (no daemon, no timer)
Spec == Init /\ [][Next]_vars /\ WF_vars(ResolveAny)

\* No guard reads a revision counter: the model's refusals of stale expected
\* revisions are the Refuse stutter. States that differ only in rev and
\* tableRev therefore have the same successors up to those counters, and
\* TLC keeps one of them (the VIEW of every instance).
View == <<cells, place, digest, kind, deps, outcome, succ, head, evidence, epoch, onePre>>

----------------------------------------------------------------------------
(* What must always hold *)

\* an admitted card sits in exactly one cell, in its stream's row, and the
\* record's reverse index names that cell; an unadmitted id sits nowhere
OnePlace ==
  \A c \in Cards :
    LET at == {x \in Cells : c \in cells[x]} IN
    IF Admitted(c) THEN at = {place[c]} /\ place[c][1] = Stream[c] ELSE at = {}

\* admission created record and placement together
AdmittedTogether ==
  \A c \in Cards : Admitted(c) <=> (digest[c] /= None /\ rev[c] >= 1)

\* the admitted definition is the committed one under that id, for good
DigestPinned ==
  \A c \in AdmittedIds : digest[c] = Def[c] /\ kind[c] = Kind[c] /\ deps[c] = Deps[c]

\* ready, and every place after it on the line, only with dependencies met
ReadyImpliesDepsMet == \A c \in Cards : St(c) \in Line => DepsMet(c)

\* live evidence is bound to the card's current head and pinned digest
EvidenceBoundToHead ==
  \A c \in Cards : \A ev \in evidence[c] : ev[3] = head[c] /\ ev[4] = digest[c]

\* merging only with the quorum of accepting reads and CI at the exact head
MergingIsAuthorized == \A c \in Cards : St(c) = "merging" => Accepted(c) /\ CIOk(c)

\* done carries an outcome, and only done; replaced names an admitted successor
OutcomeOnDone ==
  \A c \in Cards :
    /\ (St(c) = "done") <=> (outcome[c] /= None)
    /\ (outcome[c] = "replaced") <=> (succ[c] /= None)
    /\ succ[c] /= None => succ[c] /= c /\ Admitted(succ[c])

\* every accepted batch's guards held in its one pre-state (the audit)
OnePreState == onePre

----------------------------------------------------------------------------
(* What must hold of every step *)

\* waiting is entered only by admission: nothing returns to it
NoReadyWaitingOscillation ==
  [][\A c \in Cards : St(c)' = "waiting" => St(c) \in {"none", "waiting"}]_vars

\* a landed or done card's record never changes again
TerminalIsQuiet ==
  [][\A c \in Cards : St(c) \in Terminal =>
       UNCHANGED <<place[c], digest[c], kind[c], deps[c], rev[c], outcome[c],
                   succ[c], head[c], evidence[c]>>]_vars

\* a card's revision moves by exactly one when its record changes and never
\* otherwise; the table's by one per accepted batch; the epoch is never written
Record(c) == <<place[c], digest[c], kind[c], deps[c], outcome[c], succ[c], head[c], evidence[c]>>
RevisionMonotone ==
  [][/\ \A c \in Cards : rev'[c] = IF Record(c)' /= Record(c) THEN rev[c] + 1 ELSE rev[c]
     /\ tableRev' = IF \E c \in Cards : Record(c)' /= Record(c) THEN tableRev + 1 ELSE tableRev
     /\ epoch' = epoch]_vars

----------------------------------------------------------------------------
(* What must eventually happen *)

\* a waiting card whose dependencies are met leaves waiting, for ready unless
\* the coordinator ended it first (landed, cancelled, replaced)
MetWaitingBecomesReady ==
  \A c \in Cards : (St(c) = "waiting" /\ DepsMet(c)) ~> (St(c) \in {"ready", "landed", "done"})

=============================================================================
