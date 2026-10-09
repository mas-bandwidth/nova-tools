--------------------- MODULE NativeControlAuthority ---------------------
(* Protected filesystem authority complements StopGroupAnchor's identity
   checks. Native work outputs are writable, but claim/receipt/channel state
   lives outside every recursive grant of a native turn's completed policy.
   Crash loses the owner, not the group or its durable ownership. Legacy debt
   prevents a new root from silently replacing unknown existing ownership.
   This bounded model assumes the kernel enforces the validated policy;
   TestNativeWallCannotCommandSiblingCleanupChannel checks that assumption. *)
EXTENDS Naturals
CONSTANTS UnsafeVisibleRoot, LegacyDebt
VARIABLES bound, owner, live, native, owed, request, foreignCancel, denied, cleaned
vars == <<bound, owner, live, native, owed, request, foreignCancel, denied, cleaned>>
Init == /\ bound = FALSE /\ owner = TRUE /\ live = FALSE /\ native = FALSE
        /\ owed = LegacyDebt /\ request = FALSE /\ foreignCancel = FALSE
        /\ denied = FALSE /\ cleaned = FALSE
BindRoot == /\ owner /\ ~owed /\ ~bound /\ bound' = TRUE
            /\ UNCHANGED <<owner, live, native, owed, request, foreignCancel, denied, cleaned>>
BeginOwnedGroup == /\ owner /\ bound /\ ~live /\ ~cleaned /\ live' = TRUE
                   /\ UNCHANGED <<bound, owner, native, owed, request, foreignCancel, denied, cleaned>>
AdmitNative == /\ owner /\ bound /\ ~owed /\ live /\ ~native /\ native' = TRUE
               /\ UNCHANGED <<bound, owner, live, owed, request, foreignCancel, denied, cleaned>>
NativeWrite == /\ native /\ live
               /\ foreignCancel' = (foreignCancel \/ UnsafeVisibleRoot)
               /\ denied' = (denied \/ ~UnsafeVisibleRoot)
               /\ request' = (request \/ UnsafeVisibleRoot)
               /\ UNCHANGED <<bound, owner, live, native, owed, cleaned>>
OwnerCleanup == /\ owner /\ bound /\ live /\ request' = TRUE
                /\ UNCHANGED <<bound, owner, live, native, owed, foreignCancel, denied, cleaned>>
AnchorSelfSignal == /\ live /\ request /\ live' = FALSE /\ request' = FALSE
                    /\ cleaned' = TRUE
                    /\ UNCHANGED <<bound, owner, native, owed, foreignCancel, denied>>
Crash == /\ owner /\ owner' = FALSE
         /\ UNCHANGED <<bound, live, native, owed, request, foreignCancel, denied, cleaned>>
Restart == /\ ~owner /\ owner' = TRUE
           /\ UNCHANGED <<bound, live, native, owed, request, foreignCancel, denied, cleaned>>
Next == BindRoot \/ BeginOwnedGroup \/ AdmitNative \/ NativeWrite \/ OwnerCleanup \/ AnchorSelfSignal \/ Crash \/ Restart
Spec == Init /\ [][Next]_vars
NoSiblingCancellation == ~foreignCancel
LegacyOwnershipHeld == owed => (~bound /\ ~native /\ ~live)
NativeNeedsBoundRoot == native => bound
ReachDeniedThenOwnerCleanupWitness == ~(denied /\ cleaned /\ ~foreignCancel)
=============================================================================
