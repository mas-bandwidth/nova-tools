----------------------- MODULE FriendNotifications -----------------------
EXTENDS Naturals

\* SPEC-FRIEND.md, Notifications: one recipient, durable intent before source ACK,
\* accepted before receipt settlement, queue-key dedupe, and no native mutations.
\* CardCount counts distinct delivery courtesies; receiver passes consume BatchLimit.
\* Crash is process loss, not storage-media failure; the file sync/rename is the boundary.
CONSTANTS CardCount, BatchLimit, BadAck, BadDuplicate, BadNative
VARIABLES source, read, ready, phase, success, queuedReady, queuedUrgent,
          ackedReady, urgentSource, ackedUrgent, processedReady, processedUrgent,
          retry, native
vars == <<source, read, ready, phase, success, queuedReady, queuedUrgent,
          ackedReady, urgentSource, ackedUrgent, processedReady, processedUrgent,
          retry, native>>

Init == /\ source = CardCount /\ read = 0 /\ ready = FALSE
        /\ phase = "none" /\ success = FALSE /\ queuedReady = 0
        /\ queuedUrgent = FALSE /\ ackedReady = 0 /\ urgentSource = TRUE
        /\ ackedUrgent = FALSE /\ processedReady = FALSE
        /\ processedUrgent = FALSE /\ retry = FALSE /\ native = FALSE

Receive == /\ phase = "none" /\ read = 0 /\ source > 0
           /\ read' = IF source < BatchLimit THEN source ELSE BatchLimit
           /\ UNCHANGED <<source, ready, phase, success, queuedReady, queuedUrgent,
                          ackedReady, urgentSource, ackedUrgent, processedReady,
                          processedUrgent, retry, native>>
PersistReady == /\ read > 0 /\ ~ready /\ ready' = TRUE
                /\ UNCHANGED <<source, read, phase, success, queuedReady, queuedUrgent,
                               ackedReady, urgentSource, ackedUrgent, processedReady,
                               processedUrgent, retry, native>>
AckReady == /\ read > 0 /\ (ready \/ BadAck)
            /\ source' = source - read /\ ackedReady' = ackedReady + read /\ read' = 0
            /\ UNCHANGED <<ready, phase, success, queuedReady, queuedUrgent,
                           urgentSource, ackedUrgent, processedReady,
                           processedUrgent, retry, native>>
PrepareReady == /\ phase = "none" /\ ready /\ read = 0 /\ source = 0
                /\ phase' = "ready" /\ success' = FALSE
                /\ UNCHANGED <<source, read, ready, queuedReady, queuedUrgent,
                               ackedReady, urgentSource, ackedUrgent, processedReady,
                               processedUrgent, retry, native>>
PrepareUrgent == /\ phase = "none" /\ urgentSource /\ read = 0
                 /\ phase' = "urgent" /\ success' = FALSE /\ retry' = FALSE
                 /\ UNCHANGED <<source, read, ready, queuedReady, queuedUrgent,
                                ackedReady, urgentSource, ackedUrgent, processedReady,
                                processedUrgent, native>>
Enqueue == /\ phase \in {"ready", "urgent"} /\ ~retry /\ ~success
           /\ success' = TRUE
           /\ queuedReady' = IF phase = "ready" /\ (queuedReady = 0 \/ BadDuplicate)
                             THEN queuedReady + 1 ELSE queuedReady
           /\ queuedUrgent' = IF phase = "urgent" THEN TRUE ELSE queuedUrgent
           /\ UNCHANGED <<source, read, ready, phase, ackedReady, urgentSource,
                          ackedUrgent, processedReady, processedUrgent, retry, native>>
PersistAccepted == /\ success /\ phase \in {"ready", "urgent"}
                   /\ phase' = IF phase = "ready" THEN "acceptedReady" ELSE "acceptedUrgent"
                   /\ success' = FALSE
                   /\ UNCHANGED <<source, read, ready, queuedReady, queuedUrgent,
                                  ackedReady, urgentSource, ackedUrgent, processedReady,
                                  processedUrgent, retry, native>>
