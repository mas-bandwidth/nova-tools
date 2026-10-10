---------------------------- MODULE CardMachine ----------------------------
(***************************************************************************)
(* The nova-sprint card machine, written down once.                        *)
(*                                                                         *)
(* Source: nova-tools internal/nsprint/fn/lua/02_card_move.lua at          *)
(* 467c66459 (TK: the primary's one move and graph; TM: copies, reads, the *)
(* review column, verdicts, leases, expiry, clear), 03_task_event.lua (the *)
(* landing walk), deal_friend.lua (ns_deal_return), task_queue.lua (DEP).  *)
(* Every action below names the Lua it was read from. Where the Lua and    *)
(* this model differ, the Lua is the implementation and this is the claim  *)
(* about it; a TLC counterexample is a sequence the store accepts.         *)
(*                                                                         *)
(* Out of scope in this revision (named so they are not forgotten): the    *)
(* friend-queue path (deal_friend's ready -> working with a friend and no  *)
(* copy; nova-friend replaces it), the sentinel card, parked, the fine     *)
(* state index, probes, WHO/tier admission, the swarm's four read copies   *)
(* (one read copy stands for them), CI as a per-head verdict (one word per *)
(* PR head here), the dialogue states of SPEC-DIALOGUE (a later module).   *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS
  Cards,       \* the primaries of one sprint, e.g. {"c1", "c2"}
  Consumers,   \* bench:<b> and friend:<f> alike, e.g. {"k1", "k2"}
  Slots,       \* [Consumers -> Nat]: <c>:desired slots
  MaxCopies,   \* copies cut per primary in the model (the store is unbounded)
  Deps         \* [Cards -> SUBSET Cards]: DEPENDS-ON (task_queue.lua DEP)
  BEHIND_CAP   \* cap for turn-since: how long a batch turn keeps a friend up (ms)

ASSUME Slots \in [Consumers -> Nat]
ASSUME Deps \in [Cards -> SUBSET Cards]
ASSUME MaxCopies \in Nat /\ MaxCopies >= 1

NoCopy   == <<"none", 0>>
NoOne    == "none"
CopyId   == Cards \X (1..MaxCopies)

\* TK.WHERE (02_card_move.lua 1062); done carries an outcome (ok)
Where    == {"null", "waiting", "ready", "working", "review", "merging", "landed", "done"}
Terminal == {"landed", "done"}
\* a copy's where: TM.COLS (2388); "none" before it is cut
CopyWhere == {"none", "ready", "working", "ok", "fail"}
Live      == {"ready", "working"}            \* TM.LIVE
Legs      == {"none", "work", "read", "fix"}
\* TM.WANT (2391): where a primary is while a copy of each leg is live
Want(leg) == CASE leg = "work" -> {"working"}
               [] leg = "read" -> {"review"}
               [] leg = "fix"  -> {"review", "working"}
               [] OTHER        -> {}

VARIABLES
  where,    \* [Cards -> Where]
  ok,       \* [Cards -> {"-", "ok", "fail"}]       where_ok
  copy,     \* [Cards -> CopyId \cup {NoCopy}]      the live work or fix copy the primary names
  reads,    \* [Cards -> SUBSET CopyId]             the live read copies it names
  pending,  \* [Cards -> BOOLEAN]                   TK.pending: a fail's review awaits its verdict
  low,      \* [Cards -> Nat]                       low_reads
  ncut,     \* [Cards -> Nat]                       copies (the id counter)
  author,   \* [Cards -> Consumers \cup {NoOne}]    the consumer whose copy opened the PR
  head,     \* [Cards -> Nat]                       the head the primary took at its last move into review
  prHead,   \* [Cards -> Nat]                       the PR record's head now (pr:<repo>:<n> head)
  ci,       \* [Cards -> {"pending", "ok", "red"}]  the CI word at prHead
  cw,       \* [CopyId -> CopyWhere]
  ck,       \* [CopyId -> Consumers \cup {NoOne}]   consumer
  cl,       \* [CopyId -> Legs]                     leg
  leased,   \* [CopyId -> BOOLEAN]                  lease_until > now
  up        \* [Consumers -> BOOLEAN]               <c>:beat within a lease
  turnSince \* [Consumers -> Nat]                   <c>:turn-since from beat (0 when none)

pvars == <<where, ok, copy, reads, pending, low, ncut, author, head, prHead, ci>>
cvars == <<cw, ck, cl, leased>>
vars  == <<pvars, cvars, up, turnSince>>

TypeOK ==
  /\ where \in [Cards -> Where]
  /\ ok \in [Cards -> {"-", "ok", "fail"}]
  /\ copy \in [Cards -> CopyId \cup {NoCopy}]
  /\ reads \in [Cards -> SUBSET CopyId]
  /\ pending \in [Cards -> BOOLEAN]
  /\ low \in [Cards -> Nat]
  /\ ncut \in [Cards -> 0..MaxCopies]
  /\ author \in [Cards -> Consumers \cup {NoOne}]
  /\ head \in [Cards -> Nat] /\ prHead \in [Cards -> Nat]
  /\ ci \in [Cards -> {"pending", "ok", "red"}]
  /\ cw \in [CopyId -> CopyWhere]
  /\ ck \in [CopyId -> Consumers \cup {NoOne}]
  /\ cl \in [CopyId -> Legs]
  /\ leased \in [CopyId -> BOOLEAN]
  /\ up \in [Consumers -> BOOLEAN]
  /\ turnSince \in [Consumers -> Nat]

Init ==
  /\ where = [c \in Cards |-> "null"]
  /\ ok = [c \in Cards |-> "-"]
  /\ copy = [c \in Cards |-> NoCopy]
  /\ reads = [c \in Cards |-> {}]
  /\ pending = [c \in Cards |-> FALSE]
  /\ low = [c \in Cards |-> 0]
  /\ ncut = [c \in Cards |-> 0]
  /\ author = [c \in Cards |-> NoOne]
  /\ head = [c \in Cards |-> 0]
  /\ prHead = [c \in Cards |-> 0]
  /\ ci = [c \in Cards |-> "pending"]
  /\ cw = [i \in CopyId |-> "none"]
  /\ ck = [i \in CopyId |-> NoOne]
  /\ cl = [i \in CopyId |-> "none"]
  /\ leased = [i \in CopyId |-> FALSE]
  /\ up = [k \in Consumers |-> TRUE]
  /\ turnSince = [k \in Consumers |-> 0]

----------------------------------------------------------------------------
(* Derived *)

Working(k) == {i \in CopyId : cw[i] = "working" /\ ck[i] = k}
Ready(k)   == {i \in CopyId : cw[i] = "ready" /\ ck[i] = k}
\* TM.work's free = slots - ci legs - |working|; the Go deal duty deals
\* room = free - |ready| copies (assumption: the dealer never over-cuts)
Room(k)    == Slots[k] - Cardinality(Working(k)) - Cardinality(Ready(k))
DepsMet(c) == \A d \in Deps[c] : where[d] = "landed" \/ (where[d] = "done" /\ ok[d] = "ok")
\* TM.leg (2524): a primary with no live copy, no reads, not pending
Bare(c)    == copy[c] = NoCopy /\ reads[c] = {} /\ ~pending[c]
LiveCopies(c) == {i \in CopyId : i[1] = c /\ cw[i] \in Live}

\* the next copy id of c (TM.cut: copies + 1)
NextCopy(c) == <<c, ncut[c] + 1>>
CanCut(c) == ncut[c] < MaxCopies

\* Cut(c, k, leg): the copy record TM.cut writes, in cvars
CutCW(c, k) == [cw EXCEPT ![NextCopy(c)] = "ready"]
CutCK(c, k) == [ck EXCEPT ![NextCopy(c)] = k]
CutCL(c, leg) == [cl EXCEPT ![NextCopy(c)] = leg]

\* TM.retire(i, outcome): the copy leaves its live set for ok or fail
Retire(f, i, outcome) == [f EXCEPT ![i] = outcome]
\* TM.retire_reads / TM.drop: every live read copy of c retires to fail
RetireReads(f, c) == [i \in CopyId |-> IF i \in reads[c] /\ f[i] \in Live THEN "fail" ELSE f[i]]

----------------------------------------------------------------------------
(* The primary's own moves *)

\* task push (TK.create 1686): null -> waiting
Push(c) ==
  /\ where[c] = "null"
  /\ where' = [where EXCEPT ![c] = "waiting"]
  /\ UNCHANGED <<ok, copy, reads, pending, low, ncut, author, head, prHead, ci, cvars, up>>

\* DEP.release (task_queue.lua 176), the resolve duty: waiting -> ready once
\* every DEPENDS-ON is closed
Release(c) ==
  /\ where[c] = "waiting" /\ DepsMet(c)
  /\ where' = [where EXCEPT ![c] = "ready"]
  /\ UNCHANGED <<ok, copy, reads, pending, low, ncut, author, head, prHead, ci, cvars, up>>

\* ns_deal_return (deal_friend.lua 140): ready -> waiting when no live
\* consumer may take it ("Ready must be READY")
\* FINDING 2 (TLC run 4): with Release fair (DEP.release / the resolve
\* duty) and every consumer down, ready -> waiting -> ready every tick: the
\* waiting<->ready oscillation of 2026-09-25. The fix assumed from run 5
\* on: no automatic return (the action is kept, disabled by FALSE).
DealReturn(c) ==
  /\ FALSE
  /\ where[c] = "ready"
  /\ ~\E k \in Consumers : up[k] /\ Room(k) > 0
  /\ where' = [where EXCEPT ![c] = "waiting"]
  /\ UNCHANGED <<ok, copy, reads, pending, low, ncut, author, head, prHead, ci, cvars, up>>

\* TM.deal, leg work (2652, TM.leg 2524, TM.cut 2591): a waiting (deps met)
\* or ready primary with nothing live -> working; its work copy is cut ready
\* on k. Deal reads waiting as well as ready (2705).
DealWork(c, k) ==
  /\ where[c] \in {"waiting", "ready"} /\ Bare(c) /\ DepsMet(c)
  /\ up[k] /\ Room(k) > 0 /\ CanCut(c)
  /\ where' = [where EXCEPT ![c] = "working"]
  /\ copy' = [copy EXCEPT ![c] = NextCopy(c)]
  /\ ncut' = [ncut EXCEPT ![c] = @ + 1]
  /\ cw' = CutCW(c, k) /\ ck' = CutCK(c, k) /\ cl' = CutCL(c, "work")
  /\ UNCHANGED <<ok, reads, pending, low, author, head, prHead, ci, leased, up>>

\* TM.deal leg read / TM.ensure / TM.cut_reads (3356, 3432): a primary in
\* review with nothing live has a read copy cut on a consumer that is not
\* its author; the primary stays in review and names the read
DealRead(c, k) ==
  /\ where[c] = "review" /\ Bare(c)
  /\ up[k] /\ Room(k) > 0 /\ k /= author[c] /\ CanCut(c)
  /\ reads' = [reads EXCEPT ![c] = {NextCopy(c)}]
  /\ ncut' = [ncut EXCEPT ![c] = @ + 1]
  /\ cw' = CutCW(c, k) /\ ck' = CutCK(c, k) /\ cl' = CutCL(c, "read")
  /\ UNCHANGED <<where, ok, copy, pending, low, author, head, prHead, ci, leased, up>>

\* TM.work (2721): a ready copy on k starts, within k's slots, with a lease
Work(i) ==
  /\ cw[i] = "ready" /\ Cardinality(Working(ck[i])) < Slots[ck[i]]
  /\ cw' = [cw EXCEPT ![i] = "working"]
  /\ leased' = [leased EXCEPT ![i] = TRUE]
  /\ UNCHANGED <<pvars, ck, cl, up>>

\* TM.beat (3787): the holder renews; Lapse: three beats missed (time)
Beat(i) ==
  /\ cw[i] = "working" /\ ~leased[i]
  /\ leased' = [leased EXCEPT ![i] = TRUE]
  /\ UNCHANGED <<pvars, cw, ck, cl, up>>
Lapse(i) ==
  /\ cw[i] = "working" /\ leased[i]
  /\ leased' = [leased EXCEPT ![i] = FALSE]
  /\ UNCHANGED <<pvars, cw, ck, cl, up>>

\* a consumer's beat stops and returns (bench or friend down)
\* For friends: down only if turnSince is empty or BEHIND_CAP has passed
Down(k) ==
  /\ up[k]
  /\ /\ turnSince[k] = 0 \* no batch turn
     \/ turnSince[k] > BEHIND_CAP \* cap has passed
  /\ up' = [up EXCEPT ![k] = FALSE]
  /\ UNCHANGED <<pvars, cvars, turnSince>>
Up(k)   == ~up[k] /\ up' = [up EXCEPT ![k] = TRUE] /\ UNCHANGED <<pvars, cvars, turnSince>>

\* Friend batch turn start: turnSince becomes non-zero, up is kept true
FriendTurnStart(k, start_ms) ==
  /\ k \in Consumers
  /\ turnSince[k] = 0
  /\ turnSince' = [turnSince EXCEPT ![k] = start_ms]
  /\ UNCHANGED <<pvars, cvars, up>>
\* Friend batch turn end: turnSince cleared, up follows normal beat rules
FriendTurnEnd(k) ==
  /\ k \in Consumers
  /\ turnSince'[k] = 0
  /\ UNCHANGED <<pvars, cvars, up, turnSince EXCEPT ![k]>>

----------------------------------------------------------------------------
(* TM.finish (2877): a copy's end, case by case. c is the copy's primary. *)

\* the copy given back (o.keep: cancel, revoke, consumer down, a lapsed
\* read): copy -> fail, no attempt counted; a work or fix copy's primary
\* returns to waiting, a read copy's primary stays in review
GiveBack(i) ==
  LET c == i[1] IN
  /\ cw[i] \in Live
  /\ cw' = Retire(cw, i, "fail")
  /\ IF cl[i] = "read"
       THEN /\ reads' = [reads EXCEPT ![c] = @ \ {i}]
            /\ UNCHANGED <<where, copy>>
       ELSE /\ where' = [where EXCEPT ![c] = "waiting"]
            /\ copy' = [copy EXCEPT ![c] = NoCopy]
            /\ reads' = reads
  /\ UNCHANGED <<ok, pending, low, ncut, author, head, prHead, ci, ck, cl, leased, up>>

\* TM.expire (3826): a working copy whose lease lapsed. A read is given
\* back; a work or fix copy is a fail: the primary goes to review for a
\* verdict (#4072)
Expire(i) ==
  LET c == i[1] IN
  /\ cw[i] = "working" /\ ~leased[i]
  /\ IF cl[i] = "read"
       THEN GiveBack(i)
       ELSE /\ cw' = Retire(cw, i, "fail")
            /\ where' = [where EXCEPT ![c] = "review"]
            /\ pending' = [pending EXCEPT ![c] = TRUE]
            /\ copy' = [copy EXCEPT ![c] = NoCopy]
            /\ UNCHANGED <<ok, reads, low, ncut, author, head, prHead, ci, ck, cl, leased, up>>

\* TM.expire: a consumer whose beat is older than a lease holds no ready
\* copy: each is given back
DownReady(i) ==
  /\ cw[i] = "ready" /\ ~up[ck[i]]
  /\ GiveBack(i)

\* card end --fail on a work or fix copy (not given back): review, pending
EndFail(i) ==
  LET c == i[1] IN
  /\ cw[i] = "working" /\ cl[i] \in {"work", "fix"}
  /\ cw' = Retire(cw, i, "fail")
  /\ where' = [where EXCEPT ![c] = "review"]
  /\ pending' = [pending EXCEPT ![c] = TRUE]
  /\ copy' = [copy EXCEPT ![c] = NoCopy]
  /\ UNCHANGED <<ok, reads, low, ncut, author, head, prHead, ci, ck, cl, leased, up>>

\* card end --ok --pr on the work copy: the primary -> review at the PR's
\* head, its author is this consumer, CI at that head is pending; its read
\* copies are cut by TM.after_move (here: DealRead, the next step)
EndWorkPR(i) ==
  LET c == i[1] IN
  /\ cw[i] = "working" /\ cl[i] = "work"
  /\ cw' = Retire(cw, i, "ok")
  /\ where' = [where EXCEPT ![c] = "review"]
  /\ copy' = [copy EXCEPT ![c] = NoCopy]
  /\ author' = [author EXCEPT ![c] = ck[i]]
  /\ prHead' = [prHead EXCEPT ![c] = @ + 1]
  /\ head' = [head EXCEPT ![c] = prHead[c] + 1]
  /\ ci' = [ci EXCEPT ![c] = "pending"]
  /\ UNCHANGED <<ok, reads, pending, low, ncut, ck, cl, leased, up>>

\* card end --ok with no PR: done/ok; with --sha (done-already): landed
EndWorkDone(i) ==
  LET c == i[1] IN
  /\ cw[i] = "working" /\ cl[i] = "work"
  /\ cw' = Retire(cw, i, "ok")
  /\ copy' = [copy EXCEPT ![c] = NoCopy]
  /\ \/ /\ where' = [where EXCEPT ![c] = "done"] /\ ok' = [ok EXCEPT ![c] = "ok"]
     \/ /\ where' = [where EXCEPT ![c] = "landed"] /\ ok' = ok
  /\ UNCHANGED <<reads, pending, low, ncut, author, head, prHead, ci, ck, cl, leased, up>>

\* CI at the PR's head gets its word (ns_ci_* / ci github)
CIWord(c) ==
  /\ where[c] = "review" /\ ci[c] = "pending"
  /\ ci' \in {[ci EXCEPT ![c] = "ok"], [ci EXCEPT ![c] = "red"]}
  /\ UNCHANGED <<where, ok, copy, reads, pending, low, ncut, author, head, prHead, cvars, up>>

\* the PR's head moves (a push to the branch): CI is pending again
PRHeadMoves(c) ==
  /\ where[c] = "review"
  /\ prHead' = [prHead EXCEPT ![c] = @ + 1]
  /\ ci' = [ci EXCEPT ![c] = "pending"]
  /\ UNCHANGED <<where, ok, copy, reads, pending, low, ncut, author, head, cvars, up>>

\* a read copy ends with SCORE >= 8 at the record head with CI OK: the
\* primary -> merging; the other reads retire; a live fix copy is dropped
\* (TM.finish 2960-2985, after_move 3373, drop 3120)
EndReadHigh(i) ==
  LET c == i[1] IN
  /\ cw[i] = "working" /\ cl[i] = "read"
  /\ head[c] = prHead[c] /\ ci[c] = "ok"
  /\ cw' = LET r == RetireReads(cw, c) IN
           LET r2 == [r EXCEPT ![i] = "ok"] IN
           IF copy[c] /= NoCopy /\ cw[copy[c]] \in Live THEN [r2 EXCEPT ![copy[c]] = "fail"] ELSE r2
  /\ where' = [where EXCEPT ![c] = "merging"]
  /\ reads' = [reads EXCEPT ![c] = {}]
  /\ copy' = [copy EXCEPT ![c] = NoCopy]
  /\ UNCHANGED <<ok, pending, low, ncut, author, head, prHead, ci, ck, cl, leased, up>>

\* a read copy ends with SCORE >= 8 but the PR moved past the head it read
\* (#4094 c): the score does not count; TM.rehead: the open reads retire,
\* a live fix copy ends ok, the primary takes the head, reads are re-cut
\* (here: by DealRead next)
EndReadStale(i) ==
  LET c == i[1] IN
  /\ cw[i] = "working" /\ cl[i] = "read"
  /\ head[c] /= prHead[c]
  /\ cw' = LET r == RetireReads(cw, c) IN
           IF copy[c] /= NoCopy /\ cw[copy[c]] \in Live
             THEN [r EXCEPT ![copy[c]] = IF cl[copy[c]] = "fix" THEN "ok" ELSE "fail"]
             ELSE r
  /\ reads' = [reads EXCEPT ![c] = {}]
  /\ copy' = [copy EXCEPT ![c] = NoCopy]
  /\ head' = [head EXCEPT ![c] = prHead[c]]
  /\ UNCHANGED <<where, ok, pending, low, ncut, author, prHead, ci, ck, cl, leased, up>>

\* a read copy ends with SCORE < 8 at the record head (#4097): the first
\* time the primary stays in review and one fix copy is cut on the author
\* (or, with no fix route, the primary -> waiting); the second time the
\* primary is pending a verdict (read-under-8-twice) and, TM.finish 3068,
\* its copy pointer clears (mo.copy = '' since not stay) while a live fix
\* copy is left where it is
EndReadLow(i) ==
  LET c == i[1] IN
  /\ cw[i] = "working" /\ cl[i] = "read" /\ head[c] = prHead[c]
  /\ low' = [low EXCEPT ![c] = @ + 1]
  /\ reads' = [reads EXCEPT ![c] = @ \ {i}]
  /\ IF low[c] >= 1
       THEN /\ cw' = Retire(cw, i, "ok")
            /\ pending' = [pending EXCEPT ![c] = TRUE]
            /\ copy' = [copy EXCEPT ![c] = NoCopy]
            /\ UNCHANGED <<where, ncut, ck, cl>>
       ELSE IF copy[c] /= NoCopy
         THEN \* a fix copy is already out: the finding joins its brief
              /\ cw' = Retire(cw, i, "ok")
              /\ UNCHANGED <<where, copy, pending, ncut, ck, cl>>
         ELSE IF author[c] /= NoOne /\ up[author[c]] /\ CanCut(c)
           THEN /\ cw' = [Retire(cw, i, "ok") EXCEPT ![NextCopy(c)] = "ready"]
                /\ ck' = CutCK(c, author[c]) /\ cl' = CutCL(c, "fix")
                /\ copy' = [copy EXCEPT ![c] = NextCopy(c)]
                /\ ncut' = [ncut EXCEPT ![c] = @ + 1]
                /\ UNCHANGED <<where, pending>>
           ELSE /\ cw' = Retire(cw, i, "ok")
                /\ where' = [where EXCEPT ![c] = "waiting"]
                /\ UNCHANGED <<copy, pending, ncut, ck, cl>>
  /\ UNCHANGED <<ok, author, head, prHead, ci, leased, up>>

\* a read copy ends --fail (not given back): review pending (#4072)
EndReadFail(i) ==
  LET c == i[1] IN
  /\ cw[i] = "working" /\ cl[i] = "read"
  /\ cw' = Retire(cw, i, "fail")
  /\ reads' = [reads EXCEPT ![c] = @ \ {i}]
  /\ pending' = [pending EXCEPT ![c] = TRUE]
  /\ copy' = [copy EXCEPT ![c] = NoCopy]
  /\ UNCHANGED <<where, ok, low, ncut, author, head, prHead, ci, ck, cl, leased, up>>

\* a fix copy ends ok (with or without a PR): the primary stays in review,
\* its open reads retire and fresh ones are cut (recut; here DealRead next);
\* the PR's head moved with the fix
EndFixOK(i) ==
  LET c == i[1] IN
  /\ cw[i] = "working" /\ cl[i] = "fix" /\ where[c] = "review"
  /\ cw' = [RetireReads(cw, c) EXCEPT ![i] = "ok"]
  /\ reads' = [reads EXCEPT ![c] = {}]
  /\ copy' = [copy EXCEPT ![c] = NoCopy]
  /\ prHead' = [prHead EXCEPT ![c] = @ + 1]
  /\ ci' = [ci EXCEPT ![c] = "pending"]
  /\ UNCHANGED <<where, ok, pending, low, ncut, author, head, ck, cl, leased, up>>

\* review post --verdict (TM.review 3190): the one way out of a pending
\* review. recut -> waiting, redeal -> ready, drop -> landed; reassign:<k>
\* cuts a work copy on k (-> working). Leaving review retires open reads
\* (after_move).
Verdict(c) ==
  /\ where[c] = "review" /\ pending[c]
  /\ pending' = [pending EXCEPT ![c] = FALSE]
  /\ \/ /\ where' = [where EXCEPT ![c] = "waiting"]
        /\ cw' = RetireReads(cw, c) /\ reads' = [reads EXCEPT ![c] = {}]
        /\ copy' = [copy EXCEPT ![c] = NoCopy]
        /\ UNCHANGED <<ncut, ck, cl>>
     \/ /\ where' = [where EXCEPT ![c] = "ready"]
        /\ cw' = RetireReads(cw, c) /\ reads' = [reads EXCEPT ![c] = {}]
        /\ copy' = [copy EXCEPT ![c] = NoCopy]
        /\ UNCHANGED <<ncut, ck, cl>>
     \/ /\ where' = [where EXCEPT ![c] = "landed"]
        /\ cw' = RetireReads(cw, c) /\ reads' = [reads EXCEPT ![c] = {}]
        /\ copy' = [copy EXCEPT ![c] = NoCopy]
        /\ UNCHANGED <<ncut, ck, cl>>
     \/ \E k \in Consumers :
        /\ up[k] /\ CanCut(c)
        /\ where' = [where EXCEPT ![c] = "working"]
        /\ cw' = [RetireReads(cw, c) EXCEPT ![NextCopy(c)] = "ready"]
        /\ ck' = CutCK(c, k) /\ cl' = CutCL(c, "work")
        /\ reads' = [reads EXCEPT ![c] = {}]
        /\ copy' = [copy EXCEPT ![c] = NextCopy(c)]
        /\ ncut' = [ncut EXCEPT ![c] = @ + 1]
  /\ UNCHANGED <<ok, low, author, head, prHead, ci, leased, up>>

\* card cancel on a primary (TM.cancel 3643): done/fail; its live copy is
\* retired to fail; leaving review retires the reads
CancelPrimary(c) ==
  /\ where[c] \in {"waiting", "ready", "working", "review", "merging"}
  /\ where' = [where EXCEPT ![c] = "done"] /\ ok' = [ok EXCEPT ![c] = "fail"]
  /\ pending' = [pending EXCEPT ![c] = FALSE]
  /\ cw' = LET r == RetireReads(cw, c) IN
           IF copy[c] /= NoCopy /\ cw[copy[c]] \in Live THEN [r EXCEPT ![copy[c]] = "fail"] ELSE r
  /\ reads' = [reads EXCEPT ![c] = {}]
  /\ copy' = [copy EXCEPT ![c] = NoCopy]
  /\ UNCHANGED <<low, ncut, author, head, prHead, ci, ck, cl, leased, up>>

\* the stream lander merges the PR (03_task_event.lua, ns_land_member):
\* merging -> landed
Land(c) ==
  /\ where[c] = "merging"
  /\ where' = [where EXCEPT ![c] = "landed"]
  /\ UNCHANGED <<ok, copy, reads, pending, low, ncut, author, head, prHead, ci, cvars, up>>

\* a landing event for a card that is not merging (a PR merged by hand, a
\* done-already): TE.PATH landed walks waiting, ready, working -> landed in
\* one call (o.landing); TK.edge allows landed with a live copy (LIVECOPY
\* exempts done and landed); after_move retires reads only when leaving
\* review, and review -> landed needs a verdict, so review is not walked
LandEvent(c) ==
  /\ where[c] \in {"waiting", "ready", "working"}
  /\ where' = [where EXCEPT ![c] = "landed"]
  \* FINDING 1 (TLC run 3, 2026-09-27): as the Lua stands the live copy is
  \* left in its consumer's set (UNCHANGED cvars, copy) and can never end:
  \* TM.finish and TM.expire refuse DRIFT for a landed primary. The fix
  \* assumed from here on: the landing retires the live copy and the reads.
  /\ cw' = LET r == RetireReads(cw, c) IN
           IF copy[c] /= NoCopy /\ cw[copy[c]] \in Live THEN [r EXCEPT ![copy[c]] = "fail"] ELSE r
  /\ reads' = [reads EXCEPT ![c] = {}]
  /\ copy' = [copy EXCEPT ![c] = NoCopy]
  /\ UNCHANGED <<ok, pending, low, ncut, author, head, prHead, ci, ck, cl, leased, up>>

----------------------------------------------------------------------------

Next ==
  \/ \E c \in Cards : Push(c) \/ Release(c) \/ DealReturn(c) \/ CIWord(c) \/ PRHeadMoves(c)
                      \/ Verdict(c) \/ CancelPrimary(c) \/ Land(c) \/ LandEvent(c)
  \/ \E c \in Cards, k \in Consumers : DealWork(c, k) \/ DealRead(c, k)
  \/ \E i \in CopyId : Work(i) \/ Beat(i) \/ Lapse(i) \/ Expire(i) \/ DownReady(i)
                       \/ GiveBack(i) \/ EndFail(i) \/ EndWorkPR(i) \/ EndWorkDone(i)
                       \/ EndReadHigh(i) \/ EndReadStale(i) \/ EndReadLow(i) \/ EndReadFail(i)
                       \/ EndFixOK(i)
  \/ \E k \in Consumers : Down(k) \/ Up(k) \/ \E start \in Nat : FriendTurnStart(k, start) \/ FriendTurnEnd(k)

\* The duties (the reconciler's ticks) and the workers are fair; time
\* (Lapse), outages (Down), a PR's head moving and the coordinator's hand
\* (cancel, a land event) are not. Up is fair: an outage ends.
\* Friend turnStart/turnEnd are internal state changes, not scheduled events.
Fairness ==
  /\ \A c \in Cards : WF_vars(Push(c)) /\ WF_vars(Release(c)) /\ WF_vars(DealReturn(c))
                      /\ WF_vars(CIWord(c)) /\ WF_vars(Verdict(c)) /\ WF_vars(Land(c))
  \* the deal and the work are strongly fair: a consumer that is up
  \* infinitely often is dealt to (the duty ticks every second; an outage
  \* between two ticks is the only way to starve it, and TLC run 6 showed
  \* that lasso under weak fairness: consumers flapping forever)
  /\ \A c \in Cards, k \in Consumers : SF_vars(DealWork(c, k)) /\ SF_vars(DealRead(c, k))
  /\ \A i \in CopyId : SF_vars(Work(i)) /\ WF_vars(Expire(i)) /\ WF_vars(DownReady(i))
                       /\ WF_vars(EndFail(i) \/ EndWorkPR(i) \/ EndWorkDone(i))
                       /\ WF_vars(EndReadHigh(i) \/ EndReadStale(i) \/ EndReadLow(i) \/ EndReadFail(i))
                       /\ WF_vars(EndFixOK(i))
  /\ \A k \in Consumers : WF_vars(Up(k))

Spec == Init /\ [][Next]_vars /\ Fairness

\* the PR head is unbounded (a push, a fix); TLC explores up to MaxHead
\* (a CONSTRAINT in the instance)
HeadBound(MaxHead) == \A c \in Cards : prHead[c] <= MaxHead

----------------------------------------------------------------------------
(* What must always hold *)

\* a working primary has a live work or fix copy (TM.fsck: "a working
\* primary with no copy and no friend is drift")
WorkingHasCopy ==
  \A c \in Cards : where[c] = "working" => copy[c] /= NoCopy /\ cw[copy[c]] \in Live /\ cl[copy[c]] \in {"work", "fix"}

\* every live copy is named by its primary, and the primary is where that
\* leg wants it (TM.fsck; TM.finish refuses DRIFT otherwise, and the copy
\* can then never end: it holds its slot until expire, which is refused too)
LiveCopyNamed ==
  \A i \in CopyId : cw[i] \in Live =>
    /\ (copy[i[1]] = i \/ i \in reads[i[1]])
    /\ where[i[1]] \in Want(cl[i])

\* a primary names only live copies
NamedIsLive ==
  \A c \in Cards : /\ copy[c] /= NoCopy => cw[copy[c]] \in Live
                   /\ \A i \in reads[c] : cw[i] \in Live

\* at most one live work or fix copy per primary
OneLiveWork ==
  \A c \in Cards : Cardinality({i \in LiveCopies(c) : cl[i] \in {"work", "fix"}}) <= 1

\* reads are cut only for a primary in review; a pending review has no reads out
ReadsInReview ==
  \A c \in Cards : reads[c] /= {} => where[c] = "review"

\* nothing live once the primary is merging, landed or done
TerminalIsQuiet ==
  \A c \in Cards : where[c] \in {"merging", "landed", "done"} => LiveCopies(c) = {}

\* a consumer never works more copies than its slots
SlotsHeld ==
  \A k \in Consumers : Cardinality(Working(k)) <= Slots[k]

\* a verdict is pending only in review
PendingInReview ==
  \A c \in Cards : pending[c] => where[c] = "review"

Safety == /\ TypeOK /\ WorkingHasCopy /\ LiveCopyNamed /\ NamedIsLive /\ OneLiveWork
          /\ ReadsInReview /\ TerminalIsQuiet /\ SlotsHeld /\ PendingInReview

----------------------------------------------------------------------------
(* What must eventually happen *)

\* every card pushed lands or ends, or the model ran out of copies for it
\* FINDING 3 (TLC run 5): a card whose DEPENDS-ON was cancelled (done/fail)
\* waits forever: waiting_resolve.go wrLanded counts done only with
\* where_ok /= fail, and nothing raises it. Excused here from run 6 on so
\* the rest can be checked; the store owes a line or a cascade.
DepCancelled(c) == \E d \in Deps[c] : where[d] = "done" /\ ok[d] = "fail"
\* the model's copy bound (run 8): a card whose MaxCopies copies all ended
\* cannot be dealt again here, and its dependents wait on it; the store has
\* no such bound
DepExhausted(c) == \E d \in Deps[c] : ncut[d] = MaxCopies /\ where[d] \notin Terminal
Progress ==
  \A c \in Cards : (where[c] /= "null") ~> (where[c] \in Terminal \/ ncut[c] = MaxCopies \/ DepCancelled(c) \/ DepExhausted(c))

\* every copy cut ends
CopiesEnd ==
  \A i \in CopyId : (cw[i] \in Live) ~> (cw[i] \in {"ok", "fail"})

\* Ready means READY: a card in ready is dealt, not returned (Glenn
\* 2026-09-25 1:40 PM ET: waiting -> ready is one way); stated as: from
\* ready, the next place is working
ReadyIsOneWay ==
  [][\A c \in Cards : where[c] = "ready" /\ where'[c] /= "ready" => where'[c] \in {"working", "landed", "done"}]_vars

=============================================================================
