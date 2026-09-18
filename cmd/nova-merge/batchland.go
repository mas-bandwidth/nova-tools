package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE SEVEN STEPS AFTER BATCH OK, AS A VERB.
//
// Rowan ran this procedure EIGHT times on 2026-09-18, by hand or by a child, and it was the
// same procedure every time: push the branch, open (or update) the batch's pull request,
// wait for its ci-ok, rerun the two known flakes when they are what went red, enqueue at
// the front through the one door, watch the queue until the merge commit is on dev, and
// close every member with a pointer to the batch that carried it. Eight repetitions of one
// sequence is a verb nobody has written yet -- Glenn, 2026-09-17: "everything sketched
// becomes a tool".
//
// It is behind a FLAG on `batch` rather than a verb of its own because it is the same run:
// the evidence it hands the one door is the BATCH OK line the gate printed a second ago,
// over the tree it has in its hand. A second verb would have to be handed that line, that
// branch and that head, and a caller who mistyped any of the three would land a tree
// nobody gated.
//
// TWO CHANNELS, ONE RULE. Progress -- every step, with the elapsed time -- goes to stderr,
// beside one internal/log event each (SPEC-LOGS.md Part 2: one structured line beside the
// human one). STDOUT carries only what a caller RECORDS: the decisions this verb made on
// its own initiative (a flake rerun, a re-enqueue) and the verdict. The verdict is the LAST
// line on stdout, because `land --receipt-file` takes the last line of a file a caller
// piped, and a verb whose receipt was not last would be read as its own progress.
//
// IT NEVER MERGES ANYTHING ITSELF. The queue merges; this verb enqueues through
// merge.Enqueuer -- the one door, reached here by calling `land`'s own code path -- and then
// watches. There is no `gh pr merge` in this file and no auto-merge anywhere near it.

// batchLandInterval is how long the verb waits between polls of the forge when the caller
// names none. Thirty seconds is CI's own granularity: a poll per second would be a rate
// limit rather than a faster landing.
const batchLandInterval = "30s"

// batchLandRounds is how many times the ci-ok wait may be entered: the first run, and ONE
// rerun after it. A second rerun of the same failing test is not a flake, it is a red tree
// that happens to be intermittent, and a verb that kept rerunning would be a verb that
// lands what CI told it not to.
const batchLandRounds = 2

// darwinShard is the word in a job's name that says it ran on the fleet's darwin runners.
// A merge group whose DARWIN shards were CANCELLED is the queue running out of those
// runners -- there are two, and a group behind another group waits for them -- which is a
// re-enqueue and not a red tree. Any other cancellation, and any failure at all, is not.
const darwinShard = "darwin"

// landPhase is one batch's landing, already checked: the gate's own verdict line, the
// branch it built, and what it holds.
type landPhase struct {
	in      batchRun
	receipt string // the BATCH OK line, which is the evidence the one door reads
	branch  string
	headSHA string
	// clone is the checkout the gate built the branch in, HANDED IN rather than computed
	// again from --root and --name: two computations of one path are two paths waiting to
	// disagree, and this one is what the push pushes.
	clone    string
	members  []int
	dropped  []int
	flakes   merge.Flakes
	interval time.Duration
	rounds   int
}

// runBatchLand is steps 1 to 7. It returns the process's exit code: 0 when the batch is on
// the base, 1 when the landing was refused or went red, 2 when it could not run at all.
func runBatchLand(p landPhase, stdout, stderr io.Writer, deps Deps, start time.Time) int {
	lg := &landLogger{w: stderr, now: deps.Now, sleep: deps.Sleep, start: start}
	forge := deps.NewLandForge(p.in.repo, p.in.timeout)
	host := deps.NewHost(p.in.repo, p.in.timeout)

	// STEP 1: THE BRANCH REACHES THE FORGE. Until now the batch is a branch in a clone
	// under --root and nothing outside this machine has seen it.
	if code := landPush(p, stderr, deps, lg); code != 0 {
		return code
	}
	// STEP 2: the batch's own pull request, opened or brought up to date.
	pr, code := landPullRequest(p, forge, stderr, lg)
	if code != 0 {
		return code
	}
	// STEP 3 AND 4: the forge's own verdict on the tree the bench already gated, and the
	// one rerun a known flake earns.
	if code := landWaitForCI(p, host, forge, stdout, stderr, lg); code != 0 {
		return code
	}
	// STEP 5: the one door.
	if code := landEnqueue(p, pr, stdout, stderr, deps, lg); code != 0 {
		return code
	}
	// STEP 6: the queue, watched to its end.
	dev, code := landWatchQueue(p, pr, host, forge, stdout, stderr, deps, lg)
	if code != 0 {
		return code
	}
	// STEP 7: every member closed, pointing at the batch that carried it.
	landCloseMembers(p, pr, dev, forge, stderr, lg)

	fmt.Fprintf(stdout, "BATCH LAND OK name=%s dev=%s members=%s\n",
		oneline.Field(p.in.name), oneline.Field(dev), oneline.Field(numberList(p.members)))
	lg.event("done", fmt.Sprintf("the batch is on %s as %s", oneline.Field(p.in.base), oneline.Field(merge.Short(dev))), pr.Number, nil)
	return 0
}

