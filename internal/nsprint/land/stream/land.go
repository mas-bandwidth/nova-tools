package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// Refusal is a precondition the verb will not act without (exit 2).
type Refusal struct{ Why, Remedy string }

func (r *Refusal) Error() string { return "REFUSED " + r.Why + " remedy=" + r.Remedy }

// Client is the Redis the verbs need: commands, pipelines and EVAL.
type Client interface {
	redis.Cmdable
	redis.Scripter
}

// Options is one land stream run.
type Options struct {
	Repo        string
	Streams     []string
	Base        string
	Branch      string // default stream/<slug>, or land/<slug> for several streams
	DryRun      bool
	Remote      string // default cfg:land remote:<repo>, else git@github.com:<repo>.git
	Mirror      string
	Workdir     string
	Test        string // default cfg:land:test:<repo>
	TestTimeout time.Duration
	MinScore    int // < 0: cfg:land, else 8
	By          string
	Author      string
	Log         io.Writer
	GH          *GitHub
	// NoTest skips the batch test (and cfg:land:test): the stream head is
	// tested by the CI request nova-sprint land makes (nova-tools#3899).
	NoTest bool
	// ParkConflicts parks a conflicting member and lands the rest (the land
	// duty, nova-tools #3898); the default stops the build at it.
	ParkConflicts bool
}

// Report is what one land stream run did.
type Report struct {
	Slug, Branch, State string
	MinScore            int
	Members             []Member
	Skips               []Skip
	Build               Result
	PR                  int
	Reused              bool
	ParkedMoved         int
	Workdir             string
	TestCmd             string
}

// DefaultBranch is the stream branch for the slug.
func DefaultBranch(slug string, streams int) string {
	if streams > 1 {
		return "land/" + slug
	}
	return "stream/" + slug
}

// LandStream lists the members (dry run), or builds, tests, bisects, pushes
// the stream branch, opens ONE stream PR and records the landing.
func LandStream(ctx context.Context, c Client, o Options) (Report, error) {
	var rep Report
	slug, err := Slug(o.Streams...)
	if err != nil {
		return rep, &Refusal{Why: err.Error(), Remedy: "--stream <name as in ws:names>"}
	}
	rep.Slug = slug
	rep.Branch = o.Branch
	if rep.Branch == "" {
		rep.Branch = DefaultBranch(slug, len(o.Streams))
	}
	cfg, err := LoadConfig(ctx, c, o.Repo)
	if err != nil {
		return rep, err
	}
	rep.MinScore = cfg.MinScore
	if o.MinScore >= 0 {
		rep.MinScore = o.MinScore
	}
	rep.Members, rep.Skips, err = Members(ctx, c, o.Repo, o.Streams, rep.MinScore)
	if err != nil {
		return rep, err
	}
	test := o.Test
	if test == "" {
		test = cfg.Test
	}
	if o.NoTest {
		test = CIBatch
	}
	rep.TestCmd = test
	if o.DryRun {
		rep.State = "dry-run"
		return rep, nil
	}
	if len(rep.Members) == 0 {
		rep.State = "empty"
		return rep, nil
	}
	prev, hadPrev, err := LoadLanding(ctx, c, o.Repo, slug)
	if err != nil {
		return rep, err
	}
	if hadPrev && prev.State == "open" && prev.PR > 0 && prev.Branch != rep.Branch {
		return rep, &Refusal{Why: fmt.Sprintf("land:%s:%s is open as #%d on %s", o.Repo, slug, prev.PR, prev.Branch),
			Remedy: "land merge it, or pass --branch " + prev.Branch}
	}
	if o.GH == nil {
		return rep, &Refusal{Why: "no GitHub client", Remedy: "set GH_TOKEN"}
	}
	remote := o.Remote
	if remote == "" {
		remote = cfg.Remote
	}
	if remote == "" {
		remote = "git@github.com:" + o.Repo + ".git"
	}
	rep.Workdir = o.Workdir
	b := Build{Repo: o.Repo, Remote: remote, Mirror: o.Mirror, Base: o.Base, Branch: rep.Branch, Workdir: o.Workdir,
		Test: test, TestTimeout: o.TestTimeout, NoTest: o.NoTest, Author: o.Author, Log: o.Log, ParkConflicts: o.ParkConflicts}
	res, err := b.Run(ctx, rep.Members)
	rep.Build = res
	rep.TestCmd = res.TestCmd
	if err != nil {
		return rep, err
	}
	for _, m := range res.Moved {
		rep.Skips = append(rep.Skips, Skip{Task: m.Task, N: m.N, Why: m.Why})
	}
	l := Landing{Repo: o.Repo, Slug: slug, Streams: strings.Join(o.Streams, ","), Base: o.Base, BaseSHA: res.BaseSHA,
		Branch: rep.Branch, Head: res.Head, Members: res.Kept, Parked: res.Parked, Tests: res.Tests, Workdir: o.Workdir}
	if len(res.Kept) > 0 && res.Conflict == nil && !res.BaseRed {
		// The issues each kept member's commits close, while the clone is
		// here: land merge closes them with the ones its body closes.
		l.CommitCloses = b.CommitCloses(ctx, res.BaseSHA, res.Kept)
	}
	switch {
	case res.Conflict != nil:
		// Stop: Rowan resolves in the kept workdir. Nothing moves.
		l.State = "conflict"
		l.Parked = append(l.Parked, Parked{Member: Member{N: res.Conflict.Member.N}, Why: "conflict:" + strings.Join(res.Conflict.Files, ",")})
		rep.State = l.State
		_, err := SaveBuilt(ctx, c, l, o.By)
		return rep, err
	case res.BaseRed:
		l.State = "base-red"
		rep.State = l.State
		_, err := SaveBuilt(ctx, c, l, o.By)
		return rep, err
	case len(res.Kept) == 0:
		l.State = "empty"
		rep.State = l.State
		rep.ParkedMoved, err = SaveBuilt(ctx, c, l, o.By)
		removeWorkdir(o.Workdir)
		return rep, err
	}
	if err := b.Push(ctx); err != nil {
		return rep, err
	}
	if hadPrev && prev.State == "open" && prev.PR > 0 {
		rep.PR, rep.Reused = prev.PR, true
	} else {
		title := fmt.Sprintf("stream %s: %d members into %s", strings.Join(o.Streams, " + "), len(res.Kept), o.Base)
		n, err := o.GH.OpenPR(ctx, o.Repo, rep.Branch, o.Base, title, PRBody(l, test))
		if err != nil {
			l.State = "pushed"
			rep.State = l.State
			rep.ParkedMoved, _ = SaveBuilt(ctx, c, l, o.By)
			return rep, err
		}
		rep.PR = n
	}
	l.PR = rep.PR
	l.State = "open"
	rep.State = l.State
	rep.ParkedMoved, err = SaveBuilt(ctx, c, l, o.By)
	if err == nil {
		removeWorkdir(o.Workdir)
	}
	return rep, err
}

