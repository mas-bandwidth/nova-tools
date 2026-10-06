-------------------------------- MODULE FriendStage --------------------------------
\* The daemon stages every job it writes (internal/friend/stage.go, docs/SPEC-FRIEND.md
\* "The daemon stages every job it writes"; card daemon-stages-every-job2, 2026-10-06).
\* For each held work card whose brief is in her inbox, the daemon stages its job: the
\* repository's mirror fetched (cloned the first time), a clone at the base on the card's
\* branch, then JOB.md. No lane is handed the card until JOB.md is there. A repository her
\* account cannot reach, or a base it does not hold, is one judgment to the coordinator,
\* said once while it stands, and the job is staged again after StageRetryEvery.
\*
\* Each card's job is in one of four states:
\*   unstaged  -- the brief may be written; no stage is under way (stageStep may begin one)
\*   staging   -- a stage runs on its goroutine (d.staging[job])
\*   waiting   -- the stage failed; it waits out d.stageRetry[job]
\*   staged    -- JOB.md is there (Staged)
\*
\* Mapped to the code:
\*   WriteBrief   = the inbox write (inbox.go): inbox/<job>/BRIEF.md
\*   Begin        = stageStep's second loop: a brief there, no JOB.md, no stage under way,
\*                  the retry time passed
\*   EndOK        = Stager.Stage succeeds; stageStep clears the judgments of the repo and card
\*   EndUnreach   = Stager.mirror's clone or fetch fails: NotStageable with no card (key repo)
\*   EndNoBase    = Stager.base finds no such ref: NotStageable with the card (key card)
\*   Stop         = the daemon stops while a stage runs (stageResult.stopped): nothing said
\*   Retry        = StageRetryEvery passes
\*   Hand         = nextCard hands the card to a lane, guarded by stageOwed
\*   Grant/Revoke = her account given or losing access to a repository (outside)
\*   PushBase     = the base pushed to the repository (outside, the per-card remedy)
\*
\* Broken names a reversed witness:
\*   "handunstaged" -- nextCard without the stageOwed guard: HandedOnlyStaged fails
\*   "sayeach"      -- a judgment said for every failed stage, not once while it stands:
\*                     OneJudgmentWhileItStands fails
\*   "noretry"      -- a failed job is never staged again: EveryCardIsHanded fails once the
\*                     remedy lands

EXTENDS Integers

CONSTANTS Cards, Repos, RepoOf, MaxOutside, Broken

ASSUME RepoOf \in [Cards -> Repos]
ASSUME Broken \in {"none", "handunstaged", "sayeach", "noretry"}

States == {"unstaged", "staging", "waiting", "staged"}
Keys   == {<<"repo", r>> : r \in Repos} \cup {<<"card", c>> : c \in Cards}

VARIABLES
  brief,    \* inbox/<job>/BRIEF.md is written
  st,       \* each card's job state
  handed,   \* a lane was handed the card
  reach,    \* her account can fetch the repository
  baseOK,   \* the repository holds the card's base
  mirror,   \* mirrors/<owner>/<name>.git is there
  said,     \* d.stageSaid: a judgment stands on the key
  told,     \* ghost: judgments sent on the key since it last cleared
  outside   \* outside events so far (bounded by MaxOutside)

vars == <<brief, st, handed, reach, baseOK, mirror, said, told, outside>>

TypeOK ==
  /\ brief \in [Cards -> BOOLEAN]
  /\ st \in [Cards -> States]
  /\ handed \in [Cards -> BOOLEAN]
  /\ reach \in [Repos -> BOOLEAN]
  /\ baseOK \in [Cards -> BOOLEAN]
  /\ mirror \in [Repos -> BOOLEAN]
  /\ said \in [Keys -> BOOLEAN]
  /\ told \in [Keys -> Nat]
  /\ outside \in 0..MaxOutside

Init ==
  /\ brief = [c \in Cards |-> FALSE]
  /\ st = [c \in Cards |-> "unstaged"]
  /\ handed = [c \in Cards |-> FALSE]
  /\ reach \in [Repos -> BOOLEAN]
  /\ baseOK \in [Cards -> BOOLEAN]
  /\ mirror = [r \in Repos |-> FALSE]
  /\ said = [k \in Keys |-> FALSE]
  /\ told = [k \in Keys |-> 0]
  /\ outside = 0

WriteBrief(c) ==
  /\ ~brief[c]
  /\ brief' = [brief EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<st, handed, reach, baseOK, mirror, said, told, outside>>

Begin(c) ==
  /\ brief[c]
  /\ st[c] = "unstaged"
  /\ st' = [st EXCEPT ![c] = "staging"]
  /\ UNCHANGED <<brief, handed, reach, baseOK, mirror, said, told, outside>>

