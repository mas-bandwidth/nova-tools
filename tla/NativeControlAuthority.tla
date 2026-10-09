--------------------- MODULE NativeControlAuthority ---------------------
(* Protected filesystem authority complements StopGroupAnchor's identity
   checks. Native work outputs are writable, but claim/receipt/channel state
   lives outside every recursive grant of a native turn's completed policy.
   Crash loses the owner, not the group or its durable ownership. Legacy debt
   prevents a new root from silently replacing unknown existing ownership.
   This bounded model assumes the kernel enforces the validated policy;
   TestNativeWallCannotCommandSiblingCleanupChannel checks that assumption. *)
EXTENDS Naturals
CONSTANTS UnsafeVisibleRoot, UnsafeResetBinding, LegacyDebt
VARIABLES bound, owner, live, native, owed, request, foreignCancel, denied, cleaned,
          indexGood, objectSame, stateGood, resetBad
vars == <<bound, owner, live, native, owed, request, foreignCancel, denied, cleaned,
          indexGood, objectSame, stateGood, resetBad>>
extra == <<indexGood, objectSame, stateGood, resetBad>>
BindingGood == indexGood /\ objectSame /\ stateGood
Init == /\ bound = FALSE /\ owner = TRUE /\ live = FALSE /\ native = FALSE
        /\ owed = LegacyDebt /\ request = FALSE /\ foreignCancel = FALSE
        /\ denied = FALSE /\ cleaned = FALSE
        /\ indexGood = TRUE /\ objectSame = TRUE /\ stateGood = TRUE /\ resetBad = FALSE
BindRoot == /\ BindingGood /\ owner /\ ~owed /\ ~bound /\ bound' = TRUE
            /\ UNCHANGED <<owner, live, native, owed, request, foreignCancel, denied, cleaned>>
            /\ UNCHANGED extra
BeginOwnedGroup == /\ BindingGood /\ owner /\ bound /\ ~live /\ ~cleaned /\ live' = TRUE
                   /\ UNCHANGED <<bound, owner, native, owed, request, foreignCancel, denied, cleaned>>
            /\ UNCHANGED extra
AdmitNative == /\ BindingGood /\ owner /\ bound /\ ~owed /\ live /\ ~native /\ native' = TRUE
               /\ UNCHANGED <<bound, owner, live, owed, request, foreignCancel, denied, cleaned>>
            /\ UNCHANGED extra
NativeWrite == /\ native /\ live
               /\ foreignCancel' = (foreignCancel \/ UnsafeVisibleRoot)
               /\ denied' = (denied \/ ~UnsafeVisibleRoot)
               /\ request' = (request \/ UnsafeVisibleRoot)
               /\ UNCHANGED <<bound, owner, live, native, owed, cleaned>>
            /\ UNCHANGED extra
OwnerCleanup == /\ owner /\ bound /\ live /\ request' = TRUE
                /\ UNCHANGED <<bound, owner, live, native, owed, foreignCancel, denied, cleaned>>
            /\ UNCHANGED extra
AnchorSelfSignal == /\ live /\ request /\ live' = FALSE /\ request' = FALSE
                    /\ cleaned' = TRUE
                    /\ UNCHANGED <<bound, owner, native, owed, foreignCancel, denied>>
            /\ UNCHANGED extra
Crash == /\ owner /\ owner' = FALSE
         /\ UNCHANGED <<bound, live, native, owed, request, foreignCancel, denied, cleaned>>
            /\ UNCHANGED extra
Restart == /\ ~owner /\ owner' = TRUE
           /\ UNCHANGED <<bound, live, native, owed, request, foreignCancel, denied, cleaned>>
            /\ UNCHANGED extra
LoseIndex == /\ bound /\ indexGood /\ indexGood' = FALSE
             /\ UNCHANGED <<bound, owner, live, native, owed, request, foreignCancel, denied, cleaned,
                            objectSame, stateGood, resetBad>>
ChangeIdentity == /\ bound /\ objectSame /\ objectSame' = FALSE
                  /\ UNCHANGED <<bound, owner, live, native, owed, request, foreignCancel, denied, cleaned,
                                 indexGood, stateGood, resetBad>>
LoseStateProof == /\ bound /\ stateGood /\ stateGood' = FALSE
                  /\ UNCHANGED <<bound, owner, live, native, owed, request, foreignCancel, denied, cleaned,
                                 indexGood, objectSame, resetBad>>
UnsafeRebind == /\ UnsafeResetBinding /\ owner /\ bound /\ ~BindingGood
                /\ resetBad' = TRUE /\ indexGood' = TRUE /\ objectSame' = TRUE /\ stateGood' = TRUE
                /\ UNCHANGED <<bound, owner, live, native, owed, request, foreignCancel, denied, cleaned>>
Next == BindRoot \/ BeginOwnedGroup \/ AdmitNative \/ NativeWrite \/ OwnerCleanup \/ AnchorSelfSignal \/ Crash \/ Restart \/ LoseIndex \/ ChangeIdentity \/ LoseStateProof \/ UnsafeRebind
Spec == Init /\ [][Next]_vars
NoSiblingCancellation == ~foreignCancel
NoOwnershipReset == ~resetBad
LegacyOwnershipHeld == owed => (~bound /\ ~native /\ ~live)
NativeNeedsBoundRoot == native => bound
ReachDeniedThenOwnerCleanupWitness == ~(denied /\ cleaned /\ ~foreignCancel)
=============================================================================
