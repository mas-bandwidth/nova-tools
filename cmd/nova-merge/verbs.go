package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// openLane is what every verb but init does first: read the lane's state, and refuse a
// directory that is not one with the command that makes one. Never a state file written
// on the way past.
func openLane(verb, lane string, stderr io.Writer) (*merge.State, int) {
	st, err := merge.Load(lane)
	if errors.Is(err, merge.ErrNotALane) {
		fmt.Fprintf(stderr, "nova-merge %s: %s\n", verb, oneline.Escape(merge.NotALaneRefusal(lane)))
		return nil, 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "nova-merge %s: %s\n", verb, oneline.Err(err))
		return nil, 2
	}
	return st, 0
}

// cmdInit is the ONE creation verb (rule 20). quickstart is the same creation followed by
// a status, which is the natural first run: a stranger's first line makes a lane and then
// looks at it.
func cmdInit(args []string, stdout, stderr io.Writer, deps Deps, quickstart bool) int {
	verb := "init"
	if quickstart {
		verb = "quickstart"
	}
	f := laneSet(verb)
	repo := f.fs.String("repo", "", "")
	base := f.fs.String("base", "", "")
	laneBranch := f.fs.String("lane-branch", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	f.require("repo", *repo, "the repository this lane lands into, as <owner>/<name>")
	f.require("base", *base, "the branch this lane's entries are merged onto")
	f.require("lane-branch", *laneBranch, "the branch of that repository this lane's read and gate records live in")
	if strings.TrimSpace(*repo) != "" && !strings.Contains(*repo, "/") {
		f.problem(fmt.Sprintf("--repo is <owner>/<name>, got %q", *repo))
	}
	if !f.done(stderr) {
		return 2
	}
	if _, err := os.Stat(merge.StatePath(*f.lane)); err == nil {
		fmt.Fprintf(stderr, "INIT REFUSED: %s is already a lane; init creates one and never rewrites one, so its repo, base and lane branch are what the first init wrote\n",
			oneline.Field(*f.lane))
		return 1
	}
	joined, err := checkout(*f.lane, *repo, *laneBranch, f.dur(), deps)
	if err != nil {
		fmt.Fprintf(stderr, "INIT REFUSED: %s\n", oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 2
	}
	if err := merge.Init(*f.lane, *repo, *base, *laneBranch); err != nil {
		fmt.Fprintf(stderr, "INIT REFUSED: %s\n", oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 1
	}
	g := merge.NewGit(*f.lane, f.dur(), deps.Runner)
	// A lane whose state.json was LOST is re-made by init on the same branch, and its
	// checkout and its clone are still on the disk (rule 22: losing state.json loses the
	// order and nothing else). A clone that is already here is taken rather than refused.
	if _, err := os.Stat(filepath.Join(*f.lane, merge.RepoDir, ".git")); err == nil {
		merge.Appendf(*f.lane, deps.Now(), "INIT took the clone that was already at %s", filepath.Join(*f.lane, merge.RepoDir))
	} else if _, err := g.Run("clone", deps.RepoURL(*repo), merge.RepoDir); err != nil {
		fmt.Fprintf(stderr, "INIT REFUSED: the lane's own clone could not be made: %s\n", oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 2
	}
	merge.Appendf(*f.lane, deps.Now(), "INIT lane=%s repo=%s base=%s lane_branch=%s joined=%t", *f.lane, *repo, *base, *laneBranch, joined)
	fmt.Fprintf(stdout, "INIT OK lane=%s repo=%s base=%s lane_branch=%s joined=%t version=%d\n",
		oneline.Field(*f.lane), oneline.Field(*repo), oneline.Field(*base), oneline.Field(*laneBranch), joined, merge.Version)
	if !quickstart {
		return 0
	}
	return cmdStatus([]string{"--lane", *f.lane, "--timeout", strconv.Itoa(*f.timeout), "--max", strconv.Itoa(*f.max)}, stdout, stderr, deps)
}

// checkout makes the lane directory a checkout of its own branch of the repository: the
// existing branch where the host has one (joined=true, which is how a reader on another
// machine gets a lane for the same records), and otherwise one commit holding .gitignore.
func checkout(lane, repo, branch string, timeout time.Duration, deps Deps) (joined bool, err error) {
	if err := os.MkdirAll(lane, 0o755); err != nil {
		return false, err
	}
	url := deps.RepoURL(repo)
	g := merge.NewGit(lane, timeout, deps.Runner)
	if _, err := g.Run("init", "-q", "-b", branch, "."); err != nil {
		return false, err
	}
	if _, err := g.Run("remote", "add", "origin", url); err != nil {
		if _, err2 := g.Run("remote", "set-url", "origin", url); err2 != nil {
			return false, err
		}
	}
	if _, err := g.Run("fetch", "origin", branch); err == nil {
		if _, err := g.Run("checkout", "-B", branch, "FETCH_HEAD"); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := os.WriteFile(filepath.Join(lane, ".gitignore"), []byte(merge.GitIgnore), 0o644); err != nil {
		return false, err
	}
	if _, err := g.Run("add", "--", ".gitignore"); err != nil {
		return false, err
	}
	if _, err := g.Run("-c", "user.name=nova-merge", "-c", "user.email=nova-merge@localhost",
		"commit", "-m", "nova-merge: the lane's record branch"); err != nil {
		return false, err
	}
	if _, err := g.Run("push", "origin", "HEAD:refs/heads/"+branch); err != nil {
		return false, err
	}
	return false, nil
}

// cmdAdd queues an entry into a lane that EXISTS. It creates nothing: the lane was made
// by init, with its repository and its base, and a queueing verb that also created would
// be a creation with two arguments nothing asked for.
func cmdAdd(args []string, stdout, stderr io.Writer, deps Deps, isBranch bool) int {
	verb := "add"
	if isBranch {
		verb = "add-branch"
	}
	f := laneSet(verb)
	pr := f.fs.Int("pr", 0, "")
	branch := f.fs.String("branch", "", "")
	needsRead := f.fs.Bool("needs-read", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	if isBranch {
		f.require("branch", *branch, "the branch this lane is to land, as it is named at the host")
	} else if *pr < 1 {
		f.problem("--pr is required and is the pull request's number; refusing to guess")
	}
	if !f.done(stderr) {
		return 2
	}
	st, code := openLane(verb, *f.lane, stderr)
	if st == nil {
		return code
	}
	id := *branch
	if !isBranch {
		id = strconv.Itoa(*pr)
	}
	if e := st.Find(id); e != nil {
		fmt.Fprintf(stdout, "ADD NOTE %s is already in the lane (needs_read=%s)\n", oneline.Field(id), oneline.Field(e.NeedsRead))
		return 0
	}
	yn := "no"
	if *needsRead {
		yn = "yes"
	}
	err := merge.Update(*f.lane, merge.LockWait, func(s *merge.State) error {
		e := &merge.Entry{NeedsRead: yn, Reads: []merge.Read{}, State: merge.StateNew}
		if isBranch {
			e.Branch = *branch
			s.Branches = append(s.Branches, e)
		} else {
			e.PR = *pr
			s.PRs = append(s.PRs, e)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(stderr, "ADD REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	st, _ = merge.Load(*f.lane)
	merge.Appendf(*f.lane, deps.Now(), "ADD %s %s needs_read=%s", verb, id, yn)
	kind := "pr"
	if isBranch {
		kind = "branch"
	}
	fmt.Fprintf(stdout, "ADD OK kind=%s entry=%s needs_read=%s lane=%d/%d\n",
		kind, oneline.Field(id), yn, len(st.PRs), len(st.Branches))
	return 0
}

// entrySelector is (--pr <n>|--branch <name>) on read, gate and packet: exactly one.
func entrySelector(f *laneFlags, pr *int, branch *string, allowNone bool) string {
	switch {
	case *pr > 0 && *branch != "":
		f.problem("--pr and --branch name two entries and a verb acts on one; give one of them")
		return ""
	case *pr > 0:
		return strconv.Itoa(*pr)
	case *branch != "":
		return *branch
	}
	if !allowNone {
		f.problem("--pr <n> or --branch <name> is required; refusing to guess which entry this is about")
	}
	return ""
}

// cmdRead records a reader's verdict: ONE IMMUTABLE FILE, written to the outbox and
// pushed to the lane's branch by the tool (rules 19 and 22).
//
// --head is required and is the full sha the reader had open. The tool never fills it in
// from the entry's current oid, because the entry's head at record time is not the head
// the reader read: if Emma finishes reading H1 and the author pushes H2 before she types
// the verb, a stamp taken at record time would put her H1 judgment on H2.
func cmdRead(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := laneSet("read")
	pr := f.fs.Int("pr", 0, "")
	branch := f.fs.String("branch", "", "")
	who := f.fs.String("who", "", "")
	head := f.fs.String("head", "", "")
	verdict := f.fs.String("verdict", "", "")
	note := f.fs.String("note", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	id := entrySelector(f, pr, branch, false)
	f.require("who", *who, "the name of the line recording this verdict, as this lane knows it")
	f.require("head", *head, "the full 40-character sha the reader had open; the verdict binds to that sha and never to whatever the entry's head is now")
	if *head != "" && !merge.IsSHA(*head) {
		f.problem(fmt.Sprintf("--head wants the full 40-character sha the reader had open, got %q; a truncated sha might name the wrong commit", *head))
	}
	if *verdict != "approve" && *verdict != "hold" {
		f.problem(fmt.Sprintf("--verdict is approve or hold, got %q; refusing to guess", *verdict))
	}
	if !f.done(stderr) {
		return 2
	}
	st, code := openLane("read", *f.lane, stderr)
	if st == nil {
		return code
	}
	sub, err := merge.NewSubmission(deps.Now())
	if err != nil {
		fmt.Fprintf(stderr, "READ REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	file := merge.ReadFile(merge.EntryDirName(id), *who, *head, sub)
	rec := merge.Read{Who: *who, Verdict: *verdict, Note: *note, At: sub.At, Head: *head, File: file}
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "READ REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	body = append(body, '\n')
	recs := merge.NewRecords(*f.lane, st.LaneBranch, "origin", merge.NewGit(*f.lane, f.dur(), deps.Runner), f.dur())
	merge.Appendf(*f.lane, deps.Now(), "READ entry=%s who=%s head=%s verdict=%s file=%s", id, *who, *head, *verdict, file)
	pushErr := recs.Deliver(sub, []merge.Item{{Path: file, Body: body}})
	current, approvals, holds, stale := standingOf(st, id, *head)
	if pushErr != nil {
		fmt.Fprintf(stderr, "READ FAIL entry=%s who=%s head=%s file=%s pushed=false: %s; re-run the same verb to push it\n",
			oneline.Field(id), oneline.Field(*who), oneline.Field(merge.Short(*head)), oneline.Field(file),
			oneline.Escape(oneline.Cap(pushErr.Error(), oneline.TailBytes)))
		return 1
	}
	foldInto(*f.lane, st, recs, f.dur())
	current, approvals, holds, stale = standingOf(st, id, *head)
	fmt.Fprintf(stdout, "READ OK entry=%s who=%s verdict=%s head=%s current=%s approvals=%d holds=%d stale=%d file=%s pushed=true\n",
		oneline.Field(id), oneline.Field(*who), oneline.Field(*verdict), oneline.Field(merge.Short(*head)),
		current, approvals, holds, stale, oneline.Field(file))
	return 0
}

// standingOf is READ OK's counts: current says whether the sha the reader supplied is the
// oid the last pass recorded, so a reader who is already stale hears it at once.
func standingOf(st *merge.State, id, head string) (current string, approvals, holds, stale int) {
	e := st.Find(id)
	if e == nil || e.OID == "" {
		return "-", 0, 0, 0
	}
	s := merge.EvaluateReads(e, "")
	current = "false"
	if head == e.OID {
		current = "true"
	}
	return current, s.Approves, s.Holds, s.Stale
}

// cmdGate records a gate runner's verdict for ONE integration commit, named by sha.
//
// All four are refusals rather than tolerances: a gate with a truncated sha is a gate
// that might match the wrong commit, a gate with no base is a gate for a commit nobody
// can name, a gate with no merge is a gate for an object nobody can publish, and a gate
// with no summary is a claim with no evidence behind it.
func cmdGate(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := laneSet("gate")
	pr := f.fs.Int("pr", 0, "")
	branch := f.fs.String("branch", "", "")
	head := f.fs.String("head", "", "")
	baseSHA := f.fs.String("base-sha", "", "")
	mergeSHA := f.fs.String("merge", "", "")
	verdict := f.fs.String("verdict", "", "")
	summary := f.fs.String("summary", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	id := entrySelector(f, pr, branch, false)
	for _, s := range []struct{ name, value, wants string }{
		{"head", *head, "the full 40-character sha of the entry's head this gate was taken for"},
		{"base-sha", *baseSHA, "the full 40-character sha of the base this gate was taken against (rule 18)"},
		{"merge", *mergeSHA, "the full 40-character sha of the integration commit that was gated (rule 21)"},
	} {
		if strings.TrimSpace(s.value) == "" {
			f.problem(fmt.Sprintf("--%s is required; refusing to guess: %s", s.name, s.wants))
		} else if !merge.IsSHA(s.value) {
			f.problem(fmt.Sprintf("--%s wants a full 40-character sha, got %q: %s", s.name, s.value, s.wants))
		}
	}
	if *verdict != "green" && *verdict != "red" {
		f.problem(fmt.Sprintf("--verdict is green or red, got %q; refusing to guess", *verdict))
	}
	f.require("summary", *summary, "the path of the gate's own summary, which must exist: a gate with no summary is a claim with no evidence behind it")
	if *summary != "" {
		if _, err := os.Stat(*summary); err != nil {
			f.problem(fmt.Sprintf("--summary %q does not exist; a gate summary is read at record time, not trusted as a path", *summary))
		}
	}
	// THE TWO KINDS, told apart by the shas. A base gate is the base merged onto itself,
	// so all three are equal; an integration gate's three differ. Any other mix names no
	// object either kind can validate.
	if merge.IsSHA(*head) && merge.IsSHA(*baseSHA) && merge.IsSHA(*mergeSHA) {
		allEqual := *head == *baseSHA && *baseSHA == *mergeSHA
		allDiffer := *head != *baseSHA && *mergeSHA != *head && *mergeSHA != *baseSHA
		if !allEqual && !allDiffer {
			f.problem("these three shas name no object either kind of gate can validate: a BASE gate has --head, --base-sha and --merge all the base's own sha, and an INTEGRATION gate has three that differ")
		}
	}
	if !f.done(stderr) {
		return 2
	}
	st, code := openLane("gate", *f.lane, stderr)
	if st == nil {
		return code
	}
	abs, err := filepath.Abs(*summary)
	if err != nil {
		abs = *summary
	}
	sub, err := merge.NewSubmission(deps.Now())
	if err != nil {
		fmt.Fprintf(stderr, "GATE REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	file := merge.GateFile(merge.EntryDirName(id), *head, *baseSHA, sub)
	rec := merge.Gate{Head: *head, Base: *baseSHA, Merge: *mergeSHA, Verdict: *verdict,
		Summary: abs, At: sub.At, Run: sub.Rand, File: file}
	if *pr > 0 {
		rec.PR = *pr
	} else {
		rec.Branch = *branch
	}
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "GATE REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	body = append(body, '\n')
	summaryBytes, err := os.ReadFile(*summary)
	if err != nil {
		fmt.Fprintf(stderr, "GATE REFUSED: the summary could not be read: %s\n", oneline.Err(err))
		return 2
	}
	inLane := st.Find(id) != nil
	newest := isNewest(st, id, *head, *baseSHA, sub.At)
	recs := merge.NewRecords(*f.lane, st.LaneBranch, "origin", merge.NewGit(*f.lane, f.dur(), deps.Runner), f.dur())
	merge.Appendf(*f.lane, deps.Now(), "GATE entry=%s head=%s base=%s merge=%s verdict=%s file=%s", id, *head, *baseSHA, *mergeSHA, *verdict, file)
	pushErr := recs.Deliver(sub, []merge.Item{
		{Path: file, Body: body},
		{Path: merge.SummaryFile(file), Body: summaryBytes},
	})
	if pushErr != nil {
		fmt.Fprintf(stderr, "GATE FAIL entry=%s head=%s base=%s merge=%s file=%s pushed=false: %s; re-run the same verb to push it\n",
			oneline.Field(id), oneline.Field(merge.Short(*head)), oneline.Field(merge.Short(*baseSHA)),
			oneline.Field(merge.Short(*mergeSHA)), oneline.Field(file),
			oneline.Escape(oneline.Cap(pushErr.Error(), oneline.TailBytes)))
		return 1
	}
	foldInto(*f.lane, st, recs, f.dur())
	fmt.Fprintf(stdout, "GATE OK entry=%s head=%s base=%s merge=%s verdict=%s summary=%s in_lane=%t newest=%t file=%s pushed=true\n",
		oneline.Field(id), oneline.Field(merge.Short(*head)), oneline.Field(merge.Short(*baseSHA)),
		oneline.Field(merge.Short(*mergeSHA)), oneline.Field(*verdict), oneline.Field(abs), inLane, newest, oneline.Field(file))
	return 0
}

// isNewest says whether an even newer record for the pair already exists, so GATE OK can
// say newest=false rather than leave a caller believing their record decides.
func isNewest(st *merge.State, id, head, base, at string) bool {
	for _, g := range st.Gates {
		if g.ID() == id && g.Head == head && g.Base == base && g.At > at {
			return false
		}
	}
	return true
}

// foldInto pulls the lane branch and folds every record file into the state, under the
// state lock. The lists are the fold and the files are the truth.
func foldInto(lane string, st *merge.State, recs *merge.Records, timeout time.Duration) (int, []merge.FoldProblem) {
	pulled, _ := recs.Pull()
	folded, err := recs.Fold()
	if err != nil || folded == nil {
		return pulled, nil
	}
	_ = merge.Update(lane, merge.LockWait, func(s *merge.State) error {
		s.Apply(folded)
		st.PRs, st.Branches, st.Gates = s.PRs, s.Branches, s.Gates
		return nil
	})
	if fresh, err := merge.Load(lane); err == nil {
		*st = *fresh
	}
	return pulled, folded.Problems
}
