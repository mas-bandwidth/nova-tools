---------------------------- MODULE SprintTables ----------------------------
\* nova-sprint's sprint table (docs/SPEC-SPRINT.md, with the author's
\* decisions D1 to D8, F1 to F5 and G1 to G6 taken on it): the four tables on
\* nova-table (work, readers, merge, fleet), the mechanical moves between them,
\* the judgment notifications, the verbs, and the steps that touch more than
\* one table.
\*
\* The state the design owns:
\*   work[s][cell]     the work table: a row per stream, cells waiting ready
\*                     working review merging landed; members are primaries
\*   fleet[m][cell]    the fleet table: a row per member, cells ready working
\*                     done; members are work cards <<"w", p, attempt>>
\*   readers[r][cell]  the readers table: a row per reader, cells asked reading
\*                     ok broken; members are read cards <<"r", p, attempt, r>>
\*   merge[s][cell]    the merge table: cells queued merged stuck
\*   sstate[s]         the stream's merge state: waiting merging stopped landed
\*   mstatus[m]        a fleet member's status: up or down
\*   score, attempt, head, pair   the primary's fields; pair is the two
\*                     readers kept on the primary (D2); a read card's head is
\*                     its attempt
\*   gen[c]            a work card's assignment generation (D3)
\*   held[m]           what the worker on member m holds: <<card, generation>>
\*                     pairs it took; a worker keeps running after its member
\*                     is reported down
\*   fin[c]            the generation whose finish was accepted; 0 before
\*   result[c]         a finished card's outcome; "none" before
\*   cause[p], need[p] a stuck card's cause and, for a cross-stream need, the
\*                     card it needs (D6); need is kept until the card lands,
\*                     returns or is dropped
\*   added, dropped    primaries admitted; primaries that left the table
\*   made, gone, twice every card cut; cards off their table with the record
\*                     kept (withdrawn work cards, retired read cards); TRUE once
\*                     an identity is cut a second time
\*   open              the open judgment notifications: the obligations (D7);
\*                     there is no cursor, reading the inbox resolves nothing
\*   op, crashed       the sprint-wide fence (D1): the one pending operation
\*                     record, and whether the process writing it was cut
\*   bad               how many outside failures have happened (a work card
\*                     failed, a read broken, a merge stopped, a member down, a
\*                     cut, a red CI); the instance bounds it by MaxBad
\*   returnsN, ranks   bounds of the coordinator's free choices
\*   machine, stops    the machine's state, running or stopped, and how many
\*                     times the coordinator stopped it (a bound)
\*   redstop           the streams stopped by a red branch or a rejected batch
\*   epoch, rheld      the epoch (clear advances it); what each reader holds:
\*                     <<read card, epoch>> pairs it began; held carries the
\*                     epoch too
\*   stale             TRUE once a report from an old epoch was accepted
\*   acks              how many acks the coordinator gave (a bound)
\*   waived[p]         the dropped needs of p the coordinator waived
\*   released          the sentinels the coordinator released
\*
\* D1, the fence. A step over more than one table records the operation, then
\* writes every table but the work table (Phase 1, one step here), then the
\* work table (Continue: the same process; Repair: after Crash, the cut). The
\* notifications of a step open with its last write. Every verb is disabled
\* while an operation is pending: the verb repairs first or refuses. Repair
\* applies a move only where its expectation (the primary in the cell it left)
\* still holds, reports each move it skipped as a judgment, and releases (F5).
\* Invariants 1, 6, 7, 8, 9 hold at every state; 2, 3, 4, 5 hold whenever no
\* operation is pending.
\*
\* FreeCoordinator: TRUE, the coordinator may rework, drop or return any card
\* at any time; FALSE, only a card an open judgment names (a bound that keeps
\* the three-primary instance checkable). The same bound applies to
\* ask --another, which the design leaves free (F1).
\*
\* Broken is a reversed witness: a deliberately wrong design, each caught by
\* one property. "none" is the design.
\*   "onereader"        accept with one ok reader                    (6)
\*   "samereader"       accept counts every ok read card ever made for the
\*                      primary: one reader counted twice across attempts (6)
\*   "outoforder"       the merge step lands any queued cards    (work order)
\*   "finishnomove"     finish moves the card to done, not the primary   (2)
\*   "stopsilent"       a stream stops without a notification            (9)
\*   "norepair"         nothing repairs a cut step              (CutRepaired)
\*   "reworktail"       rework puts the primary at the tail              (7)
\*   "redealcopies"     a member going down deals its cards without
\*                      taking them off it                               (1)
\*   "dropkeepsreads"   drop leaves the primary's outstanding read cards (3)
\*   "returnkeeps"      return leaves the primary in merge queued/stuck  (4)
\*   "landskipswork"    a green batch moves merge, never the work table  (5)
\*   "withdrawlost"     withdrawn work cards leave with no record        (8)
\*   "recut"            start cuts <p>.w<attempt> again after a withdrawal
\*                      instead of re-dealing it (G1)                    (8)
\*   "reworkready"      rework cuts the next card and leaves the primary in
\*                      ready, as section 6 of the spec text reads   (2, D2)
\*   "nofence"          single-table verbs act on the partial state of a
\*                      pending operation                                (D1)
\*   "stalefinish"      a finish from a generation no longer live is
\*                      accepted after a redeal         (FinishIsLive, D3, F2)
\*   "resumeunresolved" resume while a stuck card's cross-stream need has not
\*                      landed                          (LandsAfterNeed, D6)
\*   "cardcloses"       an answer to one card closes the stopped stream's
\*                      judgment                                     (9, D5)
\*   "acceptkeepsreads" accept leaves the accepted primary's asked and reading
\*                      read cards                                   (3, F1)
\*   "ackstopped"       ack closes a stopped stream's judgment       (9, F3)
\*   "silentexhausted"  no judgment when a primary's reads run out
\*                                           (ReadsExhaustedIsNotified, G3)
\*   "admitcycle"       add admits a primary whose needs make a cycle
\*                                                         (NoNeedCycle)
\*   "sentinelself"     a reached sentinel lands by itself
\*                                              (SentinelLandsByRelease)
\*   "pastsentinel"     a primary resolves past a reached, unreleased
\*                      sentinel and is dealt                (SentinelHolds)
\*   "oldepoch"         a report from a worker or reader holding an old
\*                      epoch is accepted after clear      (NoOldEpochWrite)
\*   The Go as audited, each caught by OwnersRule:
\*   "noacceptnote"     two readers ok is a happened note only (gap 2)
\*   "noreturnnote"     return opens no judgment (gap 3)
\*   "nofleetnote"      the tick never writes the no-member judgment (gap 6)
\*   "ackstrands"       ack writes nothing when it strands its card (gap 4)
\*   "auditack"         ack writes "stranded" only for a primary in review,
\*                      as the audit proposes
\*   "addlanded"        add into a landed stream leaves it landed
\*                                                   (StreamStateIsTrue)

EXTENDS Naturals, FiniteSets

CONSTANTS Streams, Primaries, Members, Readers,
          StreamOf,   \* [Primaries -> Streams]
          Needs,      \* [Primaries -> SUBSET Primaries]
          Score0,     \* [Primaries -> Nat], distinct
          Admitted0,  \* the primaries admitted before the first step
          Sentinels,  \* the primaries of kind sentinel
          MaxAttempt, MaxBad, MaxReturns, MaxRanks, MaxStops, MaxEpoch, MaxAcks,
          FreeCoordinator,
          Broken

None == "none"
Br(x) == Broken = x

WorkCells == {"waiting", "ready", "working", "review", "merging", "landed"}
FleetCells == {"ready", "working", "done"}
ReadCells == {"asked", "reading", "ok", "broken"}
MergeCells == {"queued", "merged", "stuck"}
Attempts == 1..MaxAttempt
WC(p, a) == <<"w", p, a>>
RC(p, a, r) == <<"r", p, a, r>>
WorkCards == {WC(p, a) : p \in Primaries, a \in Attempts}
ReadCards == {RC(p, a, r) : p \in Primaries, a \in Attempts, r \in Readers}
Cards == WorkCards \cup ReadCards
CardCauses == {"conflict", "needs"}
CardNoteTypes == {"failed", "broken", "reads", "blocked", "ci", "skipped", "reached",
                  "accept", "returned", "stranded"}
\* A judgment notification about a card, and a stream's stopped judgment.
Note(t, p) == [t |-> t, p |-> p, s |-> StreamOf[p]]
StopNote(s) == [t |-> "stopped", p |-> None, s |-> s]
\* The fleet's judgment: no member is up (audit gap 6).
FleetNote == [t |-> "nomember", p |-> None, s |-> None]
Notes == {Note(t, p) : t \in CardNoteTypes, p \in Primaries}
         \cup {StopNote(s) : s \in Streams} \cup {FleetNote}

