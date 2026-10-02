-------------------------- MODULE FuseBoxWriters --------------------------
\* Bounded cooperating CLI writers. FuseBox is the abstract gate contract;
\* this layer exposes the read/atomic publication/verification interval.
\* One stable dedicated lock, never replaced/unlinked, protects one box.
\* FileLock supplies exclusion and kernel release on process death. Paths,
\* raw JSON, power-loss durability, dry plans and noncooperating edits are
\* outside this slice. No liveness or arbitrary-process-count claim follows.
\* Fixed requests/scenarios avoid multiplying unrelated invocation histories.
\* Broken faults are semantic mutation witnesses, not permitted behavior.
EXTENDS Naturals, FiniteSets
CONSTANTS Broken, Scenario, MaxCrashes, MaxRetries, CrashAt

Writers == {1, 2}
Surfaces == {"s1", "s2"}
Scenarios == {"lockdown-quarantine", "lockdown-lift", "two-quarantines",
              "lift-quarantine", "init-lockdown", "unreadable",
              "absent-soft", "blown-lift"}
BoxStates == [state : {"absent", "unreadable", "ok"},
              hard : BOOLEAN, soft : SUBSET Surfaces]
Empty == [state |-> "absent", hard |-> FALSE, soft |-> {}]

VARIABLES scenario, disk, owner, job, crashes, ack, event, discipline,
          recovered, successors, crashKind
vars == <<scenario, disk, owner, job, crashes, ack, event, discipline,
          recovered, successors, crashKind>>

Verb(p) ==
  CASE scenario = "lockdown-quarantine" -> IF p = 1 THEN "quarantine" ELSE "lockdown"
    [] scenario = "lockdown-lift" -> IF p = 1 THEN "lift" ELSE "lockdown"
    [] scenario = "two-quarantines" -> "quarantine"
    [] scenario = "lift-quarantine" -> IF p = 1 THEN "lift" ELSE "quarantine"
    [] scenario = "init-lockdown" -> IF p = 1 THEN "init" ELSE "lockdown"
    [] scenario = "unreadable" -> IF p = 1 THEN "quarantine" ELSE "lockdown"
    [] scenario = "absent-soft" -> IF p = 1 THEN "quarantine" ELSE "lift"
    [] scenario = "blown-lift" -> IF p = 1 THEN "lift" ELSE "quarantine"
Surface(p) == IF p = 1 THEN "s1" ELSE "s2"
Bypasses(p) == Broken = "unlocked" \/ (Broken = "liftunlocked" /\ Verb(p) = "lift")

InitialBox(s) ==
  [state |-> IF s \in {"init-lockdown", "absent-soft"} THEN "absent"
             ELSE IF s = "unreadable" THEN "unreadable" ELSE "ok",
   hard |-> s = "blown-lift",
   soft |-> IF s \in {"lockdown-lift", "blown-lift", "unreadable"} THEN {"s1"}
             ELSE IF s = "lift-quarantine" THEN Surfaces ELSE {}]
NewJob(n) == [pc |-> "idle", snap |-> Empty, readOwned |-> FALSE,
              published |-> FALSE, verified |-> FALSE,
              verifiedHard |-> FALSE, retries |-> n]

Init ==
  /\ scenario \in IF Scenario = "all" THEN Scenarios ELSE {Scenario}
  /\ disk = InitialBox(scenario)
  /\ owner = 0
  /\ job = [p \in Writers |-> NewJob(0)]
  /\ crashes = 0 /\ ack = {} /\ recovered = {} /\ successors = {}
  /\ crashKind = "none"
  /\ event = [verb |-> "none", surface |-> "s1", before |-> disk, after |-> disk]
  /\ discipline = [read |-> TRUE, preserve |-> TRUE, publish |-> TRUE, verify |-> TRUE]

Start(p) ==
  /\ job[p].pc = "idle"
  /\ job' = [job EXCEPT ![p].pc = IF Bypasses(p) \/ Broken = "latelock"
                                THEN "read" ELSE "take"]
  /\ UNCHANGED <<scenario, disk, owner, crashes, ack, event, discipline, recovered, successors, crashKind>>

Take(p) ==
  /\ job[p].pc = "take" /\ owner = 0
  /\ owner' = p
  /\ job' = [job EXCEPT ![p].pc = IF Broken = "latelock" THEN "publish" ELSE "read"]
  /\ recovered' = IF job[p].retries > 0 THEN recovered \cup {p} ELSE recovered
  /\ successors' = IF crashes > 0 /\ job[p].retries = 0 THEN successors \cup {p} ELSE successors
  /\ UNCHANGED <<scenario, disk, crashes, ack, event, discipline, crashKind>>

