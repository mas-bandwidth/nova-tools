-------------------------------- MODULE Bus --------------------------------
\* nova-bus: one inbox (the stream bus:<name> and its group), a sender, and a
\* hook that can crash anywhere, including between printing a line and acking
\* it, and is restarted (bus/main.go hook, runHook; bus/redis.go).
\*
\* The state the code owns, as Redis keeps it: sent, the entries appended
\* (ids 1..sent, XADD); delivered, the group's last-delivered id (XREADGROUP >
\* hands the next entry and adds it to pending); pend, the entries handed and
\* not acked (the group's pending list, which XREADGROUP 0 reads back); acked.
\* The hook's own state, lost on a crash: up, mode ("pending" until a read of
\* pending is empty, then "new"), cur (the entry in hand, 0 for none) and
\* stage ("read" or "printed"). The outside: printed, every id written to
\* stdout; badPrint, set when an id already acked is written again.
\*
\* A batch read (COUNT 100) is a run of single reads here: the hook handles a
\* batch one entry at a time, and a crash inside a batch leaves the rest
\* pending, which is the case of a crash after any single read.
\*
\* Broken = "none" is the design. Reversed witnesses:
\*   "ackfirst"  the entry is acked when read, then printed: AckedOnlyPrinted
\*               fails on the first read, in the state between the two
\*   "newfirst"  a restarted hook reads new entries and never its pending
\*               ones: an entry handed before a crash is never printed
EXTENDS Naturals, FiniteSets

CONSTANTS N, MaxCrash, Broken

VARIABLES sent, delivered, pend, acked, up, mode, cur, stage, crashes, printed, badPrint

vars == <<sent, delivered, pend, acked, up, mode, cur, stage, crashes, printed, badPrint>>
Ids == 1..N

TypeOK ==
  /\ sent \in 0..N /\ delivered \in 0..sent
  /\ pend \subseteq 1..delivered /\ acked \subseteq 1..delivered
  /\ up \in BOOLEAN /\ mode \in {"pending", "new"}
  /\ cur \in 0..N /\ stage \in {"read", "printed"}
  /\ crashes \in 0..MaxCrash /\ printed \subseteq Ids /\ badPrint \in BOOLEAN

Init ==
  /\ sent = 0 /\ delivered = 0 /\ pend = {} /\ acked = {}
  /\ up = TRUE /\ mode = "pending" /\ cur = 0 /\ stage = "read"
  /\ crashes = 0 /\ printed = {} /\ badPrint = FALSE

Send ==
  /\ sent < N
  /\ sent' = sent + 1
  /\ UNCHANGED <<delivered, pend, acked, up, mode, cur, stage, crashes, printed, badPrint>>

Min(S) == CHOOSE x \in S : \A y \in S : x <= y

\* Take m in hand; under "ackfirst" it is acked at once.
Take(m) ==
  /\ cur' = m /\ stage' = "read"
  /\ IF Broken = "ackfirst"
       THEN acked' = acked \cup {m} /\ pend' = (pend \cup {m}) \ {m}
       ELSE UNCHANGED acked

ReadPending ==
  /\ up /\ mode = "pending" /\ cur = 0
  /\ IF pend = {}
       THEN mode' = "new" /\ UNCHANGED <<pend, acked, cur, stage>>
       ELSE Take(Min(pend)) /\ UNCHANGED mode /\ (Broken /= "ackfirst" => UNCHANGED pend)
  /\ UNCHANGED <<sent, delivered, up, crashes, printed, badPrint>>

ReadNew ==
  /\ up /\ mode = "new" /\ cur = 0 /\ delivered < sent
  /\ delivered' = delivered + 1
  /\ Take(delivered + 1)
  /\ (Broken /= "ackfirst" => pend' = pend \cup {delivered + 1})
  /\ UNCHANGED <<sent, up, mode, crashes, printed, badPrint>>

Print ==
  /\ up /\ cur /= 0 /\ stage = "read"
  /\ printed' = printed \cup {cur}
  /\ badPrint' = (badPrint \/ cur \in acked)
  /\ IF Broken = "ackfirst"
       THEN cur' = 0 /\ UNCHANGED stage
       ELSE stage' = "printed" /\ UNCHANGED cur
  /\ UNCHANGED <<sent, delivered, pend, acked, up, mode, crashes>>

Ack ==
  /\ up /\ cur /= 0 /\ stage = "printed"
  /\ acked' = acked \cup {cur} /\ pend' = pend \ {cur}
  /\ cur' = 0 /\ stage' = "read"
  /\ UNCHANGED <<sent, delivered, up, mode, crashes, printed, badPrint>>

Crash ==
  /\ up /\ crashes < MaxCrash
  /\ up' = FALSE /\ cur' = 0 /\ stage' = "read" /\ crashes' = crashes + 1
  /\ UNCHANGED <<sent, delivered, pend, acked, mode, printed, badPrint>>

Restart ==
  /\ ~up
  /\ up' = TRUE /\ mode' = IF Broken = "newfirst" THEN "new" ELSE "pending"
  /\ UNCHANGED <<sent, delivered, pend, acked, cur, stage, crashes, printed, badPrint>>

Next == Send \/ ReadPending \/ ReadNew \/ Print \/ Ack \/ Crash \/ Restart

Spec == Init /\ [][Next]_vars
          /\ WF_vars(Send) /\ WF_vars(ReadPending) /\ WF_vars(ReadNew)
          /\ WF_vars(Print) /\ WF_vars(Ack) /\ WF_vars(Restart)

\* No entry is acked before it is printed.
AckedOnlyPrinted == acked \subseteq printed

\* After any restart, nothing acked is printed again (a printed, unacked
\* entry may be: at-least-once).
NoAckedReprint == ~badPrint

\* Every sent entry is printed at least once.
EveryPrinted == \A m \in Ids : <>(m \in printed)
=============================================================================
