----------------------------- MODULE ReaderTiers -----------------------------
\* A reader's tiers (docs/SPEC-SPRINT.md section 6, a reader row carries the
\* tiers it reads; internal/sprint reader_tiers.go readerReadsCard, readers.go
\* freeReaders, enoughReadersUp, returnedReadsAt, levelReads, sweepReads;
\* steps_review.go Ask; steps_tick.go TickAsk): the owner, 2026-10-05, of the
\* reading bottleneck, of a friend whose lane runs a flash model: could it "do
\* more reading?", it is "very fast and cheap". That lane runs a flash model,
\* and the read floor says a read runs on a route of its card's tier, never
\* below. Before rows carried tiers the ask asked that flash reader five pro reads
\* and a heavy one in ten seconds; each came back returned, a returned read with
\* no other free reader was re-asked of it in place, and six reads were retired
\* with their re-asks spent in thirty seconds.
\*
\* THE RULE. A reader counts for a card only when it reads the card's tier: the
\* ask asks it nothing else, a returned read is asked again in place only of a
\* reader of the tier, the level moves a read only to a reader of the tier, and
\* a card with fewer readers of its tier up than it needs is the few-readers
\* judgment, never asked. A reader's tiers change at any time (reader set
\* --tiers), so a read asked before the change may stand on a reader outside
\* its tier; it is never asked of that reader again.
\*
\* THE STATE.
\*   up     each reader's presence (the environment: beats and holds)
\*   tiers  each reader's tiers (the coordinator: reader add/set --tiers)
\*   rd     each card's read by reader at its one attempt: none, asked,
\*          returned (handed back, no verdict), ok, or retired (taken back,
\*          levelled away, or a return asked of another)
\*   st     each card: review or accepted
\*   few    the few-readers judgment is open (raised by the ask, closed when
\*          every card in review has readers enough)
\*   bad    a ghost: TRUE once a read was asked (placed, re-asked in place or
\*          levelled) of a reader that did not read the card's tier then
\*
\* BROKEN names the reversed witnesses: "anytier" counts every reader up for
\* every card (the ask before 2026-10-05); "inplace" re-asks a return of its
\* own reader whatever its tiers; "level" levels a read to any reader up.
EXTENDS Integers, FiniteSets

CONSTANTS Readers, Tiers, Cards, Tier, Need, InitTiers, Broken

ASSUME \A c \in Cards : Tier[c] \in Tiers
ASSUME \A r \in Readers : InitTiers[r] \subseteq Tiers /\ InitTiers[r] # {}

VARIABLES up, tiers, rd, st, few, bad
vars == <<up, tiers, rd, st, few, bad>>

\* readerReadsCard: the reader reads the card's tier ("anytier": every reader).
ReadsTier(r, c) == Broken = "anytier" \/ Tier[c] \in tiers[r]
\* upReadersFor: the readers up that read the card's tier.
UpFor(c) == {r \in Readers : up[r] /\ ReadsTier(r, c)}
\* enoughReadersUp.
Enough(c) == Cardinality(UpFor(c)) >= Need[c]
Has(c, v) == {r \in Readers : rd[c][r] = v}
\* freeReaders: up, of the tier, no read card at the attempt.
Free(c) == {r \in UpFor(c) : rd[c][r] = "none"}
Outstanding(c) == Has(c, "asked") \cup Has(c, "returned")
\* ReadsWanted, one at a time: one while none is outstanding and oks fall short.
Wants(c) == st[c] = "review" /\ Outstanding(c) = {} /\ Cardinality(Has(c, "ok")) < Need[c]

\* bad' for a read asked of r for c now: true when r does not read c's tier.
Mark(r, c) == bad' = (bad \/ Tier[c] \notin tiers[r])

Init ==
  /\ up = [r \in Readers |-> TRUE]
  /\ tiers = InitTiers
  /\ rd = [c \in Cards |-> [r \in Readers |-> "none"]]
  /\ st = [c \in Cards |-> "review"]
  /\ few = FALSE
  /\ bad = FALSE

Up(r) == /\ ~up[r] /\ up' = [up EXCEPT ![r] = TRUE] /\ UNCHANGED <<tiers, rd, st, few, bad>>
Down(r) == /\ up[r] /\ up' = [up EXCEPT ![r] = FALSE] /\ UNCHANGED <<tiers, rd, st, few, bad>>

