----------------------- MODULE FriendFinishNotice -----------------------
(* One completed one-shot job, its lanes.json Started claim, the durable
   finish-notices/<job>.json, and the bus token record. The source actions are
   endCard/finishNote/sendFinishNotes (internal/friend/lane_end.go and
   finish_notice.go). A failed atomic write changes no durable fact. A daemon
   crash loses only volatile state. The bus commits once under the same token;
   a lost response is answered by replay inside the 24-hour token life.
   The exact recipient and body are abstracted as one immutable journal value;
   the source stores those bytes before any send and reuses them on every try.
   After that life the daemon retains the owed notice and stops automatic
   sends: this model makes no unbounded eventual-delivery claim. *)
EXTENDS Naturals

CONSTANTS SafeClear, StableToken, SafeCleanup

VARIABLES report,        \* provider completion is durable
          startedDisk,   \* lanes.json Started
          startedMem,    \* current daemon's Started
          notice,        \* none, pending, sent; the journal file
          attempted,     \* first possibly committed send was marked on disk
          age,           \* 0: within token life; 1: life elapsed; 2: record expired
          busCount,      \* entries actually accepted by the recipient stream
          reply,         \* current daemon received the bus answer
          acked,         \* successful send receipt was durably recorded
          up,            \* daemon process is present
          storeUp,       \* read-only preflight and send can reach the bus
          writeFailures,
          receiptFailures,
          restarted

vars == <<report, startedDisk, startedMem, notice, attempted, age,
          busCount, reply, acked, up, storeUp, writeFailures,
          receiptFailures, restarted>>

Init == /\ report = FALSE
        /\ startedDisk = TRUE
        /\ startedMem = TRUE
        /\ notice = "none"
        /\ attempted = FALSE
        /\ age = 0
        /\ busCount = 0
        /\ reply = FALSE
        /\ acked = FALSE
        /\ up = TRUE
        /\ storeUp = TRUE
        /\ writeFailures = 0
        /\ receiptFailures = 0
        /\ restarted = FALSE

Complete == /\ up /\ ~report
            /\ report' = TRUE
            /\ UNCHANGED <<startedDisk, startedMem, notice, attempted, age,
                            busCount, reply, acked, up, storeUp, writeFailures,
                            receiptFailures, restarted>>

JournalWriteFails == /\ up /\ report /\ startedDisk
                     /\ notice = "none" /\ writeFailures = 0
                     /\ writeFailures' = 1
                     /\ UNCHANGED <<report, startedDisk, startedMem, notice,
                                     attempted, age, busCount, reply, acked, up,
                                     storeUp, receiptFailures, restarted>>

Enqueue == /\ up /\ report /\ startedDisk /\ notice = "none"
           /\ notice' = "pending"
           /\ UNCHANGED <<report, startedDisk, startedMem, attempted, age,
                           busCount, reply, acked, up, storeUp, writeFailures,
                           receiptFailures, restarted>>

