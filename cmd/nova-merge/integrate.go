package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// THE LANDING VERB. Issue #1845, L6 of the swarm roadmap (#1725).
//
// On 2026-09-19 seven landing shifts drove one loop by hand. land6 drove it sixteen times
// between 13:58Z and 16:26Z; land7 six more in the hour after. Every run was the same
// eleven steps typed out, and every step is a place a tired hand skips a read:
//
//	1  every member's head is the sha the caller meant, and no other
//	2  the hold read, per member, AT THAT HEAD
//	3  the grouping simulated onto the base AS IT STANDS NOW
//	4  the gate                       (nova-merge batch)
//	5  the push, under a lease that refuses an existing branch
//	6  the pull request, with the BATCH OK receipt and a basis sentence per member
//	7  ci-ok, polled with a deadline
//	8  the hold read AGAIN, at the door
//	9  the queue                      (nova-merge land)
//	10 each member closed with its pointer
//	11 every remaining open pull request re-verified against the NEW base
//
// `integrate` is those eleven as one verb. IT COMPOSES AND DUPLICATES NOTHING: steps 3,
// 4 and 9 are `simulate`, `batch` and `land` called with the arguments a hand would have
// typed, so a flag this verb gets wrong is refused by the verb that owns it; step 2 and
// step 8 are the hold fold of internal/merge/verdict.go (#1572, #1748) -- LoadLaneVerdicts,
// Host.Verdicts, UnliftedHolds -- and there is no second parser for a DISPOSITION line
// anywhere in this file; step 7 reads a red through internal/ci's `failed` engine, the
// one behind `nova-ci failed`.
//
// EVERY STEP PRINTS ONE TYPED LINE and every refusal names the member and the reason.
// Nothing is retried silently: a red is terminal, and the caller decides what happens
// next with the evidence on their screen.
//
// Law: docs/SPEC-MERGE.md:808-837 (the landing read condition -- a hold anywhere blocks,
// an approve by the author is not a read, and the approve must be for the entry's CURRENT
// head), as docs/SPEC-DECIDE.md reading 3 amends it (the hold is read once at admission
// and again at the door; --lane is required; no flag ignores a hold), and
// docs/SPEC-TOOLWORK.md §6.

// integrateSteps are the eleven, in order, as the typed lines name them. The list is here
// so that the dry run and the documentation cannot drift from the code: --dry-run stops
// after `simulate`, which is this list's third entry and nothing else.
var integrateSteps = []string{
	"HEADS", "HOLD", "SIMULATE", "BATCH", "PUSH", "PR", "CI", "HOLD", "LAND", "CLOSE", "REVERIFY",
}

// integrateDryRunSteps is how far --dry-run goes: heads, holds, simulate.
const integrateDryRunSteps = 3

// member is one entry of --members: the pull request, and the head the caller says it is
// at. BOTH are required. A member named without its head is a member whose head could
// move between the read and the merge, which is the whole reason this verb exists.
type member struct {
	PR   int
	Head string
}

func (m member) String() string { return "#" + strconv.Itoa(m.PR) + "@" + merge.Short(m.Head) }

