----------------------------- MODULE MemberSlot -----------------------------
\* The life of one launch's slot on a fleet member (cmd/nova-swarm slotclean.go;
\* internal/member Ender and Holder; docs/SPEC-SWARM.md, `member`): staged and
\* working under a live claim, reported, taken or refused by the sprint, retired
\* to a small done entry, and gone; the cleaner apart from the pass, the sweep
\* by the queue's word, and the cap over the slots directory.
\*
\* THE STATE, per slot (a launch name).
\*   st    none (no launch), staged (Start claimed the name and made the
\*         directory), working (the child at work), reported (the child ended
\*         and the member sent its finish or return), refused (the sprint
\*         refused the report: the slot is kept), orphan (the member died with
\*         the slot staged, working or accepted: the accepted tag is the
\*         member's memory only and the crash lost it; no live claim), accepted
\*         (the sprint took the word, or its queue let the card go: tagged for
\*         the cleaner), kept
\*         (retired: the checkout gone, the done entry under <slots>/done),
\*         gone (the done entry removed)
\*   held  the sprint's queue holds the card as this member's
\*   acc   the sprint took the slot's word: its report accepted, or its card
\*         gone from the queue (landed, dropped, dealt elsewhere)
\*   from  the state the slot's checkout was removed from ("none" until then):
\*         WorkingNeverRemoved reads it
\*   born  the order the done entries were made in: the cap evicts the oldest
\*         first (0: no entry yet)
\*   n     the done entries made so far
\*   full  the cleaner's last measure found the slots over the cap with no done
\*         entry left to evict: the member takes no card
\*   measured  the last step was the cleaner's cap round
\*   over  a card was staged while full (the take the cap must refuse)
\*
\* THE SIZE. A slot with a checkout (staged, working, reported, refused, orphan,
\* accepted) weighs Checkout; a done entry weighs 1; none and gone weigh nothing.
\* Cap is the slots directory's cap in the same unit.
\*
\* THE CLEANER'S STEPS are Retire, Sweep, Expire and CapRound; the pass's are
\* Stage, Run and End; the sprint's are Accept, Refuse and Let; Crash and
\* Recover are the member dying and starting again. The cleaner is apart from
\* the pass: no action here is both.
\*
\* BROKEN names a reversed witness, a rule as it could be written wrong, that
\* TLC must break: "sweepworking" (the sweep retires a slot whose claim is live),
\* "sweepskipsorphan" (the sweep skips an orphan: a crash-left or
\* accepted-then-crashed leftover is never re-found), "refusedremoved" (a refused
\* report retires the slot at once), "capignored" (the cap round evicts nothing),
\* "takesoverfull" (a card is staged while the slots are full), "noexpire" (a
\* done entry is never removed: the day never passes). "none" is the design.
\*
\* WHAT IS NOT MODELLED. The pid file and the ten minutes still (both say
\* "nothing runs it" in the code, here ~held and no live claim), the shared Go
\* caches (never a slot's), the bound of 32 a round, and the clock: a day passing
\* is Expire being enabled, and the 24 hours are the code's constant.
EXTENDS Integers, FiniteSets

CONSTANTS Slots, Cap, Checkout, Broken

VARIABLES st, held, acc, from, born, n, full, measured, over

vars == <<st, held, acc, from, born, n, full, measured, over>>

States == {"none", "staged", "working", "reported", "refused", "orphan", "accepted", "kept", "gone"}

WithCheckout == {"staged", "working", "reported", "refused", "orphan", "accepted"}

Kept == {s \in Slots : st[s] = "kept"}

Size == Checkout * Cardinality({s \in Slots : st[s] \in WithCheckout}) + Cardinality(Kept)

Init ==
  /\ st = [s \in Slots |-> "none"]
  /\ held = [s \in Slots |-> FALSE]
  /\ acc = [s \in Slots |-> FALSE]
  /\ from = [s \in Slots |-> "none"]
  /\ born = [s \in Slots |-> 0]
  /\ n = 0
  /\ full = FALSE
  /\ measured = FALSE
  /\ over = FALSE

\* The pass takes a card and stages its slot; the sprint's queue holds the card
\* from here. Under the cap's refusal (full) it takes none.
Stage(s) ==
  /\ st[s] = "none"
  /\ (~full \/ Broken = "takesoverfull")
  /\ st' = [st EXCEPT ![s] = "staged"]
  /\ held' = [held EXCEPT ![s] = TRUE]
  /\ over' = over \/ full
  /\ measured' = FALSE
  /\ UNCHANGED <<acc, from, born, n, full>>

Run(s) ==
  /\ st[s] = "staged"
  /\ st' = [st EXCEPT ![s] = "working"]
  /\ measured' = FALSE
  /\ UNCHANGED <<held, acc, from, born, n, full, over>>

\* The member dies: the slot stays with no live claim. An accepted slot's tag
\* is the member's memory only, so the crash loses it and the checkout is left
\* for the sweep to re-find (cmd/nova-swarm slotclean.go, sweep).
Crash(s) ==
  /\ st[s] \in {"staged", "working", "accepted"}
  /\ st' = [st EXCEPT ![s] = "orphan"]
  /\ measured' = FALSE
  /\ UNCHANGED <<held, acc, from, born, n, full, over>>

\* The member starts again and its first queue still holds the card: it runs
\* the card again under the same name (internal/member recoverWorking).
Recover(s) ==
  /\ st[s] = "orphan" /\ held[s]
  /\ st' = [st EXCEPT ![s] = "working"]
  /\ measured' = FALSE
  /\ UNCHANGED <<held, acc, from, born, n, full, over>>

\* The child ends. With the card held, the member reports it; with the card gone
\* from the queue, the launch is reaped: accepted, nothing to report to.
End(s) ==
  /\ st[s] = "working"
  /\ IF held[s]
       THEN st' = [st EXCEPT ![s] = "reported"] /\ acc' = acc
       ELSE st' = [st EXCEPT ![s] = "accepted"] /\ acc' = [acc EXCEPT ![s] = TRUE]
  /\ measured' = FALSE
  /\ UNCHANGED <<held, from, born, n, full, over>>

Accept(s) ==
  /\ st[s] = "reported"
  /\ st' = [st EXCEPT ![s] = "accepted"]
  /\ acc' = [acc EXCEPT ![s] = TRUE]
  /\ held' = [held EXCEPT ![s] = FALSE]
  /\ measured' = FALSE
  /\ UNCHANGED <<from, born, n, full, over>>

Refuse(s) ==
  /\ st[s] = "reported"
  /\ st' = [st EXCEPT ![s] = IF Broken = "refusedremoved" THEN "accepted" ELSE "refused"]
  /\ measured' = FALSE
  /\ UNCHANGED <<held, acc, from, born, n, full, over>>

\* The sprint lets the card go: it landed, was dropped, or was dealt elsewhere.
Let(s) ==
  /\ held[s] /\ st[s] \in {"staged", "working", "reported", "refused", "orphan"}
  /\ held' = [held EXCEPT ![s] = FALSE]
  /\ measured' = FALSE
  /\ UNCHANGED <<st, acc, from, born, n, full, over>>

\* The cleaner retires a tagged launch: the checkout goes, the done entry is made.
Retire(s) ==
  /\ st[s] = "accepted"
  /\ st' = [st EXCEPT ![s] = "kept"]
  /\ from' = [from EXCEPT ![s] = "accepted"]
  /\ born' = [born EXCEPT ![s] = n + 1]
  /\ n' = n + 1
  /\ measured' = FALSE
  /\ UNCHANGED <<held, acc, full, over>>

\* The sweep retires a slot the queue no longer holds and nothing runs: a crash
\* or a kill left it (a staged, working or accepted-then-crashed slot: an
\* orphan, no live claim), or a refusal kept it. The sprint's letting go is its
\* word.
Sweep(s) ==
  /\ ~held[s]
  /\ \/ st[s] = "refused"
     \/ Broken # "sweepskipsorphan" /\ st[s] = "orphan"
     \/ Broken = "sweepworking" /\ st[s] \in {"staged", "working"}
  /\ st' = [st EXCEPT ![s] = "kept"]
  /\ from' = [from EXCEPT ![s] = st[s]]
  /\ acc' = [acc EXCEPT ![s] = TRUE]
  /\ born' = [born EXCEPT ![s] = n + 1]
  /\ n' = n + 1
  /\ measured' = FALSE
  /\ UNCHANGED <<held, full, over>>

\* The day passes: the done entry goes.
Expire(s) ==
  /\ Broken # "noexpire"
  /\ st[s] = "kept"
  /\ st' = [st EXCEPT ![s] = "gone"]
  /\ measured' = FALSE
  /\ UNCHANGED <<held, acc, from, born, n, full, over>>

\* The k oldest done entries.
Oldest(k) == {s \in Kept : Cardinality({t \in Kept : born[t] < born[s]}) < k}

Need == IF Size > Cap THEN Size - Cap ELSE 0

Evicted ==
  IF Broken = "capignored" THEN {}
  ELSE Oldest(IF Need < Cardinality(Kept) THEN Need ELSE Cardinality(Kept))

\* The cap round: the cleaner measures the slots and, over the cap, removes done
\* entries oldest first until under; what is left over is working slots, never
\* removed, and the member takes no card (full) until they end.
CapRound ==
  /\ st' = [s \in Slots |-> IF s \in Evicted THEN "gone" ELSE st[s]]
  /\ full' = (Size - Cardinality(Evicted) > Cap)
  /\ measured' = TRUE
  /\ UNCHANGED <<held, acc, from, born, n, over>>

Next ==
  \/ \E s \in Slots : Stage(s) \/ Run(s) \/ Crash(s) \/ Recover(s) \/ End(s)
                        \/ Accept(s) \/ Refuse(s) \/ Let(s)
                        \/ Retire(s) \/ Sweep(s) \/ Expire(s)
  \/ CapRound

\* The cleaner is fair to what is tagged, to the leftover the sweep re-finds and
\* to the day: a tagged launch is retired, a leftover is swept and a done entry
\* expires, whatever else goes on.
Spec == Init /\ [][Next]_vars /\ \A s \in Slots : WF_vars(Retire(s)) /\ WF_vars(Sweep(s)) /\ WF_vars(Expire(s))

TypeOK ==
  /\ st \in [Slots -> States]
  /\ held \in [Slots -> BOOLEAN]
  /\ acc \in [Slots -> BOOLEAN]
  /\ from \in [Slots -> States]
  /\ born \in [Slots -> 0..Cardinality(Slots)]
  /\ n \in 0..Cardinality(Slots)
  /\ full \in BOOLEAN /\ measured \in BOOLEAN /\ over \in BOOLEAN

\* A working slot is never removed: no checkout went while its claim was live.
WorkingNeverRemoved == \A s \in Slots : from[s] \notin {"staged", "working"}

\* A removed checkout's card was accepted, landed or dropped: the sprint's word
\* came first.
CheckoutGoneOnlyAccepted == \A s \in Slots : st[s] \in {"kept", "gone"} => acc[s]

\* After a cap round the slots are under the cap, or only working slots remain.
CapHeld == measured => (Size <= Cap \/ Kept = {})

\* No card is staged while the slots are full.
TakesRefusedOverCap == ~over

\* Every accepted slot is eventually removed, even one a crash left for the
\* sweep to re-find.
EveryAcceptedGoes == \A s \in Slots : (st[s] = "accepted") ~> (st[s] = "gone")
=============================================================================
