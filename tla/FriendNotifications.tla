----------------------- MODULE FriendNotifications -----------------------
EXTENDS Naturals

\* SPEC-FRIEND.md, Notifications: one recipient, one pending deal notice per friend,
\* the ready bit set durably when a deal notice arrives, and the notice acked only
\* once its ready notification is queue-accepted, its cards are all taken (withdrawn),
\* or a newer notice replaces it (coalesced). A withdrawn notice clears the ready bit
\* with nothing queued, which NoLostReady allows. A wake ping enters the urgent batch
\* with the pong line. CardCount bounds the deal notices; BadWithdrawHeld is the
\* reversed witness that withdraws while a card is still held.
CONSTANTS CardCount, BatchLimit, BadAck, BadDuplicate, BadNative, BadWithdrawHeld
VARIABLES deal, allTaken, ready, phase, success, queuedReady, queuedUrgent,
          ackedReady, exempt, urgentSource, pong, ackedUrgent,
          processedReady, processedUrgent, retry, native
vars == <<deal, allTaken, ready, phase, success, queuedReady, queuedUrgent,
          ackedReady, exempt, urgentSource, pong, ackedUrgent,
          processedReady, processedUrgent, retry, native>>

Init == /\ deal = FALSE /\ allTaken = FALSE /\ ready = FALSE
        /\ phase = "none" /\ success = FALSE /\ queuedReady = 0
        /\ queuedUrgent = FALSE /\ ackedReady = 0 /\ exempt = 0
        /\ urgentSource = FALSE /\ pong = FALSE /\ ackedUrgent = FALSE
        /\ processedReady = FALSE /\ processedUrgent = FALSE
        /\ retry = FALSE /\ native = FALSE

DealArrives == /\ ~deal /\ phase = "none" /\ ackedReady < CardCount
               /\ deal' = TRUE /\ ready' = TRUE /\ allTaken' = FALSE
               /\ UNCHANGED <<phase, success, queuedReady, queuedUrgent, ackedReady,
                              exempt, urgentSource, pong, ackedUrgent, processedReady,
                              processedUrgent, retry, native>>
CardsTaken == /\ deal /\ ~allTaken /\ allTaken' = TRUE
              /\ UNCHANGED <<deal, ready, phase, success, queuedReady, queuedUrgent,
                             ackedReady, exempt, urgentSource, pong, ackedUrgent,
                             processedReady, processedUrgent, retry, native>>
Coalesce == /\ deal /\ phase = "none" /\ ackedReady < CardCount
            /\ ackedReady' = ackedReady + 1 /\ exempt' = exempt + 1
            /\ ready' = TRUE /\ allTaken' = FALSE
            /\ UNCHANGED <<deal, phase, success, queuedReady, queuedUrgent,
                           urgentSource, pong, ackedUrgent, processedReady,
                           processedUrgent, retry, native>>
Withdraw == /\ deal /\ phase = "none" /\ ackedReady < CardCount
            /\ (allTaken \/ BadWithdrawHeld)
            /\ ackedReady' = ackedReady + 1
            /\ exempt' = IF allTaken THEN exempt + 1 ELSE exempt
            /\ deal' = FALSE /\ ready' = FALSE /\ allTaken' = FALSE
            /\ UNCHANGED <<phase, success, queuedReady, queuedUrgent,
                           urgentSource, pong, ackedUrgent, processedReady,
                           processedUrgent, retry, native>>
PrepareReady == /\ phase = "none" /\ deal /\ ready /\ ~allTaken
                /\ phase' = "ready" /\ success' = FALSE
                /\ UNCHANGED <<deal, allTaken, ready, queuedReady, queuedUrgent,
                               ackedReady, exempt, urgentSource, pong, ackedUrgent,
                               processedReady, processedUrgent, retry, native>>
PrepareUrgent == /\ phase = "none" /\ urgentSource
                 /\ phase' = "urgent" /\ success' = FALSE /\ retry' = FALSE
                 /\ UNCHANGED <<deal, allTaken, ready, queuedReady, queuedUrgent,
                                ackedReady, exempt, urgentSource, pong, ackedUrgent,
                                processedReady, processedUrgent, native>>
WakePing == /\ ~urgentSource /\ urgentSource' = TRUE /\ pong' = TRUE
            /\ UNCHANGED <<deal, allTaken, ready, phase, success, queuedReady, queuedUrgent,
                           ackedReady, exempt, ackedUrgent, processedReady,
                           processedUrgent, retry, native>>
Enqueue == /\ phase \in {"ready", "urgent"} /\ ~retry /\ ~success
           /\ success' = TRUE
           /\ queuedReady' = IF phase = "ready" /\ (queuedReady = 0 \/ BadDuplicate)
                              THEN queuedReady + 1 ELSE queuedReady
           /\ queuedUrgent' = IF phase = "urgent" THEN TRUE ELSE queuedUrgent
           /\ UNCHANGED <<deal, allTaken, ready, phase, ackedReady, exempt,
                          urgentSource, pong, ackedUrgent, processedReady,
                          processedUrgent, retry, native>>
