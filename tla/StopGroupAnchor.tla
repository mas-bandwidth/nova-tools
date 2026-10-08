-------------------------- MODULE StopGroupAnchor --------------------------
(* One native harness group. leader is its original PGID leader; anchor is the
   TERM-resistant sidecar; child is a resistant descendant. Receipts model
   fsynced PID+kernel-birth records. The harness gate cannot open before both
   receipts exist. Crash loses only volatile ownership, never live processes or
   durable receipts. A sidecar PID can be reused while the old group persists;
   its birth mismatch must never authorize a group signal. POSIX group-number
   reuse is possible only after every old member exits.

   SignalGroup abstracts a verified TERM/grace/KILL teardown as one transition.
   It proves signal authority and return ordering, not an OS time bound.
   UnsafeBareSignal and UnsafeRetry are model mutations with expected invariant
   failures, so the signal and retry invariants have nonvacuous witnesses. *)
EXTENDS Naturals

CONSTANTS UnsafeBareSignal, UnsafeRetry, PermitLoss, PermitCrash
VARIABLES leader, anchor, child, leaderReceipt, anchorReceipt, gate,
          owner, stopped, owed, returned, failed, attempt,
          anchorPIDReused, pgidReused, signalBad, overwrote
vars == <<leader, anchor, child, leaderReceipt, anchorReceipt, gate,
          owner, stopped, owed, returned, failed, attempt,
          anchorPIDReused, pgidReused, signalBad, overwrote>>

Live == leader \/ anchor \/ child
VerifiedPin == ~pgidReused /\
               ((leader /\ leaderReceipt) \/
                (anchor /\ anchorReceipt /\ ~anchorPIDReused))

Init == /\ leader = TRUE /\ anchor = FALSE /\ child = FALSE
        /\ leaderReceipt = FALSE /\ anchorReceipt = FALSE /\ gate = FALSE
        /\ owner = TRUE /\ stopped = FALSE /\ owed = FALSE
        /\ returned = FALSE /\ failed = FALSE /\ attempt = 1
        /\ anchorPIDReused = FALSE /\ pgidReused = FALSE
        /\ signalBad = FALSE /\ overwrote = FALSE

StoreLeader == /\ owner /\ leader /\ ~leaderReceipt
               /\ leaderReceipt' = TRUE
               /\ UNCHANGED <<leader, anchor, child, anchorReceipt, gate,
                              owner, stopped, owed, returned, failed, attempt,
                              anchorPIDReused, pgidReused, signalBad, overwrote>>

StartAnchor == /\ owner /\ leaderReceipt /\ leader /\ ~anchor /\ ~stopped
               /\ anchor' = TRUE
               /\ UNCHANGED <<leader, child, leaderReceipt, anchorReceipt, gate,
                              owner, stopped, owed, returned, failed, attempt,
                              anchorPIDReused, pgidReused, signalBad, overwrote>>

StoreAnchor == /\ owner /\ anchor /\ ~anchorReceipt
               /\ anchorReceipt' = TRUE
               /\ UNCHANGED <<leader, anchor, child, leaderReceipt, gate,
                              owner, stopped, owed, returned, failed, attempt,
                              anchorPIDReused, pgidReused, signalBad, overwrote>>

OpenGate == /\ owner /\ ~stopped /\ leaderReceipt /\ anchorReceipt
            /\ leader /\ anchor /\ ~gate
            /\ gate' = TRUE /\ child' = TRUE
            /\ UNCHANGED <<leader, anchor, leaderReceipt, anchorReceipt,
                           owner, stopped, owed, returned, failed, attempt,
                           anchorPIDReused, pgidReused, signalBad, overwrote>>

Stop == /\ ~stopped
        /\ stopped' = TRUE /\ owed' = TRUE
        /\ UNCHANGED <<leader, anchor, child, leaderReceipt, anchorReceipt,
                       gate, owner, returned, failed, attempt,
                       anchorPIDReused, pgidReused, signalBad, overwrote>>

(* The original leader can exit while its sidecar and descendant remain. *)
LeaderExit == /\ gate /\ leader
              /\ leader' = FALSE
              /\ UNCHANGED <<anchor, child, leaderReceipt, anchorReceipt,
                             gate, owner, stopped, owed, returned, failed,
                             attempt, anchorPIDReused, pgidReused,
                             signalBad, overwrote>>

FailStart == /\ gate /\ leader /\ ~failed
             /\ leader' = FALSE /\ failed' = TRUE
             /\ UNCHANGED <<anchor, child, leaderReceipt, anchorReceipt,
                            gate, owner, stopped, owed, returned, attempt,
                            anchorPIDReused, pgidReused, signalBad, overwrote>>

