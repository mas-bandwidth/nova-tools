------------------------------- MODULE Redo -------------------------------
(***************************************************************************)
(* nova-sprint redo (internal/sprint/steps_redo.go, docs/SPEC-SPRINT.md     *)
(* section 7, redo; nova-tools card verb-redo, 2026-10-04): the three verbs *)
(* a conflict took, return, rework "redo the same change on the current     *)
(* tip" and resume, as one step. One stream and its cards, abstracted to    *)
(* what the three verbs and redo read and write: the work column of each    *)
(* primary (col), its merge card's column (mq), the stream's state, cause   *)
(* and the card it stopped on, each primary's attempt and returns, the      *)
(* judgments open (the stream's, and a primary's returned to review), and   *)
(* whether a member has room. Each verb is a function of the whole state,   *)
(* as the Go steps are functions of a snapshot (sprint.Return, Rework,      *)
(* Resume, Redo), so the model states the claim directly:                   *)
(*                                                                         *)
(*   RedoIsTheThree  in every reachable state, for every card in a          *)
(*                   conflict, Redo(S, c) is Resume(Rework(Return(S, c)))   *)
(*                   (the Go test TestRedoReturnsReworksAndResumesInOneVerb *)
(*                   holds the same on the tables);                         *)
(*   RedoOnConflict  redo moves only a card in a conflict (ghost redoOff);  *)
(*   StoppedNamesItsCard  a stream stopped on a conflict names its card.     *)
(*                   It does not hold that card stuck: between return and   *)
(*                   resume the three verbs leave the stream stopped on a   *)
(*                   card in review (TLC found it on the first run of this  *)
(*                   model: Conflict, then Return); redo has no such step.  *)
(*   MovesLegal      every primary move is a row of the lifecycle           *)
(*                   (internal/sprint/lifecycle.go Moves, with redo's       *)
(*                   merging -> working and merging -> ready).              *)
(*                                                                         *)
(* Out of scope: readers and reads (an accept here stands for reads that   *)
(* passed), members by name, routes, the queue's order, the tick's pump,    *)
(* a cross stop, friends' cards; Broken names a reversed witness.           *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS
  Cards,      \* the primaries of the stream, e.g. {c1, c2}
  MaxAtt,     \* attempts per card in the model
  MaxRet,     \* returns per card in the model
  Broken      \* "none", "noresume" (redo leaves the stream stopped), "offconflict" (redo's guard is merging alone)

None == "none"
Cols == {"ready", "working", "review", "merging", "landed"}
MCols == {"none", "queued", "stuck", "returned", "merged"}
States == {"merging", "stopped", "waiting"}

\* the lifecycle's rows this model can make (lifecycle.go Moves)
Legal == {<<"ready", "working">>, <<"working", "review">>, <<"review", "merging">>,
          <<"review", "working">>, <<"review", "ready">>, <<"merging", "review">>,
          <<"merging", "landed">>, <<"merging", "working">>, <<"merging", "ready">>}

VARIABLES S, redoOff

vars == <<S, redoOff>>

Init ==
  /\ S = [col |-> [c \in Cards |-> "merging"], mq |-> [c \in Cards |-> "queued"],
          st |-> "merging", cause |-> None, scard |-> None,
          att |-> [c \in Cards |-> 1], ret |-> [c \in Cards |-> 0],
          jstream |-> FALSE, jret |-> [c \in Cards |-> FALSE], room |-> TRUE]
  /\ redoOff = FALSE

TypeOK ==
  /\ S.col \in [Cards -> Cols]
  /\ S.mq \in [Cards -> MCols]
  /\ S.st \in States
  /\ S.cause \in {None, "conflict", "red"}
  /\ S.scard \in Cards \cup {None}
  /\ S.att \in [Cards -> 1..MaxAtt]
  /\ S.ret \in [Cards -> 0..MaxRet]
  /\ S.jstream \in BOOLEAN
  /\ S.jret \in [Cards -> BOOLEAN]
  /\ S.room \in BOOLEAN
  /\ redoOff \in BOOLEAN

Left(T, x) == {d \in Cards \ {x} : T.mq[d] \in {"queued", "stuck"}}

\* a card is in a conflict: merging, its merge card stuck, its stream stopped
\* with cause conflict on it (steps_redo.go redoWhy)
InConflict(T, c) == T.col[c] = "merging" /\ T.mq[c] = "stuck" /\ T.st = "stopped" /\ T.cause = "conflict" /\ T.scard = c

\* return (sprint.Return): merging -> review, off the merge queue into
\* returned, the returned judgment opened; a stream merging with nothing left
\* waits (settle); a stopped stream stays stopped
ReturnF(T, c) ==
  [T EXCEPT !.col[c] = "review", !.mq[c] = "returned", !.ret[c] = @ + 1, !.jret[c] = TRUE,
            !.st = IF T.st = "merging" /\ Left(T, c) = {} THEN "waiting" ELSE @]

\* rework (sprint.Rework): review -> working when a member has room, else
\* ready; the next attempt; the returned judgment answered
ReworkF(T, c) ==
  [T EXCEPT !.col[c] = IF T.room THEN "working" ELSE "ready", !.att[c] = @ + 1, !.jret[c] = FALSE]

\* resume (sprint.Resume): stuck -> queued, the stream merging when anything
\* is queued, else waiting; its cause and card cleared, its judgments answered
ResumeF(T) ==
  [T EXCEPT !.mq = [d \in Cards |-> IF T.mq[d] = "stuck" THEN "queued" ELSE T.mq[d]],
            !.st = IF \E d \in Cards : T.mq[d] \in {"queued", "stuck"} THEN "merging" ELSE "waiting",
            !.cause = None, !.scard = None, !.jstream = FALSE]

\* redo (sprint.Redo): the three in one unit
RedoF(T, c) ==
  LET moved == [T EXCEPT !.col[c] = IF T.room THEN "working" ELSE "ready", !.mq[c] = "returned",
                         !.att[c] = @ + 1, !.ret[c] = @ + 1]
      others == Left(T, c)
  IN IF Broken = "noresume" THEN moved
     ELSE [moved EXCEPT !.mq = [d \in Cards |-> IF d # c /\ moved.mq[d] = "stuck" THEN "queued" ELSE moved.mq[d]],
                        !.st = IF others # {} THEN "merging" ELSE "waiting",
                        !.cause = None, !.scard = None, !.jstream = FALSE]

RedoGuard(T, c) == IF Broken = "offconflict" THEN T.col[c] = "merging" ELSE InConflict(T, c)

\* --- the outside: the lander's facts, workers, readers, the fleet ---

Conflict(c) ==
  /\ S.st = "merging" /\ S.mq[c] = "queued"
  /\ S' = [S EXCEPT !.st = "stopped", !.cause = "conflict", !.scard = c, !.mq[c] = "stuck", !.jstream = TRUE]
  /\ UNCHANGED redoOff

Red ==
  /\ S.st = "merging"
  /\ S' = [S EXCEPT !.st = "stopped", !.cause = "red", !.scard = None, !.jstream = TRUE]
  /\ UNCHANGED redoOff

Merge(c) ==
  /\ S.st = "merging" /\ S.mq[c] = "queued"
  /\ S' = [S EXCEPT !.col[c] = "landed", !.mq[c] = "merged",
                    !.st = IF Left(S, c) = {} THEN "waiting" ELSE "merging"]
  /\ UNCHANGED redoOff

Deal(c) ==
  /\ S.col[c] = "ready" /\ S.room
  /\ S' = [S EXCEPT !.col[c] = "working"]
  /\ UNCHANGED redoOff

Finish(c) ==
  /\ S.col[c] = "working"
  /\ S' = [S EXCEPT !.col[c] = "review"]
  /\ UNCHANGED redoOff

\* reads passed and the card accepted: into the queue, from returned or new
Accept(c) ==
  /\ S.col[c] = "review"
  /\ S' = [S EXCEPT !.col[c] = "merging", !.mq[c] = "queued", !.jret[c] = FALSE,
                    !.st = IF S.st = "waiting" THEN "merging" ELSE @]
  /\ UNCHANGED redoOff

Room ==
  /\ S' = [S EXCEPT !.room = ~@]
  /\ UNCHANGED redoOff

\* --- the coordinator's verbs ---

Return(c) ==
  /\ S.col[c] = "merging" /\ S.mq[c] \in {"queued", "stuck"} /\ S.ret[c] < MaxRet
  /\ S' = ReturnF(S, c)
  /\ UNCHANGED redoOff

Rework(c) ==
  /\ S.col[c] = "review" /\ S.att[c] < MaxAtt
  /\ S' = ReworkF(S, c)
  /\ UNCHANGED redoOff

Resume ==
  /\ S.st = "stopped"
  /\ S' = ResumeF(S)
  /\ UNCHANGED redoOff

Redo(c) ==
  /\ RedoGuard(S, c) /\ S.att[c] < MaxAtt /\ S.ret[c] < MaxRet
  /\ S' = RedoF(S, c)
  /\ redoOff' = (redoOff \/ ~InConflict(S, c))

Next ==
  \/ \E c \in Cards : Conflict(c) \/ Merge(c) \/ Deal(c) \/ Finish(c) \/ Accept(c)
                      \/ Return(c) \/ Rework(c) \/ Redo(c)
  \/ Red \/ Room \/ Resume

Spec == Init /\ [][Next]_vars

\* --- what it proves ---

RedoIsTheThree ==
  \A c \in Cards : (InConflict(S, c) /\ S.att[c] < MaxAtt /\ S.ret[c] < MaxRet) =>
    RedoF(S, c) = ResumeF(ReworkF(ReturnF(S, c), c))

RedoOnConflict == ~redoOff

StoppedNamesItsCard == (S.st = "stopped" /\ S.cause = "conflict") => S.scard \in Cards

\* a stopped stream has its judgment open, and only a stopped one
JudgedWhileStopped == S.jstream <=> S.st = "stopped"

MovesLegal ==
  [][\A c \in Cards : S'.col[c] # S.col[c] => <<S.col[c], S'.col[c]>> \in Legal]_vars
=============================================================================