\* reader set --tiers: any tiers but none.
SetTiers(r) ==
  /\ \E T \in SUBSET Tiers \ {{}} : T # tiers[r] /\ tiers' = [tiers EXCEPT ![r] = T]
  /\ UNCHANGED <<up, rd, st, few, bad>>

\* Ask: a card that wants a read and has readers enough of its tier asks one,
\* of a free reader of its tier, when one is free (else it waits).
Ask(c) ==
  /\ Wants(c) /\ Enough(c)
  /\ \E r \in Free(c) :
       /\ rd' = [rd EXCEPT ![c][r] = "asked"]
       /\ Mark(r, c)
  /\ UNCHANGED <<up, tiers, st, few>>

\* The few-readers judgment: one card that wants a read lacks readers of its
\* tier up. Closed when none does.
Judge ==
  /\ ~few
  /\ \E c \in Cards : (Wants(c) \/ Has(c, "returned") # {}) /\ ~Enough(c)
  /\ few' = TRUE
  /\ UNCHANGED <<up, tiers, rd, st, bad>>
Close ==
  /\ few
  /\ \A c \in Cards : (Wants(c) \/ Has(c, "returned") # {}) => Enough(c)
  /\ few' = FALSE
  /\ UNCHANGED <<up, tiers, rd, st, bad>>

Return(c, r) ==
  /\ rd[c][r] = "asked"
  /\ rd' = [rd EXCEPT ![c][r] = "returned"]
  /\ UNCHANGED <<up, tiers, st, few, bad>>

Read(c, r) ==
  /\ rd[c][r] = "asked"
  /\ rd' = [rd EXCEPT ![c][r] = "ok"]
  /\ UNCHANGED <<up, tiers, st, few, bad>>

\* A return is not a read: asked of a free reader of the tier, its card
\* retired; with none free, of its own reader again in place, only when that
\* reader reads the tier ("inplace": whatever its tiers).
Reask(c, r) ==
  /\ rd[c][r] = "returned" /\ up[r] /\ Enough(c)
  /\ \/ \E o \in Free(c) :
          /\ rd' = [rd EXCEPT ![c] = [@ EXCEPT ![r] = "retired", ![o] = "asked"]]
          /\ Mark(o, c)
     \/ /\ Free(c) = {}
        /\ Broken = "inplace" \/ ReadsTier(r, c)
        /\ rd' = [rd EXCEPT ![c][r] = "asked"]
        /\ Mark(r, c)
  /\ UNCHANGED <<up, tiers, st, few>>

\* The level and the sweep: a read asked, not begun, moves to a reader up of
\* the tier with no card at the attempt ("level": any reader up).
Level(c, r) ==
  /\ rd[c][r] = "asked"
  /\ \E o \in Readers \ {r} :
       /\ up[o] /\ rd[c][o] = "none"
       /\ Broken = "level" \/ ReadsTier(o, c)
       /\ rd' = [rd EXCEPT ![c] = [@ EXCEPT ![r] = "retired", ![o] = "asked"]]
       /\ Mark(o, c)
  /\ UNCHANGED <<up, tiers, st, few>>

Accept(c) ==
  /\ st[c] = "review"
  /\ Cardinality(Has(c, "ok")) >= Need[c]
  /\ st' = [st EXCEPT ![c] = "accepted"]
  /\ UNCHANGED <<up, tiers, rd, few, bad>>

Next ==
  \/ \E r \in Readers : Up(r) \/ Down(r) \/ SetTiers(r)
  \/ Judge \/ Close
  \/ \E c \in Cards : Ask(c) \/ Accept(c)
       \/ \E r \in Readers : Return(c, r) \/ Read(c, r) \/ Reask(c, r) \/ Level(c, r)

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ up \in [Readers -> BOOLEAN]
  /\ tiers \in [Readers -> SUBSET Tiers]
  /\ rd \in [Cards -> [Readers -> {"none", "asked", "returned", "ok", "retired"}]]
  /\ st \in [Cards -> {"review", "accepted"}]
  /\ few \in BOOLEAN /\ bad \in BOOLEAN

\* THE INVARIANT: no read is ever asked of a reader outside the card's tier.
AskedWithinTier == ~bad

\* One at a time: a card has at most one read outstanding.
OneAtATime == \A c \in Cards : Cardinality(Outstanding(c)) <= 1

\* An accepted card was read ok by as many different readers as it needs.
AcceptedOnEnough == \A c \in Cards : st[c] = "accepted" => Cardinality(Has(c, "ok")) >= Need[c]
=============================================================================