\* A judgment on k: said once while it stands (Broken "sayeach": every time).
Judge(k) ==
  IF ~said[k] \/ Broken = "sayeach"
    THEN /\ said' = [said EXCEPT ![k] = TRUE]
         /\ told' = [told EXCEPT ![k] = @ + 1]
    ELSE UNCHANGED <<said, told>>

EndOK(c) ==
  LET r == RepoOf[c] IN
  /\ st[c] = "staging"
  /\ reach[r] /\ baseOK[c]
  /\ st' = [st EXCEPT ![c] = "staged"]
  /\ mirror' = [mirror EXCEPT ![r] = TRUE]
  /\ said' = [said EXCEPT ![<<"repo", r>>] = FALSE, ![<<"card", c>>] = FALSE]
  /\ told' = [told EXCEPT ![<<"repo", r>>] = 0, ![<<"card", c>>] = 0]
  /\ UNCHANGED <<brief, handed, reach, baseOK, outside>>

EndUnreach(c) ==
  LET r == RepoOf[c] IN
  /\ st[c] = "staging"
  /\ ~reach[r]
  /\ st' = [st EXCEPT ![c] = "waiting"]
  /\ Judge(<<"repo", r>>)
  /\ UNCHANGED <<brief, handed, reach, baseOK, mirror, outside>>

EndNoBase(c) ==
  LET r == RepoOf[c] IN
  /\ st[c] = "staging"
  /\ reach[r] /\ ~baseOK[c]
  /\ st' = [st EXCEPT ![c] = "waiting"]
  /\ mirror' = [mirror EXCEPT ![r] = TRUE]
  /\ Judge(<<"card", c>>)
  /\ UNCHANGED <<brief, handed, reach, baseOK, outside>>

End(c) == EndOK(c) \/ EndUnreach(c) \/ EndNoBase(c)

Stop(c) ==
  /\ st[c] = "staging"
  /\ outside < MaxOutside
  /\ st' = [st EXCEPT ![c] = "unstaged"]
  /\ outside' = outside + 1
  /\ UNCHANGED <<brief, handed, reach, baseOK, mirror, said, told>>

Retry(c) ==
  /\ st[c] = "waiting"
  /\ Broken # "noretry"
  /\ st' = [st EXCEPT ![c] = "unstaged"]
  /\ UNCHANGED <<brief, handed, reach, baseOK, mirror, said, told, outside>>

Hand(c) ==
  /\ brief[c]
  /\ ~handed[c]
  /\ st[c] = "staged" \/ Broken = "handunstaged"
  /\ handed' = [handed EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<brief, st, reach, baseOK, mirror, said, told, outside>>

Grant(r) ==
  /\ ~reach[r] /\ outside < MaxOutside
  /\ reach' = [reach EXCEPT ![r] = TRUE]
  /\ outside' = outside + 1
  /\ UNCHANGED <<brief, st, handed, baseOK, mirror, said, told>>

Revoke(r) ==
  /\ reach[r] /\ outside < MaxOutside
  /\ reach' = [reach EXCEPT ![r] = FALSE]
  /\ outside' = outside + 1
  /\ UNCHANGED <<brief, st, handed, baseOK, mirror, said, told>>

PushBase(c) ==
  /\ ~baseOK[c] /\ outside < MaxOutside
  /\ baseOK' = [baseOK EXCEPT ![c] = TRUE]
  /\ outside' = outside + 1
  /\ UNCHANGED <<brief, st, handed, reach, mirror, said, told>>

Next ==
  \/ \E c \in Cards : WriteBrief(c) \/ Begin(c) \/ End(c) \/ Stop(c) \/ Retry(c) \/ Hand(c) \/ PushBase(c)
  \/ \E r \in Repos : Grant(r) \/ Revoke(r)

Fairness ==
  \A c \in Cards : WF_vars(WriteBrief(c)) /\ WF_vars(Begin(c)) /\ WF_vars(End(c))
                   /\ WF_vars(Retry(c)) /\ WF_vars(Hand(c))

Spec == Init /\ [][Next]_vars /\ Fairness

\* No lane meets a card whose JOB.md is not there.
HandedOnlyStaged == \A c \in Cards : handed[c] => st[c] = "staged"

\* A staged checkout came from the repository's mirror.
StagedHasMirror == \A c \in Cards : st[c] = "staged" => mirror[RepoOf[c]]

\* One judgment per repository (or card) while it stands, never one per card or per loop.
OneJudgmentWhileItStands == \A k \in Keys : told[k] <= 1

\* A judgment stands only where it was told.
SaidIsTold == \A k \in Keys : said[k] <=> told[k] > 0

\* Once the outside is done and the card can be staged, its lane is handed it.
EveryCardIsHanded ==
  \A c \in Cards : (<>[](reach[RepoOf[c]] /\ baseOK[c])) => <>handed[c]

==================================================================================
