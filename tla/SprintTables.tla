---------------------------- MODULE SprintTables ----------------------------
\* nova-sprint's sprint table (docs/SPEC-SPRINT.md): the four tables on
\* nova-table (work, readers, merge, fleet), the mechanical moves between them,
\* the judgment notifications, the verbs, and the steps that touch two tables.
\*
\* The state the design owns:
\*   work[s][cell]      the work table: row per stream, cells waiting ready
\*                      working review merging landed, members primaries
\*   fleet[m][cell]     the fleet table: row per member, cells ready working
\*                      done, members work cards <<"w", p, attempt>>
\*   readers[r][cell]   the readers table: row per reader, cells asked
\*                      reading ok broken, members read cards
\*                      <<"r", p, attempt, reader>>
\*   merge[s][cell]     the merge table: cells queued merged stuck
\*   sstate[s]          the stream's merge state: waiting merging stopped landed
\*   mstatus[m]         a fleet member's status: up or down
\*   score, attempt, head   the primary's fields; a read card's head is its
\*                      attempt (a primary is read only at the head its
\*                      attempt's work card produced)
\*   stuckN, returnsN   the counters the model's bounds read (stuck, returns)
\*   result[c]          a finished card's outcome: ok, failed (work) or
\*                      ok, broken (read); "none" before
\*   added, dropped     primaries admitted; primaries that left the table
\*   made, gone, twice  every card ever cut; cards off their table with their
\*                      record kept (withdrawn work cards, retired read cards);
\*                      TRUE once an identity was cut a second time
\*   open               the open judgment notifications
\*   op, crashed        the pending two-table operation (section 10) and
\*                      whether the process that wrote it was cut
\*   downs, ranks       how many member-down events and ranks have happened
\*                      (bounds of the instance)
\*
\* A step that touches two tables (section 10) is two steps here: the verb's
\* first step records the operation in op and writes every table but the work
\* table; Continue (the same process) or Repair (after Crash, the cut) writes
\* the work table from the operation record and clears it. Notifications the
\* step opens are written with the work table. The model holds at most one
\* operation record: a second two-table verb waits for the slot.
\*
\* Invariants that hold at every reachable state: 1, 3, 6, 8, 9 and 7 (as an
\* action property). Invariants that hold only when no operation is pending:
\* 2, 4, 5 (each relates the work table to another table, and the cut lies
\* between the two writes).
\*
\* Fixes is the set of changes to the text of the design this model applies
\* (the findings of the model, each the smallest change that makes the design
\* pass). A configuration without one of them is the design as written, and
\* TLC finds the violation the finding names:
\*   "cutatstart"     rework cuts no work card; start cuts it (section 11).
\*                    Without it: rework cuts the next work card into the
\*                    fleet's ready queue at once (section 6): the primary is
\*                    in work ready while its card is in fleet ready.
\*   "redeal"         start re-deals the withdrawn work card of the primary's
\*                    attempt instead of cutting <p>.w<attempt> again.
\*   "stopbyresume"   a stream-stopped notification is answered only by
\*                    resume; a verb acting on the stuck card does not close it.
\*   "pendingblocks"  while an operation record is pending every verb refuses
\*                    but repair (and check).
\*   "readsexhausted" a read that leaves a primary in review with no read
\*                    outstanding and no two ok readers opens a judgment
\*                    notification (rework, ask another reader, drop).
\*
\* Broken is a reversed witness: a deliberately wrong design, each caught by
\* one property. "none" is the design.
\*   "onereader"      accept with one ok reader
\*   "samereader"     accept counts ok read cards ever made for the primary,
\*                    so one reader counted twice across attempts suffices
\*   "outoforder"     the merge step lands any queued cards, not the head
\*   "finishnomove"   finish moves the fleet card to done and not the primary
\*   "stopsilent"     a stream stops without a notification
\*   "norepair"       repair never runs: a cut step stays cut
\*   "reworktail"     rework puts the primary at the tail (changes the score)
\*   "redealcopies"   a member going down deals its cards without taking them
\*   "dropkeepsreads" drop leaves the primary's asked and reading read cards
\*   "returnkeeps"    return leaves the primary in merge queued or stuck
\*   "landskipswork"  a green batch moves merge and never the work table
\*   "withdrawlost"   withdrawn work cards leave the fleet with no record

EXTENDS Naturals, FiniteSets

CONSTANTS Streams, Primaries, Members, Readers,
          StreamOf,   \* [Primaries -> Streams]
          Needs,      \* [Primaries -> SUBSET Primaries]
          Score0,     \* [Primaries -> Nat], distinct
          Admitted0,  \* the primaries admitted before the first step
          MaxAttempt, MaxStuck, MaxReturns, MaxDowns, MaxRanks,
          Fixes, Broken

None == "none"
Fix(x) == x \in Fixes
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
Causes == {"conflict", "red", "needs"}
NoteTypes == {"failed", "broken", "reads", "blocked", "stopped"}
\* A judgment notification: its type, the primary, the stream, the cause.
Note(t, p, why) == [t |-> t, p |-> p, s |-> StreamOf[p], why |-> why]
\* The cause of a stop is not kept in the model's notification: the verbs
\* the model's coordinator may answer with are the same for the three causes.
Notes == {Note(t, p, None) : t \in NoteTypes, p \in Primaries}

VARIABLES added, dropped, work, fleet, readers, merge, sstate, mstatus,
          score, attempt, head, stuckN, returnsN, result,
          made, gone, twice, open, op, crashed, downs, ranks

vars == <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
          score, attempt, head, stuckN, returnsN, result,
          made, gone, twice, open, op, crashed, downs, ranks>>

\* ------------------------------------------------------------------ views

InWork(p, c) == p \in work[StreamOf[p]][c]
Cell(p) == IF \E c \in WorkCells : InWork(p, c)
           THEN CHOOSE c \in WorkCells : InWork(p, c) ELSE None
Landed == {p \in Primaries : InWork(p, "landed")}
AllLanded(s) == \A p \in Primaries : StreamOf[p] = s => InWork(p, "landed")
RL(m) == Cardinality(fleet[m]["ready"])
Up == {m \in Members : mstatus[m] = "up"}
ShortestIn(m, S) == m \in S /\ \A x \in S : RL(m) <= RL(x)
AskedLen(r) == Cardinality(readers[r]["asked"])
LiveReads == UNION {readers[r][c] : r \in Readers, c \in ReadCells}
Outstanding == UNION {readers[r][c] : r \in Readers, c \in {"asked", "reading"}}
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

\* The stream's merge state after its queued and stuck cells change.
StreamAfter(s, q, st, was) ==
  IF was = "stopped" THEN "stopped"
  ELSE IF q # {} THEN "merging"
  ELSE IF st # {} THEN was
  ELSE IF \A p \in Primaries : StreamOf[p] = s => p \in merge[s]["merged"]
       THEN "landed" ELSE "waiting"


\* An operation record: the moves of primaries in the work table (to "off"
\* for drop), the primaries whose attempt increments, new heads, the
\* notifications opened, the primaries whose notifications close.
Op(k, mv, at, hd, nt, cl) ==
  [kind |-> k, moves |-> mv, att |-> at, hd |-> hd, notes |-> nt, closes |-> cl]

NoOp == Op("none", {}, {}, {}, {}, {})

\* Two-table verbs need the one operation slot; every other verb runs while an
\* operation is pending unless the pendingblocks fix is applied.
Free == op = NoOp
Quiet == op = NoOp \/ ~Fix("pendingblocks")

Begin(o) == op' = o /\ crashed' = FALSE

Closable(n, o) ==
  /\ n.p \in o.closes
  /\ (n.t # "stopped" \/ ~Fix("stopbyresume"))

\* The work-table write of an operation, from its record.
ApplyWork(o) ==
  /\ work' = [s \in Streams |-> [c \in WorkCells |->
               (work[s][c] \ {mv[1] : mv \in {x \in o.moves : x[2] = c}})
               \cup {mv[1] : mv \in {x \in o.moves : x[3] = c /\ StreamOf[x[1]] = s}}]]
  /\ dropped' = dropped \cup {mv[1] : mv \in {x \in o.moves : x[3] = "off"}}
  /\ attempt' = [p \in Primaries |-> IF p \in o.att THEN attempt[p] + 1 ELSE attempt[p]]
  /\ head' = [p \in Primaries |-> IF \E h \in Attempts : <<p, h>> \in o.hd
                                  THEN CHOOSE h \in Attempts : <<p, h>> \in o.hd
                                  ELSE head[p]]
  /\ open' = (open \ {n \in open : Closable(n, o)}) \cup o.notes

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
  /\ stuckN = [p \in Primaries |-> 0]
  /\ returnsN = [p \in Primaries |-> 0]
  /\ result = [c \in Cards |-> None]
  /\ made = {} /\ gone = {} /\ twice = FALSE
  /\ open = {}
  /\ op = NoOp /\ crashed = FALSE
  /\ downs = 0 /\ ranks = 0

