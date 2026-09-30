---------------------------- MODULE DirtyTickRead ----------------------------
\* The tick's read (internal/sprint/store/twin.go, pipeline_load.go,
\* engine.go's Run): how a part of the tick reads the sprint from its twin
\* instead of reading the four tables whole, and why what it plans on is the
\* store's state at one generation of the fence. Written for PR 4906 (the
\* owner's requirement of 2026-09-30, "the whole intent is sub-second ticks").
\*
\* THE STORE.
\*   gen, pend    the sprint's fence: its generation, and the operation in
\*                flight ("none", or "w": another writer's). Every write of a
\*                record takes the fence at the generation its plan read
\*                (Acquire: gen' = gen + 1, only if gen is the one read).
\*   rec          each table's records (a value per key; 0 is none)
\*   rev          each table's revision: every write of the table moves it,
\*                a record's or a display cell's (outside the fence)
\*   log          each table's change stream: one event per write, the
\*                revision it left and the records it named (a display write
\*                names none)
\*   running      the machine's state, read with the fence
\*
\* THE TICK'S TWIN.
\*   tv           the twin holds the tables (a read cut short, or a dropped
\*                twin, holds none: its next read reads them whole)
\*   tg           the generation its last closed read or commit was at
\*   trec, trev   the records it holds and the revision of each table it is at
\*
\* A PART OF THE TICK (pc).
\*   "idle"  -> Begin: the view's exchange (the fence first: a pending
\*              operation waits; the shapes: each table's revision). A part's
\*              FIRST read that finds the machine STOPPED halts the part
\*              (it begins nothing); a later read of a part in flight goes on.
\*   "view"  -> CatchUp(t): a table whose revision moved since the twin's is
\*              brought to the shape's revision: the records its change stream
\*              names between the two, read again (refused, and read again
\*              from Begin, when the table moved after the shape: movedError).
\*              ReadWhole: a twin that holds no tables reads them whole, at
\*              the shapes' revisions, all or none (the two pipelined stages
\*              and the trailing fence of pipeline_load.go, one step here).
\*              Close: the fence again, last: the same generation and nothing
\*              pending, or the read is taken again.
\*   "plan"  -> Commit: the part's write, at the generation its view read
\*              (the Acquire), its receipt applied to the twin (the record,
\*              and the table's revision the store left); or Lost: the
\*              generation moved, and the part reads again.
\*   PassOver: a part with nothing to do on the twin is passed over, reading
\*              no store (it writes nothing: nothing to check).
\*   Drop: any path the engine does not know the end of drops the twin.
\*   Lock: a part that lost a try takes the fence before its next read
\*              (lock.go): nothing else writes until its write, handed the
\*              fence, commits, or NoWrite releases it.
\*
\* THE OTHER WRITERS. W: another process's step (Acquire, its write, its
\* Release; its write names its record in the change stream). D: a display
\* cell, outside the fence (the table's revision moves; no record changes;
\* no generation). Stop / Start: the machine's state.
\*
\* WHAT IS CHECKED.
\*   ViewIsSnapshot   a part that plans, at the generation the store is at
\*                    with nothing pending, plans on the store's records
\*   TwinIsTheStore   a twin at the store's generation, nothing pending,
\*                    holds the store's records
\*   BeganRunning     no part writes that found the machine STOPPED at its
\*                    first read
\*   TwinNotAhead     the twin is never at a revision the table has not reached
\*   LockedApplyNotLost  a part that plans under its lock still holds the fence
\*                    at the generation it read: its write cannot be lost

EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS Tables, Keys, Vals, MaxW, MaxD, MaxParts,
          Broken \* "none"; a reversed witness breaks one rule: "lock" (the
                 \* lock marks the part locked without taking the fence),
                 \* "acquire" (the
                 \* write takes the fence whatever generation its plan read,
                 \* the engine's one guard), "halt" (no halt on a first
                 \* read of a STOPPED machine), "catchup" (the catch-up misses
                 \* the last write its change stream names)

ASSUME 0 \in Vals

VARIABLES gen, pend, rec, rev, log, running,
          locked, lost,
          tv, tg, trec, trev,
          pc, vgen, vrev, first, began, parts,
          wn, dn, wt, wk, wv

vars == <<gen, pend, rec, rev, log, running, locked, lost, tv, tg, trec, trev, pc, vgen, vrev, first, began, parts, wn, dn, wt, wk, wv>>

Zero == [t \in Tables |-> [k \in Keys |-> 0]]

Init ==
  /\ gen = 0 /\ pend = "none" /\ rec = Zero /\ rev = [t \in Tables |-> 0]
  /\ log = [t \in Tables |-> <<>>] /\ running = TRUE
  /\ tv = FALSE /\ tg = 0 /\ trec = Zero /\ trev = [t \in Tables |-> 0]
  /\ pc = "idle" /\ vgen = 0 /\ vrev = [t \in Tables |-> 0]
  /\ first = TRUE /\ began = TRUE /\ parts = 0
  /\ locked = FALSE /\ lost = FALSE
  /\ wn = 0 /\ dn = 0 /\ wv = 0
  /\ wt = (CHOOSE t \in Tables : TRUE)
  /\ wk = (CHOOSE k \in Keys : TRUE)

\* the records a table's change stream names between two revisions
Named(t, from, to) ==
  UNION {log[t][i][2] : i \in {j \in 1..Len(log[t]) : log[t][j][1] > from /\ log[t][j][1] <= to}}

--------------------------------------------------------------------------------
\* THE TICK'S PART

\* the fence is free for the part: nothing in flight, or the part's own lock
Free == pend = "none" \/ (locked /\ pend = "t")

Begin ==
  /\ pc = "idle" /\ parts < MaxParts /\ Free
  /\ IF first /\ ~running /\ Broken # "halt"
       THEN \* the part's first read finds the machine STOPPED: it begins nothing
            /\ parts' = parts + 1 /\ first' = TRUE
            /\ UNCHANGED <<pc, vgen, vrev, began>>
       ELSE /\ pc' = "view" /\ vgen' = gen /\ vrev' = rev
            /\ began' = IF first THEN running ELSE began
            /\ first' = FALSE
            /\ UNCHANGED parts
  /\ UNCHANGED <<gen, pend, rec, rev, log, running, tv, tg, trec, trev, wn, dn, wt, wk, wv, locked, lost>>

CatchUp(t) ==
  /\ pc = "view" /\ tv /\ trev[t] # vrev[t]
  /\ IF rev[t] = vrev[t]
       THEN \* the records named since the twin's revision, read at the shape's
            /\ trec' = [trec EXCEPT ![t] = [k \in Keys |-> IF k \in Named(t, trev[t], IF Broken = "catchup" THEN vrev[t] - 1 ELSE vrev[t]) THEN rec[t][k] ELSE trec[t][k]]]
            /\ trev' = [trev EXCEPT ![t] = vrev[t]]
            /\ UNCHANGED pc
       ELSE \* the table moved after its shape: read again
            /\ pc' = "idle" /\ UNCHANGED <<trec, trev>>
  /\ UNCHANGED <<gen, pend, rec, rev, log, running, tv, tg, vgen, vrev, first, began, parts, wn, dn, wt, wk, wv, locked, lost>>

ReadWhole ==
  /\ pc = "view" /\ ~tv
  /\ IF \A t \in Tables : rev[t] = vrev[t]
       THEN /\ tv' = TRUE /\ trec' = rec /\ trev' = vrev /\ UNCHANGED pc
       ELSE /\ pc' = "idle" /\ UNCHANGED <<tv, trec, trev>>
  /\ UNCHANGED <<gen, pend, rec, rev, log, running, tg, vgen, vrev, first, began, parts, wn, dn, wt, wk, wv, locked, lost>>

Close ==
  /\ pc = "view" /\ tv /\ \A t \in Tables : trev[t] = vrev[t]
  /\ IF gen = vgen /\ Free
       THEN pc' = "plan" /\ tg' = vgen
       ELSE pc' = "idle" /\ UNCHANGED tg
  /\ UNCHANGED <<gen, pend, rec, rev, log, running, tv, trec, trev, vgen, vrev, first, began, parts, wn, dn, wt, wk, wv, locked, lost>>

Commit(t, k, v) ==
  /\ pc = "plan"
  /\ IF (gen = vgen /\ Free) \/ Broken = "acquire"
       THEN \* the Acquire at the generation read (or, locked, the lock handed
            \* to the operation: the generation is the one the lock set); the
            \* write; the receipt on the twin; the fence released
            /\ gen' = IF locked THEN gen ELSE gen + 1
            /\ rec' = [rec EXCEPT ![t][k] = v]
            /\ rev' = [rev EXCEPT ![t] = rev[t] + 1]
            /\ log' = [log EXCEPT ![t] = Append(log[t], <<rev[t] + 1, {k}>>)]
            /\ trec' = [trec EXCEPT ![t][k] = v]
            /\ trev' = [trev EXCEPT ![t] = rev[t] + 1]
            /\ tg' = IF locked THEN gen ELSE gen + 1
            /\ pend' = IF locked THEN "none" ELSE pend
            /\ locked' = FALSE /\ lost' = FALSE
            /\ pc' = "idle" /\ parts' = parts + 1 /\ first' = TRUE
       ELSE \* lost to another writer: the part reads again (it began: no
            \* halt), and takes the fence before that read (Lock)
            /\ pc' = "idle" /\ lost' = TRUE
            /\ UNCHANGED <<gen, pend, rec, rev, log, tg, trec, trev, parts, first, locked>>
  /\ UNCHANGED <<running, tv, vgen, vrev, began, wn, dn, wt, wk, wv>>

\* A part that lost a try takes the fence before its next read: once, and
\* only while nothing else holds it (lock.go). The broken witness marks the
\* part locked without taking the fence.
Lock ==
  /\ pc = "idle" /\ lost /\ ~locked /\ pend = "none"
  /\ locked' = TRUE
  /\ IF Broken = "lock"
       THEN UNCHANGED <<gen, pend>>
       ELSE pend' = "t" /\ gen' = gen + 1
  /\ UNCHANGED <<rec, rev, log, running, lost, tv, tg, trec, trev, pc, vgen, vrev, first, began, parts, wn, dn, wt, wk, wv>>

\* A part with nothing to write ends; a lock it holds is released unwritten.
NoWrite ==
  /\ pc = "plan"
  /\ pc' = "idle" /\ parts' = parts + 1 /\ first' = TRUE
  /\ locked' = FALSE /\ lost' = FALSE
  /\ pend' = IF locked THEN "none" ELSE pend
  /\ UNCHANGED <<gen, rec, rev, log, running, tv, tg, trec, trev, vgen, vrev, began, wn, dn, wt, wk, wv>>

PassOver ==
  /\ pc = "idle" /\ first /\ ~locked /\ parts < MaxParts
  /\ parts' = parts + 1
  /\ UNCHANGED <<gen, pend, rec, rev, log, running, tv, tg, trec, trev, pc, vgen, vrev, first, began, wn, dn, wt, wk, wv, locked, lost>>

Drop ==
  /\ pc = "idle" /\ tv
  /\ tv' = FALSE
  /\ UNCHANGED <<gen, pend, rec, rev, log, running, tg, trec, trev, pc, vgen, vrev, first, began, parts, wn, dn, wt, wk, wv, locked, lost>>

--------------------------------------------------------------------------------
\* THE OTHER WRITERS

WAcquire(t, k, v) ==
  /\ pend = "none" /\ wn < MaxW
  /\ pend' = "w" /\ gen' = gen + 1 /\ wt' = t /\ wk' = k /\ wv' = v
  /\ UNCHANGED <<rec, rev, log, running, tv, tg, trec, trev, pc, vgen, vrev, first, began, parts, wn, dn, locked, lost>>

WApply ==
  /\ pend = "w"
  /\ rec' = [rec EXCEPT ![wt][wk] = wv]
  /\ rev' = [rev EXCEPT ![wt] = rev[wt] + 1]
  /\ log' = [log EXCEPT ![wt] = Append(log[wt], <<rev[wt] + 1, {wk}>>)]
  /\ pend' = "applied"
  /\ UNCHANGED <<gen, running, tv, tg, trec, trev, pc, vgen, vrev, first, began, parts, wn, dn, wt, wk, wv, locked, lost>>

WRelease ==
  /\ pend = "applied"
  /\ pend' = "none" /\ wn' = wn + 1
  /\ UNCHANGED <<gen, rec, rev, log, running, tv, tg, trec, trev, pc, vgen, vrev, first, began, parts, dn, wt, wk, wv, locked, lost>>

Display(t) ==
  /\ dn < MaxD
  /\ rev' = [rev EXCEPT ![t] = rev[t] + 1]
  /\ log' = [log EXCEPT ![t] = Append(log[t], <<rev[t] + 1, {}>>)]
  /\ dn' = dn + 1
  /\ UNCHANGED <<gen, pend, rec, running, tv, tg, trec, trev, pc, vgen, vrev, first, began, parts, wn, wt, wk, wv, locked, lost>>

Stop ==
  /\ running /\ running' = FALSE
  /\ UNCHANGED <<gen, pend, rec, rev, log, tv, tg, trec, trev, pc, vgen, vrev, first, began, parts, wn, dn, wt, wk, wv, locked, lost>>

Next ==
  \/ Begin \/ ReadWhole \/ Close \/ PassOver \/ Drop \/ Lock \/ NoWrite
  \/ \E t \in Tables : CatchUp(t)
  \/ \E t \in Tables, k \in Keys, v \in Vals : Commit(t, k, v)
  \/ \E t \in Tables, k \in Keys, v \in Vals : WAcquire(t, k, v)
  \/ WApply \/ WRelease
  \/ \E t \in Tables : Display(t)
  \/ Stop

Spec == Init /\ [][Next]_vars

--------------------------------------------------------------------------------
\* INVARIANTS

TypeOK ==
  /\ pc \in {"idle", "view", "plan"}
  /\ pend \in {"none", "w", "applied", "t"}
  /\ locked \in BOOLEAN /\ lost \in BOOLEAN
  /\ tv \in BOOLEAN /\ running \in BOOLEAN

\* A part that plans, at the generation the store is at with nothing pending,
\* plans on the store's records.
ViewIsSnapshot ==
  (pc = "plan" /\ gen = vgen /\ Free) => trec = rec

\* A part that plans under its lock plans at the generation it read with the
\* fence still its own: its write cannot be lost to another writer.
LockedApplyNotLost ==
  (pc = "plan" /\ locked) => (gen = vgen /\ pend = "t")

\* A twin at the store's generation, nothing pending, holds the store's
\* records (after its part closed its read, or committed).
TwinIsTheStore ==
  (tv /\ pc = "idle" /\ tg = gen /\ pend = "none") => trec = rec

\* No part writes that found the machine STOPPED at its first read.
BeganRunning == pc = "plan" => began

\* The twin is never at a revision its table has not reached.
TwinNotAhead == \A t \in Tables : trev[t] <= rev[t]

=============================================================================
