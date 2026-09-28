----------------------------- MODULE FirstConn -----------------------------
\* The connection redisconn.Open dials, as state. nova-tools
\* internal/redisconn/open.go at f6ec9e2b8: firstConn (Read :255, Write
\* :280, disarm :291), firstDial (dialer :194, done :209), the probe :180;
\* firstconn_test.go, whose seven events and three rules are this module's.
\*
\* Why it exists. go-redis shakes hands inside the first command on a
\* connection and offers no way to ask for the handshake alone, so Open sends
\* a probe (PING). A RESP3 map ('%') as the first byte from the store is
\* HELLO accepted; then, and only then, the probe is answered here (+PONG)
\* and never written to the store, so Open costs one exchange and not two.
\* Anything else the store says first, and any write that is not exactly the
\* probe, makes the connection inert: the probe travels and the store's own
\* answer decides. Once Open returns (disarm) no write is taken again.
\*
\* The state the code owns: state (watching, armed, answering, inert) and
\* given, the bytes of the answer read so far. The wire: sent, what the store
\* has sent and the client has not read; received, what the store received.
\* The outside events, the test's seven: the store sends a reply that begins
\* '%' (accepted) or '-' (refused), Rest bytes after its first; the client
\* reads with room to spare, or Room bytes at most; the client writes the
\* probe, or another command; Open returns. wroteAfter, taken, returned and
\* readAtTake are the rules' record of what went in; undue, allSent and got
\* of what the store received, sent, and the client read. overTaken, missed
\* and wasInert are history flags. The rules are stated on the records, not
\* on the states the code keeps, as the test states them.
\*
\* Bytes are tokens: "%" and "-" a reply's first byte, "x" the rest of a
\* reply, "A" a byte of the answer. A write is "probe" or "other", whole:
\* Write takes all of a write or none of it.
\*
\* Broken = "none" is the design. Every other value is a reversed witness, a
\* misimplementation one invariant or property must catch:
\*   "refused"    the store's first byte arms the connection whatever it is
\*                (:268 without the '%' test): the probe after a refused
\*                HELLO is answered here, and the refusal is never read as
\*                the probe's own answer
\*   "any"        an armed connection takes any write, not only the probe
\*                (:282 without bytes.Equal): a command of the caller's is
\*                answered PONG and never reaches the store
\*   "misaligned" a write that is not the probe leaves the connection armed
\*                (:285 missing): the probe after it is taken while the
\*                store's reply to that write is still to come, so every
\*                reply from then on is read for the wrong command
\*   "twice"      the connection goes back to armed once the answer is read
\*                (:261 storing armed, not inert): the caller's own PING
\*                after Open is answered here and never reaches the store
\*   "short"      a read of part of the answer ends it (:260 without the
\*                count): the rest of the answer is never read, and the
\*                store's next bytes are read in its place
\*   "late"       disarm leaves an armed connection armed (:293 missing): a
\*                probe written after Open returned is taken
\*   "hang"       the probe is taken and never answered (:257 reading the
\*                store instead): the client waits for a reply that never
\*                comes
\* None of the seven was in the code at f6ec9e2b8, whose tests hold the rules
\* over every order of the seven events up to six and over long orders. Each
\* is a misimplementation the model is shown to catch; each trace was read
\* against the lines named.
\*
\* Requests and replies alternate on a go-redis connection, so in Open the
\* client has read the whole of the store's reply before it writes the probe,
\* and the answer stands exactly where the store's would have (open.go
\* :244). Over arbitrary orders, as here and in the test, the answer comes
\* first and comes alone, before any of the store's bytes still unread;
\* AnswerStandsInPlace states that.
\*
\* The failure edge, classified (redisconn/explainer.go, installed by Open
\* after OpenReturns; no state or transition of this module). A failure of a
\* command after Open is one of three by where it stands against the command:
\* before anything of it was written (a dial refused, or the setup of a
\* connection dialed for it lost: Unreachable), the login refused
\* (AuthRefused), or after it was written (a reply lost: Unconfirmed, the
\* write may have committed). The hook reads the failure and rewrites the
\* error; it sends nothing, reads nothing and keeps no state of the
\* connection's (only, for one call, what failed inside it), so every
\* action here is as it was. Nor do Options.PoolSize, Options.DialTimeout,
\* Options.Password or Conn.Trips (a count of round trips, the probe taken
\* read from the firstConn's record of this module's taken) add one.
\*
\* Not here: a Read racing a Write (they alternate in go-redis; the code
\* uses compare-and-swap so a lost race is a travelled probe, never a wrong
\* answer), a write that resembles the probe without being it byte for byte,
\* the socket (firstSysConn), the dialer's part (opening, done, hangUp),
\* bounds and deadlines. Those are open_test.go.
EXTENDS Naturals, Sequences
CONSTANTS MaxEvents, Rest, AnswerLen, Room, Broken
ASSUME AnswerLen >= 1 /\ Room >= 1 /\ Rest >= 0 /\ MaxEvents >= 0

