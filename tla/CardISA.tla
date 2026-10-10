---------------------------- MODULE CardISA ----------------------------
(***************************************************************************)
(* The card instruction set (docs/SPEC-ISA.md, layer 1): a card is one      *)
(* instruction, and its kind says what operands and results it carries.     *)
(*                                                                         *)
(* This module does not restate the card life; it EXTENDS CardMachine and   *)
(* reuses its actions. Every life action below is the base action, called   *)
(* through Frame(A) == A /\ UNCHANGED <<the ISA's own variables>>; the      *)
(* base predicate still names the Lua it was read from. What this module    *)
(* adds is what the kinds add:                                             *)
(*                                                                         *)
(*   - the one `wait` kind. Today a card waits for one of several          *)
(*     unrelated reasons, each its own path (admitted held, a sentinel, a   *)
(*     wave, DEPENDS-ON, a proposed external wait). CardMachine's Release   *)
(*     is the `release` path alone. This module replaces Release with ONE   *)
(*     action, Wait(c), guarded by ONE predicate, OperandHolds(c), whose    *)
(*     operand is WaitFor[c]: another card id (today's needs), "release"    *)
(*     (today's held and the wave behind it), or an external operand, one  *)
(*     of Ext (layer 3: `pr <repo>#<n> merged`, `<branch> contains <sha>`,  *)
(*     `after <RFC3339>`). The paths are data now, not code.               *)
(*                                                                         *)
(*     An external operand is a fact of the outside, which only comes to    *)
(*     hold (a merged PR stays merged, a time once past stays past). The    *)
(*     tick asks each distinct operand once (IsaTick: one gh or git call    *)
(*     per operand, not per card) and keeps the answer for that tick in     *)
(*     `answer`; the wait's one guard reads the answer, never the outside   *)
(*     directly, so a card is released the first tick its operand holds.   *)
(*                                                                         *)
(*     Replaced: CardMachine.Release. Also replaced, so a waiting card can  *)
(*     never be dispatched around the operand: CardMachine.DealWork (the    *)
(*     base deal reads waiting as well as ready, line 165) is here Deal,    *)
(*     which deals a ready card alone; a waiting card becomes ready only    *)
(*     through Wait. CardMachine.Land and LandEvent are IsaLand/            *)
(*     IsaLandEvent, which also record the retire.                         *)
(*                                                                         *)
(*   - the per-kind results. A finish records the result the kind owes:     *)
(*     think, script and merge a head and a verdict; verify a verdict;      *)
(*     wait neither (its results column is none). RecordResult writes it    *)
(*     once a card is landed.                                              *)
(*                                                                         *)
(* The card life itself is CardMachine's; the kinds' vocabulary and the     *)
(* one wait kind are this module's. A reader counts the wait actions here   *)
(* (one, Wait) against the paths it replaced (hold, sentinel, wave: three   *)
(* in CardMachine plus the code it was read from).                         *)
(*                                                                         *)
(* What must always hold (TLC):                                            *)
(*   NoCardRetiresTwice       no card lands twice                          *)
(*   RetiredHeadOnBase        a landed card's head is an ancestor of its base *)
(*   WaitDispatchesOnlyWhenOperandHolds  a card never leaves waiting for a  *)
(*                            consumer before its operand holds            *)
(*   AnswerIsTheOutside       the tick's answer never says an operand holds *)
(*                            that the outside does not                    *)
(* and on MCCardISALive, a wait whose operand comes to hold is dealt; on    *)
(* MCCardISAExtLive, a wait on an external operand that holds is released  *)
(* by a later tick.                                                        *)
(*                                                                         *)
(* Reversed witnesses (Broken, the same shape as CoordinatorWake):          *)
(*   "doubleland"  a landed card is counted landed a second time           *)
(*                 (breaks NoCardRetiresTwice)                             *)
(*   "wrongbase"   a card lands with its head past its base, not on it     *)
(*                 (breaks RetiredHeadOnBase)                              *)
(*   "waitfirst"   Wait releases a card whose operand does not hold        *)
(*                 (breaks WaitDispatchesOnlyWhenOperandHolds)             *)
(*   "onekey"      the tick's cache is read under one key for every        *)
(*                 external operand: a card is released on another         *)
(*                 operand's answer (breaks                                 *)
(*                 WaitDispatchesOnlyWhenOperandHolds, external form)       *)
(*                                                                         *)
(* LandTwoLevel (the merge-tree-proof branch, head                         *)
(* 40e9dc34ea18f7362f3b1b914ac204fe48263ae7, tla/LandTwoLevel.tla) is the   *)
(* two-level landing design. That branch has not landed on this base, so    *)
(* it is cited here only: IsaLand is the retire action the two-level        *)
(* landing will refine; no line of it is copied.                            *)
(***************************************************************************)
EXTENDS CardMachine

CONSTANTS
  Kind,     \* [Cards -> {"think", "verify", "script", "merge", "wait"}]
  Ext,      \* the external operands (layer 3): pr merged, branch contains, after
  WaitFor,  \* [Cards -> Cards \cup {"release"} \cup Ext]: the wait operand
  MaxBase,  \* the base branch's bound; the ISA's base starts here
  Broken    \* the reversed witnesses to turn on

ASSUME Kind \in [Cards -> {"think", "verify", "script", "merge", "wait"}]
ASSUME Ext \cap (Cards \cup {"release"}) = {}
ASSUME WaitFor \in [Cards -> Cards \cup {"release"} \cup Ext]
ASSUME \A c \in Cards : WaitFor[c] \in Cards => WaitFor[c] /= c
ASSUME MaxBase \in Nat /\ MaxBase >= 1

Kinds       == {"think", "verify", "script", "merge", "wait"}
ProducesHead(k) == k \in {"think", "script", "merge"}
ProducesVerdict(k) == k /= "wait"
WaitCards   == {c \in Cards : Kind[c] = "wait"}
ExtCards    == {c \in Cards : WaitFor[c] \in Ext}

VARIABLES
  released,  \* BOOLEAN: the coordinator's release, the operand "release"
  external,  \* [Ext -> BOOLEAN]: the outside's condition of each external operand
  answer,    \* [Ext -> BOOLEAN]: the tick's answer for each operand, kept for that tick
  lands,     \* [Cards -> Nat]: how many times a card has landed (0 or 1, or 2 broken)
  base,      \* [Cards -> Nat]: the base branch the card's head must be an ancestor of
  verdict    \* [Cards -> {"-", "ok", "broken"}]: the result the kind owes

\* the new variables as one tuple; ivars is the base's vars and these
ivars == <<released, external, answer, lands, base, verdict>>
avars == <<vars, ivars>>

\* Frame(A): a reused CardMachine action, with the ISA's own variables still.
\* An action that does not name a variable leaves it free; this is the one
\* place that keeps the reuse honest.
Frame(A) == A /\ UNCHANGED ivars

IsaTypeOK ==
  /\ TypeOK
  /\ released \in BOOLEAN
  /\ external \in [Ext -> BOOLEAN]
  /\ answer \in [Ext -> BOOLEAN]
  /\ lands \in [Cards -> Nat]
  /\ base \in [Cards -> Nat]
  /\ verdict \in [Cards -> {"-", "ok", "broken"}]

IsaInit ==
  /\ Init
  /\ released = FALSE
  /\ external = [e \in Ext |-> FALSE]
  /\ answer = [e \in Ext |-> FALSE]
  /\ lands = [c \in Cards |-> 0]
  /\ base = [c \in Cards |-> MaxBase]
  /\ verdict = [c \in Cards |-> "-"]

----------------------------------------------------------------------------
(* The one wait kind *)

\* OperandHolds(c): the one guard of the one wait kind. WaitFor[c] says what
\* the card waits for, in the forms the spec reads off DEPENDS-ON:
\* a card id (waits for it to land), "release" (the coordinator's release,
\* which today is admitted held and the wave behind a held sentinel), or an
\* external operand (internal/sprint/external.go), which holds when the
\* tick's answer for it says so.
OperandHolds(c) ==
  LET o == WaitFor[c] IN
  IF o = "release"  THEN released
  ELSE IF o \in Ext THEN answer[o]
  ELSE where[o] \in {"landed"} \/ (where[o] = "done" /\ ok[o] = "ok")

\* Wait(c): the one wait action, replacing CardMachine.Release (the judged
\* path's own release) and, as one action, the release of a held card and of
\* a wave. One guard, OperandHolds. "onekey" reads the tick's answer under
\* any operand's key for an external wait, not the card's own.
Wait(c) ==
  /\ where[c] = "waiting"
  /\ (OperandHolds(c) \/ "waitfirst" \in Broken
      \/ ("onekey" \in Broken /\ WaitFor[c] \in Ext /\ \E e \in Ext : answer[e]))
  /\ where' = [where EXCEPT ![c] = "ready"]
  /\ UNCHANGED <<ok, copy, reads, pending, low, ncut, author, head, prHead, ci, cvars, up, ivars>>

\* Release(): the coordinator releases. It makes the "release" operand hold.
\* This is the verb, not a card's wait: the operand's value, not a path.
CoordRelease ==
  /\ ~released
  /\ released' = TRUE
  /\ UNCHANGED <<pvars, cvars, up, external, answer, lands, base, verdict>>

\* Extern(e): the outside's condition of operand e comes to hold (a PR
\* merges, a branch takes a commit, the clock passes a time).
Extern(e) ==
  /\ ~external[e]
  /\ external' = [external EXCEPT ![e] = TRUE]
  /\ UNCHANGED <<pvars, cvars, up, released, answer, lands, base, verdict>>

\* IsaTick: the tick asks each external operand once and keeps the answer
\* for that tick (internal/sprint/tick_external.go, externalAsk). The
\* answer is the outside's as the tick read it.
IsaTick ==
  /\ answer /= external
  /\ answer' = external
  /\ UNCHANGED <<pvars, cvars, up, released, external, lands, base, verdict>>

----------------------------------------------------------------------------
(* The per-kind results *)

\* RecordResult(c): once a card is landed, write the result its kind owes.
\* producehead -> the head is the base it landed on; eververdict -> a word;
\* the wait kind owes neither, so its result stays none.
RecordResult(c) ==
  /\ where[c] = "landed" /\ verdict[c] = "-" /\ ProducesVerdict(Kind[c])
  /\ verdict' = [verdict EXCEPT ![c] = "ok"]
  /\ head' = IF ProducesHead(Kind[c])
              THEN [head EXCEPT ![c] = base[c]]
              ELSE head
  /\ UNCHANGED <<where, ok, copy, reads, pending, low, ncut, author, prHead, ci, cvars, up, released, external, answer, lands, base>>

----------------------------------------------------------------------------
(* Replaced life actions that touch the new variables *)

\* Deal(c, k): a ready card is dealt. Replaces CardMachine.DealWork: the base
\* deal also reads a waiting card (line 165), which would dispatch a card
\* around its operand; here only a ready card is dealt, so every waiting card
\* passes through Wait first.
Deal(c, k) ==
  /\ where[c] = "ready" /\ Bare(c) /\ DepsMet(c)
  /\ up[k] /\ Room(k) > 0 /\ CanCut(c)
  /\ where' = [where EXCEPT ![c] = "working"]
  /\ copy' = [copy EXCEPT ![c] = NextCopy(c)]
  /\ ncut' = [ncut EXCEPT ![c] = @ + 1]
  /\ cw' = CutCW(c, k) /\ ck' = CutCK(c, k) /\ cl' = CutCL(c, "work")
  /\ UNCHANGED <<ok, reads, pending, low, author, head, prHead, ci, leased, up, ivars>>

\* IsaLand(c): CardMachine.Land (merging -> landed), plus the retire record:
\* the head is set to the base it lands on (an ancestor, the same commit),
\* the base is fixed, and the land is counted. "wrongbase" lands with the
\* head one past the base: a land onto a base the head is not on.
IsaLand(c) ==
  /\ where[c] = "merging"
  /\ where' = [where EXCEPT ![c] = "landed"]
  /\ head' = [head EXCEPT ![c] = IF "wrongbase" \in Broken THEN base[c] + 1 ELSE base[c]]
  /\ lands' = [lands EXCEPT ![c] = @ + 1]
  /\ UNCHANGED <<ok, copy, reads, pending, low, ncut, author, prHead, ci, cw, ck, cl, leased, up, released, external, answer, base, verdict>>

\* IsaLandEvent(c): CardMachine.LandEvent (a landing event for a card that is
\* not merging), with the same retire record as IsaLand.
IsaLandEvent(c) ==
  /\ where[c] \in {"waiting", "ready", "working"}
  /\ where' = [where EXCEPT ![c] = "landed"]
  /\ head' = [head EXCEPT ![c] = IF "wrongbase" \in Broken THEN base[c] + 1 ELSE base[c]]
  /\ lands' = [lands EXCEPT ![c] = @ + 1]
  /\ cw' = LET r == RetireReads(cw, c) IN
           IF copy[c] /= NoCopy /\ cw[copy[c]] \in Live THEN [r EXCEPT ![copy[c]] = "fail"] ELSE r
  /\ reads' = [reads EXCEPT ![c] = {}]
  /\ copy' = [copy EXCEPT ![c] = NoCopy]
  /\ UNCHANGED <<ok, pending, low, ncut, author, prHead, ci, ck, cl, leased, up, released, external, answer, base, verdict>>

\* DoubleLand(c): the reversed witness only. A landed card is counted landed
\* a second time, which the design never does. "doubleland" turns it on.
DoubleLand(c) ==
  /\ "doubleland" \in Broken
  /\ where[c] = "landed"
  /\ lands' = [lands EXCEPT ![c] = @ + 1]
  /\ UNCHANGED <<pvars, cvars, up, released, external, answer, base, verdict>>

----------------------------------------------------------------------------

IsaNext ==
  \/ \E c \in Cards :
       Frame(Push(c)) \/ Wait(c) \/ Frame(CIWord(c)) \/ Frame(PRHeadMoves(c))
       \/ Frame(Verdict(c)) \/ Frame(CancelPrimary(c))
       \/ IsaLand(c) \/ IsaLandEvent(c) \/ RecordResult(c) \/ DoubleLand(c)
  \/ \E c \in Cards, k \in Consumers : Deal(c, k) \/ Frame(DealRead(c, k))
  \/ \E i \in CopyId :
       Frame(Work(i)) \/ Frame(Beat(i)) \/ Frame(Lapse(i)) \/ Frame(Expire(i))
       \/ Frame(DownReady(i)) \/ Frame(GiveBack(i)) \/ Frame(EndFail(i))
       \/ Frame(EndWorkPR(i)) \/ Frame(EndWorkDone(i)) \/ Frame(EndReadHigh(i))
       \/ Frame(EndReadStale(i)) \/ Frame(EndReadLow(i)) \/ Frame(EndReadFail(i))
       \/ Frame(EndFixOK(i))
  \/ \E k \in Consumers : Frame(Down(k)) \/ Frame(Up(k))
  \/ CoordRelease \/ IsaTick \/ \E e \in Ext : Extern(e)

\* Fairness as CardMachine's, on the actions this module uses: the duties and
\* the workers, the coordinator's release and the tick. The wait is fair too,
\* so a card whose operand holds is not starved of its release.
IsaFairness ==
  /\ \A c \in Cards : WF_avars(Wait(c))
  /\ WF_avars(CoordRelease) /\ WF_avars(IsaTick)
  /\ \A e \in Ext : WF_avars(Extern(e))

IsaSpec == IsaInit /\ [][IsaNext]_avars /\ IsaFairness

----------------------------------------------------------------------------
(* What must always hold *)

\* no card lands twice: the land count never passes one
NoCardRetiresTwice == \A c \in Cards : lands[c] <= 1

\* a landed card's head is an ancestor of its base: in this linear model the
\* head is at or behind the base, never past it
RetiredHeadOnBase == \A c \in Cards : where[c] = "landed" => head[c] <= base[c]

\* a wait card is never dispatched before its operand holds: once it has been
\* released for a consumer (ready or working), its operand holds. The operands
\* only grow, so a card released by Wait keeps the property. A card the
\* coordinator cancels or a landing event walks out of waiting is not a
\* dispatch, so it is not covered here.
WaitDispatchesOnlyWhenOperandHolds ==
  \A c \in WaitCards : where[c] \in {"ready", "working"} => OperandHolds(c)

\* the tick's answer is the outside's: an operand the answer says holds does
\* hold, since the outside only comes to hold
AnswerIsTheOutside == \A e \in Ext : answer[e] => external[e]

IsaSafety == /\ IsaTypeOK /\ Safety /\ NoCardRetiresTwice /\ RetiredHeadOnBase
              /\ WaitDispatchesOnlyWhenOperandHolds /\ AnswerIsTheOutside

----------------------------------------------------------------------------
(* What must eventually happen: a wait whose operand comes to hold is dealt *)

\* dealt: it leaves waiting and moves on (ready, working, or a terminal word)
Dealt(c) == where[c] \in {"ready", "working", "review", "merging", "landed", "done"}
WaitDealt == \A c \in WaitCards : (OperandHolds(c) /\ where[c] = "waiting") ~> Dealt(c)

\* an external wait is released once the outside holds: the next tick reads
\* the answer, and the wait takes it
ExtDealt == \A c \in WaitCards \cap ExtCards : (external[WaitFor[c]] /\ where[c] = "waiting") ~> Dealt(c)

=============================================================================
