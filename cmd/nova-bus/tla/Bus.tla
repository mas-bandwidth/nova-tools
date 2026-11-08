-------------------------------- MODULE Bus --------------------------------
\* nova-bus: one inbox (the stream bus:<name> and its group), a sender, and a
\* hook that can crash anywhere, including between printing a line and acking
\* it, and is restarted (cmd/nova-bus/main.go hook, runHook; cmd/nova-bus/redis.go).
\*
\* The state the code owns, as Redis keeps it: sent, the entries appended
\* (ids 1..sent, XADD); delivered, the group's last-delivered id (XREADGROUP >
\* hands the next entry and adds it to pending); pend, the entries handed and
\* not acked (the group's pending list, which XREADGROUP 0 reads back); acked.
\* The hook's own state, lost on a crash: up, mode ("pending" until a read of
\* pending (XREADGROUP 0) is empty, then "new"), cur (the entry in hand, 0
\* for none) and stage ("read" or "printed"). The outside: printed, every id
\* delivered (its line written to stdout, or its wake answered ok); badPrint,
\* set when an id already acked is delivered again.
\*
\* The wake (cmd/nova-bus/wake.go): with Fail, a delivery may fail (FailWake, a wake
\* that surely did not answer), at most MaxFail times. The entry is not acked
\* and stays pending, and the failure ends the hook (runHook exits 1; the
\* service manager or the person restarts it), so nothing behind it is
\* delivered first and the next start delivers it from the pending list.
\* Restart is weakly fair, so EveryPrinted holds with Fail as without it.
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
\*   "ackfail"   a failed wake acks the entry: AckedOnlyPrinted fails, the
\*               entry is lost
\*   "noretry"   a failed wake does not end the hook, which reads past the
\*               entry (skip): with the hook up, nothing reads it again, and
\*               EveryPrinted fails
EXTENDS Naturals, FiniteSets

CONSTANTS N, MaxCrash, MaxFail, Broken, Fail

VARIABLES sent, delivered, pend, acked, up, mode, cur, stage, crashes, fails, skip, printed, badPrint

vars == <<sent, delivered, pend, acked, up, mode, cur, stage, crashes, fails, skip, printed, badPrint>>
Ids == 1..N

TypeOK ==
  /\ sent \in 0..N /\ delivered \in 0..sent
  /\ pend \subseteq 1..delivered /\ acked \subseteq 1..delivered
  /\ up \in BOOLEAN /\ mode \in {"pending", "new"}
  /\ cur \in 0..N /\ stage \in {"read", "printed"}
  /\ crashes \in 0..MaxCrash /\ fails \in 0..MaxFail /\ skip \subseteq Ids
  /\ printed \subseteq Ids /\ badPrint \in BOOLEAN

Init ==
  /\ sent = 0 /\ delivered = 0 /\ pend = {} /\ acked = {}
  /\ up = TRUE /\ mode = "pending" /\ cur = 0 /\ stage = "read"
  /\ crashes = 0 /\ fails = 0 /\ skip = {} /\ printed = {} /\ badPrint = FALSE

Send ==
  /\ sent < N
  /\ sent' = sent + 1
  /\ UNCHANGED <<delivered, pend, acked, up, mode, cur, stage, crashes, fails, skip, printed, badPrint>>

Min(S) == CHOOSE x \in S : \A y \in S : x <= y

\* Take m in hand; under "ackfirst" it is acked at once.
Take(m) ==
  /\ cur' = m /\ stage' = "read"
  /\ IF Broken = "ackfirst"
       THEN acked' = acked \cup {m} /\ pend' = (pend \cup {m}) \ {m}
       ELSE UNCHANGED acked

\* XREADGROUP 0: the oldest pending entry (skip is empty but under "noretry").
Unread == pend \ skip

ReadPending ==
  /\ up /\ mode = "pending" /\ cur = 0
  /\ IF Unread = {}
       THEN mode' = "new" /\ UNCHANGED <<pend, acked, cur, stage>>
       ELSE Take(Min(Unread)) /\ UNCHANGED mode
              /\ (Broken /= "ackfirst" => UNCHANGED pend)
  /\ UNCHANGED <<sent, delivered, up, crashes, fails, skip, printed, badPrint>>

ReadNew ==
  /\ up /\ mode = "new" /\ cur = 0 /\ delivered < sent
  /\ delivered' = delivered + 1
  /\ Take(delivered + 1)
  /\ (Broken /= "ackfirst" => pend' = pend \cup {delivered + 1})
  /\ UNCHANGED <<sent, up, mode, crashes, fails, skip, printed, badPrint>>

Print ==
  /\ up /\ cur /= 0 /\ stage = "read"
  /\ printed' = printed \cup {cur}
  /\ badPrint' = (badPrint \/ cur \in acked)
  /\ IF Broken = "ackfirst"
       THEN cur' = 0 /\ UNCHANGED stage
       ELSE stage' = "printed" /\ UNCHANGED cur
  /\ UNCHANGED <<sent, delivered, pend, acked, up, mode, crashes, fails, skip>>

\* A wake that surely failed: one receipt, no ack, the entry stays pending,
\* and the hook ends ("noretry": it reads on past the entry instead).
FailWake ==
  /\ Fail /\ up /\ cur /= 0 /\ stage = "read" /\ fails < MaxFail
  /\ cur' = 0 /\ fails' = fails + 1
  /\ IF Broken = "ackfail"
       THEN acked' = acked \cup {cur} /\ pend' = pend \ {cur}
       ELSE UNCHANGED <<acked, pend>>
  /\ IF Broken = "noretry"
       THEN skip' = skip \cup {cur} /\ UNCHANGED up
       ELSE up' = FALSE /\ UNCHANGED skip
  /\ UNCHANGED <<sent, delivered, mode, stage, crashes, printed, badPrint>>

Ack ==
  /\ up /\ cur /= 0 /\ stage = "printed"
  /\ acked' = acked \cup {cur} /\ pend' = pend \ {cur}
  /\ cur' = 0 /\ stage' = "read"
  /\ UNCHANGED <<sent, delivered, up, mode, crashes, fails, skip, printed, badPrint>>

Crash ==
  /\ up /\ crashes < MaxCrash
  /\ up' = FALSE /\ cur' = 0 /\ stage' = "read" /\ crashes' = crashes + 1
  /\ UNCHANGED <<sent, delivered, pend, acked, mode, fails, skip, printed, badPrint>>

Restart ==
  /\ ~up
  /\ up' = TRUE /\ mode' = (IF Broken = "newfirst" THEN "new" ELSE "pending") /\ skip' = {}
  /\ UNCHANGED <<sent, delivered, pend, acked, cur, stage, crashes, fails, printed, badPrint>>

Next == Send \/ ReadPending \/ ReadNew \/ Print \/ FailWake \/ Ack \/ Crash \/ Restart

Spec == Init /\ [][Next]_vars
          /\ WF_vars(Send) /\ WF_vars(ReadPending) /\ WF_vars(ReadNew)
          /\ WF_vars(Print) /\ WF_vars(Ack) /\ WF_vars(Restart)

\* No entry is acked before it is printed.
AckedOnlyPrinted == acked \subseteq printed

\* After any restart, nothing acked is printed again (a printed, unacked
\* entry may be: at-least-once).
NoAckedReprint == ~badPrint

\* Every sent entry is printed at least once, failed wakes included.
EveryPrinted == \A m \in Ids : <>(m \in printed)
=============================================================================