VARIABLES added, dropped, work, fleet, readers, merge, sstate, mstatus,
          score, attempt, head, pair, gen, held, fin, result,
          made, gone, twice, cause, need, open, op, crashed,
          bad, returnsN, ranks, waived, released, machine, stops, redstop,
          epoch, rheld, stale, acks

vars == <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
          score, attempt, head, pair, gen, held, fin, result,
          made, gone, twice, cause, need, open, op, crashed,
          bad, returnsN, ranks, waived, released, machine, stops, redstop,
          epoch, rheld, stale, acks>>

\* ------------------------------------------------------------------ views

InWork(p, c) == p \in work[StreamOf[p]][c]
Cell(p) == IF \E c \in WorkCells : InWork(p, c)
           THEN CHOOSE c \in WorkCells : InWork(p, c) ELSE None
Landed == {p \in Primaries : InWork(p, "landed")}
\* NEEDS: every need has landed, or was dropped and waived by the coordinator.
NeedsMet(p) == \A q \in Needs[p] : InWork(q, "landed") \/ q \in waived[p]
\* SENTINELS: a sentinel whose needs have all landed (or been waived) is
\* reached; the judgments a step opens for the sentinels it leaves reached,
\* given the landed set and the waived needs after it.
ReachedIn(g, L, W) == g \in Sentinels /\ InWork(g, "waiting")
                      /\ \A q \in Needs[g] : q \in L \/ q \in W[g]
Reached(g) == ReachedIn(g, Landed, waived)
ReachNotes(L, W) == {Note("reached", g) : g \in {x \in Sentinels : ReachedIn(x, L, W)}}
RECURSIVE Reach(_, _)
Reach(X, n) == IF n = 0 THEN X ELSE Reach(X \cup UNION {Needs[x] : x \in X}, n - 1)
\* p needs itself through the needs of the primaries in S.
InCycle(p, S) == p \in Reach({q \in Needs[p] : q \in S}, Cardinality(Primaries))
Placedp(p) == p \in added /\ p \notin dropped
RL(m) == Cardinality(fleet[m]["ready"])
Up == {m \in Members : mstatus[m] = "up"}
ShortestIn(m, S) == m \in S /\ \A x \in S : RL(m) <= RL(x)
AskedLen(r) == Cardinality(readers[r]["asked"])
LiveReads == UNION {readers[r][c] : r \in Readers, c \in ReadCells}
Outstanding == UNION {readers[r][c] : r \in Readers, c \in {"asked", "reading"}}
OutOf(p) == {c \in Outstanding : c[2] = p}
Unfinished == UNION {fleet[m][c] : m \in Members, c \in {"ready", "working"}}
InFleet(c) == \E m \in Members, x \in FleetCells : c \in fleet[m][x]
InReaders(c) == \E r \in Readers, x \in ReadCells : c \in readers[r][x]
Placed(c) == IF c[1] = "w" THEN InFleet(c) ELSE InReaders(c)
OkReaders(p) == {r \in Readers : RC(p, head[p], r) \in readers[r]["ok"]}
Acceptable(p) ==
  CASE Br("onereader")  -> OkReaders(p) # {}
    [] Br("samereader") -> Cardinality({c \in made : c[1] = "r" /\ c[2] = p
                                          /\ result[c] = "ok"}) >= 2
    [] OTHER            -> Cardinality(OkReaders(p)) >= 2
MinScore == CHOOSE n \in {score[p] : p \in Primaries} :
              \A q \in Primaries : n <= score[q]
MaxScore == CHOOSE n \in {score[p] : p \in Primaries} :
              \A q \in Primaries : n >= score[q]
Bump(S) == [c \in WorkCards |-> IF c \in S THEN gen[c] + 1 ELSE gen[c]]
Failed(p) == result[WC(p, attempt[p])] = "failed"

\* G3: a primary asked at its attempt, in review, with no read outstanding,
\* without two different readers ok at its head, and no open judgment on it.
AskedNow(p) == \E r \in Readers : RC(p, attempt[p], r) \in made
Exhausted(p, outs, oks, opn) ==
  /\ InWork(p, "review") /\ AskedNow(p)
  /\ outs = {} /\ Cardinality(oks) < 2
  /\ ~\E n \in opn : n.p = p
ExhaustNote(p, outs, oks, opn) ==
  IF ~Br("silentexhausted") /\ Exhausted(p, outs, oks, opn)
  THEN {Note("reads", p)} ELSE {}

\* G4: the stream's merge state after its queued and stuck cells change. A
\* stopped stream stays stopped until resume (D5); landed when every admitted
\* primary of the stream has landed or left.
AllDone(s, mergedNow, leaving) ==
  \A p \in added : StreamOf[p] = s =>
    p \in mergedNow \/ p \in dropped \cup leaving \/ (p \in Sentinels /\ InWork(p, "landed"))
StreamAfter(s, q, st, was, mergedNow, leaving) ==
  IF was = "stopped" THEN "stopped"
  ELSE IF q # {} THEN "merging"
  ELSE IF AllDone(s, mergedNow, leaving) THEN "landed" ELSE "waiting"

\* THE OWNER'S RULE: every primary not landed and not off the table is held
\* by an outside actor, or has a move due for the tick, or has an open
\* judgment, or waits on something that itself satisfies this. A need not yet
\* admitted is held by the coordinator's add.
HeldOutside(p) ==
  \/ /\ InWork(p, "working")
     /\ \E m \in Members, c \in WorkCards :
          /\ c[2] = p
          /\ \/ m \in Up /\ c \in fleet[m]["ready"]
             \/ c \in fleet[m]["working"] /\ <<c, gen[c], epoch>> \in held[m]
  \/ InWork(p, "review") /\ OutOf(p) # {}
  \/ /\ InWork(p, "merging") /\ sstate[StreamOf[p]] = "merging"
     /\ p \in merge[StreamOf[p]]["queued"]
TickResumable(s) ==
  /\ sstate[s] = "stopped" /\ s \notin redstop /\ merge[s]["stuck"] # {}
  /\ \A x \in merge[s]["stuck"] : cause[x] = "needs" /\ InWork(need[x], "landed")
TickMove(p) ==
  \/ InWork(p, "waiting") /\ p \notin Sentinels /\ NeedsMet(p)
  \/ InWork(p, "ready") /\ Up # {}
  \/ InWork(p, "ready") /\ FleetNote \notin open /\ ~Br("nofleetnote")
  \/ InWork(p, "review") /\ ~Failed(p) /\ \A c \in LiveReads : c[2] # p
  \/ InWork(p, "merging") /\ TickResumable(StreamOf[p])
JudgedIn(p, opn) ==
  \/ \E n \in opn : n.p = p
  \/ /\ InWork(p, "merging") /\ StopNote(StreamOf[p]) \in opn
     /\ p \in merge[StreamOf[p]]["queued"] \cup merge[StreamOf[p]]["stuck"]
  \/ InWork(p, "ready") /\ FleetNote \in opn
RECURSIVE RuleIn(_, _, _)
RuleIn(p, opn, n) ==
  \/ HeldOutside(p) \/ TickMove(p) \/ JudgedIn(p, opn)
  \/ /\ n > 0 /\ InWork(p, "waiting")
     /\ LET unmet == {q \in Needs[p] : ~InWork(q, "landed") /\ q \notin waived[p]}
        IN /\ unmet # {}
           /\ \A q \in unmet : q \notin added \/ (Placedp(q) /\ RuleIn(q, opn, n - 1))

\* An operation record: the moves of primaries in the work table (to "off"
\* for drop), the primaries whose attempt increments, new heads, the
\* notifications opened, the primaries whose card notifications close.
Op(k, mv, at, hd, nt, cl) ==
  [kind |-> k, moves |-> mv, att |-> at, hd |-> hd, notes |-> nt, closes |-> cl]
NoOp == Op("none", {}, {}, {}, {}, {})

\* D1: every verb reads the fence; two-table verbs always, single-table verbs
\* too unless the fence is broken.
Free == op = NoOp
\* THE MACHINE: its tick performs the mechanical moves only while running.
Running == machine = "running"
Fenced == op = NoOp \/ Br("nofence")

Begin(o) == op' = o /\ crashed' = FALSE

\* A card the coordinator may rework, drop or return. Under the bound a
\* "ready to accept" judgment is answered by accept only.
Judged(p) ==
  \/ FreeCoordinator
  \/ \E n \in open : n.p = p /\ n.t # "accept"
  \/ \E n \in open : n.t = "stopped" /\ n.s = StreamOf[p]
                     /\ p \in merge[n.s]["stuck"] \cup merge[n.s]["queued"]

\* The outside failure budget of the instance.
Spend(isbad) == IF isbad THEN bad < MaxBad /\ bad' = bad + 1 ELSE UNCHANGED bad

