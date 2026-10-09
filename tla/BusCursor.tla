----------------------------- MODULE BusCursor -----------------------------
\* The coordination bus read cursor, open list, send index, checkout lock,
\* and push protocol. Models internal/bus (cursor.go:1-25, :401-417 WriteCursor,
\* :611-612 ReadOpen; lock.go:1-30; conflict.go:41-55; protocol.go; git.go)
\* and docs/SPEC-BUS.md:57-74, :115 test 12.
\*
\* THE STATE.
\*   bus                  the notes on the bus as a sequence of commits, each
\*                        note addressed to a set of readers
\*   cursor               per reader lane the CURSOR: the commit last read to;
\*                        a REPLACE, where the further read wins on conflict
\*   open                 the OPEN list: notes shown and not yet answered
\*   index                the INDEX of notes a lane sent
\*   receipts             the lane's RECEIPTS record of answered notes
\*   lock                 the per-checkout lock: one holder at a time, the
\*                        second waits briefly then refuses
\*   advance              a run's acknowledgement flag (--advance)
\*   wroteWithoutAdvance  history flag: a run without --advance wrote state
\*   advancedWithoutAck   history flag: cursor moved without acknowledgement
\*   nearerCursorKept     history flag: conflict resolution kept ancestor cursor
\*
\* THE ACTIONS.
\*   Send                   a reader appends a note: a new commit, an INDEX line
\*   StartRun               a run acquires the checkout lock with its --advance flag
\*   SecondRunOnOneCheckout the second run waits briefly on a held lock, then refuses
\*   InboxWithoutAdvance    shows new notes, writes nothing: no CURSOR, OPEN,
\*                          RECEIPTS, INDEX, commit or push changes
\*   InboxAdvance           moves and pushes the cursor past the delivered and
\*                          acknowledged batch, adds notes to OPEN
\*   CrashMidBatch          a run dies after delivering part of a batch: cursor
\*                          stays at the last complete acknowledged batch
\*   ConcurrentPush         another bench moved the remote: fetch-rebase-retry
\*                          settles conflict, further read wins
\*   ReplyClose             removes an OPEN entry and records a receipt
\*
\* THE INVARIANTS AND LIVENESS.
\*   NoNoteSkipped: a note addressed to a reader at a commit past its cursor is
\*     delivered before the cursor passes it (SPEC-BUS.md:69-72).
\*   AdvanceOnlyPastAcknowledged: the cursor moves only with --advance and only
\*     to a commit whose every note was delivered and acknowledged
\*     (SPEC-BUS.md:115 test 12).
\*   NoWriteWithoutAdvance: a run without --advance leaves CURSOR, OPEN and INDEX
\*     as they were (SPEC-BUS.md:69-70).
\*   OpenIsShownNotAnswered: every OPEN entry was shown and is not answered
\*     (cursor.go:33, :611-612).
\*   FurtherReadWins: on two cursors for one reader the descendant is kept and
\*     the OPEN list of the same side (cursor.go:401-417 WriteCursor comment).
\*   OneRunPerCheckout: never two runs holding the lock of one checkout
\*     (lock.go:1-30).
\*   Liveness: under fairness, every note addressed to a reader is eventually
\*     in OPEN or answered.
\*
\* REVERSED WITNESSES.
\*   BrokenAdvanceWithoutAck: the cursor moves past a note not acknowledged
\*     (AdvanceOnlyPastAcknowledged and NoNoteSkipped fail).
\*   BrokenWriteWithoutAdvance: a plain inbox writes the cursor
\*     (NoWriteWithoutAdvance fails).
\*   BrokenNearerCursorWins: the conflict keeps the ancestor
\*     (FurtherReadWins fails).
EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS Readers, Checkouts, MaxCommits, Broken

None == 0
Commits == 1..MaxCommits

ASSUME None \notin Readers
ASSUME MaxCommits \in Nat /\ MaxCommits > 0

VARIABLES bus, cursor, open, index, receipts, lock, advance,
          wroteWithoutAdvance, advancedWithoutAck, nearerCursorKept

vars == <<bus, cursor, open, index, receipts, lock, advance,
          wroteWithoutAdvance, advancedWithoutAck, nearerCursorKept>>

TypeOK ==
    /\ bus \in Seq([id: Commits, sender: Readers, to: SUBSET Readers])
    /\ cursor \in [Readers -> 0..MaxCommits]
    /\ open \in [Readers -> SUBSET Commits]
    /\ index \in [Readers -> SUBSET Commits]
    /\ receipts \in [Readers -> SUBSET Commits]
    /\ lock \in [Checkouts -> [holder: Readers \cup {None}, waiting: BOOLEAN]]
    /\ advance \in [Checkouts -> BOOLEAN]
    /\ wroteWithoutAdvance \in BOOLEAN
    /\ advancedWithoutAck \in BOOLEAN
    /\ nearerCursorKept \in BOOLEAN