// removeWorkdir deletes the scratch clone under its own parent (never the
// parent, never through a symlink): job storage is deleted at the end.
func removeWorkdir(dir string) {
	if dir != "" {
		_ = safepath.RemoveUnder(filepath.Dir(dir), dir)
	}
}

// PRBody is the stream PR body: STREAM first, the members with heads and
// reads, the parked list, the batch test. It closes nothing: the members
// close with their CLOSE line when this merges.
func PRBody(l Landing, test string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "STREAM: %s\n", l.Streams)
	fmt.Fprintf(&b, "BASE: %s\nbase-sha: %s\nDEPENDS-ON: none\n", l.Base, l.BaseSHA)
	b.WriteString("WHY: the members of one work stream, merged --no-ff oldest first (pr_ready_at) onto this branch off the base tip and tested once as a batch.\n\n")
	b.WriteString("Members (oldest first):\n")
	for _, m := range l.Members {
		fmt.Fprintf(&b, "- #%d at %s (task %s, read %s %d)\n", m.N, m.Head, m.Task, m.Who, m.Score)
	}
	if len(l.Parked) > 0 {
		b.WriteString("\nParked:\n")
		for _, p := range l.Parked {
			fmt.Fprintf(&b, "- #%d %s\n", p.N, p.Why)
		}
	}
	if test == CIBatch {
		fmt.Fprintf(&b, "\nBatch test: our own CI at %s (a CI request a bench claims; none ran on the lander's seat).\n", short(l.Head))
	} else {
		fmt.Fprintf(&b, "\nBatch test: `%s` green at %s (%d runs).\n", test, short(l.Head), l.Tests)
	}
	b.WriteString("\nOpened by nova-sprint land stream. Each member closes with a CLOSE line when this merges (nova-sprint land merge).\n")
	return b.String()
}

// Status is one landing with its stream PR record.
type Status struct {
	Landing
	CI, Mergeable string
	HeadMatch     bool
}

