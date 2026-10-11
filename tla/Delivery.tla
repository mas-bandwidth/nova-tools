----------------------------- MODULE Delivery ------------------------------
\* The present (docs/SPEC-FRIEND.md, "The present comes first"; pkg/friend/present.go:
\* PlanPresent, presentOwed, startPresent; daemon.go loop.read). One friend's
\* stream as her daemon delivers it into her session. A session start (the daemon's start, a
\* new session id, her own "present" request) owes the present; so does a delivery after a
\* gap of Stale with none. The present is one turn carrying the newest coordinator note and
\* nothing older: every other message waiting is acked superseded, a ping inside the
\* challenge Window is answered by the daemon and kept off the skipped line, and a ping past
\* it is never answered. An ack of a superseded message may be lost; the claim then hands it
\* in again, and the daemon supersedes it once more (it is from before the present).
\*
\* The note the present carries is the newest note from the seat (the coordinator, read from
\* the sprint server; "none" while unknown) and no other: with the seat unknown no note is
\* carried and every message is superseded; a one-shot friend has no batch session, so her
\* present supersedes the note too and delivers no turn. A present whose turn fails is owed
\* again after RecheckEvery (one Tick here), carrying the same note. The gap rule (a delivery
\* after Stale) holds only in batch mode, never for a passive harness, and not before the
\* first beat has said her row's mode.
\*
\* The state the code owns: pending (her stream's messages read and not acked), info (each
\* message's kind, its sender and the store's time it was sent), due (loop.presentDue),
\* lastTurn (loop.delivered), presentAt (loop.presentAt), pel (superseded messages whose ack
\* was lost, still in the group's pending list), seat (the seat holder), mode (her delivery
\* mode, unknown until the first beat), retrying (a failed present is owed again after a
\* beat), and what reached the session: delivered, superseded, answered, answeredLate, and
\* last, the last turn (its kind, whether it came after a gap, the messages it carried). The
\* outside: the coordinator and kin sending (Send), the clock (Tick), a session start
\* (Restart), the first beat (Beat), the seat read (SeatRead), the claim handing a lost ack's
\* message in again (Claim).
\*
\* Broken = "none" is the design. Every other value is a reversed witness:
\*   "nopresent"   a start or a gap delivers the backlog as a batch: GapCarriesThePresent
\*   "reclaim"     a superseded message the claim hands in again is delivered:
\*                 NoSupersededDelivered
\*   "answerstale" a ping past the challenge window is answered: NoStaleNonceAnswered
\*   "anysender"   the newest note is taken from any sender, not the seat: NoteFromSeat

EXTENDS Naturals, FiniteSets

CONSTANTS MaxMsgs, MaxTime, Stale, Window, Passive, Broken

Sender == {"coord", "kin"}
Kinds == {"deal", "ping", "note", "kin"}
Ids == 1..MaxMsgs
NoInfo == [kind |-> "none", at |-> 0, from |-> "none"]

VARIABLES now, n, info, pending, due, lastTurn, presentAt, pel, seat, mode, retrying,
          delivered, superseded, answered, answeredLate, last
vars == <<now, n, info, pending, due, lastTurn, presentAt, pel, seat, mode, retrying,
          delivered, superseded, answered, answeredLate, last>>

TypeOK ==
  /\ now \in 0..MaxTime
  /\ n \in 0..MaxMsgs
  /\ info \in [Ids -> [kind : Kinds \cup {"none"}, at : 0..MaxTime, from : Sender \cup {"none"}]]
  /\ pending \subseteq Ids
  /\ due \in BOOLEAN
  /\ lastTurn \in 0..MaxTime
  /\ presentAt \in 0..MaxTime
  /\ pel \subseteq Ids
  /\ seat \in {"none"} \cup Sender
  /\ mode \in {"unknown", "batch", "oneshot"}
  /\ retrying \in BOOLEAN
  /\ delivered \subseteq Ids /\ superseded \subseteq Ids /\ answered \subseteq Ids
  /\ answeredLate \in BOOLEAN
  /\ last \in [kind : {"none", "batch", "present"}, gap : BOOLEAN, msgs : SUBSET Ids]

Init ==
  /\ now = 0 /\ n = 0 /\ info = [i \in Ids |-> NoInfo]
  /\ pending = {} /\ due = TRUE \* the daemon's start is a session start
  /\ lastTurn = 0 /\ presentAt = 0 /\ pel = {}
  /\ seat = "none" /\ mode = "unknown" /\ retrying = FALSE
  /\ delivered = {} /\ superseded = {} /\ answered = {} /\ answeredLate = FALSE
  /\ last = [kind |-> "none", gap |-> FALSE, msgs |-> {}]

Fresh(i) == now < info[i].at + Window

