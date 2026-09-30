---------------------------- MODULE SprintEvents ----------------------------
\* The upper layers of nova-sprint (layers 3 to 8 of
\* design/EVENT-DRIVEN-TICK-v2.1.md, "the design" below, cited by section):
\* the log turned into keys (1.1), due times in running time (1.2), the rules
\* with their indexes, intents and judgments (1.3, 2), the tick (1.4), the
\* verbs (1.5, 3), written from section 5 of the design with plan and apply
\* as separate actions: a plan records what it read and what it intends; an
\* apply checks its guards on the present state and computes its intents
\* from the present state.
\*
\* WHAT IT STANDS ON. Layers 1 and 2 are assumed, as their own models prove
\* them (tla/SetTable.tla, the table, and tla/SetTableLog.tla, the log, on
\* their own branches): one call at a time, a step all or nothing, guards
\* checked at apply, one place per card and table, a revision that moves
\* with every change of a record, a receipt that makes a part identity apply
\* once, one gapless line per change in the step's own commit, a function
\* that errors keeping the writes it made before the error (fact F1). So a
\* step here is one atomic action, a revision guard is "the record is as
\* read", and a refusal writes nothing. What those models found about layers
\* 1 and 2 is theirs and is not repeated here.
\*
\* THE STATE (variables, section 5's table, as far as this model keeps it).
\*   col, score, fld     work table: card -> column, score, the fields the rules read
\*   wk                  a primary's live work card: its places (a set, so that
\*                       a card dealt twice is a state OnePlace can see), gen,
\*                       taken, untaken_replaced, and the running-time timers of
\*                       its two card kinds, each as a field and as its due entry
\*   rd                  the read cards of a primary's attempt, by reader, with the
\*                       timer of their one deadline (unbegun and unreported as one)
\*   mi                  a stream's merge-idle timer and entry
\*   status, beat, seen  a member's control card; its beat:m entry; its seen:m entry
\*   waitn, missing      the indexes written by intents (wait:n, missing); the
\*                       indexes that are functions of the fields (sent, elig,
\*                       fresh, again) are operators, since X derives them in the
\*                       step that changes the card, from its real before-state
\*   J                   jopen: the judgments open or held, one record per
\*                       (type, subject, cause), with the hold's entry
\*   agenda, parked      the rule keys queued; the parked keys
\*   log, cur            the lines not yet dropped (each with the keys ingest
\*                       gives it and how often it was ingested) and the cursor
\*   running, clk        RUNNING or STOPPED; R17's fields (due_since, stophold,
\*                       raised in this span), all in wall time
\*   stab, sw            with the stablesince repair only (H12): a member's
\*                       stable_since as a timer (up, not yet stable, stable),
\*                       and R6's 30 s entry (unset, armed, due, fired)
\*   remind, pushes      the remind entry of one person's goal; pushes (mod 2)
\*   dropping, cut       stream -> op; op -> its cut entry (wall time)
\*   receipts, next      part receipts with their continuations; the score counter
\*   dcur, acur          the deal's and the ask's rolling index (errata 3,
\*                       amendment 5), the fleet table's and the readers
\*                       table's property: the place in MemSeq (ReaderSeq) the
\*                       next scan starts at, past the member (reader) the
\*                       last card (read) went to
\*   lease, tk, vk       the lease; tick processes; verb processes
\*
\* TIME is relative, in units of one deadline: every timer holds what remains
\* until it is due (1 or 0; -1 when unset). Wall advances time: running-time
\* timers and entries move only while RUNNING, wall-time ones (cut entries,
\* R17's fields) always. So the state stays finite however long a behaviour
\* runs, and a deadline passing is an event with fairness.
\*
\* NOT MODELLED (the same list as tla/README-SprintEvents.md, "What is not
\* modelled", where each is argued):
\* - R5 (cross), R7 (level), R12 (overdue), R16 (held) and R18 (behind).
\*   R15 (done) is modelled with donestop only (errata 3 amendment 6): its
\*   notice and the tick-end count of it are not; its stop is. R16's table is the invariant `NothingSilent` instead (with a
\*   row for R6's 30 s entry under `stablesince`); the held queue with its
\*   cap, and `HeldDrop`, are not kept.
\* - Quarantine: a refusal naming a card is a layer-1 bug, and layer 1's
\*   model shows it does not happen.
\* - Clear, epochs and remove, so `EpochSafe` is not checked; CI, return,
\*   merge stops and resume; reader add; add in parts and insertion's
\*   anchors (an insertion is an add at an odd score); `ask --another`; the
\*   coordinator's `accept` verb (R9, the machine's accept, is modelled).
\* - R11's `idle:s` kind (a notice, no judgment), and R1's strangers (a
\*   beat from a member with no fleet row).
\* - The `askwait` index, and I3 for it and for `fresh` above sigma:
\*   `HeadActionable` checks `elig` below sigma, deal's heads and `wait:n`.
\* - `NoLostWork`'s "parked (and named)": a parked key counts as owed
\*   without the check that its judgment is open. `Named` does check a
\*   parked key per card.
\* - Byte and read budgets other than the step bound; the tick's step
\*   budget (a tick applies every request it planned); R19's one step a
\*   tick.
\* - The rules' reads as separate snapshots: a tick plans every key on one
\*   snapshot, then applies request by request, with every outside action
\*   free to run between two requests.
\* - A note line that queues no key is not written.
\* - The order of two keys of one priority in the round robin is the one
\*   TLC's `CHOOSE` gives; the design leaves it open, and a trace that needs
\*   the other order needs another scenario.
\* - The row of "this card cannot be placed" (H15's judgment): its
\*   decisions are not decided; the model prints `drop` only, and the
\*   judgment is neither held nor acked.
\*
\* Broken names a reversed witness (section 5's table, W1 to W27, W28 of
\* errata 3's amendment 4, the deal's order, W29 of amendment 5, the deal's
\* member choice by the rolling index, W30 of amendment 8, no tick-end note,
\* and W31 of amendment 6, R15 not stopping the machine; W5Reach
\* is W5's reach half, breaking reach and unreach and not the verb; W7 is
\* split into W7a and W7b) that changes exactly one rule; "none" is the
\* design.
\*
\* Fixes names the repairs of the holes in the design this model found, as
\* errata 3 to version 2.1 decides them (amended 03:20, and amendment 2 at
\* 04:35; tla/README-SprintEvents.md, Findings). The design as written is
\* Fixes = {}; the goal configurations run it and fail. A configuration that
\* checks other properties on a scenario that meets a hole names the repair
\* it runs with, so that the rest is checked; each repair is one decision:
\*   judgeguard  a line that queues deal (room freed, a member up, a card
\*               ready) also queues the late key of every work card past
\*               its deadline, and a replacement closes the lateness
\*               judgment                                           (H1)
\*   seenfresh   R1 sets a member up only while its beat is above the
\*               present R (at plan and at apply), and an empty plan of
\*               R2 carries a guard-only unit on the control card as read (H2)
\*   madeclose   the add that creates n closes "blocked on something
\*               missing: n" in its own step; the row prints add n only
\*               while n has no record                              (H3)
\*   cutmark     R11's cut judgment guards that the op's marks stand (H4)
\*   spanreset   wait on the STOPPED judgment lets R17 raise it again
\*               in the span once the hold has passed               (H5)
\*   stopclose   R17 closes its judgment at a look that finds no move
\*               due (R17 is planned and applied apart, always)     (H7)
\*   stopinputs  R17's step also guards on the version of what its dry
\*               plans read: the revision of every card, as read (a
\*               counter per stream, over every stream)             (H14)
\*   stoprearm   a close of the STOPPED judgment clears raised: R17
\*               raises once per span and close                     (H16)
\*   dropcond    no decision is printed that DROPPING refuses: drop,
\*               rework, release, land, ack of a dropped, missing or
\*               refused judgment, on a card of a stream being dropped,
\*               and add n while n's stream is being dropped        (H8)
\*   downdeal    a member's down or held line also queues deal      (H10)
\*   freezefirst part 1 of drop --stream (and every later part) guards
\*               that the cells before its head hold no card of the
\*               stream; a part refused so returns the verb to its
\*               continue state, to read again (at most PartRereads) (H11)
\*   donestop    R15 (errata 3 amendment 6, the owner's ruling, not a hole):
\*               a card landing or removed, and start, queue done; with no
\*               card open and one ended, done's step stops the machine in
\*               the same call, under the guard that no card is open and it
\*               runs (DoneStops; W31: the step does not stop)
\*   stablesince R6 deals only to a member up for two beat periods; and
\*               with work ready and no deal for 30 s, the judgment
\*               "work is ready and no member is stable"            (H12)
\*   unplaced    R6's 30 s entry restarts on a take, not on a deal;
\*               a card dealt and withdrawn MaxPlaceTries times without
\*               a take raises "this card cannot be placed"         (H15)
\*   rankclose   a step after which an open card of a sentinel's stream
\*               sorts before the sentinel closes its "sentinel
\*               reached": a rank or an insertion of a card before it,
\*               or a rank of the sentinel behind an open card      (H13)
EXTENDS Integers, FiniteSets, Sequences, TLC

CONSTANTS
  Streams, Members, Readers,
  Prims,        \* primaries
  Sents,        \* sentinels
  Ticks,        \* tick processes (run loops)
  VerbProcs,    \* verb processes (people and workers running commands)
  Ops,          \* op names of a verb in parts (drop --stream)
  StreamOf,     \* card -> stream
  NeedsOf,      \* card -> the needs it names (cards)
  Ord,          \* card -> a distinct number: the id order that breaks score ties
  Addable,      \* the cards an add may create (the others exist from the start)
  AddScore,     \* card -> -1 (an integer score from the counter) or an odd score (an insertion)
  Refuses,      \* the cards deal's planner refuses (a Plan.Refused entry)
  RankTo,       \* the scores rank may choose
  Cap,          \* ready cards a member (2 in the design)
  Chunk,        \* StepChunk: the changes one plan takes
  StepBound,    \* the units one request carries, as the builder counts
  L1Bound,      \* the units layer 1 accepts: below StepBound, the builder counted wrong (LIMIT)
  MaxAttempts, MaxRedeals, MaxRereads,
  MaxPlaceTries, \* unplaced (H15): untaken withdrawals before "this card cannot be placed" (3 in the design)
  GenMod,       \* generations are kept modulo GenMod
  MaxActs,      \* outside actions (verbs) in all
  MaxCrashes,   \* tick and verb crashes in all
  MaxErrors,    \* errors between the two writes of a pop or an ingest, in all
  MaxBugs,      \* bug refusals (not LIMIT) of tick steps, in all
  MaxLease,     \* the highest lease generation (takeovers are bounded)
  Beaters,      \* the members that may beat (beats are unbounded and unfair)
  Steady,       \* the members that beat without ever stopping (their beat never goes stale)
  Menu,         \* the verbs a configuration's verb processes may run
  Probes,       \* TRUE: RunTwice probes (a second delivery of a key just removed)
  TrackSeen,    \* TRUE: keep seenKeys, for CursorSound (off, it would only multiply states)
  Scn,          \* the initial state (see Init)
  Fixes,        \* proposed repairs of the holes this model found, each off unless named (see below)
  Broken

VARIABLES
  col, score, fld, wk, rd, mi,
  status, beat, seen,
  waitn, missing, J,
  agenda, parked, log, cur,
  running, clk, remind, pushes,
  stab,         \* member -> -1 (not up), 1 (up, not yet stable), 0 (stable); only with stablesince
  sw,           \* R6's 30 s entry: -1 unset, 1 armed, 0 due, 2 fired; only with stablesince
  dropping, cut, receipts, next, dcur, acur,
  lease, tk, vk,
  acts, crashes, errs, bugs,
  probe,        \* ghost: armed by an apply that removed its key or changed nothing, for RunTwice
  seenKeys,     \* ghost: every key of a line at or before the cursor (kept only with TrackSeen)
  ahead,        \* ghost: prim -> the unlanded sentinels ahead of it at its first deal
  early,        \* ghost: a sentinel landed while an open card of its stream sorted before it
  waivedRec,    \* ghost: a missing need was waived while it had a record
  applied,      \* ghost: part identity -> times applied
  raises        \* ghost: STOPPED judgments raised in this STOPPED span (since the last
                \* close with stoprearm, or the last wait with spanreset)

cards  == <<col, score, fld, wk, rd, mi>>
fleetv == <<status, beat, seen, stab>>
idxv   == <<waitn, missing, J>>
queue  == <<agenda, parked, log, cur>>
clock  == <<running, clk, remind, pushes, sw>>
sprint == <<dropping, cut, receipts, next, dcur, acur>>
procs  == <<lease, tk, vk>>
bounds == <<acts, crashes, errs, bugs>>
ghosts == <<probe, seenKeys, ahead, early, waivedRec, applied, raises>>
NoProbe == [a |-> "none"]
vars == <<cards, fleetv, idxv, queue, clock, sprint, procs, bounds, ghosts>>

-----------------------------------------------------------------------------
\* Values.

Cards == Prims \cup Sents
None == "none"
OpenCols == {"waiting", "ready", "working", "review", "merging"}
\* A fixed order of the members and of the readers (the code's name order),
\* and a rolling index into one (errata 3, amendment 5; RoundAssign, RoundTwo):
\* Past(sq, x) is the place just past x, FirstFrom(sq, at, X) the first of X
\* from the place at, wrapping.
OrderOf(X) == CHOOSE f \in [1..Cardinality(X) -> X] : \A i, j \in DOMAIN f : i # j => f[i] # f[j]
MemSeq == OrderOf(Members)
ReaderSeq == OrderOf(Readers)
Past(sq, x) == ((CHOOSE i \in DOMAIN sq : sq[i] = x) % Cardinality(DOMAIN sq)) + 1
At(sq, at, i) == sq[((at - 1 + i) % Cardinality(DOMAIN sq)) + 1]
FirstFrom(sq, at, X) ==
  IF \A i \in DOMAIN sq : sq[i] \notin X THEN None
  ELSE At(sq, at, CHOOSE i \in 0..Cardinality(DOMAIN sq) - 1 :
                    At(sq, at, i) \in X /\ \A i2 \in 0..i - 1 : At(sq, at, i2) \notin X)
Cols == {"none", "landed", "removed"} \cup OpenCols
Min(a, b) == IF a < b THEN a ELSE b
Max(a, b) == IF a > b THEN a ELSE b
Dec(x) == IF x > 0 THEN x - 1 ELSE x      \* a timer after one unit: -1 stays unset
S(c) == StreamOf[c]
Range(q) == {q[i] : i \in DOMAIN q}
Take(q, n) == IF Len(q) <= n THEN q ELSE SubSeq(q, 1, n)
Drop(q, n) == IF Len(q) <= n THEN <<>> ELSE SubSeq(q, n + 1, Len(q))

\* (A value used more than once is bound once, as a value, by the idiom
\* CHOOSE r \in {e(v) : v \in {heavy}} : TRUE. TLC keeps no LET value and no
\* operator argument while it evaluates ENABLED for the tick's fairness, and
\* evaluating them again at every use made the rules' plans exponential
\* there.)
\* Work order: score, then id (a total order: Ord is one to one).
Before(a, b) == score[a] < score[b] \/ (score[a] = score[b] /\ Ord[a] < Ord[b])
Sorted(X0) == CHOOSE r \in {[i \in 1..Cardinality(X) |-> CHOOSE c \in X : Cardinality({d \in X : Before(d, c)}) = i - 1] :
                            X \in {X0}} : TRUE

\* The deal's order: stream turns (errata 3, amendment 4; the engine's
\* dealTurns). One card from each stream's front in turn, streams in a fixed
\* order, a stream with no card in X skipped, and within a stream the cards by
\* work order. A card's place is the pair (how many cards of X of its stream
\* come before it, its stream's place among the streams), compared in that
\* order. The engine's order of streams is by name; the model takes the order
\* of the streams' first cards by Ord (a total order of the streams that have
\* a card, fixed by the constants: no property below depends on which one it
\* is, and in the instances it is the names' order).
StreamOrd(s) == CHOOSE o \in {Ord[c] : c \in {d \in Cards : S(d) = s}} : \A c \in Cards : S(c) = s => o <= Ord[c]
TurnBefore(X, a, b) ==
  LET ra == Cardinality({d \in X : S(d) = S(a) /\ Before(d, a)})
      rb == Cardinality({d \in X : S(d) = S(b) /\ Before(d, b)})
  IN ra < rb \/ (ra = rb /\ StreamOrd(S(a)) < StreamOrd(S(b)))
TurnSorted(X0) == CHOOSE r \in {[i \in 1..Cardinality(X) |-> CHOOSE c \in X : Cardinality({d \in X : TurnBefore(X, d, c)}) = i - 1] :
                               X \in {X0}} : TRUE

\* Places of a work card: a member's ready or working cell, or withdrawn.
Withdrawn == <<None, "withdrawn">>
Places == (Members \X {"ready", "working"}) \cup {Withdrawn}
InReady(w) == \E x \in w.pl : x[2] = "ready"
InWorking(w) == \E x \in w.pl : x[2] = "working"
MemberOf(w) == IF w.pl \ {Withdrawn} # {} THEN (CHOOSE x \in w.pl \ {Withdrawn} : TRUE)[1] ELSE None
ReadyAt(m) == {p \in Prims : <<m, "ready">> \in wk[p].pl}
RCount(m) == Cardinality(ReadyAt(m))
Up == {m \in Members : status[m] = "up"}
Room == Cardinality({x \in Up \X (1..Cap) : x[2] > RCount(x[1])})
BeatFresh(m) == beat[m] > 0              \* a beat is fresh while beat:m lies above R
\* H12's repair (stablesince): R6 deals only to a member up for two beat
\* periods (stable_since); the other rules place cards on any up member.
StableOn == "stablesince" \in Fixes
\* R15 (donestop): the sprint is done when no card is open and one ended.
DoneOn == "donestop" \in Fixes
SprintDone == (\A c \in Cards : col[c] \notin OpenCols) /\ (\E c \in Cards : col[c] \in {"landed", "removed"})
\* H15's repair (unplaced): a card's untaken withdrawals since its last take
\* (fld.tries) are counted, and "this card cannot be placed" opens at
\* MaxPlaceTries. The count is kept with unplaced, and with stablesince as
\* a ghost (so that the design's configurations with stablesince can check
\* UnplacedNamed and fail it); it stays 0 otherwise.
PlaceOn == "unplaced" \in Fixes
TriesOn == StableOn \/ PlaceOn
DealUp == {m \in Up : ~StableOn \/ stab[m] = 0}
DealRoom == Cardinality({x \in DealUp \X (1..Cap) : x[2] > RCount(x[1])})
\* The up members other than a work card's present one with room, and the
\* one of a set with the shortest ready queue.
Others(p) == {m \in Up \ {MemberOf(wk[p])} : RCount(m) < Cap}
Shortest(ms) == CHOOSE m \in ms : \A n \in ms : RCount(m) <= RCount(n)

\* The record a revision guards: every field but the entries, and the timers,
\* whose absolute stamps do not move with time (only this model's relative
\* view of them does).
WRec(w) == [pl |-> w.pl, gen |-> w.gen, taken |-> w.taken, repl |-> w.repl]
Rec(c) == <<col[c], score[c], fld[c], IF c \in Prims THEN WRec(wk[c]) ELSE None>>
\* H14's repair (stopinputs): the version of the inputs of R17's dry plans,
\* as the revision of every card (a counter per stream, over every stream:
\* the streams' heads and the sentinels' scores are among them). The
\* members' control cards are not in it, as amendment 2 words the decision
\* (so a fleet change still empties a dry deal past the guard: H17).
DryIn == [c \in Cards |-> Rec(c)]

\* The indexes that are functions of the fields (1.3.1), with the witnesses
\* that break a definition.
KeepsRefused == Broken = "W9"            \* W9: a refused card left in its index
Sigma(s) == CHOOSE r \in {IF G = {} THEN None ELSE CHOOSE g \in G : \A h \in G : g = h \/ Before(g, h) :
                          G \in {{g \in Sents : S(g) = s /\ col[g] = "waiting"}}} : TRUE
BelowSigma(s, x) == CHOOSE r \in {g = None \/ x < score[g] : g \in {Sigma(s)}} : TRUE
Elig(s)  == {c \in Prims : S(c) = s /\ col[c] = "waiting" /\ fld[c].open = 0 /\ (~fld[c].refused \/ KeepsRefused)}
Fresh(s) == {c \in Prims : S(c) = s /\ col[c] = "ready" /\ fld[c].attempt = 0 /\ (~fld[c].refused \/ KeepsRefused)}
Again(s) == {c \in Prims : S(c) = s /\ col[c] = "ready" /\ fld[c].attempt >= 1 /\ ~fld[c].bound /\ (~fld[c].refused \/ KeepsRefused)}
FreshBelow(s) == {c \in Fresh(s) : BelowSigma(s, score[c])}
\* n_before: the open cards of s sorting below a score (the five open cells).
NBefore(s, x) == Cardinality({c \in Cards : S(c) = s /\ col[c] \in OpenCols /\ score[c] < x})
\* What deal may place: fresh below sigma and again, over the streams not dropping.
Dealable == UNION {FreshBelow(s) \cup Again(s) : s \in {s2 \in Streams : dropping[s2] = None}}
\* H12's judgment condition: work dealable, members up, and either none is
\* stable or a stable one has room (so 30 s passed with room and no deal).
Starved == StableOn /\ Dealable # {} /\ Up # {} /\ (DealUp = {} \/ DealRoom > 0)

\* Keys: <<rule, subject>>.
DealK == <<"deal", "sprint">>
RemindK == <<"remind", "g">>
StopK == <<"stopped", "sprint">>          \* R17's step: planned at a look, no agenda key
DoneK == <<"done", "sprint">>             \* R15, with donestop
ResolveK(s) == <<"resolve", s>>
PullK(s) == <<"pullback", s>>
SentLineKeys(s) == {ResolveK(s), DealK, PullK(s)}
\* Subjects are tagged tuples, so that no two of different kinds are ever
\* compared element by element past their tags.
CS(c) == <<"c", c>>                       \* a card
WS(p) == <<"w", p>>                       \* a primary's work card
RS(p, r) == <<"r", p, r>>                 \* its read card for reader r
SprintS == <<"sprint">>
StrS(s) == <<"s", s>>                     \* a stream
OpS(o) == <<"o", o>>                      \* a verb in parts
KeyS(k) == <<"k", k>>                     \* a rule key ("the machine's step was refused")

\* Judgments: [ty, subj, cause, held, hent]; hent is the hold's entry (-1 none).
Tri(j) == <<j.ty, j.subj, j.cause>>
Present(ty, sb, ca) == \E j \in J : Tri(j) = <<ty, sb, ca>>
IsOpenJ(ty, sb, ca) == \E j \in J : Tri(j) = <<ty, sb, ca>> /\ ~j.held
HoldsTypes == {"cannotask", "nomember", "nostable", "bound", "latework", "lateread", "latemerge", "refused", "stepped", "opcut"}
AckTypes == {"dropped", "missing", "refused", "stepped"}
\* J's one per cause, decided at apply on the present jopen: a hold keeps it
\* closed (W16: J ignores the hold).
JOpen(JS, ty, sb, ca) ==
  IF \E j \in JS : Tri(j) = <<ty, sb, ca>> /\ (~j.held \/ Broken # "W16") THEN JS
  ELSE JS \cup {[ty |-> ty, subj |-> sb, cause |-> ca, held |-> FALSE, hent |-> -1]}
JClose(JS, ty, sb, ca) == {j \in JS : Tri(j) # <<ty, sb, ca>>}
JCloseSubj(JS, subs) == {j \in JS : j.subj \notin subs}
SubjOf(c) == {CS(c)} \cup (IF c \in Prims THEN {WS(c)} \cup {RS(c, r) : r \in Readers} ELSE {})

\* The owner key a close by a decision or ack, or an unheld line, queues (2.2).
OwnerKeys(ty, sb, ca) ==
  CASE ty = "reached"   -> {ResolveK(S(sb[2]))}
    [] ty = "cannotask" -> {<<"ask", sb[2]>>}
    [] ty = "nomember"  -> {DealK}
    [] ty = "nostable"  -> {DealK}
    [] ty = "latework"  -> {<<"late:" \o ca, sb[2]>>}
    [] ty = "lateread"  -> {<<"late:unread", <<sb[2], sb[3]>>>>}
    [] ty = "latemerge" -> {<<"late:mergeidle", sb[2]>>}
    [] ty = "opcut"     -> {<<"late:cut", sb[2]>>}
    [] ty = "stepped"   -> {sb[2]}
    [] OTHER            -> {}                \* held:<card> (R16) or none

-----------------------------------------------------------------------------
\* The state a step builds. Its units and intents fold one after the other on
\* the real before-state, as the derive phase folds every intent and entry of
\* one card into one entry (1.3.3); layer 1 checks every guard before the
\* first write, so the guards read the before-state only.

Cur == [col |-> col, score |-> score, fld |-> fld, wk |-> wk, rd |-> rd, mi |-> mi,
        status |-> status, stab |-> stab, sw |-> sw, waitn |-> waitn, missing |-> missing, J |-> J,
        remind |-> remind, pushes |-> pushes, next |-> next, dcur |-> dcur, acur |-> acur, ahead |-> ahead,
        lk |-> {}, early |-> early, waivedRec |-> waivedRec]

\* The keys ingest gives the line of a step (2.1), from what the step changed.
CardKeys(c, T) ==
  LET s == S(c)
      c0 == col[c]
      c1 == T.col[c]
      f0 == fld[c]
      f1 == T.fld[c]
      rescored == score[c] # T.score[c]
  IN  (IF c0 # c1 \/ rescored \/ f0 # f1 THEN {ResolveK(s)} ELSE {})
   \cup (IF c \in Sents /\ (c0 # c1 \/ rescored)                       \* W11: a sentinel line queues no deal
         THEN (IF Broken = "W11" THEN {ResolveK(s), PullK(s)} ELSE SentLineKeys(s))
         ELSE IF rescored THEN SentLineKeys(s) ELSE {})
   \cup (IF c0 = "none" /\ c1 # "none" THEN {<<"made", c>>} ELSE {})
   \cup (IF c1 = "ready" /\ (c0 # "ready" \/ f0.attempt # f1.attempt \/ f0.bound # f1.bound \/ f0.refused # f1.refused)
         THEN {DealK} ELSE {})
   \cup (IF c1 = "review" /\ c0 # "review" THEN (IF f1.result = "failed" THEN {<<"rework", c>>} ELSE {<<"ask", c>>}) ELSE {})
   \cup (IF c1 \in {"landed", "removed"} /\ c0 # c1 THEN {<<"needs", c>>} ELSE {})
   \cup (IF c1 = "removed" /\ c0 # c1 THEN {DealK} ELSE {})
   \cup (IF DoneOn /\ c1 \in {"landed", "removed"} /\ c0 # c1 THEN {DoneK} ELSE {})
   \cup (IF c \in Prims /\ \E x \in wk[c].pl \ {Withdrawn} : x \notin T.wk[c].pl THEN {DealK} ELSE {})
   \cup (IF c \in Prims /\ \E r \in Readers : T.rd[c][r].st = "ok" /\ rd[c][r].st # "ok" THEN {<<"accept", c>>} ELSE {})
   \cup (IF c \in Prims /\ \E r \in Readers : T.rd[c][r].st = "broken" /\ rd[c][r].st # "broken" THEN {<<"rework", c>>} ELSE {})
MemberKeys(m, T) ==
  IF T.status[m] = status[m] THEN {}
  ELSE IF T.status[m] = "up" THEN {DealK}
       ELSE {<<"down", m>>} \cup (IF "downdeal" \in Fixes THEN {DealK} ELSE {})   \* H10's repair
LineKeys0(T) == UNION {CardKeys(c, T) : c \in Cards} \cup UNION {MemberKeys(m, T) : m \in Members} \cup T.lk
\* H1's repair: a line that queues deal (room freed, a member up, a card
\* ready) also queues the late key of every work card past its deadline.
PastDue(T) == {<<"late:untaken", p>> : p \in {q \in Prims : InReady(T.wk[q]) /\ T.wk[q].tu = 0}}
              \cup {<<"late:unfinished", p>> : p \in {q \in Prims : InWorking(T.wk[q]) /\ T.wk[q].tf = 0}}
LineKeys(T) == CHOOSE r \in {IF "judgeguard" \in Fixes /\ DealK \in ks THEN ks \cup PastDue(T) ELSE ks :
                             ks \in {LineKeys0(T)}} : TRUE

\* The derived due entries (1.2): a card has the entry of kind k exactly when
\* it is in k's state and its timer is set. X emits only the difference
\* between the before and after memberships of a changed card, so an entry
\* already popped stays popped while the card's derived value is unchanged.
DU(w) == IF InReady(w) /\ w.tu >= 0 THEN w.tu ELSE -1
DF(w) == IF InWorking(w) /\ w.tf >= 0 THEN w.tf ELSE -1
DR(x) == IF x.st \in {"asked", "begun"} /\ x.t >= 0 THEN x.t ELSE -1
Merging(s, cl) == \E p \in Prims : S(p) = s /\ cl[p] = "merging"
DM(s, cl, t) == IF Merging(s, cl) /\ t >= 0 THEN t ELSE -1
NewWk(T) == [p \in Prims |-> [T.wk[p] EXCEPT
               !.eu = IF DU(wk[p]) = DU(T.wk[p]) THEN wk[p].eu ELSE DU(T.wk[p]),
               !.ef = IF DF(wk[p]) = DF(T.wk[p]) THEN wk[p].ef ELSE DF(T.wk[p])]]
NewRd(T) == [p \in Prims |-> [r \in Readers |-> [T.rd[p][r] EXCEPT
               !.e = IF DR(rd[p][r]) = DR(T.rd[p][r]) THEN rd[p][r].e ELSE DR(T.rd[p][r])]]]
NewMi(T) == [s \in Streams |-> [T.mi[s] EXCEPT
               !.e = IF DM(s, col, mi[s].t) = DM(s, T.col, T.mi[s].t) THEN mi[s].e ELSE DM(s, T.col, T.mi[s].t)]]
\* A lateness judgment ends with its state (1.3.4): J closes it, or its hold,
\* in the step that ends the card's timed state.
Ends(j, T) ==
  \/ j.ty = "latework" /\ j.cause = "untaken" /\ DU(wk[j.subj[2]]) # -1 /\ DU(T.wk[j.subj[2]]) = -1
  \/ j.ty = "latework" /\ j.cause = "unfinished" /\ DF(wk[j.subj[2]]) # -1 /\ DF(T.wk[j.subj[2]]) = -1
  \/ j.ty = "lateread" /\ DR(rd[j.subj[2]][j.subj[3]]) # -1 /\ DR(T.rd[j.subj[2]][j.subj[3]]) = -1
  \/ j.ty = "latemerge" /\ Merging(j.subj[2], col) /\ ~Merging(j.subj[2], T.col)
\* X takes a card leaving waiting out of every wait:n it is in.
NewWaitn(T) == [n \in Cards |-> {w \in T.waitn[n] : ~(col[w] = "waiting" /\ T.col[w] # "waiting")}]

\* One step's writes. A verb always writes its line; a tick step writes one
\* when it changes something a line records.
\* (T and keys are bound once with \E over a singleton: TLC does not keep a
\* LET's value while it evaluates ENABLED for fairness, and a step's state is
\* costly to build again at every use.)
Commit(T0, verb) ==
  \E T \in {T0} : \E keys \in {LineKeys(T)} :
  /\ col' = T.col /\ score' = T.score /\ fld' = T.fld
  /\ wk' = NewWk(T) /\ rd' = NewRd(T) /\ mi' = NewMi(T)
  /\ status' = T.status /\ stab' = T.stab /\ sw' = T.sw
  /\ waitn' = NewWaitn(T) /\ missing' = T.missing
  /\ J' = {j \in T.J : ~Ends(j, T)}
  /\ remind' = T.remind /\ pushes' = T.pushes /\ next' = T.next /\ dcur' = T.dcur /\ acur' = T.acur
  /\ ahead' = T.ahead /\ early' = T.early /\ waivedRec' = T.waivedRec
  /\ log' = IF keys # {} \/ verb THEN Append(log, [keys |-> keys, ing |-> 0]) ELSE log

-----------------------------------------------------------------------------
\* Units: one card's change in a rule's plan, kept whole in one request, with
\* what its guard compares (the record as read, a member's count as read).

\* The units that change a table member (the others are notes and sprint
\* keys, which apply while STOPPED too), and the units whose subject is a card.
CardOps == {"release", "deal", "redealw", "refuse", "seen", "setdown", "redeal2", "withdraw2",
            "replace", "replaceF", "needmet", "ask", "accept", "rework", "boundj", "reread", "pullback"}
MemberOps == {"seen", "setdown"}
OnCard == {"release", "reach", "unreach", "deal", "redealw", "refuse", "redeal2", "withdraw2", "replace",
           "replaceF", "latej", "ask", "cannotask", "accept", "rework", "boundj", "reread", "lateread", "pullback"}
U(op, c, m, x) == [op |-> op, c |-> c, m |-> m, x |-> x,
                   rec |-> IF op \in OnCard THEN Rec(c) ELSE IF op \in MemberOps THEN status[c] ELSE None,
                   cnt |-> IF op \in {"deal", "redealw", "redeal2", "replace", "replaceF", "rework"} /\ m # None
                           THEN RCount(m) ELSE 0]

\* memberup and the count guard (W18: deal, and the redeal of a withdrawn
\* card, without memberup; the other receivers keep it).
MemberUp(u) == status[u.m] = "up" \/ (Broken = "W18" /\ u.op \in {"deal", "redealw"})
Recv(u) == MemberUp(u) /\ RCount(u.m) <= u.cnt
\* W5 breaks reach, unreach and the release verb; W5Reach breaks reach and
\* unreach, not the verb.
ReachBroken == Broken \in {"W5", "W5Reach"}

UGuard(u) ==
  CASE u.op = "release"   -> col[u.c] = "waiting"                           \* place only
    [] u.op = "reach"     -> Rec(u.c) = u.rec /\ (ReachBroken \/ NBefore(S(u.c), u.rec[2]) = 0)
    [] u.op = "unreach"   -> ReachBroken \/ NBefore(S(u.c), u.rec[2]) >= 1
    [] u.op = "deal"      -> Rec(u.c) = u.rec /\ wk[u.c].pl = {} /\ Recv(u)
    [] u.op = "redealw"   -> Rec(u.c) = u.rec /\ Recv(u)
    [] u.op = "refuse"    -> Rec(u.c) = u.rec
    [] u.op = "seen"      -> status[u.c] = u.rec /\ ("seenfresh" \notin Fixes \/ BeatFresh(u.c))
    [] u.op = "setdown"   -> status[u.c] = u.rec /\ (status[u.c] = "held" \/ ~BeatFresh(u.c))   \* beatstale
    [] u.op = "downkey"   -> status[u.c] = u.x                               \* H2's repair: R2's guard on an empty plan
    [] u.op \in {"armsw", "swfire", "swdone"} -> sw = u.x                    \* XGUARD on the entry as read
    [] u.op = "r17"       -> clk = u.x.clk                                   \* XGUARD on the clock fields as read
                             /\ ("stopinputs" \notin Fixes \/ DryIn = u.x.inp)  \* H14's repair: and on the cards as read
    [] u.op = "redeal2"   -> Rec(u.c) = u.rec /\ Recv(u)
    [] u.op = "withdraw2" -> Rec(u.c) = u.rec
    [] u.op = "replace"   -> Rec(u.c) = u.rec /\ Recv(u)
    [] u.op = "replaceF"  -> Rec(u.c) = u.rec /\ Recv(u)
    [] u.op = "latej"     -> Rec(u.c) = u.rec
    [] u.op = "ask"       -> Rec(u.c) = u.rec /\ \A r \in u.x : rd[u.c][r].st = "none"
    [] u.op = "cannotask" -> Rec(u.c) = u.rec                                \* R8: the primary at review with its revision
    [] u.op = "accept"    -> Rec(u.c) = u.rec /\ rd[u.c] = u.x
    [] u.op = "rework"    -> Rec(u.c) = u.rec /\ (u.m = None \/ Recv(u))
    [] u.op = "boundj"    -> Rec(u.c) = u.rec
    [] u.op = "reread"    -> Rec(u.c) = u.rec /\ rd[u.c] = u.x[3]
    [] u.op = "lateread"  -> Rec(u.c) = u.rec
    [] u.op = "latemerge" -> Merging(u.c, col) = u.x                         \* the control card as read
    [] u.op = "cutj"      -> cut[u.c] = u.x                                  \* the entry's score as read
                             /\ ("cutmark" \notin Fixes \/ \E s \in Streams : dropping[s] = u.c)
    [] u.op = "unhold"    -> \E j \in J : Tri(j) = u.x /\ j.held             \* XGUARD: the field holds the hold
    [] u.op = "remind1"   -> remind = u.x \/ Broken = "W25"                  \* XGUARD on the entry's score
    [] u.op = "pullback"  -> col[u.c] = "ready" /\ col[u.x] = "waiting"
    [] u.op = "done"      -> running /\ \A c \in Cards : col[c] \notin OpenCols   \* MACHINESTATE; the rcount atmost 0
    [] OTHER              -> TRUE      \* nomember, needmet, needgone, made: J and the intents decide

\* J opens one note naming many subjects: one record per subject.
JOpenAll(JS, ty, subs, ca) ==
  JS \cup {[ty |-> ty, subj |-> sb, cause |-> ca, held |-> FALSE, hent |-> -1] :
           sb \in {x \in subs : ~\E j \in JS : Tri(j) = <<ty, x, ca>> /\ (~j.held \/ Broken # "W16")}}

NoReads == [r \in Readers |-> [st |-> "none", t |-> -1, e |-> -1]]
Retire(x) == IF x.st \in {"asked", "begun", "ok", "broken"} THEN [x EXCEPT !.st = "retired", !.t = -1] ELSE x
NewCard(w, m) == [w EXCEPT !.pl = {<<m, "ready">>}, !.gen = (@ + 1) % GenMod, !.taken = FALSE,
                           !.repl = FALSE, !.tu = 1, !.tf = -1, !.m0 = m]

\* H12's repair: R6's 30 s clock restarts at every deal (with unplaced,
\* H15's repair, at a take instead: VEff); when the entry fires with work
\* still dealable, the judgment opens unless this step dealt (Starved, on
\* the present state: members are up, and either none is stable or a
\* stable one has room and still nothing was dealt).
Restart(T) == IF StableOn /\ ~PlaceOn THEN [T EXCEPT !.sw = 1] ELSE T
Eff1(T, u) ==
  LET c == u.c IN
  CASE u.op = "release" -> [T EXCEPT !.col[c] = "ready"]
    [] u.op = "reach"   -> [T EXCEPT !.J = JOpen(@, "reached", CS(c), "-")]
    [] u.op = "unreach" -> [T EXCEPT !.J = JClose(@, "reached", CS(c), "-")]
    [] u.op = "deal" ->
         Restart([T EXCEPT !.col[c] = "working", !.dcur = Past(MemSeq, u.m),
                           !.fld[c].attempt = IF @ = 0 THEN 1 ELSE @,
                           !.wk[c] = NewCard(@, u.m),
                           !.ahead[c] = IF fld[c].attempt = 0
                                        THEN {g \in Sents : S(g) = S(c) /\ col[g] = "waiting" /\ score[g] < score[c]}
                                        ELSE @,
                           !.J = JClose(@, "nostable", SprintS, "-")])
    [] u.op = "redealw" ->
         Restart([T EXCEPT !.col[c] = "working", !.dcur = Past(MemSeq, u.m),
                           !.wk[c] = [@ EXCEPT !.pl = {<<u.m, "ready">>}, !.gen = (@ + 1) % GenMod,
                                               !.tu = IF @ < 0 THEN 1 ELSE @, !.m0 = u.m],
                           !.J = JClose(@, "nostable", SprintS, "-")])
    [] u.op = "refuse"   -> [T EXCEPT !.fld[c].refused = TRUE, !.J = JOpen(@, "refused", CS(c), "deal")]
    [] u.op = "nomember" -> [T EXCEPT !.J = JOpen(@, "nomember", SprintS, "-")]
    [] u.op = "armsw"    -> [T EXCEPT !.sw = 1]
    [] u.op = "swfire"   -> [T EXCEPT !.sw = 1,
                                      !.J = IF T.sw = 2 /\ Starved THEN JOpen(@, "nostable", SprintS, "-")
                                            ELSE JClose(@, "nostable", SprintS, "-")]
    [] u.op = "swdone"   -> [T EXCEPT !.sw = -1, !.J = JClose(@, "nostable", SprintS, "-")]
    [] u.op = "seen"     -> [T EXCEPT !.status[c] = "up", !.stab[c] = IF StableOn THEN 1 ELSE @,
                                      !.J = JClose(@, "nomember", SprintS, "-")]
    [] u.op = "setdown"  -> [T EXCEPT !.status[c] = IF @ = "held" /\ Broken # "W19" THEN "held" ELSE "down",
                                      !.stab[c] = IF StableOn THEN -1 ELSE @]
    [] u.op = "r17"      -> [T EXCEPT !.J = IF u.x.raise THEN JOpen(@, "stopped", SprintS, "-")
                                            ELSE IF u.x.close THEN JClose(@, "stopped", SprintS, "-")
                                            ELSE @]
    [] u.op = "redeal2" ->
         LET wasW == InWorking(wk[c]) IN
         [T EXCEPT !.wk[c] = [@ EXCEPT !.pl = {<<u.m, "ready">>}, !.gen = (@ + 1) % GenMod, !.m0 = u.m,
                                       !.tu = IF wasW THEN 1 ELSE @, !.tf = IF wasW THEN -1 ELSE @,
                                       !.taken = IF wasW THEN FALSE ELSE @],
                   !.fld[c].redeals = IF wasW /\ Broken # "W7b" THEN @ + 1 ELSE @]
    [] u.op = "withdraw2" ->                     \* (unplaced, H15: an untaken withdrawal counts)
         LET wasW == InWorking(wk[c])
             nr == IF wasW /\ fld[c].redeals < MaxRedeals /\ Broken # "W7b" THEN fld[c].redeals + 1 ELSE fld[c].redeals
             bd == nr >= MaxRedeals
             nt == IF ~wasW /\ TriesOn THEN Min(fld[c].tries + 1, MaxPlaceTries) ELSE fld[c].tries
         IN [[T EXCEPT !.wk[c] = [@ EXCEPT !.pl = {Withdrawn}, !.tf = -1, !.taken = FALSE,
                                           !.tu = IF wasW THEN -1 ELSE @],
                       !.col[c] = "ready", !.fld[c].redeals = nr, !.fld[c].bound = @ \/ bd,
                       !.fld[c].tries = nt,
                       !.J = IF bd THEN JOpen(@, "bound", CS(c), "redeals") ELSE @]
             EXCEPT !.J = IF PlaceOn /\ nt >= MaxPlaceTries THEN JOpen(@, "unplaced", CS(c), "-") ELSE @]
    [] u.op = "replace" ->                       \* R11, untaken: once per untaken span (W7a: without end)
         [T EXCEPT !.wk[c] = [@ EXCEPT !.pl = {<<u.m, "ready">>}, !.gen = (@ + 1) % GenMod, !.m0 = u.m,
                                       !.repl = Broken # "W7a", !.tu = 1],
                   !.J = IF "judgeguard" \in Fixes THEN JClose(@, "latework", WS(c), "untaken") ELSE @]
    [] u.op = "replaceF" ->                      \* R11, unfinished: a redeal that counts (W7b: not counted)
         [T EXCEPT !.wk[c] = [@ EXCEPT !.pl = {<<u.m, "ready">>}, !.gen = (@ + 1) % GenMod, !.m0 = u.m,
                                       !.tf = -1, !.tu = 1, !.taken = FALSE, !.repl = FALSE],
                   !.fld[c].redeals = IF Broken # "W7b" THEN @ + 1 ELSE @]
    [] u.op = "latej" -> [T EXCEPT !.J = JOpen(@, "latework", WS(c), u.x)]
    [] u.op = "needmet" ->                       \* decided on the present state, as the Lua does
         LET live == {w \in u.x.ws : w \in T.waitn[c]} IN
         [T EXCEPT !.waitn[c] = @ \ live,
                   !.fld = [d \in Cards |-> IF d \in live
                                            THEN [T.fld[d] EXCEPT !.open = IF Broken = "W8" THEN u.x.open[d] - 1 ELSE @ - 1]
                                            ELSE T.fld[d]],
                   !.J = {j \in @ : ~(j.ty = "missing" /\ j.subj[2] \in live /\ j.cause = c)}]
    [] u.op = "needgone" ->
         LET live == {w \in u.x.ws : w \in T.waitn[c]} IN
         [T EXCEPT !.waitn[c] = IF Broken = "W15" THEN @ ELSE @ \ live,
                   !.J = JOpenAll(@, "dropped", {CS(w) : w \in live}, c)]
    [] u.op = "made" ->
         [T EXCEPT !.J = {j \in @ : ~(j.ty = "missing" /\ j.cause = c)}, !.missing = @ \ {c}]
    [] u.op = "ask" ->
         [T EXCEPT !.rd[c] = [r \in Readers |-> IF r \in u.x THEN [T.rd[c][r] EXCEPT !.st = "asked", !.t = 1] ELSE T.rd[c][r]],
                   !.acur = Past(ReaderSeq, u.m)]
    [] u.op = "cannotask" -> [T EXCEPT !.J = JOpen(@, "cannotask", CS(c), "-")]
    [] u.op = "accept" ->
         [T EXCEPT !.col[c] = "merging", !.mi[S(c)].t = 1,
                   !.rd[c] = [r \in Readers |-> IF T.rd[c][r].st = "ok" THEN T.rd[c][r] ELSE Retire(T.rd[c][r])]]
    [] u.op = "rework" ->
         [T EXCEPT !.fld[c] = [@ EXCEPT !.attempt = @ + 1, !.avoid = wk[c].m0, !.rereads = 0, !.result = "none"],
                   !.rd[c] = NoReads,
                   !.col[c] = IF u.m = None THEN "ready" ELSE "working",
                   !.wk[c] = IF u.m = None THEN [@ EXCEPT !.pl = {}] ELSE NewCard(@, u.m)]
    [] u.op = "boundj" -> [T EXCEPT !.fld[c].bound = TRUE, !.J = JOpen(@, "bound", CS(c), "attempts")]
    [] u.op = "reread" ->
         [T EXCEPT !.rd[c][u.x[1]] = Retire(@), !.rd[c][u.x[2]] = [@ EXCEPT !.st = "asked", !.t = 1],
                   !.fld[c].rereads = @ + 1]
    [] u.op = "lateread"  -> [T EXCEPT !.J = JOpen(@, "lateread", RS(c, u.x), "unread")]
    [] u.op = "latemerge" -> [T EXCEPT !.J = JOpen(@, "latemerge", StrS(c), "-")]
    [] u.op = "cutj"      -> [T EXCEPT !.J = JOpen(@, "opcut", OpS(c), "-")]
    [] u.op = "unhold" ->                        \* R13: the unheld line wakes the owner (W10: no line)
         [T EXCEPT !.J = {j \in @ : Tri(j) # u.x},
                   !.lk = @ \cup (IF Broken = "W10" THEN {} ELSE OwnerKeys(u.x[1], u.x[2], u.x[3]))]
    [] u.op = "remind1"  -> [T EXCEPT !.remind = 1, !.pushes = (@ + 1) % 2]
    [] u.op = "pullback" -> [T EXCEPT !.col[c] = "waiting"]
    [] OTHER -> T

RECURSIVE FoldU(_, _)
FoldU(T, us) == IF us = <<>> THEN T ELSE FoldU(Eff1(T, Head(us)), Tail(us))

-----------------------------------------------------------------------------
\* The rules' plans (2.3), pure functions of the present state. A plan is its
\* key, its units in order, whether it was cut at the chunk (more), and
\* whether it skipped work of a dropping stream (skip: the key stays, 1.3.5).

Plan0(k) == [k |-> k, units |-> <<>>, more |-> FALSE, skip |-> FALSE]
PlanU(k, us, more) == [k |-> k, units |-> us, more |-> more, skip |-> FALSE]
Held(k) == [Plan0(k) EXCEPT !.skip = TRUE]
Frozen(c) == dropping[S(c)] # None

\* Members with room, the shortest ready queue first, avoiding one member
\* unless no other has room.
AssignOne(cnt, ms, av) ==
  CHOOSE r \in {IF room = {} THEN None
                ELSE CHOOSE r2 \in {CHOOSE m \in pref : \A n \in pref : cnt[m] <= cnt[n] :
                                    pref \in {IF room \ {av} # {} THEN room \ {av} ELSE room}} : TRUE :
                room \in {{m \in ms : cnt[m] < Cap}}} : TRUE
Counts == [m \in Members |-> RCount(m)]

\* The deal goes round the fleet and the ask round the readers (errata 3,
\* amendment 5; internal/sprint/round.go): a fixed order of the members (the
\* code's name order) and of the readers, and the rolling index into each (dcur,
\* acur), the place the next scan starts at. A card goes to the first member
\* from the index, wrapping, with room (the avoid member only when no other
\* has), and the index moves past it; a card refused moves nothing. It
\* replaces the shortest ready queue with its ties broken by the order, which
\* gives every card of an idle fleet to the first members. W29: the scan starts
\* at the first member every time (the old rule), not at the rolling index.
RoundOne(cnt, ms, av, at) ==
  LET room == {m \in ms : cnt[m] < Cap}
  IN IF room = {} THEN None
     ELSE FirstFrom(MemSeq, IF Broken = "W29" THEN 1 ELSE at, IF room \ {av} # {} THEN room \ {av} ELSE room)
RECURSIVE RoundAssign(_, _, _, _)
RoundAssign(q0, c0, ms0, at0) ==
  CHOOSE r \in {IF q = <<>> THEN <<>>
                ELSE IF Head(q) \in Refuses THEN << <<Head(q), None>> >> \o RoundAssign(Tail(q), cnt, ms, at)
                ELSE CHOOSE r2 \in {IF m = None THEN <<>>
                                    ELSE << <<Head(q), m>> >> \o RoundAssign(Tail(q), [cnt EXCEPT ![m] = @ + 1], ms, Past(MemSeq, m)) :
                                    m \in {RoundOne(cnt, ms, fld[Head(q)].avoid, at)}} : TRUE :
                q \in {q0}, cnt \in {c0}, ms \in {ms0}, at \in {at0}} : TRUE
\* The ask's two readers: the first able from acur, then the first able past it
\* (in place of any two, a free choice).
RoundTwo(X, at) ==
  LET a == FirstFrom(ReaderSeq, at, X) IN <<a, FirstFrom(ReaderSeq, Past(ReaderSeq, a), X \ {a})>>

\* R3 resolve:s: release, reach, unreach (W26: release reads waiting, not elig).
ResolvePlan(k, g, below, rel, ch) ==
  LET nb == IF g = None THEN 0 ELSE NBefore(S(g), score[g])
      reach == g # None /\ nb = 0 /\ fld[g].open = 0 /\ ~Present("reached", CS(g), "-")
      unreach == g # None /\ IsOpenJ("reached", CS(g), "-") /\ nb > 0
  IN PlanU(k, [i \in 1..Len(rel) |-> U("release", rel[i], None, None)]
              \o (IF reach THEN <<U("reach", g, None, None)>> ELSE <<>>)
              \o (IF unreach THEN <<U("unreach", g, None, None)>> ELSE <<>>),
           Cardinality(below) > ch)
PlanResolve(k, ch) ==
  LET s == k[2] IN
  IF dropping[s] # None THEN Held(k)
  ELSE CHOOSE r \in UNION {{ResolvePlan(k, g, below, rel, ch) : rel \in {Take(Sorted(below), ch)}} :
                           g \in {Sigma(s)},
                           below \in {{c \in (IF Broken = "W26" THEN {d \in Prims : S(d) = s /\ col[d] = "waiting"} ELSE Elig(s)) :
                                        BelowSigma(s, score[c])}}} : TRUE

\* R19 pullback:s: fresh cards above sigma go back to waiting.
PlanPull(k, ch) ==
  LET s == k[2] IN
  IF dropping[s] # None THEN Held(k)
  ELSE CHOOSE r \in UNION {{PlanU(k, [i \in 1..Len(q) |-> U("pullback", q[i], None, g)], Cardinality(above) > ch) :
                            q \in {Take(Sorted(above), ch)}} :
                           g \in {Sigma(s)}, above \in {IF Sigma(s) = None THEN {} ELSE {c \in Fresh(s) : score[c] > score[Sigma(s)]}}} : TRUE

\* R6 deal: the room of fresh below sigma and again, over the streams not
\* dropping, taken in stream turns (TurnSorted: one card from each stream's
\* front in turn, within a stream by score; errata 3, amendment 4); no member up and a card dealable: "no fleet member is up".
\* With stablesince, only stable members receive, and the 30 s entry: armed
\* while work is dealable, restarted by a deal; fired, it opens "work is
\* ready and no member is stable" (Starved) and is armed again, or is
\* cleared when nothing is dealable.
DealUnit(x) == IF x[1] \in Refuses THEN U("refuse", x[1], None, None)
               ELSE IF wk[x[1]].pl = {Withdrawn} THEN U("redealw", x[1], x[2], None)
               ELSE U("deal", x[1], x[2], None)
StabUnits(D) ==
  IF ~StableOn THEN <<>>
  ELSE IF D # {} /\ sw = -1 THEN <<U("armsw", "sprint", None, sw)>>
  ELSE IF D # {} /\ sw = 2 THEN <<U("swfire", "sprint", None, sw)>>
  ELSE IF D = {} /\ sw = 2 THEN <<U("swdone", "sprint", None, sw)>>
  ELSE <<>>
DealPlan(k, D, asg) ==
  [k |-> k,
   units |-> [i \in 1..Len(asg) |-> DealUnit(asg[i])] \o (IF Up = {} /\ D # {} THEN <<U("nomember", "sprint", None, None)>> ELSE <<>>)
             \o StabUnits(D),
   more |-> DealRoom > Len(asg) /\ Cardinality(D) > Len(asg),
   skip |-> \E s \in Streams : dropping[s] # None /\ FreshBelow(s) \cup Again(s) # {}]
\* W28: the deal takes the room lowest over the whole table, as the design's
\* body words R6, not one card from each stream's front in turn.
DealOrder(D) == IF Broken = "W28" THEN Sorted(D) ELSE TurnSorted(D)
PlanDeal(k, ch) ==
  CHOOSE r \in UNION {{DealPlan(k, D, asg) : asg \in {RoundAssign(Take(DealOrder(D), Min(DealRoom, ch)), Counts, DealUp, dcur)}} :
                      D \in {Dealable}} : TRUE

\* R2 down:m: m's cards as sets, redealt or withdrawn; a held member stays held.
DownUnit(p, m2) == IF m2 = None \/ (InWorking(wk[p]) /\ fld[p].redeals >= MaxRedeals)
                   THEN U("withdraw2", p, None, None) ELSE U("redeal2", p, m2, None)
RECURSIVE DownUnits(_, _, _)
DownUnits(q0, c0, ms0) ==
  CHOOSE r \in {IF q = <<>> THEN <<>>
                ELSE CHOOSE r2 \in UNION {{<<u>> \o DownUnits(Tail(q), IF u.op = "withdraw2" THEN cnt ELSE [cnt EXCEPT ![m2] = @ + 1], ms) :
                                           u \in {DownUnit(Head(q), m2)}} :
                                          m2 \in {AssignOne(cnt, ms, None)}} : TRUE :
                q \in {q0}, cnt \in {c0}, ms \in {ms0}} : TRUE
\* H2's repair (seenfresh, its second clause): a plan of R2 with nothing to
\* change removes down:m only under R2's guard, the control card as read.
DownKey(P0) == CHOOSE r \in {IF "seenfresh" \in Fixes /\ P.units = <<>>
                              THEN [P EXCEPT !.units = <<U("downkey", P.k[2], None, status[P.k[2]])>>] ELSE P :
                              P \in {P0}} : TRUE
PlanDown(k, ch) ==
  LET m == k[2] IN
  DownKey(IF status[m] = "up" /\ BeatFresh(m) THEN Plan0(k)
          ELSE CHOOSE r \in {PlanU(k, (IF status[m] = "up" \/ (status[m] = "held" /\ Broken = "W19") THEN <<U("setdown", m, None, None)>> ELSE <<>>)
                                      \o DownUnits(Take(Sorted(mine), ch), Counts, Up \ {m}),
                                   Cardinality(mine) > ch) :
                             mine \in {{p \in Prims : \E x \in wk[p].pl : x[1] = m}}} : TRUE)

\* R1 seen:m.
PlanSeen(k) == IF status[k[2]] = "down" /\ ("seenfresh" \notin Fixes \/ BeatFresh(k[2]))
               THEN PlanU(k, <<U("seen", k[2], None, None)>>, FALSE) ELSE Plan0(k)

\* R4 needs:n and made:n.
NeedsPlan(k, n, cand, ws, frz, ch) ==
  IF col[n] \in {"landed", "removed"} /\ ws # {}
  THEN [PlanU(k, <<U(IF col[n] = "landed" THEN "needmet" ELSE "needgone", n, None,
                     [ws |-> ws, open |-> [w \in ws |-> fld[w].open]])>>, Cardinality(cand) > ch) EXCEPT !.skip = frz]
  ELSE [Plan0(k) EXCEPT !.skip = frz]
PlanNeeds(k, ch) ==
  LET n == k[2] IN
  CHOOSE r \in UNION {{NeedsPlan(k, n, cand, ws, frz, ch) : ws \in {Range(Take(Sorted(cand), ch))}} :
                      cand \in {{w \in waitn[n] : ~Frozen(w)}}, frz \in {\E w \in waitn[n] : Frozen(w)}} : TRUE
PlanMade(k) ==
  LET n == k[2] IN
  IF col[n] # "none" /\ (n \in missing \/ \E j \in J : j.ty = "missing" /\ j.cause = n)
  THEN PlanU(k, <<U("made", n, None, None)>>, FALSE) ELSE Plan0(k)

\* R8 ask, R9 accept, R10 rework.
PlanAsk(k) ==
  LET p == k[2]
      able == {r \in Readers : rd[p][r].st = "none"}
  IN IF Frozen(p) THEN Held(k)
     ELSE IF col[p] = "review" /\ fld[p].result = "ok" /\ ~fld[p].bound /\ able = Readers
     THEN PlanU(k, <<IF Cardinality(able) >= 2
                     THEN LET two == RoundTwo(able, acur) IN U("ask", p, two[2], {two[1], two[2]})
                     ELSE U("cannotask", p, None, None)>>, FALSE)
     ELSE Plan0(k)
PlanAccept(k) ==
  LET p == k[2] IN
  IF Frozen(p) THEN Held(k)
  ELSE IF col[p] = "review" /\ fld[p].result = "ok" /\ ~fld[p].bound /\ Cardinality({r \in Readers : rd[p][r].st = "ok"}) >= 2
  THEN PlanU(k, <<U("accept", p, None, rd[p])>>, FALSE) ELSE Plan0(k)
PlanRework(k) ==
  LET p == k[2]
      bad == fld[p].result = "failed" \/ \E r \in Readers : rd[p][r].st = "broken"
  IN IF Frozen(p) THEN Held(k)
     ELSE IF col[p] = "review" /\ bad /\ ~fld[p].bound
     THEN PlanU(k, <<IF fld[p].attempt < MaxAttempts THEN U("rework", p, AssignOne(Counts, Up, wk[p].m0), None)
                     ELSE U("boundj", p, None, None)>>, FALSE)
     ELSE Plan0(k)

\* R11 late:<kind>:<id> and the cut clock.
PlanLate(k) ==
  CASE k[1] = "late:untaken" ->
         LET p == k[2] IN
         IF Frozen(p) THEN Held(k)
         ELSE IF InReady(wk[p]) /\ wk[p].tu = 0
         THEN PlanU(k, <<IF ~wk[p].repl /\ Others(p) # {} THEN U("replace", p, Shortest(Others(p)), None)
                         ELSE U("latej", p, None, "untaken")>>, FALSE)
         ELSE Plan0(k)
    [] k[1] = "late:unfinished" ->
         LET p == k[2] IN
         IF Frozen(p) THEN Held(k)
         ELSE IF InWorking(wk[p]) /\ wk[p].tf = 0
         THEN PlanU(k, <<IF fld[p].redeals < MaxRedeals /\ Others(p) # {} THEN U("replaceF", p, Shortest(Others(p)), None)
                         ELSE U("latej", p, None, "unfinished")>>, FALSE)
         ELSE Plan0(k)
    [] k[1] = "late:unread" ->
         LET p == k[2][1]
             r == k[2][2]
             fresh == {r2 \in Readers : rd[p][r2].st = "none"}
         IN IF Frozen(p) THEN Held(k)
            ELSE IF rd[p][r].st \in {"asked", "begun"} /\ rd[p][r].t = 0
            THEN PlanU(k, <<IF fld[p].rereads < MaxRereads /\ fresh # {}
                            THEN U("reread", p, None, <<r, CHOOSE r2 \in fresh : TRUE, rd[p]>>)
                            ELSE U("lateread", p, None, r)>>, FALSE)
            ELSE Plan0(k)
    [] k[1] = "late:mergeidle" ->
         IF Merging(k[2], col) /\ mi[k[2]].t = 0 THEN PlanU(k, <<U("latemerge", k[2], None, TRUE)>>, FALSE) ELSE Plan0(k)
    [] k[1] = "late:cut" ->
         IF (\E s \in Streams : dropping[s] = k[2]) /\ cut[k[2]] <= 0
         THEN PlanU(k, <<U("cutj", k[2], None, cut[k[2]])>>, FALSE) ELSE Plan0(k)
    [] OTHER -> Plan0(k)

\* R13 hold:<note>, R14 remind (phase 1).
PlanHold(k) ==
  IF \E j \in J : Tri(j) = k[2] /\ j.held /\ j.hent <= 0
  THEN PlanU(k, <<U("unhold", "sprint", None, k[2])>>, FALSE) ELSE Plan0(k)
\* R15 (donestop): with the sprint done, one unit: the notice and the stop.
PlanDone(k) == IF SprintDone THEN PlanU(k, <<U("done", "sprint", None, None)>>, FALSE) ELSE Plan0(k)

PlanRemind(k) ==
  IF Scn.goal /\ remind <= 0 THEN PlanU(k, <<U("remind1", "sprint", None, remind)>>, FALSE) ELSE Plan0(k)

ChunkOf(h) == IF h THEN Max(1, Chunk \div 2) ELSE Chunk
PlanOf(k, h) ==
  CASE k[1] = "resolve"  -> PlanResolve(k, ChunkOf(h))
    [] k[1] = "pullback" -> PlanPull(k, ChunkOf(h))
    [] k[1] = "deal"     -> PlanDeal(k, ChunkOf(h))
    [] k[1] = "down"     -> PlanDown(k, ChunkOf(h))
    [] k[1] = "seen"     -> PlanSeen(k)
    [] k[1] = "needs"    -> PlanNeeds(k, ChunkOf(h))
    [] k[1] = "made"     -> PlanMade(k)
    [] k[1] = "ask"      -> PlanAsk(k)
    [] k[1] = "accept"   -> PlanAccept(k)
    [] k[1] = "rework"   -> PlanRework(k)
    [] k[1] = "hold"     -> PlanHold(k)
    [] k[1] = "remind"   -> PlanRemind(k)
    [] k[1] = "done"     -> PlanDone(k)
    [] OTHER             -> PlanLate(k)

\* What a plan would do if applied now.
Changes(u) == Eff1(Cur, u) # Cur
NonEmpty(P) == \E i \in DOMAIN P.units : Changes(P.units[i])
CardChange(P) == \E i \in DOMAIN P.units : P.units[i].op \in CardOps /\ Changes(P.units[i])

\* The keys.
StaticKeys ==
  {DealK, RemindK} \cup (IF DoneOn THEN {DoneK} ELSE {}) \cup {ResolveK(s) : s \in Streams} \cup {PullK(s) : s \in Streams}
  \cup {<<"needs", c>> : c \in Cards} \cup {<<"made", c>> : c \in Cards}
  \cup {<<r, p>> : r \in {"ask", "accept", "rework", "late:untaken", "late:unfinished"}, p \in Prims}
  \cup {<<"late:unread", x>> : x \in Prims \X Readers}
  \cup {<<"late:mergeidle", s>> : s \in Streams} \cup {<<"late:cut", o>> : o \in Ops}
  \cup {<<"seen", m>> : m \in Members} \cup {<<"down", m>> : m \in Members}
AllKeys == StaticKeys \cup {<<"hold", Tri(j)>> : j \in {j2 \in J : j2.held}}

\* The builder cuts a plan into requests of at most StepBound units, each
\* atomic alone (1.3.6); a plan with no unit is one request that finds
\* nothing and removes its key.
NReq(P) == IF Len(P.units) = 0 THEN 1 ELSE (Len(P.units) + StepBound - 1) \div StepBound
Req(P, i) == IF Len(P.units) = 0 THEN <<>>
             ELSE SubSeq(P.units, (i - 1) * StepBound + 1, Min(i * StepBound, Len(P.units)))
Ops1(rq, ops) == {rq[i] : i \in {j \in DOMAIN rq : rq[j].op \in ops}}
\* The zguards: no sentinel placed before a released card, or before a fresh
\* card dealt, since the read (S.zguard on sent:s, rcount atmost 0). (W5 is
\* the rcount on the open cells before sigma: reach's and the release
\* verb's, not this one.)
ZGuard(us) == \A u \in us : ~\E g \in Sents : S(g) = S(u.c) /\ col[g] = "waiting" /\ score[g] < score[u.c]
ReqGuard(rq) ==
  /\ \A i \in DOMAIN rq : UGuard(rq[i])
  /\ \A i \in DOMAIN rq : (rq[i].op \in CardOps \cap OnCard) => ~Frozen(rq[i].c)   \* DROPPING
  /\ ZGuard(Ops1(rq, {"release"}))
  /\ Broken = "W4" \/ ZGuard({u \in Ops1(rq, {"deal"}) : fld[u.c].attempt = 0})
CardReq(rq) == \E i \in DOMAIN rq : rq[i].op \in CardOps
\* After a single-request plan applied: is its key requeued? needs:n by the
\* state it left (section 5's ApplyNeeds), the others by being cut at the chunk.
RequeueAfter(k, P, T) ==
  IF k[1] = "needs" THEN {w \in T.waitn[k[2]] : ~Frozen(w)} # {} ELSE P.more
\* The variant a requeue must lower (ChunkProgress).
Variant(k) ==
  CASE k[1] = "resolve"  -> Cardinality({c \in (IF Broken = "W26" THEN {d \in Prims : S(d) = k[2] /\ col[d] = "waiting"} ELSE Elig(k[2])) :
                                         BelowSigma(k[2], score[c])})
    [] k[1] = "pullback" -> Cardinality({c \in Fresh(k[2]) : ~BelowSigma(k[2], score[c])})
    [] k[1] = "deal"     -> Cardinality(Dealable)
    [] k[1] = "down"     -> Cardinality({p \in Prims : \E x \in wk[p].pl : x[1] = k[2]})
    [] k[1] = "needs"    -> Cardinality({w \in waitn[k[2]] : ~Frozen(w)})
    [] OTHER             -> 0

-----------------------------------------------------------------------------
\* The initial state. Scn gives the tables (col, score, result, the work
\* cards' places wpl, the members' status), whether the machine runs, the
\* counter, whether a goal exists, the lines not yet ingested (log), and the
\* keys queued (owe; with oweall every key whose rule is not quiet). The
\* indexes, open counts, judgments and due entries are derived from them.

MaxParts == Cardinality(Cards) + 2
\* The facts the rules' plans read (a probe runs only while they are as the plan read them).
planv == <<col, score, fld, wk, rd, mi, status, beat, stab, sw, waitn, missing, J, remind, next, dcur, acur, dropping, cut, running>>
commitv == <<col, score, fld, wk, rd, mi, status, stab, sw, waitn, missing, J, remind, pushes, next, dcur, acur,
             ahead, early, waivedRec, log>>
TkIdle(h) == [pc |-> "idle", gen |-> 0, from |-> 0, to |-> 0, lk |-> {}, bk |-> FALSE,
              plans |-> [k \in {} |-> Plan0(DealK)], pend |-> <<>>, half |-> h, seen0 |-> <<>>,
              n |-> 0, ends |-> 0, due |-> 0]
VIdle == [pc |-> "idle", verb |-> None, arg |-> None, snap |-> None, op |-> None, s |-> None,
          part |-> 0, cont |-> <<0, 0>>, rr |-> 0]
WK0(p) == LET pl == Scn.wpl[p]
              r == \E x \in pl : x[2] = "ready"
              w == \E x \in pl : x[2] = "working"
          IN [pl |-> pl, gen |-> 0, taken |-> w, repl |-> FALSE,
              m0 |-> IF pl = {} THEN None ELSE (CHOOSE x \in pl : TRUE)[1],
              tu |-> IF r THEN 1 ELSE -1, eu |-> IF r THEN 1 ELSE -1,
              tf |-> IF w THEN 1 ELSE -1, ef |-> IF w THEN 1 ELSE -1]
Wait0 == [n \in Cards |-> {w \in Cards : Scn.col[w] = "waiting" /\ n \in NeedsOf[w] /\ Scn.col[n] \in {"none"} \cup OpenCols}]

Init ==
  /\ col = [c \in Cards |-> Scn.col[c]]
  /\ score = [c \in Cards |-> Scn.score[c]]
  /\ fld = [c \in Cards |-> [open |-> Cardinality({n \in Cards : c \in Wait0[n]}), refused |-> FALSE, bound |-> FALSE,
                             attempt |-> IF Scn.col[c] \in {"working", "review", "merging", "landed"} THEN 1 ELSE 0,
                             redeals |-> 0, rereads |-> 0, avoid |-> None,
                             result |-> IF Scn.col[c] \in {"review", "merging", "landed"} THEN Scn.result[c] ELSE "none",
                             waived |-> {}, tries |-> 0]]
  /\ wk = [p \in Prims |-> WK0(p)]
  /\ rd = [p \in Prims |-> NoReads]
  /\ mi = [s \in Streams |-> IF \E p \in Prims : S(p) = s /\ Scn.col[p] = "merging"
                             THEN [t |-> 1, e |-> 1] ELSE [t |-> -1, e |-> -1]]
  /\ status = Scn.status
  /\ beat = [m \in Members |-> IF Scn.status[m] = "up" THEN 1 ELSE -1]
  /\ seen = [m \in Members |-> FALSE]
  /\ stab = [m \in Members |-> IF Scn.status[m] = "up" THEN 0 ELSE -1]
  /\ sw = -1
  /\ waitn = Wait0
  /\ missing = {n \in Cards : Scn.col[n] = "none" /\ Wait0[n] # {}}
  /\ J = {[ty |-> "missing", subj |-> CS(x[1]), cause |-> x[2], held |-> FALSE, hent |-> -1] :
          x \in {y \in Cards \X Cards : y[1] \in Wait0[y[2]] /\ Scn.col[y[2]] = "none"}}
  /\ parked = {}
  /\ log = Scn.log
  /\ cur = 0
  /\ running = Scn.running
  /\ clk = [since |-> -1, hold |-> 0, raised |-> FALSE]
  /\ remind = IF Scn.goal THEN 1 ELSE -1
  /\ pushes = 0
  /\ dropping = [s \in Streams |-> None]
  /\ cut = [o \in Ops |-> -1]
  /\ receipts = {}
  /\ next = Scn.next
  /\ dcur = Scn.dcur /\ acur = 1
  /\ lease = [owner |-> None, gen |-> 0]
  /\ tk = [t \in Ticks |-> TkIdle({})]
  /\ vk = [v \in VerbProcs |-> VIdle]
  /\ acts = 0 /\ crashes = 0 /\ errs = 0 /\ bugs = 0
  /\ probe = NoProbe
  /\ seenKeys = {}
  /\ ahead = [p \in Prims |-> {}]
  /\ early = FALSE
  /\ waivedRec = FALSE
  /\ applied = [x \in Ops \X (1..MaxParts) |-> 0]
  /\ raises = 0
  /\ agenda = Scn.owe \cup (IF Scn.oweall THEN {k \in AllKeys : k[1] # "seen" /\ NonEmpty(PlanOf(k, FALSE))} ELSE {})

-----------------------------------------------------------------------------
\* The tick (1.4.2, 1.4.5), one process per run loop. Every write carries
\* the lease generation it read (E4, T1; W6: a tick write without it). The
\* round trips are the actions: RT1's step (the lease and pop parts in one
\* call), RT1's page read, RT2 (the ingest part, then each rule's read and
\* plan, or the look while STOPPED), and RT3's requests one by one, in the
\* round robin's priority order. Any outside action may run between two of
\* these, and between any two requests.

GenOK(t) == (lease.owner = t /\ lease.gen = tk[t].gen) \/ Broken = "W6"
PendingKeys == UNION {log[i].keys : i \in (cur + 1)..Len(log)}
LeaseFree(t) == lease.owner = t \/ (lease.owner = None /\ lease.gen < MaxLease)
NewGen(t) == IF lease.owner = t THEN lease.gen ELSE lease.gen + 1
\* When every loop is idle, the lines at or before the cursor are dropped
\* from the model (nothing reads them again), which keeps the state finite.
Rebase == IF \A u \in Ticks : tk[u].pc = "idle"
          THEN log' = Drop(log, cur) /\ cur' = 0
          ELSE UNCHANGED <<log, cur>>

LeaseExpire ==
  /\ lease.owner # None /\ lease.gen < MaxLease
  /\ lease' = [lease EXCEPT !.owner = None]
  /\ probe' = NoProbe
  /\ UNCHANGED <<cards, fleetv, idxv, queue, clock, sprint, tk, vk, bounds, seenKeys, ahead, early, waivedRec, applied, raises>>

\* The pop part: every entry due at R (and cut entries at wall) as its key.
DueKeys ==
  {<<"late:untaken", p>> : p \in {q \in Prims : wk[q].eu = 0}}
  \cup {<<"late:unfinished", p>> : p \in {q \in Prims : wk[q].ef = 0}}
  \cup {<<"late:unread", x>> : x \in {y \in Prims \X Readers : rd[y[1]][y[2]].e = 0}}
  \cup {<<"late:mergeidle", s>> : s \in {s2 \in Streams : mi[s2].e = 0}}
  \cup {<<"down", m>> : m \in {m2 \in Members : beat[m2] = 0}}
  \cup {<<"seen", m>> : m \in {m2 \in Members : seen[m2]}}
  \cup (IF remind = 0 THEN {RemindK} ELSE {})
  \cup {<<"late:cut", o>> : o \in {o2 \in Ops : cut[o2] = 0}}
  \cup {<<"hold", Tri(j)>> : j \in {j2 \in J : j2.hent = 0}}
  \cup (IF sw = 0 THEN {DealK} ELSE {})                     \* R6's 30 s entry (stablesince)
Z(x) == IF x = 0 THEN -1 ELSE x
PopEntries ==
  /\ wk' = [p \in Prims |-> [wk[p] EXCEPT !.eu = Z(@), !.ef = Z(@)]]
  /\ rd' = [p \in Prims |-> [r \in Readers |-> [rd[p][r] EXCEPT !.e = Z(@)]]]
  /\ mi' = [s \in Streams |-> [mi[s] EXCEPT !.e = Z(@)]]
  /\ beat' = [m \in Members |-> Z(beat[m])]
  /\ seen' = [m \in Members |-> FALSE]
  /\ remind' = Z(remind)
  /\ cut' = [o \in Ops |-> Z(cut[o])]
  /\ J' = {[j EXCEPT !.hent = Z(@)] : j \in J}
  /\ sw' = IF sw = 0 THEN 2 ELSE sw                         \* fired: deal handles it
popv == <<wk, rd, mi, beat, seen, remind, cut, J, sw>>
rt1Others == <<col, score, fld, status, stab, waitn, missing, parked, running, clk, pushes, dropping,
               receipts, next, dcur, acur, vk, seenKeys, ahead, early, waivedRec, applied, raises>>

\* RT1's step: take a free lease (gen + 1) or renew one's own, then pop: the
\* keys first, then the entries (A1, D3). A lease another loop holds: this
\* loop does nothing this tick.
RT1(t) ==
  /\ tk[t].pc = "idle" /\ LeaseFree(t)
  /\ lease' = [owner |-> t, gen |-> NewGen(t)]
  /\ agenda' = agenda \cup DueKeys
  /\ PopEntries
  /\ tk' = [tk EXCEPT ![t] = [TkIdle(@.half) EXCEPT !.pc = "popped", !.gen = NewGen(t), !.bk = Len(log) > cur]]
  /\ Rebase
  /\ probe' = NoProbe
  /\ UNCHANGED <<rt1Others, bounds>>
\* A function that errors keeps the writes it made before the error: the
\* pop's first write kept, the second lost (W24: the entries are removed
\* first, so the keys are the write lost).
ErrorRT1(t) ==
  /\ tk[t].pc = "idle" /\ LeaseFree(t) /\ errs < MaxErrors /\ DueKeys # {}
  /\ lease' = [owner |-> t, gen |-> NewGen(t)]
  /\ IF Broken = "W24"
     THEN PopEntries /\ UNCHANGED agenda
     ELSE agenda' = agenda \cup DueKeys /\ UNCHANGED popv
  /\ tk' = [tk EXCEPT ![t] = [TkIdle(@.half) EXCEPT !.pc = "popped", !.gen = NewGen(t), !.bk = Len(log) > cur]]
  /\ Rebase
  /\ errs' = errs + 1
  /\ probe' = NoProbe
  /\ UNCHANGED <<rt1Others, acts, crashes, bugs>>

\* RT1's page: the lines after the cursor, read with the cursor.
ReadEvents(t) ==
  /\ tk[t].pc = "popped"
  /\ tk' = [tk EXCEPT ![t] = [@ EXCEPT !.pc = "read", !.from = cur, !.to = Len(log),
                                       !.lk = UNION {log[i].keys : i \in (cur + 1)..Len(log)}]]
  /\ probe' = NoProbe
  /\ UNCHANGED <<cards, fleetv, idxv, queue, clock, sprint, lease, vk, bounds, seenKeys, ahead, early, waivedRec, applied, raises>>

\* The rules' order (1.4.2): R1, R2, R4, R3, R6, R8, R9, R10, R11, R13, R14,
\* R19 (the rules this model has), round robin: every key's first request
\* before any key's second.
Prio(k) == CASE k[1] = "seen" -> 1 [] k[1] = "down" -> 2 [] k[1] \in {"needs", "made"} -> 3
             [] k[1] = "resolve" -> 4 [] k[1] = "deal" -> 6 [] k[1] = "ask" -> 8 [] k[1] = "accept" -> 9
             [] k[1] = "rework" -> 10 [] k[1] = "hold" -> 13 [] k[1] = "remind" -> 14
             [] k[1] = "pullback" -> 19 [] k[1] = "done" -> 16 [] OTHER -> 11
RECURSIVE KeySeq(_)
KeySeq(K0) == CHOOSE r \in {IF K = {} THEN <<>>
                             ELSE CHOOSE r2 \in {<<k>> \o KeySeq(K \ {k}) : k \in {CHOOSE x \in K : \A y \in K : Prio(x) <= Prio(y)}} : TRUE :
                             K \in {K0}} : TRUE
MaxN(K, P) == IF K = {} THEN 0 ELSE NReq(P[CHOOSE k \in K : \A k2 \in K : NReq(P[k]) >= NReq(P[k2])])
RECURSIVE Rounds(_, _, _, _)
Rounds(ks, P, i, n) ==
  IF i > n THEN <<>>
  ELSE SelectSeq([j \in 1..Len(ks) |-> <<ks[j], i>>], LAMBDA y : NReq(P[y[1]]) >= i) \o Rounds(ks, P, i + 1, n)
Order(K0, P0) == CHOOSE r \in {Rounds(KeySeq(K), P, 1, MaxN(K, P)) : K \in {K0}, P \in {P0}} : TRUE
NoPlans == [k \in {} |-> Plan0(DealK)]

\* RT2, RUNNING: every key of the agenda (after the ingest) planned on the
\* present state, each plan cut into requests by the builder (1.3.6).
\* STOPPED: the look (1.4.5): the rules plan dry; R17 plans its step on
\* the clock as read (the clock's fields, and "the machine is STOPPED and
\* moves are due" raised once per span; W20: moves due counted from the
\* backlog), sent in RT3 only when it changes something, with the cut
\* clock's judgments, the only rule steps sent. R17's step is applied as a
\* request (Apply), guarded by the clock fields as read (2.3), and with
\* stopinputs (H14) by the cards as read. With stoprearm (H16) a close
\* clears raised, so R17 raises once per span and close.
R17Unit(due) ==
  LET raise == due /\ clk.since = 0 /\ clk.hold = 0 /\ ~clk.raised
      close == ~due /\ "stopclose" \in Fixes /\ IsOpenJ("stopped", SprintS, "-")   \* H7's repair
      nclk == [since |-> IF due THEN (IF clk.since = -1 THEN 1 ELSE clk.since) ELSE -1,
               hold |-> clk.hold,
               raised |-> IF close /\ "stoprearm" \in Fixes THEN FALSE ELSE clk.raised \/ raise]
  IN [op |-> "r17", c |-> "sprint", m |-> None, rec |-> None, cnt |-> 0,
      x |-> [clk |-> clk, nclk |-> nclk, raise |-> raise, close |-> close,
             inp |-> IF "stopinputs" \in Fixes THEN DryIn ELSE None]]
R17Needed(u) == u.x.nclk # u.x.clk \/ u.x.raise \/ u.x.close
PlanOrLook(t, ag) ==
  LET x == tk[t] IN
  IF running
  THEN \E P \in {[k \in ag |-> PlanOf(k, k \in x.half)]} :
       tk' = [tk EXCEPT ![t] = [x EXCEPT !.pc = IF ag = {} THEN "idle" ELSE "apply",
                                        !.plans = IF ag = {} THEN NoPlans ELSE P, !.pend = Order(ag, P),
                                        !.seen0 = IF Probes THEN planv ELSE <<>>]]
  ELSE \E due \in {IF Broken = "W20"
                   THEN x.bk \/ ag # {}                          \* W20: the backlog (lines and keys queued)
                   ELSE \E k \in ag : CardChange(PlanOf(k, FALSE))} :
       \E u17 \in {R17Unit(due)} :
       \E K \in {{k \in ag : k[1] = "late:cut"} \cup (IF R17Needed(u17) THEN {StopK} ELSE {})} :
       \E P \in {[k \in K |-> IF k[1] = "stopped" THEN PlanU(StopK, <<u17>>, FALSE) ELSE PlanOf(k, FALSE)]} :
          tk' = [tk EXCEPT ![t] = [x EXCEPT !.pc = IF K = {} THEN "idle" ELSE "apply",
                                           !.plans = IF K = {} THEN NoPlans ELSE P, !.pend = Order(K, P)]]
\* The ingest part: refused unless the gen is current and cur is where it
\* starts (INGESTAT; W1: without comparing the cursor); keys first, then the
\* cursor (A1).
IngestWrites(x) ==
  /\ cur' = x.to
  /\ log' = [i \in DOMAIN log |-> IF i > x.from /\ i <= x.to THEN [log[i] EXCEPT !.ing = @ + 1] ELSE log[i]]
  /\ seenKeys' = IF TrackSeen THEN seenKeys \cup x.lk ELSE seenKeys
IngestOK(t) == GenOK(t) /\ (cur = tk[t].from \/ Broken = "W1") /\ tk[t].to > tk[t].from
rt2Others == <<col, score, fld, wk, rd, mi, fleetv, waitn, missing, J, parked, running, clk, remind, pushes, sw,
               sprint, lease, vk, ahead, early, waivedRec, applied, raises>>
RT2(t) ==
  LET x == tk[t]
      ag == IF IngestOK(t) THEN agenda \cup x.lk ELSE agenda
  IN /\ x.pc = "read"
     /\ UNCHANGED <<rt2Others, bounds>>        \* first: a transition of another action fails here, cheaply
     /\ probe' = NoProbe
     /\ agenda' = ag
     /\ IF IngestOK(t) THEN IngestWrites(x) ELSE UNCHANGED <<cur, log, seenKeys>>
     /\ PlanOrLook(t, ag)
\* The error between the ingest's two writes (W2: the cursor moves first).
ErrorRT2(t) ==
  LET x == tk[t]
      ag == IF Broken = "W2" THEN agenda ELSE agenda \cup x.lk
  IN /\ x.pc = "read" /\ IngestOK(t) /\ Broken # "W1" /\ errs < MaxErrors
     /\ UNCHANGED <<rt2Others, acts, crashes, bugs>>
     /\ errs' = errs + 1
     /\ probe' = NoProbe
     /\ agenda' = ag
     /\ IF Broken = "W2" THEN IngestWrites(x) ELSE UNCHANGED <<cur, log, seenKeys>>
     /\ PlanOrLook(t, ag)

\* One request (RT3), atomic alone: the head of the tick's pending list. A
\* LIMIT is checked first (the size, in S.open), then the machine's refusals
\* (STALEGEN, STOPPED) and the guards: a race writes nothing and its key
\* stays. A plan that fit one request removes its key unless it requeues; a
\* cut plan removes none of its keys (W27: its first request does).
Settle(x, pend2) == [x EXCEPT !.pend = pend2, !.pc = IF pend2 = <<>> THEN "idle" ELSE "apply",
                              !.plans = IF pend2 = <<>> THEN NoPlans ELSE x.plans,
                              !.seen0 = IF pend2 = <<>> THEN <<>> ELSE x.seen0]

\* The tick-end note (errata 3 amendment 8; internal/sprint/machine/tickend.go):
\* the coordinator is woken once a tick, at its end, and not by a tick that
\* addressed it nothing. A tick counts in n the judgments its requests open
\* (J's new (type, subject, cause) records, not held); its last request is
\* followed by the tick-end step, a notes-only step of its own that carries
\* the lease generation: with n above zero it writes one tick-end note (ends),
\* and refused STALEGEN (a take since the tick read) it is owed (due) to the
\* loop's next RT1, whose error step writes it (RT1 sets due to 0 as it resets
\* the tick). A loop that dies loses what it owed, as it loses its halvings
\* (TickCrash). What a tick ingests of other writers' judgments (a verb's) is
\* not modelled: this model's log holds the keys a line queues, not its note.
\* W30: no tick-end note is written (the design before amendment 8).
Opens(JS2) == Cardinality({j \in JS2 : ~j.held /\ ~\E j0 \in J : Tri(j0) = Tri(j)})
TickEnd(t, x, pend2, o) ==
  LET n2 == x.n + o
      write == pend2 = <<>> /\ n2 > 0 /\ Broken # "W30"
  IN [Settle(x, pend2) EXCEPT !.n = n2,
        !.ends = IF write /\ GenOK(t) THEN x.ends + 1 ELSE x.ends,
        !.due = IF write /\ ~GenOK(t) THEN x.due + 1 ELSE x.due]
Applies(t) ==
  LET x == tk[t]
      P == x.plans[Head(x.pend)[1]]
      rq == Req(P, Head(x.pend)[2])
  IN Len(rq) <= L1Bound /\ GenOK(t) /\ (running \/ ~CardReq(rq)) /\ ReqGuard(rq)
Apply(t) ==
  /\ tk[t].pc = "apply" /\ tk[t].pend # <<>>
  /\ UNCHANGED <<beat, seen, cur, dropping, cut, receipts, lease, vk, bounds, seenKeys, applied>>
  /\ \E x \in {tk[t]} : \E P \in {tk[t].plans[Head(tk[t].pend)[1]]} :
     \E rq \in {Req(P, Head(tk[t].pend)[2])} : \E T \in {FoldU(Cur, rq)} :
     LET k == Head(x.pend)[1]
         i == Head(x.pend)[2]
         pend2 == Tail(x.pend)
         chunk1 == ChunkOf(k \in x.half) = 1
         single == NReq(P) = 1
         remove == IF single THEN ~RequeueAfter(k, P, T) /\ ~P.skip ELSE Broken = "W27" /\ i = 1
     IN IF Len(rq) > L1Bound
        THEN \* LIMIT, a bug: the error step names it; the key is planned at half
             \* the chunk, parked only at a chunk of one (W23: requeued unchanged).
             \* The error step carries the lease generation like every tick step
             \* (1.3.5): a stale loop's is refused STALEGEN and writes nothing.
             /\ IF Broken = "W23" \/ ~GenOK(t)
                THEN /\ tk' = [tk EXCEPT ![t] = TickEnd(t, x, pend2, 0)]
                     /\ UNCHANGED <<J, agenda, parked>>
                ELSE /\ J' = JOpen(J, "stepped", KeyS(k), "-")
                     /\ parked' = IF chunk1 THEN parked \cup {k} ELSE parked
                     /\ agenda' = IF chunk1 THEN agenda \ {k} ELSE agenda
                     /\ tk' = [tk EXCEPT ![t] = [TickEnd(t, x, pend2, Opens(J')) EXCEPT !.half = @ \cup {k}]]
             /\ probe' = NoProbe
             /\ UNCHANGED <<col, score, fld, wk, rd, mi, status, stab, sw, waitn, missing, remind, pushes, next, dcur, acur,
                            ahead, early, waivedRec, log, clk, raises, running>>
        ELSE IF ~Applies(t)
        THEN /\ tk' = [tk EXCEPT ![t] = TickEnd(t, x, pend2, 0)]
             /\ probe' = NoProbe
             /\ UNCHANGED <<commitv, agenda, parked, clk, raises, running>>
        ELSE /\ Commit(T, FALSE)
             \* R15's step stops the machine in the same call, as the stop verb
             \* does (donestop; W31: it writes its notice and does not stop).
             /\ \E stops \in {k[1] = "done" /\ Broken # "W31" /\ Len(rq) > 0} :
                /\ running' = IF stops THEN FALSE ELSE running
                \* R17's step writes its clock fields and counts its raise (a
                \* close with stoprearm starts the count again, H16).
                /\ clk' = CASE stops -> [since |-> -1, hold |-> 0, raised |-> FALSE]
                            [] k[1] = "stopped" -> rq[1].x.nclk
                            [] OTHER -> clk
                /\ raises' = CASE stops -> 0
                               [] k[1] = "stopped" /\ rq[1].x.raise -> raises + 1
                               [] k[1] = "stopped" /\ rq[1].x.close /\ "stoprearm" \in Fixes -> 0
                               [] OTHER -> raises
             /\ agenda' = IF remove THEN agenda \ {k} ELSE agenda
             /\ tk' = [tk EXCEPT ![t] = [TickEnd(t, x, pend2, Opens(J')) EXCEPT !.half = IF single THEN @ \ {k} ELSE @]]
             /\ probe' = IF Probes /\ single /\ (remove \/ T = Cur) /\ planv = x.seen0
                         THEN [a |-> "armed", t |-> t, k |-> k, removed |-> remove, noop |-> T = Cur]
                         ELSE NoProbe
             /\ UNCHANGED parked

\* A request refused as a bug that is not a LIMIT: named, and its key parked
\* (the park written before the agenda's removal, one step here). The
\* error step carries the lease generation (1.3.5): a stale loop's is
\* refused STALEGEN and writes nothing.
ParkOnBug(t) ==
  LET x == tk[t]
      k == Head(x.pend)[1]
  IN /\ bugs < MaxBugs /\ x.pc = "apply" /\ x.pend # <<>> /\ k[1] # "stopped"
     /\ IF GenOK(t)
        THEN /\ J' = JOpen(J, "stepped", KeyS(k), "-")
             /\ parked' = parked \cup {k}
             /\ agenda' = agenda \ {k}
        ELSE UNCHANGED <<J, parked, agenda>>
     /\ tk' = [tk EXCEPT ![t] = TickEnd(t, x, Tail(x.pend), Opens(J'))]
     /\ bugs' = bugs + 1
     /\ probe' = NoProbe
     /\ UNCHANGED <<col, score, fld, wk, rd, mi, fleetv, waitn, missing, log, cur, clock, sprint, lease, vk,
                    acts, crashes, errs, seenKeys, ahead, early, waivedRec, applied, raises>>

\* A loop dies: what it held in memory (its read, its plans, the halvings)
\* is gone; the store keeps what it wrote.
TickCrash(t) ==
  /\ tk[t].pc # "idle" /\ crashes < MaxCrashes
  /\ tk' = [tk EXCEPT ![t] = TkIdle({})]
  /\ crashes' = crashes + 1
  /\ probe' = NoProbe
  /\ UNCHANGED <<cards, fleetv, idxv, queue, clock, sprint, lease, vk, acts, errs, bugs, seenKeys, ahead,
                 early, waivedRec, applied, raises>>

\* The probe of E7 and ReplayNoop: a second delivery of a key right after a
\* run that removed it (or changed nothing), planned and applied again with
\* no action between. The design says it writes nothing.
RunTwice(t) ==
  /\ Probes /\ probe.a = "armed" /\ probe.t = t
  /\ \E P \in {PlanOf(probe.k, probe.k \in tk[t].half)} : \E rq \in {Req(P, 1)} :
       IF GenOK(t) /\ (running \/ ~CardReq(rq)) /\ Len(rq) <= L1Bound /\ ReqGuard(rq)
       THEN Commit(FoldU(Cur, rq), FALSE) ELSE UNCHANGED commitv
  /\ probe' = [a |-> "ran", removed |-> probe.removed, noop |-> probe.noop]
  /\ UNCHANGED <<beat, seen, agenda, parked, cur, running, clk, sprint, lease, tk, vk, bounds, seenKeys, applied, raises>>

-----------------------------------------------------------------------------
\* The verbs (1.5, 3). VerbPlan reads (the arguments, and a snapshot of what
\* the verb's guards compare); VerbApply checks the guards on the present
\* state and applies in one step, or is refused and writes nothing. A verb
\* is the coordinator's, a worker's, a reader's or the merger's; none is
\* fair, and each raises acts.

QHead(p) == col[p] = "merging" /\ \A q \in Prims : (S(q) = S(p) /\ col[q] = "merging") => (q = p \/ Before(p, q))
\* The op names a new drop --stream may take: those with no receipt.
FreeOps == {o \in Ops : ~\E r \in receipts : r.op = o}
ReworkOK(p) == col[p] = "review" \/ (col[p] = "ready" /\ fld[p].bound /\ fld[p].redeals >= MaxRedeals)
Placed(c) == col[c] \notin {"none", "removed"}
CanResume(o) == (\E r \in receipts : r.op = o) /\ ~\E r \in receipts : r.op = o /\ (r.k = 0 \/ r.final)

VArgs(v) ==
  CASE v = "add" -> Addable
    [] v \in {"take", "finishok", "finishfail", "land", "rework"} -> Prims
    [] v \in {"readbegin", "readok", "readbroken"} -> Prims \X Readers
    [] v = "release" -> Sents
    [] v = "drop" -> Cards
    [] v = "rank" -> Cards \X RankTo
    [] v \in {"ack", "wait"} -> {Tri(j) : j \in J}
    [] v \in {"fleetdown", "fleetup"} -> Members
    [] v = "dropstream" -> Streams
    [] v \in {"resume", "abort"} -> Ops
    [] OTHER -> {"sprint"}

VPre(v, a) ==
  CASE v = "add" -> col[a] = "none"
    [] v = "take" -> InReady(wk[a])
    [] v \in {"finishok", "finishfail"} -> InWorking(wk[a])
    [] v = "readbegin" -> rd[a[1]][a[2]].st = "asked"
    [] v \in {"readok", "readbroken"} -> rd[a[1]][a[2]].st = "begun"
    [] v = "land" -> QHead(a)
    [] v = "release" -> col[a] = "waiting"
    [] v = "drop" -> col[a] \in OpenCols
    [] v = "rank" -> col[a[1]] \in OpenCols /\ a[2] # score[a[1]] /\ (a[2] < next => a[2] % 2 = 1)
    [] v = "rework" -> ReworkOK(a)
    [] v = "ack" -> IsOpenJ(a[1], a[2], a[3]) /\ a[1] \in AckTypes
    [] v = "wait" -> IsOpenJ(a[1], a[2], a[3]) /\ a[1] \in HoldsTypes
    [] v = "waitstop" -> IsOpenJ("stopped", SprintS, "-")
    [] v = "fleetdown" -> TRUE
    [] v = "fleetup" -> status[a] # "up"
    [] v = "start" -> ~running
    [] v = "stop" -> running
    [] v = "dropstream" -> dropping[a] = None /\ \E c \in Cards : S(c) = a /\ col[c] \in OpenCols /\ FreeOps # {}
    [] v = "resume" -> CanResume(a)
    [] v = "abort" -> \E s \in Streams : dropping[s] = a
    [] OTHER -> FALSE

VSnap(v, a) ==
  CASE v = "add" -> [next |-> next, sig |-> IF Sigma(S(a)) = None THEN -1 ELSE score[Sigma(S(a))]]
    [] v \in {"take", "finishok", "finishfail"} -> WRec(wk[a])
    [] v \in {"readbegin", "readok", "readbroken"} -> rd[a[1]][a[2]].st
    [] v \in {"land", "release", "drop", "rework"} -> Rec(a)
    [] v = "rank" -> <<Rec(a[1]), next>>
    [] v \in {"fleetdown", "fleetup"} -> status[a]
    [] OTHER -> None

AddSc(a, sn) == IF AddScore[a] = -1 THEN sn.next ELSE AddScore[a]
\* H13's repair: a step after which an open card of a sentinel's stream
\* sorts before the sentinel closes its "sentinel reached": a card placed at
\* score x before it (a rank or an insertion), or the sentinel itself
\* ranked to x behind an open card of its stream (amendment 2).
ReachedPassed(j, c, x) ==
  /\ j.ty = "reached" /\ S(j.subj[2]) = S(c)
  /\ \/ j.subj[2] # c /\ x < score[j.subj[2]]
     \/ j.subj[2] = c /\ \E d \in Cards : d # c /\ S(d) = S(c) /\ col[d] \in OpenCols /\ score[d] < x
\* A card with needs, a sentinel, and a card behind an unlanded sentinel as
\* read go to waiting; the others to ready, guarded by the zguard.
AddDest(a, sn) == IF NeedsOf[a] # {} \/ a \in Sents \/ (sn.sig >= 0 /\ sn.sig < AddSc(a, sn)) THEN "waiting" ELSE "ready"
ScoreTaken(s, x) == \E c \in Cards : S(c) = s /\ Placed(c) /\ score[c] = x

VGuard(v, a, sn) ==
  CASE v = "add" ->
         /\ col[a] = "none" /\ ~Frozen(a)
         /\ AddScore[a] = -1 => next = sn.next                              \* COUNTER
         /\ AddScore[a] # -1 => ~ScoreTaken(S(a), AddScore[a])             \* rcount at the score
         /\ AddDest(a, sn) = "ready" =>
              ~\E g \in Sents : S(g) = S(a) /\ col[g] = "waiting" /\ score[g] < AddSc(a, sn)
    [] v \in {"take", "finishok", "finishfail"} -> WRec(wk[a]) = sn /\ ~Frozen(a)     \* by generation
    [] v \in {"readbegin", "readok", "readbroken"} -> rd[a[1]][a[2]].st = sn /\ col[a[1]] = "review" /\ ~Frozen(a[1])
    [] v = "land" -> Rec(a) = sn /\ QHead(a) /\ ~Frozen(a)
    [] v = "release" -> Rec(a) = sn /\ ~Frozen(a)
                        /\ (Broken = "W5" \/ NBefore(S(a), score[a]) = 0)   \* rcount of the open cells before G (W5: without)
    [] v = "drop" -> Rec(a) = sn /\ ~Frozen(a)
    [] v = "rank" -> /\ Rec(a[1]) = sn[1] /\ next = sn[2] /\ ~Frozen(a[1])
                     /\ ~ScoreTaken(S(a[1]), a[2])
    [] v = "rework" -> Rec(a) = sn /\ ReworkOK(a) /\ ~Frozen(a)
    [] v = "ack" -> IsOpenJ(a[1], a[2], a[3])
                    /\ (a[1] = "missing" => (col[a[3]] = "none" \/ Broken = "W22"))   \* waive only a need with no record
                    \* an ack that waives or clears changes the card: DROPPING (section 3)
                    /\ (a[1] \in {"dropped", "missing", "refused"} => ~Frozen(a[2][2]))
    [] v = "wait" -> IsOpenJ(a[1], a[2], a[3])
    [] v = "waitstop" -> IsOpenJ("stopped", SprintS, "-") /\ ~running
    [] v \in {"fleetdown", "fleetup"} -> status[a] = sn
    [] v = "start" -> ~running                                            \* MACHINESTATE
    [] v = "stop" -> running
    [] OTHER -> FALSE

DropEff(T, c) ==
  IF c \in Prims
  THEN [T EXCEPT !.col[c] = "removed",
                 !.wk[c] = [@ EXCEPT !.pl = {}, !.tu = -1, !.tf = -1, !.taken = FALSE],
                 !.rd[c] = [r \in Readers |-> Retire(T.rd[c][r])],
                 !.J = JCloseSubj(@, SubjOf(c))]
  ELSE [T EXCEPT !.col[c] = "removed", !.J = JCloseSubj(@, SubjOf(c))]

AckEff(a) ==
  LET ty == a[1]
      w == a[2][2]
      n == a[3]
      T0 == [Cur EXCEPT !.J = JClose(@, ty, a[2], n),
                        !.lk = OwnerKeys(ty, a[2], n) \cup (IF ty = "stepped" THEN {a[2][2]} ELSE {})]
  IN CASE ty = "dropped" -> [T0 EXCEPT !.fld[w].open = @ - 1, !.fld[w].waived = @ \cup {n}]
       [] ty = "missing" ->
            [T0 EXCEPT !.waitn[n] = @ \ {w}, !.fld[w].open = @ - 1, !.fld[w].waived = @ \cup {n},
                       !.missing = IF waitn[n] \ {w} = {} THEN @ \ {n} ELSE @,
                       !.waivedRec = @ \/ col[n] # "none"]
       [] ty = "refused" -> [T0 EXCEPT !.fld[w].refused = FALSE]
       [] OTHER -> T0

VEff(v, a, sn) ==
  CASE v = "add" ->
         LET sc == AddSc(a, sn)
             wo == {n \in NeedsOf[a] : col[n] \in OpenCols}
             wn == {n \in NeedsOf[a] : col[n] = "none"}
             wg == {n \in NeedsOf[a] : col[n] = "removed"}
         IN [Cur EXCEPT !.col[a] = AddDest(a, sn), !.score[a] = sc,
                        !.fld[a].open = Cardinality(wo) + Cardinality(wn) + Cardinality(wg),
                        !.waitn = [n \in Cards |-> IF n \in wo \cup wn THEN waitn[n] \cup {a} ELSE waitn[n]],
                        !.missing = (@ \cup wn) \ (IF "madeclose" \in Fixes THEN {a} ELSE {}),
                        !.J = {j \in @ : ~("madeclose" \in Fixes /\ j.ty = "missing" /\ j.cause = a)     \* H3's repair
                                         /\ ~("rankclose" \in Fixes /\ ReachedPassed(j, a, sc))}          \* H13's repair
                                \cup {[ty |-> "missing", subj |-> CS(a), cause |-> n, held |-> FALSE, hent |-> -1] : n \in wn}
                                \cup {[ty |-> "dropped", subj |-> CS(a), cause |-> n, held |-> FALSE, hent |-> -1] : n \in wg},
                        !.next = IF AddScore[a] = -1 THEN @ + 2 ELSE @]
    [] v = "take" ->                               \* unplaced (H15): the count ends; R6's 30 s entry restarts
         [Cur EXCEPT !.wk[a] = [@ EXCEPT !.pl = {<<MemberOf(wk[a]), "working">>}, !.taken = TRUE,
                                         !.repl = FALSE, !.tu = -1, !.tf = 1],
                     !.fld[a].tries = 0,
                     !.sw = IF StableOn /\ PlaceOn THEN 1 ELSE @,
                     !.J = JClose(@, "unplaced", CS(a), "-")]
    [] v \in {"finishok", "finishfail"} ->
         [Cur EXCEPT !.wk[a] = [@ EXCEPT !.pl = {}, !.tf = -1, !.taken = FALSE],
                     !.col[a] = "review", !.fld[a].result = IF v = "finishok" THEN "ok" ELSE "failed"]
    [] v = "readbegin" -> [Cur EXCEPT !.rd[a[1]][a[2]] = [@ EXCEPT !.st = "begun", !.t = 1]]
    [] v = "readok" -> [Cur EXCEPT !.rd[a[1]][a[2]] = [@ EXCEPT !.st = "ok", !.t = -1]]
    [] v = "readbroken" -> [Cur EXCEPT !.rd[a[1]][a[2]] = [@ EXCEPT !.st = "broken", !.t = -1]]
    [] v = "land" -> [Cur EXCEPT !.col[a] = "landed", !.mi[S(a)].t = 1]
    [] v = "release" -> [Cur EXCEPT !.col[a] = "landed", !.J = JClose(@, "reached", CS(a), "-"),
                                    !.early = @ \/ NBefore(S(a), score[a]) > 0]
    [] v = "drop" -> DropEff(Cur, a)
    [] v = "rank" -> [Cur EXCEPT !.score[a[1]] = a[2],
                                 !.next = IF a[2] >= next /\ Broken # "W17" THEN (a[2] \div 2) * 2 + 2 ELSE @,
                                 !.J = IF "rankclose" \in Fixes THEN {j \in @ : ~ReachedPassed(j, a[1], a[2])} ELSE @]
    [] v = "rework" ->
         [Cur EXCEPT !.fld[a] = [@ EXCEPT !.attempt = Min(@ + 1, MaxAttempts), !.bound = FALSE, !.refused = FALSE,
                                          !.redeals = 0, !.rereads = 0, !.result = "none", !.avoid = wk[a].m0,
                                          !.tries = 0],
                     !.col[a] = "ready",
                     !.wk[a] = [@ EXCEPT !.pl = {}, !.tu = -1, !.tf = -1, !.taken = FALSE],
                     !.rd[a] = NoReads,
                     !.J = {j \in @ : ~(j.subj = CS(a) /\ j.ty \in {"cannotask", "bound", "refused", "unplaced"})}]
    [] v = "ack" -> AckEff(a)
    [] v = "wait" ->                               \* a hold; its line queues nothing (W16: its owner key)
         [Cur EXCEPT !.J = {IF Tri(j) = a THEN [j EXCEPT !.held = TRUE, !.hent = 1] ELSE j : j \in J},
                     !.lk = IF Broken = "W16" THEN OwnerKeys(a[1], a[2], a[3]) ELSE {}]
    [] v = "waitstop" -> [Cur EXCEPT !.J = JClose(@, "stopped", SprintS, "-")]
    [] v = "fleetdown" -> [Cur EXCEPT !.status[a] = "held", !.stab[a] = IF StableOn THEN -1 ELSE @]
    [] v = "fleetup" -> [Cur EXCEPT !.status[a] = IF BeatFresh(a) THEN "up" ELSE "down",
                                    !.stab[a] = IF StableOn THEN (IF BeatFresh(a) THEN 1 ELSE -1) ELSE @,
                                    !.J = IF BeatFresh(a) THEN JClose(@, "nomember", SprintS, "-") ELSE @]
    [] v = "start" -> [Cur EXCEPT !.J = JClose(@, "stopped", SprintS, "-"),
                                  !.remind = IF Scn.goal THEN 0 ELSE @,
                                  !.lk = {DealK} \cup {<<"ask", j.subj[2]>> : j \in {j2 \in J : j2.ty = "cannotask"}}
                                         \cup (IF DoneOn THEN {DoneK} ELSE {})]
    [] OTHER -> Cur

vothers == <<beat, seen, agenda, cur, lease, tk, crashes, errs, bugs, seenKeys>>

VerbPlan(v) ==
  /\ vk[v].pc = "idle" /\ acts < MaxActs
  /\ \E vb \in Menu : \E a \in VArgs(vb) :
       /\ VPre(vb, a)
       /\ vk' = [vk EXCEPT ![v] =
                   CASE vb = "dropstream" ->
                          [VIdle EXCEPT !.pc = "cont", !.verb = "part", !.op = CHOOSE o \in FreeOps : TRUE, !.s = a,
                                        !.part = 1, !.cont = <<1, 0>>]
                     [] vb = "resume" ->
                          LET r == CHOOSE r \in receipts : r.op = a /\ \A r2 \in receipts : r2.op = a => r2.k <= r.k
                          IN [VIdle EXCEPT !.pc = "cont", !.verb = "part", !.op = a, !.s = r.s,
                                           !.part = r.k + 1, !.cont = r.cont]
                     [] OTHER -> [VIdle EXCEPT !.pc = "planned", !.verb = vb, !.arg = a, !.snap = VSnap(vb, a)]]
  /\ acts' = acts + 1
  /\ probe' = NoProbe
  /\ UNCHANGED <<commitv, beat, seen, agenda, parked, cur, running, clk, dropping, cut, receipts, lease, tk,
                 crashes, errs, bugs, seenKeys, applied, raises>>

VerbApply(v) ==
  LET x == vk[v] IN
  /\ x.pc = "planned" /\ x.verb \notin {"part", "abort"}
  /\ IF VGuard(x.verb, x.arg, x.snap)
     THEN /\ Commit(VEff(x.verb, x.arg, x.snap), TRUE)
          /\ running' = CASE x.verb = "start" -> TRUE [] x.verb = "stop" -> FALSE [] OTHER -> running
          /\ clk' = CASE x.verb \in {"start", "stop"} -> [since |-> -1, hold |-> 0, raised |-> FALSE]
                      [] x.verb = "waitstop" -> [clk EXCEPT !.hold = 1,
                                                  !.raised = @ /\ "spanreset" \notin Fixes]     \* H5's repair
                      [] OTHER -> clk
          /\ raises' = IF x.verb \in {"start", "stop"} \/ (x.verb = "waitstop" /\ "spanreset" \in Fixes)
                       THEN 0 ELSE raises
          /\ parked' = IF x.verb = "ack" /\ x.arg[1] = "stepped" THEN parked \ {x.arg[2][2]} ELSE parked
          /\ probe' = NoProbe
     ELSE /\ UNCHANGED <<commitv, running, clk, raises, parked>>
          /\ probe' = NoProbe
  /\ vk' = [vk EXCEPT ![v] = VIdle]
  /\ UNCHANGED <<beat, seen, agenda, cur, dropping, cut, receipts, lease, tk, bounds, seenKeys, applied>>

\* drop --stream in parts (1.5.4): part 1 marks the stream; each part removes
\* the head of the current cell (streams' cells in the order waiting, ready,
\* working, review, merging), so nothing is skipped or taken twice; the last
\* part clears the mark and writes the request line; each part moves the
\* cut clock and writes its receipt with its continuation.
CellOrder == <<"waiting", "ready", "working", "review", "merging">>
\* The bound on a part's fresh reads after a freezefirst refusal (the design
\* says "bounded"; the model takes 2).
PartRereads == 2
InCellT(s, ci, cl) == {c \in Cards : S(c) = s /\ cl[c] = CellOrder[ci]}
\* W14: the part resumes by offset instead of draining the head.
PartPick(s, cont) ==
  LET ci0 == cont[1]
      off == IF Broken = "W14" THEN cont[2] ELSE 0
      has(ci) == IF ci = ci0 THEN Cardinality(InCellT(s, ci, col)) > off ELSE InCellT(s, ci, col) # {}
      cis == {ci \in ci0..5 : has(ci)}
  IN IF cis = {} THEN <<None, 6, 0>>
     ELSE LET ci == CHOOSE i \in cis : \A j \in cis : i <= j
              o == IF ci = ci0 THEN off ELSE 0
          IN <<Sorted(InCellT(s, ci, col))[o + 1], ci, o>>
PartPlan(v) ==
  LET x == vk[v]
      pk == PartPick(x.s, x.cont)
  IN /\ x.pc = "cont"
     /\ vk' = [vk EXCEPT ![v] = [x EXCEPT !.pc = "planned",
                                         !.snap = [c |-> pk[1], ci |-> pk[2], off |-> pk[3],
                                                   rec |-> IF pk[1] = None THEN None ELSE Rec(pk[1])]]]
     /\ probe' = NoProbe
     /\ UNCHANGED <<commitv, beat, seen, agenda, parked, cur, running, clk, sprint, lease, tk, bounds, seenKeys,
                    applied, raises>>
PartApply(v) ==
  LET x == vk[v]
      sn == x.snap
      c == sn.c
      s == x.s
      own == IF x.part = 1 THEN dropping[s] = None ELSE (dropping[s] = x.op \/ Broken = "W13")
      fresh == ~\E r \in receipts : r.op = x.op /\ r.k \in {x.part, 0}
      passed == \A ci \in 1..(sn.ci - 1) : InCellT(s, ci, col) = {}
      guard == /\ own
               /\ c = None \/ Rec(c) = sn.rec
               /\ fresh
               /\ "freezefirst" \notin Fixes \/ passed                                                  \* H11's repair
      moved == "freezefirst" \in Fixes /\ own /\ fresh /\ ~passed
      T1 == IF c = None THEN Cur ELSE DropEff(Cur, c)
      cont2 == <<Min(sn.ci, 5), IF Broken = "W14" THEN sn.off + 1 ELSE 0>>
      final == sn.ci > 5 \/ (Cardinality(InCellT(s, sn.ci, T1.col)) <= cont2[2]
                             /\ \A ci \in (sn.ci + 1)..5 : InCellT(s, ci, T1.col) = {})
      T == [T1 EXCEPT !.lk = IF final THEN {ResolveK(s), PullK(s), DealK} ELSE {},
                      !.J = IF final THEN JClose(@, "opcut", OpS(x.op), "-") ELSE @]
  IN /\ x.pc = "planned" /\ x.verb = "part"
     /\ IF guard
        THEN /\ Commit(T, TRUE)
             /\ dropping' = [dropping EXCEPT ![s] = IF final \/ Broken = "W13" THEN None ELSE x.op]
             /\ cut' = [cut EXCEPT ![x.op] = IF final THEN -1 ELSE 1]
             /\ receipts' = receipts \cup {[op |-> x.op, k |-> x.part, s |-> s, cont |-> cont2, final |-> final]}
             /\ applied' = [applied EXCEPT ![<<x.op, Min(x.part, MaxParts)>>] = @ + 1]
             /\ vk' = [vk EXCEPT ![v] = IF final THEN VIdle ELSE [x EXCEPT !.pc = "cont", !.part = @ + 1, !.cont = cont2,
                                                                                 !.rr = 0]]
             /\ probe' = NoProbe
        ELSE /\ UNCHANGED <<commitv, dropping, cut, receipts, applied>>
             \* freezefirst (H11, amendment 2): a part refused because a card
             \* moved back to a passed cell returns the verb to its continue
             \* state, to read again, at most PartRereads times; any other
             \* refusal ends the verb.
             /\ vk' = [vk EXCEPT ![v] = IF moved /\ x.rr < PartRereads THEN [x EXCEPT !.pc = "cont", !.rr = @ + 1]
                                        ELSE VIdle]
             /\ probe' = NoProbe
     /\ UNCHANGED <<beat, seen, agenda, parked, cur, running, clk, next, dcur, acur, lease, tk, bounds, seenKeys, raises>>
\* drop --abort --op: clears the op's marks and cut entry, with the request line.
AbortApply(v) ==
  LET x == vk[v]
      o == x.arg
      ss == {s \in Streams : dropping[s] = o}
  IN /\ x.pc = "planned" /\ x.verb = "abort"
     /\ IF ss # {} /\ ~\E r \in receipts : r.op = o /\ r.k = 0
        THEN /\ Commit([Cur EXCEPT !.lk = UNION {{ResolveK(s), PullK(s)} : s \in ss} \cup {DealK},
                                   !.J = JClose(@, "opcut", OpS(o), "-")], TRUE)
             /\ dropping' = [s \in Streams |-> IF s \in ss THEN None ELSE dropping[s]]
             /\ cut' = [cut EXCEPT ![o] = -1]
             /\ receipts' = receipts \cup {[op |-> o, k |-> 0, s |-> None, cont |-> <<0, 0>>, final |-> TRUE]}
             /\ probe' = NoProbe
        ELSE /\ UNCHANGED <<commitv, dropping, cut, receipts>>
             /\ probe' = NoProbe
     /\ vk' = [vk EXCEPT ![v] = VIdle]
     /\ UNCHANGED <<beat, seen, agenda, parked, cur, running, clk, next, dcur, acur, lease, tk, bounds, seenKeys, applied, raises>>

\* A verb's process dies between its read and its step, or between parts.
VerbCrash(v) ==
  /\ vk[v].pc \in {"planned", "cont"} /\ crashes < MaxCrashes
  /\ vk' = [vk EXCEPT ![v] = VIdle]
  /\ crashes' = crashes + 1
  /\ probe' = NoProbe
  /\ UNCHANGED <<cards, fleetv, idxv, queue, clock, sprint, lease, tk, acts, errs, bugs, seenKeys, ahead, early,
                 waivedRec, applied, raises>>

-----------------------------------------------------------------------------
\* Outside, unbounded: beats and time.

\* A beat moves beat:m to R + 15 s, and enters seen:m at R for a member that
\* is down (held does not count). It writes no line and changes no card.
Beat(m) ==
  /\ beat' = [beat EXCEPT ![m] = 1]
  /\ seen' = IF status[m] = "down" THEN [seen EXCEPT ![m] = TRUE] ELSE seen
  /\ <<beat', seen'>> # <<beat, seen>>
  /\ probe' = NoProbe
  /\ UNCHANGED <<cards, status, stab, idxv, queue, clock, sprint, procs, bounds, seenKeys, ahead, early, waivedRec,
                 applied, raises>>

\* One unit of time passes. Running-time timers and entries move only while
\* RUNNING (W3: the card entries in wall time; W12: the STOPPED judgment's
\* hold in running time); R17's fields and the cut entries are wall time.
\* stablesince: a member up for two beat periods becomes stable, and R6's
\* 30 s entry moves, in running time (one unit stands for each).
Wall ==
  LET rt(y) == IF running THEN Dec(y) ELSE y
      re(y) == IF running \/ Broken = "W3" THEN Dec(y) ELSE y
      nwk == [p \in Prims |-> [wk[p] EXCEPT !.tu = rt(@), !.tf = rt(@), !.eu = re(@), !.ef = re(@)]]
      nrd == [p \in Prims |-> [r \in Readers |-> [rd[p][r] EXCEPT !.t = rt(@), !.e = re(@)]]]
      nmi == [s \in Streams |-> [mi[s] EXCEPT !.t = rt(@), !.e = re(@)]]
      nbeat == [m \in Members |-> IF m \in Steady THEN beat[m] ELSE rt(beat[m])]   \* a steady member keeps beating
      nstab == [m \in Members |-> rt(stab[m])]
      nsw == IF sw = 2 THEN sw ELSE rt(sw)
      nJ == {[j EXCEPT !.hent = rt(@)] : j \in J}
      nclk == [clk EXCEPT !.since = Dec(@), !.hold = IF Broken = "W12" THEN rt(@) ELSE Dec(@)]
      ncut == [o \in Ops |-> Dec(cut[o])]
      nrem == rt(remind)
  IN /\ <<nwk, nrd, nmi, nbeat, nstab, nsw, nJ, nclk, ncut, nrem>> # <<wk, rd, mi, beat, stab, sw, J, clk, cut, remind>>
     /\ wk' = nwk /\ rd' = nrd /\ mi' = nmi /\ beat' = nbeat /\ J' = nJ /\ clk' = nclk
     /\ stab' = nstab /\ sw' = nsw
     /\ cut' = ncut /\ remind' = nrem
     /\ probe' = NoProbe
     /\ UNCHANGED <<col, score, fld, status, seen, waitn, missing, queue, running, pushes, dropping, receipts,
                    next, dcur, acur, procs, bounds, seenKeys, ahead, early, waivedRec, applied, raises>>

-----------------------------------------------------------------------------
TickNext(t) == RT1(t) \/ ReadEvents(t) \/ RT2(t) \/ Apply(t)

Next ==
  \/ \E t \in Ticks : TickNext(t) \/ TickCrash(t) \/ ErrorRT1(t) \/ ErrorRT2(t) \/ ParkOnBug(t) \/ RunTwice(t)
  \/ LeaseExpire
  \/ \E v \in VerbProcs : VerbPlan(v) \/ VerbApply(v) \/ PartPlan(v) \/ PartApply(v) \/ AbortApply(v) \/ VerbCrash(v)
  \/ \E m \in Beaters : Beat(m)
  \/ Wall

\* Fairness on the tick and on time only; none on the coordinator, workers,
\* readers, merger or beats (section 5).
Fairness == /\ \A t \in Ticks : WF_vars(TickNext(t))
            /\ WF_vars(Wall)
Spec == Init /\ [][Next]_vars /\ Fairness

-----------------------------------------------------------------------------
\* Invariants (section 5).

IsOpen(c) == col[c] \in OpenCols
Ended(c) == col[c] \in {"landed", "removed"}

TypeOK ==
  /\ col \in [Cards -> Cols]
  /\ running \in BOOLEAN
  /\ cur \in 0..Len(log)
  /\ \A t \in Ticks : tk[t].pc \in {"idle", "leased", "popped", "read", "ingested", "apply"}

\* A card is in at most one place in each table (the fleet: one live work card).
OnePlace == \A p \in Prims : Cardinality(wk[p].pl) <= 1

\* E2: the lines ingested are exactly those at or before the cursor, once each.
IngestOnce == \A i \in DOMAIN log : log[i].ing = IF i <= cur THEN 1 ELSE 0

\* The entry that queues a key when it fires (a due or cut entry, a hold's
\* entry, a beat:m or seen:m entry).
EntryQueues(k) ==
  CASE k[1] = "late:untaken"    -> wk[k[2]].eu >= 0
    [] k[1] = "late:unfinished" -> wk[k[2]].ef >= 0
    [] k[1] = "late:unread"     -> rd[k[2][1]][k[2][2]].e >= 0
    [] k[1] = "late:mergeidle"  -> mi[k[2]].e >= 0
    [] k[1] = "down"            -> beat[k[2]] >= 0
    [] k[1] = "seen"            -> seen[k[2]]
    [] k[1] = "remind"          -> remind >= 0
    [] k[1] = "late:cut"        -> cut[k[2]] >= 0
    [] k[1] = "hold"            -> \E j \in J : Tri(j) = k[2] /\ j.hent >= 0
    [] k[1] = "deal"            -> sw \in {0, 1}              \* R6's 30 s entry (stablesince)
    [] OTHER                    -> FALSE
Covered(k) == k \in agenda \/ k \in parked \/ k \in PendingKeys \/ EntryQueues(k)
\* A rule whose plan on the present state is not empty is owed: its key is
\* queued, parked, on a line after the cursor, or an entry will queue it.
\* R1's work (a down member's status) is owed only to a beat, which is an
\* outside event and unfair; its key is left out.
Owes(k) == k[1] # "seen" /\ NonEmpty(PlanOf(k, FALSE))
NoLostWork == \A k \in AllKeys : Owes(k) => Covered(k)
\* E3, CursorSound: the keys of the lines at or before the cursor. As
\* written in the design (queued, held or parked, or quiet) it leaves out a
\* key owed again by a later line: CursorSoundLiteral is that text, and fails.
CursorSound == \A k \in seenKeys : Owes(k) => Covered(k)
CursorSoundLiteral == \A k \in seenKeys : Owes(k) => (k \in agenda \/ k \in parked)

\* D1: a card in a timed state has its entry with its due field; or the entry
\* fired and the late key is owed, or the lateness judgment is open or held.
LateOwed(k, ty, sb, ca) == k \in agenda \/ k \in parked \/ k \in PendingKeys \/ Present(ty, sb, ca)
DueAgrees ==
  /\ \A p \in Prims :
       /\ DU(wk[p]) # -1 => (wk[p].eu = wk[p].tu \/ (wk[p].eu = -1 /\ LateOwed(<<"late:untaken", p>>, "latework", WS(p), "untaken")))
       /\ wk[p].eu # -1 => DU(wk[p]) # -1
       /\ DF(wk[p]) # -1 => (wk[p].ef = wk[p].tf \/ (wk[p].ef = -1 /\ LateOwed(<<"late:unfinished", p>>, "latework", WS(p), "unfinished")))
       /\ wk[p].ef # -1 => DF(wk[p]) # -1
       /\ \A r \in Readers :
            /\ DR(rd[p][r]) # -1 => (rd[p][r].e = rd[p][r].t \/
                                     (rd[p][r].e = -1 /\ LateOwed(<<"late:unread", <<p, r>>>>, "lateread", RS(p, r), "unread")))
            /\ rd[p][r].e # -1 => DR(rd[p][r]) # -1
  /\ \A s \in Streams :
       /\ DM(s, col, mi[s].t) # -1 => (mi[s].e = mi[s].t \/ (mi[s].e = -1 /\ LateOwed(<<"late:mergeidle", s>>, "latemerge", StrS(s), "-")))
       /\ mi[s].e # -1 => DM(s, col, mi[s].t) # -1

\* D1 as written: the late key queued (in the agenda), not on a line after
\* the cursor.
LateOwedLit(k, ty, sb, ca) == k \in agenda \/ k \in parked \/ Present(ty, sb, ca)
DueAgreesLiteral ==
  \A p \in Prims :
    /\ DU(wk[p]) # -1 => (wk[p].eu = wk[p].tu \/ (wk[p].eu = -1 /\ LateOwedLit(<<"late:untaken", p>>, "latework", WS(p), "untaken")))
    /\ DF(wk[p]) # -1 => (wk[p].ef = wk[p].tf \/ (wk[p].ef = -1 /\ LateOwedLit(<<"late:unfinished", p>>, "latework", WS(p), "unfinished")))

\* I1 for the indexes written by intents: wait:n holds exactly the waiting
\* cards naming n (not waived) while n has no record or is open, and only
\* waiting cards naming n after; a missing need with waiters is in missing.
Waits(w, n) == col[w] = "waiting" /\ n \in NeedsOf[w] /\ n \notin fld[w].waived
IndexAgrees ==
  /\ \A n \in Cards : \A w \in waitn[n] : Waits(w, n)
  /\ \A n \in Cards : col[n] \in {"none"} \cup OpenCols => \A w \in Cards : Waits(w, n) => w \in waitn[n]
  /\ \A n \in Cards : (col[n] = "none" /\ waitn[n] # {}) => n \in missing
\* I1 as written: missing is exactly the needs with no record and waiters.
IndexAgreesLiteral == IndexAgrees /\ \A n \in missing : col[n] = "none" /\ waitn[n] # {}
\* I2.
OpenExact == \A w \in Cards : col[w] = "waiting" =>
               fld[w].open = Cardinality({n \in Cards : w \in waitn[n]}) + Cardinality({n \in Cards : IsOpenJ("dropped", CS(w), n)})

\* I3: the first member of an index a rule reads, in the rule's order, is a
\* card the rule would change now, unless a shared condition holds it back.
HeadIn(u, h) == IF u.op \in {"needmet", "needgone"} THEN h \in u.x.ws ELSE u.c = h
UnitChanges(P, h) == \E i \in DOMAIN P.units : Changes(P.units[i]) /\ HeadIn(P.units[i], h)
HeadActionable ==
  /\ \A s \in Streams :
       LET E == {c \in Elig(s) : BelowSigma(s, score[c])} IN
       (E # {} /\ running /\ dropping[s] = None) => UnitChanges(PlanResolve(ResolveK(s), Chunk), Sorted(E)[1])
  /\ (Dealable # {} /\ running /\ DealRoom > 0) => UnitChanges(PlanDeal(DealK, Chunk), TurnSorted(Dealable)[1])
  /\ \A n \in Cards :
       LET W == {w \in waitn[n] : ~Frozen(w)} IN
       (col[n] \in {"landed", "removed"} /\ W # {} /\ running) => UnitChanges(PlanNeeds(<<"needs", n>>, Chunk), Sorted(W)[1])

\* No primary sorting after an unlanded sentinel of its stream is past ready
\* unless it was first dealt before the sentinel was placed; no sentinel
\* lands while an open card of its stream sorts before it.
PositionHolds ==
  /\ \A p \in Prims : col[p] \in {"working", "review", "merging", "landed"} =>
        \A g \in ahead[p] : ~(col[g] = "waiting" /\ score[g] < score[p])
  /\ ~early

\* A primary outside waiting has every need landed, or dropped or missing
\* and waived; a waived missing need had no record when it was waived.
NeedsHold ==
  /\ \A w \in Prims : col[w] \in {"ready", "working", "review", "merging", "landed"} =>
        \A n \in NeedsOf[w] : col[n] = "landed" \/ n \in fld[w].waived
  /\ ~waivedRec

\* A parked key whose rule could move c (section 5: k.rule \in
\* RulesThatMove(c)), or raise the judgment that names it.
ParkedMoves(k, c) ==
  \/ k[1] = "deal" /\ col[c] = "ready"
  \/ k[1] = "down" /\ c \in Prims /\ \E x \in wk[c].pl : x[1] = k[2]
  \/ k[1] = "resolve" /\ S(c) = k[2] /\ col[c] = "waiting"
  \/ k[1] = "pullback" /\ S(c) = k[2] /\ col[c] = "ready"
  \/ k[1] = "needs" /\ c \in waitn[k[2]]
  \/ k[1] = "made" /\ \E j \in J : j.ty = "missing" /\ j.subj = CS(c) /\ j.cause = k[2]
  \/ k[1] \in {"ask", "accept", "rework", "late:untaken", "late:unfinished"} /\ k[2] = c
  \/ k[1] = "late:unread" /\ k[2][1] = c
  \/ k[1] = "late:mergeidle" /\ S(c) = k[2] /\ col[c] = "merging"
  \/ k[1] = "late:cut" /\ dropping[S(c)] = k[2]
  \/ k[1] = "hold" /\ (k[2][2] \in SubjOf(c) \/ k[2][2] = SprintS)
\* The judgments that name a card (section 5's Named, with a card's work and
\* read cards as its subjects, and a hold counted as named).
Named(c) ==
  \/ \E j \in J : j.subj \in SubjOf(c)
  \/ \E j \in J : j.subj = SprintS /\ j.ty \in {"nomember", "nostable", "stopped"}
  \/ ~running /\ clk.hold > 0
  \/ col[c] = "merging" /\ Present("latemerge", StrS(S(c)), "-")
  \/ dropping[S(c)] # None /\ Present("opcut", OpS(dropping[S(c)]), "-")
  \/ \E k \in parked : ParkedMoves(k, c)
\* (d) of R16's table: what a card waits on (an open need, the first
\* sentinel, the cards before a sentinel, the cards filling every ready
\* cell deal may fill).
WaitsOn(c) ==
  IF col[c] = "waiting"
  THEN LET g == Sigma(S(c)) IN
       {n \in Cards : c \in waitn[n] /\ col[n] \in OpenCols}
       \cup (IF g # None /\ g # c /\ score[g] < score[c] THEN {g} ELSE {})
       \cup (IF c = g THEN {d \in Cards : S(d) = S(c) /\ col[d] \in OpenCols /\ score[d] < score[c]} ELSE {})
  ELSE IF col[c] = "ready" /\ DealUp # {} /\ DealRoom = 0 THEN UNION {ReadyAt(m) : m \in DealUp}
  ELSE {}
RECURSIVE NamedD(_, _)
NamedD(c, d) == Named(c) \/ (d > 0 /\ \E x \in WaitsOn(c) : NamedD(x, d - 1))

\* NothingSilent: every open card has a local holder, row by row of R16's
\* table: (a) an outside actor before its deadline, (b) a rule whose
\* condition the state meets, (c) a judgment open or held, (d) what it waits
\* on, itself held, (e) the machine STOPPED.
ActorBefore(c) ==                                                      \* (a)
  \/ c \in Prims /\ col[c] = "working" /\ MemberOf(wk[c]) # None /\ status[MemberOf(wk[c])] = "up"
     /\ ((InReady(wk[c]) /\ wk[c].tu > 0) \/ (InWorking(wk[c]) /\ wk[c].tf > 0))
  \/ c \in Prims /\ col[c] = "review" /\ \E r \in Readers : rd[c][r].st \in {"asked", "begun"} /\ rd[c][r].t > 0
  \/ col[c] = "merging" /\ mi[S(c)].t > 0
RuleHolds(c) ==                                                        \* (b)
  \/ col[c] = "waiting" /\ \E n \in Cards : c \in waitn[n] /\ col[n] \in {"landed", "removed"}              \* R4
  \/ c \in Prims /\ col[c] = "waiting" /\ fld[c].open = 0 /\ ~fld[c].refused /\ BelowSigma(S(c), score[c])
     /\ dropping[S(c)] = None                                                                               \* R3 release
  \/ c \in Sents /\ c = Sigma(S(c)) /\ NBefore(S(c), score[c]) = 0 /\ fld[c].open = 0                       \* R3 reach
  \/ c \in Prims /\ col[c] = "ready" /\ (c \in FreshBelow(S(c)) \/ c \in Again(S(c)))
     /\ (DealRoom > 0 \/ (StableOn /\ Up # {} /\ DealUp = {}))              \* R6 (stablesince: arms or fires its 30 s entry)
  \/ c \in Prims /\ col[c] = "ready" /\ c \in Fresh(S(c)) /\ ~BelowSigma(S(c), score[c])                     \* R19
  \/ c \in Prims /\ col[c] = "working" /\ MemberOf(wk[c]) # None
     /\ (status[MemberOf(wk[c])] # "up" \/ (InReady(wk[c]) /\ wk[c].tu = 0) \/ (InWorking(wk[c]) /\ wk[c].tf = 0))  \* R2, R11
  \/ c \in Prims /\ col[c] = "review" /\ ~fld[c].bound
     /\ \/ fld[c].result = "ok" /\ \A r \in Readers : rd[c][r].st = "none"                                  \* R8
        \/ fld[c].result = "ok" /\ Cardinality({r \in Readers : rd[c][r].st = "ok"}) >= 2                   \* R9
        \/ fld[c].result = "failed" \/ \E r \in Readers : rd[c][r].st = "broken"                            \* R10
        \/ \E r \in Readers : rd[c][r].st \in {"asked", "begun"} /\ rd[c][r].t = 0                          \* R11
  \/ col[c] = "merging" /\ mi[S(c)].t = 0                                                                   \* R11
  \/ Frozen(c) /\ (cut[dropping[S(c)]] >= 0 \/ <<"late:cut", dropping[S(c)]>> \in agenda)                   \* the cut clock
NothingSilent ==
  \A c \in Cards : IsOpen(c) =>
    \/ ~running                                                        \* (e)
    \/ Named(c)                                                        \* (c)
    \/ ActorBefore(c)
    \/ RuleHolds(c)
    \/ WaitsOn(c) # {}                                                 \* (d)
    \/ col[c] = "ready" /\ DealRoom = 0 /\ (DealUp # {} \/ Up = {})   \* (d) every ready cell deal may fill is full (vacuous with none up)

\* One open judgment per (type, subject, cause); none while a hold on it is
\* before its time.
JudgmentOnce == \A j1, j2 \in J : (~j1.held /\ ~j2.held /\ Tri(j1) = Tri(j2)) => j1 = j2
\* The coordinator's one wake a tick (errata 3 amendment 8): a tick that
\* opened a judgment ends with exactly one tick-end note, written or owed to
\* the loop's next RT1; a tick that opened none, with none (W30).
\* R15 (donestop): a machine left running with the sprint done is one whose
\* done key is still owed (queued, on a line not ingested, or parked and
\* named), never one every loop has passed at rest (W31).
DoneStops == DoneOn => ((running /\ SprintDone /\ \A t \in Ticks : tk[t].pc = "idle")
                        => (DoneK \in agenda \/ DoneK \in PendingKeys \/ DoneK \in parked))
TickEndOnce == \A t \in Ticks : tk[t].pc = "idle" => tk[t].ends + tk[t].due = IF tk[t].n > 0 THEN 1 ELSE 0
WaitHolds == ~\E j1, j2 \in J : Tri(j1) = Tri(j2) /\ j1.held /\ j1.hent > 0 /\ ~j2.held

\* Every decision printed for an open judgment is accepted by its verb's
\* guard in the present state (2.2's decisions, as far as the model has the
\* verbs; W21: the late-work judgment offers rework).
Decisions(j) ==
  LET sb == j.subj IN
  CASE j.ty = "reached"   -> {<<"release", sb[2]>>, <<"drop", sb[2]>>}
    [] j.ty = "dropped"   -> {<<"ack", Tri(j)>>, <<"drop", sb[2]>>}
    [] j.ty = "missing"   -> {<<"drop", sb[2]>>}
                             \cup (IF col[j.cause] = "none" THEN {<<"ack", Tri(j)>>} ELSE {})   \* "while n has no record"
                             \* madeclose (H3): the row prints add n only while n has no record
                             \cup (IF "madeclose" \notin Fixes \/ col[j.cause] = "none" THEN {<<"add", j.cause>>} ELSE {})
    [] j.ty = "cannotask" -> {<<"rework", sb[2]>>, <<"drop", sb[2]>>, <<"wait", Tri(j)>>}
    [] j.ty = "nomember"  -> {<<"wait", Tri(j)>>} \cup {<<"fleetup", m>> : m \in {m2 \in Members : status[m2] = "held"}}
    [] j.ty = "nostable"  -> {<<"wait", Tri(j)>>}
    [] j.ty = "bound"     -> {<<"rework", sb[2]>>, <<"drop", sb[2]>>, <<"wait", Tri(j)>>}
    [] j.ty = "latework"  -> {<<"drop", sb[2]>>, <<"wait", Tri(j)>>}
                             \cup (IF MemberOf(wk[sb[2]]) # None THEN {<<"fleetdown", MemberOf(wk[sb[2]])>>} ELSE {})
                             \cup (IF Broken = "W21" THEN {<<"rework", sb[2]>>} ELSE {})
    [] j.ty = "lateread"  -> {<<"drop", sb[2]>>, <<"wait", Tri(j)>>}
    [] j.ty = "latemerge" -> {<<"land", p>> : p \in {q \in Prims : S(q) = sb[2] /\ QHead(q)}} \cup {<<"wait", Tri(j)>>}
    [] j.ty = "refused"   -> {<<"ack", Tri(j)>>, <<"drop", sb[2]>>, <<"wait", Tri(j)>>}
    [] j.ty = "stepped"   -> {<<"ack", Tri(j)>>, <<"wait", Tri(j)>>} \cup (IF running THEN {<<"stop", "sprint">>} ELSE {})
    [] j.ty = "stopped"   -> {<<"start", "sprint">>, <<"waitstop", "sprint">>}
    [] j.ty = "opcut"     -> {<<"abort", sb[2]>>, <<"wait", Tri(j)>>} \cup (IF CanResume(sb[2]) THEN {<<"resume", sb[2]>>} ELSE {})
    [] j.ty = "unplaced"  -> {<<"drop", sb[2]>>}                  \* H15's row, not decided: drop only
    [] OTHER -> {}
Accepted(d) ==
  CASE d[1] = "resume" -> CanResume(d[2])
    [] d[1] = "abort"  -> VPre("abort", d[2]) /\ ~\E r \in receipts : r.op = d[2] /\ r.k = 0
    [] OTHER           -> VPre(d[1], d[2]) /\ VGuard(d[1], d[2], VSnap(d[1], d[2]))
\* H8's repair: a decision that DROPPING refuses (a verb that changes a card
\* of a stream being dropped: drop, rework, release, land, and ack of a
\* dropped, missing or refused judgment, which waives or clears; and add n
\* while n's stream is being dropped, amendment 2) is not printed.
Printed(d) == ~("dropcond" \in Fixes /\
                 \/ d[1] \in {"drop", "rework", "release", "land", "add"} /\ Frozen(d[2])
                 \/ d[1] = "ack" /\ d[2][1] \in {"dropped", "missing", "refused"} /\ Frozen(d[2][2][2]))
Answerable == \A j \in J : ~j.held => \A d \in Decisions(j) : Printed(d) => Accepted(d)

\* V4: a part identity is applied at most once.
OpOnce == \A x \in DOMAIN applied : applied[x] <= 1

\* U1, U2.
UniqueScores == \A a, b \in Cards : (a # b /\ S(a) = S(b) /\ Placed(a) /\ Placed(b)) => score[a] # score[b]
ScoresBelowCounter == \A c \in Cards : Placed(c) => score[c] < next

\* "The machine is STOPPED and moves are due" is open only when a dry plan
\* would change a card, and is raised at most once per STOPPED span (with
\* spanreset, per span and wait; with stoprearm, per span and close).
DryDueAll == \E k \in agenda \cup PendingKeys : CardChange(PlanOf(k, FALSE))
StoppedJudgmentTrue ==
  /\ IsOpenJ("stopped", SprintS, "-") => (~running /\ DryDueAll)
  /\ raises <= 1
\* The part of it that holds with the H7 repair at every state: raised at
\* most once per span. (The rest is StoppedStaleCloses and StoppedRaiseFresh.)
StoppedOnce == raises <= 1

\* H15's repair (unplaced): a card dealt and withdrawn MaxPlaceTries times
\* without a take is named ("this card cannot be placed") until it is taken.
\* With stablesince alone the count is a ghost, and this fails.
UnplacedNamed == \A p \in Prims : (IsOpen(p) /\ fld[p].tries >= MaxPlaceTries) => Named(p)

\* T6: every step sent fits the model's step bound.
StepWithinBounds == \A t \in Ticks : \A k \in DOMAIN tk[t].plans :
                      \A i \in 1..NReq(tk[t].plans[k]) : Len(Req(tk[t].plans[k], i)) <= StepBound

\* Errata 3, amendment 4 (R6): the cards a deal takes are each stream's front
\* in turn (the owner's ruling of 2026-09-30, "the whole point is that multiple
\* work streams are worked on in parallel"). Stated of the plan the deal would
\* make, from the counts and not from TurnSorted: within a stream the cards
\* taken are its lowest by work order, and a stream with a dealable card left
\* over is taken from at most once for each card of any other stream that came
\* in a full turn (a stream taken n_t times while a stream u has a card left
\* has at most n_t + 1 taken if it is before u in the streams' order, else at
\* most n_t).
DealTaken(P) == {u.c : u \in {P.units[i] : i \in DOMAIN P.units}} \cap Dealable
DealTakesTurns ==
  (Dealable # {} /\ DealRoom > 0) =>
    LET T == DealTaken(PlanDeal(DealK, Chunk))
        n(x) == Cardinality({c \in T : S(c) = x})
    IN /\ \A c \in T, d \in Dealable \ T : S(c) = S(d) => Before(c, d)
       /\ \A d \in Dealable \ T : \A x \in Streams :
             n(x) <= n(S(d)) + (IF n(x) > 0 /\ x # S(d) /\ StreamOrd(x) < StreamOrd(S(d)) THEN 1 ELSE 0)

\* Errata 3, amendment 5 (R6): the deal goes round the fleet. With every member
\* up and idle (no ready card at any), consecutive deals go to distinct members
\* in the fixed order, from the rolling index dcur, until every member has one:
\* the i-th card the plan deals goes to the member i - 1 places past dcur in the
\* ring of the members (no card dealt is one to avoid a member, which shifts
\* the ring). Stated from dcur and the ring, not from RoundOne.
DealOp(u) == u.op \in {"deal", "redealw"}
DealGoesRound ==
  (Dealable # {} /\ DealRoom > 0 /\ DealUp = Members /\ \A m \in Members : RCount(m) = 0) =>
    LET dl == SelectSeq(PlanDeal(DealK, Chunk).units, DealOp)
    IN (\A i \in DOMAIN dl : fld[dl[i].c].avoid = None) =>
         \A i \in 1..Min(Len(dl), Cardinality(Members)) : dl[i].m = At(MemSeq, dcur, i - 1)

-----------------------------------------------------------------------------
\* Action properties (section 5). Lifecycle and DropComplete speak of steps,
\* so they are action properties here, not state invariants.

\* A rule run a second time on the same keys, with nothing changed, writes nothing.
ReplayNoop == [][(probe'.a = "ran" /\ probe'.removed) => UNCHANGED commitv]_vars
\* A rule whose apply changed nothing, planned again at once, plans nothing.
QuietStaysQuiet == [][(probe'.a = "ran" /\ probe'.noop) => UNCHANGED commitv]_vars
\* An apply that requeues its own key strictly lowers that key's variant: the
\* head request of a single-request plan, applied, its key kept.
Requeues(t, k) ==
  /\ tk[t].pc = "apply" /\ tk[t].pend # <<>> /\ Head(tk[t].pend)[1] = k
  /\ tk'[t].pend = Tail(tk[t].pend) /\ crashes' = crashes /\ bugs' = bugs
  /\ Applies(t)
  /\ NReq(tk[t].plans[k]) = 1 /\ ~tk[t].plans[k].skip /\ k \in agenda'
ChunkProgress == [][\A t \in Ticks : \A k \in StaticKeys : Requeues(t, k) => Variant(k)' < Variant(k)]_vars
\* A step that places a card in a member's ready cell does so only while the member is up.
PlaceOnlyUp == [][\A p \in Prims, m \in Members : (<<m, "ready">> \notin wk[p].pl /\ <<m, "ready">> \in wk'[p].pl) => status[m] = "up"]_vars
\* A member held by fleet down stays held until fleet up.
HeldSticky == [][\A m \in Members : (status[m] = "held" /\ status'[m] # "held") =>
                   \E v \in VerbProcs : vk[v].pc = "planned" /\ vk[v].verb = "fleetup" /\ vk[v].arg = m /\ vk'[v].pc = "idle"]_vars
\* Every change of a card's column is a row of Moves.
Moves == {<<"none", "waiting">>, <<"none", "ready">>, <<"waiting", "ready">>, <<"ready", "waiting">>,
          <<"ready", "working">>, <<"working", "ready">>, <<"working", "review">>, <<"review", "working">>,
          <<"review", "ready">>, <<"review", "merging">>, <<"merging", "landed">>, <<"waiting", "landed">>}
         \cup {<<x, "removed">> : x \in OpenCols}
Lifecycle == [][\A c \in Cards : col'[c] # col[c] => <<col[c], col'[c]>> \in Moves]_vars
\* V6: while dropping[s] is set, no step other than the op's parts moves a
\* card of s; when a drop records its last part, no card of s is open.
\* The freeze is keyed on an op in flight (a part receipt for s, with no
\* final part and no abort), not on the mark, so that a drop without its
\* mark (W13) is seen.
InFlight(s) == {o \in Ops : (\E r \in receipts : r.op = o /\ r.k > 0 /\ r.s = s)
                            /\ ~\E r \in receipts : r.op = o /\ (r.final \/ r.k = 0)}
DropComplete ==
  [][/\ \A c \in Cards : (InFlight(S(c)) # {} /\ col'[c] # col[c]) =>
                            \E r \in receipts' \ receipts : r.op \in InFlight(S(c)) /\ r.k > 0
     /\ \A r \in receipts' \ receipts : (r.k > 0 /\ r.final) => \A c \in Cards : S(c) = r.s => col'[c] \notin OpenCols]_vars
\* R2 and R11: a take that ends without a finish is counted. A work card that
\* leaves working other than by a finish or a drop raises its primary's
\* redeals by one, up to the bound (W7b: not raised).
RedealsCounted ==
  [][\A p \in Prims : (InWorking(wk[p]) /\ ~InWorking(wk'[p]) /\ col'[p] \notin {"review", "removed"})
                      => fld'[p].redeals = Min(fld[p].redeals + 1, MaxRedeals)]_vars
\* H7, as the split and stopclose leave it: R17 raises only on a dry plan
\* that still changes a card when its step applies (the claim errata 3
\* makes for the split; without stopinputs its guard is the clock fields
\* only, H14; with it, a change of the fleet still passes the guard, H17).
StoppedRaiseFresh ==
  [][(~Present("stopped", SprintS, "-") /\ Present("stopped", SprintS, "-")') => DryDueAll]_vars
\* E4, T1: every tick write carries the lease generation current when it
\* applies (a step of a loop past its read that changes what the store holds).
storev == <<commitv, agenda, parked, cur, clk, raises>>
LeaseSafe == [][\A t \in Ticks : (tk[t].pc \in {"read", "apply"} /\ tk'[t] # tk[t] /\ crashes' = crashes /\ storev' # storev)
                                 => (lease.owner = t /\ lease.gen = tk[t].gen)]_vars

-----------------------------------------------------------------------------
\* Liveness, with fairness on the tick and on time only.

\* Section 5's Progress, with Named as written.
ProgressLiteral == \A c \in Prims : IsOpen(c) ~> (Ended(c) \/ Named(c))
\* With (d): a card that waits on a named card is named through it.
Progress == \A c \in Prims : IsOpen(c) ~> (Ended(c) \/ NamedD(c, Cardinality(Cards)))
\* H6's decision: Progress is claimed while RUNNING or while a move is due;
\* a machine left STOPPED with nothing due is the owner's choice.
StoppedIdle == ~running /\ ~DryDueAll
ProgressScoped == \A c \in Prims : IsOpen(c) ~> (Ended(c) \/ NamedD(c, Cardinality(Cards)) \/ StoppedIdle)
\* The design's liveness rests on every hold ending: a STOPPED hold passes in
\* wall time (W12: in running time, so never while STOPPED).
HoldEnds == (~running /\ clk.hold > 0) ~> (clk.hold = 0 \/ running)
\* H7 with its repair: a STOPPED judgment left open with no move due is
\* closed by a later look (or the machine starts, or a move is due again).
StoppedStaleCloses == (IsOpenJ("stopped", SprintS, "-") /\ StoppedIdle)
                        ~> (~IsOpenJ("stopped", SprintS, "-") \/ ~StoppedIdle)

=============================================================================