// LoadStatus reads every landing of the repo and each stream PR's record:
// three round trips, no GitHub.
func LoadStatus(ctx context.Context, c Client, repo string) ([]Status, error) {
	ls, err := LoadLandings(ctx, c, repo)
	if err != nil {
		return nil, err
	}
	var ns []int
	for _, l := range ls {
		if l.PR > 0 {
			ns = append(ns, l.PR)
		}
	}
	recs, err := LoadPRs(ctx, c, repo, ns)
	if err != nil {
		return nil, err
	}
	byN := map[int]PR{}
	for _, r := range recs {
		byN[r.N] = r
	}
	out := make([]Status, 0, len(ls))
	for _, l := range ls {
		s := Status{Landing: l, CI: "-", Mergeable: "-"}
		if r, ok := byN[l.PR]; ok && r.Exists {
			s.CI, s.Mergeable = orDash(r.CI), orDash(r.Mergeable)
			s.HeadMatch = r.Head == l.Head
		}
		out = append(out, s)
	}
	return out, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// MergeOptions is one land merge run.
type MergeOptions struct {
	Repo    string
	Streams []string
	By      string
	GH      *GitHub
}

// MergeReport is what one land merge did.
type MergeReport struct {
	Landing  Landing
	MergeSHA string
	Moved    int
	Missing  int
	Lines    int
	Skipped  []string
	Unread   []int // members whose closes stayed unknown (no record, no read)
	Already  bool
	Closed   []int
	Unclosed []int
	// IssuesClosed and IssuesUnclosed are the issues the members' bodies
	// close that the lander closed by REST, or could not (budget, error).
	IssuesClosed   []int
	IssuesUnclosed []int
	// Release is the fleet:release version this merge wrote (a landing of
	// nova-tools into dev, #4050), "" when it wrote none.
	Release string
}

// Merge merges the stream PR when its record says ci=green and mergeable at
// the landing's head and marks the landing merged, writing fleet:release
// version and commit in the same call when it lands nova-tools into dev
// (#4050: the reconciler's deploy duty takes it from there); then, per member in one
// fenced Lua call each, writes its CLOSE line on its record and moves every
// task naming it or an issue it closes to landed (nova-tools#3779); then
// closes the issues each member's body closes (GitHub does not on a merge
// into dev) and comments on and closes each member (budgeted; a re-run
// closes the rest and repeats the per-member step, which moves nothing
// twice).
func Merge(ctx context.Context, c Client, o MergeOptions) (MergeReport, error) {
	var rep MergeReport
	slug, err := Slug(o.Streams...)
	if err != nil {
		return rep, &Refusal{Why: err.Error(), Remedy: "--stream <name as in ws:names>"}
	}
	l, ok, err := LoadLanding(ctx, c, o.Repo, slug)
	if err != nil {
		return rep, err
	}
	if !ok {
		return rep, &Refusal{Why: "no landing " + LandKey(o.Repo, slug), Remedy: "nova-sprint land stream --repo " + o.Repo + " --stream <s>"}
	}
	rep.Landing = l
	// The member step is a library function: refuse before GitHub merges
	// anything when the store's library predates it.
	if err := c.FCall(ctx, FunctionLandMember, nil).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return rep, &Refusal{Why: FunctionLandMember + ": " + err.Error(), Remedy: "nova-sprint fn load"}
	}
	if l.State == "merged" {
		rep.Already, rep.MergeSHA = true, l.MergeSHA
	} else {
		if l.State != "open" || l.PR <= 0 {
			return rep, &Refusal{Why: fmt.Sprintf("landing %s is %s with no open stream PR", slug, l.State), Remedy: "nova-sprint land stream first"}
		}
		recs, err := LoadPRs(ctx, c, o.Repo, []int{l.PR})
		if err != nil {
			return rep, err
		}
		r := recs[0]
		prCmd := fmt.Sprintf("nova-sprint pr record --repo %s --n %d", o.Repo, l.PR)
		switch {
		case !r.Exists:
			return rep, &Refusal{Why: "no record " + PRKey(o.Repo, l.PR), Remedy: prCmd + " --head " + l.Head + " --base " + l.Base + " --stream <s>"}
		case r.Head != l.Head:
			return rep, &Refusal{Why: fmt.Sprintf("record head %s is not the stream head %s", short(r.Head), short(l.Head)), Remedy: "re-run land stream, or " + prCmd + " --head <sha>"}
		case r.CI != "green":
			return rep, &Refusal{Why: fmt.Sprintf("ci=%s on %s#%d at %s, not green", orDash(r.CI), o.Repo, l.PR, short(l.Head)), Remedy: prCmd + " --ci green once CI passes"}
		case !r.MergeableOK():
			return rep, &Refusal{Why: fmt.Sprintf("mergeable=%s on %s#%d", orDash(r.Mergeable), o.Repo, l.PR), Remedy: prCmd + " --mergeable true"}
		}
		if o.GH == nil {
			return rep, &Refusal{Why: "no GitHub client", Remedy: "set GH_TOKEN"}
		}
		title := fmt.Sprintf("Merge stream %s (#%d): %d members", l.Streams, l.PR, len(l.Members))
		sha, err := o.GH.MergePR(ctx, o.Repo, l.PR, l.Head, title)
		if err != nil {
			return rep, err
		}
		rep.MergeSHA = sha
		rep.Already, rep.Release, err = saveLanded(ctx, c, l, o.By, sha)
		if err != nil {
			return rep, err
		}
	}
	var ns []int
	for _, m := range l.Members {
		ns = append(ns, m.N)
	}
	recs, err := LoadPRs(ctx, c, o.Repo, ns)
	if err != nil {
		return rep, err
	}
	// The issues each member closes: its commit messages' (recorded by land
	// stream) and its body's: the record's closes, else the body (one REST
	// read; a failed read leaves them unknown and a re-run reads again).
	closes := map[int]string{}
	for _, r := range recs {
		if !r.Exists {
			continue
		}
		body := r.Closes
		if body == "" && o.GH != nil {
			if b, err := o.GH.PRBody(ctx, o.Repo, r.N); err == nil {
				body = ParseCloses(b)
			}
		}
		if body == "" {
			// Unknown until a run reads it: nothing is recorded for it.
			rep.Unread = append(rep.Unread, r.N)
			continue
		}
		if u := UnionCloses(body, r.CommitCloses); u != r.Closes {
			closes[r.N] = u
		}
	}
	landed, err := LandMembers(ctx, c, l, o.By, rep.MergeSHA, closes)
	if err != nil {
		return rep, err
	}
	rep.Moved, rep.Missing, rep.Lines, rep.Skipped = landed.Moved, landed.Missing, landed.Lines, landed.Skipped
	// Close the issues each member closes: GitHub does not on a merge into
	// dev (not the default branch). One comment naming the merge sha, then
	// state=closed; an issue closed on an earlier run is not closed again.
	var closeErr error
	issuesDone := map[int][]int{}
	for i, r := range recs {
		cl := closes[r.N]
		if cl == "" {
			cl = r.Closes
		}
		already := map[string]bool{}
		for _, x := range strings.Fields(r.IssuesClosed) {
			already[x] = true
		}
		for _, x := range strings.Fields(cl) {
			issue, err := strconv.Atoi(x)
			if err != nil || issue <= 0 || already[x] {
				continue
			}
			if closeErr != nil || o.GH == nil {
				rep.IssuesUnclosed = append(rep.IssuesUnclosed, issue)
				continue
			}
			line := IssueCloseLine(l.Members[i].Head, l.Branch, l.Head, o.Repo, l.PR, rep.MergeSHA, r.N)
			if _, err := o.GH.Comment(ctx, o.Repo, issue, line); err != nil {
				closeErr = err
				rep.IssuesUnclosed = append(rep.IssuesUnclosed, issue)
				continue
			}
			if err := o.GH.CloseIssue(ctx, o.Repo, issue); err != nil {
				closeErr = err
				rep.IssuesUnclosed = append(rep.IssuesUnclosed, issue)
				continue
			}
			rep.IssuesClosed = append(rep.IssuesClosed, issue)
			issuesDone[r.N] = append(issuesDone[r.N], issue)
		}
	}
	if err := MarkIssuesClosed(ctx, c, o.Repo, issuesDone, recs); err != nil {
		return rep, err
	}
	// Close the members: the CLOSE line as the comment, then state=closed,
	// each budgeted.
	for i, r := range recs {
		if r.ClosedAt != "" {
			continue
		}
		if closeErr != nil || o.GH == nil {
			rep.Unclosed = append(rep.Unclosed, r.N)
			continue
		}
		line := CloseLine(l.Members[i].Head, l.Branch, l.Head, o.Repo, l.PR, rep.MergeSHA)
		if _, err := o.GH.Comment(ctx, o.Repo, r.N, line); err != nil {
			closeErr = err
			rep.Unclosed = append(rep.Unclosed, r.N)
			continue
		}
		if err := o.GH.Close(ctx, o.Repo, r.N); err != nil {
			closeErr = err
			rep.Unclosed = append(rep.Unclosed, r.N)
			continue
		}
		rep.Closed = append(rep.Closed, r.N)
	}
	if err := MarkClosed(ctx, c, o.Repo, rep.Closed); err != nil {
		return rep, err
	}
	if closeErr != nil && !errors.Is(closeErr, ErrBudget) {
		return rep, closeErr
	}
	return rep, nil
}