\* ------------------------------------------------------------------ verbs

\* add: admit a primary into its stream, waiting if it needs something.
Add(p) ==
  /\ Quiet /\ p \notin added
  /\ added' = added \cup {p}
  /\ LET to == IF Needs[p] \subseteq Landed THEN "ready" ELSE "waiting"
     IN work' = [work EXCEPT ![StreamOf[p]][to] = @ \cup {p}]
  /\ open' = IF Needs[p] \cap dropped # {} THEN open \cup {Note("blocked", p, None)}
             ELSE open
  /\ UNCHANGED <<dropped, fleet, readers, merge, sstate, mstatus, score, attempt,
                 head, stuckN, returnsN, result, made, gone, twice, op, crashed,
                 downs, ranks>>

\* resolve: waiting -> ready where needs have landed.
Resolve(p) ==
  /\ Quiet /\ InWork(p, "waiting") /\ Needs[p] \subseteq Landed
  /\ work' = [work EXCEPT ![StreamOf[p]]["waiting"] = @ \ {p},
                          ![StreamOf[p]]["ready"] = @ \cup {p}]
  /\ UNCHANGED <<added, dropped, fleet, readers, merge, sstate, mstatus, score,
                 attempt, head, stuckN, returnsN, result, made, gone, twice,
                 open, op, crashed, downs, ranks>>