VARIABLES state, given,
          sent, received,
          wroteAfter, taken, returned, readAtTake,
          undue, allSent, got,
          overTaken, missed, wasInert,
          events
vars == <<state, given, sent, received, wroteAfter, taken, returned,
          readAtTake, undue, allSent, got, overTaken, missed, wasInert,
          events>>
code == <<state, given>>
rules == <<wroteAfter, taken, returned, readAtTake, undue>>
flags == <<overTaken, missed>>

Min(a, b) == IF a < b THEN a ELSE b
Reply(first) == <<first>> \o [i \in 1..Rest |-> "x"]
Answer(n) == [i \in 1..n |-> "A"]
Drop(s, n) == IF n >= Len(s) THEN <<>> ELSE SubSeq(s, n + 1, Len(s))
NotA(t) == t # "A"
NoA(s) == \A i \in 1..Len(s) : s[i] # "A"
WithoutA(s) == SelectSeq(s, NotA)
IsPrefix(p, s) == Len(p) <= Len(s) /\ SubSeq(s, 1, Len(p)) = p

\* The first byte the client read from the store, "none" until it read one.
FirstRead == LET r == WithoutA(got) IN IF r = <<>> THEN "none" ELSE Head(r)
\* Bytes of the answer the client has yet to read, by the rules.
Owed == IF ~taken \/ Len(got) - readAtTake >= AnswerLen
        THEN 0 ELSE AnswerLen - (Len(got) - readAtTake)

Init ==
 /\ state = "watching" /\ given = 0
 /\ sent = <<>> /\ received = <<>>
 /\ wroteAfter = FALSE /\ taken = FALSE /\ returned = FALSE /\ readAtTake = 0
 /\ undue = <<>> /\ allSent = <<>> /\ got = <<>>
 /\ overTaken = FALSE /\ missed = FALSE /\ wasInert = FALSE
 /\ events = 0

\* Sends, writes and Open's return are counted to a bound; reads are not,
\* since each consumes what it reads and a read with nothing to give does
\* not happen.
Event == events < MaxEvents /\ events' = events + 1
Remember == wasInert' = (wasInert \/ state = "inert")

\* The store sends a reply: HELLO accepted begins '%', refused begins '-'.
Send(first) ==
 /\ Event
 /\ sent' = sent \o Reply(first)
 /\ allSent' = allSent \o Reply(first)
 /\ Remember
 /\ UNCHANGED <<code, received, rules, got, flags>>
SendAccepted == Send("%")
SendRefused == Send("-")

\* The code's Read (:255) with room bytes of room: from the store, except
\* for the answer to a probe taken by Write, which is read from here.
FromStore(room) ==
 /\ sent # <<>>
 /\ LET n == Min(room, Len(sent)) IN
    /\ got' = got \o SubSeq(sent, 1, n)
    /\ sent' = Drop(sent, n)
Read(room) ==
 /\ \/ /\ state = "answering" /\ Broken # "hang"
       /\ LET n == Min(room, AnswerLen - given) IN
          /\ got' = got \o Answer(n)
          /\ given' = given + n
          /\ state' = CASE Broken = "short" -> "inert"
                        [] given + n < AnswerLen -> "answering"
                        [] Broken = "twice" -> "armed"
                        [] OTHER -> "inert"
       /\ UNCHANGED sent
    \/ /\ state = "watching"
       /\ FromStore(room)
       /\ state' = IF Head(sent) = "%" \/ Broken = "refused"
                   THEN "armed" ELSE "inert"
       /\ UNCHANGED given
    \/ /\ state \in {"armed", "inert"} \/ (state = "answering" /\ Broken = "hang")
       /\ FromStore(room)
       /\ UNCHANGED code
 /\ Remember
 /\ UNCHANGED <<received, rules, allSent, flags, events>>
