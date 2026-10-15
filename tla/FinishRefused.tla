---------------------------- MODULE FinishRefused ----------------------------
\* A work card's finish the store refuses, as the member holds it (pkg/member/member.go:
\* applyReport, rememberFinish, retryFinishes, handBackFinishRefused, recoverWorking).
\* One card, its claim on this member's row at generation gen. The member runs the card
\* (Launch), its child ends with or without a pushed commit (End), the member sends the
\* finish (Send). A refused finish is remembered, pushed or not: recoverWorking never runs
\* the card again at that generation while it is held, retryFinishes sends the same finish
\* again on its backoff, and after Max refusals of the same text the card is handed back
\* once with stop-return (HandBack), which the store deals again at gen+1.
\*
\* The store's answer is free: it may take a send, refuse it with exit 1, refuse it with
\* exit 2 (nova-sprint's refuse(): the receipt contract, the epoch), or not answer (exit 2
\* classed sprintwire.Unknown: nothing changes, the send is owed again). A refusal that is
\* the same every time (the receipt-contract refusal of hetzner1, 2026-10-15, exit 2) is one
\* of its behaviours. Time (the backoff's 1m, 2m, 8m cap) is below the grain: a retry is an action
\* that may happen at any moment after the refusal.
\*
\* BROKEN names the reversed witness: "forgetunpushed" is the member before the fix, which
\* remembered only a refused finish whose generation pushed, so a refused finish with
\* nothing pushed was forgotten and recoverWorking ran the card again at once.
\* "exit2noanswer" is retryFinishes reading every exit 2 as no answer (m.run, not
\* m.runAnswer): a refusal answered with exit 2 was never counted, so the same finish was
\* sent again every pass and never handed back. "none" is the design.

EXTENDS Naturals

CONSTANTS Max, MaxGen, Broken

VARIABLES
  gen,      \* the claim's generation on this row
  phase,    \* idle (no lane), running (the child at work), ended (finish owed), done (taken)
  pushed,   \* this generation's child pushed a commit
  runs,     \* runs of the card at this generation
  refused,  \* refusals the store answered at this generation (exit 1 or exit 2), the truth
  held,     \* a refused finish is remembered at this generation
  count     \* refusals of the same text in a row, while held

vars == <<gen, phase, pushed, runs, refused, held, count>>

TypeOK ==
  /\ gen \in 1..MaxGen
  /\ phase \in {"idle", "running", "ended", "done"}
  /\ pushed \in BOOLEAN
  /\ runs \in Nat /\ refused \in Nat
  /\ held \in BOOLEAN
  /\ count \in 0..Max

Init ==
  /\ gen = 1 /\ phase = "idle" /\ pushed = FALSE
  /\ runs = 0 /\ refused = 0 /\ held = FALSE /\ count = 0

\* recoverWorking: a card working on this row with no lane of ours is run, unless its
\* refused finish is held at this generation.
Launch ==
  /\ phase = "idle" /\ ~held
  /\ phase' = "running"
  /\ runs' = runs + 1
  /\ UNCHANGED <<gen, pushed, refused, held, count>>

End ==
  /\ phase = "running"
  /\ phase' = "ended"
  /\ pushed' \in BOOLEAN
  /\ UNCHANGED <<gen, runs, refused, held, count>>

\* applyReport: the finish is sent; taken, the card is done here; refused (exit 1, or exit 2
\* with the store's answer: applyReport classes it with runAnswer), the lane is forgotten
\* and the finish remembered (pushed or not: the fix), or forgotten too when nothing was
\* pushed (Broken = "forgetunpushed"). No answer leaves the launch for the next pass: a
\* stutter here.
Send ==
  /\ phase = "ended"
  /\ \/ /\ phase' = "done" /\ UNCHANGED <<held, count, refused>>
     \/ /\ phase' = "idle"
        /\ refused' = refused + 1
        /\ IF Broken = "forgetunpushed" /\ ~pushed
             THEN UNCHANGED <<held, count>>
             ELSE held' = TRUE /\ count' = 1
  /\ UNCHANGED <<gen, pushed, runs>>

\* retryFinishes: the same finish again, below Max refusals of the same text. The answer is
\* taken, a refusal with exit 1, a refusal with exit 2, or none. Only none leaves the count
\* as it is; "exit2noanswer" reads the exit-2 refusal as none.
RetryAnswers == {"taken", "refused1", "refused2", "none"}

Retry ==
  /\ held /\ count < Max
  /\ \E a \in RetryAnswers :
       CASE a = "taken" ->
              /\ phase' = "done" /\ held' = FALSE /\ count' = 0 /\ refused' = refused
         [] a = "none" ->
              UNCHANGED <<phase, held, count, refused>>
         [] a = "refused2" /\ Broken = "exit2noanswer" ->
              /\ refused' = refused + 1 /\ UNCHANGED <<phase, held, count>>
         [] OTHER ->
              /\ refused' = refused + 1 /\ count' = count + 1 /\ UNCHANGED <<phase, held>>
  /\ UNCHANGED <<gen, pushed, runs>>

\* handBackFinishRefused: at Max the card is handed back once; the store deals it again at
\* the next generation, where it may be run again.
HandBack ==
  /\ held /\ count = Max /\ gen < MaxGen
  /\ gen' = gen + 1
  /\ phase' = "idle" /\ pushed' = FALSE
  /\ runs' = 0 /\ refused' = 0 /\ held' = FALSE /\ count' = 0

Next == Launch \/ End \/ Send \/ Retry \/ HandBack

Spec == Init /\ [][Next]_vars

\* A generation is run once: a refused finish never runs the card again at it.
OneRunPerGeneration == runs <= 1

\* A generation's finish is refused at most Max times before it is handed back: a refusal,
\* whatever its exit code, is counted toward the hand-back.
RefusalsBounded == refused <= Max

=============================================================================