ClearStartedMem == /\ up /\ report /\ startedMem /\ (notice # "none" \/ ~SafeClear)
                   /\ startedMem' = FALSE
                   /\ UNCHANGED <<report, startedDisk, notice, attempted, age,
                                   busCount, reply, acked, up, storeUp,
                                   writeFailures, receiptFailures, restarted>>

SaveStarted == /\ up /\ startedDisk /\ ~startedMem
               /\ startedDisk' = FALSE
               /\ UNCHANGED <<report, startedMem, notice, attempted, age,
                               busCount, reply, acked, up, storeUp,
                               writeFailures, receiptFailures, restarted>>

SaveStartedFails == /\ up /\ startedDisk /\ ~startedMem
                    /\ writeFailures = 1
                    /\ writeFailures' = 2
                    /\ UNCHANGED <<report, startedDisk, startedMem, notice,
                                    attempted, age, busCount, reply, acked, up,
                                    storeUp, receiptFailures, restarted>>

StoreFails == /\ up /\ storeUp
              /\ storeUp' = FALSE
              /\ UNCHANGED <<report, startedDisk, startedMem, notice,
                              attempted, age, busCount, reply, acked, up,
                              writeFailures, receiptFailures, restarted>>

StoreRecovers == /\ ~storeUp
                 /\ storeUp' = TRUE
                 /\ UNCHANGED <<report, startedDisk, startedMem, notice,
                                 attempted, age, busCount, reply, acked, up,
                                 writeFailures, receiptFailures, restarted>>

MarkFirstAttempt == /\ up /\ storeUp /\ notice = "pending" /\ ~attempted
                    /\ attempted' = TRUE
                    /\ UNCHANGED <<report, startedDisk, startedMem, notice,
                                    age, busCount, reply, acked, up, storeUp,
                                    writeFailures, receiptFailures, restarted>>

\* A lost answer may follow a committed send. Replaying the fixed token
\* leaves busCount unchanged. StableToken=FALSE is the reversed witness.
Send(lost) == /\ up /\ storeUp /\ notice = "pending" /\ attempted
              /\ age = 0 /\ busCount < 2
              /\ busCount' = IF busCount = 0 \/ ~StableToken
                                THEN busCount + 1 ELSE busCount
              /\ reply' = ~lost
              /\ UNCHANGED <<report, startedDisk, startedMem, notice,
                              attempted, age, acked, up, storeUp,
                              writeFailures, receiptFailures, restarted>>

ReceiptWriteFails == /\ up /\ reply /\ notice = "pending"
                     /\ receiptFailures = 0
                     /\ receiptFailures' = 1
                     /\ reply' = FALSE \* next loop must replay the token
                     /\ UNCHANGED <<report, startedDisk, startedMem, notice,
                                     attempted, age, busCount, acked, up,
                                     storeUp, writeFailures, restarted>>

MarkReceipt == /\ up /\ reply /\ notice = "pending"
               /\ notice' = "sent"
               /\ acked' = TRUE
               /\ UNCHANGED <<report, startedDisk, startedMem, attempted,
                               age, busCount, reply, up, storeUp,
                               writeFailures, receiptFailures, restarted>>

Cleanup == /\ up /\ notice = "sent"
           /\ ((~startedDisk /\ ~startedMem) \/ ~SafeCleanup)
           /\ notice' = "none"
           /\ UNCHANGED <<report, startedDisk, startedMem, attempted, age,
                           busCount, reply, acked, up, storeUp, writeFailures,
                           receiptFailures, restarted>>

Crash == /\ up
         /\ up' = FALSE
         /\ reply' = FALSE
         /\ UNCHANGED <<report, startedDisk, startedMem, notice, attempted,
                         age, busCount, acked, storeUp, writeFailures,
                         receiptFailures, restarted>>

Restart == /\ ~up
           /\ up' = TRUE
           /\ startedMem' = startedDisk
           /\ restarted' = TRUE
           /\ UNCHANGED <<report, startedDisk, notice, attempted, age,
                           busCount, reply, acked, storeUp, writeFailures,
                           receiptFailures>>

Tick == /\ attempted /\ age < 2
        /\ age' = age + 1
        /\ UNCHANGED <<report, startedDisk, startedMem, notice, attempted,
                        busCount, reply, acked, up, storeUp, writeFailures,
                        receiptFailures, restarted>>

Next == \/ Complete \/ JournalWriteFails \/ Enqueue \/ ClearStartedMem
        \/ SaveStarted \/ SaveStartedFails \/ StoreFails \/ StoreRecovers \/ MarkFirstAttempt
        \/ \E lost \in BOOLEAN : Send(lost)
        \/ ReceiptWriteFails \/ MarkReceipt \/ Cleanup \/ Crash \/ Restart \/ Tick

SafetySpec == Init /\ [][Next]_vars

\* Deterministic witness: a journal write fails once; completion is then
\* enqueued; the bus commits but loses the answer; a restart replays that
\* token; a receipt write fails once, then replay records the same ID.
RetryNext ==
    \/ Complete
    \/ JournalWriteFails
    \/ (writeFailures = 1 /\ Enqueue)
    \/ ClearStartedMem
    \/ SaveStarted
    \/ (~startedDisk /\ MarkFirstAttempt)
    \/ (busCount = 0 /\ Send(TRUE))
    \/ (busCount = 1 /\ ~restarted /\ Crash)
    \/ Restart
    \/ (restarted /\ receiptFailures = 0 /\ busCount = 1 /\ ~reply /\ Send(FALSE))
    \/ ReceiptWriteFails
    \/ (receiptFailures = 1 /\ ~reply /\ Send(FALSE))
    \/ (receiptFailures = 1 /\ MarkReceipt)
    \/ Cleanup

RetrySpec == Init /\ [][RetryNext]_vars /\ WF_vars(RetryNext)

TypeOK == /\ report \in BOOLEAN /\ startedDisk \in BOOLEAN
          /\ startedMem \in BOOLEAN /\ notice \in {"none", "pending", "sent"}
          /\ attempted \in BOOLEAN /\ age \in 0..2 /\ busCount \in 0..2
          /\ reply \in BOOLEAN /\ acked \in BOOLEAN /\ up \in BOOLEAN
          /\ storeUp \in BOOLEAN /\ writeFailures \in 0..2
          /\ receiptFailures \in 0..1 /\ restarted \in BOOLEAN

NoLostCompletion == report => (startedDisk \/ notice # "none" \/ acked)
AtMostOneBusEntry == busCount <= 1
NoPrematureReceipt == acked => busCount = 1
CleanupAfterDurableClear == notice = "none" /\ acked => ~startedDisk
ExpiredStillOwed == age >= 1 /\ attempted /\ ~acked => notice = "pending"
EventuallyReceipt == <> (acked /\ notice = "none")
=======================================================================