\* the present is owed: due (a session start or a failed present), or, in batch mode only
\* (and never for a passive harness, and once the first beat has said the row's mode), a
\* delivery after a gap of Stale with something waiting.
Owed ==
  /\ ~Passive /\ mode # "unknown" /\ ~retrying
  /\ due \/ (mode = "batch" /\ pending # {} /\ now >= lastTurn + Stale)

Send(k, f) ==
  /\ n < MaxMsgs
  /\ n' = n + 1
  /\ LET sender == IF k = "note" THEN f ELSE "kin"
     IN info' = [info EXCEPT ![n + 1] = [kind |-> k, at |-> now, from |-> sender]]
  /\ pending' = pending \cup {n + 1}
  /\ UNCHANGED <<now, due, lastTurn, presentAt, pel, seat, mode, retrying, delivered, superseded, answered, answeredLate, last>>

Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ retrying' = FALSE
  /\ UNCHANGED <<n, info, pending, due, lastTurn, presentAt, pel, seat, mode, delivered, superseded, answered, answeredLate, last>>

\* a new session (the daemon's start, a new session id, her own "present" on the bus)
Restart ==
  /\ ~due
  /\ due' = TRUE
  /\ UNCHANGED <<now, n, info, pending, lastTurn, presentAt, pel, seat, mode, retrying, delivered, superseded, answered, answeredLate, last>>

\* the first beat says her row's mode: batch (a turn per delivery) or one-shot (the lanes
\* hand each card, no batch session)
Beat ==
  /\ mode = "unknown"
  /\ mode' \in {"batch", "oneshot"}
  /\ UNCHANGED <<now, n, info, pending, due, lastTurn, presentAt, pel, seat, retrying, delivered, superseded, answered, answeredLate, last>>

\* the seat holder read from the sprint server; "none" while the read has not answered
SeatRead ==
  /\ seat = "none"
  /\ seat' \in Sender
  /\ UNCHANGED <<now, n, info, pending, due, lastTurn, presentAt, pel, mode, retrying, delivered, superseded, answered, answeredLate, last>>

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
  /\ UNCHANGED <<now, n, info, due, lastTurn, presentAt, pel, seat, mode, retrying, delivered, superseded, last>>

\* a message is from the seat (or, as a reversed witness, any sender will do)
FromSeat(i) == info[i].from = seat \/ Broken = "anysender"

\* the newest coordinator note from the seat, the one the present carries (PlanPresent)
Newest(S) == {i \in S : info[i].kind = "note" /\ FromSeat(i)
                  /\ \A j \in S : info[j].kind = "note" /\ FromSeat(j) => j <= i}

\* the present (loop.startPresent): one turn carrying the newest note from the seat; the fresh
\* pings answered; everything else acked superseded, some acks possibly lost (pel). A one-shot
\* friend has no batch session: the note is superseded too and no turn is delivered. A present
\* whose turn fails is owed again after RecheckEvery, carrying the same note.
Present ==
  /\ Owed
  /\ Broken # "nopresent"
  /\ LET note == Newest(pending)
         fresh == {i \in pending : info[i].kind = "ping" /\ Fresh(i)}
         keep == IF mode = "oneshot" THEN note ELSE {}
         sup == pending \ (note \cup fresh)
     IN /\ \E lost \in SUBSET (sup \cup keep) :
           /\ pel' = pel \cup lost
           /\ superseded' = superseded \cup sup \cup keep
           /\ answered' = answered \cup fresh
           /\ \/ /\ mode = "batch"
                 /\ \E fail \in BOOLEAN :
                     /\ IF fail
                          THEN /\ delivered' = delivered
                               /\ due' = TRUE
                               /\ retrying' = TRUE
                               /\ pending' = note
                               /\ UNCHANGED <<last, lastTurn, presentAt>>
                          ELSE /\ delivered' = delivered \cup note
                               /\ due' = FALSE
                               /\ retrying' = retrying
                               /\ pending' = {}
                               /\ last' = [kind |-> "present", gap |-> TRUE, msgs |-> note]
                               /\ lastTurn' = now
                               /\ presentAt' = now
              \/ /\ mode # "batch"
                 /\ delivered' = delivered
                 /\ due' = FALSE
                 /\ retrying' = retrying
                 /\ pending' = {}
                 /\ UNCHANGED <<last, lastTurn, presentAt>>
  /\ UNCHANGED <<now, n, info, answeredLate, seat, mode>>

\* a turn with every message waiting (loop.startBatch), the pings read first
Batch ==
  /\ mode = "batch"
  /\ pending # {}
  /\ \A i \in pending : info[i].kind # "ping"
  /\ (~due /\ now < lastTurn + Stale) \/ Broken = "nopresent"
  /\ delivered' = delivered \cup pending
  /\ last' = [kind |-> "batch", gap |-> (due \/ now >= lastTurn + Stale), msgs |-> pending]
  /\ pending' = {} /\ due' = FALSE /\ lastTurn' = now
  /\ UNCHANGED <<now, n, info, presentAt, pel, seat, mode, retrying, superseded, answered, answeredLate>>

\* the claim hands in a superseded message whose ack was lost: it is from before the present,
\* superseded again and acked (loop.read); "reclaim" takes it as new
Claim(i) ==
  /\ i \in pel
  /\ pel' = pel \ {i}
  /\ IF Broken = "reclaim" /\ info[i].kind # "ping"
       THEN pending' = pending \cup {i}
       ELSE UNCHANGED pending
  /\ UNCHANGED <<now, n, info, due, lastTurn, presentAt, delivered, superseded, answered, answeredLate, last, seat, mode, retrying>>

Next ==
  \/ \E k \in Kinds, f \in Sender : Send(k, f)
  \/ Tick
  \/ Restart
  \/ Beat
  \/ SeatRead
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

\* the note the present carries is from the seat
NoteFromSeat == last.kind = "present" => \A i \in last.msgs : info[i].from = seat
=============================================================================
