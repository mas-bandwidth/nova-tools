------------------------------ MODULE Delivery ------------------------------
\* The present (docs/SPEC-FRIEND.md, "The present comes first"; pkg/friend/present.go:
\* PlanPresent, loop.presentOwed, loop.startPresent, loop.presentEnded; daemon.go loop.seat).
\* One friend's stream as her daemon delivers it into her session. A session start (the daemon's
\* start, a new session id, her own "present" request) owes the present; so does a delivery after
\* a gap of Stale with none, in batch mode only and never before the first beat, and a present
\* turn that did not end at exit 0 is owed again after RecheckEvery with the same note. A passive
\* harness reads her stream itself and takes no turn. The present is one turn carrying the newest
\* coordinator note from the seat and nothing older: every other message waiting is acked
\* superseded, a ping inside the challenge Window is answered by the daemon and kept off the
\* skipped line, and a ping past it is never answered. With the seat unknown no note goes and
\* every message is superseded; in one-shot mode the note is superseded too. An ack of a
\* superseded message may be lost; the claim then hands it in again, and the daemon supersedes it
\* once more (it is from before the present).
\*
\* The state the code owns: pending (her stream's messages read and not acked), info (each
\* message's kind, sender and the store's time it was sent), due (loop.presentDue), lastTurn
\* (loop.delivered), presentAt (loop.presentAt), retryAt (loop.presentRetry), seat (the seat
\* holder loop.seat reads), mode and passive (the friend row's delivery mode and the harness that
\* takes no turn), beats (her row's beats), pel (superseded messages whose ack was lost, still in
\* the group's pending list), and what reached the session: delivered, superseded, answered,
\* answeredLate, and last, the last turn (its kind, whether it came after a gap, the seat at the
\* time, the messages it carried). The outside: the coordinator and kin sending (Send), the clock
\* (Tick), a session start (Restart), the seat read (SeatRead), the mode read (ModeRead), a
\* passive harness (PassiveRead) and the claim handing a lost ack's message in again (Claim).
\*
\* Broken = "none" is the design. Every other value is a reversed witness:
\*   "nopresent"   a start or a gap delivers the backlog as a batch: GapCarriesThePresent
\*   "reclaim"     a superseded message the claim hands in again is delivered:
\*                 NoSupersededDelivered
\*   "answerstale" a ping past the challenge window is answered: NoStaleNonceAnswered
\*   "anysender"   the present carries a note that is not from the seat: NoteFromSeat

EXTENDS Naturals, FiniteSets

CONSTANTS MaxMsgs, MaxTime, Stale, Window, RecheckEvery, Broken

Senders == {"seat", "kin"}
Kinds == {"deal", "ping", "note", "kin"}
Ids == 1..MaxMsgs
NoInfo == [kind |-> "none", from |-> "seat", at |-> 0]

VARIABLES now, n, info, pending, due, lastTurn, presentAt, pel,
          delivered, superseded, answered, answeredLate, last,
          seat, mode, passive, beats, retryAt
vars == <<now, n, info, pending, due, lastTurn, presentAt, pel,
          delivered, superseded, answered, answeredLate, last,
          seat, mode, passive, beats, retryAt>>

TypeOK ==
  /\ now \in 0..MaxTime
  /\ n \in 0..MaxMsgs
  /\ info \in [Ids -> [kind : Kinds \cup {"none"}, from : Senders, at : 0..MaxTime]]
  /\ pending \subseteq Ids
  /\ due \in BOOLEAN
  /\ lastTurn \in 0..MaxTime
  /\ presentAt \in 0..MaxTime
  /\ pel \subseteq Ids
  /\ delivered \subseteq Ids /\ superseded \subseteq Ids /\ answered \subseteq Ids
  /\ answeredLate \in BOOLEAN
  /\ last \in [kind : {"none", "batch", "present"}, gap : BOOLEAN, seat : Senders \cup {"unknown"}, msgs : SUBSET Ids]
  /\ seat \in Senders \cup {"unknown"}
  /\ mode \in {"batch", "oneshot"}
  /\ passive \in BOOLEAN
  /\ beats \in 0..1
  /\ retryAt \in 0..MaxTime

Init ==
  /\ now = 0 /\ n = 0 /\ info = [i \in Ids |-> NoInfo]
  /\ pending = {} /\ due = TRUE \* the daemon's start is a session start
  /\ lastTurn = 0 /\ presentAt = 0 /\ pel = {}
  /\ delivered = {} /\ superseded = {} /\ answered = {} /\ answeredLate = FALSE
  /\ last = [kind |-> "none", gap |-> FALSE, seat |-> "seat", msgs |-> {}]
  /\ seat = "seat" /\ mode = "batch" /\ passive = FALSE /\ beats = 0 /\ retryAt = 0

\* a message sent at now, from one of the seat's circle
Send(k, s) ==
  /\ n < MaxMsgs
  /\ k \in Kinds /\ s \in Senders
  /\ n' = n + 1
  /\ info' = [info EXCEPT ![n + 1] = [kind |-> k, from |-> s, at |-> now]]
  /\ pending' = pending \cup {n + 1}
  /\ UNCHANGED <<now, due, lastTurn, presentAt, pel, delivered, superseded, answered, answeredLate, last,
                 seat, mode, passive, beats, retryAt>>

Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ UNCHANGED <<n, info, pending, due, lastTurn, presentAt, pel, delivered, superseded, answered, answeredLate, last,
                 seat, mode, passive, beats, retryAt>>

\* the seat holder read from the sprint server (loop.seat): a sender's name, or unknown
SeatRead(s) ==
  /\ s \in Senders \cup {"unknown"}
  /\ seat' = s
  /\ UNCHANGED <<now, n, info, pending, due, lastTurn, presentAt, pel, delivered, superseded, answered, answeredLate, last,
                 mode, passive, beats, retryAt>>

\* the friend's row read at her beat: the delivery mode
ModeRead(m) ==
  /\ m \in {"batch", "oneshot"}
  /\ mode' = m
  /\ UNCHANGED <<now, n, info, pending, due, lastTurn, presentAt, pel, delivered, superseded, answered, answeredLate, last,
                 seat, passive, beats, retryAt>>

\* a harness that reads the stream itself: no turn is taken
PassiveRead(p) ==
  /\ p \in BOOLEAN
  /\ passive' = p
  /\ UNCHANGED <<now, n, info, pending, due, lastTurn, presentAt, pel, delivered, superseded, answered, answeredLate, last,
                 seat, mode, beats, retryAt>>

\* her row's beat: before the first one the mode is not known and no present is owed
Beat ==
  /\ beats < 1
  /\ beats' = beats + 1
  /\ UNCHANGED <<now, n, info, pending, due, lastTurn, presentAt, pel, delivered, superseded, answered, answeredLate, last,
                 seat, mode, passive, retryAt>>

\* a new session (the daemon's start, a new session id, her own "present" on the bus)
Restart ==
  /\ ~due
  /\ due' = TRUE
  /\ UNCHANGED <<now, n, info, pending, lastTurn, presentAt, pel, delivered, superseded, answered, answeredLate, last,
                 seat, mode, passive, beats, retryAt>>

Fresh(i) == now < info[i].at + Window

\* the newest note from the seat, and no older: the note the present carries (PlanPresent);
\* with the seat unknown there is none, and "anysender" is the witness that takes any note
SeatNote(S) ==
  {i \in S : info[i].kind = "note" /\ info[i].from = seat
             /\ \A j \in S : (info[j].kind = "note" /\ info[j].from = seat) => j <= i}
AnyNote(S) ==
  {i \in S : info[i].kind = "note" /\ \A j \in S : info[j].kind = "note" => j <= i}
Newest(S) ==
  IF seat = "unknown" THEN {}
  ELSE IF Broken = "anysender" THEN AnyNote(S)
  ELSE SeatNote(S)

\* a delivery after a gap: a session start, or (in batch mode only) Stale with none. A passive
\* harness takes no turn, and no present is owed before the first beat answers the row (presentOwed)
Gap == due \/ (mode = "batch" /\ now >= lastTurn + Stale)
PresentOwed == ~passive /\ beats > 0 /\ now >= retryAt /\ Gap

\* a ping read with no present owed (loop.read): a fresh one is answered by the daemon, an
\* expired one dropped, never answered; acked either way. With the present owed it waits for it.
ReadPing(i) ==
  /\ i \in pending /\ info[i].kind = "ping"
  /\ ~passive
  /\ ~PresentOwed \/ Broken = "nopresent"
  /\ pending' = pending \ {i}
  /\ IF Fresh(i) \/ Broken = "answerstale"
       THEN /\ answered' = answered \cup {i}
            /\ answeredLate' = (answeredLate \/ ~Fresh(i))
       ELSE UNCHANGED <<answered, answeredLate>>
  /\ UNCHANGED <<now, n, info, due, lastTurn, presentAt, pel, delivered, superseded, last,
                 seat, mode, passive, beats, retryAt>>

\* the present (loop.startPresent): one turn carrying the newest coordinator note; the fresh pings
\* answered; everything else acked superseded, some acks possibly lost (pel). In one-shot mode
\* there is no batch session to carry the note, so it is superseded too.
Present ==
  /\ PresentOwed /\ (pending # {} \/ due)
  /\ Broken # "nopresent"
  /\ LET note == IF mode = "oneshot" THEN {} ELSE Newest(pending)
         fresh == {i \in pending : info[i].kind = "ping" /\ Fresh(i)}
         sup == pending \ (note \cup fresh)
     IN /\ \E lost \in SUBSET sup : pel' = pel \cup lost
        /\ superseded' = superseded \cup sup
        /\ answered' = answered \cup fresh
        /\ delivered' = delivered \cup note
        /\ last' = [kind |-> "present", gap |-> TRUE, seat |-> seat, msgs |-> note]
  /\ pending' = {} /\ due' = FALSE /\ presentAt' = now /\ lastTurn' = now /\ retryAt' = 0
  /\ UNCHANGED <<now, n, info, answeredLate, seat, mode, passive, beats>>

\* a present turn that does not end at exit 0 (loop.presentEnded): the note is not delivered, the
\* rest is superseded all the same, and the present is owed again after RecheckEvery with the
\* same note still pending
PresentFail ==
  /\ PresentOwed /\ (pending # {} \/ due)
  /\ Broken # "nopresent"
  /\ now + RecheckEvery <= MaxTime
  /\ LET note == IF mode = "oneshot" THEN {} ELSE Newest(pending)
         fresh == {i \in pending : info[i].kind = "ping" /\ Fresh(i)}
         sup == pending \ (note \cup fresh)
     IN /\ \E lost \in SUBSET sup : pel' = pel \cup lost
        /\ superseded' = superseded \cup sup
        /\ answered' = answered \cup fresh
        /\ pending' = note
        /\ due' = TRUE /\ retryAt' = now + RecheckEvery
  /\ UNCHANGED <<now, n, info, lastTurn, presentAt, delivered, answeredLate, last,
                 seat, mode, passive, beats>>

\* a turn with every message waiting (loop.startBatch), the pings read first; a one-shot friend's
\* lanes hand the cards, so the batch turn is batch mode's alone
Batch ==
  /\ ~passive /\ mode = "batch"
  /\ pending # {}
  /\ \A i \in pending : info[i].kind # "ping"
  /\ ~PresentOwed \/ Broken = "nopresent"
  /\ delivered' = delivered \cup pending
  /\ last' = [kind |-> "batch", gap |-> PresentOwed, seat |-> seat, msgs |-> pending]
  /\ pending' = {} /\ due' = FALSE /\ lastTurn' = now
  /\ UNCHANGED <<now, n, info, presentAt, pel, superseded, answered, answeredLate,
                 seat, mode, passive, beats, retryAt>>

\* the claim hands in a superseded message whose ack was lost: it is from before the present,
\* superseded again and acked (loop.read); "reclaim" takes it as new
Claim(i) ==
  /\ i \in pel
  /\ pel' = pel \ {i}
  /\ IF Broken = "reclaim" /\ info[i].kind # "ping"
       THEN pending' = pending \cup {i}
       ELSE UNCHANGED pending
  /\ UNCHANGED <<now, n, info, due, lastTurn, presentAt, delivered, superseded, answered, answeredLate, last,
                 seat, mode, passive, beats, retryAt>>

Next ==
  \/ \E k \in Kinds : \E s \in Senders : Send(k, s)
  \/ Tick
  \/ \E s \in Senders \cup {"unknown"} : SeatRead(s)
  \/ \E m \in {"batch", "oneshot"} : ModeRead(m)
  \/ \E p \in BOOLEAN : PassiveRead(p)
  \/ Beat
  \/ Restart
  \/ \E i \in Ids : ReadPing(i)
  \/ Present
  \/ PresentFail
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

\* the present carries a note from the seat that was read when it was planned, and nothing else
NoteFromSeat == last.kind = "present" => (\A i \in last.msgs : info[i].from = last.seat)
=============================================================================