Settle == /\ phase \in {"acceptedReady", "acceptedUrgent"}
          /\ phase' = "none" /\ retry' = FALSE
          /\ ready' = IF phase = "acceptedReady" THEN FALSE ELSE ready
          /\ urgentSource' = IF phase = "acceptedUrgent" THEN FALSE ELSE urgentSource
          /\ ackedUrgent' = IF phase = "acceptedUrgent" THEN TRUE ELSE ackedUrgent
          /\ UNCHANGED <<source, read, success, queuedReady, queuedUrgent, ackedReady,
                         processedReady, processedUrgent, native>>
Fail == /\ phase \in {"ready", "urgent"} /\ ~success /\ ~retry
        /\ retry' = TRUE
        /\ UNCHANGED <<source, read, ready, phase, success, queuedReady, queuedUrgent,
                       ackedReady, urgentSource, ackedUrgent, processedReady,
                       processedUrgent, native>>
RetryDue == /\ retry /\ retry' = FALSE
            /\ UNCHANGED <<source, read, ready, phase, success, queuedReady, queuedUrgent,
                           ackedReady, urgentSource, ackedUrgent, processedReady,
                           processedUrgent, native>>
Crash == /\ (read > 0 \/ success) /\ read' = 0 /\ success' = FALSE
         /\ UNCHANGED <<source, ready, phase, queuedReady, queuedUrgent, ackedReady,
                        urgentSource, ackedUrgent, processedReady, processedUrgent,
                        retry, native>>
ConsumeReady == /\ queuedReady > 0 /\ queuedReady' = 0 /\ processedReady' = TRUE
                /\ UNCHANGED <<source, read, ready, phase, success, queuedUrgent,
                               ackedReady, urgentSource, ackedUrgent, processedUrgent,
                               retry, native>>
ConsumeUrgent == /\ queuedUrgent /\ queuedUrgent' = FALSE /\ processedUrgent' = TRUE
                 /\ UNCHANGED <<source, read, ready, phase, success, queuedReady,
                                ackedReady, urgentSource, ackedUrgent, processedReady,
                                retry, native>>
NativeMutation == /\ BadNative /\ ~native /\ native' = TRUE
                  /\ UNCHANGED <<source, read, ready, phase, success, queuedReady,
                                 queuedUrgent, ackedReady, urgentSource, ackedUrgent,
                                 processedReady, processedUrgent, retry>>
Next == Receive \/ PersistReady \/ AckReady \/ PrepareReady \/ PrepareUrgent \/ Enqueue
        \/ PersistAccepted \/ Settle \/ Fail \/ RetryDue \/ Crash \/ ConsumeReady
        \/ ConsumeUrgent \/ NativeMutation

TypeOK == /\ source \in 0..CardCount /\ read \in 0..BatchLimit
          /\ ackedReady \in 0..CardCount /\ queuedReady \in 0..2
          /\ phase \in {"none", "ready", "urgent", "acceptedReady", "acceptedUrgent"}
          /\ ready \in BOOLEAN /\ success \in BOOLEAN /\ queuedUrgent \in BOOLEAN
          /\ urgentSource \in BOOLEAN /\ ackedUrgent \in BOOLEAN
          /\ processedReady \in BOOLEAN /\ processedUrgent \in BOOLEAN
          /\ retry \in BOOLEAN /\ native \in BOOLEAN
ReadyQueueBounded == queuedReady <= 1
NoLostReady == ackedReady > 0 => ready \/ phase \in {"ready", "acceptedReady"}
                                \/ queuedReady > 0 \/ processedReady
NoLostUrgent == ackedUrgent => queuedUrgent \/ processedUrgent
NativeUntouched == ~native
=============================================================================
