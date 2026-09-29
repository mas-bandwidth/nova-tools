----------------------------- MODULE CardManager -----------------------------
(***************************************************************************)
(* The card layer: cards are plain data, and one manager transforms the    *)
(* ARRAY of cards. Every card variable below is a function from card ids   *)
(* to a property; there is no card object and no per-card action. Each     *)
(* manager action is one batch: one server call that reads one pre-state, *)
(* checks every guard against it, and commits every change together with   *)
(* one table revision and one receipt. An accepted batch that changes no   *)
(* card still advances the revision and writes its receipt; a refused      *)
(* batch writes nothing (the Refuse stutter). A single card is the         *)
(* degenerate batch of one.                                                *)
(*                                                                         *)
(* The contract is mas-bandwidth/ideas#825 with its rulings:               *)
(*  - identity is card:<id>; the member is created by admission and never  *)
(*    re-keyed; replacement is a NEW admission under a new id, the old     *)
(*    member ending done/replaced with a successor link;                   *)
(*  - an admitted definition (digest, kind, dependencies) never changes;   *)
(*  - a dependency is met when landed, or done/completed for a kind that   *)
(*    carries no PR; replaced, cancelled and dependency-failed never count;*)
(*  - evidence is bound to the head and digest it was taken at; a new head *)
(*    or a rework invalidates it; a negative record at the head (a         *)
(*    rejecting read, red CI, a queue rejection) blocks, and is recorded   *)
(*    beside any acceptance, never replaced by one.                        *)
(*                                                                         *)
(* The table under the card array is abstracted to its one-place contract *)
(* (MemberTable.tla, EpochMemberTable.tla) and the batch contract of       *)
(* SPEC-NOVA-TABLE: cells hold member sets per epoch, place is the member  *)
(* record's reverse index, a card's row is its stream. The epoch advances  *)
(* from outside (the table layer); the manager never writes it, a request  *)
(* carries the epoch the coordinator observed, and a card of an earlier    *)
(* epoch is out of every batch's reach.                                    *)
(*                                                                         *)
(* Ground truth sits beside the records: truth is the head the PR (or the  *)
(* result artifact) is really at, pushed from outside; Obs is the oracle   *)
(* of observations readers, CI and the merge queue really make, an         *)
(* instance constant (a scenario with rejections, red and green runs of    *)
(* CI at one head, a reader changing its mind, a queue rejection). The     *)
(* manager records only what the verifier finds in Obs.                    *)
(*                                                                         *)
(* Left out, and held elsewhere or by tests: operation records and replay  *)
(* of an operation id (the table batch model's receipt ledger), request    *)
(* revisions other than the epoch (their staleness is the Refuse stutter), *)
(* reader eligibility beyond distinct recorded identities (author          *)
(* exclusion, a required-reader set), and dispatch: jobs, slots, leases,   *)
(* workers.                                                                *)
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
  Obs,        \* [Cards -> SUBSET Evidence]: the observations really made
  Quorum,     \* accepting reads required at the exact head
  MaxHead,    \* bound on a card's true head (pushes and results)
  MaxEpoch,   \* bound on the table's epoch
  BatchMax,   \* bound on the entries of one batch (1..3)
  Broken      \* "none", or the misimplementation a reversed witness turns on

ASSUME BatchMax \in 1..3 /\ MaxHead \in Nat /\ Quorum \in Nat /\ MaxEpoch \in Nat \ {0}
ASSUME Broken \in {"none", "secondplace", "digest", "deps", "stale", "sequential",
                   "return", "resurrect", "lastwins", "forged", "staleepoch",
                   "noopsilent"}

States   == {"waiting", "ready", "working", "review", "merging", "landed", "done"}
Open     == {"waiting", "ready", "working", "review", "merging"}
Line     == {"ready", "working", "review", "merging"}
Terminal == {"landed", "done"}
Outcomes == {"completed", "cancelled", "depfailed", "replaced"}
Epochs   == 1..MaxEpoch
Cells    == Epochs \X Streams \X States
NoPlace  == <<0, "-", "-">>
None     == "-"
Issuers  == Reviewers \cup {"ci", "queue"}
Positive == {"accept", "ok"}
Negative == {"reject", "red"}
Disps(i) == IF i = "ci" THEN {"ok", "red"}
            ELSE IF i = "queue" THEN {"reject"} ELSE {"accept", "reject"}
Digests  == {Def[c] : c \in Cards}
Evidence == Issuers \X (Positive \cup Negative) \X (0..MaxHead) \X Digests

VARIABLES
  cells,     \* [Cells -> SUBSET Cards]         the table's owned cells, per epoch
  place,     \* [Cards -> Cells \cup {NoPlace}] the member record's reverse index
  digest,    \* [Cards -> Digests \cup {None}]  the pinned definition digest
  kind,      \* [Cards -> kinds \cup {None}]    KIND, pinned at admission
  deps,      \* [Cards -> SUBSET Cards]         DEPENDS-ON, pinned at admission
  rev,       \* [Cards -> Nat]                  the member revision
  outcome,   \* [Cards -> Outcomes \cup {None}] set on done
  succ,      \* [Cards -> Cards \cup {None}]    the successor of a replaced card
  head,      \* [Cards -> 0..MaxHead]           the head the card's record is at
  evidence,  \* [Cards -> SUBSET Evidence]      live evidence records
  truth,     \* [Cards -> 0..MaxHead]           ground truth: the head really pushed
  tableRev,  \* the table revision: once per accepted batch, no-op included
  receipts,  \* receipts written: one per accepted batch
  epoch,     \* the table's active epoch: advanced from outside, never by the manager
  seen,      \* the epoch the coordinator's requests carry (its context)
  onePre     \* audit: every accepted batch's guards held in its one pre-state

cardVars == <<cells, place, digest, kind, deps, rev, outcome, succ, head, evidence>>
vars == <<cardVars, truth, tableRev, receipts, epoch, seen, onePre>>

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
  /\ truth \in [Cards -> 0..MaxHead]
  /\ tableRev \in Nat /\ receipts \in Nat
  /\ epoch \in Epochs /\ seen \in Epochs /\ onePre \in BOOLEAN

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
  /\ truth = [c \in Cards |-> 0]
  /\ tableRev = 0 /\ receipts = 0
  /\ epoch = 1 /\ seen = 1 /\ onePre = TRUE

----------------------------------------------------------------------------
(* Reading the array *)

Admitted(c) == place[c] /= NoPlace
St(c)       == IF place[c] = NoPlace THEN "none" ELSE place[c][3]
EpochOf(c)  == place[c][1]
Fresh       == {c \in Cards : ~Admitted(c) /\ digest[c] = None}

\* The epoch a request is served in: the active one. The stale-epoch witness
\* serves the epoch the request carries instead.
Serve     == IF Broken = "staleepoch" THEN seen ELSE epoch
\* A request carrying another epoch than the active one refuses whole.
Current   == Broken = "staleepoch" \/ seen = epoch
Active(c) == Admitted(c) /\ EpochOf(c) = Serve
ActiveIds == {c \in Cards : Active(c)}

\* a snapshot of the array's placements and outcomes: the pre-state every
\* guard reads (Pre), or, in the one-at-a-time witness, a partial update
Pre == [st |-> [c \in Cards |-> St(c)], oc |-> outcome]
\* a dependency is read in its dependant's own epoch
MetIn(S, c, d) ==
  /\ Admitted(d) /\ EpochOf(d) = EpochOf(c)
  /\ \/ S.st[d] = "landed"
     \/ S.st[d] = "done" /\ S.oc[d] = "completed" /\ kind[d] \in NonPR
DepsMetIn(S, c)   == \A d \in deps[c] : MetIn(S, c, d)
DepFailedIn(S, c) == \E d \in deps[c] : /\ Admitted(d) /\ EpochOf(d) = EpochOf(c)
                                         /\ S.st[d] = "done"
                                         /\ S.oc[d] \in {"cancelled", "depfailed", "replaced"}
DepsMet(c) == DepsMetIn(Pre, c)

PRKind(c) == kind[c] \notin NonPR
AtHead(c) == {e \in evidence[c] : e[3] = head[c] /\ e[4] = digest[c]}
Accepts(c) == {r \in Reviewers : <<r, "accept", head[c], digest[c]>> \in evidence[c]}
CIOk(c)    == <<"ci", "ok", head[c], digest[c]>> \in evidence[c]
\* a negative record at the head blocks, whatever acceptance sits beside it
Blocked(c) == \E e \in AtHead(c) : e[2] \in Negative
Authorized(c) ==
  /\ Cardinality(Accepts(c)) >= Quorum /\ ~Blocked(c)
  /\ PRKind(c) => CIOk(c)

\* at most BatchMax entries drawn from S (a batch is a set, never a loop)
Small(S) == {{p} : p \in S}
            \cup (IF BatchMax >= 2 THEN {{p, q} : p, q \in S} ELSE {})
            \cup (IF BatchMax >= 3 THEN {{p, q, r} : p, q, r \in S} ELSE {})
OnePerCard(B) == \A p, q \in B : p[1] = q[1] => p = q
Ids(B) == {p[1] : p \in B}
Of(B, c) == (CHOOSE p \in B : p[1] = c)[2]

\* one accepted batch: every changed card's revision once, the table's
\* revision once, one receipt
Commit(D) ==
  /\ rev' = [c \in Cards |-> IF c \in D THEN rev[c] + 1 ELSE rev[c]]
  /\ tableRev' = tableRev + 1
  /\ receipts' = receipts + 1
\* cells follow the records in the same commit
Recell(D, np) == [x \in Cells |-> (cells[x] \ D) \cup {c \in D : np[c] = x}]
Cell(c, s) == <<Serve, Stream[c], s>>

----------------------------------------------------------------------------
(* AdmitBatch: every selected committed definition is created in waiting   *)
(* together, in the active epoch, record and placement in one commit; an  *)
(* id already admitted (record or placement) refuses the whole batch.     *)

AdmitBatch(A) ==
  /\ Current
  /\ A \subseteq (IF Broken = "secondplace" THEN Cards ELSE Fresh)
  /\ LET np == [c \in Cards |-> IF c \in A THEN Cell(c, "waiting") ELSE place[c]] IN
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
  /\ UNCHANGED <<outcome, succ, head, evidence, truth, epoch, seen, onePre>>

----------------------------------------------------------------------------
(* ResolveSet: over a declared scope, the waiting cards whose dependencies *)
(* are met in the pre-state move to ready together. The complete scope is  *)
(* the set of every eligible card; a narrower scope is a subset of it.     *)

Eligible == {c \in ActiveIds : St(c) = "waiting" /\ (Broken = "deps" \/ DepsMet(c))}

ResolveSet(E) ==
  /\ Current
  /\ E /= {} /\ E \subseteq Eligible
  /\ LET np == [c \in Cards |-> IF c \in E THEN Cell(c, "ready") ELSE place[c]] IN
     /\ place' = np /\ cells' = Recell(E, np)
  /\ Commit(E)
  /\ UNCHANGED <<digest, kind, deps, outcome, succ, head, evidence, truth,
                 epoch, seen, onePre>>

ResolveAny == \E E \in Small(Eligible) : ResolveSet(E)

\* the witness for Ready means READY: a ready card with no taker is sent
\* back to waiting (the old deal-return)
ReturnSet(E) ==
  /\ Broken = "return" /\ Current
  /\ E /= {} /\ \A c \in E : St(c) = "ready"
  /\ LET np == [c \in Cards |-> IF c \in E THEN Cell(c, "waiting") ELSE place[c]] IN
     /\ place' = np /\ cells' = Recell(E, np)
  /\ Commit(E)
  /\ UNCHANGED <<digest, kind, deps, outcome, succ, head, evidence, truth,
                 epoch, seen, onePre>>

\* An accepted batch that changes no card (a resolve over a scope with
\* nothing eligible, an event array whose every entry is already recorded):
\* one revision and one noop receipt. The witness writes the receipt and
\* leaves the revision where it was.
NoOpBatch ==
  /\ Current
  /\ receipts' = receipts + 1
  /\ tableRev' = IF Broken = "noopsilent" THEN tableRev ELSE tableRev + 1
  /\ UNCHANGED <<cells, place, digest, kind, deps, rev, outcome, succ, head, evidence,
                 truth, epoch, seen, onePre>>

----------------------------------------------------------------------------
(* ApplyEvents: a manifest of typed events, one per card, applied as one   *)
(* transaction. Every guard reads the one pre-state; events chain across   *)
(* batches, never inside one. Two events for one card refuse (OnePerCard). *)

Events == {"start", "result", "head", "merge", "rework", "requeue", "land",
           "complete", "cancel", "depfail"}

QueueRejection(c) == <<"queue", "reject", head[c], digest[c]>>

To(c, e) ==
  CASE e = "start"    -> "working"
    [] e = "result"   -> "review"
    [] e = "head"     -> "review"
    [] e = "merge"    -> "merging"
    [] e = "rework"   -> "ready"
    [] e = "requeue"  -> "review"
    [] e = "land"     -> "landed"
    [] e \in {"complete", "cancel", "depfail"} -> "done"
NewOutcome(c, e) ==
  CASE e = "complete" -> "completed"
    [] e = "cancel"   -> "cancelled"
    [] e = "depfail"  -> "depfailed"
    [] OTHER          -> outcome[c]

\* the card's own guards (its own pre-state fields and ground truth the
\* verifier reads)
OwnOK(c, e) ==
  CASE e = "start"    -> St(c) = "ready"
    \* a bound result: a new head (or result artifact) really exists
    [] e = "result"   -> St(c) = "working" /\ truth[c] > head[c]
    \* a push since the recorded head
    [] e = "head"     -> St(c) \in {"review", "merging"} /\ truth[c] > head[c]
    [] e = "merge"    -> St(c) = "review" /\ PRKind(c) /\ Authorized(c) /\ truth[c] = head[c]
    [] e = "rework"   -> St(c) = "review" /\ head[c] < MaxHead
    \* the merge queue rejected the head: back to review at the same head,
    \* the rejection recorded
    [] e = "requeue"  -> St(c) = "merging" /\ QueueRejection(c) \in Obs[c]
    \* a verified landing: the true head, which the queue did not reject
    [] e = "land"     -> \/ St(c) = "merging" /\ truth[c] = head[c] /\ QueueRejection(c) \notin Obs[c]
                         \/ St(c) \in {"waiting", "ready", "working"} /\ PRKind(c)
                         \/ Broken = "resurrect" /\ St(c) = "done" /\ PRKind(c)
    [] e = "complete" -> St(c) = "review" /\ ~PRKind(c) /\ Authorized(c) /\ truth[c] = head[c]
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
  /\ Current
  /\ OnePerCard(B)
  /\ IF Broken = "sequential" THEN GuardsOneAtATime(B) ELSE GuardsPre(B)
  /\ onePre' = (onePre /\ GuardsPre(B))
  /\ LET D  == Ids(B)
         np == [c \in Cards |-> IF c \in D THEN Cell(c, To(c, Of(B, c))) ELSE place[c]]
     IN /\ place' = np /\ cells' = Recell(D, np)
        /\ outcome' = [c \in Cards |-> IF c \in D THEN NewOutcome(c, Of(B, c)) ELSE outcome[c]]
        \* a result, a head change or a landing records the verified head
        /\ head' = [c \in Cards |-> IF c \in D /\ Of(B, c) \in {"result", "head", "land"}
                                      THEN truth[c] ELSE head[c]]
        \* a new head or a rework invalidates the evidence taken before it;
        \* a queue rejection is recorded beside what is there
        /\ evidence' = [c \in Cards |->
                          IF c \notin D THEN evidence[c]
                          ELSE IF Of(B, c) \in {"result", "head", "rework"} THEN {}
                          ELSE IF Of(B, c) = "requeue" THEN evidence[c] \cup {QueueRejection(c)}
                          ELSE evidence[c]]
        /\ Commit(D)
  /\ UNCHANGED <<digest, kind, deps, succ, truth, epoch, seen>>

EventChoices == {<<c, e>> : c \in ActiveIds, e \in Events}
ApplyAny == \E B \in Small({p \in EventChoices : OwnOK(p[1], p[2])}) : ApplyEvents(B)

----------------------------------------------------------------------------
(* RecordEvidence: an array of evidence records for cards in review, each  *)
(* bound to the head and digest it was taken at and found by the verifier  *)
(* among the observations really made. A record at another head or digest *)
(* refuses the batch, as does one Obs does not hold, one already         *)
(* recorded, and an acceptance from an issuer whose rejection at that head *)
(* is recorded (the remedy is a new head or a rework). A rejection after   *)
(* an acceptance is recorded beside it and blocks. Evidence recorded in a *)
(* batch cannot authorize a transition in that batch: a batch is one       *)
(* operation.                                                              *)

Issue(c) == IF PRKind(c) THEN Reviewers \cup {"ci"} ELSE Reviewers
\* the candidates the coordinator can submit: what it read of the
\* observations (the forged witness submits anything); the guard does the
\* refusing
Candidates(c) ==
  IF Broken = "forged"
    THEN UNION {{<<i, d, head[c], digest[c]>> : d \in Disps(i)} : i \in Issue(c)}
    ELSE Obs[c]
Offered == UNION {{<<c, e>> : e \in Candidates(c)} :
                   c \in {x \in ActiveIds : St(x) = "review"}}

EvidenceOK(c, e) ==
  /\ St(c) = "review"
  /\ e[1] \in Issue(c) /\ e[2] \in Disps(e[1])
  /\ e[4] = digest[c]
  /\ (Broken = "stale" \/ e[3] = head[c])
  /\ (Broken = "forged" \/ e \in Obs[c])
  /\ e \notin evidence[c]
  /\ (e[2] \in Positive /\ Broken /= "lastwins") =>
        ~\E o \in evidence[c] : o[1] = e[1] /\ o[3] = e[3] /\ o[2] \in Negative

RecordEvidence(B) ==
  /\ Current
  /\ \A p, q \in B : p[1] = q[1] /\ p[2][1] = q[2][1] => p = q
  /\ \A p \in B : EvidenceOK(p[1], p[2])
  /\ LET D == Ids(B)
         New(c) == {p[2] : p \in {q \in B : q[1] = c}}
         \* the witness: a later record from an issuer replaces its earlier
         \* one at that head
         Kept(c) == IF Broken = "lastwins"
                      THEN {o \in evidence[c] : ~\E n \in New(c) : n[1] = o[1] /\ n[3] = o[3]}
                      ELSE evidence[c]
     IN /\ evidence' = [c \in Cards |-> Kept(c) \cup New(c)]
        /\ Commit(D)
  /\ UNCHANGED <<cells, place, digest, kind, deps, outcome, succ, head, truth,
                 epoch, seen, onePre>>

RecordAny == \E B \in Small({p \in Offered : EvidenceOK(p[1], p[2])}) : RecordEvidence(B)

----------------------------------------------------------------------------
(* ReplaceDefinitions: pairs of an admitted non-working, non-terminal old  *)
(* card and a fresh id. In one commit every old card ends done/replaced    *)
(* with its successor named, and every successor is admitted in waiting.   *)
(* Dependants keep their declared ids: nothing follows the link.           *)

Replaceable == {c \in ActiveIds : St(c) \in {"waiting", "ready", "review", "merging"}}

ReplaceDefinitions(P) ==
  /\ Current
  /\ \A p, q \in P : (p[1] = q[1] \/ p[2] = q[2]) => p = q
  /\ LET Old == {p[1] : p \in P}
         New == {p[2] : p \in P}
     IN IF Broken = "digest"
        \* the witness: the successor's definition installed on the old member
        THEN /\ digest' = [c \in Cards |-> IF c \in Old THEN Def[Of(P, c)] ELSE digest[c]]
             /\ Commit(Old)
             /\ UNCHANGED <<cells, place, kind, deps, outcome, succ, head, evidence,
                            truth, epoch, seen, onePre>>
        ELSE LET np == [c \in Cards |-> IF c \in Old THEN Cell(c, "done")
                                        ELSE IF c \in New THEN Cell(c, "waiting")
                                        ELSE place[c]]
             IN /\ place' = np /\ cells' = Recell(Old \cup New, np)
                /\ outcome' = [c \in Cards |-> IF c \in Old THEN "replaced" ELSE outcome[c]]
                /\ succ' = [c \in Cards |-> IF c \in Old THEN Of(P, c) ELSE succ[c]]
                /\ digest' = [c \in Cards |-> IF c \in New THEN Def[c] ELSE digest[c]]
                /\ kind' = [c \in Cards |-> IF c \in New THEN Kind[c] ELSE kind[c]]
                /\ deps' = [c \in Cards |-> IF c \in New THEN Deps[c] ELSE deps[c]]
                /\ Commit(Old \cup New)
                /\ UNCHANGED <<head, evidence, truth, epoch, seen, onePre>>

ReplaceAny == \E P \in Small(Replaceable \X Fresh) : ReplaceDefinitions(P)

----------------------------------------------------------------------------
(* Refuse: a batch whose epoch, table revision, card revisions, places,    *)
(* identities, bounds or guards fail against the pre-state writes nothing. *)
(* It is the stuttering step [Next]_vars already allows, so Next does not  *)
(* list it (TLC would only generate each state again).                     *)
Refuse == UNCHANGED vars

----------------------------------------------------------------------------
(* The outside: none of these is the manager's. *)

\* a push to the PR, or a new result artifact, for a card being worked on
Push(c) ==
  /\ Admitted(c) /\ EpochOf(c) = epoch /\ truth[c] < MaxHead
  /\ St(c) \in {"working", "review"} \cup (IF PRKind(c) THEN {"merging"} ELSE {})
  /\ truth' = [truth EXCEPT ![c] = @ + 1]
  /\ UNCHANGED <<cardVars, tableRev, receipts, epoch, seen, onePre>>

\* the table layer advances the epoch; the coordinator observes it
EpochAdvance ==
  /\ epoch < MaxEpoch
  /\ epoch' = epoch + 1
  /\ UNCHANGED <<cardVars, truth, tableRev, receipts, seen, onePre>>
Observe ==
  /\ seen /= epoch
  /\ seen' = epoch
  /\ UNCHANGED <<cardVars, truth, tableRev, receipts, epoch, onePre>>

Next ==
  \/ \E A \in Small(IF Broken = "secondplace" THEN Cards ELSE Fresh) : AdmitBatch(A)
  \/ ResolveAny
  \/ ApplyAny
  \/ RecordAny
  \/ ReplaceAny
  \/ NoOpBatch
  \/ \E E \in Small({c \in ActiveIds : St(c) = "ready"}) : ReturnSet(E)
  \/ \E c \in Cards : Push(c)
  \/ EpochAdvance
  \/ Observe

\* the coordinator invokes resolve over the complete scope fairly and
\* refreshes a stale context; nothing else is promised (no daemon, no timer)
Spec == Init /\ [][Next]_vars /\ WF_vars(ResolveAny) /\ WF_vars(Observe)

\* The VIEW of every instance, what TLC fingerprints a state by. Two
\* reductions, each sound because what it leaves out is read by no guard
\* and changed by no action of the correct model:
\*  - the counters rev, tableRev and receipts: the refusals of stale
\*    expected revisions are the Refuse stutter; the properties about the
\*    counters are action properties, checked on every step;
\*  - a card of an epoch before the active one: no batch reaches it, no push
\*    or observation touches it, and dependencies are read in a card's own
\*    epoch, so its record is frozen and never read again; the view keeps
\*    only that it is there. Its invariants were checked while it was
\*    active, and a witness that writes to it breaks EpochFenced on the step.
Frozen(c) == Admitted(c) /\ EpochOf(c) /= epoch
ViewCard(c) == IF Frozen(c) THEN <<"frozen", EpochOf(c)>>
               ELSE <<place[c], digest[c], kind[c], deps[c], outcome[c], succ[c],
                      head[c], evidence[c], truth[c]>>
View == <<[c \in Cards |-> ViewCard(c)],
          [x \in {y \in Cells : y[1] = epoch} |-> cells[x]],
          epoch, seen, onePre>>

----------------------------------------------------------------------------
(* What must always hold *)

\* an admitted card sits in exactly one cell, in its admission epoch and its
\* stream's row, and the record's reverse index names that cell; an
\* unadmitted id sits nowhere
OnePlace ==
  \A c \in Cards :
    LET at == {x \in Cells : c \in cells[x]} IN
    IF Admitted(c) THEN at = {place[c]} /\ place[c][2] = Stream[c] ELSE at = {}

\* admission created record and placement together
AdmittedTogether ==
  \A c \in Cards : Admitted(c) <=> (digest[c] /= None /\ rev[c] >= 1)

\* the admitted definition is the committed one under that id, for good
DigestPinned ==
  \A c \in Cards : Admitted(c) => digest[c] = Def[c] /\ kind[c] = Kind[c] /\ deps[c] = Deps[c]

\* ready, and every place after it on the line, only with dependencies met
ReadyImpliesDepsMet == \A c \in Cards : St(c) \in Line => DepsMet(c)

\* live evidence is bound to the card's current head and pinned digest
EvidenceBoundToHead ==
  \A c \in Cards : \A e \in evidence[c] : e[3] = head[c] /\ e[4] = digest[c]

\* every recorded observation was really made
EvidenceIsTrue == \A c \in Cards : evidence[c] \subseteq Obs[c]

\* the recorded head was really pushed; a landing records the true head
HeadNotAhead == \A c \in Cards : head[c] <= truth[c]
LandedAtTrueHead == \A c \in Cards : St(c) = "landed" => head[c] = truth[c]

\* merging only with the quorum of accepting reads and CI at the exact head
\* and no negative record there
MergingIsAuthorized == \A c \in Cards : St(c) = "merging" => Authorized(c)

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

Record(c) == <<place[c], digest[c], kind[c], deps[c], outcome[c], succ[c], head[c], evidence[c]>>
Changed(c) == Record(c)' /= Record(c)

\* waiting is entered only by admission: nothing returns to it
NoReadyWaitingOscillation ==
  [][\A c \in Cards : St(c)' = "waiting" => St(c) \in {"none", "waiting"}]_vars

\* a landed or done card's record never changes again
TerminalIsQuiet ==
  [][\A c \in Cards : St(c) \in Terminal => UNCHANGED <<Record(c), rev[c]>>]_vars

\* a card's revision moves by exactly one when its record changes and never
\* otherwise; every receipt (an accepted batch, a no-op included) moves the
\* table revision by exactly one, and nothing else moves it; a change to a
\* card comes with a receipt; the epoch only ever advances by one
RevisionMonotone ==
  [][/\ \A c \in Cards : rev'[c] = IF Changed(c) THEN rev[c] + 1 ELSE rev[c]
     /\ receipts' \in {receipts, receipts + 1}
     /\ tableRev' = IF receipts' = receipts + 1 THEN tableRev + 1 ELSE tableRev
     /\ (\E c \in Cards : Changed(c)) => receipts' = receipts + 1
     /\ epoch' \in {epoch, epoch + 1}]_vars

\* only cards of the active epoch change, and admission is into it: a
\* request carrying an earlier epoch reaches nothing
EpochFenced ==
  [][\A c \in Cards : Changed(c) =>
       /\ place'[c][1] = epoch
       /\ (place[c] = NoPlace \/ place[c][1] = epoch)]_vars

\* at one head, a record is never removed except by a rework: nothing
\* replaces an earlier record, negative or positive
EvidenceAppendOnly ==
  [][\A c \in Cards : (head'[c] = head[c] /\ St(c)' /= "ready") =>
                        evidence[c] \subseteq evidence'[c]]_vars

----------------------------------------------------------------------------
(* What must eventually happen *)

\* a waiting card of the active epoch whose dependencies are met leaves
\* waiting, for ready unless the coordinator ended it first (landed,
\* cancelled, replaced) or the epoch moved on
MetWaitingBecomesReady ==
  \A c \in Cards :
    (St(c) = "waiting" /\ Admitted(c) /\ EpochOf(c) = epoch /\ DepsMet(c))
      ~> (St(c) \in {"ready", "landed", "done"} \/ EpochOf(c) /= epoch)

=============================================================================