\* start: ready -> working; cuts the work card and deals it to the up member
\* with the shortest ready queue. Fleet first, work last.
Start(p) ==
  /\ Free /\ InWork(p, "ready")
  /\ LET c == WC(p, attempt[p]) IN
     \E m \in Up :
       /\ ShortestIn(m, Up)
       /\ fleet' = [fleet EXCEPT ![m]["ready"] = @ \cup {c}]
       /\ IF Fix("redeal") /\ c \in gone
            THEN gone' = gone \ {c} /\ UNCHANGED <<made, twice>>
            ELSE /\ made' = made \cup {c}
                 /\ twice' = (twice \/ c \in made)
                 /\ UNCHANGED gone
  /\ Begin(Op("start", {<<p, "ready", "working">>}, {}, {}, {}, {}))
  /\ UNCHANGED <<added, dropped, work, readers, merge, sstate, mstatus, score,
                 attempt, head, stuckN, returnsN, result, open, downs, ranks>>

\* take: a worker moves its work card fleet ready -> working.
Take(m, c) ==
  /\ Quiet /\ mstatus[m] = "up" /\ c \in fleet[m]["ready"]
  /\ fleet' = [fleet EXCEPT ![m]["ready"] = @ \ {c}, ![m]["working"] = @ \cup {c}]
  /\ UNCHANGED <<added, dropped, work, readers, merge, sstate, mstatus, score,
                 attempt, head, stuckN, returnsN, result, made, gone, twice,
                 open, op, crashed, downs, ranks>>

\* finish: the work card is done, ok or failed; its primary to review at the
\* head the card produced. Failed notifies for judgment. Fleet first.
Finish(m, c, v) ==
  /\ Free /\ c \in fleet[m]["working"]
  /\ fleet' = [fleet EXCEPT ![m]["working"] = @ \ {c}, ![m]["done"] = @ \cup {c}]
  /\ result' = [result EXCEPT ![c] = v]
  /\ IF Br("finishnomove") THEN UNCHANGED <<op, crashed>>
     ELSE Begin(Op("finish", {<<c[2], "working", "review">>}, {}, {<<c[2], c[3]>>},
                   IF v = "failed" THEN {Note("failed", c[2], None)} ELSE {}, {}))
  /\ UNCHANGED <<added, dropped, work, readers, merge, sstate, mstatus, score,
                 attempt, head, stuckN, returnsN, made, gone, twice, open,
                 downs, ranks>>