// cmdIntegrate is the verb's front door: it checks the invocation and hands a checked
// run to runIntegrate.
func cmdIntegrate(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("integrate")
	repo := f.fs.String("repo", "", "")
	local := f.fs.String("local", "", "")
	base := f.fs.String("base", batchBase, "")
	membersRaw := f.fs.String("members", "", "")
	lane := f.fs.String("lane", "", "")
	reviewersFile := f.fs.String("reviewers", "", "")
	on := f.fs.String("on", "", "")
	name := f.fs.String("name", "", "")
	root := f.fs.String("root", "", "")
	basis := f.fs.String("basis", "", "")
	title := f.fs.String("title", "", "")
	reference := f.fs.String("reference", "", "")
	checks := f.fs.String("checks", defaultChecks, "")
	sensitive := f.fs.String("sensitive", "", "")
	designated := f.fs.String("designated", "", "")
	untypedComments := f.fs.String("untyped-comments", "", "")
	reason := f.fs.String("reason", "", "")
	dryRun := f.fs.Bool("dry-run", false, "")
	noDraft := f.fs.Bool("no-draft", false, "")
	gomaxprocs := f.fs.Int("gomaxprocs", 0, "")
	timeoutRaw := f.fs.String("timeout", batchTimeout, "")
	ciTimeoutRaw := f.fs.String("ci-timeout", "40m", "")
	ciIntervalRaw := f.fs.String("ci-interval", "60s", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.require("repo", *repo, "the <owner>/<name> whose pull requests this batch lands, like mas-bandwidth/nova-tools")
	if strings.TrimSpace(*repo) != "" {
		if err := validRepoSlug(*repo); err != nil {
			f.problem(fmt.Sprintf("--repo is <owner>/<name>, got %q: %s", *repo, oneline.Escape(err.Error())))
		}
	}
	f.require("local", *local, "the path of a local clone whose origin holds the members' heads; `simulate` predicts the grouping in it and the integration branch is pushed from it")
	f.require("members", *membersRaw, "the members and the head each one is at, in landing order, like 1749@4f7092ad,1753@9589cc26")
	// --lane is SPEC-DECIDE reading 3's, and it is required HERE rather than only inside
	// `batch`: a landing verb whose hold read had no lane to read records from would be a
	// landing verb that folds half the evidence.
	f.require("lane", *lane, "the lane directory holding this landing's read records and bus notes; the hold fold reads them (SPEC-DECIDE reading 3)")
	if strings.TrimSpace(*lane) == "none" {
		f.problem("--lane none is not a lane; reading 3 requires the records this landing folds")
	}
	f.require("reviewers", *reviewersFile, "the reviewer TSV the hold fold resolves logins through (who, logins, may-hold)")
	f.require("on", *on, "the bench the gate runs on, which every typed line records, like hulk")
	f.require("name", *name, "the batch's own name: its directory under --root and the branch rowan/<name> it builds, like integration-17")
	f.require("root", *root, "the directory the gate clones and builds under, which it rebuilds on every run")
	if !*dryRun {
		f.require("basis", *basis, "the file holding one basis sentence per member, which goes into the pull request body: what each member landed on and at which head")
	}
	if s := strings.TrimSpace(*name); s != "" && !safepath.NameOK(s) {
		f.problem(fmt.Sprintf("--name is one path element of letters, digits, dot, dash and underscore, and never begins with a dash, got %q", *name))
	} else if s != "" {
		if err := merge.ValidRefName("rowan/" + s); err != nil {
			f.problem(fmt.Sprintf("--name: %s", oneline.Escape(err.Error())))
		}
	}
	if s := strings.TrimSpace(*base); s != "" {
		if err := merge.ValidRefName(s); err != nil {
			f.problem(fmt.Sprintf("--base: %s", oneline.Escape(err.Error())))
		}
	}
	members, merr := parseMembers(*membersRaw)
	if merr != nil {
		f.problem(oneline.Escape(merr.Error()))
	}
	if *untypedComments == "ignore" && strings.TrimSpace(*reason) == "" {
		f.problem("--untyped-comments=ignore requires --reason <text>")
	}
	if *untypedComments != "" && *untypedComments != "ignore" {
		f.problem(fmt.Sprintf("--untyped-comments must be ignore, got %q", *untypedComments))
	}
	if strings.TrimSpace(*sensitive) != "" && strings.TrimSpace(*designated) == "" {
		f.problem("--sensitive names the prefixes whose read is the designated mind's, so it requires --designated <who> (SPEC-TOOLWORK eligibility rule 13)")
	}
	timeout, terr := time.ParseDuration(*timeoutRaw)
	if terr != nil || timeout <= 0 {
		f.problem(fmt.Sprintf("--timeout is a duration per step like 30m, got %q", *timeoutRaw))
	}
	ciTimeout, cerr := time.ParseDuration(*ciTimeoutRaw)
	if cerr != nil || ciTimeout <= 0 {
		f.problem(fmt.Sprintf("--ci-timeout is how long this verb waits for ci-ok before saying so, as a duration like 40m; a wait with no deadline is a landing that has stopped saying anything, got %q", *ciTimeoutRaw))
	}
	ciInterval, ierr := time.ParseDuration(*ciIntervalRaw)
	if ierr != nil || ciInterval <= 0 {
		f.problem(fmt.Sprintf("--ci-interval is how long to wait between polls, as a duration like 60s, got %q", *ciIntervalRaw))
	}
	if *gomaxprocs < 0 {
		f.problem(fmt.Sprintf("--gomaxprocs is the share of the machine the gate takes; 0 is all of them, and a negative one is a typo, got %d", *gomaxprocs))
	}
	if !f.done(stderr) {
		return 2
	}
	return runIntegrate(integrateRun{
		repo:            strings.TrimSpace(*repo),
		local:           strings.TrimSpace(*local),
		base:            strings.TrimSpace(*base),
		members:         members,
		lane:            strings.TrimSpace(*lane),
		reviewersFile:   strings.TrimSpace(*reviewersFile),
		on:              strings.TrimSpace(*on),
		name:            strings.TrimSpace(*name),
		root:            strings.TrimSpace(*root),
		basis:           strings.TrimSpace(*basis),
		title:           strings.TrimSpace(*title),
		reference:       strings.TrimSpace(*reference),
		checks:          *checks,
		sensitive:       strings.TrimSpace(*sensitive),
		designated:      strings.TrimSpace(*designated),
		untypedComments: strings.TrimSpace(*untypedComments),
		reason:          strings.TrimSpace(*reason),
		dryRun:          *dryRun,
		draft:           !*noDraft,
		gomaxprocs:      *gomaxprocs,
		timeout:         timeout,
		ciTimeout:       ciTimeout,
		ciInterval:      ciInterval,
	}, stdout, stderr, deps)
}

// integrateRun is one landing's whole invocation, checked.
type integrateRun struct {
	repo            string
	local           string
	base            string
	members         []member
	lane            string
	reviewersFile   string
	on              string
	name            string
	root            string
	basis           string
	title           string
	reference       string
	checks          string
	sensitive       string
	designated      string
	untypedComments string
	reason          string
	dryRun          bool
	draft           bool
	gomaxprocs      int
	timeout         time.Duration
	ciTimeout       time.Duration
	ciInterval      time.Duration
}

// parseMembers reads --members: `<n>@<sha>` separated by commas, in landing order.
//
// The head is NOT optional. The hand loop's one recurring near-miss was a read recorded
// at a head that had since moved (land6: "reads at old heads … rest on the coordinator's
// ruling"), and a member named with no head asks this verb to read whatever the forge
// happens to say at the moment it looks.
func parseMembers(raw string) ([]member, error) {
	fields := strings.Split(raw, ",")
	out := make([]member, 0, len(fields))
	seen := map[int]bool{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		numRaw, headRaw, ok := strings.Cut(field, "@")
		if !ok {
			return nil, fmt.Errorf("--members takes <number>@<sha> so the head is the caller's and not the forge's, got %q", field)
		}
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(numRaw, "#")))
		if err != nil || n < 1 {
			return nil, fmt.Errorf("--members holds %q, which is not a pull request number", numRaw)
		}
		head := strings.ToLower(strings.TrimSpace(headRaw))
		if !isHexSHA(head) {
			return nil, fmt.Errorf("--members holds %q for #%d, which is not a commit sha of 7 to 40 hex characters", headRaw, n)
		}
		if seen[n] {
			return nil, fmt.Errorf("--members names #%d twice; a member lands once", n)
		}
		seen[n] = true
		out = append(out, member{PR: n, Head: head})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--members names no member")
	}
	return out, nil
}