Read(p) ==
  /\ job[p].pc = "read"
  /\ job' = [job EXCEPT ![p].snap = disk, ![p].readOwned = (owner = p),
               ![p].pc = IF Broken = "latelock" THEN "take"
                         ELSE IF Verb(p) = "lockdown" /\ disk.state = "unreadable"
                              THEN "preserve" ELSE "publish"]
  /\ discipline' = [discipline EXCEPT !.read = @ /\ owner = p]
  /\ UNCHANGED <<scenario, disk, owner, crashes, ack, event, recovered, successors, crashKind>>

\* Evidence preservation (or its reported failure) does not change the box.
\* Both outcomes proceed to emergency lockdown; bytes/errors are not modeled.
Preserve(p) ==
  /\ job[p].pc = "preserve"
  /\ job' = [job EXCEPT ![p].pc = "publish"]
  /\ discipline' = [discipline EXCEPT !.preserve = @ /\ owner = p]
  /\ UNCHANGED <<scenario, disk, owner, crashes, ack, event, recovered, successors, crashKind>>

CanPublish(p) ==
  CASE Verb(p) = "init" -> job[p].snap.state = "absent" /\ disk.state = "absent"
    [] Verb(p) = "lockdown" -> TRUE
    [] Verb(p) = "quarantine" -> job[p].snap.state = "ok"
    [] Verb(p) = "lift" -> job[p].snap.state = "ok" /\ Surface(p) \in job[p].snap.soft

Replacement(p) ==
  CASE Verb(p) = "init" -> [state |-> "ok", hard |-> FALSE, soft |-> {}]
    [] Verb(p) = "lockdown" ->
         [state |-> "ok", hard |-> TRUE,
          soft |-> IF job[p].snap.state = "ok" THEN job[p].snap.soft ELSE {}]
    [] Verb(p) = "quarantine" -> [job[p].snap EXCEPT !.soft = @ \cup {Surface(p)}]
    [] Verb(p) = "lift" -> [job[p].snap EXCEPT !.soft = @ \ {Surface(p)}]

Publish(p) ==
  /\ job[p].pc = "publish" /\ CanPublish(p)
  /\ disk' = Replacement(p)
  /\ event' = [verb |-> Verb(p), surface |-> Surface(p), before |-> disk, after |-> disk']
  /\ job' = [job EXCEPT ![p].published = TRUE, ![p].pc = "verify"]
  /\ discipline' = [discipline EXCEPT !.publish = @ /\ owner = p /\ job[p].readOwned]
  /\ owner' = IF Broken = "earlyrelease" /\ owner = p THEN 0 ELSE owner
  /\ UNCHANGED <<scenario, crashes, ack, recovered, successors, crashKind>>

Postcondition(p) ==
  disk.state = "ok" /\
    CASE Verb(p) = "init" -> ~disk.hard /\ disk.soft = {}
      [] Verb(p) = "lockdown" -> disk.hard
      [] Verb(p) = "quarantine" -> Surface(p) \in disk.soft
      [] Verb(p) = "lift" -> Surface(p) \notin disk.soft

Verify(p) ==
  /\ job[p].pc = "verify"
  /\ job' = [job EXCEPT ![p].verified = Postcondition(p),
               ![p].verifiedHard = (Postcondition(p) /\ disk.hard), ![p].pc = "release"]
  /\ discipline' = [discipline EXCEPT !.verify = @ /\ owner = p /\ job[p].readOwned]
  /\ UNCHANGED <<scenario, disk, owner, crashes, ack, event, recovered, successors, crashKind>>

\* Release succeeds before success is rendered. A release error can leave a
\* publication recorded, but yields failure. This conservative branch drops
\* ownership; Abort below also represents verification/I/O errors.
Release(p, ok) ==
  /\ job[p].pc = "release"
  /\ owner' = IF owner = p THEN 0 ELSE owner
  /\ job' = [job EXCEPT ![p].pc = IF ok /\ job[p].published /\ job[p].verified
                                THEN "done" ELSE "failed"]
  /\ ack' = IF ok /\ job[p].published /\ job[p].verified THEN ack \cup {p} ELSE ack
  /\ UNCHANGED <<scenario, disk, crashes, event, discipline, recovered, successors, crashKind>>

\* Includes contention refusal, failed preconditions, and pre/post-publication
\* I/O/verification failure. It never rolls back a completed publication.
Abort(p) ==
  /\ job[p].pc \in {"take", "read", "preserve", "publish", "verify", "release"}
  /\ owner' = IF owner = p THEN 0 ELSE owner
  /\ job' = [job EXCEPT ![p].pc = "failed"]
  /\ UNCHANGED <<scenario, disk, crashes, ack, event, discipline, recovered, successors, crashKind>>