\* ask: a primary in review with no read card on the table is dealt to two
\* different readers, each to the shortest asked queue.
Ask(p) ==
  /\ Quiet /\ InWork(p, "review")
  /\ \A c \in LiveReads : c[2] # p
  /\ \E r1, r2 \in Readers :
       /\ r1 # r2
       /\ \A x \in Readers : AskedLen(r1) <= AskedLen(x)
       /\ \A x \in Readers \ {r1} : AskedLen(r2) <= AskedLen(x)
       /\ LET c1 == RC(p, attempt[p], r1)
              c2 == RC(p, attempt[p], r2)
          IN /\ readers' = [readers EXCEPT ![r1]["asked"] = @ \cup {c1},
                                           ![r2]["asked"] = @ \cup {c2}]
             /\ made' = made \cup {c1, c2}
             /\ twice' = (twice \/ c1 \in made \/ c2 \in made)
  /\ UNCHANGED <<added, dropped, work, fleet, merge, sstate, mstatus, score,
                 attempt, head, stuckN, returnsN, result, gone, open, op,
                 crashed, downs, ranks>>

\* The judgment decision "ask another reader", answering a broken read.
AskAnother(p, r) ==
  /\ Quiet /\ InWork(p, "review")
  /\ \E c \in LiveReads : c[2] = p /\ c[3] = attempt[p] /\ result[c] = "broken"
  /\ RC(p, attempt[p], r) \notin made
  /\ readers' = [readers EXCEPT ![r]["asked"] = @ \cup {RC(p, attempt[p], r)}]
  /\ made' = made \cup {RC(p, attempt[p], r)}
  /\ open' = open \ {Note("broken", p, None), Note("reads", p, None)}
  /\ UNCHANGED <<added, dropped, work, fleet, merge, sstate, mstatus, score,
                 attempt, head, stuckN, returnsN, result, gone, twice, op,
                 crashed, downs, ranks>>

\* A reader moves its own read card asked -> reading.
ReadStart(r, c) ==
  /\ Quiet /\ c \in readers[r]["asked"]
  /\ readers' = [readers EXCEPT ![r]["asked"] = @ \ {c}, ![r]["reading"] = @ \cup {c}]
  /\ UNCHANGED <<added, dropped, work, fleet, merge, sstate, mstatus, score,
                 attempt, head, stuckN, returnsN, result, made, gone, twice,
                 open, op, crashed, downs, ranks>>

\* read: a reader records ok or broken. Broken notifies for judgment.
Read(r, c, v) ==
  /\ Quiet /\ c \in readers[r]["reading"]
  /\ readers' = [readers EXCEPT ![r]["reading"] = @ \ {c}, ![r][v] = @ \cup {c}]
  /\ result' = [result EXCEPT ![c] = v]
  /\ LET p == c[2]
         others == {x \in Outstanding : x[2] = p} \ {c}
         oks == {x \in Readers : RC(p, head[p], x) \in readers[x]["ok"]}
                \cup (IF v = "ok" /\ c[3] = head[p] THEN {r} ELSE {})
         short == Fix("readsexhausted") /\ v = "ok" /\ others = {}
                  /\ Cardinality(oks) < 2 /\ InWork(p, "review")
     IN open' = open \cup (IF v = "broken" THEN {Note("broken", p, None)} ELSE {})
                     \cup (IF short THEN {Note("reads", p, None)} ELSE {})
  /\ UNCHANGED <<added, dropped, work, fleet, merge, sstate, mstatus, score,
                 attempt, head, stuckN, returnsN, made, gone, twice, op,
                 crashed, downs, ranks>>

