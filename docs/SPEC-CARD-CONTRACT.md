# The card contract: the frame around a card's task

A card's author writes the task: what to change, the gate, what to report. Everything around
the task is the sprint's: which repository, at which commit, on which branch, where the child
works, how its commit leaves the machine, how its pull request is opened, and how its end is
judged. The sprint writes that frame from the card's data, never from the brief's prose, and
the child meets it through the commands it already knows: `git push` works, `gh pr create`
finishes the card, `gh pr review` finishes a read. The child pushes nothing and opens nothing;
the member does both, outside the wall, with its own credential.

The code is `pkg/cardcontract` (the frame, the result shape, the profiles and their shims),
`pkg/member` (the finish), `cmd/nova-swarm` (native installs the frame; the member pushes
and opens the pull request). The model is `tla/CardContract.tla`.

## 1. The layers, bottom up

| layer | what it guarantees | checked by |
|---|---|---|
| 1. the frame | the member writes `<slot>.frame.json` from the packet and the brief's header lines: the repository, the base ref, the commit to stage (the base, or for a rework the last pushed head of any earlier attempt, `base_head` and its attempt `base_attempt` in the packet, which staging carries onto the tip of the base branch, or for a read the head under read), the branch, the attempt, the head it continues and that head's attempt, why the attempt exists (`why`), the readers' finding and the coordinator's fix, the tier, the model | `TestTheFrameIsThePackets`, `TestALaterAttemptStartsFromTheLastPushedHeadOfAnyEarlierAttempt`, `TestAReworkStagesAtTheLastPushedHeadOfAnyEarlierAttempt` |
| 2. staging | `native --frame` stages that commit on that branch (never the brief's prose, never a branch name that never reached origin), a rework at the tip of its base branch with that commit's work carried on top where it applies cleanly, and writes `JOB.md` into the job directory | `TestStageCardStagesTheFramesCommitOnItsBranch`, `TestAReworkIsStagedAtTheTipOfItsBase`, `TestAReworkCarriesThePreviousWorkThatApplies`, `TestAReworkWhoseWorkDoesNotApplyIsTheBareTip` (functional tier) |
| 3. the profile | the child's model family picks a profile; the profile writes the shims first on the child's `PATH` and the text of `JOB.md` | `pkg/cardcontract`: unit tests of the text and the shape, functional tests of every shim verb form |
| 4. the finish | the member reads the result shape, pushes the head, opens the pull request, and judges the finish: ok, failed with its reason, or reaped | `TestJudgeIsTheFinishRule`, `TestJudgeNamesTheProviderForARunItFailed` and the push tests of `pkg/member`, the twin tests of `cmd/nova-sprint`, `tla/CardContract.tla` |
| 5. end to end | a scripted child (clone, branch, commit, push, `gh pr create`) runs under the real member and native on the mem twin with a local bare origin, once per profile | `TestTheScriptedChildEndToEnd` (functional tier) |

`STAGE OK` reports total staging `secs` and cumulative Git command seconds for `clone`
(including its initial checkout), `fetch` (excluding probes and retry waits), and `checkout`;
`FRAME OK secs` reports the whole successful frame installation separately, including recipes,
shims and the read base refresh.

## 2. The frame and JOB.md

`JOB.md` is the first thing the child reads: the harness prompt begins `Read <job>/JOB.md
first.` and then carries the card. It says, in the profile's words: the repository, the branch
and the commit the checkout is at, the base it came from, that the child works there and
commits as usual, how its commit and its pull request leave (the sprint does both), the test
environment (the child's `GOCACHE` is the machine's shared, warm build cache `<root>/cache/go-build`,
already set and named, never a cold one of its own under the job: `go help cache` says "The cache
is safe for concurrent invocations of the go command.", and sixteen reads each compiling the
repository from nothing kept a 36-thread bench 85% in the kernel on 2026-10-02; niced, `-count=1 -timeout`;
`TestJobTextNamesTheSharedBuildCache`), the attempt, and for attempt
2 and later where it was staged: the tip of its base branch and, when an earlier attempt pushed,
whether that attempt's work was carried on top (`This checkout is staged at the tip of <base> as
origin held it when it was staged, <sha>, with the work of attempt <n> (its head <sha>) carried on
top as one commit, <sha>, ...`), the tip already held it, or it did not apply cleanly and is not in
the checkout; a base that never moves (a sha, a tag) keeps the sentence `This checkout continues
attempt <n>: its head, <sha>, is the last pushed by any attempt before this one, and the checkout
starts from it.` (`TestJobTextSaysWhereAReworkWasStaged`) and, right after the attempt
line, why the attempt exists. A rework
says three lines, each left out when its value is empty: `This attempt exists because: <how the
attempt before ended>`, `A reader found: <the finding of its broken read>`, `The coordinator
asks: <the --fix text>` (left out too when it is the finding, or already in how the attempt
ended), and, when the work before did not apply at the tip, `The previous work: the work of
attempt <n> must be redone from this tip: ...`; then `Do that first; a finish with no new commit is
refused.` The three are the
packet's `why`, `finding` and `fix`, which the rework wrote on the attempt's work card
(`TestReworkCarriesTheFixTheFindingAndWhyInTheNextPacket`; the lines:
`TestJobTextOfAReworkSaysWhyAndWhatToDoFirst`). A read's `JOB.md` says to review the
change on the branch against its base as a pull request is reviewed, and states the work's change
exactly: the commit the work started from, a full sha, the two commands that show it
(`git diff <start>..HEAD`, `git diff --stat <start>..HEAD`), and that the base branch may have
moved since and is not what to compare against: a diff against the tip of `<base>` or
`origin/<base>` shows every change landed since as a deletion, never the work's and never a
finding. Its first line is `# JOB: read <card>, attempt <n>` in every profile
(`cardcontract.ReadTitle`) and a work card's never is, so a brief that speaks to its readers
names that line (`TestQuackBriefTellsAReadByJobMdsFirstLine`). The packet carries the base's name, never the
commit the work was staged on, so native finds the start when it stages the read: the merge base
of the read's head and the base in the staged checkout; the gh shim's `pr diff` and `pr view`
read from it too. The base is a full sha, else a branch when the checkout holds
`origin/<base>`, else a tag when it holds `refs/tags/<base>`, else a branch. A sha or a tag never
moves and is used as it is. A branch is fetched from origin into `origin/<base>` first, and a
fetch that fails refuses the read at staging: native prints a STAGE FAIL line naming the base
and the fetch error, writes no JOB.md, and the sprint deals the read again; the checkout's own
branch is never trusted in its place
(`TestAReadWhoseBaseCannotBeFetchedIsRefusedAtStaging`, `TestAReadAgainstATagOrAShaNeedsNoFetch`).
A missing base object or an operational failure while finding the merge base also refuses the
read before writing its frame. Valid unrelated histories have no common ancestor; their frame
names no exact start (`TestReviewAMissingImmutableBaseRefusesTheReadBeforeWritingItsFrame`,
`TestReviewUnrelatedHistoriesKeepTheUnknownStartPolicy`). On the
moving base, cards land every few seconds, and a reader that
ran `git diff origin/dev` saw every file landed since the work began as a deletion and sent a
correct work card back (`TestAReadIsToldTheWorksChangeWhenTheBaseMoved`). The fetch is because
the checkout is cloned from the bench mirror, whose base can be older than the work's start: the
merge base against it was that older tip, and readers judged correct work broken because the diff
held every card landed in between ("diff has 22 files not exactly one"). origin's base holds the
work's start and not the work, so the merge base against it is the start however far the base
has moved (`TestAReadsDiffIsExactlyTheWorkWhereverTheBaseIs`).

**The read's gate.** A read's JOB.md names the gate it runs, as commands, in place of the card's
and of any rule that asks for more (`cardcontract.ReadGate`, `TestAReadIsGatedOnThePackagesItsDiffTouches`,
`TestAReadsGateIsReadOffItsDiff`): `go vet` and `go test` of the packages the work's change
touches (`git diff --name-only <start>..HEAD`: the directory of a changed `.go` file that still
holds one, and the package whose `testdata/` holds a changed file), and of the packages whose
tests read a changed `docs/*.md` (a `_test.go` that names its path in a string, `"docs/<name>.md"` or
`"../../docs/<name>.md"`, or as `filepath.Join`'s `"docs", "<name>.md"`; nova-tools#5111); `go build ./...`
when `go.mod` or `go.sum` changed. The class-test packages the pull request's CI runs whole on
every change (`pkgselect.EveryRun`: `internal/ci`, `internal/docs`) are never run whole by a read:
of them it runs only the Test functions of a test file the change touches or of one that reads a
changed doc, with `-run`. A change that reaches nothing says the read runs no go command. On
a 36-thread bench, 2026-10-02, sixteen reads each ran `go test ./internal/ci/`, whose tests fork thousands
of processes and build every command, and the machine spent 85% of its CPU in the kernel; the
work's own gate ran those tests, and CI runs them again. A checkout with no `go.mod` has no gate
of its own, and its JOB.md says to run the card's. JOB.md repeats no rules:
the RULES paragraph is in the card the child is handed, once. Rules by reference (the owner,
2026-10-02: "Rules by reference: the member injects fleet/child-rules.txt once; the card does not
carry it; a per-repo rules file for second repos."; nova-tools#5174 rule 6): the stored brief is
the card's text alone, and the member, when it writes the card file at the start of a launch,
appends the RULES paragraph of the held rules file the card names (none when it names none:
such a card carries its own; docs/SPEC-SPRINT.md section 2), so what the child
reads is the shape it read when the card carried them
(`TestTheChildsCardIsUnchangedByRulesByReference`).

**Where a rework starts.** `sprint.BaseOf` is the one place that decides it: the packet's `base_head`
is the head of the latest earlier attempt whose finish was ok at a full sha, with `base_attempt` its
number, whatever happened to the attempts after it (an attempt that failed, or ended with no commit,
changes nothing); with no such attempt both are empty and the base is staged
(`tla/CardContract.tla`, `NoPushedWorkUnreachable`, with the reversed witness `previousonly`, a
rework staged from the immediately previous attempt only). `nova-sprint card <id>` prints each
attempt's pushed head (`head=`, `-` when none) and one `NEXT` line: the attempt whose head the next
attempt starts from, or the base (`TestCardShowsTheHeadTheNextAttemptStartsFrom`).

The member stages that work at the tip of the card's base branch (docs/SPEC-SWARM.md, "A rework
is staged at its base branch's tip"; nova-tools#5215): origin's branch is fetched when the rework
is staged, and `base_head`'s work is carried onto its tip as one commit, a squashed three-way merge;
a head that already descends from the tip is staged as it is, work the tip already holds adds
nothing, and work that does not apply cleanly leaves the bare tip, which JOB.md says. The finish's
staged commit is that commit, so a child that starts again from the tip is accepted and a head on
the old base is refused (`TestAReworkStagedAtTheTipFinishesFromItAndNotFromTheOldHead`; end to end, the base moved after
attempt one, `TestAReworkAfterTheBaseMovedIsStagedAtANewCarryOnItsTip`), and the
finish's report begins, after the push, with the stage's words (`stage: staged=<sha> tip=<sha> of
<base> carry=<carried|held|conflict|none>`), so the card's timeline says it. A base that is a full sha
or a tag never moves, and its rework is staged at `base_head` itself. A rework staged at the old
head kept a base hours old, and its child, told to start again from the tip, was refused at its
finish for not descending from the staged commit.

The frame reads the brief's **header only**: line 1 and the `key: value` lines that follow it,
up to the first blank line or line of prose (`swarm.ReadCardBase`). A `base-repo:`, `BASE:` or
clone URL in the body names nothing.

The brief's header grammar also names two reading lines: `START: <files or packages to read
first>` and `STOP: <the condition that ends the task>`. The card lint checks them beside `TEST:`
(`pkg/swarm/lintheader.go`, tokens `start-named` and `stop-named`): line 1's tier is the
condition, read as the sprint writes it (`tier: flash` or `tier: pro`), and a tier flash brief
missing either line is refused, the finding naming the missing line; for tier pro, or no tier,
the same finding is advice, and the verdict stands. The lint decides that by tier before it
consults its advisory set, so a tier flash brief's missing line cannot be downgraded to a note.
A brief with no stopping condition lets a flash child read past its budget, which is what these
two lines bound.

**The head a stage checks out.** The stage checks out the frame's commit only when its tree is
in the stage: else it fetches that head from origin by sha (`git fetch --refetch origin <sha>`:
a plain fetch does nothing for a commit a stage ref already reaches), and once more after a short
delay (`stageFetchRetryDelay`), then checks out. The mirror's own refresh is no
dependency of a stage, and a commit without its tree is no head. A head still absent is refused
in one line naming the sha, the mirror and origin (`TestStageCardFetchesAHeadTheMirrorLacks`,
`TestStageCardFetchesAHeadWhoseCommitTheStageHasWithoutItsTree`,
`TestStageCardFetchesAHeadAStageRefReachesWhoseTreeIsAbsent`,
`TestStageCardRefusesAHeadNeitherTheMirrorNorOriginHolds`, functional tier). A read whose stage
fails (native's `STAGE FAIL` line) is no verdict and no finish: the reader's member runs it again
once (`TestAReadWhoseStageFailedOnceIsRunAgainThenReads`), and a second failure is returned with
the stage's reason (`TestAReadWhoseStageFailedTwiceIsReturned`); a work card's stage failure ends
as any failed launch.

**Staged recipes.** A header line `Stage: <path> [<path>...]` names files the member stages into
`<job>/recipes/<path>` before the child starts, from its own `<root>/recipes/<path>`: a recipe a
card works from (a pull request body to rewrite, a long table) can be larger than the brief's
16 KiB, and the wall gives the child no forge to fetch it from. A path that is not a relative
path inside the recipes directory, a link, or a missing file refuses the launch, which the
finish reports failed; JOB.md lists what was staged. Each source opens through an `os.Root` of the
recipes directory, so no path component leaves it, a symlinked directory included; the refusal
names the `Stage:` path and the reason.

**The WHO line** (the owner, 2026-10-03: "Could we try expressing the work left for
nova-tools-1.1.0 into cards, and doing it via the sprint, but doing parts on friends where we
would normally do friend work."; 2026-10-04: pins only by choice). `WHO: friend` prefers any
friend whose class covers the tier. `WHO: friend <name>` prefers that friend while she is up
with room, then another covering friend, then the fleet. `WHO: only friend <name>` waits for
that friend alone. A card with no WHO line, or `WHO: -`, is offered to covering friends and
then framed for the fleet as this page says. A card placed on a friend is never framed or
staged: the sprint delivers it to her inbox as `inbox/<card>/BRIEF.md` and finishes it from
her `outbox/<card>/REPORT.md` (docs/SPEC-SPRINT.md section 1, a friend's card; docs/FRIENDS.md,
a sprint card). `cardhdr.ReadWho` is the one parser: the key in any case, under line 1 and
above the first blank line; `nova-sprint add` and `brief` refuse any other value, and a name
the friends table lacks (`TestReadWhoReadsAFriendOrNone`, `TestAddHoldsTheWhoLineToTheFriendsTable`).

## 3. The result shape

The child's end is one shape, one `key: value` per line, then free text. `typedrec.ParseCardResult`
is its one reader (the one-typed-parser rule). Native counts the gh shim's record as the card's
published result (`swarm.FindCardResult`), so a child whose `gh pr create` or `gh pr review` is
its whole end earns `NATIVE OK`, and a reader's review alone reaches the sprint as its verdict:

```
head: <the commit, full sha>
branch: <the branch it is on>
verdict: ok | not-done | nothing   (a read: ok | broken)
gate: <the gate command, or ->
output: <the path of the gate's output, or ->
report: <one line>
title: <a pull request title>   (optional)

## Body

<the pull request body, or a review's findings with file:line>
```

In the claude and openai profiles `gh pr create` and `gh pr review` record it in `<job>/.sprint/finish.md`,
a file the child is never told to write, so a RESULT.md the child also writes (as an older card
template asked) overwrites nothing: it rides at the end of the pull request body. A plain child
writes `<job>/RESULT.md` itself. The finish record wins over RESULT.md. A result without the six
keys is no result.

**The verdict per step.** A tree card's result (docs/SPEC-SPRINT.md, a card is a tree of steps)
also carries one line per work step in its body (under `## Body`, never among the header's
keys), in walk order: `step <n>: <ok|broken|not-done|skipped> <commit sha|-> <one line>`, the
commit a full sha or its first twelve; a line whose commit is any other word is a defect, read as
not-done (`TestAStepLineWhoseCommitIsAWordIsADefect`). `cardtree.ParseVerdicts` reads the body's
lines, and the member's finish of a tree card is judged from them (`member.treeFinish`,
`TestAFailedStepTwoOfThreeLandsStepOneAndWritesTheRemainder`). A script card's body is written by
`nova-swarm step --result`, one line per step it ran.

The rules on the shape:

- **Every work card ends with a commit.** A child with nothing to do says `verdict: nothing`
  with the reason as its report (claude: `gh pr create --title "nothing: <why>"`); the finish
  is failed with the reason `nothing to do: <why>`, which opens the failed-work judgment for
  the coordinator.
- **The report line is the pull request title**, and the body carries the whole RESULT.md,
  with the gate's output, so the readers see it.

**A broken read names its defect.** A read's `verdict: broken` tells the work what to do: at
least one line of its report or body names the file (a path, or `file:line`), the line
(`line <n>`), or the card's `STEP <n>` or RULE the work breaks, and says what to change
(`typedrec.FindingPattern`, `typedrec.NamesADefect`; `TestABrokenFindingNamesAFileALineOrARule`).
"Request changes." alone, or an approval's words under a broken verdict, is no finding. Every
profile's read JOB.md says so (`cardcontract.BrokenFindingText`); the claude and openai gh shims
refuse a `gh pr review --request-changes` whose body names none, in one line, so the reader names
it before it ends (`TestGhPrReviewIsTheRead`, functional tier). The member reports a broken read's
finding in full, every line of its report and body joined with ` / ` and cut to
`member.MaxFindingBytes`, never its first line alone; a broken verdict whose finding names no
defect is no verdict: the member hands the read back (`read --return`, its reason beginning
`no finding:`), and the sprint asks another reader as for any return
(`TestABrokenReadNamesItsDefectOrIsHandedBack`). The sprint's own `read --broken` refuses a
finding naming none, so the rule is one predicate consulted at the review (the shim), at the
hand-back (the member) and at the record (the store); refused, the read stays the reader's to
report or hand back (`TestAuthoritativeBrokenReadFindingBoundary`). A file is any name with an
extension, `a.go` too; `e.g.` and `i.e.` are not, since a dot follows.

### script-cards-self-verify.w1: a script read's finding

A read of a script card that a script reader found identical to its program's output reports `verdict: ok`
with a `report:` line beginning `script read: ` (what was run, at which start commit, against which
head, and the bytes compared). A script reader that found a difference reports nothing of its own: it
reads the card as a model reader and gives that read's verdict (docs/SPEC-SPRINT.md section 6, the script read).

A read card dealt to a friend (docs/SPEC-SPRINT.md section 6, "A read is a consumer card")
is a card of hers: its BRIEF.md (sprint `ReadCardBrief`) says what a read is, the head to check
out, how to read it and how to finish, and its end is outbox/<job>/REPORT.md whose first line is
`Verdict: LAND` (an ok read) or `Verdict: HOLD` (a broken read, each defect on a line naming the
file, line or rule it breaks); a read commits nothing and pushes nothing. A read card dealt to a
member is run as a reader's read is, its verdict reported with `read --as <member>`.

## 4. The finish

A work card's finish is judged in one place, `member.Judge`, cited from the model's `Finish`:

- **ok** only when the result has the shape, its verdict is `ok`, its head has a commit the
  staged commit does not (the child committed), and the member's push of it to the card's
  branch succeeded;
- **no result**, a failed finish of its own kind, when the child ended by itself, within its
  budget and its deadline, having written no result at all and its push was not refused; its
  reason is `no result: no RESULT.md shape`, and the sprint treats it as an ended take
  (docs/SPEC-SPRINT.md, the work card's redeals), never as the card's failure: no work came
  back, so there is nothing to judge (the owner, 2026-10-01: "that's fine with me."). A run
  its budget or its deadline ended with no result is failed work, the end said first, and
  for a budget which budget and at what count, from native's `NATIVE BUDGET` line, the cost
  the harness reported to the cent and rounded up
  (`budget: tokens 509,940 of 400,000, $0.03: no RESULT.md shape`);
- **failed** otherwise, with the reason: `<end>: no RESULT.md shape`, `nothing to do: <why>`
  (`cardhdr.EndNothing`),
  `verdict <word>`, `no commit: <why>` (`cardhdr.EndNoCommit`), `push refused: <git's line>`; a failed finish passes
  `--failed` and opens the failed-work judgment, never review, and passes `--head` and
  `--branch` only when a push landed;
- **provider failure**, a failed finish of its own kind, when the run ended with no result and
  the harness's own record says the provider failed it (below), and its push was not
  refused; its reason is `provider failure: provider: class=<class> status=<status|-> msg=<words>`
  (the cause, below; the 5xx hand-back's too), and the sprint treats it as an ended
  take (docs/SPEC-SPRINT.md, the work card's redeals), never as the card's failure. A refused
  push, and a result with the shape (nothing to do, not done) whatever the run's end, are
  failed work with their own reasons and no provider kind;
- **staging refused**, a failed finish of its own kind, when native refused the launch at
  staging before any child ran (its `STAGE FAIL ... base=<the card's base> reason=<why>` line:
  no bench mirror, the pushed head missing, the stage's timeout): the member reads nothing else
  of the launch, its reason is `staging refused: <why>`, and the sprint deals the card to
  another member, the member's failure and never the card's (docs/SPEC-SPRINT.md, the work
  card's redeals; `tla/CardContract.tla`, `StageRefused`, its invariant
  `StagingIsNotFailedWork` with a reversed witness);
- **reaped** when the claim moved under the child (a clear, a redeal) or the card left the
  member's queue (a drop, a return): nothing is reported, because the result is nobody's. A
  moved claim is reaped whatever column the queue lists the card in: a redeal, and a withdrawn
  card dealt again, list it in the member's own ready (a read: asked) column at a later
  generation (attempt), and the ended launch is reaped within one tick and its place of the
  width taken again.

The child has ended when native's process has. Native's output goes to a file in the slot,
never a pipe back to the member, so what a harness left running cannot hold the finish; and
native, which runs the harness as the leader of its own process group, ends what the harness
left in that group before it exits and names it on its line (docs/SPEC-SWARM.md, `native`).

**A provider failure is not the card's** (`tla/CardContract.tla`, `ProviderFailure`; its
invariants `ProviderIsNotFailedWork`, `RedealBound`, `BoundJudgedOnce` and
`RedealAvoidsFailedRoute`, each with a reversed witness). After a run, native reads the
harness's own record in the job's data home, at the places the harness profile names: its log
(`opencode/log/opencode.log`), from where it stood when the run began and bounded to its last
64 KiB, and its session database (`opencode/opencode.db`, the `message` table). A run that ended
with no result, by no end of the machinery's (the deadline, a TERM, the watch, the wall, the
budget, a lost response, a question), is a provider failure when one holds: the session's
record of a failed message names the provider's refusal for credit (class `out-of-credit`: a
402, insufficient credit or funds, a payment required), whatever the run's exit and wall; the
harness's printed output (what the run appended to `<job>/harness-output.log`, where the harness
prints its `ERROR` lines), or else its log, carries a provider error written by this run, an
`ERROR` line that says a stream error, `server_error`, a rate limit, an overload, an account out
of credit or quota (`Insufficient credits`, `Insufficient account funds`, `out of credit`, a
quota, a payment required) or an HTTP 402, 429 or 5xx status; or the run exited 0 and the
session's last message is not a final assistant message (the last assistant message finished
`tool-calls`, or none finished). A session error of any other class is not a provider failure
by itself; when one of the others holds, it names the cause. The refusal for credit is read
first (nova-tools#5199; the owner, 2026-10-03: "provider out of funds should never be a mystery
failure."): a non-retryable 402 at launch makes the harness exit 1 within two seconds, before
its log holds a line, so the 402 is in the printed output and the session alone
(`TestANonRetryableProviderRefusalAtLaunchIsAProviderFailure`,
`TestASessionErrorThatIsNotARefusalForCreditIsNotAProviderFailureByItself`). The reason is the cause (`pkg/swarm` providercause.go):
`provider: class=<class> status=<status|-> msg=<words>`. The class is one of `unknown-model`
(the provider does not know or serve the model id: a config error), `auth`, `out-of-credit`,
`rate-limited`, `provider-5xx`, `timeout` and `other` (none of these, or the harness recorded
no cause, as its own `UnknownError` does); the status is the provider's HTTP status, `-` when
the record names none; the words are the provider's own message, one line, everything
after a `key:` or `key ` dropped and every other secret-shaped value removed, cut to 120 bytes with the cut said. They come from the session's
record of the failed message when it has one (an API error keeps the provider's status and
body there), else the log's error line, else (a run that ended on a tool) `ended without a
final message`, class `other`. Native prints it as `NATIVE PROVIDER-FAIL label=<l> wall=<s>s
route=<model> reason=<cause>`, and the 5xx hand-back ends its own line with the same
`reason=<cause>`, read from the session and the log first and from the harness's own last
words else. The member reads either line's reason (`member.Result`'s `Provider`) and judges the
finish (`TestCauseFromTheHarnessLogAndOutput`, `TestCauseFromTheSessionsRecord`,
`TestTheSessionsRecordOfTheFailedMessageIsTheCause`). The class is a record: it changes no
judgment. A run with no result and no provider error that ends with a final assistant message
stays `no-result`.

The head the member pushes is the result's `head`, else the last head the git shim recorded in
`<job>/.sprint/pushed.tsv`. The member pushes from its own bare repository, fetching every
branch and the `HEAD` of the staged checkout, after dropping any ref this launch's namespace kept
from an interrupted push, so a commit on any branch the child made, in the
checkout or in a clone the shim linked to it, is found. The head must be on one of those
fetched refs (the push repository keeps every launch's objects, so a commit being there is no
evidence it is this launch's) and must descend from the staged commit, else the push is
refused. A result head on none of those refs is replaced by the checkout's own head when the
child made exactly one line of work: exactly one fetched tip descends from the staged commit
with a commit of its own; it is pushed, never forced, and the member's output says
`NOTE push <card> head: the result named <claimed>, which is no commit of the checkout; the
checkout's own head <sha> was pushed`. With no such tip, or more than one, the refusal stands
(the failure it refuses is a model
writing a sha whose first characters were right and whose tail was invented, from one route,
the commit on the checkout's branch: `TestAWrongTailHeadPushesTheCheckoutsOwnCommit`,
`TestAnAbsentHeadWithTwoCandidateBranchesIsStillRefused`, `TestARightHeadIsPushedAsBefore`).
Whether the child committed is counted
there, `rev-list <head> ^<staged>`, from the commit native recorded in `<slot>/staged` when it
staged the checkout: never from the checkout's own refs, which a stale bench mirror leaves
behind and the child can edit (`git remote remove`).

**The child's environment is an allowlist.** The member starts each native child with `PATH`,
`HOME` (native's own, which it needs for the bench mirror; native hands the child the slot's
data home), `TMPDIR`, `LANG`, `LC_*`, `TERM`, `USER`, `LOGNAME`, the `GO*`, `NOVA_SWARM_*`,
`NOVA_TEST_*`, `XDG_*` and `OPENCODE_*` families, `GIT_CONFIG_GLOBAL` and `GIT_CONFIG_NOSYSTEM`,
and the secrets `--pass` names (the loop record's nova-secrets keys) with the worker
description's secret; everything else is dropped. Native puts the bench's toolchain first on
the child's `PATH` after its shell wrappers (`swarm.BenchPath`): the `bin` of every home root
the wall executes, read off the wall's own root list (`~/sdk/bin`, the standard's links to
every sdk tool), then the directory the bench's `go` really lives in (`swarm.BenchGoBin`: the
first `go` in those, `~/go/bin`, then the member's own `PATH`, resolved through its links), so
a card's bare `go`, `gofmt`, `dotnet` or `cargo` resolves to the toolchain the wall grants
whatever `PATH` the loop unit started the member with, never to a stale copy on the member's
own. Under the darwin wall, which denies `setpriority`, native
starts the child's process group at nice 19 and the wrappers' directory carries a `nice` that
runs its command without asking for a priority the group already has, so a gate's
`nice -n 19` prints no warning. A name matching
`TOKEN|SECRET|PASSWORD|PASSWD|KEY|CREDENTIAL|AUTH` is dropped unless `--pass` names it, even in
an allowed family. The member keeps the forge credentials for its own push and pull request. A loop record whose harness reads its provider key from the environment carries `--pass <KEY>`; without it the children start with no provider key and fail at the provider, and a member started with no `--pass`, no worker secret and no `--auth` file for a model that is not a local one (`ollama`, `lmstudio`, `llamacpp`, `local`) says so in one `NOTE` line. When the result carries a `title`, the member opens the pull
request after the push, as itself, from the card's branch into the base ref, with the title and
the body, and the finish's report carries its address.

### recut-widen-r.w1: a HOLD proposes the PATHS it lacked

A child that holds because its card's PATHS are too narrow says which globs it needs on one
line of its report or result body: `PATHS-PROPOSED: <glob>[,<glob>...]`, each a path or a glob
relative to the repository root, never climbing out with `..`, each naming a file at the card's
base or one its pushed head creates. A path may be followed by the writer's reason: the reader
takes each comma-separated item's path up to its first whitespace, dash or semicolon, reads the
rest as prose, and refuses an item with no path before its prose, printing that item. The child
still pushes what it did and reports its head.
The member keeps that line at the end of the finish's report, inside the 500-byte cut
(`member.CarryProposed`), and friend sync keeps it on a friend's HOLD, with the HOLD's `Head`
when it is origin's tip of the card's branch; so the line is on the card, and `nova-sprint brief
<id> --widen` reads it there, each item's path read before its prose, and widens the card
in place, the same id (docs/SPEC-SPRINT.md section 2). The widened brief carries a header line
`CARRY: <id> attempt <n> head=<sha>`, and the member stages the card's next attempt at that
head as it stages a rework at its last pushed head (`member.Carried`), as does a friend's brief
(`TestBriefWidenKeepsTheId`).

### What admission verifies

`nova-sprint add`, `brief` and `recut` check a brief that names `PATHS:`, `REPO:` and `BASE:`
against the BASE tip of that repository before the brief is admitted
(`sprint.PathsAdmission`, `cmd/nova-sprint/add.go`). The tree is the lander's clone, fetched
shallow into `refs/nova-add/<base>` (or the commit `BASE:` pins), cached per tip for the call.
A literal PATHS entry must be a file or a directory there. A glob must match at least one file.
A new `*_test` file, and an entry a `NEW:` line names, may be absent. A brief whose header
carries `CARRY: <id> attempt <n> head=<sha>` skips that existence check (a widen's new path
may exist only at the carried head) and still checks identifiers. Every `func`, `type` or
`verb` that `STOP:` or `START:` names in the same clause as a repository path, and a `TEST:`
name the tree already holds, must occur inside a file PATHS covers, found by a plain grep of
the tree. Markdown code-span delimiters are not part of the name or the path
(`func` then a backticked `cmdBrief`, or a backticked `sprint.cmdBrief`, is `cmdBrief`).
A qualified name is its last component (`type sprint.AdmissionTree` is `AdmissionTree`),
not the package, which may sit in a PATHS file while the name does not. A dotted file
name (`file.go`) is not a qualified name. A `TEST:` name the tree does not hold is the
new red test and is not a miss. A brief that fails is refused with one line per miss, and
the line names the nearest file that holds the identifier, so the author fixes PATHS in
one edit. A tip that cannot be read is one `MISSING` line, not a pass. A named `REPO:`
that no clone can be made of is that same `MISSING` line, not a pass. A brief with no
PATHS, or that omits REPO, or that omits BASE, is not read against a tree.

A worker HOLD after admission whose words are `PATHS do not hold` is a brief defect at that
first finish, not at the fourth attempt (`sprint.BriefDefectOf`). The defect carries the
worker's proposed PATHS, from `PATHS-PROPOSED:` or a `PATHS:` line of the report, as the
one-line fix. A report that only proposes PATHS, without those words, stays the worker's
failed work.

## 5. Profiles

A profile is keyed by model family, derived from the model id the member runs the child on:
`claude`, `openai`, `gemini`, `grok`, `deepseek`, and `plain`, the fallback for any other.
`claude`, `openai` and `plain` are built; `gemini`, `grok` and `deepseek` are named and serve
`plain` until one is written.

| profile | git | gh | JOB.md asks for |
|---|---|---|---|
| `plain` | `push` recorded and answered as a push; everything else passes through | refused, one line | commit on the branch, write RESULT.md in the shape |
| `claude` | `push` recorded and answered as a push (a delete, prune, mirror, `--all`, `--branches`, `--tags` or push option is refused, one line naming the flag as typed, and recorded as nothing, a long flag at every prefix of those names too, since git accepts an unambiguous prefix (an ambiguous prefix is refused as well: git rejects it anyway); an option's value is never read as an operand); `clone` of the card's repository (https, ssh, scp form, the scheme's default port, `.git` or not) becomes a link to the staged checkout, any other clone is refused; `checkout -b`, `switch -c`, `branch`, `fetch` and `pull` pass through | `pr create` writes a work card's result and finishes; `pr review --approve` / `--request-changes` writes a read's verdict (each refused on the other kind); `pr diff`, `pr view` and `pr checks` answer from the staged checkout against the base; everything else is refused, one line, with the reason: the wall holds no forge credential and no network | work as on any pull request: branch, commit, push, `gh pr create`, and nothing else to write; a read reviews with `gh pr review` |
| `openai` | `-C`, linked worktree, branch and commit use real Git; `push` and `push origin [HEAD\|branch]` record a source commit, with optional `-u`; deletion, force and other push options are refused | `pr create --title ... --body-file ...` records a work finish from the current checkout or linked worktree; `pr review --approve\|--request-changes --body-file ...` records a read verdict; `pr diff [--name-only]`, `pr view` and `pr checks` read the staged checkout; target-changing, draft and unknown forms are refused | use the staged checkout or its linked worktree, commit and record a push, then create a pull request from the commit-owning directory; a read records a review with a body file |

### Writing a profile

A profile is a value of `cardcontract.Profile`:

```go
type Profile interface {
	Family() string                  // the family it serves
	JobText(f Frame, s Staged) string // the whole of JOB.md
	Shims(f Frame, s Staged) []Shim   // the scripts first on the child's PATH, by command name
}
```

It is registered in `profiles` by its family. Its shims are POSIX `sh` scripts written into
`<slot>/shim`, which the wall lets the child run and not rewrite. Whatever its commands look
like, a profile keeps the contract: a push leaves nothing but a line in `<job>/.sprint/pushed.tsv`
(`branch`, `head`, `top`, tab separated); the child's end has the shape above, recorded by a
command profile in `<job>/.sprint/finish.md` or written explicitly in `<job>/RESULT.md` as a
fallback. Nothing reaches a forge from inside the wall. `cardcontract.ContractShim` is the push
recorder every profile may reuse. A push whose remote is a local path (an absolute path, a
`./` or `../` path) or a `file://` URL is handed to the real git unchanged, as typed, and
records nothing: it lands on the child's own machine and reaches no forge, and a test or tool
inside a card that pushes to a bare repository it made under its own temp directory needs the
push to land. So is a push run in any repository but the staged checkout (one whose git common
directory is not the checkout's; a linked worktree and the clone link share it): a gate's tests
push to an `origin` of their own temp repositories, and a recorded test commit would be taken
for the child's head by a finish that names none (`LastPushed`).

A profile is done when it passes the harness every profile passes: `TestEveryProfileKeepsTheContract`
(the shims answer every verb form the profile claims) and `TestTheScriptedChildEndToEnd`, the
scripted child of section 1 layer 5, which runs once per family in `cardcontract.Families`
with the Go test binary `pkg/cardcontract/testdata/scripted/child` placed as `<family>` as the
harness (`plain` for a family with none): add the steps the family's models follow (how they clone, branch,
commit, push and finish), and the test asserts the member pushed the child's commit to the
card's branch on origin and, when the child ran `gh pr create`, opened the pull request with
its title and body. Run it with `go test -tags functional -run TestTheScriptedChildEndToEnd
./cmd/nova-swarm/`; it needs no store and no network.

A friend's card is staged by her own daemon, not by native: for each held work card whose
brief it writes, `nova-friend run` clones the card's `REPO` at its `BASE` on the card's branch
into `jobs/<job>/repo`, from a mirror it keeps per repository, and writes `jobs/<job>/JOB.md` in
this shape (`friend.JobText`: the checkout, the branch and its push, the outbox report and the
finish); a repository her account cannot reach is a judgment to the coordinator
(docs/SPEC-FRIEND.md, "The daemon stages every job it writes").

The card template (`nova-swarm template --name card`) ends with STEP 6, "End as JOB.md says":
under a profile whose JOB.md ends the card with its pull request, there is nothing else to write;
under one that asks for RESULT.md, the shape above.

`TestTheScriptedChildEndToEndInsideTheWall` runs the claude and openai children again on
Darwin or Linux with the wall binary built by TestMain. It skips when that binary reports
no supported backend; otherwise it asserts the named real backend and the child's job cwd.

## 6. Generated cards

A card is written by a program from the source the work comes from whenever the
source is structured: a ratchet ledger of `internal/ci`, a reader's findings
file, a tool's rendered help. `nova-card generate` is that program
(`cmd/nova-card`; the planner is `internal/cardgen`, pure functions over text
with no clock and no store). What it holds:

- One card per file the source names, its id `<prefix>-<slug of the file>`,
  its `RESULT:` line carrying the tier (`tier: flash|pro`), `REPO:` and `BASE:`
  from the checkout the source was read from, `KIND:` (`ledger` for a card cut
  from a ledger, `fix-red` for every other), `DEPENDS-ON:`,
  `PATHS:` (at most eight entries, files folded into their directory's glob
  past that), `NEW:` for a test file the card creates in a package that has
  none, `TEST:`, `STOP:` and the `Deadline:` line; the RULES paragraph of the general
  child rules (`swarm.ChildRulesParagraph`, the paragraph the card template
  carries); the `ATTRIBUTION:` line (`cardgen.Attribution`); the task from the
  source's template with the rows substituted; the template's steps; and the
  `AS A READ` section (`cardgen.AsARead`), the text a reader of the work is given.
- No brief names its author. A commit names the worker who did the work, and the
  deal may hand any card, a pinned one too, to any worker, a friend or a fleet
  machine, so the brief cannot know who that is: its `ATTRIBUTION:` line reads
  `By: your own name, the worker who does this attempt`, says a model name is
  never a `By:`, and that a Claude worker adds its true `Co-Authored-By` trailer
  while any other worker adds none; a `WHO:` line stays a preference for who is
  dealt the card, never the name to sign. Neither the line nor the held rules
  (`fleet/child-rules.txt`, rule `commit-trailer`) spell a fill-in Claude trailer,
  which a worker of another model completes with its own model's name. Its
  `AS A READ` section says a `By:` trailer is judged only for being present and
  true (the worker who pushed the branch under read), and attribution alone never
  decides a verdict. It also says the scope of the change is its `PATHS` line and
  carries `cardgen.AlwaysInPathsRule`, so a reader holds a test or a ledger the
  line does not name inside the change. Briefs stamped `By: <friend>` from a WHO pin sent readers to
  fail landed-quality heads for that line alone, because another worker had done
  the work, and landed heads signed with the name of a friend who never ran them.
- A card's PATHS are computed from the START line its brief carries, never
  typed (`card.Paths`, over `card.PackagePaths`): every directory a START file
  lives in, as its Go files and its tests (`<dir>/*.go`, `<dir>/*_test.go`),
  and the docs the card names, as themselves. START is the card's file and the
  package of its test, so a ledger card's PATHS are the row's file's package,
  the class test's package and the ledger; a findings card's, the file's
  package and its test's package; a help card's, `cmd/<tool>` and
  `docs/CLI.md`. A package is the unit a change lives in: a typed file list
  one file short holds the card at land (E12). Files a change must touch to
  keep the tree green are always inside PATHS, whatever the brief names: every
  `*_test.go`, every file under a `testdata/` directory, `tla/RUNS.tsv` and
  `tla/CASES.tsv`, `internal/docs/catalog.go`, and every `AGENTS.md` map; any
  other file outside PATHS is still out of scope (`cardgen.AlwaysInPathsRule`;
  SPEC-SPRINT.md section 7, always inside PATHS): the lander's E12
  (`sprint.LandScope`) and the readers (`sprint.FilesOutsidePaths`) hold every
  card to that sentence, and every generated brief carries it in its AS A READ
  section. Two cards that share an entry and neither needs the other set
  `shared-paths=yes`. With a checkout, every entry is checked to exist in it;
  a package's `*.go` is not answered by a test file the card creates.
- A ledger card is its own kind of card, `KIND: ledger`: its `TEST:` is the
  ledger's class test under `internal/ci`, green at the base by construction,
  and its proof is the ledger shrinking with that test still green, so its
  `STOP:` line reads `the ledger rows for <file> in <ledger> shrink from N to 0
  and the class test <Test> stays green` (for a counted row, dead code's
  `pkg N`, `the count on the ledger row ... shrinks from N to 0`). Every other
  card's `STOP:` is its test red before the change and green after it. `nova-sprint
  add`'s `donewhen-test-name` lets a test that exists at the base pass for a
  ledger card, and only for one whose brief says `KIND: ledger` and whose
  `TEST:` package is under `internal/ci/` (`swarm.LedgerKind`).
- Waves: a ledger plan is one wave with no dependency chain. The lander resolves
  a ledger conflict as the union of removals, so adjacent deletions of one file
  no longer conflict at land; every card's `DEPENDS-ON:` is `-`, and every card
  shares the ledger's path with no need between them, so the CARDS line says
  `shared-paths=yes`.
- Every brief is held to the lint `nova-sprint add` runs (the model lines, the
  child rules under the default rule set, a tree card's steps), and past the add
  to the typed header and the template's placeholders, which the add does not
  read, before the directory is written; one red brief and nothing is written.
  A sprint initialised with `--rules` holds a brief to that file at the add.
  Every brief is held as well to the card checks (`card.Checks`), which
  `nova-sprint add` runs too: a card brief names its tier on line 1
  (`tier-line`) and a TEST whose package is a directory its PATHS names
  (`test-outside-paths`); no brief carries a name `--name` gives outside
  double-quoted words (the owner's, quoted), its own id or its `WHO:` line
  (`personal-name`), nor the id of a card `--dropped` gives (`dropped-card`),
  nor `By:` followed by such a name, quoted or not, `friend.<name>` included
  (`author-name`; `Co-Authored-By:` is no `By:`;
  `TestABriefNamesNoFriendAsAuthor`).
  The tree holds no name of the deployment (internal/ci TestGeneralityText),
  so the names come from the caller: `--name` to nova-card, the store's
  coordinator, owner and friends table to the add.
- A card whose PATHS reach a model or a configuration under `tla/` runs the
  model in its own gate: its STEP 4 adds, on a Linux TLC bench, `make tlc` for
  each group `tlacheck groups --stale` lists (the groups of the cases the edit
  touched) and `tlacheck merge --keep tla/RUNS.tsv` of what they wrote, and its
  PATHS line carries `tla/RUNS.tsv` so the record is committed with the change
  (`cardgen.modelGate`). The lander refuses a head that edits `tla/*.tla` or
  `tla/*.cfg` without a current record for each case it touches, naming the case
  (SPEC-SPRINT.md section 7, the run records).
  The output is the directory, its `manifest.tsv` (id, file, test, wave, deps)
  and one `CARDS OK dir= cards= waves= tier=` line.

`nova-swarm lint --card` is the fuller contract lint and is run over a
generated directory by the coordinator before the add; its `no-sandbox` check
matches the word `nova-sandbox` on any line, so a card whose PATHS name
`cmd/nova-sandbox/` draws it though the add admits the card.

### lint-allows-quoted-patterns-in-tests: the PATTERNS TO REFUSE paragraph

A class test that refuses a dangerous command must name it, and the step scans refuse a card
for naming it. One narrow exemption: a card whose `TEST:` or `PATHS:` line names a class test
under `internal/ci` (`internal/ci/<name>_class_test.go`) may carry one paragraph that begins
`PATTERNS TO REFUSE.` and runs to the first blank line. A backtick-quoted literal in that
paragraph is a pattern, not a command, and no `step-` scan fires on it. Everything else is
scanned as before:

- every other line of the card, and every unquoted command inside the paragraph;
- the paragraph in a card that names no class test is itself the finding
  `patterns-block-without-class-test`, at the paragraph's first line.

`nova-sprint add` runs the same lint, so the exemption holds there. Pinned by
`TestPatternsToRefuseBlockIsExemptForAClassTestCard` (`pkg/swarm`).