// landPush puts the batch's branch on the forge.
//
// THE THREE CASES ARE DIFFERENT PUSHES and the difference is the whole safety of the step.
// A branch the forge does not have is an ordinary push. A branch whose head is already this
// commit is nothing at all -- a rebuild of the same batch on the same base. A branch the
// forge has at ANOTHER commit is a batch rebuilt under its own name, and it is published by
// the compare-and-swap of rule 21: the lease names the head this run READ, so a branch
// somebody else moved in between is a refusal rather than a silent overwrite of their work.
func landPush(p landPhase, stderr io.Writer, deps Deps, lg *landLogger) int {
	g := merge.NewGit(p.clone, p.in.timeout, deps.Runner)
	ref := "refs/heads/" + p.branch
	out, err := g.Out("ls-remote", "origin", ref)
	if err != nil {
		return landRefuse(stderr, lg, "push", err)
	}
	prev, _, _ := strings.Cut(strings.TrimSpace(out), "\t")
	prev = strings.TrimSpace(prev)
	switch {
	case prev == p.headSHA:
		lg.say(stderr, "push", fmt.Sprintf("branch=%s head=%s state=unchanged", oneline.Field(p.branch), oneline.Field(p.headSHA)))
		lg.event("push", "the forge already has this branch at this commit", 0, nil)
		return 0
	case prev == "":
		if _, err := g.Run("push", "origin", p.headSHA+":"+ref); err != nil {
			return landRefuse(stderr, lg, "push", err)
		}
		lg.say(stderr, "push", fmt.Sprintf("branch=%s head=%s state=created", oneline.Field(p.branch), oneline.Field(p.headSHA)))
	default:
		if _, err := g.Publish("origin", p.branch, prev, p.headSHA); err != nil {
			return landRefuse(stderr, lg, "push", fmt.Errorf(
				"the branch %s is at %s on the forge and this run built %s on top of a base it read itself; the lease was refused, so somebody else moved that branch: %w",
				p.branch, merge.Short(prev), merge.Short(p.headSHA), err))
		}
		lg.say(stderr, "push", fmt.Sprintf("branch=%s head=%s was=%s state=moved", oneline.Field(p.branch), oneline.Field(p.headSHA), oneline.Field(prev)))
	}
	lg.event("push", "the batch's branch is on the forge", 0, nil)
	return 0
}

// landPullRequest opens the batch's pull request, or brings the one that is already open up
// to date. The body IS the receipt: the BATCH OK line, then the members and the drops, so a
// reader of the pull request can see which tree was gated and what was left out of it.
func landPullRequest(p landPhase, forge merge.LandForge, stderr io.Writer, lg *landLogger) (merge.LandPR, int) {
	body := landBody(p)
	title := fmt.Sprintf("%s: %d pull requests", oneline.Field(p.in.name), len(p.members))
	pr, ok, err := forge.PRForHead(p.branch)
	if err != nil {
		return merge.LandPR{}, landRefuse(stderr, lg, "pr", err)
	}
	if ok {
		if err := forge.UpdatePR(pr.Number, body); err != nil {
			return merge.LandPR{}, landRefuse(stderr, lg, "pr", err)
		}
		lg.say(stderr, "pr", fmt.Sprintf("pr=%d state=updated head=%s", pr.Number, oneline.Field(p.branch)))
		lg.event("pr", "the batch's pull request was already open and now names this tree", pr.Number, nil)
		return pr, 0
	}
	pr, err = forge.OpenPR(p.branch, p.in.base, title, body)
	if err != nil {
		return merge.LandPR{}, landRefuse(stderr, lg, "pr", err)
	}
	lg.say(stderr, "pr", fmt.Sprintf("pr=%d state=opened base=%s", pr.Number, oneline.Field(p.in.base)))
	lg.event("pr", "the batch's pull request is open", pr.Number, nil)
	return pr, 0
}