\* Which open notifications an operation's last write closes: the card
\* judgments of the primaries it acted on; never a stream's stopped judgment
\* (D5) unless the witness says so.
Closes(n, o) ==
  \/ n.t # "stopped" /\ n.p \in o.closes
  \/ Br("cardcloses") /\ n.t = "stopped" /\ \E p \in o.closes : StreamOf[p] = n.s

\* The work-table write of an operation, from its record; a move applies only
\* where its expectation holds, and each move skipped is reported (D1, F5).
ApplyWork(o) ==
  LET ok == {x \in o.moves : InWork(x[1], x[2])}
      moved == {x[1] : x \in ok}
      skipped == {x[1] : x \in o.moves \ ok}
  IN
  /\ work' = [s \in Streams |-> [c \in WorkCells |->
               (work[s][c] \ {x[1] : x \in {y \in ok : y[2] = c}})
               \cup {x[1] : x \in {y \in ok : y[3] = c /\ StreamOf[y[1]] = s}}]]
  /\ dropped' = dropped \cup {x[1] : x \in {y \in ok : y[3] = "off"}}
  /\ attempt' = [p \in Primaries |-> IF p \in o.att \cap moved THEN attempt[p] + 1
                                     ELSE attempt[p]]
  /\ head' = [p \in Primaries |-> IF p \in moved /\ \E h \in Attempts : <<p, h>> \in o.hd
                                  THEN CHOOSE h \in Attempts : <<p, h>> \in o.hd
                                  ELSE head[p]]
  /\ open' = (open \ {n \in open : Closes(n, o)}) \cup o.notes
             \cup {Note("skipped", p) : p \in skipped}
             \cup ReachNotes(Landed \cup {x[1] : x \in {y \in ok : y[3] = "landed"}}, waived)

\* ------------------------------------------------------------------ init

Init ==
  /\ added = Admitted0 /\ dropped = {}
  /\ work = [s \in Streams |-> [c \in WorkCells |->
              {p \in Admitted0 : StreamOf[p] = s /\
                 c = IF Needs[p] = {} THEN "ready" ELSE "waiting"}]]
  /\ fleet = [m \in Members |-> [c \in FleetCells |-> {}]]
  /\ readers = [r \in Readers |-> [c \in ReadCells |-> {}]]
  /\ merge = [s \in Streams |-> [c \in MergeCells |-> {}]]
  /\ sstate = [s \in Streams |-> "waiting"]
  /\ mstatus = [m \in Members |-> "up"]
  /\ score = Score0
  /\ attempt = [p \in Primaries |-> 1]
  /\ head = [p \in Primaries |-> 0]
  /\ pair = [p \in Primaries |-> {}]
  /\ gen = [c \in WorkCards |-> 1]
  /\ held = [m \in Members |-> {}]
  /\ fin = [c \in WorkCards |-> 0]
  /\ result = [c \in Cards |-> None]
  /\ made = {} /\ gone = {} /\ twice = FALSE
  /\ cause = [p \in Primaries |-> None]
  /\ need = [p \in Primaries |-> None]
  /\ open = {Note("reached", g) : g \in {x \in Sentinels \cap Admitted0 : Needs[x] = {}}}
  /\ op = NoOp /\ crashed = FALSE
  /\ bad = 0 /\ returnsN = [p \in Primaries |-> 0] /\ ranks = 0
  /\ waived = [p \in Primaries |-> {}]
  /\ released = {}
  /\ machine = "running" /\ stops = 0 /\ redstop = {}
  /\ epoch = 0 /\ rheld = [r \in Readers |-> {}] /\ stale = FALSE
  /\ acks = 0

\* ------------------------------------------------------------------ verbs

\* add: admit a primary into its stream, waiting if it needs something; a
\* landed stream is waiting again (as built; see "addlanded").
Add(p) ==
  /\ Fenced /\ p \in Primaries \ added
  /\ Br("admitcycle") \/ ~InCycle(p, added \cup {p})
  /\ added' = added \cup {p}
  /\ LET to == IF NeedsMet(p) /\ p \notin Sentinels THEN "ready" ELSE "waiting"
     IN work' = [work EXCEPT ![StreamOf[p]][to] = @ \cup {p}]
  /\ open' = open \cup (IF Needs[p] \cap dropped # {} THEN {Note("blocked", p)} ELSE {})
                   \cup (IF p \in Sentinels /\ NeedsMet(p) THEN {Note("reached", p)} ELSE {})
  /\ sstate' = [sstate EXCEPT ![StreamOf[p]] =
                  IF @ = "landed" /\ ~Br("addlanded") THEN "waiting" ELSE @]
  /\ UNCHANGED <<dropped, fleet, readers, merge, mstatus, score, attempt,
                 head, pair, gen, held, fin, result, made, gone, twice, cause,
                 need, op, crashed, bad, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* resolve (a tick duty): waiting -> ready where needs have landed.
Resolve(p) ==
  /\ Running /\ Fenced /\ InWork(p, "waiting") /\ p \notin Sentinels
  /\ NeedsMet(p) \/ (Br("pastsentinel") /\ \A q \in Needs[p] :
                       InWork(q, "landed") \/ q \in waived[p] \/ Reached(q))
  /\ work' = [work EXCEPT ![StreamOf[p]]["waiting"] = @ \ {p},
                          ![StreamOf[p]]["ready"] = @ \cup {p}]
  /\ UNCHANGED <<added, dropped, fleet, readers, merge, sstate, mstatus, score,
                 attempt, head, pair, gen, held, fin, result, made, gone, twice,
                 cause, need, open, op, crashed, bad, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* waive: the coordinator waives a dropped need of a waiting primary; the
\* blocked judgment closes when no dropped need is left unwaived.
Waive(p, q) ==
  /\ Fenced /\ InWork(p, "waiting") /\ q \in Needs[p] \cap dropped /\ q \notin waived[p]
  /\ waived' = [waived EXCEPT ![p] = @ \cup {q}]
  /\ open' = (IF Needs[p] \cap dropped \subseteq waived[p] \cup {q}
              THEN open \ {Note("blocked", p)} ELSE open)
             \cup ReachNotes(Landed, [waived EXCEPT ![p] = @ \cup {q}])
  /\ UNCHANGED <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
                 score, attempt, head, pair, gen, held, fin, result, made, gone,
                 twice, cause, need, op, crashed, bad, returnsN, ranks, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* release: the coordinator lands a reached sentinel (waiting -> landed), the
\* only way a sentinel lands; its reached judgment closes, the release is
\* notified, and the waiting primaries it held whose needs are now met become
\* ready in the same step.
Release(g) ==
  /\ Fenced /\ Reached(g)
  /\ LET L == Landed \cup {g}
         freed == {q \in Primaries : InWork(q, "waiting") /\ q \notin Sentinels /\ q # g
                     /\ g \in Needs[q] /\ \A x \in Needs[q] : x \in L \/ x \in waived[q]}
     IN /\ work' = [s \in Streams |-> [c \in WorkCells |->
                     CASE c = "waiting" -> work[s][c] \ ({g} \cup freed)
                       [] c = "ready"   -> work[s][c] \cup {q \in freed : StreamOf[q] = s}
                       [] c = "landed"  -> work[s][c] \cup (IF StreamOf[g] = s THEN {g} ELSE {})
                       [] OTHER         -> work[s][c]]]
        /\ open' = (open \ {Note("reached", g)}) \cup ReachNotes(L, waived)
  /\ released' = released \cup {g}
  /\ UNCHANGED <<added, dropped, fleet, readers, merge, sstate, mstatus, score,
                 attempt, head, pair, gen, held, fin, result, made, gone, twice,
                 cause, need, op, crashed, bad, returnsN, ranks, waived, machine,
                 stops, redstop, epoch, rheld, stale, acks>>

\* deal (a tick duty; there is no verb that deals): ready -> working; the work
\* card of the attempt is dealt to the up member with the shortest ready
\* queue: cut, or, if it was withdrawn, the same card dealt again at a new
\* generation (G1, D3). Fleet first, work last.
Deal(p) ==
  /\ Running /\ Free /\ InWork(p, "ready")
  /\ LET c == WC(p, attempt[p]) IN
     /\ \E m \in Up : ShortestIn(m, Up) /\ fleet' = [fleet EXCEPT ![m]["ready"] = @ \cup {c}]
     /\ IF c \in gone /\ ~Br("recut")
          THEN /\ gone' = gone \ {c} /\ gen' = Bump({c})
               /\ UNCHANGED <<made, twice>>
          ELSE /\ made' = made \cup {c} /\ twice' = (twice \/ c \in made)
               /\ UNCHANGED <<gone, gen>>
  /\ Begin(Op("deal", {<<p, "ready", "working">>}, {}, {}, {}, {}))
  /\ UNCHANGED <<added, dropped, work, readers, merge, sstate, mstatus, score,
                 attempt, head, pair, held, fin, result, cause, need, open, bad,
                 returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* take: the worker on an up member moves a work card ready -> working and