ChildExit == /\ child /\ child' = FALSE
             /\ UNCHANGED <<leader, anchor, leaderReceipt, anchorReceipt,
                            gate, owner, stopped, owed, returned, failed,
                            attempt, anchorPIDReused, pgidReused,
                            signalBad, overwrote>>

AnchorLoss == /\ PermitLoss /\ anchor
              /\ anchor' = FALSE
              /\ UNCHANGED <<leader, child, leaderReceipt, anchorReceipt,
                             gate, owner, stopped, owed, returned, failed,
                             attempt, anchorPIDReused, pgidReused,
                             signalBad, overwrote>>

(* A dead sidecar's PID may be reissued while the old descendant still holds
   the original PGID. The stored birth must then fail verification. *)
AnchorPIDReuse == /\ ~anchor /\ anchorReceipt /\ ~anchorPIDReused
                  /\ anchorPIDReused' = TRUE
                  /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                                 anchorReceipt, gate, owner, stopped, owed,
                                 returned, failed, attempt, pgidReused,
                                 signalBad, overwrote>>

(* The group number can be reused only after the entire old group exits. *)
PGIDReuse == /\ ~Live /\ ~pgidReused
             /\ pgidReused' = TRUE
             /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                            anchorReceipt, gate, owner, stopped, owed,
                            returned, failed, attempt, anchorPIDReused,
                            signalBad, overwrote>>

Crash == /\ PermitCrash /\ owner /\ owner' = FALSE
         /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                        anchorReceipt, gate, stopped, owed, returned,
                        failed, attempt, anchorPIDReused, pgidReused,
                        signalBad, overwrote>>
Restart == /\ ~owner /\ owner' = TRUE
           /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                          anchorReceipt, gate, stopped, owed, returned,
                          failed, attempt, anchorPIDReused, pgidReused,
                          signalBad, overwrote>>

(* The real implementation refuses signal when neither recorded live identity
   pins the group. The mutation permits a bare PGID kill and records the
   actual identity predicate at the signal instant. *)
SignalGroup == /\ owner /\ (owed \/ failed) /\ Live
               /\ (VerifiedPin \/ UnsafeBareSignal)
               /\ signalBad' = (signalBad \/ ~VerifiedPin)
               /\ leader' = FALSE /\ anchor' = FALSE /\ child' = FALSE
               /\ UNCHANGED <<leaderReceipt, anchorReceipt, gate, owner,
                              stopped, owed, returned, failed, attempt,
                              anchorPIDReused, pgidReused, overwrote>>

(* A failed launch may retry only after no old group member can execute.
   The mutation retries while the sidecar or child still runs and replaces
   the only durable identity for the old group. *)
Retry == /\ owner /\ failed /\ ~stopped /\ attempt = 1
         /\ (~Live \/ UnsafeRetry)
         /\ attempt' = 2 /\ overwrote' = (overwrote \/ Live)
         /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                        anchorReceipt, gate, owner, stopped, owed,
                        returned, failed, anchorPIDReused, pgidReused,
                        signalBad>>

Return == /\ owner /\ owed /\ ~Live
          /\ returned' = TRUE /\ owed' = FALSE
          /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                         anchorReceipt, gate, owner, stopped, failed,
                         attempt, anchorPIDReused, pgidReused,
                         signalBad, overwrote>>

Next == StoreLeader \/ StartAnchor \/ StoreAnchor \/ OpenGate \/ Stop \/
        LeaderExit \/ FailStart \/ ChildExit \/ AnchorLoss \/
        AnchorPIDReuse \/ PGIDReuse \/ Crash \/ Restart \/
        SignalGroup \/ Retry \/ Return
Spec == Init /\ [][Next]_vars

NoUnpinnedSignal == ~signalBad
NoReceiptOverwriteWhileLive == ~overwrote
NoPrematureReturn == returned => ~Live
LostIdentityKeepsDebt == owed /\ Live /\ ~VerifiedPin => ~returned
GateNeedsDurableAnchor == gate => leaderReceipt /\ anchorReceipt

(* Conditional progress: when the owner stays up and identities are not lost,
   weak fairness of recording, STOP, verified teardown, and return suffices.
   Crash and unexpected anchor loss are enabled in the safety case, not here. *)
FairSpec == Spec /\ WF_vars(StoreLeader) /\ WF_vars(Stop) /\
            WF_vars(SignalGroup) /\ WF_vars(Return)
EventuallyReturned == <>returned
=============================================================================