\* NoNoteSkipped: a note addressed to a reader at a commit past its cursor is
\* delivered before the cursor passes it (SPEC-BUS.md:69-72).
NoNoteSkipped ==
    \A r \in Readers :
        \A c \in 1..cursor[r] :
            (r \in bus[c].to) => (c \in open[r] \/ c \in receipts[r])

\* AdvanceOnlyPastAcknowledged: the cursor moves only with --advance and only
\* to a commit whose every note was delivered and acknowledged (SPEC-BUS.md:115 test 12).
AdvanceOnlyPastAcknowledged ==
    ~advancedWithoutAck

\* NoWriteWithoutAdvance: a run without --advance leaves CURSOR, OPEN and INDEX
\* as they were (SPEC-BUS.md:69-70).
NoWriteWithoutAdvance ==
    ~wroteWithoutAdvance

\* OpenIsShownNotAnswered: every OPEN entry was shown and is not answered
\* (cursor.go:33, :611-612).
OpenIsShownNotAnswered ==
    \A r \in Readers :
        \A n \in open[r] :
            /\ n \notin receipts[r]
            /\ n <= Len(bus)
            /\ r \in bus[n].to

\* FurtherReadWins: on two cursors for one reader the descendant (or the later
\* stamp) is kept and the OPEN list of the same side (cursor.go:401-417 WriteCursor).
FurtherReadWins ==
    ~nearerCursorKept

\* OneRunPerCheckout: never two runs holding the lock of one checkout (lock.go:1-30).
OneRunPerCheckout ==
    \A c \in Checkouts :
        lock[c].holder = None \/ lock[c].holder \in Readers

\* Liveness: under fairness, every note addressed to a reader is eventually
\* in OPEN or answered.
Liveness ==
    \A r \in Readers :
        \A c \in 1..MaxCommits :
            [] (c <= Len(bus) /\ r \in bus[c].to => <> (c \in open[r] \/ c \in receipts[r]))

Init ==
    /\ bus = <<>>
    /\ cursor = [r \in Readers |-> 0]
    /\ open = [r \in Readers |-> {}]
    /\ index = [r \in Readers |-> {}]
    /\ receipts = [r \in Readers |-> {}]
    /\ lock = [c \in Checkouts |-> [holder |-> None, waiting |-> FALSE]]
    /\ advance = [c \in Checkouts |-> FALSE]
    /\ wroteWithoutAdvance = FALSE
    /\ advancedWithoutAck = FALSE
    /\ nearerCursorKept = FALSE

Send(c, s, toSet) ==
    /\ lock[c].holder = None
    /\ Len(bus) < MaxCommits
    /\ toSet # {}
    /\ toSet \subseteq Readers
    /\ LET newId == Len(bus) + 1
           newNote == [id |-> newId, sender |-> s, to |-> toSet]
       IN
          /\ bus' = Append(bus, newNote)
          /\ index' = [index EXCEPT ![s] = @ \cup {newId}]
          /\ UNCHANGED <<cursor, open, receipts, lock, advance,
                         wroteWithoutAdvance, advancedWithoutAck, nearerCursorKept>>

StartRun(c, r, adv) ==
    /\ lock[c].holder = None
    /\ lock' = [lock EXCEPT ![c] = [holder |-> r, waiting |-> FALSE]]
    /\ advance' = [advance EXCEPT ![c] = adv]
    /\ UNCHANGED <<bus, cursor, open, index, receipts,
                   wroteWithoutAdvance, advancedWithoutAck, nearerCursorKept>>

SecondRunOnOneCheckout(c) ==
    /\ lock[c].holder # None
    /\ ~lock[c].waiting
    /\ lock' = [lock EXCEPT ![c] = [lock[c] EXCEPT !.waiting = TRUE]]
    /\ UNCHANGED <<bus, cursor, open, index, receipts, advance,
                   wroteWithoutAdvance, advancedWithoutAck, nearerCursorKept>>

SecondRunRefused(c) ==
    /\ lock[c].holder # None
    /\ lock[c].waiting
    /\ lock' = [lock EXCEPT ![c] = [lock[c] EXCEPT !.waiting = FALSE]]
    /\ UNCHANGED <<bus, cursor, open, index, receipts, advance,
                   wroteWithoutAdvance, advancedWithoutAck, nearerCursorKept>>

InboxWithoutAdvance(c) ==
    /\ lock[c].holder # None
    /\ advance[c] = FALSE
    /\ LET r == lock[c].holder
       IN
          IF Broken = "BrokenWriteWithoutAdvance" THEN
             /\ cursor' = [cursor EXCEPT ![r] = Len(bus)]
             /\ wroteWithoutAdvance' = TRUE
             /\ lock' = [lock EXCEPT ![c] = [holder |-> None, waiting |-> FALSE]]
             /\ UNCHANGED <<bus, open, index, receipts, advance,
                            advancedWithoutAck, nearerCursorKept>>
          ELSE
             /\ lock' = [lock EXCEPT ![c] = [holder |-> None, waiting |-> FALSE]]
             /\ UNCHANGED <<bus, cursor, open, index, receipts, advance,
                            wroteWithoutAdvance, advancedWithoutAck, nearerCursorKept>>

