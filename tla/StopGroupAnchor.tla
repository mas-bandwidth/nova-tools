-------------------------- MODULE StopGroupAnchor --------------------------
(* Native STOP proof when the original harness group leader exits. The durable
   sidecar is started and birth-recorded before the harness gate releases.
   A signal may be sent only while a verified leader or sidecar pins the group.
   Unexpected anchor loss leaves owed debt; it never licenses a bare PGID kill. *)
EXTENDS Naturals

VARIABLES leader, anchor, child, owed, returned, signaledUnpinned
vars == <<leader, anchor, child, owed, returned, signaledUnpinned>>

Init == /\ leader = TRUE /\ anchor = TRUE /\ child = TRUE
        /\ owed = FALSE /\ returned = FALSE /\ signaledUnpinned = FALSE

Stop == /\ ~owed /\ owed' = TRUE /\ returned' = FALSE
        /\ UNCHANGED <<leader, anchor, child, signaledUnpinned>>

LeaderExit == /\ leader /\ leader' = FALSE
              /\ UNCHANGED <<anchor, child, owed, returned, signaledUnpinned>>

AnchorLost == /\ anchor /\ anchor' = FALSE
              /\ UNCHANGED <<leader, child, owed, returned, signaledUnpinned>>

ChildExit == /\ child /\ child' = FALSE
             /\ UNCHANGED <<leader, anchor, owed, returned, signaledUnpinned>>

(* TERM may be ignored by a resistant child; the sidecar deliberately ignores
   TERM. KILL is allowed only while an identity-backed pin still exists. *)
Term == /\ owed /\ (leader \/ anchor)
        /\ UNCHANGED vars

Kill == /\ owed /\ (leader \/ anchor)
        /\ leader' = FALSE /\ anchor' = FALSE /\ child' = FALSE
        /\ UNCHANGED <<owed, returned, signaledUnpinned>>

Return == /\ owed /\ ~leader /\ ~anchor /\ ~child
          /\ owed' = FALSE /\ returned' = TRUE
          /\ UNCHANGED <<leader, anchor, child, signaledUnpinned>>

Restart == UNCHANGED vars

Next == Stop \/ LeaderExit \/ AnchorLost \/ ChildExit \/ Term \/ Kill \/ Return \/ Restart
Spec == Init /\ [][Next]_vars

NoPrematureReturn == returned => ~leader /\ ~anchor /\ ~child
NoUnpinnedSignal == ~signaledUnpinned
DebtUntilProof == owed /\ child => ~returned

(* When the sidecar survives leader exit, fair escalation eventually clears
   the group and permits Return. AnchorLost is deliberately outside this
   liveness premise; its safe outcome is retained debt. *)
=============================================================================
