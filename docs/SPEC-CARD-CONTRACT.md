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
| 1. the frame | the member writes `<slot>.frame.json` from the packet and the brief's header lines: the repository, the base ref, the commit to stage (the base, or for attempt 2 and later the previous attempt's pushed head, `base_head` in the packet, or for a read the head under read), the branch, the attempt, the previous head and the readers' finding, the tier, the model | `TestTheFrameIsThePackets`, `TestALaterAttemptStartsFromThePreviousPushedHead` |
| 2. staging | `native --frame` stages that commit on that branch (never the brief's prose, never a branch name that never reached origin) and writes `JOB.md` into the job directory | `TestStageCardStagesTheFramesCommitOnItsBranch` (functional tier) |
| 3. the profile | the child's model family picks a profile; the profile writes the shims first on the child's `PATH` and the text of `JOB.md` | `internal/cardcontract`: unit tests of the text and the shape, functional tests of every shim verb form |
| 4. the finish | the member reads the result shape, pushes the head, opens the pull request, and judges the finish: ok, failed with its reason, or reaped | `TestJudgeIsTheFinishRule` and the push tests of `internal/member`, the twin tests of `cmd/nova-sprint`, `tla/CardContract.tla` |
| 5. end to end | a scripted child (clone, branch, commit, push, `gh pr create`) runs under the real member and native on the mem twin with a local bare origin, once per profile | `TestTheScriptedChildEndToEnd` (functional tier) |

## 2. The frame and JOB.md

`JOB.md` is the first thing the child reads: the harness prompt begins `Read <job>/JOB.md
first.` and then carries the card. It says, in the profile's words: the repository, the branch
and the commit the checkout is at, the base it came from, that the child works there and
commits as usual, how its commit and its pull request leave (the sprint does both), the test
environment (`GOCACHE=<job>/gocache`, niced, `-count=1 -timeout`), the attempt, and for attempt
2 and later the previous head and the reader's finding. A read's `JOB.md` says to review the
change on the branch against its base as a pull request is reviewed. A frame that carries a
RULES paragraph ends JOB.md with it; the card's own RULES paragraph stays in the brief, where
the add lint holds it.

## 3. The result shape

The child's end is one shape, one `key: value` per line, then free text. `typedrec.ParseCardResult`
is its one reader (the one-typed-parser rule):

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

The rulings of 2026-09-30 on the shape:

- **Every work card ends with a commit.** A child with nothing to do says `verdict: nothing`
  with the reason as its report (claude: `gh pr create --title "nothing: <why>"`); the finish
  is failed with the reason `nothing to do: <why>`, which opens the failed-work judgment for
  the coordinator.
- **The report line is the pull request title**, and the body carries the whole RESULT.md,
  with the gate's output, so the readers see it.

## 4. The finish

A work card's finish is judged in one place, `member.Judge`, cited from the model's `Finish`:

- **ok** only when the result has the shape, its verdict is `ok`, its head has a commit the
  staged commit does not (the child committed), and the member's push of it to the card's
  branch succeeded;
- **failed** otherwise, with the reason: `no RESULT.md shape`, `nothing to do: <why>`,
  `verdict <word>`, `no commit: <why>`, `push refused: <git's line>`; a failed finish passes
  `--failed` and opens the failed-work judgment, never review, and passes `--head` and
  `--branch` only when a push landed;
- **reaped** when the claim moved under the child (a clear, a redeal) or the card left the
  member's queue (a drop, a return): nothing is reported, because the result is nobody's.

The head the member pushes is the result's `head`, else the last head the git shim recorded in
`<job>/.sprint/pushed.tsv`. The member pushes from its own bare repository, fetching every
branch and the `HEAD` of the staged checkout, so a commit on any branch the child made, in the
checkout or in a clone the shim linked to it, is found. Whether the child committed is counted
there, `rev-list <head> ^<staged>`, from the commit native recorded in `<slot>/staged` when it
staged the checkout: never from the checkout's own refs, which a stale bench mirror leaves
behind and the child can edit (`git remote remove`).

**The child's environment is an allowlist.** The member starts each native child with `PATH`,
`HOME` (native's own, which it needs for the bench mirror; native hands the child the slot's
data home), `TMPDIR`, `LANG`, `LC_*`, `TERM`, `USER`, `LOGNAME`, the `GO*`, `NOVA_SWARM_*`,
`NOVA_TEST_*`, `XDG_*` and `OPENCODE_*` families, `GIT_CONFIG_GLOBAL` and `GIT_CONFIG_NOSYSTEM`,
and the secrets `--pass` names (the loop record's nova-secrets keys) with the worker
description's secret; everything else is dropped. A name matching
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
| `claude` | `push` recorded and answered as a push; `clone` of the card's repository (https, ssh, scp form, `.git` or not) becomes a link to the staged checkout, any other clone is refused; `checkout -b`, `switch -c`, `branch`, `fetch` and `pull` pass through | `pr create` writes the result and finishes; `pr review --approve` / `--request-changes` writes a read's verdict; `pr diff`, `pr view` and `pr checks` answer from the staged checkout against the base; everything else is refused, one line, with the reason: the wall holds no forge credential and no network | work as on any pull request: branch, commit, push, `gh pr create`, and nothing else to write; a read reviews with `gh pr review` |
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

The end-to-end test runs once more for the claude child with the wall on
(`TestTheScriptedChildEndToEndInsideTheWall`) on a machine whose PATH holds the wall binary; the
functional image holds none, so there it says so and skips.