\* accept: review -> merging and into merge queued; refused without two
\* different readers ok at the head. Merge first, work last.
Accept(p) ==
  /\ Free /\ InWork(p, "review") /\ Acceptable(p)
  /\ LET s == StreamOf[p] IN
     /\ merge' = [merge EXCEPT ![s]["queued"] = @ \cup {p}]
     /\ sstate' = [sstate EXCEPT ![s] = IF @ = "stopped" THEN @ ELSE "merging"]
  /\ Begin(Op("accept", {<<p, "review", "merging">>}, {}, {}, {}, {p}))
  /\ UNCHANGED <<added, dropped, work, fleet, readers, mstatus, score, attempt,
                 head, stuckN, returnsN, result, made, gone, twice, open,
                 downs, ranks>>

\* rework: review -> ready with a fix; retires the primary's read cards; the
\* next attempt. Readers first (and fleet, as section 6 reads), work last.
Rework(p) ==
  /\ Free /\ InWork(p, "review") /\ attempt[p] < MaxAttempt
  /\ LET live == {c \in LiveReads : c[2] = p}
         nc == WC(p, attempt[p] + 1)
     IN /\ readers' = [r \in Readers |-> [x \in ReadCells |-> readers[r][x] \ live]]
        /\ IF Fix("cutatstart") \/ Up = {}
             THEN /\ gone' = gone \cup live
                  /\ UNCHANGED <<fleet, made, twice>>
             ELSE \E m \in Up :
                    /\ ShortestIn(m, Up)
                    /\ fleet' = [fleet EXCEPT ![m]["ready"] = @ \cup {nc}]
                    /\ made' = made \cup {nc}
                    /\ twice' = (twice \/ nc \in made)
                    /\ gone' = gone \cup live
  /\ score' = IF Br("reworktail") THEN [score EXCEPT ![p] = MaxScore + 1] ELSE score
  /\ Begin(Op("rework", {<<p, "review", "ready">>}, {p}, {}, {}, {p}))
  /\ UNCHANGED <<added, dropped, work, merge, sstate, mstatus, attempt, head,
                 stuckN, returnsN, result, open, downs, ranks>>

\* drop: off the table with the reason. Its unfinished work card is withdrawn,
\* its outstanding read cards retired, its merge place removed; work last.
\* A primary that needs it and waits is blocked: a judgment notification.
Drop(p) ==
  /\ Free /\ p \in added /\ p \notin dropped /\ ~InWork(p, "landed")
  /\ LET s == StreamOf[p]
         wl == {c \in Unfinished : c[2] = p}
         rl == IF Br("dropkeepsreads") THEN {} ELSE {c \in Outstanding : c[2] = p}
         q2 == merge[s]["queued"] \ {p}
         st2 == merge[s]["stuck"] \ {p}
     IN /\ fleet' = [m \in Members |-> [x \in FleetCells |-> fleet[m][x] \ wl]]
        /\ readers' = [r \in Readers |-> [x \in ReadCells |-> readers[r][x] \ rl]]
        /\ gone' = gone \cup wl \cup rl
        /\ merge' = [merge EXCEPT ![s]["queued"] = q2, ![s]["stuck"] = st2]
        /\ sstate' = [sstate EXCEPT ![s] = StreamAfter(s, q2, st2, @)]
        /\ Begin(Op("drop", {<<p, Cell(p), "off">>}, {}, {},
                    {Note("blocked", q, None) : q \in {x \in Primaries :
                        InWork(x, "waiting") /\ p \in Needs[x]}}, {p}))
  /\ UNCHANGED <<added, dropped, work, mstatus, score, attempt, head, stuckN,
                 returnsN, result, made, twice, open, downs, ranks>>

\* rank: the coordinator changes a score: here, rank that card first.
Rank(p) ==
  /\ Quiet /\ ranks < MaxRanks
  /\ p \in added /\ p \notin dropped /\ ~InWork(p, "landed")
  /\ score[p] # MinScore
  /\ score' = [score EXCEPT ![p] = MinScore - 1]
  /\ ranks' = ranks + 1
  /\ UNCHANGED <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
                 attempt, head, stuckN, returnsN, result, made, gone, twice,
                 open, op, crashed, downs>>

