---------------------- MODULE StopVerifierAdmission ----------------------
(* One script read between first admission and a possible model fallback.
   Verify may take 30 minutes or ignore cancellation. STOP must be observable
   while Verify is in flight; a later START does not revive the old admission.
   Final admission is a fresh store read, represented by FinalAdmit. The model
   abstracts the store's claim and generation checks into the machine word. *)
EXTENDS Naturals

CONSTANTS FenceStopVersion, ResponsiveStop
VARIABLES machine, phase, stopVersion, captured, badFallback
vars == <<machine, phase, stopVersion, captured, badFallback>>

Init == /\ machine = "RUNNING" /\ phase = "idle"
        /\ stopVersion = 0 /\ captured = 0 /\ badFallback = FALSE

BeginVerify == /\ machine = "RUNNING" /\ phase = "idle"
               /\ phase' = "verifying" /\ captured' = stopVersion
               /\ UNCHANGED <<machine, stopVersion, badFallback>>

(* A broken start mutex held through verification disables this action. *)
Stop == /\ machine = "RUNNING" /\ stopVersion < 1
        /\ (ResponsiveStop \/ phase # "verifying")
        /\ machine' = "STOPPED" /\ stopVersion' = stopVersion + 1
        /\ UNCHANGED <<phase, captured, badFallback>>

StartMachine == /\ machine = "STOPPED"
                /\ machine' = "RUNNING"
                /\ UNCHANGED <<phase, stopVersion, captured, badFallback>>

(* This action may happen after STOP/START even if cancellation was requested. *)
VerifierReturns == /\ phase = "verifying"
                   /\ phase' = "fallback"
                   /\ UNCHANGED <<machine, stopVersion, captured, badFallback>>

FinalAdmit == /\ phase = "fallback" /\ machine = "RUNNING"
              /\ (captured = stopVersion \/ ~FenceStopVersion)
              /\ phase' = "launched"
              /\ badFallback' = (badFallback \/ captured # stopVersion)
              /\ UNCHANGED <<machine, stopVersion, captured>>

Refuse == /\ phase = "fallback"
          /\ (machine = "STOPPED" \/ (FenceStopVersion /\ captured # stopVersion))
          /\ phase' = "refused"
          /\ UNCHANGED <<machine, stopVersion, captured, badFallback>>

Next == BeginVerify \/ Stop \/ StartMachine \/ VerifierReturns \/ FinalAdmit \/ Refuse
Spec == Init /\ [][Next]_vars

NoStaleFallback == ~badFallback
StopCanInterruptVerifier == (machine = "RUNNING" /\ phase = "verifying" /\ stopVersion = 0) => ENABLED Stop
=============================================================================
