-------------------------- MODULE StopGroupAnchor --------------------------
(* One native harness group. leader is its original PGID leader; anchor is the
   TERM-resistant sidecar; child is a resistant descendant. Receipts model
   fsynced PID+kernel-birth records. The harness gate cannot open before both
   receipts exist. Crash loses only volatile ownership, never live processes or
   durable receipts. A sidecar PID can be reused while the old group persists;
   its birth mismatch must never authorize a group signal. POSIX group-number
   reuse is possible only after every old member exits.

   CheckPin and SendCommand are separated by arbitrary exits and PGID reuse.
   A FIFO write is only a request; AnchorSignal runs within the still-live
   anchor's own group. UnsafeBareSignal instead sends to a numeric PGID after
   an earlier check, demonstrating the foreign-group counterexample. TERM,
   grace and KILL are abstracted as one successful self-signal transition;
   this model does not establish an elapsed-time bound. *)
EXTENDS Naturals

CONSTANTS UnsafeBareSignal, UnsafeRetry, PermitLoss, PermitCrash
VARIABLES leader, anchor, child, leaderReceipt, anchorReceipt, gate,
          owner, stopped, owed, returned, failed, attempt,
          anchorPIDReused, pgidReused, signalBad, overwrote,
          checked, pending, channelIntact
vars == <<leader, anchor, child, leaderReceipt, anchorReceipt, gate,
          owner, stopped, owed, returned, failed, attempt,
          anchorPIDReused, pgidReused, signalBad, overwrote,
          checked, pending, channelIntact>>
extra == <<checked, pending, channelIntact>>

Live == leader \/ anchor \/ child
VerifiedPin == ~pgidReused /\ anchor /\ anchorReceipt /\ ~anchorPIDReused

Init == /\ leader = TRUE /\ anchor = FALSE /\ child = FALSE
        /\ leaderReceipt = FALSE /\ anchorReceipt = FALSE /\ gate = FALSE
        /\ owner = TRUE /\ stopped = FALSE /\ owed = FALSE
        /\ returned = FALSE /\ failed = FALSE /\ attempt = 1
        /\ anchorPIDReused = FALSE /\ pgidReused = FALSE
        /\ signalBad = FALSE /\ overwrote = FALSE
        /\ checked = FALSE /\ pending = FALSE /\ channelIntact = TRUE

StoreLeader == /\ owner /\ leader /\ ~leaderReceipt
               /\ leaderReceipt' = TRUE
               /\ UNCHANGED <<leader, anchor, child, anchorReceipt, gate,
                              owner, stopped, owed, returned, failed, attempt,
                              anchorPIDReused, pgidReused, signalBad, overwrote>>
               /\ UNCHANGED extra

StartAnchor == /\ owner /\ leaderReceipt /\ leader /\ ~anchor
               /\ anchor' = TRUE
               /\ UNCHANGED <<leader, child, leaderReceipt, anchorReceipt, gate,
                              owner, stopped, owed, returned, failed, attempt,
                              anchorPIDReused, pgidReused, signalBad, overwrote>>
               /\ UNCHANGED extra

StoreAnchor == /\ owner /\ anchor /\ ~anchorReceipt
               /\ anchorReceipt' = TRUE
               /\ UNCHANGED <<leader, anchor, child, leaderReceipt, gate,
                              owner, stopped, owed, returned, failed, attempt,
                              anchorPIDReused, pgidReused, signalBad, overwrote>>
               /\ UNCHANGED extra

OpenGate == /\ owner /\ ~stopped /\ leaderReceipt /\ anchorReceipt
            /\ leader /\ anchor /\ ~gate
            /\ gate' = TRUE /\ child' = TRUE
            /\ UNCHANGED <<leader, anchor, leaderReceipt, anchorReceipt,
                           owner, stopped, owed, returned, failed, attempt,
                           anchorPIDReused, pgidReused, signalBad, overwrote>>
               /\ UNCHANGED extra

Stop == /\ ~stopped
        /\ stopped' = TRUE /\ owed' = TRUE
        /\ UNCHANGED <<leader, anchor, child, leaderReceipt, anchorReceipt,
                       gate, owner, returned, failed, attempt,
                       anchorPIDReused, pgidReused, signalBad, overwrote>>
               /\ UNCHANGED extra

(* The original leader can exit while its sidecar and descendant remain. *)
LeaderExit == /\ gate /\ leader
              /\ leader' = FALSE
              /\ UNCHANGED <<anchor, child, leaderReceipt, anchorReceipt,
                             gate, owner, stopped, owed, returned, failed,
                             attempt, anchorPIDReused, pgidReused,
                             signalBad, overwrote>>
               /\ UNCHANGED extra

FailStart == /\ gate /\ leader /\ ~failed
             /\ leader' = FALSE /\ failed' = TRUE
             /\ UNCHANGED <<anchor, child, leaderReceipt, anchorReceipt,
                            gate, owner, stopped, owed, returned, attempt,
                            anchorPIDReused, pgidReused, signalBad, overwrote>>
               /\ UNCHANGED extra

ChildExit == /\ child /\ child' = FALSE
             /\ UNCHANGED <<leader, anchor, leaderReceipt, anchorReceipt,
                            gate, owner, stopped, owed, returned, failed,
                            attempt, anchorPIDReused, pgidReused,
                            signalBad, overwrote>>
               /\ UNCHANGED extra

