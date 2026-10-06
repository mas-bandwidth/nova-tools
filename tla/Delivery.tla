------------------------------ MODULE Delivery ------------------------------
(* The outbox is the daemon's, never the session's turn (internal/friend/outbox.go,
   docs/SPEC-FRIEND.md "the outbox is the daemon's"). A card is working on her
   row; she writes its REPORT.md while her session, or the lane running the card,
   is busy or free, and may stay busy as long as it likes. The daemon steps on its
   own clock: each step it watches the outbox (a change may be missed: the watch
   is a stat each step, the poll is the bound), and passes when it saw a change or
   Poll steps went by with none; a pass finishes every report on a working card
   not finished before, marked in the state directory so a daemon that starts
   again sends it never twice; a report whose card left her row is superseded.
   age counts the daemon's steps a written report waits for its finish.
   LaneWaits = TRUE is the reversed witness: the pass before this change, which
   left a job a lane was running to the lane's end. *)
EXTENDS Naturals

CONSTANTS Cards,     \* the cards on her row
          Poll,      \* OutboxPoll in daemon steps
          LaneWaits  \* TRUE: a job a lane runs is left to the lane's end (the broken pass)

VARIABLES col,     \* the server's word: "working", "done" (finished), "gone" (left her row)
          report,  \* outbox REPORT.md: "none", "written"
          lane,    \* a lane's turn (the session) is running the card
          age,     \* daemon steps since the report was written, while it waits (capped)
          dirty,   \* a report was written since the last pass
          since,   \* daemon steps since the last pass
          marked,  \* the state directory's marks: finished or superseded
          sent     \* finishes the server took, per card

vars == <<col, report, lane, age, dirty, since, marked, sent>>

Waiting(c) == col[c] = "working" /\ report[c] = "written"

Init == /\ col = [c \in Cards |-> "working"]
        /\ report = [c \in Cards |-> "none"]
        /\ lane = [c \in Cards |-> FALSE]
        /\ age = [c \in Cards |-> 0]
        /\ dirty = FALSE
        /\ since = 0
        /\ marked = [c \in Cards |-> FALSE]
        /\ sent = [c \in Cards |-> 0]

\* the session, or a lane's turn on the card, goes busy or free: the environment's
Busy(c) == /\ lane' = [lane EXCEPT ![c] = ~lane[c]]
           /\ UNCHANGED <<col, report, age, dirty, since, marked, sent>>

\* she writes the report, mid-turn or not
Write(c) == /\ col[c] = "working" /\ report[c] = "none"
            /\ report' = [report EXCEPT ![c] = "written"]
            /\ age' = [age EXCEPT ![c] = 0]
            /\ dirty' = TRUE
            /\ UNCHANGED <<col, lane, since, marked, sent>>

\* the card leaves her row with no finish (taken back, dealt again elsewhere)
TakeBack(c) == /\ col[c] = "working"
               /\ col' = [col EXCEPT ![c] = "gone"]
               /\ UNCHANGED <<report, lane, age, dirty, since, marked, sent>>

\* the daemon starts again: its memory is gone, the marks stand, and its first step passes
Restart == /\ dirty' = TRUE /\ since' = Poll - 1
           /\ UNCHANGED <<col, report, lane, age, marked, sent>>

Takes(c) == /\ report[c] = "written" /\ ~marked[c]
            /\ (~LaneWaits \/ ~lane[c])

\* one daemon step: the clock, the watch (seen: whether the stat caught the change),
\* and the pass when it is due
Step == \E seen \in BOOLEAN :
          LET due == (dirty /\ seen) \/ since + 1 >= Poll
          IN /\ since' = IF due THEN 0 ELSE since + 1
             /\ dirty' = IF due THEN FALSE ELSE dirty
             /\ IF due
                  THEN /\ col' = [c \in Cards |-> IF Takes(c) /\ col[c] = "working" THEN "done" ELSE col[c]]
                       /\ sent' = [c \in Cards |-> IF Takes(c) /\ col[c] = "working" THEN sent[c] + 1 ELSE sent[c]]
                       /\ marked' = [c \in Cards |-> marked[c] \/ (report[c] = "written" /\ (Takes(c) \/ col[c] = "gone"))]
                  ELSE UNCHANGED <<col, sent, marked>>
             /\ age' = [c \in Cards |-> IF Waiting(c) /\ col'[c] = "working" THEN IF age[c] > Poll THEN age[c] ELSE age[c] + 1 ELSE 0]
             /\ UNCHANGED <<report, lane>>

Next == \/ Step \/ Restart
        \/ \E c \in Cards : Busy(c) \/ Write(c) \/ TakeBack(c)

Spec == Init /\ [][Next]_vars /\ WF_vars(Step)

TypeOK == /\ col \in [Cards -> {"working", "done", "gone"}]
          /\ report \in [Cards -> {"none", "written"}]
          /\ lane \in [Cards -> BOOLEAN]
          /\ age \in [Cards -> 0..Poll + 1]
          /\ dirty \in BOOLEAN
          /\ since \in 0..Poll
          /\ marked \in [Cards -> BOOLEAN]
          /\ sent \in [Cards -> 0..2]

\* a written report on a working card is finished within the poll bound, the session
\* or its lane busy or not
WrittenIsFinished == \A c \in Cards : Waiting(c) => age[c] <= Poll

\* never finished twice, across restarts too
FinishedOnce == \A c \in Cards : sent[c] <= 1

\* a finish only from a report, and a card that left her row is never finished
FinishOnlyWritten == \A c \in Cards : col[c] = "done" => report[c] = "written" /\ sent[c] = 1
=============================================================================