// landBody is what the pull request says: the gate's verdict line and, under it, what
// landed and what did not. The dropped members are named because a member dropped silently
// is a member whose author is waiting for a landing that is not coming.
func landBody(p landPhase) string {
	return fmt.Sprintf(`%s

members: %s
dropped: %s

Built and gated by nova-merge batch --land on this bench: build, vet, a cross vet for
windows, the test suite the way CI runs it, and the lisp suite. Every member above was
merged onto %s in the order given.
`, p.receipt, numberList(p.members), numberList(p.dropped), oneline.Field(p.in.base))
}

// landWaitForCI is steps 3 and 4: the forge's ci-ok on the batch's own head, polled on the
// caller's interval, and the ONE rerun a failure earns when every failing test is on the
// flake list.
//
// THE TWO GREENS ARE DIFFERENT QUESTIONS AND BOTH ARE ASKED. The gate's green is a bench's:
// this tree builds, vets, tests and runs the lisp suite on one operating system. CI's green
// is the forge's, on three. integration-4 went green on hulk and three CI legs then failed.
func landWaitForCI(p landPhase, host merge.Host, forge merge.LandForge, stdout, stderr io.Writer, lg *landLogger) int {
	deadline := lg.now().Add(p.in.timeout)
	rounds, reran := 1, false
	// awaitChange is what a rerun buys: the forge's ci-ok stays FAILURE for the seconds or
	// minutes between `run rerun --failed` and the rollup being recomputed, and a verb that
	// judged that stale failure would fail the batch for the very flake it just reran.
	awaitChange := false
	for {
		checks, err := host.Checks(p.headSHA)
		if err != nil {
			return landRefuse(stderr, lg, "pr-ci", err)
		}
		state := checkState(checks.ForSHA(p.headSHA), batchRequiredCheck)
		switch {
		case state == "green":
			lg.say(stderr, "pr-ci", fmt.Sprintf("check=%s state=green rounds=%d", batchRequiredCheck, rounds))
			lg.event("pr-ci", "the forge's rollup is green on the batch's head", 0, nil)
			return 0
		case state == "failure" && awaitChange:
			// Still the pre-rerun conclusion: wait for it to move.
		case state == "failure":
			run, err := forge.HeadRun(p.headSHA)
			if err != nil {
				return landFail(stdout, stderr, lg, "pr-ci", "none", nil, nil,
					fmt.Errorf("the batch's head is red and the run behind it could not be read: %w", err))
			}
			if rounds < p.rounds && !reran && landAllFlakes(p.flakes, run) {
				if err := forge.RerunFailed(run.ID); err != nil {
					return landFail(stdout, stderr, lg, "pr-ci", firstName(run.FailedNames()), run.FailedTests(), run.FailedLines(),
						fmt.Errorf("every failing test is a known flake and the rerun could not be asked for: %w", err))
				}
				fmt.Fprintf(stdout, "BATCH LAND rerun=flake tests=%s\n", oneline.Field(numberOrNone(run.FailedTests())))
				lg.say(stderr, "pr-ci", fmt.Sprintf("run=%d state=rerun-flake tests=%s", run.ID, oneline.Field(numberOrNone(run.FailedTests()))))
				lg.event("pr-ci", "every failing test is on the flake list; the failed jobs were rerun once", 0, nil)
				rounds, reran, awaitChange = rounds+1, true, true
			} else {
				return landFail(stdout, stderr, lg, "pr-ci", firstName(run.FailedNames()), run.FailedTests(), run.FailedLines(),
					landWhyNotAFlake(p.flakes, run, reran))
			}
		default:
			// pending, or no ci-ok at all yet: a rollup nobody has finished is not a verdict.
			if awaitChange {
				awaitChange = false
			}
		}
		if !lg.wait(deadline, p.interval) {
			return landFail(stdout, stderr, lg, "pr-ci", "none", nil, nil,
				fmt.Errorf("the batch's %s did not conclude within --timeout %s; the pull request is open and the branch is pushed, so a later run of this verb picks it up where this one stopped", batchRequiredCheck, p.in.timeout))
		}
	}
}

