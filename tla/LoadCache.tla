------------------------------ MODULE LoadCache ------------------------------
\* The load cache (internal/sprint/store/loadcache.go, load.go placedRecords,
\* pipeline_load.go): a process keeps each table's placed records as its last
\* whole read found them, at the revision read, with the mark of the change event
\* that left that revision (its stream id), and a later load of the table brings
\* them to the shape's revision from the table's change stream instead of reading
\* the table whole (2026-10-10: the server read the fleet table whole about 56
\* times a second, most of it finished cards). The catch-up is the tick's twin's
\* (tla/DirtyTickRead.tla, CatchUp); what is new is that the cache is shared by
\* loads running at once (one lock per table, held across a table's catch-up),
\* never writes, is read without the fence (a fenced read reads the fence after,
\* as DirtyTickRead's Close does), and outlives a store that loses its last writes
\* (a Redis restart from an AOF synced every second, an RDB or a backup restore:
\* the same epoch, a lower revision, and a new history written from there, whose
\* events are new events with new marks).
\*
\* THE STORE.
\*   rec, rev, log  each table's records, its revision, its change stream (one
\*                  event per write: the revision it left, the keys it named, its
\*                  mark; a display write names none)
\*   hist           each table's records at every revision of its present
\*                  history: hist[t][r+1] is the table at revision r (what a whole
\*                  read of revision r gives)
\*   mk             the marks given so far (every event gets a new one)
\*
\* THE CACHE (one per process; its tables each behind their own lock).
\*   cv, crec, crev, cmark  the cache holds the table, its records, their
\*                  revision, and the mark of the event that left that revision
\*                  (0: revision 0, the epoch's empty table, left by no event)
\*
\* A LOAD (l in Loaders, running at once).
\*   "idle" -> Begin: the shapes, one exchange: each table's revision (vrev).
\*   "view" -> Take(t): one table, under its lock, all or nothing:
\*               the store behind the cache (crev > the shape's revision): the
\*                 entry is dropped, and a later Take reads the table whole;
\*               the cache at or behind the shape's revision: the stream is read
\*                 back from the shape's revision to the cache's; its event at the
\*                 cache's revision must have the cache's mark, else the entry is
\*                 dropped; the keys written between are read at the shape's
\*                 revision (refused when the table moved after the shape:
\*                 movedError, and the load begins again), the cache moves on;
\*               not held: the mark at the shape's revision read, then the table
\*                 read whole at it (refused when it moved), kept with the mark.
\*             Drop(t): the cache forgets a table (its counts did not agree).
\*             End: every table taken: the load returns.
\*
\* WHAT IS CHECKED.
\*   LoadIsSnapshot  every table a load took was, when it took it, the store's
\*                   records at the revision its shape read
\*   CacheIsHistory  a table the cache holds whose mark is still the store's
\*                   event at its revision is the store's records at it
\* Reversed witnesses (Broken): "catchup" (the catch-up misses the last write its
\* stream names), "ahead" (a cache later than the shape used as it is),
\* "nomark" (the mark is not checked: a store that lost writes and wrote others to
\* the cache's revision or past it gives the cache's history as its own).

EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS Tables, Keys, Vals, Loaders, MaxW, MaxLoads, MaxRollbacks, Broken

ASSUME 0 \in Vals

VARIABLES rec, rev, log, hist, wn, mk, rb,
          cv, crec, crev, cmark,
          pc, vrev, out, done, good, loads

vars == <<rec, rev, log, hist, wn, mk, rb, cv, crec, crev, cmark, pc, vrev, out, done, good, loads>>
storeVars == <<rec, rev, log, hist, wn, mk, rb>>
cacheVars == <<cv, crec, crev, cmark>>

ZeroT == [k \in Keys |-> 0]

Init ==
  /\ rec = [t \in Tables |-> ZeroT] /\ rev = [t \in Tables |-> 0]
  /\ log = [t \in Tables |-> <<>>] /\ hist = [t \in Tables |-> <<ZeroT>>]
  /\ wn = 0 /\ mk = 0 /\ rb = 0
  /\ cv = [t \in Tables |-> FALSE] /\ crec = [t \in Tables |-> ZeroT]
  /\ crev = [t \in Tables |-> 0] /\ cmark = [t \in Tables |-> 0]
  /\ pc = [l \in Loaders |-> "idle"] /\ vrev = [l \in Loaders |-> [t \in Tables |-> 0]]
  /\ out = [l \in Loaders |-> [t \in Tables |-> ZeroT]]
  /\ done = [l \in Loaders |-> [t \in Tables |-> FALSE]]
  /\ good = [l \in Loaders |-> [t \in Tables |-> TRUE]]
  /\ loads = 0

\* the keys a table's change stream names between two revisions
Named(t, from, to) ==
  UNION {log[t][i][2] : i \in {j \in 1..Len(log[t]) : log[t][j][1] > from /\ log[t][j][1] <= to}}

\* no event: a mark no event has (events are marked 1..MaxW)
NoEvent == MaxW + 1

\* the mark of the event that left revision r in the present stream: 0 for
\* revision 0, NoEvent when no event left it (the store is behind r)
MarkAt(t, r) ==
  IF r = 0 THEN 0
  ELSE IF \E i \in 1..Len(log[t]) : log[t][i][1] = r
         THEN log[t][CHOOSE i \in 1..Len(log[t]) : log[t][i][1] = r][3]
         ELSE NoEvent

\* what a whole read of revision r gives, in the present history (ZeroT past it)
At(t, r) == IF r < Len(hist[t]) THEN hist[t][r + 1] ELSE ZeroT
HasAt(t, r) == r < Len(hist[t])

--------------------------------------------------------------------------------
\* A LOAD

Begin(l) ==
  /\ pc[l] = "idle" /\ loads < MaxLoads
  /\ pc' = [pc EXCEPT ![l] = "view"] /\ vrev' = [vrev EXCEPT ![l] = rev]
  /\ done' = [done EXCEPT ![l] = [t \in Tables |-> FALSE]]
  /\ good' = [good EXCEPT ![l] = [t \in Tables |-> TRUE]]
  /\ loads' = loads + 1
  /\ UNCHANGED <<storeVars, cacheVars, out>>

\* the load begins again: a table moved after its shape
Again(l) ==
  /\ pc' = [pc EXCEPT ![l] = "idle"]
  /\ UNCHANGED <<cacheVars, out, done, good>>

\* table t of load l taken with records r: good when they are the store's at the
\* shape's revision, in the history the store has as it is taken
Took(l, t, r) ==
  /\ out' = [out EXCEPT ![l][t] = r]
  /\ done' = [done EXCEPT ![l][t] = TRUE]
  /\ good' = [good EXCEPT ![l][t] = HasAt(t, vrev[l][t]) /\ r = At(t, vrev[l][t])]

DropEntry(t) ==
  /\ cv' = [cv EXCEPT ![t] = FALSE]
  /\ UNCHANGED <<crec, crev, cmark, pc, out, done, good>>

Take(l, t) ==
  /\ pc[l] = "view" /\ ~done[l][t]
  /\ LET v == vrev[l][t] IN
     IF cv[t] /\ crev[t] > v /\ Broken # "ahead"
       THEN \* the store is behind the cache: the entry dropped
            DropEntry(t)
     ELSE IF cv[t] /\ (crev[t] <= v \/ Broken = "ahead")
       THEN IF MarkAt(t, crev[t]) # cmark[t] /\ Broken # "nomark"
              THEN \* the event at the cache's revision is another: dropped
                   DropEntry(t)
            ELSE IF crev[t] >= v
              THEN \* held at the shape's revision (or, broken, later): its records
                   /\ Took(l, t, crec[t])
                   /\ UNCHANGED <<pc, cacheVars>>
            ELSE IF rev[t] = v
              THEN LET to == IF Broken = "catchup" THEN v - 1 ELSE v
                       r  == [k \in Keys |-> IF k \in Named(t, crev[t], to) THEN rec[t][k] ELSE crec[t][k]]
                   IN /\ crec' = [crec EXCEPT ![t] = r] /\ crev' = [crev EXCEPT ![t] = v]
                      /\ cmark' = [cmark EXCEPT ![t] = MarkAt(t, v)]
                      /\ Took(l, t, r)
                      /\ UNCHANGED <<pc, cv>>
            ELSE Again(l)
     ELSE \* not held: the mark, then the table read whole, kept
          IF rev[t] = v
            THEN /\ Took(l, t, rec[t])
                 /\ cv' = [cv EXCEPT ![t] = TRUE]
                 /\ crec' = [crec EXCEPT ![t] = rec[t]]
                 /\ crev' = [crev EXCEPT ![t] = v]
                 /\ cmark' = [cmark EXCEPT ![t] = MarkAt(t, v)]
                 /\ UNCHANGED pc
            ELSE Again(l)
  /\ UNCHANGED <<storeVars, vrev, loads>>

Drop(t) ==
  /\ cv[t] /\ cv' = [cv EXCEPT ![t] = FALSE]
  /\ UNCHANGED <<storeVars, crec, crev, cmark, pc, vrev, out, done, good, loads>>

End(l) ==
  /\ pc[l] = "view" /\ \A t \in Tables : done[l][t]
  /\ pc' = [pc EXCEPT ![l] = "idle"]
  /\ UNCHANGED <<storeVars, cacheVars, vrev, out, done, good, loads>>

--------------------------------------------------------------------------------
\* THE STORE: a record's write (a step's operation, any process), a display cell's
\* (its revision moves, no record changes), and a restart that loses the last
\* writes of a table (the same epoch, a lower revision: the stream and the records
\* as they were at it; the writes after it are new events with new marks)

Write(t, k, v) ==
  /\ wn < MaxW
  /\ rec' = [rec EXCEPT ![t][k] = v]
  /\ rev' = [rev EXCEPT ![t] = rev[t] + 1]
  /\ log' = [log EXCEPT ![t] = Append(log[t], <<rev[t] + 1, {k}, mk + 1>>)]
  /\ hist' = [hist EXCEPT ![t] = Append(hist[t], [rec[t] EXCEPT ![k] = v])]
  /\ wn' = wn + 1 /\ mk' = mk + 1
  /\ UNCHANGED <<rb, cacheVars, pc, vrev, out, done, good, loads>>

Display(t) ==
  /\ wn < MaxW
  /\ rev' = [rev EXCEPT ![t] = rev[t] + 1]
  /\ log' = [log EXCEPT ![t] = Append(log[t], <<rev[t] + 1, {}, mk + 1>>)]
  /\ hist' = [hist EXCEPT ![t] = Append(hist[t], rec[t])]
  /\ wn' = wn + 1 /\ mk' = mk + 1
  /\ UNCHANGED <<rec, rb, cacheVars, pc, vrev, out, done, good, loads>>

Rollback(t, r) ==
  /\ rb < MaxRollbacks /\ r < rev[t]
  /\ rev' = [rev EXCEPT ![t] = r]
  /\ rec' = [rec EXCEPT ![t] = hist[t][r + 1]]
  /\ hist' = [hist EXCEPT ![t] = SubSeq(hist[t], 1, r + 1)]
  /\ log' = [log EXCEPT ![t] = SelectSeq(log[t], LAMBDA e : e[1] <= r)]
  /\ rb' = rb + 1
  /\ UNCHANGED <<wn, mk, cacheVars, pc, vrev, out, done, good, loads>>

Next ==
  \/ \E l \in Loaders : Begin(l) \/ End(l) \/ \E t \in Tables : Take(l, t)
  \/ \E t \in Tables : Drop(t) \/ Display(t)
  \/ \E t \in Tables, k \in Keys, v \in Vals : Write(t, k, v)
  \/ \E t \in Tables, r \in 0..MaxW : Rollback(t, r)

Spec == Init /\ [][Next]_vars

--------------------------------------------------------------------------------
\* INVARIANTS

TypeOK ==
  /\ \A l \in Loaders : pc[l] \in {"idle", "view"}
  /\ \A t \in Tables : Len(hist[t]) = rev[t] + 1

\* every table a load took was, when it took it, the store's records at the
\* revision its shape read
LoadIsSnapshot ==
  \A l \in Loaders, t \in Tables : done[l][t] => good[l][t]

\* a table the cache holds whose mark is still the store's event at its revision
\* is the store's records at it
CacheIsHistory ==
  \A t \in Tables : (cv[t] /\ MarkAt(t, crev[t]) = cmark[t]) => crec[t] = At(t, crev[t])

=============================================================================
