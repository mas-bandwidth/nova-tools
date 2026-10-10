------------------------------ MODULE LoadCache ------------------------------
\* The load cache (internal/sprint/store/loadcache.go, load.go placedRecords,
\* pipeline_load.go): a process keeps each table's placed records as its last
\* whole read found them, at the revision read, and a later load of the table
\* brings them to the shape's revision from the table's change stream instead
\* of reading the table whole (2026-10-10: the server read the fleet table
\* whole about 56 times a second, most of it finished cards). The catch-up is
\* the tick's twin's (tla/DirtyTickRead.tla, CatchUp); what is new is that the
\* cache is shared by loads running at once (one lock per table, held across a
\* table's catch-up), never writes, and is read without the fence (a fenced
\* read reads the fence after, as DirtyTickRead's Close does).
\*
\* THE STORE.
\*   rec, rev, log  each table's records, its revision, its change stream (one
\*                  event per write: the revision it left and the keys it named;
\*                  a display write names none)
\*   hist           each table's records at every revision so far: hist[t][r+1]
\*                  is the table at revision r (what a whole read of revision r
\*                  gives)
\*
\* THE CACHE (one per process; its tables each behind their own lock).
\*   cv, crec, crev the cache holds the table, its records, their revision
\*
\* A LOAD (l in Loaders, running at once).
\*   "idle" -> Begin: the shapes, one exchange: each table's revision (vrev).
\*   "view" -> Take(t): one table, under its lock, all or nothing:
\*               the cache holds it at the shape's revision: its records;
\*               the cache holds it at an earlier one: the change stream names
\*                 the keys written between; they are read at the shape's
\*                 revision (refused when the table moved after the shape:
\*                 movedError, and the load begins again), the cache moves on;
\*               else (not held, or held at a LATER revision than the shape's):
\*                 the table read whole at the shape's revision (refused when it
\*                 moved), kept unless the cache is already later.
\*             Drop(t): the cache forgets a table (its counts did not agree).
\*             End: every table taken: the load returns.
\*
\* WHAT IS CHECKED.
\*   LoadIsSnapshot  every table a load took is the store's records at the
\*                   revision its shape read
\*   CacheIsHistory  a table the cache holds is the store's records at the
\*                   revision it says
\*   CacheNotAhead   the cache is never at a revision its table has not reached
\* Reversed witnesses (Broken): "catchup" (the catch-up misses the last write its
\* stream names), "ahead" (a cache later than the shape is used as it is).

EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS Tables, Keys, Vals, Loaders, MaxW, MaxLoads, Broken

ASSUME 0 \in Vals

VARIABLES rec, rev, log, hist, wn,
          cv, crec, crev,
          pc, vrev, out, done, loads

vars == <<rec, rev, log, hist, wn, cv, crec, crev, pc, vrev, out, done, loads>>

ZeroT == [k \in Keys |-> 0]

Init ==
  /\ rec = [t \in Tables |-> ZeroT] /\ rev = [t \in Tables |-> 0]
  /\ log = [t \in Tables |-> <<>>] /\ hist = [t \in Tables |-> <<ZeroT>>]
  /\ wn = 0
  /\ cv = [t \in Tables |-> FALSE] /\ crec = [t \in Tables |-> ZeroT] /\ crev = [t \in Tables |-> 0]
  /\ pc = [l \in Loaders |-> "idle"] /\ vrev = [l \in Loaders |-> [t \in Tables |-> 0]]
  /\ out = [l \in Loaders |-> [t \in Tables |-> ZeroT]]
  /\ done = [l \in Loaders |-> [t \in Tables |-> FALSE]]
  /\ loads = 0

\* the keys a table's change stream names between two revisions
Named(t, from, to) ==
  UNION {log[t][i][2] : i \in {j \in 1..Len(log[t]) : log[t][j][1] > from /\ log[t][j][1] <= to}}

--------------------------------------------------------------------------------
\* A LOAD

Begin(l) ==
  /\ pc[l] = "idle" /\ loads < MaxLoads
  /\ pc' = [pc EXCEPT ![l] = "view"] /\ vrev' = [vrev EXCEPT ![l] = rev]
  /\ done' = [done EXCEPT ![l] = [t \in Tables |-> FALSE]]
  /\ loads' = loads + 1
  /\ UNCHANGED <<rec, rev, log, hist, wn, cv, crec, crev, out>>

\* the load begins again: a table moved after its shape
Again(l) ==
  /\ pc' = [pc EXCEPT ![l] = "idle"]
  /\ UNCHANGED <<cv, crec, crev, out, done>>