// landAllFlakes is the rerun rule, and it is EVERY failing test or none of them.
//
// A job that failed and named NO test is never a flake: nobody knows what went wrong, and a
// rerun of an unexplained failure is a coin flipped on a landing. A run with no failed job
// at all is not a flake either -- it is a ci-ok that says failure over jobs that say
// otherwise, which is a forge this verb has no reading of.
func landAllFlakes(flakes merge.Flakes, run merge.LandRun) bool {
	if flakes.Len() == 0 {
		return false
	}
	failed := run.Failed()
	if len(failed) == 0 {
		return false
	}
	for _, job := range failed {
		if len(job.Tests) == 0 {
			return false
		}
		for _, test := range job.Tests {
			if _, ok := flakes.Known(job.Package, test); !ok {
				return false
			}
		}
	}
	return true
}

// landWhyNotAFlake is the sentence a red landing carries: which of the three reasons this
// failure was not rerun. A caller told only "FAIL" reads the log; a caller told the reason
// fixes the thing.
func landWhyNotAFlake(flakes merge.Flakes, run merge.LandRun, reran bool) error {
	switch {
	case reran:
		return fmt.Errorf("these tests were rerun once already and failed again; a test that fails twice is a red tree and not a flake")
	case flakes.Len() == 0:
		return fmt.Errorf("the batch's head is red and no --flakes list was given, so no failure here is a known one")
	}
	for _, job := range run.Failed() {
		if len(job.Tests) == 0 {
			return fmt.Errorf("job %q failed and its log named no test, so this verb cannot tell a flake from a broken tree; open the run", job.Name)
		}
		for _, test := range job.Tests {
			if _, ok := flakes.Known(job.Package, test); !ok {
				return fmt.Errorf("%s %s is not on the flake list; fix it, or if it really is a flake add it with its reason and say so to Glenn", job.Package, test)
			}
		}
	}
	return fmt.Errorf("the batch's head is red")
}

// landEnqueue is step 5: THE ONE DOOR, reached by running `land`'s own code path with the
// receipt this gate just printed. It is that code and not a copy of it, because the tool
// holds one rule about what may enter a merge queue and one place it is written down.
func landEnqueue(p landPhase, pr merge.LandPR, stdout, stderr io.Writer, deps Deps, lg *landLogger) int {
	code := runLandVerb(landRun{
		repo:    p.in.repo,
		pr:      pr.Number,
		receipt: p.receipt,
		jump:    true,
		timeout: p.in.timeout,
	}, stdout, stderr, deps)
	if code != 0 {
		lg.say(stderr, "land", fmt.Sprintf("pr=%d state=refused", pr.Number))
		lg.event("land", "the one door refused this batch; the refusal is on the line above", pr.Number, fmt.Errorf("land exit %d", code))
		fmt.Fprintf(stdout, "BATCH LAND FAIL step=land job=none tests=none reason=%q\n",
			oneline.Cap("the one door refused the batch; its LAND REFUSED line names why", oneline.TailBytes))
		return 1
	}
	lg.say(stderr, "land", fmt.Sprintf("pr=%d state=enqueued jump=true", pr.Number))
	lg.event("land", "the batch is at the front of the queue", pr.Number, nil)
	return 0
}