PersistAccepted == /\ success /\ phase \in {"ready", "urgent"}
                   /\ phase' = IF phase = "ready" THEN "acceptedReady" ELSE "acceptedUrgent"
                   /\ success' = FALSE
                   /\ UNCHANGED <<deal, allTaken, ready, queuedReady, queuedUrgent,
                                  ackedReady, exempt, urgentSource, pong, ackedUrgent,
                                  processedReady, processedUrgent, retry, native>>
Settle == /\ phase \in {"acceptedReady", "acceptedUrgent"}
          /\ phase' = "none" /\ retry' = FALSE
          /\ ready' = IF phase = "acceptedReady" THEN FALSE ELSE ready
          /\ deal' = IF phase = "acceptedReady" THEN FALSE ELSE deal
          /\ ackedReady' = IF phase = "acceptedReady" /\ ackedReady < CardCount
                             THEN ackedReady + 1 ELSE ackedReady
          /\ urgentSource' = IF phase = "acceptedUrgent" THEN FALSE ELSE urgentSource
          /\ pong' = IF phase = "acceptedUrgent" THEN FALSE ELSE pong
          /\ ackedUrgent' = IF phase = "acceptedUrgent" THEN TRUE ELSE ackedUrgent
          /\ UNCHANGED <<allTaken, success, queuedReady, queuedUrgent, exempt,
                         processedReady, processedUrgent, native>>
Fail == /\ phase \in {"ready", "urgent"} /\ ~success /\ ~retry
        /\ retry' = TRUE
        /\ UNCHANGED <<deal, allTaken, ready, phase, success, queuedReady, queuedUrgent,
                       ackedReady, exempt, urgentSource, pong, ackedUrgent,
                       processedReady, processedUrgent, native>>
RetryDue == /\ retry /\ retry' = FALSE
            /\ UNCHANGED <<deal, allTaken, ready, phase, success, queuedReady, queuedUrgent,
                           ackedReady, exempt, urgentSource, pong, ackedUrgent,
                           processedReady, processedUrgent, native>>
Crash == /\ success /\ success' = FALSE
         /\ UNCHANGED <<deal, allTaken, ready, phase, queuedReady, queuedUrgent,
                        ackedReady, exempt, urgentSource, pong, ackedUrgent,
                        processedReady, processedUrgent, retry, native>>
ConsumeReady == /\ queuedReady > 0 /\ queuedReady' = 0 /\ processedReady' = TRUE
                /\ UNCHANGED <<deal, allTaken, ready, phase, success, queuedUrgent,
                               ackedReady, exempt, urgentSource, pong, ackedUrgent,
                               processedUrgent, retry, native>>
ConsumeUrgent == /\ queuedUrgent /\ queuedUrgent' = FALSE /\ processedUrgent' = TRUE
                 /\ UNCHANGED <<deal, allTaken, ready, phase, success, queuedReady,
                                ackedReady, exempt, urgentSource, pong, ackedUrgent,
                                processedReady, retry, native>>
NativeMutation == /\ BadNative /\ ~native /\ native' = TRUE
                  /\ UNCHANGED <<deal, allTaken, ready, phase, success, queuedReady,
                                 queuedUrgent, ackedReady, exempt, urgentSource, pong,
                                 ackedUrgent, processedReady, processedUrgent, retry>>
BadAckAction == /\ BadAck /\ deal /\ phase = "none" /\ ackedReady < CardCount
                /\ ackedReady' = ackedReady + 1
                /\ deal' = FALSE /\ ready' = FALSE
                /\ UNCHANGED <<allTaken, phase, success, queuedReady, queuedUrgent,
                               exempt, urgentSource, pong, ackedUrgent,
                               processedReady, processedUrgent, retry, native>>
Next == DealArrives \/ CardsTaken \/ Coalesce \/ Withdraw \/ PrepareReady
        \/ PrepareUrgent \/ WakePing \/ Enqueue \/ PersistAccepted \/ Settle
        \/ Fail \/ RetryDue \/ Crash \/ ConsumeReady \/ ConsumeUrgent
        \/ NativeMutation \/ BadAckAction

TypeOK == /\ deal \in BOOLEAN /\ allTaken \in BOOLEAN /\ ready \in BOOLEAN
          /\ phase \in {"none", "ready", "urgent", "acceptedReady", "acceptedUrgent"}
          /\ success \in BOOLEAN /\ queuedReady \in 0..2 /\ queuedUrgent \in BOOLEAN
          /\ ackedReady \in 0..CardCount /\ exempt \in 0..CardCount
          /\ urgentSource \in BOOLEAN /\ pong \in BOOLEAN /\ ackedUrgent \in BOOLEAN
          /\ processedReady \in BOOLEAN /\ processedUrgent \in BOOLEAN
          /\ retry \in BOOLEAN /\ native \in BOOLEAN
ReadyQueueBounded == queuedReady <= 1
NoLostReady == (ackedReady > exempt) => queuedReady > 0 \/ processedReady
NoLostUrgent == ackedUrgent => queuedUrgent \/ processedUrgent
NativeUntouched == ~native
=============================================================================
