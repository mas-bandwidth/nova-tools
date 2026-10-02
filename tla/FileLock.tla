----------------------------- MODULE FileLock -----------------------------
\* A lock on a file, across processes: one holder at a time, and a refusal
\* that tells the truth. The design of the shared module internal/filelock, written
\* from the locks nova-tools already has (internal/bus, merge, tokens, swarm,
\* wake and update each carry a copy) and against the first candidate,
\* nova-tools#4473 at d653eb53e (internal/filelock/filelock_unix.go).
\*
\* The rule the design stands on is internal/merge/lock.go's: THE KERNEL
\* RELEASES THE LOCK WHEN ITS HOLDER DIES, so there is nothing to break and
\* no age to compute. The lock is the kernel's lock on the file (flock,
\* LockFileEx) and nothing else. What is written in the file is a note, read
\* by people and by refusals, and never what decides.
\*
\* The state: which file is at the path (path; a file is an inode, and a
\* path can come to name another one), what is written in each file (stamp:
\* a process or nobody), the kernel's lock on each file (ex: the one
\* exclusive holder; sh: the shared holders), and for each process where it
\* is (pc) and which file it has open (fd). The outside: a process dies
\* wherever it is, and its pid comes back as another process. crashed, lied
\* and toldWrong are history, kept so the invariants can say what happened.
\*
\* The design, Broken = {}:
\*   take     open the path, creating the file when absent; ask the kernel
\*            for the exclusive lock without waiting; holding it, read what
\*            the file says, write this process in, and only then hold.
\*            What the file said is handed to the caller: empty means the
\*            last holder released; a name means it never did.
\*   refused  ask for a shared lock: refused too, there is an exclusive
\*            holder and the answer is "held"; granted, the only others are
\*            askers, so let go and try again, a bounded number of times,
\*            and then the answer is "busy", never "held".
\*   release  clear what the file says, then let the kernel lock go. The
\*            file stays.
\*   The file is never removed, by anyone. Waiting (Lock with a bound) is
\*   take again, and adds no state.
\*   A probe (ask who holds it without taking) was here until 2026-10-02 and
\*   left with the module's Probe, which no caller used; it can return with a
\*   caller. HeldIsTrue is still the refused taker's: in Blocked it answers
\*   "held" only when its shared ask is refused too, and the askers in the way
\*   are other refused takers asking (the "peek" state).
\*
\* Every other value of Broken is a reversed witness:
\*   "stale"      a name in the file whose process is dead is a refusal, and
\*                clearing it removes the file at the path (d653eb53e:
\*                lockInternal lines 71 and 97, ClearStaleWithOptions line
\*                221; the rule internal/merge/lock.go records as having
\*                lost 3 of 20 concurrent writes on 2026-09-11)
\*   "sentinel"   the lock is a file whose existence means held, so a death
\*                releases nothing (the lock_other.go of internal/bus,
\*                tokens and swarm, and internal/wake/lockprobe_other.go,
\*                where there is no flock; each says so of itself)
\*   "busyisheld" a refused taker whose shared ask is granted (only askers
\*                in the way) answers "held" all the same, where take.go lets
\*                go and tries again and then answers busy
\*   "unlink"     release removes the file
\*   "keepstamp"  release leaves the name in the file
\* The first two are in code that exists or existed, each read against the lines
\* named. The last three are misimplementations the invariants are shown to
\* catch.
\*
\* Not here: the wait and its jitter (a bound on retries, not a state), what
\* the stamp holds beside the process (host, start time, label), a path that
\* is a directory, a symlink or a FIFO, permissions, a file system whose
\* locks are not the kernel's (NFS). Those are tests of the module.
EXTENDS Naturals, FiniteSets
CONSTANTS Procs, MaxInodes, MaxLives, Broken
Faults == {"stale", "sentinel", "busyisheld", "unlink", "keepstamp"}
None == 0
Nobody == 0
ASSUME Broken \subseteq Faults /\ 0 \notin Procs
Inodes == 1..MaxInodes
VARIABLES path, used, stamp, ex, sh, crashed, alive, lives, pc, fd,
          lied, toldWrong
vars == <<path, used, stamp, ex, sh, crashed, alive, lives, pc, fd,
          lied, toldWrong>>
files == <<path, used, stamp>>
kernel == <<ex, sh>>
life == <<alive, lives>>
history == <<crashed, lied, toldWrong>>

Init ==
 /\ path = None /\ used = 0
 /\ stamp = [i \in Inodes |-> Nobody]
 /\ ex = [i \in Inodes |-> None] /\ sh = [i \in Inodes |-> {}]
 /\ crashed = [i \in Inodes |-> FALSE]
 /\ alive = [p \in Procs |-> TRUE] /\ lives = [p \in Procs |-> 0]
 /\ pc = [p \in Procs |-> "idle"] /\ fd = [p \in Procs |-> None]
 /\ lied = FALSE /\ toldWrong = FALSE

