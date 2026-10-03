# The card contract: the frame around a card's task

A card's author writes the task: what to change, the gate, what to report. Everything around
the task is the sprint's: which repository, at which commit, on which branch, where the child
works, how its commit leaves the machine, how its pull request is opened, and how its end is
judged. The sprint writes that frame from the card's data, never from the brief's prose, and
the child meets it through the commands it already knows: `git push` works, `gh pr create`
finishes the card, `gh pr review` finishes a read. The child pushes nothing and opens nothing;
the member does both, outside the wall, with its own credential.

The code is `internal/cardcontract` (the frame, the result shape, the profiles and their shims),
`internal/member` (the finish), `cmd/nova-swarm` (native installs the frame; the member pushes
and opens the pull request). The model is `tla/CardContract.tla`.

## 1. The layers, bottom up

| layer | what it guarantees | checked by |
|---|---|---|
| 1. the frame | the member writes `<slot>.frame.json` from the packet and the brief's header lines: the repository, the base ref, the commit to stage (the base, or for a rework the last pushed head of any earlier attempt, `base_head` and its attempt `base_attempt` in the packet, or for a read the head under read), the branch, the attempt, the head it continues and that head's attempt, why the attempt exists (`why`), the readers' finding and the coordinator's fix, the tier, the model | `TestTheFrameIsThePackets`, `TestALaterAttemptStartsFromTheLastPushedHeadOfAnyEarlierAttempt`, `TestAReworkStagesAtTheLastPushedHeadOfAnyEarlierAttempt` |
| 2. staging | `native --frame` stages that commit on that branch (never the brief's prose, never a branch name that never reached origin) and writes `JOB.md` into the job directory | `TestStageCardStagesTheFramesCommitOnItsBranch` (functional tier) |
| 3. the profile | the child's model family picks a profile; the profile writes the shims first on the child's `PATH` and the text of `JOB.md` | `internal/cardcontract`: unit tests of the text and the shape, functional tests of every shim verb form |
| 4. the finish | the member reads the result shape, pushes the head, opens the pull request, and judges the finish: ok, failed with its reason, or reaped | `TestJudgeIsTheFinishRule`, `TestJudgeNamesTheProviderForARunItFailed` and the push tests of `internal/member`, the twin tests of `cmd/nova-sprint`, `tla/CardContract.tla` |
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
2 and later the attempt it continues and its head (`This checkout continues attempt <n>: its head,
<sha>, is the last pushed by any attempt before this one, and the checkout starts from it.`; left
out when no earlier attempt pushed, and the checkout is the base) and, right after the attempt
line, why the attempt exists. A rework
says three lines, each left out when its value is empty: `This attempt exists because: <how the
attempt before ended>`, `A reader found: <the finding of its broken read>`, `The coordinator
asks: <the --fix text>` (left out too when it is the finding, or already in how the attempt
ended); then `Do that first; a finish with no new commit is refused.` The three are the
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
the card's own RULES paragraph is in the brief, where the add lint holds it, and the child
reads it once.

**Where a rework starts.** `sprint.BaseOf` is the one place that decides it: the packet's `base_head`
is the head of the latest earlier attempt whose finish was ok at a full sha, with `base_attempt` its
number, whatever happened to the attempts after it (an attempt that failed, or ended with no commit,
changes nothing); with no such attempt both are empty and the base is staged
(`tla/CardContract.tla`, `NoPushedWorkUnreachable`, with the reversed witness `previousonly`, a
rework staged from the immediately previous attempt only). `nova-sprint card <id>` prints each
attempt's pushed head (`head=`, `-` when none) and one `NEXT` line: the attempt whose head the next
attempt starts from, or the base (`TestCardShowsTheHeadTheNextAttemptStartsFrom`).

The frame reads the brief's **header only**: line 1 and the `key: value` lines that follow it,
up to the first blank line or line of prose (`swarm.ReadCardBase`). A `base-repo:`, `BASE:` or
clone URL in the body names nothing.

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
  its budget or its deadline ended with no result is failed work, the end said first
  (`budget: no RESULT.md shape`);
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
budget, a lost response, a question), is a provider failure when either holds: the log carries
a provider error written by this run, an `ERROR` line that says a stream error, `server_error`,
a rate limit, an overload or an HTTP 5xx status; or the run exited 0 and the session's
last message is not a final assistant message (the last assistant message finished
`tool-calls`, or none finished). The reason is the cause (`internal/swarm` providercause.go):
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
description's secret; everything else is dropped. Native puts the bench's Go first on the
child's `PATH` after its shell wrappers: the directory the bench's `go` really lives in
(`swarm.BenchGoBin`: the first `go` in `~/sdk/bin`, `~/go/bin`, then the member's own `PATH`,
resolved through its links), so a card's bare `go` and `gofmt` resolve whatever `PATH` the loop
unit started the member with. A name matching
`TOKEN|SECRET|PASSWORD|PASSWD|KEY|CREDENTIAL|AUTH` is dropped unless `--pass` names it, even in
an allowed family. The member keeps the forge credentials for its own push and pull request. A loop record whose harness reads its provider key from the environment carries `--pass <KEY>`; without it the children start with no provider key and fail at the provider, and a member started with no `--pass`, no worker secret and no `--auth` file for a model that is not a local one (`ollama`, `lmstudio`, `llamacpp`, `local`) says so in one `NOTE` line. When the result carries a `title`, the member opens the pull
request after the push, as itself, from the card's branch into the base ref, with the title and
the body, and the finish's report carries its address.

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
recorder every profile may reuse.

A profile is done when it passes the harness every profile passes: `TestEveryProfileKeepsTheContract`
(the shims answer every verb form the profile claims) and `TestTheScriptedChildEndToEnd`, the
scripted child of section 1 layer 5, which runs once per family in `cardcontract.Families`
with `internal/cardcontract/testdata/scripted/<family>.sh` as the harness (`plain.sh` for a
family with none): write the script the family's models follow (how they clone, branch,
commit, push and finish), and the test asserts the member pushed the child's commit to the
card's branch on origin and, when the child ran `gh pr create`, opened the pull request with
its title and body. Run it with `go test -tags functional -run TestTheScriptedChildEndToEnd
./cmd/nova-swarm/`; it needs no store and no network.

The card template (`nova-swarm template --name card`) ends with STEP 6, "End as JOB.md says":
under a profile whose JOB.md ends the card with its pull request, there is nothing else to write;
under one that asks for RESULT.md, the shape above.

`TestTheScriptedChildEndToEndInsideTheWall` runs the claude and openai children again on
Darwin or Linux with the wall binary built by TestMain. It skips when that binary reports
no supported backend; otherwise it asserts the named real backend and the child's job cwd.