\* Room to spare: everything there is to read.
ReadAll == Read(Len(sent) + AnswerLen)
ReadSome == Read(Room)

\* The rule (firstconn_test.go, "Taken only when accepted"): a write is due
\* to be taken when it is the probe, the first byte read from the store was
\* '%', nothing but the handshake was written before, no write was taken
\* before, and Open has not returned.
Due(kind) ==
 /\ kind = "probe" /\ FirstRead = "%"
 /\ ~wroteAfter /\ ~taken /\ ~returned
\* The code's Write (:280): the probe of an armed connection is taken.
Takes(kind) == state = "armed" /\ (kind = "probe" \/ Broken = "any")
Write(kind) ==
 /\ Event
 /\ IF Takes(kind)
    THEN /\ state' = "answering"
         /\ UNCHANGED received
    ELSE /\ received' = Append(received, kind)
         /\ state' = IF state = "armed" /\ Broken # "misaligned"
                     THEN "inert" ELSE state
 /\ IF Due(kind)
    THEN /\ taken' = TRUE /\ readAtTake' = Len(got)
         /\ UNCHANGED <<undue, wroteAfter>>
    ELSE /\ undue' = Append(undue, kind)
         /\ wroteAfter' = (wroteAfter \/ FirstRead # "none")
         /\ UNCHANGED <<taken, readAtTake>>
 /\ overTaken' = (overTaken \/ (Takes(kind) /\ ~Due(kind)))
 /\ missed' = (missed \/ (Due(kind) /\ ~Takes(kind)))
 /\ Remember
 /\ UNCHANGED <<given, sent, returned, allSent, got>>
WriteProbe == Write("probe")
WriteOther == Write("other")

\* Open returns: disarm (:291) makes the connection inert unless an answer
\* is still to be read.
OpenReturns ==
 /\ Event
 /\ returned' = TRUE
 /\ state' = CASE state = "watching" -> "inert"
               [] state = "armed" /\ Broken # "late" -> "inert"
               [] OTHER -> state
 /\ Remember
 /\ UNCHANGED <<given, sent, received, wroteAfter, taken, readAtTake, undue,
                allSent, got, flags>>

Reads == ReadAll \/ ReadSome
Next == SendAccepted \/ SendRefused \/ Reads \/ WriteProbe \/ WriteOther
        \/ OpenReturns
\* The client keeps reading while there is something to read: what the code
\* owes is that a read never waits on the store for the answer it gave.
Spec == Init /\ [][Next]_vars /\ WF_vars(Reads)

Tokens == {"%", "-", "x", "A"}
TypeOK ==
 /\ state \in {"watching", "armed", "answering", "inert"}
 /\ given \in 0..AnswerLen
 /\ sent \in Seq(Tokens) /\ allSent \in Seq(Tokens) /\ got \in Seq(Tokens)
 /\ received \in Seq({"probe", "other"}) /\ undue \in Seq({"probe", "other"})
 /\ wroteAfter \in BOOLEAN /\ taken \in BOOLEAN /\ returned \in BOOLEAN
 /\ readAtTake \in 0..Len(got)
 /\ overTaken \in BOOLEAN /\ missed \in BOOLEAN /\ wasInert \in BOOLEAN
 /\ events \in 0..MaxEvents

\* Taken only when accepted: no write is taken that was not due.
TakenOnlyWhenDue == ~overTaken
\* ...and when all of it holds, it is taken.
TakenWhenDue == ~missed
\* Everything else travels: the store receives every other write, whole and
\* in order.
TheRestTravels == received = undue
\* The client reads the store's bytes in order, none dropped, none repeated.
StoreBytesInOrder == IsPrefix(WithoutA(got), allSent)
\* The answer stands in the store's place: read whole, once, first and alone
\* after the taken write, and never otherwise.
AnswerStandsInPlace ==
 IF ~taken THEN NoA(got)
 ELSE LET a == Min(Len(got) - readAtTake, AnswerLen)
      IN /\ NoA(SubSeq(got, 1, readAtTake))
         /\ SubSeq(got, readAtTake + 1, readAtTake + a) = Answer(a)
         /\ NoA(Drop(got, readAtTake + AnswerLen))
\* Inert is for good.
InertStays == wasInert => state = "inert"

\* A taken write is answered: the client is never left waiting for it.
AnswerDelivered == taken ~> Owed = 0
=============================================================================