\* return: merging -> review (the stream's CI went red and the coordinator
\* sent it back). Merge first, work last.
Return(p) ==
  /\ Free /\ InWork(p, "merging") /\ returnsN[p] < MaxReturns
  /\ LET s == StreamOf[p]
         q2 == IF Br("returnkeeps") THEN merge[s]["queued"] ELSE merge[s]["queued"] \ {p}
         st2 == IF Br("returnkeeps") THEN merge[s]["stuck"] ELSE merge[s]["stuck"] \ {p}
     IN /\ merge' = [merge EXCEPT ![s]["queued"] = q2, ![s]["stuck"] = st2]
        /\ sstate' = [sstate EXCEPT ![s] = StreamAfter(s, q2, st2, @)]
  /\ returnsN' = [returnsN EXCEPT ![p] = @ + 1]
  /\ Begin(Op("return", {<<p, "merging", "review">>}, {}, {}, {}, {p}))
  /\ UNCHANGED <<added, dropped, work, fleet, readers, mstatus, score, attempt,
                 head, stuckN, result, made, gone, twice, open, downs, ranks>>

\* merge, given the fact green: the batch, the head of the stream's queued
\* cell in score order, merged to the development branch. Merge first.
MergeGreen(s, B) ==
  /\ Free /\ sstate[s] = "merging"
  /\ B # {} /\ B \subseteq merge[s]["queued"]
  /\ Br("outoforder") \/ \A b \in B, q \in merge[s]["queued"] \ B : score[b] < score[q]
  /\ LET q2 == merge[s]["queued"] \ B IN
     /\ merge' = [merge EXCEPT ![s]["queued"] = q2, ![s]["merged"] = @ \cup B]
     /\ sstate' = [sstate EXCEPT ![s] =
                     IF q2 # {} THEN "merging"
                     ELSE IF \A p \in Primaries : StreamOf[p] = s => p \in merge[s]["merged"] \cup B
                          THEN "landed" ELSE "waiting"]
  /\ IF Br("landskipswork") THEN UNCHANGED <<op, crashed>>
     ELSE Begin(Op("land", {<<b, "merging", "landed">> : b \in B}, {}, {}, {}, {}))
  /\ UNCHANGED <<added, dropped, work, fleet, readers, mstatus, score, attempt,
                 head, stuckN, returnsN, result, made, gone, twice, open,
                 downs, ranks>>

\* merge, given a fact that stops the stream: a conflict on a card, the stream
\* branch red (the suspect named), a card needing a card of another stream
\* first. The card goes to stuck, the stream stops, the coordinator is told.
MergeStop(s, p, why) ==
  /\ Quiet /\ sstate[s] = "merging"
  /\ p \in merge[s]["queued"] /\ stuckN[p] < MaxStuck
  /\ merge' = [merge EXCEPT ![s]["queued"] = @ \ {p}, ![s]["stuck"] = @ \cup {p}]
  /\ sstate' = [sstate EXCEPT ![s] = "stopped"]
  /\ stuckN' = [stuckN EXCEPT ![p] = @ + 1]
  /\ open' = IF Br("stopsilent") THEN open ELSE open \cup {Note("stopped", p, None)}
  /\ UNCHANGED <<added, dropped, work, fleet, readers, mstatus, score, attempt,
                 head, returnsN, result, made, gone, twice, op, crashed,
                 downs, ranks>>

\* resume: a stopped stream moves again; its stuck cards back to queued.
Resume(s) ==
  /\ Quiet /\ sstate[s] = "stopped"
  /\ LET q2 == merge[s]["queued"] \cup merge[s]["stuck"] IN
     /\ merge' = [merge EXCEPT ![s]["queued"] = q2, ![s]["stuck"] = {}]
     /\ sstate' = [sstate EXCEPT ![s] = StreamAfter(s, q2, {}, "merging")]
  /\ open' = open \ {n \in open : n.t = "stopped" /\ n.s = s}
  /\ UNCHANGED <<added, dropped, work, fleet, readers, mstatus, score, attempt,
                 head, stuckN, returnsN, result, made, gone, twice, op, crashed,
                 downs, ranks>>

