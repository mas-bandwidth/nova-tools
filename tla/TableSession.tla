--------------------------- MODULE TableSession ---------------------------
\* nova-table shell as state: one process reading one command per line over
\* one connection. nova-tools#4458 at ed959e1a3: cmd/nova-table/session.go
\* (cmdShell, readCommands), watch.go (cmdWatch, watchLoop), main.go (client,
\* storeRefusal); go-redis v9.22.0 internal/pool/pool.go (dialConn, tryDial).
\*
\* The state the session owns: the lines not yet read (input), whether it
\* goes on after a failed line (keep: --keep-going), where the reader is (pc:
\* at a line, in a verb, in a watch, ended), the line in hand (cur), its
\* connection (conn: none, live, or dead, a connection the store has closed
\* and the session has not used since), the dial error the pool keeps (kept),
\* the exit code so far (exit) and why the session ended (how). The outside:
\* the store (up; refusing, nothing listens at the address; gone, the socket
\* path is not there), a reply lost on its way back, and two signals.
\* stopped, high, afterStop, afterFail, falseAlarm, wrongCode and twice are
\* history, kept so the invariants can say what happened and not only what
\* is.
\*
\* The signals (Stella, stella-ba91222b58ce). SIGTERM is a stop wherever it
\* arrives: the session ends and no line follows. SIGINT inside a watch is
\* not a stop: it is how a watch is left, and the reader goes on to the next
\* line. SIGINT at the prompt or inside a verb is a stop. A session ended by
\* a stop reports the signal (143, 130), not the codes of its lines.
\*
\* The reader waits for input as long as the outside likes: a shell left
\* open at its prompt is not stuck. So the liveness here is only of what
\* the session itself owes: a verb ends, a watch is left when asked, a stop
\* ends the session.
\*
\* A line is one of: "ok" (a verb the store takes, code 0), "no" (a verb the
\* store refuses, code 1), "usage" (refused before the store, code 2),
\* "long" (longer than a line may be, code 2), "watch" (draws until a stop
\* signal), "quit".
\*
\* Broken = "none" is the design. Every other value is a reversed witness, a
\* misimplementation one invariant must catch:
\*   "term"   SIGTERM during a watch ends the watch, and the session reads
\*            the next line (ed959e1a3: watch.go:75 takes both signals
\*            alike, watchLoop returns 0, session.go:126 goes on)
\*   "stale"  a line answers from the pool's kept dial error and dials
\*            nothing (ed959e1a3: session.go:88 opens a pool of one;
\*            pool.go:692 returns the kept error until tryDial's probe, once
\*            a second, succeeds)
\*   "class"  a store that is gone gives code 1 (ed959e1a3: main.go:303
\*            knows "connection refused" and not "no such file or
\*            directory"; README:410 promises 2)
\*   "long"   a line too long ends the session, --keep-going or not
\*            (ed959e1a3: session.go:135, the scanner cannot go on)
\*   "replay" a write whose reply was lost is sent again (ed959e1a3:
\*            session.go:88 opens with go-redis's own command retries, and
\*            error.go shouldRetry answers true for io.EOF on the reply;
\*            found by Stella, stella-db2749edae17: code 0 and a second
\*            receipt)
\*   "on"     the session goes on after a failed line without --keep-going
\*   "last"   the exit code is the last line's, not the highest
\*   "int"    SIGINT inside a watch ends the session
\* The first five are defects of the code at ed959e1a3, each reproduced on a
\* store and read against the lines named. The last three are
\* misimplementations the invariants are shown to catch.
\*
\* What a stop does to a verb in flight is left open on purpose: the verb
\* may finish, or the process may end inside it (the store's verb is one
\* call, whole or not at all). What is fixed: no line starts after it.
\*
\* Not here: words and quoting (shellWords), the prompt, the byte limit's own
\* boundary (a line of exactly the limit is refused at ed959e1a3), the entry
\* defaults, receipts, what a verb does to a table (TableEdit, TableOrder), a
\* login the store refuses, a store that accepts and never answers (the
\* reader measured 10 s a line: a bound, not a state). Those are functional
\* tests.
EXTENDS Naturals, Sequences
CONSTANTS MaxLines, Broken
Kinds == {"ok", "no", "usage", "long", "watch", "quit"}
Inputs == UNION {[1..n -> Kinds] : n \in 0..MaxLines}
Down == {"refusing", "gone"}
NoLine == "-"
NoEnd == "-"
VARIABLES input, keep, pc, cur, conn, kept, store, pending, exit, how,
          stopped, high, afterStop, afterFail, falseAlarm, wrongCode, twice
vars == <<input, keep, pc, cur, conn, kept, store, pending, exit, how,
          stopped, high, afterStop, afterFail, falseAlarm, wrongCode, twice>>
outside == <<store, pending, stopped>>
Signals == {"int", "term"}
link == <<conn, kept, falseAlarm, wrongCode, twice>>
reader == <<input, keep, afterStop, afterFail>>
Max(a, b) == IF a > b THEN a ELSE b

Init ==
 /\ input \in Inputs
 /\ keep \in BOOLEAN
 /\ pc = "read" /\ cur = NoLine
 \* entering the shell dials nothing: the first verb is the probe
 /\ conn = "none" /\ kept = "none"
 /\ store \in {"up"} \cup Down
 /\ pending = "none" /\ stopped = FALSE
 /\ exit = 0 /\ high = 0 /\ how = NoEnd
 /\ afterStop = FALSE /\ afterFail = FALSE
 /\ falseAlarm = FALSE /\ wrongCode = FALSE /\ twice = FALSE

\* The line in hand ends with a code: readCommands' `if code > result` and
\* `if code != 0 && !keepGoing`.
Finish(code) ==
 /\ exit' = IF Broken = "last" THEN code ELSE Max(exit, code)
 /\ high' = Max(high, code)
 /\ cur' = NoLine
 /\ IF code # 0 /\ ~keep /\ Broken # "on"
    THEN pc' = "done" /\ how' = "fail"
    ELSE pc' = "read" /\ how' = how

\* A line is read. A signal that is pending at the prompt ends the session
\* (Stop); it is never followed by a read.
ReadLine ==
 /\ pc = "read" /\ input # <<>> /\ pending = "none"
 /\ input' = Tail(input)
 /\ afterStop' = (afterStop \/ stopped)
 /\ afterFail' = (afterFail \/ (high # 0 /\ ~keep))
 /\ LET k == Head(input)
    IN CASE k = "quit" ->
             /\ pc' = "done" /\ how' = "quit"
             /\ UNCHANGED <<cur, exit, high>>
         [] k = "usage" -> Finish(2)
         [] k = "long" ->
             IF Broken = "long"
             THEN /\ pc' = "done" /\ how' = "long"
                  /\ exit' = 2 /\ high' = 2 /\ cur' = NoLine
             ELSE Finish(2)
         [] OTHER ->
             /\ pc' = IF k = "watch" THEN "watch" ELSE "verb"
             /\ cur' = k
             /\ UNCHANGED <<exit, high, how>>
 /\ UNCHANGED <<keep, outside, link>>

EndOfInput ==
 /\ pc = "read" /\ input = <<>> /\ pending = "none"
 /\ pc' = "done" /\ how' = "eof"
 /\ UNCHANGED <<cur, exit, high, reader, outside, link>>

Code(k) == IF k = "ok" THEN 0 ELSE 1
\* storeRefusal: a store that could not be reached is 2.
ConnCode(kind) == IF Broken = "class" /\ kind = "gone" THEN 1 ELSE 2

\* The verb reached the store on a whole connection. Its answer comes back,
\* or is lost on the way. Lost, the line ends with code 2 and the connection
\* is dead; the verb is not sent again, because it may have been done.
Answered ==
 /\ Finish(Code(cur))
 /\ conn' = "live"
 /\ UNCHANGED twice
Lost ==
 IF Broken = "replay"
 THEN /\ Finish(Code(cur))
      /\ conn' = "live"
      /\ twice' = (twice \/ cur = "ok")
 ELSE /\ Finish(2)
      /\ conn' = "dead"
      /\ UNCHANGED twice

\* The verb in hand runs. With a live connection it is sent. Without one the
\* session dials for this line: the store up, the verb is sent on the new
\* connection; the store down, the line fails with what the dial said, and
\* the pool keeps that error.
RunVerb ==
 /\ pc = "verb"
 /\ \/ /\ conn = "live"
       /\ (Answered \/ Lost)
       /\ UNCHANGED <<kept, falseAlarm, wrongCode>>
    \/ /\ conn # "live"
       /\ IF Broken = "stale" /\ kept \in Down
          THEN /\ Finish(ConnCode(kept))
               /\ falseAlarm' = TRUE
               /\ wrongCode' = (wrongCode \/ ConnCode(kept) # 2)
               /\ UNCHANGED <<conn, kept, twice>>
          ELSE IF store = "up"
               THEN /\ kept' = "none"
                    /\ (Answered \/ Lost)
                    /\ UNCHANGED <<falseAlarm, wrongCode>>
               ELSE /\ conn' = "none" /\ kept' = store
                    /\ Finish(ConnCode(store))
                    /\ wrongCode' = (wrongCode \/ ConnCode(store) # 2)
                    /\ UNCHANGED <<falseAlarm, twice>>
 /\ UNCHANGED <<reader, outside>>

\* A signal reaches a watch. SIGINT leaves the watch with code 0 and the
\* reader goes on; SIGTERM ends the session.
WatchStops ==
 /\ pc = "watch" /\ pending \in Signals
 /\ pending' = "none"
 /\ IF (pending = "int" /\ Broken # "int") \/ Broken = "term"
    THEN Finish(0)
    ELSE /\ pc' = "done" /\ how' = "stop" /\ cur' = NoLine
         /\ UNCHANGED <<exit, high>>
 /\ UNCHANGED <<reader, store, stopped, link>>

\* Either signal reaches the session at the prompt or inside a verb.
Stop ==
 /\ pc \in {"read", "verb"} /\ pending \in Signals
 /\ pc' = "done" /\ how' = "stop" /\ cur' = NoLine
 /\ pending' = "none"
 /\ UNCHANGED <<exit, high, reader, store, stopped, link>>

\* What the session owes whatever the outside does. Reading a line is not
\* among them: the next line comes when the outside sends it.
Owed == RunVerb \/ WatchStops \/ Stop
Session == ReadLine \/ EndOfInput \/ Owed

\* The outside.
Term ==
 /\ pc # "done"
 /\ pending' = "term" /\ stopped' = TRUE
 /\ UNCHANGED <<pc, cur, exit, how, high, reader, store, link>>
Int ==
 /\ pc # "done" /\ pending # "term"
 /\ pending' = "int" /\ stopped' = (stopped \/ pc # "watch")
 /\ UNCHANGED <<pc, cur, exit, how, high, reader, store, link>>
StoreDown ==
 /\ pc # "done" /\ store = "up"
 /\ store' \in Down
 /\ conn' = IF conn = "live" THEN "dead" ELSE conn
 /\ UNCHANGED <<pc, cur, exit, how, high, reader, pending, stopped, kept,
                falseAlarm, wrongCode, twice>>
StoreUp ==
 /\ pc # "done" /\ store \in Down
 /\ store' = "up"
 /\ UNCHANGED <<pc, cur, exit, how, high, reader, pending, stopped, link>>
\* The pool's own probe (tryDial): it clears the kept error when the store
\* answers, and keeps the newer error when it does not.
Probe ==
 /\ pc # "done" /\ kept \in Down
 /\ kept' = IF store = "up" THEN "none" ELSE store
 /\ UNCHANGED <<pc, cur, exit, how, high, reader, outside, conn,
                falseAlarm, wrongCode, twice>>

Next == Session \/ Term \/ Int \/ StoreDown \/ StoreUp \/ Probe
Spec == Init /\ [][Next]_vars /\ WF_vars(Owed)

TypeOK ==
 /\ input \in Inputs /\ keep \in BOOLEAN
 /\ pc \in {"read", "verb", "watch", "done"}
 /\ cur \in {NoLine, "ok", "no", "watch"}
 /\ conn \in {"none", "live", "dead"}
 /\ kept \in {"none"} \cup Down
 /\ store \in {"up"} \cup Down
 /\ pending \in {"none"} \cup Signals /\ stopped \in BOOLEAN
 /\ exit \in 0..2 /\ high \in 0..2
 /\ how \in {NoEnd, "eof", "quit", "stop", "fail", "long"}
 /\ afterStop \in BOOLEAN /\ afterFail \in BOOLEAN
 /\ falseAlarm \in BOOLEAN /\ wrongCode \in BOOLEAN /\ twice \in BOOLEAN

\* After a stop no new line starts. An in-flight write may still complete.
NothingStartsAfterStop == ~afterStop
\* A line fails for the connection only when a dial for that line failed,
\* even if the store remains down when a kept error would be returned.
NoFalseAlarm == ~falseAlarm
\* A store that could not be reached is code 2, whatever the dial said.
ConnectionFailureIsTwo == ~wrongCode
\* A write is sent once. A lost reply is never a reason to send it again.
AtMostOnce == ~twice
\* Without --keep-going nothing is read after the first failed line.
StopsAtFirstFailure == ~afterFail
\* Only a stop ends the session as one: SIGINT inside a watch does not.
StopOnlyWhenStopped == (how = "stop") => stopped
\* The exit code is the highest code of any line, unless a stop ended it.
ExitIsHighest == how # "stop" => exit = high
\* With --keep-going every line is read, unless the session was told to end.
KeepGoingReadsEveryLine ==
 (pc = "done" /\ keep /\ how \notin {"quit", "stop"}) => input = <<>>
\* Without it, reaching the end of the input means no line failed.
EndOfInputMeansNoFailure ==
 (pc = "done" /\ ~keep /\ how = "eof") => high = 0
EndsForAReason == (pc = "done") = (how # NoEnd)
LineInHand == (cur # NoLine) = (pc \in {"verb", "watch"})
LiveMeansUp == conn = "live" => store = "up"

\* SIGTERM ends the session, wherever it arrives.
TermEnds == (pending = "term") ~> (pc = "done")
\* SIGINT leaves a watch.
IntLeavesWatch == (pc = "watch" /\ pending = "int") ~> (pc # "watch")
\* A verb ends: the session is never stuck inside a line.
VerbEnds == (pc = "verb") ~> (pc # "verb")
=============================================================================