\* holds it at its generation (F2: take names the generation).
Take(m, c) ==
  /\ Fenced /\ mstatus[m] = "up" /\ c \in fleet[m]["ready"]
  /\ fleet' = [fleet EXCEPT ![m]["ready"] = @ \ {c}, ![m]["working"] = @ \cup {c}]
  /\ held' = [held EXCEPT ![m] = @ \cup {<<c, gen[c], epoch>>}]
  /\ UNCHANGED <<added, dropped, work, readers, merge, sstate, mstatus, score,
                 attempt, head, pair, gen, fin, result, made, gone, twice,
                 cause, need, open, op, crashed, bad, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* finish: a worker reports the card it holds, naming its generation (F2),
\* done ok or failed. Accepted only for the live generation in its member's
\* working cell (D3): the card to done, its primary to review at the head the
\* card produced; failed notifies for judgment; ok asks the readers kept on
\* the primary (G2). Fleet and readers first, work last.
Live(m, c, g) == c \in fleet[m]["working"] /\ gen[c] = g
\* CLEAR: a writer holding an old epoch is refused.
InEpoch(e) == e = epoch \/ Br("oldepoch")
Accepted(m, h) == InEpoch(h[3]) /\ (Live(m, h[1], h[2]) \/ (Br("stalefinish") /\ h[1] \in Unfinished))
Finish(m, h, v) ==
  LET c == h[1]
      g == h[2]
  IN
  /\ Free /\ h \in held[m] /\ Accepted(m, h)
  /\ Spend(v = "failed")
  /\ held' = [held EXCEPT ![m] = @ \ {h}]
  /\ stale' = (stale \/ h[3] # epoch)
  /\ fleet' = [x \in Members |-> [y \in FleetCells |->
                 IF x = m /\ y = "done" THEN fleet[x][y] \cup {c} ELSE fleet[x][y] \ {c}]]
  /\ result' = [result EXCEPT ![c] = v]
  /\ fin' = [fin EXCEPT ![c] = g]
  /\ LET p == c[2]
         again == IF v = "ok" /\ ~Br("finishnomove") THEN pair[p] ELSE {}
         rcs == {RC(p, c[3], r) : r \in again}
     IN /\ readers' = [r \in Readers |-> [x \in ReadCells |->
                         IF x = "asked" /\ r \in again
                         THEN readers[r][x] \cup {RC(p, c[3], r)} ELSE readers[r][x]]]
        /\ made' = made \cup rcs
        /\ twice' = (twice \/ rcs \cap made # {})
  /\ IF Br("finishnomove") THEN UNCHANGED <<op, crashed>>
     ELSE Begin(Op("finish", {<<c[2], "working", "review">>}, {}, {<<c[2], c[3]>>},
                   IF v = "failed" THEN {Note("failed", c[2])} ELSE {}, {}))
  /\ UNCHANGED <<added, dropped, work, merge, sstate, mstatus, score, attempt,
                 head, pair, gen, gone, cause, need, open, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, acks>>

\* A finish refused as stale, or from an old epoch: it changes nothing but
\* the worker's hold.
FinishRefused(m, h) ==
  /\ Free /\ h \in held[m] /\ ~Accepted(m, h)
  /\ held' = [held EXCEPT ![m] = @ \ {h}]
  /\ UNCHANGED <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
                 score, attempt, head, pair, gen, fin, result, made, gone, twice,
                 cause, need, open, op, crashed, bad, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* ask (a tick duty): a primary in review whose work did not fail (G2), with no read card
\* on the table, is dealt to two different readers: the two kept on it (D2),
\* or the shortest asked queues.
Ask(p) ==
  /\ Running /\ Fenced /\ InWork(p, "review") /\ ~Failed(p)
  /\ \A c \in LiveReads : c[2] # p
  /\ \E two \in SUBSET Readers :
       /\ Cardinality(two) = 2
       /\ IF pair[p] # {} THEN two = pair[p]
          ELSE \E r1 \in two :
                 /\ \A x \in Readers : AskedLen(r1) <= AskedLen(x)
                 /\ \A r2 \in two \ {r1} : \A x \in Readers \ {r1} : AskedLen(r2) <= AskedLen(x)
       /\ pair' = [pair EXCEPT ![p] = two]
       /\ readers' = [r \in Readers |-> [x \in ReadCells |->
                        IF x = "asked" /\ r \in two
                        THEN readers[r][x] \cup {RC(p, attempt[p], r)} ELSE readers[r][x]]]
       /\ made' = made \cup {RC(p, attempt[p], r) : r \in two}
       /\ twice' = (twice \/ \E r \in two : RC(p, attempt[p], r) \in made)
  /\ UNCHANGED <<added, dropped, work, fleet, merge, sstate, mstatus, score,
                 attempt, head, gen, held, fin, result, gone, cause, need, open,
                 op, crashed, bad, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* ask --another: the coordinator's judgment, free (F1): one more reader for
\* a primary in review already asked at its attempt (refused before the first
\* ask); under the judged-coordinator bound, only for a card a judgment
\* names. It answers a broken read or reads exhausted.
AskAnother(p, r) ==
  /\ Fenced /\ InWork(p, "review") /\ Judged(p) /\ AskedNow(p)
  /\ RC(p, attempt[p], r) \notin made
  /\ readers' = [readers EXCEPT ![r]["asked"] = @ \cup {RC(p, attempt[p], r)}]
  /\ made' = made \cup {RC(p, attempt[p], r)}
  /\ open' = open \ {Note("broken", p), Note("reads", p)}
  /\ UNCHANGED <<added, dropped, work, fleet, merge, sstate, mstatus, score,
                 attempt, head, pair, gen, held, fin, result, gone, twice, cause,
                 need, op, crashed, bad, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* A reader moves its own read card asked -> reading and holds it at the
\* epoch.
ReadStart(r, c) ==
  /\ Fenced /\ c \in readers[r]["asked"]
  /\ readers' = [readers EXCEPT ![r]["asked"] = @ \ {c}, ![r]["reading"] = @ \cup {c}]
  /\ rheld' = [rheld EXCEPT ![r] = @ \cup {<<c, epoch>>}]
  /\ UNCHANGED <<added, dropped, work, fleet, merge, sstate, mstatus, score,
                 attempt, head, pair, gen, held, fin, result, made, gone, twice,
                 cause, need, open, op, crashed, bad, returnsN, ranks, waived, released, machine, stops, redstop, epoch, stale, acks>>

\* read: a reader records ok or broken; a report against a retired card finds
\* no card and is refused. Broken notifies for judgment; the read that leaves
\* the reads exhausted notifies for judgment (G3).
Read(r, h, v) ==
  LET c == h[1] IN
  /\ Fenced /\ h \in rheld[r] /\ c \in readers[r]["reading"] /\ InEpoch(h[2])
  /\ Spend(v = "broken")
  /\ rheld' = [rheld EXCEPT ![r] = @ \ {h}]
  /\ stale' = (stale \/ h[2] # epoch)
  /\ readers' = [readers EXCEPT ![r]["reading"] = @ \ {c}, ![r][v] = @ \cup {c}]
  /\ result' = [result EXCEPT ![c] = v]
  /\ LET p == c[2]
         opn == open \cup (IF v = "broken" THEN {Note("broken", p)} ELSE {})
         oks == OkReaders(p) \cup (IF v = "ok" /\ c[3] = head[p] THEN {r} ELSE {})
     IN open' = opn \cup ExhaustNote(p, OutOf(p) \ {c}, oks, opn)
                    \cup (IF v = "ok" /\ Cardinality(oks) >= 2 /\ InWork(p, "review")
                             /\ ~Br("noacceptnote")
                          THEN {Note("accept", p)} ELSE {})
  /\ UNCHANGED <<added, dropped, work, fleet, merge, sstate, mstatus, score,
                 attempt, head, pair, gen, held, fin, made, gone, twice, cause,
                 need, op, crashed, returnsN, ranks, waived, released, machine, stops, redstop, epoch, acks>>

\* A report refused: the card is not in the reader's reading cell (retired,
\* or of an old epoch): it changes nothing but the reader's hold.
ReadRefused(r, h) ==
  /\ Fenced /\ h \in rheld[r]
  /\ ~(h[1] \in readers[r]["reading"] /\ InEpoch(h[2]))
  /\ rheld' = [rheld EXCEPT ![r] = @ \ {h}]
  /\ UNCHANGED <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
                 score, attempt, head, pair, gen, held, fin, result, made, gone,
                 twice, cause, need, open, op, crashed, bad, returnsN, ranks,
                 waived, released, machine, stops, redstop, epoch, stale, acks>>