\* A member goes down (reported, or fleet down): its unfinished work cards
\* are dealt to the up member with the shortest ready queue; with no member
\* up they are withdrawn and their primaries return to ready (fleet, then work).
FleetDown(m) ==
  /\ mstatus[m] = "up" /\ downs < MaxDowns
  /\ mstatus' = [mstatus EXCEPT ![m] = "down"]
  /\ downs' = downs + 1
  /\ LET cs == fleet[m]["ready"] \cup fleet[m]["working"]
         others == Up \ {m}
     IN IF others # {}
        THEN /\ Quiet
             /\ \E t \in others :
                  /\ ShortestIn(t, others)
                  /\ fleet' = [fleet EXCEPT
                       ![m]["ready"] = IF Br("redealcopies") THEN @ ELSE {},
                       ![m]["working"] = IF Br("redealcopies") THEN @ ELSE {},
                       ![t]["ready"] = @ \cup cs]
             /\ UNCHANGED <<gone, op, crashed>>
        ELSE IF cs = {}
        THEN /\ Quiet
             /\ UNCHANGED <<fleet, gone, op, crashed>>
        ELSE /\ Free
             /\ fleet' = [fleet EXCEPT ![m]["ready"] = {}, ![m]["working"] = {}]
             /\ gone' = IF Br("withdrawlost") THEN gone ELSE gone \cup cs
             /\ Begin(Op("withdraw", {<<c[2], "working", "ready">> : c \in cs},
                         {}, {}, {}, {}))
  /\ UNCHANGED <<added, dropped, work, readers, merge, sstate, score, attempt,
                 head, stuckN, returnsN, result, made, twice, open, ranks>>

\* A member comes up: ready queues are levelled in one call, the newest cards
\* (highest score) of the longest queue moving to it.
Newest(S, k) == CHOOSE T \in SUBSET S :
                  /\ Cardinality(T) = k
                  /\ \A t \in T, u \in S \ T : score[t[2]] >= score[u[2]]
FleetUp(m) ==
  /\ Quiet /\ mstatus[m] = "down"
  /\ mstatus' = [mstatus EXCEPT ![m] = "up"]
  /\ LET ups == Up \ {m} IN
     IF ups = {} THEN UNCHANGED fleet
     ELSE \E d \in ups :
            /\ \A x \in ups : RL(x) <= RL(d)
            /\ LET mv == Newest(fleet[d]["ready"], RL(d) \div 2) IN
               fleet' = [fleet EXCEPT ![d]["ready"] = @ \ mv, ![m]["ready"] = @ \cup mv]
  /\ UNCHANGED <<added, dropped, work, readers, merge, sstate, score, attempt,
                 head, stuckN, returnsN, result, made, gone, twice, open, op,
                 crashed, downs, ranks>>

\* The second write of a two-table step: the same process (Continue), or
\* repair after the process was cut (Crash).
Continue ==
  /\ op # NoOp /\ ~crashed
  /\ ApplyWork(op)
  /\ op' = NoOp
  /\ UNCHANGED <<added, fleet, readers, merge, sstate, mstatus, score, stuckN,
                 returnsN, result, made, gone, twice, crashed, downs, ranks>>

Crash ==
  /\ op # NoOp /\ ~crashed
  /\ crashed' = TRUE
  /\ UNCHANGED <<added, dropped, work, fleet, readers, merge, sstate, mstatus,
                 score, attempt, head, stuckN, returnsN, result, made, gone,
                 twice, open, op, downs, ranks>>

Repair ==
  /\ op # NoOp /\ crashed /\ ~Br("norepair")
  /\ ApplyWork(op)
  /\ op' = NoOp /\ crashed' = FALSE
  /\ UNCHANGED <<added, fleet, readers, merge, sstate, mstatus, score, stuckN,
                 returnsN, result, made, gone, twice, downs, ranks>>

\* ------------------------------------------------------------------ next

\* The decisions open to a judgment notification (section 8), as verbs.
Answer(n) ==
  /\ n \in open
  /\ CASE n.t = "failed"  -> Rework(n.p) \/ Drop(n.p)
       [] n.t \in {"broken", "reads"} ->
                             Rework(n.p) \/ Drop(n.p) \/ \E r \in Readers : AskAnother(n.p, r)
       [] n.t = "blocked" -> Drop(n.p)
       [] n.t = "stopped" -> Resume(n.s) \/ Return(n.p) \/ Drop(n.p)

MergeStep(s) ==
  \/ \E B \in SUBSET Primaries : MergeGreen(s, B)
  \/ \E p \in Primaries, why \in Causes : MergeStop(s, p, why)

