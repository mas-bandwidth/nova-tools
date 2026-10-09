------------------------------ MODULE LandPass ------------------------------
\* The lander's gate wait (cmd/nova-sprint/landgate.go, gateRerun's call at
\* :87-89). A red batch asks decide.Gate under a context that ends: the
\* caller's, or one minute, whichever is sooner (context.WithTimeout(ctx,
\* time.Minute); cancel() once the ask returns). On this HEAD a land by hand
\* passes context.Background (land.go cmdLand :357), so the minute is what
\* ends a gate that never answers. The backend that does not answer fails
\* the ask and must not hold the caller (internal/decide/jev.go :24-26).
\* origin had no rowan/landpass-waits-watch-ctx-2026-10-09 (git ls-remote,
\* this sitting); this is the gate on this HEAD, not that branch.
\*
\* The state the wait owns: phase, the clock now, asked (when the ask
\* started), gate (unset until the outside says this gate answers or never
\* does), and ctx (live until the wait abandons it).
\* The outside: Never, the gate does not answer; Answer, it does. Tick is
\* the clock. Abandon is the context ending the wait. Tick does not step
\* into a minute a wait is still open through, unless the witness lets it.
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* each caught by one property below:
\*   "wait"  the minute passes and the gate is still waited: NotWaitedForever
\* NotWaitedForever rejects that witness. The behavior is finite: Ask, then
\* Tick across the minute, phase still "waiting". SilentGateAbandoned is
\* the liveness, under fairness of Tick and Abandon: a never-answering gate
\* is abandoned, not waited forever.

EXTENDS Naturals

CONSTANTS Minute, MaxTime, Broken

VARIABLES phase, now, asked, gate, ctx
vars == <<phase, now, asked, gate, ctx>>

TypeOK ==
  /\ phase \in {"idle", "waiting", "answered", "abandoned"}
  /\ now \in 0..MaxTime
  /\ asked \in 0..MaxTime
  /\ gate \in {"unset", "silent", "answered"}
  /\ ctx \in {"live", "cancelled"}

Due == phase = "waiting" /\ gate # "answered" /\ now + 1 >= asked + Minute

Init ==
  /\ phase = "idle"
  /\ now = 0
  /\ asked = 0
  /\ gate = "unset"
  /\ ctx = "live"

\* The ask (gateRerun calls decide.Gate). Only while the minute still fits
\* on the clock, so a silent gate can reach its deadline.
Ask ==
  /\ phase = "idle"
  /\ now + Minute <= MaxTime
  /\ phase' = "waiting"
  /\ asked' = now
  /\ UNCHANGED <<now, gate, ctx>>

\* The outside event: the gate never answers this wait.
Never ==
  /\ phase = "waiting"
  /\ gate = "unset"
  /\ now < asked + Minute
  /\ gate' = "silent"
  /\ UNCHANGED <<phase, now, asked, ctx>>

\* The outside event: the gate answers before the context ends.
Answer ==
  /\ phase = "waiting"
  /\ gate = "unset"
  /\ phase' = "answered"
  /\ gate' = "answered"
  /\ UNCHANGED <<now, asked, ctx>>

\* The clock. The witness lets it cross a minute the wait is still open
\* through; the design stops on Due and leaves that step to Abandon.
Tick ==
  /\ now < MaxTime
  /\ (Broken = "wait" \/ ~Due)
  /\ now' = now + 1
  /\ UNCHANGED <<phase, asked, gate, ctx>>

\* The context ends the wait (the minute, WithTimeout's cancel).
Abandon ==
  /\ Due
  /\ Broken # "wait"
  /\ phase' = "abandoned"
  /\ ctx' = "cancelled"
  /\ UNCHANGED <<now, asked, gate>>

Next ==
  \/ Ask
  \/ Never
  \/ Answer
  \/ Tick
  \/ Abandon

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick) /\ WF_vars(Abandon)

\* ---------------------------------------------------------------- the rules

\* A wait still open is younger than its minute, and its context is live.
\* The witness is a finite run that is still waiting once the minute has
\* passed.
NotWaitedForever ==
  phase = "waiting" => (ctx = "live" /\ now < asked + Minute)

\* A never-answering gate is abandoned, not waited forever.
SilentGateAbandoned ==
  (phase = "waiting" /\ gate = "silent") ~> (phase = "abandoned")

=============================================================================
