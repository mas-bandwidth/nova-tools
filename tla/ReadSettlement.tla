-------------------------- MODULE ReadSettlement --------------------------
(* One read ID across two queue leases. Old and New include epoch, generation,
   owner, and attempt; equality is the exact lease fence used by ACTIVE and
   SETTLEMENT. The server remembers operation tokens after a lost RPC reply.
   A crash erases only volatile response state when PersistIntent is true.
   Fair progress assumes a recoverable native group, healthy disk and server,
   and at most one crash. StaleAdvance explores a newer queue lease while an
   old ACTIVE still exists; it is excluded from the progress configuration. *)
EXTENDS Naturals, FiniteSets

CONSTANTS None, StableOp, PersistIntent, FenceActive, FenceLease,
          RequireQuiescence,
          StaleAdvance, CheckStop, IgnoreActiveOnRecovery
Old == [id |-> "read-1", epoch |-> "epoch-1", gen |-> 1,
        owner |-> "friend-a", attempt |-> 1]
New == [id |-> "read-1", epoch |-> "epoch-1", gen |-> 2,
        owner |-> "friend-b", attempt |-> 2]

VARIABLES current, phase, active, live, intent, token, remote, effects,
          launches, reply, crashed, debt, returned, badLaunch, badReturn,
          badCleanup
vars == <<current, phase, active, live, intent, token, remote, effects,
          launches, reply, crashed, debt, returned, badLaunch, badReturn,
          badCleanup>>
Leases == {Old, New}
Tokens == {1, 2}

TypeOK == /\ current \in Leases
          /\ phase \in {"asked", "running", "complete", "settling", "recover", "done"}
          /\ active \in Leases \cup {None} /\ live \in BOOLEAN
          /\ intent \in Leases \cup {None} /\ token \in Tokens
          /\ remote \subseteq (Leases \X Tokens)
          /\ effects \in [Leases -> 0..2] /\ launches \in [Leases -> 0..2]
          /\ reply \in BOOLEAN /\ crashed \in BOOLEAN
          /\ debt \in Leases \cup {None} /\ returned \subseteq Leases
          /\ badLaunch \in BOOLEAN /\ badReturn \in BOOLEAN
          /\ badCleanup \in BOOLEAN

Init == /\ current = Old /\ phase = "asked" /\ active = None /\ live = FALSE
        /\ intent = None /\ token = 1 /\ remote = {}
        /\ effects = [l \in Leases |-> 0] /\ launches = [l \in Leases |-> 0]
        /\ reply = FALSE /\ crashed = FALSE /\ debt = None /\ returned = {}
        /\ badLaunch = FALSE /\ badReturn = FALSE /\ badCleanup = FALSE

(* The old ACTIVE is a durable native-child lease. An asked newer generation
   cannot replace it, even if the old queue entry has disappeared. *)
Launch == /\ phase = "asked" /\ (active = None \/ ~FenceActive)
          /\ launches' = [launches EXCEPT ![current] = @ + 1]
          /\ badLaunch' = (badLaunch \/ active # None \/ launches[current] # 0)
          /\ active' = current /\ live' = TRUE /\ phase' = "running"
          /\ UNCHANGED <<intent, token, remote, effects, reply, crashed,
                        debt, returned, badReturn, badCleanup, current>>

(* A bounded, fair child exit supplies group quiescence before settlement. *)
Complete == /\ phase = "running" /\ active = current /\ live
            /\ live' = FALSE /\ phase' = "complete"
            /\ UNCHANGED <<current, active, intent, token, remote, effects,
                          launches, reply, crashed, debt, returned, badLaunch,
                          badReturn, badCleanup>>

SaveIntent == /\ (phase \in {"complete", "settling"} \/
                  (~RequireQuiescence /\ phase = "running"))
              /\ intent = None /\ (~live \/ ~RequireQuiescence)
              /\ active = current
              /\ intent' = current
              /\ token' = IF StableOp \/ ~crashed THEN 1 ELSE 2
              /\ phase' = "settling"
              /\ UNCHANGED <<current, active, live, remote, effects, launches,
                            reply, crashed, debt, returned, badLaunch,
                            badReturn, badCleanup>>

(* Same-op replay has one server effect. An alternate token after a lost
   response makes a second effect, which NoDuplicateEffect detects. *)
