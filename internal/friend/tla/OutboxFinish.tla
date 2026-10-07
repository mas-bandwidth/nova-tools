---------------------------- MODULE OutboxFinish ----------------------------
(* The daemon reads every outbox job (internal/friend/outbox.go,
   docs/SPEC-FRIEND.md "the daemon reads every outbox job"). A card is dealt
   onto her row, its brief written by the daemon or by another hand (friend
   sync, the coordinator's stopgap: Others); she writes her REPORT.md, first
   with no Verdict line perhaps, then with one; the card may leave her row
   with no finish (taken back). The daemon asks the server for her row (a
   snapshot) and then passes over her outbox: a job whose card was working in
   the snapshot and whose report has a verdict is finished, once; the server
   takes a finish only for a card still working on her row. ReadsAll = FALSE is
   the reversed witness: the daemon before this change, which finished only the
   cards it staged itself.
   A reworked card (Reworked) has a fix, THE ONE THING LEFT, the first line
   of its brief (internal/friend/rework.go); the report she writes addresses
   it or not (addr, its key words named or not), and the daemon finishes a
   LAND that does not as a HOLD (landed FALSE). HoldUnaddressed = FALSE is
   the reversed witness: the daemon before the-fix-is-the-first-line-of-the-
   next-brief.w1, which landed such a report and sent the card round its
   readers to be found broken the same way again.
   Friend sync (cmd/nova-sprint, friendBrief and friendFinish) is a second
   writer of the brief and a second finisher beside the daemon: each writes the
   brief only when none is there (NoReplace), so either may write it, and
   either may finish a working card with a verdict, the server taking the first.
   With SyncSame it writes the daemon's form (the fix first) and holds an
   unaddressed LAND by the daemon's check (friend.UnaddressedLand); SyncSame =
   FALSE is the reversed witness, friend sync before the-fix-is-the-first-line-
   of-the-next-brief.w2, which wrote the server's form and landed such a report
   whenever it got there before the daemon. *)
EXTENDS FiniteSets, Naturals

CONSTANTS Cards, Others, ReadsAll, Reworked, HoldUnaddressed, SyncSame

VARIABLES col,     \* the server's word: "ready", "working", "done" (finished), "gone" (left her row)
          report,  \* outbox REPORT.md: "none", "noverdict", "verdict"
          seen,    \* her row as the daemon's last ask answered it
          sent,    \* the finishes the daemon sent that the server took, per card
          sentBad, \* the daemon sent a finish for a card its snapshot did not show working
          addr,    \* her report names the key words of the card's fix
          landed,  \* the card was finished as a LAND, not --failed
          brief    \* inbox BRIEF.md: "none", "fixfirst" (THE ONE THING LEFT first), "server" (the fix under the start)

vars == <<col, report, seen, sent, sentBad, addr, landed, brief>>

Init == /\ col = [c \in Cards |-> "ready"]
        /\ report = [c \in Cards |-> "none"]
        /\ seen = [c \in Cards |-> "ready"]
        /\ sent = [c \in Cards |-> 0]
        /\ sentBad = FALSE
        /\ addr = [c \in Cards |-> FALSE]
        /\ landed = [c \in Cards |-> FALSE]
        /\ brief = [c \in Cards |-> "none"]

Work(c) == /\ col[c] = "ready"
           /\ col' = [col EXCEPT ![c] = "working"]
           /\ UNCHANGED <<report, seen, sent, sentBad, addr, landed, brief>>

\* the brief of a card dealt to her, written by whichever hand comes first, never over one there
Form(byDaemon) == IF byDaemon \/ SyncSame THEN "fixfirst" ELSE "server"
Deliver(c, byDaemon) == /\ col[c] \in {"ready", "working"} /\ brief[c] = "none"
                        /\ brief' = [brief EXCEPT ![c] = Form(byDaemon)]
                        /\ UNCHANGED <<col, report, seen, sent, sentBad, addr, landed>>

Write(c) == /\ col[c] = "working" /\ report[c] # "verdict" /\ brief[c] # "none"
            /\ \E r \in {"noverdict", "verdict"} : report' = [report EXCEPT ![c] = r]
            /\ \E a \in BOOLEAN : addr' = [addr EXCEPT ![c] = a]
            /\ UNCHANGED <<col, seen, sent, sentBad, landed, brief>>

TakeBack(c) == /\ col[c] \in {"ready", "working"}
               /\ col' = [col EXCEPT ![c] = "gone"]
               /\ UNCHANGED <<report, seen, sent, sentBad, addr, landed, brief>>

Ask == /\ seen' = col
       /\ UNCHANGED <<col, report, sent, sentBad, addr, landed, brief>>

Mine(c) == ReadsAll \/ c \notin Others

\* a LAND is finished as a LAND unless its card has a fix its report does not address
Lands(c) == c \notin Reworked \/ addr[c] \/ ~HoldUnaddressed
\* friend sync's rule: the daemon's own check when SyncSame, none before
SyncLands(c) == c \notin Reworked \/ addr[c] \/ ~SyncSame

\* the daemon's pass finishes card c; the server takes it only while c is working
Pass(c) == /\ seen[c] = "working" /\ report[c] = "verdict" /\ Mine(c)
           /\ sent[c] = 0 /\ col[c] # "done"
           /\ sentBad' = (sentBad \/ seen[c] # "working")
           /\ IF col[c] = "working"
                THEN /\ col' = [col EXCEPT ![c] = "done"]
                     /\ sent' = [sent EXCEPT ![c] = 1]
                     /\ landed' = [landed EXCEPT ![c] = Lands(c)]
                ELSE UNCHANGED <<col, sent, landed>>
           /\ UNCHANGED <<report, seen, addr, brief>>

\* friend sync finishes card c from its report, asking the server directly; the server takes it
\* only while c is working, so a card is finished once, by whichever comes first
SyncFinish(c) == /\ col[c] = "working" /\ report[c] = "verdict"
                 /\ col' = [col EXCEPT ![c] = "done"]
                 /\ landed' = [landed EXCEPT ![c] = SyncLands(c)]
                 /\ UNCHANGED <<report, seen, sent, sentBad, addr, brief>>

Next == \/ Ask
        \/ \E c \in Cards : \/ Work(c) \/ Write(c) \/ TakeBack(c) \/ Pass(c) \/ SyncFinish(c)
                           \/ \E byDaemon \in BOOLEAN : Deliver(c, byDaemon)

Spec == Init /\ [][Next]_vars /\ WF_vars(Ask) /\ \A c \in Cards : WF_vars(Pass(c))

TypeOK == /\ col \in [Cards -> {"ready", "working", "done", "gone"}]
          /\ report \in [Cards -> {"none", "noverdict", "verdict"}]
          /\ seen \in [Cards -> {"ready", "working", "done", "gone"}]
          /\ sent \in [Cards -> 0..1]
          /\ sentBad \in BOOLEAN
          /\ addr \in [Cards -> BOOLEAN]
          /\ landed \in [Cards -> BOOLEAN]
          /\ brief \in [Cards -> {"none", "fixfirst", "server"}]

\* a finish is sent only for a card the server said was working, from a report with a verdict
FinishOnlyWorking == ~sentBad
FinishOnlyVerdict == \A c \in Cards : col[c] = "done" => report[c] = "verdict"

\* a reworked card lands only from a report that addresses its fix: the same finding is
\* never sent round the readers twice
NoUnaddressedLand == \A c \in Reworked : landed[c] => addr[c]

\* a reworked card's brief opens with its fix, whichever hand wrote it
FixFirst == \A c \in Reworked : brief[c] # "server"

\* every working card with a verdict in its report is finished, or leaves her row
Finished == \A c \in Cards : (col[c] = "working" /\ report[c] = "verdict") ~> col[c] # "working"
=============================================================================