// landWatchQueue is step 6: the queue, watched until the batch is on the base or has left
// the queue without getting there.
//
// A DEQUEUE IS READ, NEVER GUESSED. The forge takes an entry out for two reasons this verb
// tells apart: the merge group's darwin shards were CANCELLED, which is the fleet's two
// darwin runners being busy and is worth exactly one more go at the queue, and anything
// else, which is a red tree and stops. Two consecutive empty reads are what counts as a
// dequeue: the entry also leaves the queue the instant it merges, and one empty read beside
// a pull request the forge has not finished marking merged is the same picture.
func landWatchQueue(p landPhase, pr merge.LandPR, host merge.Host, forge merge.LandForge, stdout, stderr io.Writer, deps Deps, lg *landLogger) (string, int) {
	deadline := lg.now().Add(p.in.timeout)
	seen, empties, requeued := false, 0, false
	for {
		data, err := host.PR(pr.Number)
		if err != nil {
			return "", landRefuse(stderr, lg, "queue", err)
		}
		if data.Merged {
			dev := strings.TrimSpace(data.MergeSHA)
			if dev == "" {
				dev = "unknown"
			}
			lg.say(stderr, "queue", fmt.Sprintf("pr=%d state=merged dev=%s", pr.Number, oneline.Field(dev)))
			lg.event("queue", "the queue merged the batch", pr.Number, nil)
			return dev, 0
		}
		state, err := forge.QueueState(pr.Number)
		if err != nil {
			return "", landRefuse(stderr, lg, "queue", err)
		}
		if strings.TrimSpace(state) != "" {
			seen, empties = true, 0
			lg.say(stderr, "queue", fmt.Sprintf("pr=%d state=%s", pr.Number, oneline.Field(state)))
		} else if seen {
			empties++
		}
		if seen && empties >= 2 {
			run, err := forge.QueueRun(pr.Number, p.in.base)
			if err != nil {
				return "", landFail(stdout, stderr, lg, "queue", "none", nil, nil,
					fmt.Errorf("the entry left the queue without merging and its merge-group run could not be read: %w", err))
			}
			failed := run.Failed()
			if len(failed) == 0 {
				return "", landFail(stdout, stderr, lg, "queue", "none", nil, nil,
					fmt.Errorf("the entry left the queue without merging and its merge-group run names no failed job; look at run %d", run.ID))
			}
			if !requeued && landOnlyCancelledDarwin(failed) {
				requeued = true
				fmt.Fprintf(stdout, "BATCH LAND requeue=cancelled jobs=%s\n", oneline.Field(numberOrNone(run.FailedNames())))
				lg.say(stderr, "queue", fmt.Sprintf("pr=%d state=requeue jobs=%s", pr.Number, oneline.Field(numberOrNone(run.FailedNames()))))
				lg.event("queue", "the merge group's darwin shards were cancelled; the batch goes back to the front of the queue once", pr.Number, nil)
				if code := landEnqueue(p, pr, stdout, stderr, deps, lg); code != 0 {
					return "", code
				}
				seen, empties = false, 0
			} else {
				return "", landFail(stdout, stderr, lg, "queue", firstName(run.FailedNames()), run.FailedTests(), run.FailedLines(),
					landWhyDequeued(failed, requeued))
			}
		}
		if !lg.wait(deadline, p.interval) {
			return "", landFail(stdout, stderr, lg, "queue", "none", nil, nil,
				fmt.Errorf("the batch was still in the queue when --timeout %s ran out; it is enqueued, so watch it or run this verb again", p.in.timeout))
		}
	}
}

// landOnlyCancelledDarwin is the ONE dequeue worth a second go: every job that did not
// succeed was CANCELLED and every one of them is a darwin shard. A single failure among
// them, or a cancellation on any other runner, is not this case.
func landOnlyCancelledDarwin(failed []merge.LandJob) bool {
	for _, j := range failed {
		if !j.Cancelled() || !strings.Contains(strings.ToLower(j.Name), darwinShard) {
			return false
		}
	}
	return len(failed) > 0
}

// landWhyDequeued says which half of the rule refused the second go.
func landWhyDequeued(failed []merge.LandJob, requeued bool) error {
	if requeued {
		return fmt.Errorf("the batch was re-enqueued once already and left the queue again; a second cancellation is the queue telling us to stop rather than a runner that was busy")
	}
	for _, j := range failed {
		if !j.Cancelled() {
			return fmt.Errorf("the merge group's job %q concluded %s; the batch is red on the base it would land on", j.Name, j.Conclusion)
		}
	}
	return fmt.Errorf("the merge group was cancelled on a job that is not a darwin shard, which is not the fleet running out of darwin runners")
}

// landCloseMembers is step 7: every member closed with a pointer to the batch that carried
// it, so an author who comes back to their pull request finds where it went.
//
// A CLOSE THAT FAILS IS NOT A FAILED LANDING. The base has moved; the batch is in. A verb
// that reported FAIL after dev already carried the members would send the next person to
// land it again.
func landCloseMembers(p landPhase, pr merge.LandPR, dev string, forge merge.LandForge, stderr io.Writer, lg *landLogger) {
	for _, n := range p.members {
		comment := fmt.Sprintf("Landed on %s in %s (#%d) as %s. This pull request's commits are on that branch now; closing it here rather than merging it again.",
			oneline.Field(p.in.base), oneline.Field(p.in.name), pr.Number, oneline.Field(merge.Short(dev)))
		if err := forge.CloseMember(n, comment); err != nil {
			lg.say(stderr, "close", fmt.Sprintf("pr=%d state=refused reason=%q", n, oneline.Cap(oneline.Err(err), oneline.TailBytes)))
			lg.event("close", "a member could not be closed; the batch is landed either way", n, err)
			continue
		}
		lg.say(stderr, "close", fmt.Sprintf("pr=%d state=closed batch=%d", n, pr.Number))
		lg.event("close", "the member is closed and points at its batch", n, nil)
	}
}