\* accept: a named set, all or nothing (D4): review -> merging and into merge
\* queued; refused unless every one has two different readers ok at its head.
\* Its asked and reading read cards retire in the same step (F1); a waiting
\* stream becomes merging (G4); its card judgments close. Readers and merge
\* first, work last.
Accept(S) ==
  /\ Free /\ S # {}
  /\ \A p \in S : InWork(p, "review") /\ Acceptable(p)
  /\ LET rl == IF Br("acceptkeepsreads") THEN {} ELSE {c \in Outstanding : c[2] \in S}
     IN /\ readers' = [r \in Readers |-> [x \in ReadCells |-> readers[r][x] \ rl]]
        /\ gone' = gone \cup rl
  /\ merge' = [s \in Streams |-> [merge[s] EXCEPT !["queued"] =
                 @ \cup {p \in S : StreamOf[p] = s}]]
  /\ sstate' = [s \in Streams |->
                  IF sstate[s] # "stopped" /\ \E p \in S : StreamOf[p] = s
                  THEN "merging" ELSE sstate[s]]
  /\ Begin(Op("accept", {<<p, "review", "merging">> : p \in S}, {}, {}, {}, S))
  /\ UNCHANGED <<added, dropped, work, fleet, mstatus, score, attempt, head,
                 pair, gen, held, fin, result, made, twice, cause, need, open,
                 bad, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* rework (D2): the primary's read cards retire; with a member up the next
\* work card is cut into the shortest ready queue and the primary goes
\* review -> working; with none up, review -> ready and start cuts it later.
\* Readers and fleet first, work last.
Rework(p) ==
  /\ Free /\ InWork(p, "review") /\ attempt[p] < MaxAttempt /\ Judged(p)
  /\ LET live == {c \in LiveReads : c[2] = p}
         nc == WC(p, attempt[p] + 1)
         to == IF Up # {} /\ ~Br("reworkready") THEN "working" ELSE "ready"
     IN /\ readers' = [r \in Readers |-> [x \in ReadCells |-> readers[r][x] \ live]]
        /\ gone' = gone \cup live
        /\ IF Up = {} THEN UNCHANGED <<fleet, made, twice>>
           ELSE \E m \in Up :
                  /\ ShortestIn(m, Up)
                  /\ fleet' = [fleet EXCEPT ![m]["ready"] = @ \cup {nc}]
                  /\ made' = made \cup {nc}
                  /\ twice' = (twice \/ nc \in made)
        /\ Begin(Op("rework", {<<p, "review", to>>}, {p}, {}, {}, {p}))
  /\ score' = IF Br("reworktail") THEN [score EXCEPT ![p] = MaxScore + 1] ELSE score
  /\ UNCHANGED <<added, dropped, work, merge, sstate, mstatus, attempt, head,
                 pair, gen, held, fin, result, cause, need, open, bad, returnsN,
                 ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* drop: off the table with the reason. Its unfinished work card is withdrawn,
\* its outstanding read cards retire, its merge place goes; work last. A
\* waiting primary that needs it is blocked: a judgment notification.
Drop(p) ==
  /\ Free /\ Placedp(p) /\ ~InWork(p, "landed") /\ Judged(p) /\ p \notin Sentinels
  /\ LET s == StreamOf[p]
         wl == {c \in Unfinished : c[2] = p}
         rl == IF Br("dropkeepsreads") THEN {} ELSE OutOf(p)
         q2 == merge[s]["queued"] \ {p}
         st2 == merge[s]["stuck"] \ {p}
     IN /\ fleet' = [m \in Members |-> [x \in FleetCells |-> fleet[m][x] \ wl]]
        /\ readers' = [r \in Readers |-> [x \in ReadCells |-> readers[r][x] \ rl]]
        /\ gone' = gone \cup wl \cup rl
        /\ merge' = [merge EXCEPT ![s]["queued"] = q2, ![s]["stuck"] = st2]
        /\ sstate' = [sstate EXCEPT ![s] =
                        StreamAfter(s, q2, st2, @, merge[s]["merged"], {p})]
        /\ Begin(Op("drop", {<<p, Cell(p), "off">>}, {}, {},
                    {Note("blocked", q) : q \in {x \in Primaries :
                        InWork(x, "waiting") /\ p \in Needs[x]}}, {p}))
  /\ cause' = [cause EXCEPT ![p] = None]
  /\ need' = [need EXCEPT ![p] = None]
  /\ UNCHANGED <<added, dropped, work, mstatus, score, attempt, head, pair, gen,
                 held, fin, result, made, twice, open, bad, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* rank: the coordinator changes a score; here, ranks a card first. Refused
\* for a landed primary (G6).
Rank(p) ==
  /\ Fenced /\ ranks < MaxRanks
  /\ Placedp(p) /\ ~InWork(p, "landed")
  /\ score[p] # MinScore
  /\ score' = [score EXCEPT ![p] = MinScore - 1]
  /\ ranks' = ranks + 1
  /\ UNCHANGED <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
                 attempt, head, pair, gen, held, fin, result, made, gone, twice,
                 cause, need, open, op, crashed, bad, returnsN, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* return: merging -> review; the card leaves merge queued or stuck (with its
\* cause); it answers the card's judgments (F3). Merge first, work last.
Return(p) ==
  /\ Free /\ InWork(p, "merging") /\ returnsN[p] < MaxReturns /\ Judged(p)
  /\ LET s == StreamOf[p]
         q2 == IF Br("returnkeeps") THEN merge[s]["queued"] ELSE merge[s]["queued"] \ {p}
         st2 == IF Br("returnkeeps") THEN merge[s]["stuck"] ELSE merge[s]["stuck"] \ {p}
     IN /\ merge' = [merge EXCEPT ![s]["queued"] = q2, ![s]["stuck"] = st2]
        /\ sstate' = [sstate EXCEPT ![s] =
                        StreamAfter(s, q2, st2, @, merge[s]["merged"], {})]
  /\ cause' = [cause EXCEPT ![p] = None]
  /\ need' = [need EXCEPT ![p] = None]
  /\ returnsN' = [returnsN EXCEPT ![p] = @ + 1]
  /\ Begin(Op("return", {<<p, "merging", "review">>}, {}, {},
              IF Br("noreturnnote") THEN {} ELSE {Note("returned", p)}, {p}))
  /\ UNCHANGED <<added, dropped, work, fleet, readers, mstatus, score, attempt,
                 head, pair, gen, held, fin, result, made, gone, twice, open,
                 bad, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* merge, given the fact green: the batch, a prefix of the stream's queued and
\* stuck cards in score order (a stuck card is a barrier, D6), lands on the
\* development branch. Merge first, work last.
MergeGreen(s, B) ==
  /\ Free /\ sstate[s] = "merging"
  /\ B # {} /\ B \subseteq merge[s]["queued"]
  /\ Br("outoforder") \/
       \A b \in B, q \in (merge[s]["queued"] \cup merge[s]["stuck"]) \ B : score[b] < score[q]
  /\ LET q2 == merge[s]["queued"] \ B IN
     /\ merge' = [merge EXCEPT ![s]["queued"] = q2, ![s]["merged"] = @ \cup B]
     /\ sstate' = [sstate EXCEPT ![s] =
                     StreamAfter(s, q2, merge[s]["stuck"], "merging", merge[s]["merged"] \cup B, {})]
  /\ IF Br("landskipswork") THEN UNCHANGED <<op, crashed>>
     ELSE Begin(Op("land", {<<b, "merging", "landed">> : b \in B}, {}, {}, {}, {}))
  /\ UNCHANGED <<added, dropped, work, fleet, readers, mstatus, score, attempt,
                 head, pair, gen, held, fin, result, made, gone, twice, cause,
                 need, open, bad, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* merge, given a fact on a card that stops the stream: a conflict on it, or
\* its need of a card q of another stream first, recorded as data (D6);
\* refused unless q is placed, in another stream, and not landed (F4). The
\* card goes to stuck, the stream stops, the coordinator is told.
MergeStop(s, p, why, q) ==
  /\ Fenced /\ sstate[s] = "merging" /\ p \in merge[s]["queued"]
  /\ IF why = "needs" THEN q \in Primaries /\ Placedp(q) /\ StreamOf[q] # s
                           /\ ~InWork(q, "landed")
                      ELSE q = None
  /\ Spend(TRUE)
  /\ merge' = [merge EXCEPT ![s]["queued"] = @ \ {p}, ![s]["stuck"] = @ \cup {p}]
  /\ sstate' = [sstate EXCEPT ![s] = "stopped"]
  /\ cause' = [cause EXCEPT ![p] = why]
  /\ need' = [need EXCEPT ![p] = q]
  /\ open' = IF Br("stopsilent") THEN open ELSE open \cup {StopNote(s)}
  /\ UNCHANGED <<added, dropped, work, fleet, readers, mstatus, score, attempt,
                 head, pair, gen, held, fin, result, made, gone, twice, op,
                 crashed, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* merge, given a fact on the stream: its branch red, or the merge queue
