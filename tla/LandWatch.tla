---------------------------- MODULE LandWatch ----------------------------
(* The land watch (nova-tools internal/nsprint/reconcile/land_watch.go,     *)
(* the watcher half; the merge-card cutter is left out: it retires with     *)
(* SPEC-COORDINATOR section 6, the coordinator landing from her session).   *)
(*                                                                          *)
(* One stream. Members enter merging (the move stamps merging_at on the     *)
(* task, or not yet), and leave it (the landing). Every pass the watch      *)
(* stamps first sight (land:merging, HSETNX), drops stamps of members that  *)
(* left, finds the oldest by merging_at (the move's stamp when present,     *)
(* else the watch's first sight), and keeps land:slow: SLOW past Slow,      *)
(* WALL past Wall, deleted below Slow or when merging is empty; one note    *)
(* per episode (oldest@at) per word.                                        *)
(*                                                                          *)
(* Written 2026-09-27 under Glenn's rule: TLA+ for every state machine.     *)
(* Checked with MCLandWatch; findings in FINDINGS.md.                       *)
EXTENDS Naturals, FiniteSets

CONSTANTS Members, Slow, Wall, MaxTime,
          NONE          \* a model value: no stamp, no record, nothing noted
ASSUME Slow < Wall /\ Wall < MaxTime

VARIABLES
  now,        \* the clock
  merging,    \* ws:<s>:merging
  moveAt,     \* task:<m> merging_at: the move's stamp, or NONE
  gen,        \* task:<m> merging_gen: the move's count of entries into merging
  seenGen,    \* land:gen:<s>: the generation the watch saw with its first sight
  stamped,    \* land:merging:<s>: first sight by the watch (a function on a subset of Members)
  slow,       \* land:slow:<s>: [word, oldest, at] or NONE
  noted,      \* land:slow noted:<m>: the words already noted for m in its current stay
  notes,      \* notes sent per member per word during its current stay
  passed      \* TRUE right after a pass, FALSE after any other change

vars == <<now, merging, moveAt, gen, seenGen, stamped, slow, noted, notes, passed>>

Words == {"SLOW", "WALL"}

Init ==
  /\ now = 0
  /\ merging = {}
  /\ moveAt = [m \in Members |-> NONE]
  /\ gen = [m \in Members |-> 0]
  /\ seenGen = [m \in Members |-> 0]
  /\ stamped = [m \in {} |-> 0]
  /\ slow = NONE
  /\ noted = [m \in Members |-> {}]
  /\ notes = [m \in Members |-> [w \in Words |-> 0]]
  /\ passed = FALSE

Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ passed' = FALSE   \* the record is the last pass's; the clock moved since
  /\ UNCHANGED <<merging, moveAt, gen, seenGen, stamped, slow, noted, notes>>

(* The move puts m in merging; it stamps merging_at now, or the stamp   *)
(* arrives later (Stamp): the watch reads whichever is there.           *)
(* The move stamps merging_at now (or not: a hand move); it touches nothing *)
(* of the watch's: stamped and noted are the watch's hashes (Stella's re-  *)
(* entry finding on #4449, 2026-09-27). notes is a ghost counter per stay. *)
(* The fix for L3: every entry into merging goes through the one task     *)
(* writer, which stamps merging_at with the move's time, atomically with   *)
(* the set. The watch reads a stamp newer than its first sight as a new    *)
(* stay (compare-and-reset in Pass).                                       *)
Enter(m) ==
  /\ m \notin merging
  /\ merging' = merging \union {m}
  /\ moveAt' = [moveAt EXCEPT ![m] = now]
  /\ gen' = [gen EXCEPT ![m] = gen[m] + 1]     \* the stay's identity (run 12): the clock can tie, the count cannot
  /\ notes' = [notes EXCEPT ![m] = [w \in Words |-> 0]]
  /\ passed' = FALSE
  /\ UNCHANGED <<now, seenGen, stamped, slow, noted>>

(* No Stamp action any more: the stamp is written by the one task writer *)
(* atomically with the entry, so it can never arrive later or older (L1's *)
(* scenario is unreachable in the code; keying the episode on first sight *)
(* stays as defence in depth).                                            *)

Leave(m) ==
  /\ m \in merging
  /\ merging' = merging \ {m}
  /\ passed' = FALSE
  /\ UNCHANGED <<now, moveAt, gen, seenGen, stamped, slow, noted, notes>>

(* at(m) under the stamps a pass has: the move's, else first sight. *)
At(st, m) == IF moveAt[m] # NONE THEN moveAt[m] ELSE st[m]

Oldest(st) == CHOOSE m \in merging : \A o \in merging : At(st, m) <= At(st, o)

(* NewStay(m): the record's stamp is newer than the watch's first sight, *)
(* so m left and came back between passes (L3): first sight and the noted *)
(* words start again.                                                     *)
NewStay(m) == m \in DOMAIN stamped /\ gen[m] # seenGen[m]

Pass ==
  LET st == [m \in merging |-> IF m \in DOMAIN stamped /\ ~NewStay(m) THEN stamped[m] ELSE now]
      kept == [m \in Members |-> IF m \in merging /\ ~NewStay(m) THEN noted[m] ELSE {}]
  IN
  /\ stamped' = st
  /\ seenGen' = [m \in Members |-> IF m \in merging THEN gen[m] ELSE 0]
  /\ passed' = TRUE
  /\ UNCHANGED <<now, merging, moveAt, gen>>
  /\ IF merging = {} THEN
       /\ slow' = NONE
       /\ noted' = [m \in Members |-> {}]   \* the record is deleted with its noted fields
       /\ UNCHANGED notes
     ELSE
       LET o == Oldest(st)
           age == now - At(st, o)
       IN
       IF age < Slow THEN
         /\ slow' = NONE
         /\ noted' = kept  \* L2: noted follows the member, not the record; L3: a new stay starts clean
         /\ UNCHANGED notes
       ELSE
         LET word == IF age >= Wall THEN "WALL" ELSE "SLOW"
         IN
         /\ slow' = [word |-> word, oldest |-> o, at |-> At(st, o)]
         /\ IF word \in kept[o] THEN
              /\ noted' = kept
              /\ UNCHANGED notes
            ELSE
              /\ noted' = [kept EXCEPT ![o] = kept[o] \union {word}]
              /\ notes' = [notes EXCEPT ![o] = [notes[o] EXCEPT ![word] = notes[o][word] + 1]]

Next ==
  \/ Tick
  \/ Pass
  \/ \E m \in Members : Enter(m) \/ Leave(m)

Spec == Init /\ [][Next]_vars /\ WF_vars(Pass) /\ WF_vars(Tick)

----------------------------------------------------------------------------
(* Invariants *)

TypeOK ==
  /\ now \in 0..MaxTime
  /\ gen \in [Members -> Nat] /\ seenGen \in [Members -> Nat]
  /\ merging \subseteq Members
  /\ DOMAIN stamped \subseteq Members
  /\ slow = NONE \/ slow.oldest \in Members

(* After a pass the watch's stamps are exactly the members in merging. *)
StampsMatch == passed => DOMAIN stamped = merging

(* After a pass the slow record stands iff the oldest is past Slow. *)
SlowIffLate ==
  passed =>
    (slow # NONE) = (merging # {} /\ now - At(stamped, Oldest(stamped)) >= Slow)

(* One note per word for one stay of a member in merging. *)
OneNotePerStay == \A m \in Members : \A w \in Words : notes[m][w] <= 1

(* WALL means the record says stalled and the oldest is past Wall. *)
WallIsLate == passed /\ slow # NONE /\ slow.word = "WALL" => now - slow.at >= Wall

----------------------------------------------------------------------------
GenBound == \A m \in Members : gen[m] <= 3   \* the instance: three stays per member

(* Liveness: a member past Wall that stays is reported WALL (never hangs). *)
Stalled(m) == m \in merging /\ m \in DOMAIN stamped /\ now - At(stamped, m) >= Wall
Reported == slow # NONE /\ slow.word = "WALL"
WallReported == \A m \in Members : [](Stalled(m) => <>(Reported \/ m \notin merging))

(* A new stay is eventually noted when it lasts: the property Stella asked *)
(* for (L3). The alarm is the stream's and names the oldest member, so the *)
(* member the property follows is the oldest late one: in its current stay *)
(* it gets its note, or it leaves, or another member becomes the oldest.   *)
(* notes counts the current stay only.                                     *)
Late(m) == m \in merging /\ m \in DOMAIN stamped /\ now - At(stamped, m) >= Slow
IsOldest(m) == m \in merging /\ m = Oldest(stamped)
Noted(m) == notes[m]["SLOW"] >= 1 \/ notes[m]["WALL"] >= 1   \* a stay past Wall between passes gets the WALL note straight away
StayNoted == \A m \in Members :
  [](Late(m) /\ IsOldest(m) => <>(Noted(m) \/ m \notin merging \/ ~IsOldest(m)))

=============================================================================
