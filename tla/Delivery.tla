------------------------------ MODULE Delivery ------------------------------
\* The present (docs/SPEC-FRIEND.md, "The present comes first"; internal/friend/present.go:
\* PlanPresent, loop.presentOwed, loop.startPresent; daemon.go loop.read). One friend's
\* stream as her daemon delivers it into her session. A session start (the daemon's start, a
\* new session id, her own "present" request) owes the present; so does a delivery after a
\* gap of Stale with none. The present is one turn carrying the newest coordinator note and
\* nothing older: every other message waiting is acked superseded, a ping inside the
\* challenge Window is answered by the daemon and kept off the skipped line, and a ping past
\* it is never answered. An ack of a superseded message may be lost; the claim then hands it
\* in again, and the daemon supersedes it once more (it is from before the present).
\*
\* The state the code owns: pending (her stream's messages read and not acked), info (each
\* message's kind and the store's time it was sent), due (loop.presentDue), lastTurn
\* (loop.delivered), presentAt (loop.presentAt), pel (superseded messages whose ack was lost,
\* still in the group's pending list), and what reached the session: delivered, superseded,
\* answered, answeredLate, and last, the last turn (its kind, whether it came after a gap,
\* the messages it carried). The outside: the coordinator and kin sending (Send), the clock
\* (Tick), a session start (Restart), the claim handing a lost ack's message in again (Claim).
\*
\* Broken = "none" is the design. Every other value is a reversed witness:
\*   "nopresent"   a start or a gap delivers the backlog as a batch: GapCarriesThePresent
\*   "reclaim"     a superseded message the claim hands in again is delivered:
\*                 NoSupersededDelivered
\*   "answerstale" a ping past the challenge window is answered: NoStaleNonceAnswered

EXTENDS Naturals, FiniteSets

CONSTANTS MaxMsgs, MaxTime, Stale, Window, Broken

Kinds == {"deal", "ping", "note", "kin"}
Ids == 1..MaxMsgs
NoInfo == [kind |-> "none", at |-> 0]

VARIABLES now, n, info, pending, due, lastTurn, presentAt, pel,
          delivered, superseded, answered, answeredLate, last
vars == <<now, n, info, pending, due, lastTurn, presentAt, pel,
          delivered, superseded, answered, answeredLate, last>>

TypeOK ==
  /\ now \in 0..MaxTime
  /\ n \in 0..MaxMsgs
  /\ info \in [Ids -> [kind : Kinds \cup {"none"}, at : 0..MaxTime]]
  /\ pending \subseteq Ids
  /\ due \in BOOLEAN
  /\ lastTurn \in 0..MaxTime
  /\ presentAt \in 0..MaxTime
  /\ pel \subseteq Ids
  /\ delivered \subseteq Ids /\ superseded \subseteq Ids /\ answered \subseteq Ids
  /\ answeredLate \in BOOLEAN
  /\ last \in [kind : {"none", "batch", "present"}, gap : BOOLEAN, msgs : SUBSET Ids]

Init ==
  /\ now = 0 /\ n = 0 /\ info = [i \in Ids |-> NoInfo]
  /\ pending = {} /\ due = TRUE \* the daemon's start is a session start
  /\ lastTurn = 0 /\ presentAt = 0 /\ pel = {}
  /\ delivered = {} /\ superseded = {} /\ answered = {} /\ answeredLate = FALSE
  /\ last = [kind |-> "none", gap |-> FALSE, msgs |-> {}]

Fresh(i) == now < info[i].at + Window
Gap == due \/ now >= lastTurn + Stale

Send(k) ==
  /\ n < MaxMsgs
  /\ n' = n + 1
  /\ info' = [info EXCEPT ![n + 1] = [kind |-> k, at |-> now]]
  /\ pending' = pending \cup {n + 1}
  /\ UNCHANGED <<now, due, lastTurn, presentAt, pel, delivered, superseded, answered, answeredLate, last>>

Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ UNCHANGED <<n, info, pending, due, lastTurn, presentAt, pel, delivered, superseded, answered, answeredLate, last>>

\* a new session (the daemon's start, a new session id, her own "present" on the bus)
Restart ==
  /\ ~due
  /\ due' = TRUE
  /\ UNCHANGED <<now, n, info, pending, lastTurn, presentAt, pel, delivered, superseded, answered, answeredLate, last>>

\* a ping read with no present owed (loop.read): a fresh one is answered by the daemon, an
\* expired one dropped, never answered; acked either way. With the present owed it waits for it.
ReadPing(i) ==
  /\ i \in pending /\ info[i].kind = "ping"
  /\ ~due \/ Broken = "nopresent"
  /\ pending' = pending \ {i}
  /\ IF Fresh(i) \/ Broken = "answerstale"
       THEN /\ answered' = answered \cup {i}
            /\ answeredLate' = (answeredLate \/ ~Fresh(i))
       ELSE UNCHANGED <<answered, answeredLate>>
  /\ UNCHANGED <<now, n, info, due, lastTurn, presentAt, pel, delivered, superseded, last>>

\* the newest coordinator note waiting, the one the present carries (PlanPresent)
Newest(S) == {i \in S : info[i].kind = "note" /\ \A j \in S : info[j].kind = "note" => j <= i}

\* the present (loop.startPresent): one turn carrying the newest coordinator note; the fresh
\* pings answered; everything else acked superseded, some acks possibly lost (pel)
Present ==
  /\ Gap /\ (pending # {} \/ due)
  /\ Broken # "nopresent"
  /\ LET note == Newest(pending)
         fresh == {i \in pending : info[i].kind = "ping" /\ Fresh(i)}
         sup == pending \ (note \cup fresh)
     IN /\ \E lost \in SUBSET sup : pel' = pel \cup lost
        /\ superseded' = superseded \cup sup
        /\ answered' = answered \cup fresh
        /\ delivered' = delivered \cup note
        /\ last' = [kind |-> "present", gap |-> TRUE, msgs |-> note]
  /\ pending' = {} /\ due' = FALSE /\ presentAt' = now /\ lastTurn' = now
  /\ UNCHANGED <<now, n, info, answeredLate>>

\* a turn with every message waiting (loop.startBatch), the pings read first
Batch ==
  /\ pending # {}
  /\ \A i \in pending : info[i].kind # "ping"
  /\ ~Gap \/ Broken = "nopresent"
  /\ delivered' = delivered \cup pending
  /\ last' = [kind |-> "batch", gap |-> Gap, msgs |-> pending]
  /\ pending' = {} /\ due' = FALSE /\ lastTurn' = now
  /\ UNCHANGED <<now, n, info, presentAt, pel, superseded, answered, answeredLate>>

\* the claim hands in a superseded message whose ack was lost: it is from before the present,
\* superseded again and acked (loop.read); "reclaim" takes it as new
Claim(i) ==
  /\ i \in pel
  /\ pel' = pel \ {i}
  /\ IF Broken = "reclaim" /\ info[i].kind # "ping"
       THEN pending' = pending \cup {i}
       ELSE UNCHANGED pending
  /\ UNCHANGED <<now, n, info, due, lastTurn, presentAt, delivered, superseded, answered, answeredLate, last>>

Next ==
  \/ \E k \in Kinds : Send(k)
  \/ Tick
  \/ Restart
  \/ \E i \in Ids : ReadPing(i)
  \/ Present
  \/ Batch
  \/ \E i \in Ids : Claim(i)

Spec == Init /\ [][Next]_vars

\* the card's invariant: a delivery after a gap (a session start, or Stale with none) carries
\* the present, and no message the present superseded
GapCarriesThePresent == last.gap => (last.kind = "present" /\ last.msgs \cap superseded = {})

\* nothing superseded ever reaches the session
NoSupersededDelivered == delivered \cap superseded = {}

\* a nonce past the challenge window is never answered
NoStaleNonceAnswered == ~answeredLate
=============================================================================