Take(l, t) ==
  /\ pc[l] = "view" /\ ~done[l][t]
  /\ LET v == vrev[l][t] IN
     IF cv[t] /\ (crev[t] = v \/ (crev[t] > v /\ Broken = "ahead"))
       THEN \* held at the shape's revision: its records
            /\ out' = [out EXCEPT ![l][t] = crec[t]]
            /\ done' = [done EXCEPT ![l][t] = TRUE]
            /\ UNCHANGED <<pc, cv, crec, crev>>
     ELSE IF cv[t] /\ crev[t] < v
       THEN IF rev[t] = v
              THEN LET to == IF Broken = "catchup" THEN v - 1 ELSE v
                       r  == [k \in Keys |-> IF k \in Named(t, crev[t], to) THEN rec[t][k] ELSE crec[t][k]]
                   IN /\ crec' = [crec EXCEPT ![t] = r] /\ crev' = [crev EXCEPT ![t] = v]
                      /\ out' = [out EXCEPT ![l][t] = r]
                      /\ done' = [done EXCEPT ![l][t] = TRUE]
                      /\ UNCHANGED <<pc, cv>>
              ELSE Again(l)
     ELSE \* not held, or held at a later revision: read whole
          IF rev[t] = v
            THEN /\ out' = [out EXCEPT ![l][t] = rec[t]]
                 /\ done' = [done EXCEPT ![l][t] = TRUE]
                 /\ IF cv[t] /\ crev[t] > v
                      THEN UNCHANGED <<cv, crec, crev>>
                      ELSE /\ cv' = [cv EXCEPT ![t] = TRUE]
                           /\ crec' = [crec EXCEPT ![t] = rec[t]]
                           /\ crev' = [crev EXCEPT ![t] = v]
                 /\ UNCHANGED pc
            ELSE Again(l)
  /\ UNCHANGED <<rec, rev, log, hist, wn, vrev, loads>>

Drop(t) ==
  /\ cv[t] /\ cv' = [cv EXCEPT ![t] = FALSE]
  /\ UNCHANGED <<rec, rev, log, hist, wn, crec, crev, pc, vrev, out, done, loads>>

End(l) ==
  /\ pc[l] = "view" /\ \A t \in Tables : done[l][t]
  /\ pc' = [pc EXCEPT ![l] = "idle"]
  /\ UNCHANGED <<rec, rev, log, hist, wn, cv, crec, crev, vrev, out, done, loads>>

--------------------------------------------------------------------------------
\* THE WRITERS: a record's write (a step's operation, any process), and a display
\* cell's (its revision moves, no record changes)

Write(t, k, v) ==
  /\ wn < MaxW
  /\ rec' = [rec EXCEPT ![t][k] = v]
  /\ rev' = [rev EXCEPT ![t] = rev[t] + 1]
  /\ log' = [log EXCEPT ![t] = Append(log[t], <<rev[t] + 1, {k}>>)]
  /\ hist' = [hist EXCEPT ![t] = Append(hist[t], [rec[t] EXCEPT ![k] = v])]
  /\ wn' = wn + 1
  /\ UNCHANGED <<cv, crec, crev, pc, vrev, out, done, loads>>

Display(t) ==
  /\ wn < MaxW
  /\ rev' = [rev EXCEPT ![t] = rev[t] + 1]
  /\ log' = [log EXCEPT ![t] = Append(log[t], <<rev[t] + 1, {}>>)]
  /\ hist' = [hist EXCEPT ![t] = Append(hist[t], rec[t])]
  /\ wn' = wn + 1
  /\ UNCHANGED <<rec, cv, crec, crev, pc, vrev, out, done, loads>>

Next ==
  \/ \E l \in Loaders : Begin(l) \/ End(l) \/ \E t \in Tables : Take(l, t)
  \/ \E t \in Tables : Drop(t) \/ Display(t)
  \/ \E t \in Tables, k \in Keys, v \in Vals : Write(t, k, v)

Spec == Init /\ [][Next]_vars

--------------------------------------------------------------------------------
\* INVARIANTS

TypeOK ==
  /\ \A l \in Loaders : pc[l] \in {"idle", "view"}
  /\ \A t \in Tables : Len(hist[t]) = rev[t] + 1

\* every table a load took is the store's records at the revision its shape read
LoadIsSnapshot ==
  \A l \in Loaders, t \in Tables : done[l][t] => out[l][t] = hist[t][vrev[l][t] + 1]

\* a table the cache holds is the store's records at the revision it says
CacheIsHistory ==
  \A t \in Tables : cv[t] => crec[t] = hist[t][crev[t] + 1]

\* the cache is never at a revision its table has not reached
CacheNotAhead == \A t \in Tables : crev[t] <= rev[t]

=============================================================================