\* rejected the batch. The stream stops; no card moves; the coordinator is
\* told. The suspect is taken off by return (G5, as built).
MergeRed(s) ==
  /\ Fenced /\ sstate[s] = "merging"
  /\ Spend(TRUE)
  /\ sstate' = [sstate EXCEPT ![s] = "stopped"]
  /\ redstop' = redstop \cup {s}
  /\ open' = IF Br("stopsilent") THEN open ELSE open \cup {StopNote(s)}
  /\ UNCHANGED <<added, dropped, work, fleet, readers, merge, mstatus, score,
                 attempt, head, pair, gen, held, fin, result, made, gone, twice,
                 cause, need, op, crashed, returnsN, ranks, waived, released, machine, stops, epoch, rheld, stale, acks>>

\* resume (D6, G5), with what was done (--did): refused while a stuck card's
\* cross-stream need has not landed; a conflict or a red branch is resolved
\* by what the resume records. The stuck cards go back to queued at their
\* scores; the stream is no longer stopped, and its judgment closes (D5).
Resolved(p) ==
  CASE cause[p] = "needs" -> InWork(need[p], "landed")
    [] OTHER              -> TRUE
ResumeStep(s) ==
  /\ Fenced /\ sstate[s] = "stopped"
  /\ Br("resumeunresolved") \/ \A p \in merge[s]["stuck"] : Resolved(p)
  /\ LET q2 == merge[s]["queued"] \cup merge[s]["stuck"] IN
     /\ merge' = [merge EXCEPT ![s]["queued"] = q2, ![s]["stuck"] = {}]
     /\ sstate' = [sstate EXCEPT ![s] =
                     StreamAfter(s, q2, {}, "merging", merge[s]["merged"], {})]
  /\ cause' = [p \in Primaries |-> IF p \in merge[s]["stuck"] THEN None ELSE cause[p]]
  /\ open' = open \ {StopNote(s)}
  /\ redstop' = redstop \ {s}
  /\ UNCHANGED <<added, dropped, work, fleet, readers, mstatus, score, attempt,
                 head, pair, gen, held, fin, result, made, gone, twice, need, op,
                 crashed, bad, returnsN, ranks, waived, released, machine, stops, epoch, rheld, stale, acks>>

\* resume: the coordinator's verb.
Resume(s) == ResumeStep(s)

\* The tick resumes a stream stopped only for cross-stream needs that have
\* all landed.
TickResume(s) ==
  /\ Running /\ s \notin redstop /\ merge[s]["stuck"] # {}
  /\ \A p \in merge[s]["stuck"] : cause[p] = "needs"
  /\ ResumeStep(s)

\* A member goes down (reported, or fleet down): its unfinished work cards are
\* dealt to the up member with the shortest ready queue at a new generation;
\* with no member up they are withdrawn (a new generation, kept off the table)
\* and their primaries return to ready (fleet, then work). Its worker keeps
\* what it holds.
FleetDown(m) ==
  /\ mstatus[m] = "up"
  /\ Spend(TRUE)
  /\ mstatus' = [mstatus EXCEPT ![m] = "down"]
  /\ LET cs == fleet[m]["ready"] \cup fleet[m]["working"]
         others == Up \ {m}
     IN IF others # {}
        THEN /\ Fenced
             /\ \E t \in others :
                  /\ ShortestIn(t, others)
                  /\ fleet' = [fleet EXCEPT
                       ![m]["ready"] = IF Br("redealcopies") THEN @ ELSE {},
                       ![m]["working"] = IF Br("redealcopies") THEN @ ELSE {},
                       ![t]["ready"] = @ \cup cs]
             /\ gen' = Bump(cs)
             /\ UNCHANGED <<gone, op, crashed, open>>
        ELSE IF cs = {}
        THEN /\ Fenced
             /\ UNCHANGED <<fleet, gen, gone, op, crashed, open>>
        ELSE /\ Free
             /\ fleet' = [fleet EXCEPT ![m]["ready"] = {}, ![m]["working"] = {}]
             /\ gen' = Bump(cs)
             /\ gone' = IF Br("withdrawlost") THEN gone ELSE gone \cup cs
             /\ Begin(Op("withdraw", {<<c[2], "working", "ready">> : c \in cs},
                         {}, {}, {}, {}))
             /\ UNCHANGED open
  /\ UNCHANGED <<added, dropped, work, readers, merge, sstate, score, attempt,
                 head, pair, held, fin, result, made, twice, cause, need,
                 returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* A member comes up: ready queues are levelled in one call; the newest cards
\* (highest score) of the longest queue move to it at a new generation.
Newest(S, k) == CHOOSE T \in SUBSET S :
                  /\ Cardinality(T) = k
                  /\ \A t \in T, u \in S \ T : score[t[2]] >= score[u[2]]
FleetUp(m) ==
  /\ Fenced /\ mstatus[m] = "down"
  /\ mstatus' = [mstatus EXCEPT ![m] = "up"]
  /\ open' = open \ {FleetNote}
  /\ LET ups == Up \ {m} IN
     IF ups = {} THEN UNCHANGED <<fleet, gen>>
     ELSE \E d \in ups :
            /\ \A x \in ups : RL(x) <= RL(d)
            /\ LET mv == Newest(fleet[d]["ready"], RL(d) \div 2) IN
               /\ fleet' = [fleet EXCEPT ![d]["ready"] = @ \ mv, ![m]["ready"] = @ \cup mv]
               /\ gen' = Bump(mv)
  /\ UNCHANGED <<added, dropped, work, readers, merge, sstate, score, attempt,
                 head, pair, held, fin, result, made, gone, twice, cause, need,
                 op, crashed, bad, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* level (a tick duty): when two up members' ready queues differ by more than
\* one, the newest card of the longer moves to the shorter at a new
\* generation.
Level(a, b) ==
  /\ Running /\ Fenced
  /\ a \in Up /\ b \in Up /\ RL(a) > RL(b) + 1
  /\ LET mv == Newest(fleet[a]["ready"], 1) IN
     /\ fleet' = [fleet EXCEPT ![a]["ready"] = @ \ mv, ![b]["ready"] = @ \cup mv]
     /\ gen' = Bump(mv)
  /\ UNCHANGED <<added, dropped, work, readers, merge, sstate, mstatus, score,
                 attempt, head, pair, held, fin, result, made, gone, twice, cause,
                 need, open, op, crashed, bad, returnsN, ranks, waived, released,
                 machine, stops, redstop, epoch, rheld, stale, acks>>

\* The no-member judgment is a condition the tick (or the first verb) keeps
\* true: a primary ready, or a work card withdrawn, and no member up.
NoMemberCond ==
  /\ Up = {}
  /\ \/ \E p \in Primaries : Placedp(p) /\ InWork(p, "ready")
     \/ \E c \in gone \cap WorkCards : result[c] = None /\ Placedp(c[2])
TickNoMember ==
  /\ Fenced /\ NoMemberCond /\ FleetNote \notin open /\ ~Br("nofleetnote")
  /\ open' = open \cup {FleetNote}
  /\ UNCHANGED <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
                 score, attempt, head, pair, gen, held, fin, result, made, gone,
                 twice, cause, need, op, crashed, bad, returnsN, ranks, waived,
                 released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* THE MACHINE: the verbs start and stop. While stopped the tick does
\* nothing; every verb and every outside actor still acts.
MachineStart ==
  /\ Fenced /\ machine = "stopped"
  /\ machine' = "running"
  /\ UNCHANGED <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
                 score, attempt, head, pair, gen, held, fin, result, made, gone,
                 twice, cause, need, open, op, crashed, bad, returnsN, ranks,
                 waived, released, stops, redstop, epoch, rheld, stale, acks>>

MachineStop ==
  /\ Fenced /\ machine = "running" /\ stops < MaxStops
  /\ machine' = "stopped" /\ stops' = stops + 1
  /\ UNCHANGED <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
                 score, attempt, head, pair, gen, held, fin, result, made, gone,
                 twice, cause, need, open, op, crashed, bad, returnsN, ranks,
                 waived, released, redstop, epoch, rheld, stale, acks>>