// isHexSHA is the shape of a commit: 7 to 40 hex characters, which is what git's own
// abbreviations run to.
func isHexSHA(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

// integrateRefused is the verb saying NO: exit 1, one line, naming the step, the member
// where there is one, and the reason.
func integrateRefused(stderr io.Writer, step, why string, n int) int {
	if n > 0 {
		fmt.Fprintf(stderr, "INTEGRATE REFUSED step=%s member=#%d reason=%q\n",
			oneline.Field(strings.ToLower(step)), n, oneline.Cap(why, oneline.TailBytes))
		return 1
	}
	fmt.Fprintf(stderr, "INTEGRATE REFUSED step=%s reason=%q\n",
		oneline.Field(strings.ToLower(step)), oneline.Cap(why, oneline.TailBytes))
	return 1
}

// integrateCouldNotRun is exit 2: the invocation was readable and something outside the
// decision failed -- a clone that would not fetch, a forge that would not answer. A
// caller retries this and does not retry a refusal.
func integrateCouldNotRun(stderr io.Writer, step, why string) int {
	fmt.Fprintf(stderr, "INTEGRATE REFUSED step=%s reason=%q\n",
		oneline.Field(strings.ToLower(step)), oneline.Cap(why, oneline.TailBytes))
	return 2
}

func runIntegrate(in integrateRun, stdout, stderr io.Writer, deps Deps) int {
	start := time.Now()
	host := deps.NewHost(in.repo, in.timeout)

	fmt.Fprintf(stdout, "INTEGRATE START name=%s base=%s members=%s lane=%s on=%s dry_run=%t steps=%d\n",
		oneline.Field(in.name), oneline.Field(in.base), oneline.Field(memberList(in.members)),
		oneline.Field(in.lane), oneline.Field(in.on), in.dryRun, len(integrateSteps))

	// STEP 1: THE HEADS ARE THE CALLER'S.
	for _, m := range in.members {
		pr, err := host.PR(m.PR)
		if err != nil {
			return integrateCouldNotRun(stderr, "heads", fmt.Sprintf("pull request %d could not be read: %s", m.PR, oneline.Err(err)))
		}
		if !sameHead(pr.HeadOID, m.Head) {
			fmt.Fprintf(stdout, "INTEGRATE HEADS member=#%d asked=%s forge=%s verdict=moved\n",
				m.PR, oneline.Field(merge.Short(m.Head)), oneline.Field(merge.Short(pr.HeadOID)))
			return integrateRefused(stderr, "heads", fmt.Sprintf(
				"head moved: the caller named %s and the forge says %s; every read this landing folds was recorded at the head the caller named, so this member waits for a fresh read at %s",
				oneline.Field(merge.Short(m.Head)), oneline.Field(merge.Short(pr.HeadOID)), oneline.Field(merge.Short(pr.HeadOID))), m.PR)
		}
		if pr.Merged || pr.Closed {
			return integrateRefused(stderr, "heads", fmt.Sprintf("pull request %d is not open; a member of a batch is an open pull request", m.PR), m.PR)
		}
	}
	fmt.Fprintf(stdout, "INTEGRATE HEADS ok=%d moved=none members=%s t=%.1fs\n",
		len(in.members), oneline.Field(memberList(in.members)), since(start))

	// STEP 2: THE HOLD READ, PASS 1, AT THOSE EXACT HEADS.
	rs, err := merge.LoadReviewers(in.reviewersFile)
	if err != nil {
		return integrateCouldNotRun(stderr, "hold", fmt.Sprintf("reviewer file %s could not be read: %s", oneline.Field(in.reviewersFile), oneline.Err(err)))
	}
	if code := integrateHoldPass(in, host, rs, 1, stdout, stderr, start); code != 0 {
		return code
	}

	// STEP 3: THE GROUPING, SIMULATED ONTO THE BASE AS IT STANDS NOW.
	simArgs := []string{"--repo", in.local, "--base", in.base, "--prs", prList(in.members),
		"--checks", in.checks, "--timeout", in.timeout.String()}
	var sim bytes.Buffer
	if code := cmdSimulate(simArgs, io.MultiWriter(stdout, &sim), stderr, deps); code != 0 {
		if n, ok := firstSimulateConflict(sim.String()); ok {
			return integrateRefused(stderr, "simulate", "the merge conflicts with the entries ahead", n)
		}
		return integrateCouldNotRun(stderr, "simulate", fmt.Sprintf("`simulate` exited %d; the grouping was not predicted, so nothing is gated", code))
	}
	if n, ok := firstSimulateConflict(sim.String()); ok {
		fmt.Fprintf(stdout, "INTEGRATE SIMULATE member=#%d verdict=conflict\n", n)
		return integrateRefused(stderr, "simulate", "the merge conflicts with the entries ahead", n)
	}
	fmt.Fprintf(stdout, "INTEGRATE SIMULATE entries=%d conflicts=none base=%s t=%.1fs\n",
		len(in.members), oneline.Field(in.base), since(start))

	if in.dryRun {
		fmt.Fprintf(stdout, "INTEGRATE DONE name=%s dry_run=true steps=%d/%d landed=none t=%.1fs\n",
			oneline.Field(in.name), integrateDryRunSteps, len(integrateSteps), since(start))
		return 0
	}

	// STEP 4: THE GATE.
	batchArgs := []string{"--name", in.name, "--pr", prList(in.members), "--repo", in.repo,
		"--root", in.root, "--base", in.base, "--timeout", in.timeout.String(),
		"--reviewers", in.reviewersFile, "--lane", in.lane,
		"--gomaxprocs", strconv.Itoa(in.gomaxprocs)}
	if in.reference != "" {
		batchArgs = append(batchArgs, "--reference", in.reference)
	}
	if in.untypedComments != "" {
		batchArgs = append(batchArgs, "--untyped-comments", in.untypedComments, "--reason", in.reason)
	}
	var gate bytes.Buffer
	if code := cmdBatch(batchArgs, io.MultiWriter(stdout, &gate), stderr, deps); code != 0 {
		return integrateRefused(stderr, "batch", fmt.Sprintf("the gate on %s did not say BATCH OK: %s", oneline.Field(in.on), oneline.Escape(oneline.Cap(lastLineOf(gate.String()), oneline.TailBytes))), 0)
	}
	receipt := lastLineOf(gate.String())
	rec, err := merge.ParseBatchReceipt(receipt)
	if err != nil {
		return integrateCouldNotRun(stderr, "batch", fmt.Sprintf("the gate's last line is not a receipt this tool can read: %s", oneline.Err(err)))
	}
	if rec.Dropped != "" && rec.Dropped != "none" && rec.Dropped != "-" {
		return integrateRefused(stderr, "batch", fmt.Sprintf(
			"the gate dropped %s; a landing lands the group the caller named or none of it, so re-run with the members that remain", oneline.Field(rec.Dropped)), 0)
	}
	fmt.Fprintf(stdout, "INTEGRATE BATCH on=%s head=%s members=%s dropped=none receipt=ok t=%.1fs\n",
		oneline.Field(in.on), oneline.Field(rec.Head), oneline.Field(rec.Members), since(start))

	// STEP 5: THE PUSH, UNDER A LEASE THAT REFUSES AN EXISTING BRANCH.
	branch := "rowan/" + in.name
	if code := integratePush(in, branch, rec.Head, stdout, stderr, deps, start); code != 0 {
		return code
	}

	// STEP 6: THE PULL REQUEST, WITH THE RECEIPT AND THE BASIS.
	basis, err := os.ReadFile(in.basis)
	if err != nil {
		return integrateCouldNotRun(stderr, "pr", fmt.Sprintf("--basis %s could not be read: %s", oneline.Field(in.basis), oneline.Err(err)))
	}
	if strings.TrimSpace(string(basis)) == "" {
		return integrateRefused(stderr, "pr", fmt.Sprintf(
			"--basis %s is empty; a batch whose members have no stated basis is a batch nobody can check", oneline.Field(in.basis)), 0)
	}
	body := merge.IntegrationBody{
		Bench: in.on, Receipt: receipt, Basis: string(basis), Branch: branch, Head: rec.Head,
		Lane: in.lane, Reviewers: filepath.Base(in.reviewersFile),
	}.Render()
	title := in.title
	if title == "" {
		title = in.name + ": " + rec.Members + " — gated on " + in.on
	}
	forge := deps.NewIntegrateForge(in.repo, in.timeout)
	ref, err := forge.CreatePR(merge.NewPR{Base: in.base, Head: branch, Title: title, Body: body, Draft: in.draft})
	if err != nil {
		return integrateCouldNotRun(stderr, "pr", fmt.Sprintf("the pull request for %s could not be opened: %s", oneline.Field(branch), oneline.Err(err)))
	}
	fmt.Fprintf(stdout, "INTEGRATE PR number=%d url=%s draft=%t receipt=in-body basis=%s t=%.1fs\n",
		ref.Number, oneline.Field(ref.URL), in.draft, oneline.Field(filepath.Base(in.basis)), since(start))

	// STEP 7: ci-ok, POLLED WITH A DEADLINE.
	if code := integrateCI(in, host, ref.Number, rec.Head, stdout, stderr, deps, start); code != 0 {
		return code
	}

	// STEP 8: THE HOLD READ AGAIN, AT THE DOOR.
	if code := integrateHoldPass(in, host, rs, 2, stdout, stderr, start); code != 0 {
		return code
	}

	// STEP 9: THE QUEUE. `land` stays the one caller of the one door.
	// THE RECEIPT IS PASSED AS A LINE, not as a file: `land --receipt` takes the BATCH OK
	// line itself, read by the same parser a file would have been, and cmd/nova-merge
	// writes no file but the lane's own (source_test.go).
	landArgs := []string{"--repo", in.repo, "--pr", strconv.Itoa(ref.Number),
		"--receipt", receipt, "--reviewers", in.reviewersFile, "--lane", in.lane,
		"--timeout", strconv.Itoa(int(in.timeout.Seconds()))}
	if in.untypedComments != "" {
		landArgs = append(landArgs, "--untyped-comments", in.untypedComments, "--reason", in.reason)
	}
	var landed bytes.Buffer
	if code := cmdLand(landArgs, io.MultiWriter(stdout, &landed), stderr, deps); code != 0 {
		return integrateRefused(stderr, "land", fmt.Sprintf("`land` exited %d and the batch is not in the queue; the pull request %d stands", code, ref.Number), 0)
	}
	fmt.Fprintf(stdout, "INTEGRATE LAND pr=%d head=%s members=%s t=%.1fs\n",
		ref.Number, oneline.Field(rec.Head), oneline.Field(rec.Members), since(start))

	// STEP 10: EACH MEMBER CLOSED WITH ITS POINTER.
	closed := 0
	for _, m := range in.members {
		pointer := merge.MemberPointer{
			Batch: ref.Number, Name: in.name, Bench: in.on, Head: m.Head,
			BatchHead: rec.Head, Base: in.base, Members: rec.Members, Receipt: receipt,
		}.Render()
		if err := forge.ClosePR(m.PR, pointer); err != nil {
			fmt.Fprintf(stdout, "INTEGRATE CLOSE member=#%d verdict=unclosed reason=%q\n", m.PR, oneline.Err(err))
			return integrateCouldNotRun(stderr, "close", fmt.Sprintf(
				"member %d could not be closed: %s; the batch IS in the queue, so close the remaining members by hand rather than running this verb again", m.PR, oneline.Err(err)))
		}
		closed++
		fmt.Fprintf(stdout, "INTEGRATE CLOSE member=#%d head=%s pointer=batch-%d verdict=closed\n",
			m.PR, oneline.Field(merge.Short(m.Head)), ref.Number)
	}

	// STEP 11: EVERY REMAINING OPEN PULL REQUEST, AGAINST THE NEW BASE.
	integrateReverify(in, stdout, stderr, deps, start)

	fmt.Fprintf(stdout, "INTEGRATE DONE name=%s pr=%d head=%s members=%s closed=%d steps=%d/%d t=%.1fs\n",
		oneline.Field(in.name), ref.Number, oneline.Field(rec.Head), oneline.Field(rec.Members),
		closed, len(integrateSteps), len(integrateSteps), since(start))
	return 0
}

// integrateHoldPass is THE hold fold, and it is verdict.go's.
//
// It makes the same five calls `batch`'s admission and `land`'s door make -- the lane's
// read records, the forge's comments and reviews, and UnliftedHolds over both -- because
// a second implementation is a second definition of what a hold is, and the day they
// drift is the day a held member lands.
func integrateHoldPass(in integrateRun, host merge.Host, rs *merge.ReviewerSet, pass int, stdout, stderr io.Writer, start time.Time) int {
	for _, m := range in.members {
		pr, err := host.PR(m.PR)
		if err != nil {
			return integrateCouldNotRun(stderr, "hold", fmt.Sprintf(
				"pull request %d could not be read, and the hold read is on, so this landing cannot tell whether a reader has held it: %s", m.PR, oneline.Err(err)))
		}
		// Pass 2 re-checks the head as well: the door is not the admission, and a head
		// that moved while CI ran is a head nobody's read is at.
		if !sameHead(pr.HeadOID, m.Head) {
			fmt.Fprintf(stdout, "INTEGRATE HOLD pass=%d member=#%d verdict=moved forge=%s\n",
				pass, m.PR, oneline.Field(merge.Short(pr.HeadOID)))
			return integrateRefused(stderr, "hold", fmt.Sprintf(
				"head moved between the gate and the door: the caller named %s and the forge now says %s", oneline.Field(merge.Short(m.Head)), oneline.Field(merge.Short(pr.HeadOID))), m.PR)
		}
		var vs []merge.Verdict
		laneVs, err := merge.LoadLaneVerdicts(in.lane, m.PR)
		if err != nil {
			return integrateCouldNotRun(stderr, "hold", fmt.Sprintf("lane read records for pull request %d could not be read: %s", m.PR, oneline.Err(err)))
		}
		vs = append(vs, laneVs...)
		forgeVs, err := host.Verdicts(m.PR, merge.VerdictOpts{
			Author:          pr.Author,
			CurrentHead:     pr.HeadOID,
			Reviewers:       rs,
			UntypedComments: in.untypedComments,
		})
		if err != nil {
			return integrateCouldNotRun(stderr, "hold", fmt.Sprintf(
				"pull request %d's comments and reviews could not be read, and the hold read is on: %s", m.PR, oneline.Err(err)))
		}
		vs = append(vs, forgeVs...)

		if holds := merge.UnliftedHolds(vs, pr.HeadOID, pr.Author, rs); len(holds) > 0 {
			h := holds[0]
			carried := "no"
			if h.Carried {
				carried = "yes"
			}
			fmt.Fprintf(stdout, "INTEGRATE HOLD pass=%d member=#%d verdict=held who=%s hold=%s source=%s held_at=%s carried=%s at=%s\n",
				pass, m.PR, oneline.Field(h.Who), oneline.Field(h.ID), oneline.Field(h.Source),
				oneline.Field(merge.Short(h.Head)), oneline.Field(carried), oneline.Field(h.At))
			return integrateRefused(stderr, "hold", fmt.Sprintf(
				"head %s carries an unreleased HOLD by %s (%s, %s); no flag here lifts one", oneline.Field(merge.Short(pr.HeadOID)), oneline.Field(h.Who), oneline.Field(h.Source), oneline.Field(h.At)), m.PR)
		}
		// THE SENSITIVE-PREFIX RULE (SPEC-TOOLWORK eligibility rule 13): a member whose
		// diff touches one of the named prefixes has ONE reader -- the designated mind --
		// and that mind's APPROVE at THIS head is the only thing that satisfies it. With
		// no --sensitive file the rule is UNCHECKED and says so, exactly as hygiene's
		// out-of-path check prints `paths=-` rather than passing a diff it never bounded.
		word, why, code := integrateSensitive(in, m, vs, pr)
		if code != 0 {
			fmt.Fprintf(stdout, "INTEGRATE HOLD pass=%d member=#%d verdict=sensitive prefixes=%s\n", pass, m.PR, oneline.Field(why))
			return integrateRefused(stderr, "hold", why, m.PR)
		}
		fmt.Fprintf(stdout, "INTEGRATE HOLD pass=%d member=#%d head=%s verdict=clear verdicts=%d sensitive=%s\n",
			pass, m.PR, oneline.Field(merge.Short(pr.HeadOID)), len(vs), oneline.Field(word))
	}
	fmt.Fprintf(stdout, "INTEGRATE HOLD pass=%d members=%d held=none t=%.1fs\n", pass, len(in.members), since(start))
	return 0
}

// integrateSensitive answers what the sensitive-prefix rule says about one member. The
// word is what the typed line prints: `unchecked` when no prefix file was given, `none`
// when the member touches none of them, and `<who>` when the designated mind's approve
// at this head is what admitted it.
func integrateSensitive(in integrateRun, m member, vs []merge.Verdict, pr merge.PR) (word, why string, code int) {
	if in.sensitive == "" {
		return "unchecked", "", 0
	}
	prefixes, err := merge.ReadPrefixes(in.sensitive)
	if err != nil {
		return "", fmt.Sprintf("--sensitive %s could not be read: %s", oneline.Field(in.sensitive), oneline.Err(err)), 1
	}
	paths, err := memberPaths(in, m)
	if err != nil {
		return "", fmt.Sprintf("the changed paths of member %d could not be read: %s", m.PR, oneline.Err(err)), 1
	}
	var touched []string
	for _, p := range paths {
		for _, pre := range prefixes {
			if strings.HasPrefix(p, pre) {
				touched = append(touched, pre)
				break
			}
		}
	}
	if len(touched) == 0 {
		return "none", "", 0
	}
	sort.Strings(touched)
	touched = dedupe(touched)
	for _, v := range vs {
		if !strings.EqualFold(v.Who, in.designated) || v.Word != "approve" {
			continue
		}
		if sameHead(v.Head, pr.HeadOID) {
			return in.designated, "", 0
		}
	}
	return "", fmt.Sprintf(
		"the diff touches %s, whose read is %s's and nobody else's, and %s has recorded no APPROVE at head %s; where that mind is asleep the work waits",
		oneline.Field(strings.Join(touched, ",")), oneline.Field(in.designated), oneline.Field(in.designated), oneline.Field(merge.Short(pr.HeadOID))), 1
}

// integratePush is step 5: the branch is pushed ONLY if no branch of that name exists.
//
// The hand loop wrote the lease as `--force-with-lease=refs/heads/<branch>:`, and this
// package's git guard refuses exactly that spelling (gitops.go: "a ref and no sha", so
// the remote is asked to compare with whatever it last told this clone). It is refused
// for a good reason, and the requirement it was standing in for -- REFUSE IF THE BRANCH
// EXISTS, NEVER FORCE -- is better served by what this does: read the remote's refs, and
// push plainly, which creates a branch and cannot overwrite one.
func integratePush(in integrateRun, branch, head string, stdout, stderr io.Writer, deps Deps, start time.Time) int {
	g := merge.NewGit(in.local, in.timeout, deps.Runner)
	out, err := g.Out("ls-remote", "--heads", "origin", "refs/heads/"+branch)
	if err != nil {
		return integrateCouldNotRun(stderr, "push", fmt.Sprintf("origin could not be asked whether %s exists: %s", oneline.Field(branch), oneline.Err(err)))
	}
	if existing := countRefs(out); existing != 0 {
		fmt.Fprintf(stdout, "INTEGRATE PUSH branch=%s lease=must-not-exist existed=%d verdict=refused\n",
			oneline.Field(branch), existing)
		return integrateRefused(stderr, "push", fmt.Sprintf(
			"origin already holds %s; this verb's lease is must-not-exist and it never forces, so give --name a name of its own", oneline.Field(branch)), 0)
	}
	// THE GATED OBJECT LIVES IN THE GATE'S OWN CLONE and nowhere else: `batch` clones
	// under --root, builds rowan/<name> there and PUSHES NOTHING. So the object is
	// fetched from that clone into the caller's, which is the clone whose origin the
	// caller can write to -- and the fetch is by the branch the gate built, not by a bare
	// sha, because a server need not serve an arbitrary object to a want.
	gate, err := gateClonePath(in)
	if err != nil {
		return integrateCouldNotRun(stderr, "push", oneline.Err(err))
	}
	if _, err := g.Run("fetch", "--quiet", gate, branch); err != nil {
		return integrateCouldNotRun(stderr, "push", fmt.Sprintf("the gate's tree at %s could not be fetched: %s", oneline.Field(gate), oneline.Err(err)))
	}
	fetched, err := g.Out("rev-parse", "FETCH_HEAD")
	if err != nil {
		return integrateCouldNotRun(stderr, "push", fmt.Sprintf("the gate's head could not be resolved after the fetch: %s", oneline.Err(err)))
	}
	if !sameHead(fetched, head) {
		return integrateRefused(stderr, "push", fmt.Sprintf(
			"the gate's receipt names %s and its clone's %s is at %s; the tree about to be pushed is not the tree that was gated",
			oneline.Field(merge.Short(head)), oneline.Field(branch), oneline.Field(merge.Short(fetched))), 0)
	}
	if _, err := g.Run("push", "origin", head+":refs/heads/"+branch); err != nil {
		return integrateCouldNotRun(stderr, "push", fmt.Sprintf("%s could not be pushed to origin: %s", oneline.Field(branch), oneline.Err(err)))
	}
	back, err := g.Out("ls-remote", "--heads", "origin", "refs/heads/"+branch)
	if err != nil {
		return integrateCouldNotRun(stderr, "push", fmt.Sprintf("%s could not be read back off origin: %s", oneline.Field(branch), oneline.Err(err)))
	}
	readback := firstRefSHA(back)
	if !sameHead(readback, head) {
		return integrateRefused(stderr, "push", fmt.Sprintf(
			"origin read back %s for %s and the gate's head is %s; somebody else is writing this branch", oneline.Field(merge.Short(readback)), oneline.Field(branch), oneline.Field(merge.Short(head))), 0)
	}
	fmt.Fprintf(stdout, "INTEGRATE PUSH branch=%s head=%s lease=must-not-exist existed=0 readback=%s forced=no t=%.1fs\n",
		oneline.Field(branch), oneline.Field(head), oneline.Field(readback), since(start))
	return 0
}

// integrateCI is step 7: poll ci-ok on the batch's own head until it is green, red, or
// the deadline runs out. ONE TYPED LINE PER STATE, and nothing is retried: a red is
// terminal, and a red with a named failing test names it.
//
// The runner-cleanup shape of #1751 is NOT special-cased. It was a real bench fault that
// eleven runs hit on 2026-09-19, it is fixed on dev since #1779, and a verb carrying a
// permanent exemption for a fault that has been repaired is a verb that will one day
// swallow a real red wearing the same clothes.
func integrateCI(in integrateRun, host merge.Host, pr int, head string, stdout, stderr io.Writer, deps Deps, start time.Time) int {
	deadline := deps.Now().Add(in.ciTimeout)
	last := ""
	for {
		checks, err := host.Checks(head)
		if err != nil {
			return integrateCouldNotRun(stderr, "ci", fmt.Sprintf("the checks of pull request %d could not be read: %s", pr, oneline.Err(err)))
		}
		state := checkState(checks.ForSHA(head), batchRequiredCheck)
		if state != last {
			fmt.Fprintf(stdout, "INTEGRATE CI pr=%d head=%s check=%s state=%s t=%.1fs\n",
				pr, oneline.Field(merge.Short(head)), batchRequiredCheck, oneline.Field(state), since(start))
			last = state
		}
		switch state {
		case "green":
			return 0
		case "failure":
			return integrateCIRed(in, pr, stdout, stderr, deps)
		}
		if !deps.Now().Before(deadline) {
			fmt.Fprintf(stdout, "INTEGRATE CI pr=%d head=%s check=%s state=timeout waited=%s\n",
				pr, oneline.Field(merge.Short(head)), batchRequiredCheck, oneline.Field(in.ciTimeout.String()))
			return integrateRefused(stderr, "ci", fmt.Sprintf(
				"%s was still %s after %s; the pull request stands and nothing was queued", batchRequiredCheck, oneline.Field(state), oneline.Field(in.ciTimeout.String())), 0)
		}
		deps.Sleep(in.ciInterval)
	}
}

// integrateCIRed is what a red costs: the failing tests are NAMED, through the same
// engine `nova-ci failed` prints, and the landing stops.
func integrateCIRed(in integrateRun, pr int, stdout, stderr io.Writer, deps Deps) int {
	if deps.NewFailForge == nil {
		return integrateRefused(stderr, "ci", fmt.Sprintf("%s is red on pull request %d", batchRequiredCheck, pr), 0)
	}
	forge := deps.NewFailForge(in.repo, in.timeout)
	if forge == nil {
		return integrateRefused(stderr, "ci", fmt.Sprintf("%s is red on pull request %d, and this build has no edge to read the run through", batchRequiredCheck, pr), 0)
	}
	runID, report, err := ci.ReadFailedRun(forge, ci.RunSelector{PR: pr}, "")
	if err != nil {
		fmt.Fprintf(stdout, "INTEGRATE FAIL pr=%d test=unread reason=%q\n", pr, oneline.Err(err))
		return integrateRefused(stderr, "ci", fmt.Sprintf(
			"%s is red on pull request %d and the run could not be read to name the test: %s", batchRequiredCheck, pr, oneline.Err(err)), 0)
	}
	if len(report.Failures) == 0 {
		fmt.Fprintf(stdout, "INTEGRATE FAIL pr=%d run=%d test=none jobs=%d cancelled=%d\n",
			pr, runID, report.Jobs, report.Cancelled)
		return integrateRefused(stderr, "ci", fmt.Sprintf(
			"%s is red on pull request %d and run %d named no failing test; read the run before anything else happens (%s)",
			batchRequiredCheck, pr, runID, oneline.Escape(oneline.Cap(report.SummaryLine(), oneline.TailBytes))), 0)
	}
	for _, f := range report.Failures {
		fmt.Fprintf(stdout, "INTEGRATE FAIL pr=%d run=%d test=%s pkg=%s job=%s at=%s\n",
			pr, runID, oneline.Field(f.Test), oneline.Field(f.Package), oneline.Field(f.Job), oneline.Field(f.At))
	}
	return integrateRefused(stderr, "ci", fmt.Sprintf(
		"%s is red on pull request %d with %d named failing test(s), the first of them %s; a named failing test is a finding and never a rerun",
		batchRequiredCheck, pr, len(report.Failures), oneline.Field(report.Failures[0].Test)), 0)
}

// integrateReverify is step 11: every pull request still open, against the base the
// landing just moved. It is a TABLE and never a refusal -- the landing has happened, and
// what this prints is what the next one has to deal with.
func integrateReverify(in integrateRun, stdout, stderr io.Writer, deps Deps, start time.Time) {
	if deps.NewRebaseList == nil {
		fmt.Fprintf(stdout, "INTEGRATE REVERIFY open=unread reason=%q\n", "this build has no open-list edge")
		return
	}
	open, err := deps.NewRebaseList(in.repo, in.timeout).OpenPRs()
	if err != nil {
		fmt.Fprintf(stdout, "INTEGRATE REVERIFY open=unread reason=%q\n", oneline.Err(err))
		return
	}
	landed := map[int]bool{}
	for _, m := range in.members {
		landed[m.PR] = true
	}
	dirty := 0
	counted := 0
	for _, p := range open {
		if landed[p.Number] {
			continue
		}
		counted++
		state := strings.ToUpper(strings.TrimSpace(p.MergeState))
		if state == "" {
			state = "UNKNOWN"
		}
		if state == "DIRTY" || state == "CONFLICTING" {
			dirty++
		}
		fmt.Fprintf(stdout, "INTEGRATE REVERIFY pr=#%d base=%s mergeable=%s\n",
			p.Number, oneline.Field(in.base), oneline.Field(state))
	}
	fmt.Fprintf(stdout, "INTEGRATE REVERIFY open=%d dirty=%d base=%s t=%.1fs\n",
		counted, dirty, oneline.Field(in.base), since(start))
}

// sameHead compares two heads, either of which may be an abbreviation.
func sameHead(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return strings.HasPrefix(b, a)
}

// memberList is the members as the typed lines print them: #n@sha, comma separated.
func memberList(ms []member) string {
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		parts = append(parts, m.String())
	}
	return strings.Join(parts, ",")
}