Go(p, to) == pc' = [pc EXCEPT ![p] = to]
Close(p) == fd' = [fd EXCEPT ![p] = None]
\* The kernel's two grants.
ExFree(i, p) == ex[i] = None /\ sh[i] \ {p} = {}
ShFree(i) == ex[i] = None
\* A process that has taken the lock as a lock, holding it or about to.
Holder(i) == ex[i] # None /\ pc[ex[i]] \in {"locked", "held"}
\* An answer of held is true when a holder is there, and a lie otherwise.
Answer(i, held) == lied' = (lied \/ (held # Holder(i)))

\* ---- take
Open(p) ==
 /\ pc[p] = "idle"
 /\ IF path = None
    THEN /\ used < MaxInodes
         /\ used' = used + 1 /\ path' = used + 1
         /\ fd' = [fd EXCEPT ![p] = used + 1]
    ELSE /\ fd' = [fd EXCEPT ![p] = path]
         /\ UNCHANGED <<path, used>>
 /\ Go(p, "opened")
 /\ UNCHANGED <<stamp, kernel, life, history>>

TakeEx(p) ==
 /\ pc[p] = "opened"
 /\ IF ExFree(fd[p], p)
    THEN /\ ex' = [ex EXCEPT ![fd[p]] = p]
         /\ Go(p, "locked")
    ELSE /\ Go(p, "blocked")
         /\ UNCHANGED ex
 /\ UNCHANGED <<files, sh, fd, life, history>>

\* Refused. The design asks the kernel whether the one in the way holds it.
Blocked(p) ==
 /\ pc[p] = "blocked"
 /\ LET i == fd[p]
    IN IF Broken \cap {"stale", "sentinel", "busyisheld"} # {} \/ ~ShFree(i)
       THEN \* answered "held": on the refusal alone (the candidate, the
            \* sentinel), on a granted ask ("busyisheld"), or on the second
            \* refusal (the design)
            /\ Answer(i, TRUE)
            /\ Go(p, "idle") /\ Close(p)
            /\ UNCHANGED sh
       ELSE /\ sh' = [sh EXCEPT ![i] = @ \cup {p}]
            /\ Go(p, "peek")
            /\ UNCHANGED <<fd, lied>>
 /\ UNCHANGED <<files, ex, life, crashed, toldWrong>>

\* Only askers were in the way: let go, and try again or answer "busy".
Peek(p) ==
 /\ pc[p] = "peek"
 /\ sh' = [sh EXCEPT ![fd[p]] = @ \ {p}]
 /\ \/ Go(p, "opened") /\ UNCHANGED fd
    \/ Go(p, "idle") /\ Close(p)
 /\ UNCHANGED <<files, ex, life, history>>

\* Holding the kernel lock: read the file, write this process in, hold.
Stamp(p) ==
 /\ pc[p] = "locked"
 /\ LET i == fd[p]
        found == stamp[i]
    IN IF "stale" \in Broken /\ found # Nobody /\ ~alive[found]
       THEN \* the candidate: a dead name is a refusal
            /\ ex' = [ex EXCEPT ![i] = None]
            /\ Go(p, "idle") /\ Close(p)
            /\ UNCHANGED <<stamp, crashed, toldWrong>>
       ELSE /\ stamp' = [stamp EXCEPT ![i] = p]
            /\ toldWrong' = (toldWrong \/ ((found # Nobody) # crashed[i]))
            /\ crashed' = [crashed EXCEPT ![i] = FALSE]
            /\ Go(p, "held")
            /\ UNCHANGED <<ex, fd>>
 /\ UNCHANGED <<path, used, sh, life, lied>>

\* ---- release
Release(p) ==
 /\ pc[p] = "held"
 /\ LET i == fd[p]
    IN /\ stamp' = [stamp EXCEPT ![i] =
                      IF "keepstamp" \in Broken THEN @ ELSE Nobody]
       /\ ex' = [ex EXCEPT ![i] = None]
       /\ path' = IF "unlink" \in Broken /\ path = i THEN None ELSE path
 /\ Go(p, "idle") /\ Close(p)
 /\ UNCHANGED <<used, sh, life, history>>

\* ---- clearing a stale lock: the candidate's, and only with "stale"
ClearProbe(p) ==
 /\ "stale" \in Broken
 /\ pc[p] = "idle" /\ path # None
 /\ stamp[path] # Nobody /\ ~alive[stamp[path]]
 /\ Go(p, "clear")
 /\ UNCHANGED <<files, kernel, fd, life, history>>