\* clear: stops the machine and advances one epoch; every table is empty at
\* the new epoch. The outside actors keep what they hold (with its epoch).
\* The members' status survives; the fleet's judgment is the tick's
\* condition and is written again at the new epoch when it holds.
Clear ==
  /\ Free /\ epoch < MaxEpoch
  /\ epoch' = epoch + 1 /\ machine' = "stopped"
  /\ added' = {} /\ dropped' = {}
  /\ work' = [s \in Streams |-> [c \in WorkCells |-> {}]]
  /\ fleet' = [m \in Members |-> [c \in FleetCells |-> {}]]
  /\ readers' = [r \in Readers |-> [c \in ReadCells |-> {}]]
  /\ merge' = [s \in Streams |-> [c \in MergeCells |-> {}]]
  /\ sstate' = [s \in Streams |-> "waiting"]
  /\ score' = Score0 /\ attempt' = [p \in Primaries |-> 1] /\ head' = [p \in Primaries |-> 0]
  /\ pair' = [p \in Primaries |-> {}] /\ gen' = [c \in WorkCards |-> 1]
  /\ fin' = [c \in WorkCards |-> 0] /\ result' = [c \in Cards |-> None]
  /\ made' = {} /\ gone' = {} /\ twice' = FALSE
  /\ cause' = [p \in Primaries |-> None] /\ need' = [p \in Primaries |-> None]
  /\ open' = {} /\ redstop' = {}
  /\ waived' = [p \in Primaries |-> {}] /\ released' = {}
  /\ returnsN' = [p \in Primaries |-> 0] /\ ranks' = 0
  /\ UNCHANGED <<mstatus, held, op, crashed, bad, stops, rheld, stale, acks>>

\* ci (D8): a red CI observation on a placed primary in any state. It
\* notifies for judgment and moves nothing. (Green only notifies what
\* happened.)
CiRed(p) ==
  /\ Fenced /\ Placedp(p) /\ p \notin Sentinels
  /\ Spend(TRUE)
  /\ open' = open \cup {Note("ci", p)}
  /\ UNCHANGED <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
                 score, attempt, head, pair, gen, held, fin, result, made, gone,
                 twice, cause, need, op, crashed, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* ack <note> --reason (F3): the coordinator looked and nothing is to be done;
\* the judgment closes with the reason. Refused for a stopped stream's
\* judgment, a reached sentinel's and the fleet's (they close only when their
\* cause does). ACK CANNOT SILENCE A CARD: refused when it is the last open
\* judgment on a card that would then satisfy no clause of the owner's rule
\* (the refusal names the decisions that move the card).
Silences(n) ==
  /\ n.p # None /\ Placedp(n.p) /\ ~InWork(n.p, "landed")
  /\ ~RuleIn(n.p, open \ {n}, Cardinality(Primaries))
Ack(n) ==
  /\ Fenced /\ n \in open /\ acks < MaxAcks
  /\ n.t \notin {"stopped", "reached", "nomember"} \/ (Br("ackstopped") /\ n.t = "stopped")
  /\ ~Silences(n) \/ Br("ackstrands") \/ Br("auditack")
  /\ acks' = acks + 1
  /\ LET opn == open \ {n}
         p == n.p
         strands == Br("auditack") /\ InWork(p, "review") /\ OutOf(p) = {}
                    /\ ~Acceptable(p) /\ ~\E x \in opn : x.p = p
     IN open' = opn \cup (IF p # None /\ strands THEN {Note("stranded", p)} ELSE {})
  /\ UNCHANGED <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
                 score, attempt, head, pair, gen, held, fin, result, made, gone,
                 twice, cause, need, op, crashed, bad, returnsN, ranks, waived,
                 released, machine, stops, redstop, epoch, rheld, stale>>

\* The reversed witness "sentinelself": a reached sentinel lands by itself.
SentinelSelf(g) ==
  /\ Br("sentinelself") /\ Fenced /\ Reached(g)
  /\ work' = [work EXCEPT ![StreamOf[g]]["waiting"] = @ \ {g},
                          ![StreamOf[g]]["landed"] = @ \cup {g}]
  /\ open' = open \ {Note("reached", g)}
  /\ UNCHANGED <<added, dropped, fleet, readers, merge, sstate, mstatus, score,
                 attempt, head, pair, gen, held, fin, result, made, gone, twice,
                 cause, need, op, crashed, bad, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* The last write of a step over two tables: the same process (Continue), or
\* repair after the process was cut (Crash), from the operation record.
Continue ==
  /\ op # NoOp /\ ~crashed
  /\ ApplyWork(op)
  /\ op' = NoOp
  /\ UNCHANGED <<added, fleet, readers, merge, sstate, mstatus, score, pair, gen,
                 held, fin, result, made, gone, twice, cause, need, crashed, bad,
                 returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

Crash ==
  /\ op # NoOp /\ ~crashed
  /\ Spend(TRUE)
  /\ crashed' = TRUE
  /\ UNCHANGED <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
                 score, attempt, head, pair, gen, held, fin, result, made, gone,
                 twice, cause, need, open, op, returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

Repair ==
  /\ op # NoOp /\ crashed /\ ~Br("norepair")
  /\ ApplyWork(op)
  /\ op' = NoOp /\ crashed' = FALSE
  /\ UNCHANGED <<added, fleet, readers, merge, sstate, mstatus, score, pair, gen,
                 held, fin, result, made, gone, twice, cause, need, bad,
                 returnsN, ranks, waived, released, machine, stops, redstop, epoch, rheld, stale, acks>>

\* ------------------------------------------------------------------ next

\* The decisions open to a judgment notification (section 8), as verbs.
Answer(n) ==
  /\ n \in open
  /\ CASE n.t = "failed"  -> Rework(n.p) \/ Drop(n.p)
       [] n.t \in {"broken", "reads"} ->
             Rework(n.p) \/ Drop(n.p) \/ \E r \in Readers : AskAnother(n.p, r)
       [] n.t = "blocked" -> Drop(n.p) \/ \E q \in Primaries : Waive(n.p, q)
       [] n.t = "ci"      -> Ack(n) \/ Rework(n.p) \/ Return(n.p) \/ Drop(n.p)
       [] n.t = "skipped" -> Ack(n) \/ Drop(n.p)
       [] n.t = "reached" -> Release(n.p)
       [] n.t \in {"accept", "returned"} -> Accept({n.p}) \/ Rework(n.p) \/ Drop(n.p)
       [] n.t = "stranded" -> Rework(n.p) \/ Drop(n.p) \/ \E r \in Readers : AskAnother(n.p, r)
                              \/ \E q \in Primaries : Waive(n.p, q)
       [] n.t = "nomember" -> \E m \in Members : FleetUp(m)
       [] n.t = "stopped" -> Resume(n.s) \/
             \E p \in merge[n.s]["stuck"] \cup merge[n.s]["queued"] : Return(p) \/ Drop(p)

MergeStep(s) ==
  \/ \E B \in SUBSET Primaries : MergeGreen(s, B)
  \/ \E p \in Primaries, why \in CardCauses, q \in Primaries \cup {None} : MergeStop(s, p, why, q)
  \/ MergeRed(s)

Next ==
  \/ \E p \in Primaries :
       \/ Add(p) \/ Resolve(p) \/ Deal(p) \/ Ask(p) \/ Rework(p) \/ Drop(p)
       \/ Rank(p) \/ Return(p) \/ CiRed(p)
       \/ \E r \in Readers : AskAnother(p, r)
       \/ \E q \in Primaries : Waive(p, q)
       \/ Release(p) \/ SentinelSelf(p)
  \/ \E n \in open : Ack(n)
  \/ \E S \in SUBSET Primaries : Accept(S)
  \/ \E m \in Members :
       \/ FleetDown(m) \/ FleetUp(m)
       \/ \E c \in WorkCards : Take(m, c)
       \/ \E h \in held[m] :
            FinishRefused(m, h) \/ \E v \in {"ok", "failed"} : Finish(m, h, v)
  \/ \E r \in Readers :
       \/ \E c \in ReadCards : ReadStart(r, c)
       \/ \E h \in rheld[r] : ReadRefused(r, h) \/ \E v \in {"ok", "broken"} : Read(r, h, v)
  \/ \E s \in Streams : MergeStep(s) \/ Resume(s) \/ TickResume(s)
  \/ \E a, b \in Members : Level(a, b)
  \/ MachineStart \/ MachineStop \/ Clear \/ TickNoMember
  \/ Continue \/ Crash \/ Repair

Spec == Init /\ [][Next]_vars

\* Fairness: the mechanical moves run, workers and readers report, the merge
\* step is run with some fact, a down member eventually comes up, the last
\* write and repair run, the coordinator accepts what is acceptable and
\* answers every open judgment with one of its decisions. A member going down,
\* a cut, a red CI, ask --another and rank are never forced.
Fairness ==
  /\ \A p \in Primaries :
       /\ WF_vars(Add(p)) /\ WF_vars(Resolve(p)) /\ WF_vars(Deal(p))
       /\ WF_vars(Ask(p)) /\ WF_vars(Accept({p}))
  /\ \A m \in Members :
       /\ WF_vars(\E c \in WorkCards : Take(m, c))
       /\ WF_vars(\E h \in held[m] : FinishRefused(m, h) \/
                    \E v \in {"ok", "failed"} : Finish(m, h, v))
  /\ \A r \in Readers :
       /\ WF_vars(\E c \in ReadCards : ReadStart(r, c))
       /\ WF_vars(\E h \in rheld[r] : ReadRefused(r, h) \/
                    \E v \in {"ok", "broken"} : Read(r, h, v))
  /\ \A s \in Streams : WF_vars(MergeStep(s))
  /\ WF_vars(\E m \in Members : FleetUp(m))
  /\ WF_vars(Continue) /\ WF_vars(Repair)
  /\ \A s \in Streams : WF_vars(TickResume(s))
  /\ WF_vars(\E a, b \in Members : Level(a, b))
  /\ WF_vars(MachineStart)
  /\ WF_vars(TickNoMember)
  /\ \A n \in Notes : WF_vars(Answer(n))