InboxAdvance(c, targetCommit) ==
    /\ lock[c].holder # None
    /\ advance[c] = TRUE
    /\ LET r == lock[c].holder
       IN
          /\ targetCommit > cursor[r]
          /\ targetCommit <= Len(bus)
          /\ LET newDelivered == {k \in (cursor[r] + 1)..targetCommit :
                                     r \in bus[k].to /\ k \notin receipts[r]}
             IN
                /\ cursor' = [cursor EXCEPT ![r] = targetCommit]
                /\ open' = [open EXCEPT ![r] = @ \cup newDelivered]
                /\ lock' = [lock EXCEPT ![c] = [holder |-> None, waiting |-> FALSE]]
                /\ UNCHANGED <<bus, index, receipts, advance,
                               wroteWithoutAdvance, advancedWithoutAck, nearerCursorKept>>

InboxAdvanceEmpty(c) ==
    /\ lock[c].holder # None
    /\ advance[c] = TRUE
    /\ cursor[lock[c].holder] = Len(bus)
    /\ lock' = [lock EXCEPT ![c] = [holder |-> None, waiting |-> FALSE]]
    /\ UNCHANGED <<bus, cursor, open, index, receipts, advance,
                   wroteWithoutAdvance, advancedWithoutAck, nearerCursorKept>>

CrashMidBatch(c, targetCommit) ==
    /\ lock[c].holder # None
    /\ advance[c] = TRUE
    /\ LET r == lock[c].holder
       IN
          /\ targetCommit > cursor[r]
          /\ targetCommit <= Len(bus)
          /\ lock' = [lock EXCEPT ![c] = [holder |-> None, waiting |-> FALSE]]
          /\ IF Broken = "BrokenAdvanceWithoutAck" THEN
                /\ cursor' = [cursor EXCEPT ![r] = targetCommit]
                /\ advancedWithoutAck' = TRUE
                /\ UNCHANGED <<bus, open, index, receipts, advance,
                               wroteWithoutAdvance, nearerCursorKept>>
             ELSE
                /\ UNCHANGED <<bus, cursor, open, index, receipts, advance,
                               wroteWithoutAdvance, advancedWithoutAck, nearerCursorKept>>

ConcurrentPush(c, candCommit) ==
    /\ lock[c].holder # None
    /\ advance[c] = TRUE
    /\ LET r == lock[c].holder
       IN
          /\ candCommit \in 1..Len(bus)
          /\ candCommit # cursor[r]
          /\ LET candOpen == {k \in 1..candCommit : r \in bus[k].to /\ k \notin receipts[r]}
                 descendantWins == candCommit >= cursor[r]
                 ancestorWins == candCommit <= cursor[r]
             IN
                /\ lock' = [lock EXCEPT ![c] = [holder |-> None, waiting |-> FALSE]]
                /\ IF Broken = "BrokenNearerCursorWins" THEN
                      /\ nearerCursorKept' = TRUE
                      /\ IF ancestorWins THEN
                            /\ cursor' = [cursor EXCEPT ![r] = candCommit]
                            /\ open' = [open EXCEPT ![r] = candOpen]
                         ELSE
                            /\ UNCHANGED <<cursor, open>>
                   ELSE
                      /\ UNCHANGED nearerCursorKept
                      /\ IF descendantWins THEN
                            /\ cursor' = [cursor EXCEPT ![r] = candCommit]
                            /\ open' = [open EXCEPT ![r] = candOpen]
                         ELSE
                            /\ UNCHANGED <<cursor, open>>
                /\ UNCHANGED <<bus, index, receipts, advance,
                               wroteWithoutAdvance, advancedWithoutAck>>

ReplyClose(r, n) ==
    /\ n \in open[r]
    /\ open' = [open EXCEPT ![r] = @ \ {n}]
    /\ receipts' = [receipts EXCEPT ![r] = @ \cup {n}]
    /\ UNCHANGED <<bus, cursor, index, lock, advance,
                   wroteWithoutAdvance, advancedWithoutAck, nearerCursorKept>>

Next ==
    \/ \E c \in Checkouts, s \in Readers, toSet \in (SUBSET Readers \ {{}}) :
          Send(c, s, toSet)
    \/ \E c \in Checkouts, r \in Readers, adv \in BOOLEAN :
          StartRun(c, r, adv)
    \/ \E c \in Checkouts :
          SecondRunOnOneCheckout(c)
    \/ \E c \in Checkouts :
          SecondRunRefused(c)
    \/ \E c \in Checkouts :
          InboxWithoutAdvance(c)
    \/ \E c \in Checkouts, target \in Commits :
          InboxAdvance(c, target)
    \/ \E c \in Checkouts :
          InboxAdvanceEmpty(c)
    \/ \E c \in Checkouts, target \in Commits :
          CrashMidBatch(c, target)
    \/ \E c \in Checkouts, cand \in Commits :
          ConcurrentPush(c, cand)
    \/ \E r \in Readers, n \in Commits :
          ReplyClose(r, n)

Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

=============================================================================