// prList is the members as `batch --pr` takes them: numbers, comma separated, in order.
func prList(ms []member) string {
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		parts = append(parts, strconv.Itoa(m.PR))
	}
	return strings.Join(parts, ",")
}

// firstSimulateConflict is the first member `simulate` said conflicts with the entries
// ahead. It reads simulate's OWN typed line rather than judging the merge again.
func firstSimulateConflict(out string) (int, bool) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "SIMULATE CONFLICT #") {
			continue
		}
		rest := strings.TrimPrefix(line, "SIMULATE CONFLICT #")
		num, _, _ := strings.Cut(rest, " ")
		if n, err := strconv.Atoi(strings.TrimSpace(num)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// lastLineOf is a sub-verb's verdict: its last non-empty line, the same rule `land`
// reads a receipt file by.
func lastLineOf(raw string) string {
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// countRefs counts the refs `git ls-remote` printed.
func countRefs(out string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// firstRefSHA is the sha of the first ref `git ls-remote` printed.
func firstRefSHA(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) >= 1 && fields[0] != "" {
			return fields[0]
		}
	}
	return ""
}

// dedupe removes repeats from a sorted list.
func dedupe(in []string) []string {
	out := in[:0:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// memberPaths is the member's changed paths, read out of the local clone against the
// base: `git diff --name-only <base>...<head>`, the three-dot form, so the answer is what
// the member changed and not what the base did while it waited.
func memberPaths(in integrateRun, m member) ([]string, error) {
	g := merge.NewGit(in.local, in.timeout, nil)
	if _, err := g.Run("fetch", "--quiet", "origin"); err != nil {
		return nil, err
	}
	if _, err := g.Run("fetch", "--quiet", "origin", "pull/"+strconv.Itoa(m.PR)+"/head"); err != nil {
		return nil, err
	}
	out, err := g.Out("diff", "--name-only", "origin/"+in.base+"..."+m.Head)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// gateClonePath is where `batch` put the tree it gated: <root>/<name>/repo, the one
// place the integration branch exists after a gate that pushes nothing. It is computed
// the same way runBatch computes it, and a drift between the two is a fetch that fails
// by name rather than a push of the wrong tree.
func gateClonePath(in integrateRun) (string, error) {
	rootAbs, err := filepath.Abs(in.root)
	if err != nil {
		return "", fmt.Errorf("--root %s: %w", in.root, err)
	}
	return filepath.Join(rootAbs, in.name, "repo"), nil
}
