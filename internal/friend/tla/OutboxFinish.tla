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
   readers to be found broken the same way again. *)
EXTENDS FiniteSets, Naturals

CONSTANTS Cards, Others, ReadsAll, Reworked, HoldUnaddressed

VARIABLES col,     \* the server's word: "ready", "working", "done" (finished), "gone" (left her row)
          report,  \* outbox REPORT.md: "none", "noverdict", "verdict"
          seen,    \* her row as the daemon's last ask answered it
          sent,    \* the finishes the daemon sent that the server took, per card
          sentBad, \* the daemon sent a finish for a card its snapshot did not show working
          addr,    \* her report names the key words of the card's fix
          landed   \* the card was finished as a LAND, not --failed

vars == <<col, report, seen, sent, sentBad, addr, landed>>

Init == /\ col = [c \in Cards |-> "ready"]
        /\ report = [c \in Cards |-> "none"]
        /\ seen = [c \in Cards |-> "ready"]
        /\ sent = [c \in Cards |-> 0]
        /\ sentBad = FALSE
        /\ addr = [c \in Cards |-> FALSE]
        /\ landed = [c \in Cards |-> FALSE]

Work(c) == /\ col[c] = "ready"
           /\ col' = [col EXCEPT ![c] = "working"]
           /\ UNCHANGED <<report, seen, sent, sentBad, addr, landed>>

Write(c) == /\ col[c] = "working" /\ report[c] # "verdict"
            /\ \E r \in {"noverdict", "verdict"} : report' = [report EXCEPT ![c] = r]
            /\ \E a \in BOOLEAN : addr' = [addr EXCEPT ![c] = a]
            /\ UNCHANGED <<col, seen, sent, sentBad, landed>>

TakeBack(c) == /\ col[c] \in {"ready", "working"}
               /\ col' = [col EXCEPT ![c] = "gone"]
               /\ UNCHANGED <<report, seen, sent, sentBad, addr, landed>>

Ask == /\ seen' = col
       /\ UNCHANGED <<col, report, sent, sentBad, addr, landed>>

Mine(c) == ReadsAll \/ c \notin Others

\* a LAND is finished as a LAND unless its card has a fix its report does not address
Lands(c) == c \notin Reworked \/ addr[c] \/ ~HoldUnaddressed

\* the daemon's pass finishes card c; the server takes it only while c is working
Pass(c) == /\ seen[c] = "working" /\ report[c] = "verdict" /\ Mine(c)
           /\ sent[c] = 0 /\ col[c] # "done"
           /\ sentBad' = (sentBad \/ seen[c] # "working")
           /\ IF col[c] = "working"
                THEN /\ col' = [col EXCEPT ![c] = "done"]
                     /\ sent' = [sent EXCEPT ![c] = 1]
                     /\ landed' = [landed EXCEPT ![c] = Lands(c)]
                ELSE UNCHANGED <<col, sent, landed>>
           /\ UNCHANGED <<report, seen, addr>>

Next == \/ Ask
        \/ \E c \in Cards : Work(c) \/ Write(c) \/ TakeBack(c) \/ Pass(c)

Spec == Init /\ [][Next]_vars /\ WF_vars(Ask) /\ \A c \in Cards : WF_vars(Pass(c))

TypeOK == /\ col \in [Cards -> {"ready", "working", "done", "gone"}]
          /\ report \in [Cards -> {"none", "noverdict", "verdict"}]
          /\ seen \in [Cards -> {"ready", "working", "done", "gone"}]
          /\ sent \in [Cards -> 0..1]
          /\ sentBad \in BOOLEAN
          /\ addr \in [Cards -> BOOLEAN]
          /\ landed \in [Cards -> BOOLEAN]

\* a finish is sent only for a card the server said was working, from a report with a verdict
FinishOnlyWorking == ~sentBad
FinishOnlyVerdict == \A c \in Cards : col[c] = "done" => report[c] = "verdict"

\* a reworked card lands only from a report that addresses its fix: the same finding is
\* never sent round the readers twice
NoUnaddressedLand == \A c \in Reworked : landed[c] => addr[c]

\* every working card with a verdict in its report is finished, or leaves her row
Finished == \A c \in Cards : (col[c] = "working" /\ report[c] = "verdict") ~> col[c] # "working"
=============================================================================
