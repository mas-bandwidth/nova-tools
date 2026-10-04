-------------------------------- MODULE BenchStage --------------------------------
\* The stage directory of one bench under fleet/tools.yml (nova-tools#5102, the
\* fast install cycle): one bench, two tools, runs that may crash anywhere and
\* run again. Written by Zhi (2026-10-02) against the first cut of the play,
\* where it found ReusedByteIdentical broken; adopted here with the send step
\* changed to the play's present rule, under the constant ByBytes.
\*
\* For each file the stage is in one of four states:
\*   absent   -- not in the stage directory
\*   seeded   -- copied, on the machine and unverified, from the installed
\*               build's directory
\*   sent     -- sent from the release build (the release's bytes)
\*   verified -- `release install` verified the whole set against SHA256SUMS
\*
\* The installed build's directory is the seed source. Each of its files has a
\* recorded sum (installedSum, its SHA256SUMS line) and actual bytes
\* (installedBytes), which differ when the directory is corrupt and are None
\* when it is partial.
\*
\* Mapped to fleet/tools.yml:
\*   Seed / NoSeed = "the stage seeded from the installed build's directory",
\*                   taken only when the stage is not begun and there is a
\*                   directory to seed from
\*   SendOne       = "send them", one file of the send list
\*   SendDone      = the end of that loop; the run goes on to install
\*   Verify        = `release install` verifying the whole SHA256SUMS before
\*                   its first rename; Refuse is its refusal, which ends the run
\*   InstallOne    = `release install` replacing one binary (a binary already
\*                   holding the release's bytes is skipped)
\*   Crash         = the run halts anywhere; the stage stays as it is and the
\*                   next run starts (bounded by MaxCrashes)
\*
\* The send list (ToSend) is the one thing ByBytes changes:
\*   ByBytes = TRUE  -- the play today: one listing measures the sha256 of
\*                      every file in the stage after the seed, and a file is
\*                      sent when its bytes differ from the release's line
\*   ByBytes = FALSE -- the first cut (c876eb8c6): after a seed, a file was
\*                      sent when its line in the installed build's SHA256SUMS
\*                      differed from the release's, so a seeded file corrupt on
\*                      the machine whose line matched was never sent; without
\*                      a seed every file was compared and sent when it differed

EXTENDS Integers

CONSTANTS ByBytes, MaxCrashes

Files == {"f1", "f2"}
None  == "none"

\* The release's SHA256SUMS: f1 unchanged (reused), f2 rebuilt.
Rel == [f1 |-> "s0", f2 |-> "s1"]

VARIABLES
  installedSum,    \* the installed build's SHA256SUMS lines
  installedBytes,  \* the installed build's directory, the seed's source
  running,         \* the installed binaries
  stage,           \* each file's stage state
  phase,           \* where the run is: start, send, install, done
  seeded,          \* this run seeded the stage
  crashes          \* runs that crashed so far

vars == <<installedSum, installedBytes, running, stage, phase, seeded, crashes>>

StageBytes(f) ==
  CASE stage[f] = "seeded"   -> installedBytes[f]
    [] stage[f] = "sent"     -> Rel[f]
    [] stage[f] = "verified" -> Rel[f]
    [] OTHER                 -> None

AllVerified      == \A f \in Files : stage[f] = "verified"
AllCorrect       == \A f \in Files : StageBytes(f) = Rel[f]
AllInstalled     == \A f \in Files : running[f] = Rel[f]
Begun            == \E f \in Files : stage[f] /= "absent"
InstalledPresent == \E f \in Files : installedBytes[f] /= None

ToSend(f) ==
  IF ByBytes \/ ~seeded
    THEN StageBytes(f) /= Rel[f]
    ELSE Rel[f] /= installedSum[f] /\ stage[f] /= "sent"

\* The installed build: healthy, corrupt (f1's bytes are not its line), or
\* partial (a rebuilt or a reused file missing). Its lines say s0 throughout.
Init ==
  /\ installedSum = [f \in Files |-> "s0"]
  /\ installedBytes \in {[f1 |-> "s0", f2 |-> "s0"], [f1 |-> "s2", f2 |-> "s0"],
                         [f1 |-> "s0", f2 |-> None], [f1 |-> None, f2 |-> "s0"]}
  /\ running = [f \in Files |-> "s0"]
  /\ stage = [f \in Files |-> "absent"]
  /\ phase = "start"
  /\ seeded = FALSE
  /\ crashes = 0

Seed ==
  /\ phase = "start" /\ ~Begun /\ InstalledPresent
  /\ stage' = [f \in Files |-> IF installedBytes[f] /= None THEN "seeded" ELSE "absent"]
  /\ phase' = "send" /\ seeded' = TRUE
  /\ UNCHANGED <<installedSum, installedBytes, running, crashes>>

NoSeed ==
  /\ phase = "start" /\ (Begun \/ ~InstalledPresent)
  /\ phase' = "send" /\ seeded' = FALSE
  /\ UNCHANGED <<installedSum, installedBytes, running, stage, crashes>>

SendOne(f) ==
  /\ phase = "send" /\ ToSend(f)
  /\ stage' = [stage EXCEPT ![f] = "sent"]
  /\ UNCHANGED <<installedSum, installedBytes, running, phase, seeded, crashes>>

SendDone ==
  /\ phase = "send" /\ \A f \in Files : ~ToSend(f)
  /\ phase' = "install"
  /\ UNCHANGED <<installedSum, installedBytes, running, stage, seeded, crashes>>

Verify ==
  /\ phase = "install" /\ AllCorrect /\ ~AllVerified
  /\ stage' = [f \in Files |-> "verified"]
  /\ UNCHANGED <<installedSum, installedBytes, running, phase, seeded, crashes>>

\* The install refuses a set that does not verify: nothing is replaced, the
\* run ends, and the next run begins with the stage as it is.
Refuse ==
  /\ phase = "install" /\ ~AllCorrect
  /\ phase' = "start" /\ seeded' = FALSE
  /\ UNCHANGED <<installedSum, installedBytes, running, stage, crashes>>

InstallOne(f) ==
  /\ phase = "install" /\ AllVerified /\ running[f] /= Rel[f]
  /\ running' = [running EXCEPT ![f] = Rel[f]]
  /\ UNCHANGED <<installedSum, installedBytes, stage, phase, seeded, crashes>>

Finish ==
  /\ phase = "install" /\ AllVerified /\ AllInstalled
  /\ phase' = "done"
  /\ UNCHANGED <<installedSum, installedBytes, running, stage, seeded, crashes>>

Crash ==
  /\ phase \in {"send", "install"} /\ crashes < MaxCrashes
  /\ phase' = "start" /\ seeded' = FALSE /\ crashes' = crashes + 1
  /\ UNCHANGED <<installedSum, installedBytes, running, stage>>

Progress ==
  \/ Seed \/ NoSeed \/ SendDone \/ Verify \/ Refuse \/ Finish
  \/ \E f \in Files : SendOne(f) \/ InstallOne(f)

Next == Progress \/ Crash

Spec == Init /\ [][Next]_vars /\ WF_vars(Progress)

TypeOK ==
  /\ stage \in [Files -> {"absent", "seeded", "sent", "verified"}]
  /\ phase \in {"start", "send", "install", "done"}
  /\ seeded \in BOOLEAN
  /\ crashes \in 0..MaxCrashes

\* 1. No binary is replaced by anything but the release's bytes.
NoWrongBinary == \A f \in Files : running[f] \in {Rel[f], installedSum[f]}

\* 2. When the send step is done, every file in the stage holds the release's
\*    bytes, a reused file included: nothing runs from the stage, and the
\*    install is never asked to refuse (Refuse needs a wrong file at install). Broken by
\*    ByBytes = FALSE (MCBenchStageBrokenLines).
ReusedByteIdentical == phase = "install" => AllCorrect

\* 3. The install verifies only a correct set.
NoVerifiedWithWrong == AllVerified => AllCorrect

\* Liveness: every run that is let finish installs the release.
RerunReachesVerified == <> AllVerified
Liveness == <> AllInstalled

================================================================================