Next ==
  \/ \E p \in Primaries :
       \/ Add(p) \/ Resolve(p) \/ Start(p) \/ Ask(p) \/ Accept(p) \/ Rework(p)
       \/ Drop(p) \/ Rank(p) \/ Return(p)
       \/ \E r \in Readers : AskAnother(p, r)
  \/ \E m \in Members :
       \/ FleetDown(m) \/ FleetUp(m)
       \/ \E c \in WorkCards : Take(m, c) \/ \E v \in {"ok", "failed"} : Finish(m, c, v)
  \/ \E r \in Readers, c \in ReadCards :
       ReadStart(r, c) \/ \E v \in {"ok", "broken"} : Read(r, c, v)
  \/ \E s \in Streams : MergeStep(s) \/ Resume(s)
  \/ Continue \/ Crash \/ Repair

Spec == Init /\ [][Next]_vars

\* Fairness: the mechanical moves run, workers and readers report, the merge
\* step is run with some fact, a down member eventually comes up, the second
\* write and repair run, the coordinator accepts what is acceptable and
\* answers every open judgment notification with one of its decisions.
\* A member going down, a cut, and rank are never forced.
Fairness ==
  /\ \A p \in Primaries :
       /\ WF_vars(Add(p)) /\ WF_vars(Resolve(p)) /\ WF_vars(Start(p))
       /\ WF_vars(Ask(p)) /\ WF_vars(Accept(p))
  /\ \A m \in Members :
       /\ WF_vars(\E c \in WorkCards : Take(m, c))
       /\ WF_vars(\E c \in WorkCards, v \in {"ok", "failed"} : Finish(m, c, v))
  /\ \A r \in Readers :
       /\ WF_vars(\E c \in ReadCards : ReadStart(r, c))
       /\ WF_vars(\E c \in ReadCards, v \in {"ok", "broken"} : Read(r, c, v))
  /\ \A s \in Streams : WF_vars(MergeStep(s))
  /\ WF_vars(\E m \in Members : FleetUp(m))
  /\ WF_vars(Continue) /\ WF_vars(Repair)
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

\* 2. Primaries in work working = primaries of work cards in fleet ready +
\* working. Only when no operation is pending.
WorkingMatchesFleet ==
  op = NoOp =>
    {p \in Primaries : InWork(p, "working")} = {c[2] : c \in Unfinished}

\* 3. Primaries with read cards in asked or reading are in review.
ReadsOnlyInReview ==
  \A c \in Outstanding : InWork(c[2], "review")

\* 4. Primaries in merge queued + stuck = primaries in work merging.
\* Only when no operation is pending.
MergeMatchesWork ==
  op = NoOp =>
    \A s \in Streams :
      merge[s]["queued"] \cup merge[s]["stuck"] = work[s]["merging"]

\* 5. Primaries in merge merged = primaries in work landed.
\* Only when no operation is pending.
MergedIsLanded ==
  op = NoOp => \A s \in Streams : merge[s]["merged"] = work[s]["landed"]

\* 6. No primary enters merging without ok read cards from two different
\* readers at its head (and they stay: landed keeps them).
AcceptedHadTwoReaders ==
  \A p \in Primaries :
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

\* 9. A stopped stream has an open judgment notification.
StoppedIsNotified ==
  \A s \in Streams :
    sstate[s] = "stopped" => \E n \in open : n.t = "stopped" /\ n.s = s

\* Section 7, 1: in work order, the head of the stream's queued cell first.
MergeInWorkOrder ==
  [][\A s \in Streams :
       \A b \in merge'[s]["merged"] \ merge[s]["merged"],
          q \in merge[s]["queued"] \ merge'[s]["merged"] :
         score[b] < score[q]]_vars

\* Section 10: a step cut short is finished.
CutRepaired == (op # NoOp) ~> (op = NoOp)

\* Every primary that is not dropped eventually lands.
EveryPrimaryEnds ==
  \A p \in Primaries : <>(InWork(p, "landed") \/ p \in dropped)

\* A stopped stream eventually moves.
StoppedStreamMoves ==
  \A s \in Streams : (sstate[s] = "stopped") ~> (sstate[s] # "stopped")

=============================================================================