Crash(p) ==
  /\ job[p].pc \in {"read", "preserve", "publish", "verify", "release"}
  /\ crashes < MaxCrashes
  /\ CrashAt = "any" \/ (CrashAt = "before" /\ ~job[p].published)
                      \/ (CrashAt = "after" /\ job[p].published)
  /\ owner' = IF owner = p THEN 0 ELSE owner
  /\ job' = [job EXCEPT ![p].pc = "crashed"]
  /\ crashes' = crashes + 1
  /\ crashKind' = IF job[p].published THEN "after" ELSE "before"
  /\ UNCHANGED <<scenario, disk, ack, event, discipline, recovered, successors>>

Retry(p) ==
  /\ job[p].pc = "crashed" /\ job[p].retries < MaxRetries
  /\ job' = [job EXCEPT ![p] = NewJob(@.retries + 1)]
  /\ UNCHANGED <<scenario, disk, owner, crashes, ack, event, discipline, recovered, successors, crashKind>>

Next == \E p \in Writers :
  Start(p) \/ Take(p) \/ Read(p) \/ Preserve(p) \/ Publish(p) \/ Verify(p)
  \/ (\E ok \in BOOLEAN : Release(p, ok)) \/ Abort(p) \/ Crash(p) \/ Retry(p)
Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ scenario \in Scenarios /\ disk \in BoxStates /\ owner \in Writers \cup {0}
  /\ job \in [Writers -> [pc : {"idle", "take", "read", "preserve", "publish",
                                "verify", "release", "done", "failed", "crashed"},
                         snap : BoxStates, readOwned : BOOLEAN, published : BOOLEAN,
                         verified : BOOLEAN, verifiedHard : BOOLEAN, retries : 0..MaxRetries]]
  /\ crashes \in 0..MaxCrashes /\ ack \subseteq Writers /\ recovered \subseteq Writers
  /\ successors \subseteq Writers
  /\ crashKind \in {"none", "before", "after"}
  /\ discipline \in [read : BOOLEAN, preserve : BOOLEAN, publish : BOOLEAN, verify : BOOLEAN]
  /\ event \in [verb : {"none", "init", "lockdown", "quarantine", "lift"},
                surface : Surfaces, before : BoxStates, after : BoxStates]

OneWriterPerBox ==
  \A p \in Writers : job[p].pc \in {"read", "preserve", "publish", "verify", "release"} => owner = p
OwnerIsActive ==
  IF owner = 0 THEN TRUE
  ELSE job[owner].pc \in {"read", "preserve", "publish", "verify", "release"}
ReadAndPublishUnderSameOwnership == discipline.read /\ discipline.preserve /\ discipline.publish
OwnershipThroughVerification == ReadAndPublishUnderSameOwnership /\ discipline.verify
ToolWritesPreserveLockdown == event.before.hard => event.after.hard
InitOnlyWhereNoBox == event.verb = "init" => event.before.state = "absent"
OtherQuarantinesSurvive ==
  event.before.state = "ok" /\ event.verb \in {"lockdown", "quarantine", "lift"} =>
    (event.before.soft \ (IF event.verb = "lift" THEN {event.surface} ELSE {})) \subseteq event.after.soft
Clear(b, s) == b.state = "ok" /\ ~b.hard /\ s \notin b.soft
OnlyAuthorizedClearing ==
  \A s \in Surfaces \cup {"(none)"} :
    Clear(event.after, s) /\ ~Clear(event.before, s) =>
      event.verb = "init" \/ (event.verb = "lift" /\ s = event.surface)
SuccessWasVerified == \A p \in ack : job[p].published /\ job[p].verified /\ job[p].pc = "done"
SuccessfulLockdownWasVerified == \A p \in ack : Verb(p) = "lockdown" => job[p].verifiedHard

\* Reversed reachability witnesses. These are intentionally false in the
\* desired states; expected named violations establish non-vacuity only.
BothNeverSucceed == ack # Writers
QuarantinesNeverAccumulate == ~(ack = Writers /\ disk.soft = Surfaces)
LiftNeverKeepsOtherSurface == ~(ack = Writers /\ disk.soft = {"s2"})
CrashBeforeNeverRecovers == ~(crashKind = "before" /\ (ack \cap recovered) # {})
CrashAfterNeverRecovers == ~(crashKind = "after" /\ (ack \cap recovered) # {})
CrashNeverFreesNextWriter == ~(crashes > 0 /\ (ack \cap successors) # {})
=============================================================================
