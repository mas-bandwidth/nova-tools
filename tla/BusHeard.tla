------------------------------ MODULE BusHeard ------------------------------
\* What the bus has seen each name do, and what is heard of it (docs/SPEC-BUS.md,
\* bus-heard-activity; pkg/bus/heard.go: Standing, markReceived; the writes are
\* pkg/bus/bus.go Send, RecvKinds and Ack). A send marks its sender in the
\* send's own transaction (HSET bus2:heard:send <name> <store time>); a recv or
\* an ack by id marks its reader (HSET bus2:heard:recv <name> <store time>); a
\* mark is overwritten, so each hash holds one field per name for ever. A name
\* is HEARD when its friend daemon's proof is up and renewed under Fresh ago, or
\* it received under Fresh ago and its daemon has not said, under Fresh ago,
\* that the session did not answer. Nothing here gates delivery: a send lands
\* whoever is heard, and the NOTE and names are read off these marks.
\*
\* The state the code owns: sent[n] and recv[n], the two hashes' fields (the
\* store's clock at the last send / last recv or ack by n, NoMark before the
\* first), and proof[n] with proofAt[n], the friend daemon's word (pkg/friend
\* PushProver: "up" while the session answers its SESSION CHECK, "down" when it
\* does not, "none" before the daemon ever wrote one). The outside: clock, the
\* store's time (monotone); each name sending, receiving and acking at any time;
\* the daemon writing its word at any time. lastSend and lastRecv are ghosts:
\* when n really last sent / read, which the marks must equal.
\*
\* Broken = "none" is the design. Every other value is a reversed witness:
\*   "sendmarksrecv"  a send also marks its sender as having received, so a
\*                    friend who can send and not receive looks heard (the
\*                    finding of 2026-10-12, two friends in a row): RecvMarkIsReal
\*   "recvnomark"     a recv leaves the recv mark alone: RecvMakesHeard
\*   "recvmarkssent"  a recv marks the sender side too: SentMarkIsReal
\*   "sendcounts"     a send counts as hearing, so a name that only sends is
\*                    heard: OnlyProofOrRecvHears
\*   "ignoredown"     a recent recv outweighs the daemon's word that the
\*                    session did not answer (the daemon's own recv loop reads
\*                    for a session that is not there): DownOutweighsRecv
\*   "gaterefuses"    a send refuses a recipient that is not heard (v1.1.0's
\*                    gate, issue #5450): EverySendLands
\*   "gatestored"     a gated recv is judged on the stored marks, so a name that
\*                    never received can never make its first recv:
\*                    ReadingGateOnlyRefusesOnDown

EXTENDS Naturals

CONSTANTS Names, MaxClock, Fresh, Broken

ASSUME Fresh > 0 /\ Fresh <= MaxClock

VARIABLES clock, sent, recv, lastSend, lastRecv, proof, proofAt, refused

vars == <<clock, sent, recv, lastSend, lastRecv, proof, proofAt, refused>>

\* no mark yet: the field is not in the hash
NoMark == 99

Time == 0..MaxClock \cup {NoMark}

TypeOK ==
    /\ clock \in 0..MaxClock
    /\ sent \in [Names -> Time]
    /\ recv \in [Names -> Time]
    /\ lastSend \in [Names -> Time]
    /\ lastRecv \in [Names -> Time]
    /\ proof \in [Names -> {"none", "up", "down"}]
    /\ proofAt \in [Names -> Time]
    /\ refused \in BOOLEAN

\* a mark or a word is fresh while it is younger than Fresh ago (heard.go: fresh)
IsFresh(t) == t # NoMark /\ clock - t < Fresh

ProofUp(n) == proof[n] = "up" /\ IsFresh(proofAt[n])
ProofDown(n) == proof[n] = "down" /\ IsFresh(proofAt[n])

\* heard.go: Standing.Heard
Heard(n) ==
    \/ ProofUp(n)
    \/ /\ \/ IsFresh(recv[n])
          \/ (Broken = "sendcounts" /\ IsFresh(sent[n]))
       /\ (Broken = "ignoredown" \/ ~ProofDown(n))

\* heard.go: Standing.Deaf (names: it can send and cannot receive)
Deaf(n) == IsFresh(sent[n]) /\ ~Heard(n)

\* the gate of a gated recv: a recv is itself a receive (heard.go: HeardReading)
ReadingOK(n) == IF Broken = "gatestored" THEN Heard(n) ELSE ~ProofDown(n)

Init ==
    /\ clock = 0
    /\ sent = [n \in Names |-> NoMark]
    /\ recv = [n \in Names |-> NoMark]
    /\ lastSend = [n \in Names |-> NoMark]
    /\ lastRecv = [n \in Names |-> NoMark]
    /\ proof = [n \in Names |-> "none"]
    /\ proofAt = [n \in Names |-> NoMark]
    /\ refused = FALSE

Tick ==
    /\ clock < MaxClock
    /\ clock' = clock + 1
    /\ UNCHANGED <<sent, recv, lastSend, lastRecv, proof, proofAt, refused>>

\* Send: n sends to m. The message lands whoever is heard; the mark of n is in
\* the same transaction. With "gaterefuses" a recipient not heard refuses it.
Send(n, m) ==
    /\ n # m
    /\ IF Broken = "gaterefuses" /\ ~Heard(m)
       THEN /\ refused' = TRUE
            /\ UNCHANGED <<clock, sent, recv, lastSend, lastRecv, proof, proofAt>>
       ELSE /\ sent' = [sent EXCEPT ![n] = clock]
            /\ lastSend' = [lastSend EXCEPT ![n] = clock]
            /\ recv' = IF Broken = "sendmarksrecv" THEN [recv EXCEPT ![n] = clock] ELSE recv
            /\ UNCHANGED <<clock, lastRecv, proof, proofAt, refused>>

\* Recv and Ack: n reads. One HSET of the recv mark at the store's time.
Read(n) ==
    /\ lastRecv' = [lastRecv EXCEPT ![n] = clock]
    /\ recv' = IF Broken = "recvnomark" THEN recv ELSE [recv EXCEPT ![n] = clock]
    /\ sent' = IF Broken = "recvmarkssent" THEN [sent EXCEPT ![n] = clock] ELSE sent
    /\ UNCHANGED <<clock, lastSend, proof, proofAt, refused>>

Recv(n) == Read(n)
Ack(n) == Read(n)

\* the friend daemon writes its word (pkg/friend/pushproof.go: Step)
Daemon(n, word) ==
    /\ proof' = [proof EXCEPT ![n] = word]
    /\ proofAt' = [proofAt EXCEPT ![n] = clock]
    /\ UNCHANGED <<clock, sent, recv, lastSend, lastRecv, refused>>

Next ==
    \/ Tick
    \/ \E n, m \in Names : Send(n, m)
    \/ \E n \in Names : Recv(n) \/ Ack(n)
    \/ \E n \in Names : Daemon(n, "up") \/ Daemon(n, "down")

Spec == Init /\ [][Next]_vars

\* the marks are what the names really did: a send writes only the send mark,
\* a recv or an ack only the recv mark (SPEC-BUS.md, bus-heard-activity)
RecvMarkIsReal == \A n \in Names : recv[n] = lastRecv[n]
SentMarkIsReal == \A n \in Names : sent[n] = lastSend[n]

\* a name that read a moment ago is heard, unless its daemon says the session
\* did not answer
RecvMakesHeard == \A n \in Names : (IsFresh(lastRecv[n]) /\ ~ProofDown(n)) => Heard(n)

\* only a proof up or a recent real receive makes a name heard: sending never does
OnlyProofOrRecvHears == \A n \in Names : Heard(n) => (ProofUp(n) \/ IsFresh(lastRecv[n]))

\* the daemon's fresh word that the session did not answer outweighs a recent recv
DownOutweighsRecv == \A n \in Names : ProofDown(n) => ~Heard(n)

\* a deaf name sent lately and has neither a proof nor a recent receive to show
DeafMeansNoEars == \A n \in Names : Deaf(n) => (IsFresh(lastSend[n]) /\ ~ProofUp(n) /\ (~IsFresh(lastRecv[n]) \/ ProofDown(n)))

\* a gated recv is refused only by the daemon's fresh word, never for want of an
\* earlier recv (a first recv is what makes a name heard)
ReadingGateOnlyRefusesOnDown == \A n \in Names : ~ReadingOK(n) => ProofDown(n)

\* no send is ever refused for the want of a proof or a recv
EverySendLands == ~refused

\* the marks only move forward: the store's clock is monotone and a mark is
\* overwritten with it
MarksMonotone ==
    [][\A n \in Names : /\ (sent'[n] = NoMark \/ sent[n] = NoMark \/ sent'[n] >= sent[n])
                        /\ (recv'[n] = NoMark \/ recv[n] = NoMark \/ recv'[n] >= recv[n])]_vars
=============================================================================
