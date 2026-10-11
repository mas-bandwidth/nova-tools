------------------------------ MODULE Delivery ------------------------------
\* The present (docs/SPEC-FRIEND.md, "The present comes first"; pkg/friend/present.go:
\* PlanPresent, loop.presentOwed, loop.startPresent, loop.presentEnded). One friend's
\* stream as her daemon delivers it into her session. A session start (the daemon's start, a
\* new session id, her own "present" request) owes the present; so does a delivery after a
\* gap of Stale with something waiting, but only in batch mode, never for a passive harness,
\* and not before the first beat (the row's mode is not known). The present is one turn
\* carrying the newest note from the seat, and nothing older: every other message waiting is
\* acked superseded, a ping inside the challenge Window is answered by the daemon and kept
\* off the skipped line, and a ping past it is never answered. The note comes from the seat
\* alone; with the seat unknown no note goes and every message is superseded, and in one-shot
\* mode (no batch session) the note is superseded too. A batch present that fails is owed
\* again after Retry, carrying the same note. An ack of a superseded message may be lost; the
\* claim then hands it in again, and the daemon supersedes it once more (it is from before
\* the present).
\*
\* The state the code owns: pending (her stream's messages read and not acked), info (each
\* message's kind and the store's time it was sent), due (loop.presentDue), lastTurn
\* (loop.delivered), presentAt (loop.presentAt), pel (superseded messages whose ack was lost,
\* still in the group's pending list), retry (loop.presentRetry), carry (loop.presentCarry,
\* the note of a failed present), the seat (known or unknown), the delivery mode (batch or
\* one-shot), whether the harness is passive, and the first beat; and what reached the
\* session: delivered, superseded, answered, answeredLate, and last, the last turn (its kind,
\* whether it came after a gap, the messages it carried). The outside: the coordinator and
\* kin sending (Send), the clock (Tick), a session start (Restart), the first beat (Beat),
\* the claim handing a lost ack's message in again (Claim). What is below the grain: the
\* read loop's hand and dealt cards, the deliverer's own retry of a turn's message (FriendEnvelope),
\* and a passive harness reading its stream itself.
\*
\* Broken = "none" is the design. Every other value is a reversed witness:
\*   "nopresent"   a start or a gap delivers the backlog as a batch: GapCarriesThePresent
\*   "reclaim"     a superseded message the claim hands in again is delivered:
\*                 NoSupersededDelivered
\*   "answerstale" a ping past the challenge window is answered: NoStaleNonceAnswered
\*   "anysender"   a note not from the seat is carried: NoteFromSeat

EXTENDS Naturals, FiniteSets

CONSTANTS MaxMsgs, MaxTime, Stale, Window, Retry, Broken

Kinds == {"deal", "ping", "note", "kin"}
Ids == 1..MaxMsgs
NoInfo == [kind |-> "none", at |-> 0]

VARIABLES now, n, info, pending, due, lastTurn, presentAt, pel,
          delivered, superseded, answered, answeredLate, last,
          seat, mode, passive, beats, retry, carry

vars == <<now, n, info, pending, due, lastTurn, presentAt, pel,
          delivered, superseded, answered, answeredLate, last,
          seat, mode, passive, beats, retry, carry>>

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
  /\ seat \in BOOLEAN
  /\ mode \in {"batch", "one-shot"}
  /\ passive \in BOOLEAN
  /\ beats \in 0..1
  /\ retry \in 0..(MaxTime + Retry)
  /\ carry \in 0..MaxMsgs

Init ==
  /\ now = 0 /\ n = 0 /\ info = [i \in Ids |-> NoInfo]
  /\ pending = {} /\ due = TRUE \* the daemon's start is a session start
  /\ lastTurn = 0 /\ presentAt = 0 /\ pel = {}
  /\ delivered = {} /\ superseded = {} /\ answered = {} /\ answeredLate = FALSE
  /\ last = [kind |-> "none", gap |-> FALSE, msgs |-> {}]
  /\ seat \in BOOLEAN
  /\ mode \in {"batch", "one-shot"}
  /\ passive \in BOOLEAN
  /\ beats = 0
  /\ retry = 0
  /\ carry = 0

Fresh(i) == now < info[i].at + Window

\* the present is owed (loop.presentOwed): a session start (due) owes it, and in batch
\* mode a delivery after a gap of Stale with something waiting owes it; never for a
\* passive harness, never before the first beat in batch, and a failed present is owed
\* again only after Retry.
Owed ==
  /\ ~passive
  /\ now >= retry
  /\ \/ due /\ (mode = "one-shot" \/ beats > 0)
     \/ mode = "batch" /\ beats > 0 /\ pending # {} /\ now >= lastTurn + Stale

Send(k) ==
  /\ n < MaxMsgs
  /\ n' = n + 1
  /\ info' = [info EXCEPT ![n + 1] = [kind |-> k, at |-> now]]
  /\ pending' = pending \cup {n + 1}
  /\ UNCHANGED <<now, due, lastTurn, presentAt, pel, delivered, superseded, answered,
                answeredLate, last, seat, mode, passive, beats, retry, carry>>

Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ UNCHANGED <<n, info, pending, due, lastTurn, presentAt, pel, delivered, superseded,
                answered, answeredLate, last, seat, mode, passive, beats, retry, carry>>

\* a new session (the daemon's start, a new session id, her own "present" on the bus)
Restart ==
  /\ ~due
  /\ due' = TRUE
  /\ UNCHANGED <<now, n, info, pending, lastTurn, presentAt, pel, delivered, superseded,
                answered, answeredLate, last, seat, mode, passive, beats, retry, carry>>

\* the first beat: the row's mode is now known, so a batch session may owe the present
Beat ==
  /\ beats = 0
  /\ beats' = 1
  /\ UNCHANGED <<now, n, info, pending, due, lastTurn, presentAt, pel, delivered,
                superseded, answered, answeredLate, last, seat, mode, passive, retry, carry>>

\* a ping read with no present owed (loop.read): a fresh one is answered by the daemon, an
\* expired one dropped, never answered; acked either way. With the present owed it waits for it.
ReadPing(i) ==
  /\ i \in pending /\ info[i].kind = "ping"
  /\ ~Owed \/ Broken = "nopresent"
  /\ pending' = pending \ {i}
  /\ IF Fresh(i) \/ Broken = "answerstale"
       THEN /\ answered' = answered \cup {i}
            /\ answeredLate' = (answeredLate \/ ~Fresh(i))
       ELSE UNCHANGED <<answered, answeredLate>>
  /\ UNCHANGED <<now, n, info, due, lastTurn, presentAt, pel, delivered, superseded, last,
                seat, mode, passive, beats, retry, carry>>

\* the newest message of the given kinds in S (the note the present carries)
Newest(S, kinds) == {i \in S : info[i].kind \in kinds /\ \A j \in S : info[j].kind \in kinds => j <= i}

\* the note the present carries (PlanPresent): the newest note from the seat, taken only
\* while the seat is known; the anysender witness takes any note, from the seat or a kin
NoteSet(S) ==
  IF ~seat THEN {}
  ELSE IF Broken = "anysender"
       THEN Newest(S, {"note", "kin"})
       ELSE Newest(S, {"note"})

\* the present (loop.startPresent): one turn carrying the newest note from the seat; the
\* fresh pings answered; everything else acked superseded, some acks possibly lost (pel).
\* In one-shot mode the note is superseded too (no batch session carries it). A batch
\* present either succeeds (the note delivered) or fails: it is owed again after Retry,
\* carrying the same note (presentEnded).
Present ==
  /\ Owed /\ (pending # {} \/ carry # 0 \/ due)
  /\ Broken # "nopresent"
  /\ LET pool == pending \cup (IF carry = 0 THEN {} ELSE {carry})
         note == NoteSet(pool)
         fresh == {i \in pool : info[i].kind = "ping" /\ Fresh(i)}
         sup == pool \ (note \cup fresh)
     IN /\ \E lost \in SUBSET sup : pel' = pel \cup lost
        /\ superseded' = superseded \cup sup \cup (IF mode = "one-shot" THEN note ELSE {})
        /\ answered' = answered \cup fresh
        /\ pending' = {}
        /\ \/ mode = "one-shot"
               /\ delivered' = delivered
               /\ due' = FALSE
               /\ carry' = 0
               /\ retry' = retry
               /\ last' = [kind |-> "present", gap |-> Owed, msgs |-> {}]
           \/ mode = "batch"
               /\ delivered' = delivered \cup note
               /\ due' = FALSE
               /\ carry' = 0
               /\ retry' = retry
               /\ last' = [kind |-> "present", gap |-> Owed, msgs |-> note]
           \/ mode = "batch"
               /\ delivered' = delivered
               /\ due' = TRUE
               /\ retry' = now + Retry
               /\ carry' = IF note = {} THEN 0 ELSE CHOOSE x \in note : TRUE
               /\ last' = [kind |-> "present", gap |-> Owed, msgs |-> {}]
  /\ presentAt' = now /\ lastTurn' = now
  /\ UNCHANGED <<now, n, info, answeredLate, seat, mode, passive, beats>>

\* a turn with every message waiting (loop.startBatch), the pings read first
Batch ==
  /\ pending # {}
  /\ \A i \in pending : info[i].kind # "ping"
  /\ ~Owed \/ Broken = "nopresent"
  /\ delivered' = delivered \cup pending
  /\ last' = [kind |-> "batch", gap |-> Owed, msgs |-> pending]
  /\ pending' = {} /\ due' = FALSE /\ lastTurn' = now
  /\ UNCHANGED <<now, n, info, presentAt, pel, superseded, answered, answeredLate, seat,
                mode, passive, beats, retry, carry>>

\* the claim hands in a superseded message whose ack was lost: it is from before the present,
\* superseded again and acked (loop.read); "reclaim" takes it as new
Claim(i) ==
  /\ i \in pel
  /\ pel' = pel \ {i}
  /\ IF Broken = "reclaim" /\ info[i].kind # "ping"
       THEN pending' = pending \cup {i}
       ELSE UNCHANGED pending
  /\ UNCHANGED <<now, n, info, due, lastTurn, presentAt, delivered, superseded, answered,
                answeredLate, last, seat, mode, passive, beats, retry, carry>>

Next ==
  \/ \E k \in Kinds : Send(k)
  \/ Tick
  \/ Restart
  \/ Beat
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

\* the note the present carries is from the seat, never a kin's message
NoteFromSeat == last.kind = "present" => \A i \in last.msgs : info[i].kind = "note"
=============================================================================