ClearOpen(p) ==
 /\ pc[p] = "clear"
 /\ IF path = None
    THEN Go(p, "idle") /\ UNCHANGED fd
    ELSE Go(p, "clearopen") /\ fd' = [fd EXCEPT ![p] = path]
 /\ UNCHANGED <<files, kernel, life, history>>
ClearTake(p) ==
 /\ pc[p] = "clearopen"
 /\ IF ExFree(fd[p], p)
    THEN /\ ex' = [ex EXCEPT ![fd[p]] = p]
         /\ Go(p, "cleartaken") /\ UNCHANGED fd
    ELSE /\ Go(p, "idle") /\ Close(p) /\ UNCHANGED ex
 /\ UNCHANGED <<files, sh, life, history>>
\* The second look is at the file it has open; the removal is of the path.
ClearRemove(p) ==
 /\ pc[p] = "cleartaken"
 /\ IF stamp[fd[p]] # Nobody /\ alive[stamp[fd[p]]]
    THEN UNCHANGED path
    ELSE path' = None
 /\ Go(p, "cleared")
 /\ UNCHANGED <<used, stamp, kernel, fd, life, history>>
ClearDone(p) ==
 /\ pc[p] = "cleared"
 /\ ex' = [ex EXCEPT ![fd[p]] = None]
 /\ Go(p, "idle") /\ Close(p)
 /\ UNCHANGED <<files, sh, life, history>>

\* ---- the outside
\* A process dies wherever it is. The kernel lets go of what it held.
Die(p) ==
 /\ alive[p]
 /\ alive' = [alive EXCEPT ![p] = FALSE]
 /\ crashed' = [i \in Inodes |-> crashed[i] \/ (pc[p] = "held" /\ fd[p] = i)]
 /\ ex' = [i \in Inodes |->
            IF ex[i] = p /\ "sentinel" \notin Broken THEN None ELSE ex[i]]
 /\ sh' = [i \in Inodes |-> sh[i] \ {p}]
 /\ Go(p, "dead") /\ Close(p)
 /\ UNCHANGED <<files, lives, lied, toldWrong>>
\* Its pid comes back as another process, which holds nothing.
Reborn(p) ==
 /\ ~alive[p] /\ lives[p] < MaxLives
 /\ alive' = [alive EXCEPT ![p] = TRUE]
 /\ lives' = [lives EXCEPT ![p] = @ + 1]
 /\ Go(p, "idle")
 /\ UNCHANGED <<files, kernel, fd, history>>

Step(p) ==
 \/ Open(p) \/ TakeEx(p) \/ Blocked(p) \/ Peek(p) \/ Stamp(p) \/ Release(p)
 \/ ClearProbe(p) \/ ClearOpen(p) \/ ClearTake(p) \/ ClearRemove(p)
 \/ ClearDone(p)
 \/ Die(p) \/ Reborn(p)
Next == \E p \in Procs : Step(p)
Spec == Init /\ [][Next]_vars

States == {"idle", "opened", "blocked", "peek", "locked", "held",
           "clear", "clearopen", "cleartaken", "cleared", "dead"}
TypeOK ==
 /\ path \in Inodes \cup {None} /\ used \in 0..MaxInodes
 /\ stamp \in [Inodes -> Procs \cup {Nobody}]
 /\ ex \in [Inodes -> Procs \cup {None}]
 /\ sh \in [Inodes -> SUBSET Procs]
 /\ crashed \in [Inodes -> BOOLEAN]
 /\ alive \in [Procs -> BOOLEAN] /\ lives \in [Procs -> 0..MaxLives]
 /\ pc \in [Procs -> States] /\ fd \in [Procs -> Inodes \cup {None}]
 /\ lied \in BOOLEAN /\ toldWrong \in BOOLEAN

\* One holder at a time: the lock is never handed to two callers.
MutualExclusion == Cardinality({p \in Procs : pc[p] = "held"}) <= 1
\* Who holds it holds the file at the path, by the kernel's lock.
HolderHoldsThePath ==
 \A p \in Procs : pc[p] \in {"locked", "held"} =>
   (fd[p] = path /\ ex[path] = p)
\* The file names its holder.
HolderIsNamed == \A p \in Procs : pc[p] = "held" => stamp[fd[p]] = p
\* The path names one file for ever: the lock file is never replaced.
OneFileForEver == used <= 1
\* "held" is said only of a holder: a refused taker never takes an asker for
\* one.
HeldIsTrue == ~lied
\* Who takes the lock is told, truly, whether the last holder released it.
UncleanIsTold == ~toldWrong
\* The kernel's lock is only ever with a live process that knows it has it:
\* a death leaves nothing for anybody to clear.
NothingToClear ==
 \A i \in Inodes : ex[i] # None =>
   (alive[ex[i]] /\ fd[ex[i]] = i
    /\ pc[ex[i]] \in {"locked", "held", "cleartaken", "cleared"})
=============================================================================
