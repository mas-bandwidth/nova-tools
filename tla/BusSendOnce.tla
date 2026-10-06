----------------------------- MODULE BusSendOnce -----------------------------
\* A send under a token is one logical message (docs/SPEC-BUS.md,
\* a-lost-send-response-is-safe-to-retry.w1; internal/bus/token.go: Send,
\* sendOnce and replay; the store's step is AddOnce, a script Redis runs
\* alone: GET the record, else SET it with PX and XADD the message).
\*
\* The state the code owns: records[t], the record of token t in the store
\* (the arguments it was sent with, the message's id, its at, and until, the
\* instant its key expires: at + the cleanup); msgs, the messages written
\* (one entry on every recipient's stream and the log, written in one step,
\* so one element here); answers, the answers a caller received (token,
\* arguments, id). The outside: clock, the store's time; the callers, each
\* sending under any token with any arguments, any number of times (a
\* retry, a watchdog's resend, a changed body), each answer delivered or
\* lost; a process that restarts holds nothing, so it is any caller sending
\* again.
\*
\* Broken = "none" is the design. Every other value is a reversed witness:
\*   "checkapart"    the record is read in one step and the message written in
\*                   another (GET, then MULTI/EXEC), so two racing retries
\*                   both find none and both write: OneMessageWithinLife
\*   "newid"         a send that finds the record writes again with a new id
\*                   (XADD * on every call, the bug): OneMessageWithinLife
\*   "dropearly"     the record's key expires before the life ends (a
\*                   cleanup under the life, which token.go's tokenCleanup
\*                   never allows): OneMessageWithinLife
\*   "nofingerprint" the record is answered whatever the arguments, so a
\*                   changed body is told it was sent: AnswerIsTheOriginal

EXTENDS Naturals, FiniteSets

CONSTANTS Tokens, Args, Callers, Life, Cleanup, MaxClock, MaxSends, Broken

ASSUME Life > 1 /\ Cleanup >= Life

VARIABLES clock, records, msgs, answers, pc, sends

vars == <<clock, records, msgs, answers, pc, sends>>

None == [arg |-> "none", id |-> 0, at |-> 0, until |-> 0]
Idle == [t |-> "none", a |-> "none", saw |-> None]

\* when a record's key expires after it is written: the cleanup, which
\* tokenCleanup holds at the life or later
Keep == IF Broken = "dropearly" THEN Life - 1 ELSE Cleanup

Msg == [id : 1..MaxSends, t : Tokens, arg : Args, at : 0..MaxClock]
Rec == [arg : Args, id : 1..MaxSends, at : 0..MaxClock, until : 0..(MaxClock + Cleanup)]

TypeOK ==
    /\ clock \in 0..MaxClock
    /\ \A t \in Tokens : records[t] = None \/ records[t] \in Rec
    /\ msgs \subseteq Msg
    /\ answers \subseteq (Tokens \X Args \X (1..MaxSends))
    /\ sends \in 0..MaxSends

Init ==
    /\ clock = 0
    /\ records = [t \in Tokens |-> None]
    /\ msgs = {}
    /\ answers = {}
    /\ pc = [c \in Callers |-> Idle]
    /\ sends = 0

\* the store still holds the record (the key has not expired)
Live(r) == r # None /\ clock < r.until

\* the store's step for a send of a under t, against the record it saw, the
\* answer delivered or lost. A live record is answered (its id, when the
\* arguments are the same and the token is inside its life; else a refusal,
\* which writes nothing and answers no id); no record is the write.
Step(t, a, saw, delivered) ==
    IF Live(saw) /\ Broken # "newid"
    THEN /\ UNCHANGED <<records, msgs>>
         /\ answers' = IF delivered /\ (saw.arg = a \/ Broken = "nofingerprint") /\ clock < saw.at + Life
                       THEN answers \cup {<<t, a, saw.id>>}
                       ELSE answers
    ELSE LET id == Cardinality(msgs) + 1 IN
         /\ msgs' = msgs \cup {[id |-> id, t |-> t, arg |-> a, at |-> clock]}
         /\ records' = [records EXCEPT ![t] = [arg |-> a, id |-> id, at |-> clock, until |-> clock + Keep]]
         /\ answers' = IF delivered THEN answers \cup {<<t, a, id>>} ELSE answers

\* the design: one step reads the record and writes (AddOnce)
Send(c, t, a) ==
    /\ Broken # "checkapart"
    /\ pc[c] = Idle
    /\ sends < MaxSends
    /\ sends' = sends + 1
    /\ \E d \in BOOLEAN : Step(t, a, records[t], d)
    /\ UNCHANGED <<clock, pc>>

\* "checkapart": the read is its own step, and the write acts on what it saw
Check(c, t, a) ==
    /\ Broken = "checkapart"
    /\ pc[c] = Idle
    /\ sends < MaxSends
    /\ sends' = sends + 1
    /\ pc' = [pc EXCEPT ![c] = [t |-> t, a |-> a, saw |-> records[t]]]
    /\ UNCHANGED <<clock, records, msgs, answers>>

Write(c) ==
    /\ pc[c] # Idle
    /\ \E d \in BOOLEAN : Step(pc[c].t, pc[c].a, pc[c].saw, d)
    /\ pc' = [pc EXCEPT ![c] = Idle]
    /\ UNCHANGED <<clock, sends>>

Tick ==
    /\ clock < MaxClock
    /\ clock' = clock + 1
    /\ UNCHANGED <<records, msgs, answers, pc, sends>>

Next ==
    \/ Tick
    \/ \E c \in Callers, t \in Tokens, a \in Args : Send(c, t, a) \/ Check(c, t, a)
    \/ \E c \in Callers : Write(c)

Spec == Init /\ [][Next]_vars

\* Two messages under one token are a life apart at least: inside the life
\* a retry, however often, wherever its answers went, is the one message.
OneMessageWithinLife ==
    \A m1, m2 \in msgs :
        (m1.t = m2.t /\ m1.id # m2.id) => (m1.at >= m2.at + Life \/ m2.at >= m1.at + Life)

\* An answer names the message written under its token with its arguments:
\* a retry answers the original, and other arguments are never told they went.
AnswerIsTheOriginal ==
    \A ans \in answers :
        \E m \in msgs : m.id = ans[3] /\ m.t = ans[1] /\ m.arg = ans[2]
=============================================================================