Commit == /\ phase = "settling" /\ intent = current /\ ~reply
          /\ LET op == <<intent, token>> IN
             /\ remote' = remote \cup {op}
             /\ effects' = IF op \in remote THEN effects
                            ELSE [effects EXCEPT ![intent] = @ + 1]
          /\ reply' = TRUE
          /\ UNCHANGED <<current, phase, active, live, intent, token,
                        launches, crashed, debt, returned, badLaunch,
                        badReturn, badCleanup>>

(* Exactly one lost reply after remote commit. Durable intent stays on disk in
   the normal case; the broken variant forgets it and may mint a new token. *)
Crash == /\ reply /\ ~crashed /\ phase = "settling"
         /\ reply' = FALSE /\ crashed' = TRUE /\ phase' = "recover"
         /\ intent' = IF PersistIntent THEN intent ELSE None
         /\ UNCHANGED <<current, active, live, token, remote, effects,
                       launches, debt, returned, badLaunch, badReturn, badCleanup>>

(* Reboot chooses from durable ACTIVE and SETTLEMENT, not its erased goroutine
   state. Ignoring an old ACTIVE after losing intent can choose a fresh launch;
   the broken configuration also disables the final ACTIVE launch fence. *)
Recover == /\ phase = "recover"
           /\ phase' = IF IgnoreActiveOnRecovery THEN "asked"
                        ELSE IF active = current
                          THEN IF intent = current THEN "settling"
                               ELSE IF live THEN "running" ELSE "complete"
                          ELSE "asked"
           /\ UNCHANGED <<current, active, live, intent, token, remote, effects,
                         launches, reply, crashed, debt, returned, badLaunch,
                         badReturn, badCleanup>>

Cleanup == /\ reply /\ phase = "settling" /\ intent = current
           /\ active = current /\ (~live \/ ~RequireQuiescence)
           /\ badCleanup' = (badCleanup \/ live \/ intent # active)
           /\ active' = None /\ intent' = None /\ phase' = "done"
           /\ UNCHANGED <<current, live, token, remote, effects, launches,
                         reply, crashed, debt, returned, badLaunch, badReturn>>

Advance == /\ phase = "done" /\ current = Old
           /\ current' = New /\ phase' = "asked" /\ reply' = FALSE
           /\ UNCHANGED <<active, live, intent, token, remote, effects,
                         launches, crashed, debt, returned, badLaunch,
                         badReturn, badCleanup>>

(* The store may expose a newer generation while an older receipt remains.
   This control tests the launch fence, not progress of that superseded read. *)
AdvanceEarly == /\ StaleAdvance /\ current = Old /\ phase # "done"
                /\ current' = New /\ phase' = "asked" /\ reply' = FALSE
                /\ UNCHANGED <<active, live, intent, token, remote, effects,
                              launches, crashed, debt, returned, badLaunch,
                              badReturn, badCleanup>>

MarkDebt == /\ CheckStop /\ active = Old /\ debt = None
            /\ debt' = Old
            /\ UNCHANGED <<current, phase, active, live, intent, token, remote,
                          effects, launches, reply, crashed, returned, badLaunch,
                          badReturn, badCleanup>>

(* The STOP return must use the saved lease, including owner and attempt, even
   if the visible queue has advanced to New. *)
ReturnDebt == /\ CheckStop /\ debt = Old /\ ~live
              /\ LET target == IF FenceLease THEN debt ELSE current IN
                 /\ returned' = returned \cup {target}
                 /\ badReturn' = (badReturn \/ target # debt)
              /\ debt' = None
              /\ UNCHANGED <<current, phase, active, live, intent, token,
                            remote, effects, launches, reply, crashed,
                            badLaunch, badCleanup>>

Next == Launch \/ Complete \/ SaveIntent \/ Commit \/ Crash \/ Recover \/ Cleanup
        \/ Advance \/ AdvanceEarly \/ MarkDebt \/ ReturnDebt
Fairness == /\ WF_vars(Complete) /\ WF_vars(SaveIntent)
            /\ WF_vars(Commit) /\ WF_vars(Recover) /\ WF_vars(Cleanup)
Spec == Init /\ [][Next]_vars /\ Fairness

NoDuplicateEffect == \A l \in Leases : effects[l] <= 1
NoDuplicateLaunch == \A l \in Leases : launches[l] <= 1
NoActiveOverwrite == ~badLaunch
ExactStopReturn == ~badReturn
NoUnsafeCleanup == ~badCleanup
OldEventuallySettles == (launches[Old] = 1) ~> (phase = "done" \/ current = New)
=============================================================================