FairSpec == Spec /\ Fairness

\* ------------------------------------------------------------------ rules

TypeOK ==
  /\ added \subseteq Primaries /\ dropped \subseteq added
  /\ \A s \in Streams, c \in WorkCells : \A p \in work[s][c] : StreamOf[p] = s
  /\ \A s \in Streams, c \in MergeCells : \A p \in merge[s][c] : StreamOf[p] = s
  /\ \A r \in Readers, c \in ReadCells : \A x \in readers[r][c] : x[4] = r
  /\ sstate \in [Streams -> {"waiting", "merging", "stopped", "landed"}]
  /\ mstatus \in [Members -> {"up", "down"}]
  /\ attempt \in [Primaries -> Attempts]
  /\ open \subseteq Notes
  /\ made \subseteq Cards /\ gone \subseteq Cards
  /\ bad \in 0..MaxBad

\* 1. A card is in one place in each table it is in.
OnePlace ==
  /\ \A p \in Primaries :
       Cardinality({x \in Streams \X WorkCells : p \in work[x[1]][x[2]]}) <= 1
  /\ \A p \in Primaries :
       Cardinality({x \in Streams \X MergeCells : p \in merge[x[1]][x[2]]}) <= 1
  /\ \A w \in WorkCards :
       Cardinality({x \in Members \X FleetCells : w \in fleet[x[1]][x[2]]}) <= 1
  /\ \A c \in ReadCards :
       Cardinality({x \in Readers \X ReadCells : c \in readers[x[1]][x[2]]}) <= 1

\* 2 (D3). Working iff delegated, a bijection: each primary in work working has
\* exactly one live work card (fleet ready or working), of its attempt; each
\* live work card has its primary in work working.
Delegated ==
  /\ \A p \in Primaries : InWork(p, "working") =>
       {c \in Unfinished : c[2] = p} = {WC(p, attempt[p])}
  /\ \A c \in Unfinished : InWork(c[2], "working")
WorkingIsDelegated == op = NoOp => Delegated

\* 3. Primaries with read cards in asked or reading are in review.
ReadsInReview == \A c \in Outstanding : InWork(c[2], "review")
ReadsOnlyInReview == op = NoOp => ReadsInReview

\* 4. Primaries in merge queued + stuck = primaries in work merging.
MergeMatches == \A s \in Streams :
                  merge[s]["queued"] \cup merge[s]["stuck"] = work[s]["merging"]
MergeMatchesWork == op = NoOp => MergeMatches

\* 5. Primaries in merge merged = primaries in work landed.
MergedLanded == \A s \in Streams : merge[s]["merged"] = work[s]["landed"] \ Sentinels
MergedIsLanded == op = NoOp => MergedLanded

\* The same four stated for every state, pending operation or not: the cut
\* between a step's writes breaks each of them (the configurations
\* MCSprintCut*), which is why they hold only when nothing is pending.
AlwaysDelegated == Delegated
AlwaysReadsInReview == ReadsInReview
AlwaysMergeMatches == MergeMatches
AlwaysMergedLanded == MergedLanded

\* 6. No primary enters merging without ok read cards from two different
\* readers at its head (and they stay: landed keeps them). A sentinel is
\* never read.
AcceptedHadTwoReaders ==
  \A p \in Primaries \ Sentinels :
    (InWork(p, "merging") \/ InWork(p, "landed")) => Cardinality(OkReaders(p)) >= 2

\* 7. A score never changes except by rank (an action property).
ScoreOnlyByRank ==
  [][score' # score => ranks' = ranks + 1]_vars

\* 8. No card is lost or made twice: every card cut is in exactly one place,
\* its table or its kept record, and no identity is cut twice; every admitted
\* primary is on the work table or dropped, never both.
NoCardLostOrTwice ==
  /\ ~twice
  /\ \A c \in Cards :
       IF c \in made THEN Placed(c) # (c \in gone)
       ELSE ~Placed(c) /\ c \notin gone
  /\ \A p \in Primaries :
       IF p \in added THEN (Cell(p) # None) # (p \in dropped)
       ELSE Cell(p) = None /\ p \notin dropped

\* 9 (D5). A stopped stream has an open judgment notification.
StoppedIsNotified ==
  \A s \in Streams : sstate[s] = "stopped" => StopNote(s) \in open

\* D3, F2. A finish is accepted only from the live generation.
FinishIsLive == \A c \in WorkCards : fin[c] # 0 => fin[c] = gen[c]

\* D6. A card with a recorded cross-stream need lands only after the card it
\* needs.
LandsAfterNeed ==
  \A p \in Primaries :
    (InWork(p, "landed") /\ need[p] # None) => InWork(need[p], "landed")

\* G3. A primary whose reads ran out is never silent.
ReadsExhaustedIsNotified ==
  op = NoOp => \A p \in Primaries : ~Exhausted(p, OutOf(p), OkReaders(p), open)

\* G4. The stream state is true: merging has queued cards, a stuck card stops
\* the stream, waiting has none queued, landed has every admitted primary of
\* the stream landed or dropped.
StreamStateIsTrue ==
  op = NoOp =>
    \A s \in Streams :
      /\ sstate[s] = "merging" => merge[s]["queued"] # {}
      /\ merge[s]["stuck"] # {} => sstate[s] = "stopped"
      /\ sstate[s] \in {"waiting", "landed"} => merge[s]["queued"] = {}
      /\ sstate[s] = "landed" => AllDone(s, merge[s]["merged"], {})

\* NEEDS. A primary anywhere but waiting has every need landed or waived.
NeedsMetOutsideWaiting ==
  \A p \in Primaries : (Placedp(p) /\ ~InWork(p, "waiting")) => NeedsMet(p)

\* NEEDS. A cycle of needs is never admitted.
NoNeedCycle == \A p \in added : ~InCycle(p, added)

\* SENTINELS. No primary that needs a sentinel leaves waiting before the
\* sentinel has landed.
SentinelHolds ==
  \A p \in Primaries, g \in Sentinels :
    (g \in Needs[p] /\ Placedp(p) /\ ~InWork(p, "waiting")) => InWork(g, "landed")

\* SENTINELS. A reached sentinel has an open judgment.
ReachedIsNotified == op = NoOp => \A g \in Sentinels : Reached(g) => Note("reached", g) \in open

\* SENTINELS. A sentinel lands only by release, and is never dealt, read or
\* merged.
SentinelLandsByRelease == \A g \in Sentinels : InWork(g, "landed") => g \in released
SentinelNeverDealt ==
  /\ \A c \in made : c[2] \notin Sentinels
  /\ \A s \in Streams, x \in MergeCells : merge[s][x] \cap Sentinels = {}

\* CLEAR. Nothing of an old epoch changes state at the new one: no report
\* from a worker or reader holding an old epoch is ever accepted.
NoOldEpochWrite == ~stale

\* THE OWNER'S RULE, checked whenever no operation is pending (a pending
\* operation is itself the tick's next move).
OwnersRule ==
  op = NoOp =>
    \A p \in Primaries :
      (Placedp(p) /\ ~InWork(p, "landed")) => RuleIn(p, open, Cardinality(Primaries))

\* Section 7, 1: in work order, the head of the stream's queued cell first.
MergeInWorkOrder ==
  [][\A s \in Streams :
       \A b \in merge'[s]["merged"] \ merge[s]["merged"],
          q \in merge[s]["queued"] \ merge'[s]["merged"] :
         score[b] < score[q]]_vars

\* D1. A step cut short is finished.
CutRepaired == (op # NoOp) ~> (op = NoOp)

\* The no-member condition is eventually judged (or clears).
NoMemberIsJudged == NoMemberCond ~> (FleetNote \in open \/ ~NoMemberCond)

\* Every primary that is not dropped eventually lands.
EveryPrimaryEnds ==
  \A p \in Primaries : <>(InWork(p, "landed") \/ p \in dropped)

\* A stopped stream eventually moves.
StoppedStreamMoves ==
  \A s \in Streams : (sstate[s] = "stopped") ~> (sstate[s] # "stopped")

=============================================================================