// landFail is the red verdict: ONE line a caller parses, and the failing lines under it on
// stderr where a person reads them.
func landFail(stdout, stderr io.Writer, lg *landLogger, step, job string, tests, lines []string, why error) int {
	fmt.Fprintf(stdout, "BATCH LAND FAIL step=%s job=%s tests=%s reason=%q\n",
		oneline.Field(step), oneline.Field(job), oneline.Field(numberOrNone(tests)),
		oneline.Cap(oneline.Err(why), oneline.TailBytes))
	for _, line := range lines {
		fmt.Fprintf(stderr, "  %s\n", oneline.Escape(line))
	}
	lg.event(step, "the landing stopped", 0, why)
	return 1
}

// landRefuse is a step that COULD NOT RUN -- git would not talk to the forge, gh timed out.
// Exit 2, like every other "could not run" in this tool, because a caller retries that and
// does not retry a red.
func landRefuse(stderr io.Writer, lg *landLogger, step string, err error) int {
	fmt.Fprintf(stderr, "BATCH LAND REFUSED step=%s: %s\n", oneline.Field(step), oneline.Err(err))
	lg.event(step, "the landing could not run", 0, err)
	return 2
}

// firstName is the job a red line names: the first failed one, or "none" when the run named
// no job at all.
func firstName(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return names[0]
}

// landLogger writes the two lines every step of this verb writes: the human one on stderr,
// with the elapsed time, and the structured one beside it (SPEC-LOGS.md Part 2). The clock
// is injected, so a test's ts is the test's own instant and the sleep between polls is the
// test's own sleep -- no test of this verb waits on the wall clock.
type landLogger struct {
	w     io.Writer
	now   func() time.Time
	sleep func(time.Duration)
	start time.Time
}

// say is the human line: BATCH LAND STEP <name> <fields> t=<seconds>. Every call site
// builds fields out of oneline.Field values, so the one value printed raw here is a token
// this binary already rendered (the audit config names that claim, and
// TestBatchLandPushesOpensEnqueuesWatchesAndClosesTheMembers reads the lines).
func (l *landLogger) say(stderr io.Writer, name, fields string) {
	fmt.Fprintf(stderr, "BATCH LAND STEP %s %s t=%.1fs\n", oneline.Field(name), fields, since(l.start))
}

// event is the structured line: one JSON object per state change, verb batch-land, so a
// LogQL query can ask why a landing is hung without an ssh and a grep (SPEC-LOGS.md Part
// 2). It goes through log.Emit rather than the Line's own Write, because the one writer
// this binary is allowed is fmt.Fprintf and internal/log is the package that escapes.
func (l *landLogger) event(name, msg string, pr int, err error) {
	line := log.New(l.clock(), log.ProcessGUID, "nova-merge")
	line.Verb = "batch-land"
	line.Event = name
	line.Msg = msg
	line.PR = pr
	line.DurMS = time.Since(l.start).Milliseconds()
	if err != nil {
		line.Level = "ERROR"
		line.Err = err.Error()
	}
	_ = log.Emit(l.w, line)
}

// clock is the injected now, or the machine's when a caller injected none.
func (l *landLogger) clock() log.Clock {
	if l.now == nil {
		return func() time.Time { return time.Now().UTC() }
	}
	return func() time.Time { return l.now() }
}

// wait sleeps one interval and reports whether there is time left to poll again. The
// deadline is read from the INJECTED clock and the sleep is the INJECTED sleep -- the same
// two seams `wait` and every loop of this tool takes -- so a test that exercises a
// --timeout runs to its end in microseconds (the two-minute rule: a suite that really waits
// is a suite nobody runs).
func (l *landLogger) wait(deadline time.Time, interval time.Duration) bool {
	if !l.now().Before(deadline) {
		return false
	}
	l.sleep(interval)
	return l.now().Before(deadline)
}

// readFlakes reads --flakes. A path the caller gave and this tool could not read is a
// REFUSAL and never an empty list: a caller who presented the flake list and had it
// silently ignored would read a red landing for the very test they listed.
func readFlakes(path string) (merge.Flakes, error) {
	if strings.TrimSpace(path) == "" {
		return merge.Flakes{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return merge.Flakes{}, fmt.Errorf("--flakes could not be read: %w", err)
	}
	f, err := merge.ParseFlakes(string(raw))
	if err != nil {
		return merge.Flakes{}, fmt.Errorf("--flakes %s: %w", path, err)
	}
	return f, nil
}
