---------------------- MODULE MCFriendKeepalive ----------------------
EXTENDS Integers, FiniteSets
CONSTANT Broken
\* One receiving endpoint; the opposite direction uses the same rules.
\* Time is this endpoint's monotonic seconds, never a peer timestamp.
\* Three emitted challenges per incarnation reduce state space; production
\* uses an eleven-slot ledger at one-second cadence. No transport/auth proof.
VARIABLES now, instance, peerInstance, seat, issued, consumed, sequence,
          lastEmit, proved, lastPong, invalidProof, duplicateRefresh,
          burst, asleep, nativeTurns, peerSequence, transitionCut, staleTransition
vars == <<now, instance, peerInstance, seat, issued, consumed, sequence,
          lastEmit, proved, lastPong, invalidProof, duplicateRefresh,
          burst, asleep, nativeTurns, peerSequence, transitionCut, staleTransition>>
Init == /\ now = 0 /\ instance = 1 /\ peerInstance = 0 /\ seat = 1
        /\ issued = {} /\ consumed = 0 /\ sequence = 0 /\ lastEmit = -1
        /\ proved = FALSE /\ lastPong = 0 /\ invalidProof = FALSE
        /\ duplicateRefresh = FALSE /\ burst = FALSE
        /\ asleep = TRUE /\ nativeTurns = 0 /\ peerSequence = 0
        /\ transitionCut = 0 /\ staleTransition = FALSE
Emit == /\ sequence < 3
        /\ (lastEmit < now \/ Broken = "burst")
        /\ LET n == [i |-> instance, p |-> peerInstance, s |-> seat,
                       q |-> sequence + 1, t |-> now]
           IN issued' = issued \cup {n}
        /\ sequence' = sequence + 1 /\ lastEmit' = now
        /\ burst' = (burst \/ lastEmit = now)
        /\ UNCHANGED <<now, instance, peerInstance, seat, consumed, proved,
                       lastPong, invalidProof, duplicateRefresh, asleep, nativeTurns,
                       peerSequence, transitionCut, staleTransition>>
Valid(n) == /\ n.i = instance /\ n.s = seat
            /\ n.q > consumed /\ n.q > transitionCut /\ now - n.t < 10
Admitted(n) == /\ (n.i = instance \/ Broken = "instance")
               /\ (n.s = seat \/ Broken = "seat")
               /\ (n.q > consumed \/ Broken = "duplicate")
               /\ (now - n.t < 10 \/ Broken = "expired")
Ack(n, p, r) == /\ n \in issued /\ Admitted(n)
          /\ (n.q > transitionCut \/ Broken = "transition")
          /\ (p # peerInstance \/ r > peerSequence \/ Broken = "peer")
          /\ consumed' = n.q /\ peerInstance' = p /\ peerSequence' = r
          /\ transitionCut' = (IF p # peerInstance THEN sequence ELSE transitionCut)
          /\ staleTransition' = (staleTransition \/ n.q <= transitionCut)
          /\ proved' = TRUE /\ lastPong' = now
          /\ invalidProof' = (invalidProof \/ ~Valid(n))
          /\ duplicateRefresh' = (duplicateRefresh \/ n.q <= consumed
                 \/ (p = peerInstance /\ r <= peerSequence))
          /\ asleep' = (IF Broken = "wake" THEN FALSE ELSE asleep)
          /\ nativeTurns' = (IF Broken = "turn" THEN nativeTurns + 1 ELSE nativeTurns)
          /\ UNCHANGED <<now, instance, seat, issued, sequence,
                         lastEmit, burst>>
Tick == /\ now < 14 /\ now' = now + 1
        /\ UNCHANGED <<instance, peerInstance, seat, issued, consumed, sequence,
                       lastEmit, proved, lastPong, invalidProof, duplicateRefresh,
                       burst, asleep, nativeTurns, peerSequence, transitionCut, staleTransition>>
\* Old frames remain in issued to exercise delayed ACK rejection. Production
\* discards its ledger on reset; old frames cannot find a matching new nonce.
ResetLocal == /\ instance = 1 /\ instance' = 2 /\ sequence' = 0
              /\ proved' = FALSE /\ consumed' = 0 /\ peerSequence' = 0
              /\ transitionCut' = 0
              /\ UNCHANGED <<now, peerInstance, seat, issued, lastEmit, lastPong,
                             invalidProof, duplicateRefresh, burst, asleep, nativeTurns, staleTransition>>
SeatChange == /\ seat = 1 /\ seat' = 2 /\ proved' = FALSE
              /\ transitionCut' = sequence
              /\ UNCHANGED <<now, instance, peerInstance, issued, consumed, sequence,
                             lastEmit, lastPong, invalidProof, duplicateRefresh,
                             burst, asleep, nativeTurns, peerSequence, staleTransition>>
\* The stable seat generation is external authority; concrete sprint mapping
\* and atomic publication fencing are separate implementation obligations.
Up == proved /\ now - lastPong < 10
Status == IF ~Up THEN "down" ELSE IF asleep THEN "asleep" ELSE "up"
Next == Emit \/ Tick \/ ResetLocal \/ SeatChange
        \/ (\E n \in issued, p \in 1..2, r \in 1..3 : Ack(n, p, r))
Spec == Init /\ [][Next]_vars
FreshProofOnly == ~invalidProof
NoDuplicateRefresh == ~duplicateRefresh
OneEmissionPerTick == ~burst
NoSessionWake == asleep
NoSessionTurn == nativeTurns = 0
TimeoutDown == now - lastPong >= 10 => Status = "down"
NoPreTransitionProof == ~staleTransition
LedgerBound == Cardinality(issued) <= 6
=====================================================================