AnchorLoss == /\ PermitLoss /\ anchor
              /\ anchor' = FALSE
              /\ UNCHANGED <<leader, child, leaderReceipt, anchorReceipt,
                             gate, owner, stopped, owed, returned, failed,
                             attempt, anchorPIDReused, pgidReused,
                             signalBad, overwrote>>
               /\ UNCHANGED extra

(* A dead sidecar's PID may be reissued while the old descendant still holds
   the original PGID. The stored birth must then fail verification. *)
AnchorPIDReuse == /\ ~anchor /\ anchorReceipt /\ ~anchorPIDReused
                  /\ anchorPIDReused' = TRUE
                  /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                                 anchorReceipt, gate, owner, stopped, owed,
                                 returned, failed, attempt, pgidReused,
                                 signalBad, overwrote>>
               /\ UNCHANGED extra

(* The group number can be reused only after the entire old group exits. *)
PGIDReuse == /\ ~Live /\ ~pgidReused
             /\ pgidReused' = TRUE
             /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                            anchorReceipt, gate, owner, stopped, owed,
                            returned, failed, attempt, anchorPIDReused,
                            signalBad, overwrote>>
               /\ UNCHANGED extra

Crash == /\ PermitCrash /\ owner /\ owner' = FALSE
         /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                        anchorReceipt, gate, stopped, owed, returned,
                        failed, attempt, anchorPIDReused, pgidReused,
                        signalBad, overwrote>>
         /\ UNCHANGED extra

Restart == /\ ~owner /\ owner' = TRUE
           /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                          anchorReceipt, gate, stopped, owed, returned,
                          failed, attempt, anchorPIDReused, pgidReused,
                          signalBad, overwrote>>
               /\ UNCHANGED extra

(* A recorded anchor identity authorizes only an attempt to write to its
   protected FIFO. The world may remove the anchor or reuse the PGID before
   SendCommand. No external group signal occurs at either step. *)
CheckPin == /\ owner /\ (owed \/ failed) /\ Live /\ VerifiedPin
            /\ ~checked /\ ~pending
            /\ checked' = TRUE
            /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                           anchorReceipt, gate, owner, stopped, owed,
                           returned, failed, attempt, anchorPIDReused,
                           pgidReused, signalBad, overwrote,
                           pending, channelIntact>>

SendCommand == /\ owner /\ checked
               /\ checked' = FALSE
               /\ pending' = (VerifiedPin /\ channelIntact)
               /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                              anchorReceipt, gate, owner, stopped, owed,
                              returned, failed, attempt, anchorPIDReused,
                              pgidReused, signalBad, overwrote, channelIntact>>

(* A replacement FIFO has a different device/inode, so a writer refuses it.
   An anchor already blocked on the old inode can still receive only old-FIFO
   commands; replacing the path cannot redirect a verified command. *)
ReplaceChannel == /\ PermitLoss /\ channelIntact /\ anchorReceipt
                  /\ channelIntact' = FALSE
                  /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                                 anchorReceipt, gate, owner, stopped, owed,
                                 returned, failed, attempt, anchorPIDReused,
                                 pgidReused, signalBad, overwrote,
                                 checked, pending>>

(* The anchor sends kill(0) while it is itself a member of the old group.
   If it died after the FIFO write, this action is disabled and debt remains. *)
AnchorSignal == /\ pending /\ anchor
                /\ leader' = FALSE /\ anchor' = FALSE /\ child' = FALSE
                /\ pending' = FALSE
                /\ UNCHANGED <<leaderReceipt, anchorReceipt, gate, owner,
                               stopped, owed, returned, failed, attempt,
                               anchorPIDReused, pgidReused, signalBad,
                               overwrote, checked, channelIntact>>

(* Mutation: the owner uses a checked-but-stale numeric PGID after all old
   members exit and an unrelated group reuses the number. It exposes the
   check-to-signal race that the anchor's self-signal removes. *)
UnsafeExternalKill == /\ UnsafeBareSignal /\ checked /\ pgidReused
                      /\ signalBad' = TRUE /\ checked' = FALSE
                      /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                                     anchorReceipt, gate, owner, stopped,
                                     owed, returned, failed, attempt,
                                     anchorPIDReused, pgidReused, overwrote,
                                     pending, channelIntact>>

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
               /\ UNCHANGED extra

Return == /\ owner /\ owed /\ ~Live
          /\ returned' = TRUE /\ owed' = FALSE
          /\ UNCHANGED <<leader, anchor, child, leaderReceipt,
                         anchorReceipt, gate, owner, stopped, failed,
                         attempt, anchorPIDReused, pgidReused,
                         signalBad, overwrote>>
               /\ UNCHANGED extra

Next == StoreLeader \/ StartAnchor \/ StoreAnchor \/ OpenGate \/ Stop \/
        LeaderExit \/ FailStart \/ ChildExit \/ AnchorLoss \/
        AnchorPIDReuse \/ PGIDReuse \/ Crash \/ Restart \/
        CheckPin \/ SendCommand \/ ReplaceChannel \/ AnchorSignal \/
        UnsafeExternalKill \/ Retry \/ Return
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
            WF_vars(StartAnchor) /\ WF_vars(StoreAnchor) /\
            WF_vars(CheckPin) /\ WF_vars(SendCommand) /\
            WF_vars(AnchorSignal) /\ WF_vars(Return)
EventuallyReturned == <>returned
=============================================================================
