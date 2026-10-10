package friend

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The daemon stages every job it holds (the finding of 2026-10-05: the first lanes of the
// rocketnet audit held with "no worktree, no remote" and the schema cards with "JOB.md
// missing: card not staged", and the coordinator staged clones and JOB.md files by hand all
// night). Writing a brief without staging its job is half a delivery: for each held work
// card whose brief is in her inbox, the daemon adds a git worktree of REPO at BASE on the
// card's branch at jobs/<job>/repo, of the one full bare mirror of the repository it keeps
// under mirrors/ (so a stage is a fetch and a worktree add, seconds and megabytes: on
// 2026-10-05 a whole clone per job had her disk at 99% with 42 staged clones and 765 finished
// job dirs), and writes jobs/<job>/JOB.md in the card-contract shape
// (docs/SPEC-CARD-CONTRACT.md). After each inbox cleanup the finished jobs' worktrees past
// FinishedJobsKept are pruned (Stager.Prune, pruneStep). No lane is handed the card until JOB.md is there. A
// repository her account cannot reach is one judgment to the coordinator with the remedy,
// never a lane that discovers it (docs/SPEC-FRIEND.md, staging). The machine is modelled in
// tla/FriendStage.tla (MCFriendStage*: a lane handed only a staged card, one judgment while it
// stands, a failed job staged again), and the worktrees and their pruning in
// internal/friend/tla/JobWorktrees.tla (MCJobWorktrees*: a live job never pruned, a branch's
// work never lost, the finished worktrees within the cap after a prune). A rework whose fix
// names no new files starts in the last attempt's worktree, moved into its job (Stager.keep),
// and the last worktree of a card is kept by every prune until it lands (ReworkKeptMax): the
// same module, its rework half (MCJobWorktrees: LastKept, KeptWhenItMay, KeptOnlyWhenFixable).

// The directories under her working directory that staging writes.
const (
	JobsDir    = "jobs"
	MirrorsDir = "mirrors"
	JobFile    = "JOB.md"
)

// StageRetryEvery is how long a job whose stage failed waits before it is staged again; a
// judgment stands, said once, until a stage of its repository or card succeeds.
// MirrorFreshFor is how long a fetched mirror serves stages without fetching again, so a
// burst of cards on one repository is one fetch. MirrorCloneBudget bounds a mirror's fetch:
// the first, of the whole repository, is the one slow step (every later one fetches only what
// is new).
const (
	StageRetryEvery   = time.Minute
	MirrorFreshFor    = 10 * time.Second
	MirrorCloneBudget = 30 * time.Minute
)

// Packet is what a held work card says about its checkout: the repository (owner/name), the
// base it starts at (a branch, a tag or a full sha) and its pin (BaseSha, the commit a
// `BASE: <ref>@<sha40>` names; "" when unpinned), the branch its work is pushed to, and its
// attempt. A rework's packet says too what its brief carries of the attempt before: its fix
// (the brief's `The coordinator asks:` line), the files the fix names outside the card's
// PATHS (NewFiles), and the head the last attempt pushed (Carry, of attempt CarryAttempt; ""
// when none pushed).
type Packet struct {
	Card, Job, Repo, Base, BaseSha, Branch string
	Attempt                                int
	Fix, Carry                             string
	CarryAttempt                           int
	NewFiles                               []string
}

var (
	statusBranchRE  = regexp.MustCompile(`push your work to the branch ([^\s;]+);`)
	statusAttemptRE = regexp.MustCompile(`, attempt (\d+);`)
	repoRE          = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*/[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	refRE           = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./~-]*$`)
	shaRE           = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// the head a rework carries: friend sync's start line (nova-sprint friendStart), else a
	// recut twin's CARRY: line (member.CarryLine)
	carryStartRE = regexp.MustCompile(`Carry the work of attempt (\d+) onto it yourself: its head, ([0-9a-f]{40}),`)
	carryLineRE  = regexp.MustCompile(`(?m)^CARRY: \S+ attempt (\d+) head=([0-9a-f]{40})\s*$`)
)

// FixKey begins the line of a friend's brief that carries a rework's fix (nova-sprint
// friendBrief: `The coordinator asks: <fix>`).
const FixKey = "The coordinator asks: "

// PacketOf is the packet of a held card, the server's fields first and the brief's lines
// (its STATUS line, REPO: and BASE:) for any it did not send. It answers false for a card that
// stages nothing here: a read, or a work card whose brief names no REPO.
func PacketOf(h HeldCard) (Packet, bool) {
	if (h.Kind != "" && h.Kind != "work") || !strings.HasPrefix(h.Brief, "STATUS: nova-sprint card ") {
		return Packet{}, false
	}
	p := Packet{Card: h.Card, Job: h.Job, Repo: h.Repo, Base: h.Base, Branch: h.Branch, Attempt: h.Attempt}
	status, _, _ := strings.Cut(h.Brief, "\n")
	if m := statusBranchRE.FindStringSubmatch(status); p.Branch == "" && m != nil {
		p.Branch = m[1]
	}
	if m := statusAttemptRE.FindStringSubmatch(status); p.Attempt == 0 && m != nil {
		p.Attempt, _ = strconv.Atoi(m[1]) // ignored: the pattern is digits
	}
	if v, ok := cardhdr.Value(h.Brief, "REPO"); ok && p.Repo == "" {
		p.Repo = v
	}
	if v, ok := cardhdr.Value(h.Brief, "BASE"); ok && p.Base == "" {
		p.Base = v
	}
	// a pinned base (<ref>@<sha40>, as card trees write it) is its ref and its pin; one that
	// does not read stays as written, for check to refuse
	if ref, sha, ok := cardhdr.ParseBase(p.Base); ok && p.BaseSha == "" {
		p.Base, p.BaseSha = ref, sha
	}
	// a rework's fix is a line of the brief's head, before the card's own text
	head, _, _ := strings.Cut(h.Brief, "\n\n")
	for _, l := range strings.Split(head, "\n") {
		if fix, ok := strings.CutPrefix(l, FixKey); ok && strings.TrimSpace(fix) != "" {
			p.Fix = strings.TrimSpace(fix)
			p.NewFiles = sprint.FilesOutsidePaths(h.Brief, p.Fix)
			break
		}
	}
	m := carryStartRE.FindStringSubmatch(h.Brief)
	if m == nil {
		m = carryLineRE.FindStringSubmatch(h.Brief)
	}
	if m != nil {
		p.CarryAttempt, _ = strconv.Atoi(m[1]) // ignored: the pattern is digits
		p.Carry = m[2]
	}
	return p, p.Repo != ""
}

// check refuses a packet whose values could be read by git as anything but what they name.
func (p Packet) check() error {
	switch {
	case !validJob(p.Job):
		return fmt.Errorf("its job %q is no jobs directory", p.Job)
	case !repoRE.MatchString(p.Repo) || strings.Contains(p.Repo, ".."):
		return fmt.Errorf("its REPO %q is no owner/name", p.Repo)
	case !validRef(p.Base):
		return fmt.Errorf("its BASE %q is no branch, tag or sha", p.Base)
	case p.BaseSha != "" && !shaRE.MatchString(p.BaseSha):
		return fmt.Errorf("its BASE pin %q is no full sha", p.BaseSha)
	case !validRef(p.Branch):
		return fmt.Errorf("its branch %q is no branch name", p.Branch)
	case p.Carry != "" && !shaRE.MatchString(p.Carry):
		return fmt.Errorf("its carried head %q is no full sha", p.Carry)
	}
	return nil
}

func validRef(r string) bool {
	return len(r) <= 200 && refRE.MatchString(r) && !strings.Contains(r, "..") && !strings.Contains(r, "//") &&
		!strings.HasSuffix(r, "/") && !strings.HasSuffix(r, ".lock") && !strings.HasSuffix(r, ".")
}

// NotStageable is a stage that waits on a person: her account cannot reach the repository
// (Card empty: every card on it waits), or the card's packet names what is not there. It is
// one judgment to the coordinator, its Remedy what clears it.
type NotStageable struct {
	Repo, Card, Why, Remedy string
	Job                     string // the job it was staging, when there was one
}

func (n *NotStageable) Error() string {
	if n.Card == "" {
		if n.Job != "" {
			return fmt.Sprintf("her account cannot reach %s (staging %s/%s): %s", n.Repo, JobsDir, n.Job, n.Why)
		}
		return fmt.Sprintf("her account cannot reach %s: %s", n.Repo, n.Why)
	}
	return fmt.Sprintf("card %s cannot be staged from %s: %s", n.Card, n.Repo, n.Why)
}

// key is what a judgment stands on: the repository, or the card.
func (n *NotStageable) key() string {
	if n.Card == "" {
		return "repo " + n.Repo
	}
	return "card " + n.Card
}

// StageJudgmentText is what the coordinator is told of a stage that waits on a person: one
// judgment, with its remedy.
func StageJudgmentText(friend string, n *NotStageable) (subject, body string) {
	if n.Card == "" {
		subject = fmt.Sprintf("judgment: %s cannot reach %s", friend, n.Repo)
	} else {
		subject = fmt.Sprintf("judgment: card %s cannot be staged on %s", n.Card, friend)
	}
	body = fmt.Sprintf("%s. The daemon staged no checkout and wrote no %s/<job>/%s, so no lane is handed the card(s); it tries again once a %s, and says this once until a stage succeeds. The remedy: %s\n",
		n.Error(), JobsDir, JobFile, StageRetryEvery, n.Remedy)
	return subject, body
}

// GitHubURL is the clone URL of a repository named owner/name.
func GitHubURL(repo string) string { return "https://github.com/" + repo + ".git" }

// Stager stages jobs under Dir, her working directory, with the git credentials of the
// process that runs it (the daemon's: her account's). URL names a repository's remote (nil:
// GitHubURL); Env is git's whole environment (nil: the daemon's own, with
// GIT_TERMINAL_PROMPT=0, so git never waits on a prompt no one answers).
type Stager struct {
	Dir string
	URL func(repo string) string
	Env []string

	mu      sync.Mutex
	repos   map[string]*sync.Mutex
	fetched map[string]time.Time
}

func (s *Stager) url(repo string) string {
	if s.URL != nil {
		return s.URL(repo)
	}
	return GitHubURL(repo)
}

func (s *Stager) env() []string {
	if s.Env != nil {
		return s.Env
	}
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
}

func (s *Stager) git(ctx context.Context, budget time.Duration, args ...string) (string, error) {
	return gitrun.Output(ctx, gitrun.Options{Env: s.env(), OwnRepo: true, Timeout: budget}, args...)
}

// repoLock is the lock of one repository's mirror: its fetch, the worktrees added to it and
// the ones pruned from it run one at a time.
func (s *Stager) repoLock(repo string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.repos == nil {
		s.repos, s.fetched = map[string]*sync.Mutex{}, map[string]time.Time{}
	}
	if s.repos[repo] == nil {
		s.repos[repo] = &sync.Mutex{}
	}
	return s.repos[repo]
}

// JobDir is where a job is staged, under her working directory.
func JobDir(dir, job string) string { return filepath.Join(dir, JobsDir, job) }

// Staged says a job's JOB.md is there.
func Staged(dir, job string) bool {
	_, err := os.Lstat(filepath.Join(JobDir(dir, job), JobFile))
	return err == nil
}

// Stage stages p's job: the mirror of its repository fetched (cloned the first time), a git
// worktree of it at the base on the card's branch at jobs/<job>/repo, whose origin is the
// repository itself (the mirror's origin), and jobs/<job>/JOB.md. It answers the commit the
// checkout is at. A job already staged is left as it is; a checkout is never half there (the
// worktree is added beside, under jobs/<job>/.staging, and moved in whole), and JOB.md is
// written last, so a lane never meets a checkout not ready. A branch the mirror already holds
// (a pruned job's, staged again) is checked out as it stands, never reset to the base.
//
// A rework starts in the last worktree when it may (keep): the previous attempt's checkout on
// this friend, moved whole into the job on the card's new branch, so the lane edits and pushes
// without re-learning the tree. When it may not (the tree is gone, the friend differs, or a
// lane is still in that checkout), its branch starts at the head the last attempt pushed
// (Carry) when the mirror holds it, else at the base.
func (s *Stager) Stage(ctx context.Context, p Packet) (string, error) {
	if err := p.check(); err != nil {
		return "", &NotStageable{Repo: p.Repo, Card: p.Card, Job: p.Job, Why: err.Error(), Remedy: "rework the card with a packet that names its repository (owner/name), its base and its branch"}
	}
	job := JobDir(s.Dir, p.Job)
	checkout := filepath.Join(job, "repo")
	if Staged(s.Dir, p.Job) {
		return "", nil
	}
	if _, err := os.Lstat(checkout); err == nil {
		// a stage that ended after the checkout was in place and before JOB.md: it is the card's
		// when it is on its branch. A tree moved in from the attempt before (KEPT names that job)
		// is written as the kept job, so the fix stays the first line.
		branch, err := s.git(ctx, 0, "-C", checkout, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil || branch != p.Branch {
			return "", fmt.Errorf("%s/%s/repo is there with no %s and is not on %s (%q); it is left as found", JobsDir, p.Job, JobFile, p.Branch, branch)
		}
		sha, err := s.git(ctx, 0, "-C", checkout, "rev-parse", "HEAD")
		if err != nil {
			return "", err
		}
		if prev, ok := readKeptFrom(job); ok && p.Fix != "" && len(p.NewFiles) == 0 {
			return sha, s.writeJobText(p, KeptJobText(s.Dir, p, prev, sha))
		}
		return sha, s.writeJob(p, sha)
	}
	lock := s.repoLock(p.Repo)
	lock.Lock()
	defer lock.Unlock()
	sha, prev, kept, err := s.keep(ctx, p)
	if err != nil {
		return "", err
	}
	if kept {
		return sha, s.writeJobText(p, KeptJobText(s.Dir, p, prev, sha))
	}
	mirror, err := s.mirror(ctx, p.Repo)
	var ns *NotStageable
	if errors.As(err, &ns) {
		ns.Job = p.Job
	}
	if err != nil {
		return "", err
	}
	sha, err = s.base(ctx, mirror, p)
	if err != nil {
		return "", err
	}
	scratch := filepath.Join(job, stageScratch)
	tmp := filepath.Join(scratch, p.Job) // the worktree's name in the mirror is the job's
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return "", err
	}
	s.dropScratch(ctx, mirror, job, scratch) // a stage that ended part way: its own scratch, never the checkout
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return "", err
	}
	_, err = s.git(ctx, 0, "-C", mirror, "rev-parse", "--verify", "--quiet", "--end-of-options", "refs/heads/"+p.Branch)
	created := err != nil
	if !created || !s.holds(ctx, mirror, p) {
		p.Carry = "" // the branch as it stands, or the base: JobText names which
	} else {
		sha = p.Carry
	}
	add := []string{"-C", mirror, "worktree", "add", "--quiet", tmp, p.Branch}
	if created {
		add = []string{"-C", mirror, "worktree", "add", "--quiet", "-b", p.Branch, tmp, sha}
	}
	for _, argv := range [][]string{add, {"-C", mirror, "worktree", "move", tmp, checkout}} {
		if _, err := s.git(ctx, 0, argv...); err != nil {
			s.dropScratch(ctx, mirror, job, scratch)
			if created {
				_, _ = s.git(ctx, 0, "-C", mirror, "branch", "--quiet", "-D", p.Branch) // ignored: at the base, nothing on it; the next stage creates it again or takes it as it stands
			}
			return "", err
		}
	}
	_ = safepath.RemoveUnder(job, scratch) // ignored: an empty directory the next stage removes first
	head, err := s.git(ctx, 0, "-C", checkout, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return head, s.writeJob(p, head)
}

// holds says the mirror holds the head p carries, fetched once more when it does not: a
// mirror fetched within MirrorFreshFor may predate the last attempt's push.
func (s *Stager) holds(ctx context.Context, mirror string, p Packet) bool {
	if p.Carry == "" {
		return false
	}
	has := func() bool {
		c, err := s.git(ctx, 0, "-C", mirror, "rev-parse", "--verify", "--quiet", "--end-of-options", p.Carry+"^{commit}")
		return err == nil && c == p.Carry
	}
	if has() {
		return true
	}
	if _, err := s.git(ctx, MirrorCloneBudget, "-C", mirror, "fetch", "--quiet", "--prune", "origin"); err != nil {
		return false
	}
	s.mu.Lock()
	s.fetched[p.Repo] = time.Now()
	s.mu.Unlock()
	return has()
}

// keep is a rework started in the last worktree, its mirror's lock held (tla/JobWorktrees.tla
// KeepStage: KeptWhenItMay, KeptOnlyWhenFixable). For a later attempt whose fix names no file
// outside the card's PATHS, the checkout of the attempt before it on this friend (the newest
// job of <primary>.w<n-1>) is moved whole into this job with git worktree move (never a rename
// or a copy: the mirror's record of the tree moves with it) and put on the card's new branch at
// its own HEAD, when that attempt is over: its outbox REPORT.md is there, its brief is retired
// from her inbox, and no lane is still in that checkout (laneInCheckout). A report and a
// retired brief do not say the lane has left. The checkout is a worktree of this repository's
// mirror, no tracked file has an uncommitted change, it holds the carried head, and the mirror
// holds no branch of the card's name yet. It answers the commit the checkout is at and the job
// it came from. ok is false when any of that is not so, and Stage stages afresh, the old tree
// untouched. err is set only when the tree was moved and could not be put back. The prune
// keeps the last worktree of a card (Prune, ReworkKeptMax), so it is there for the rework.
func (s *Stager) keep(ctx context.Context, p Packet) (sha, prev string, ok bool, err error) {
	refuse := func() (string, string, bool, error) { return "", "", false, nil }
	if p.Attempt < 2 || p.Fix == "" || len(p.NewFiles) > 0 {
		return refuse()
	}
	prev = lastJob(s.Dir, PreviousCard(p.Card, p.Attempt))
	if prev == "" || !exists(filepath.Join(s.Dir, "outbox", prev, "REPORT.md")) || exists(filepath.Join(s.Dir, "inbox", prev)) || laneInCheckout(ctx, s.Dir, prev, time.Now()) {
		return refuse()
	}
	kept := filepath.Join(JobDir(s.Dir, prev), "repo")
	repo, mirror, isTree := s.worktreeOf(kept)
	if !isTree || repo != p.Repo {
		return refuse()
	}
	if dirty, derr := s.git(ctx, 0, "-C", kept, "status", "--porcelain", "--untracked-files=no"); derr != nil || dirty != "" {
		return refuse()
	}
	if p.Carry != "" {
		if _, derr := s.git(ctx, 0, "-C", kept, "merge-base", "--is-ancestor", p.Carry, "HEAD"); derr != nil {
			return refuse() // the tree is not where the last attempt's pushed work is
		}
	}
	if _, derr := s.git(ctx, 0, "-C", mirror, "rev-parse", "--verify", "--quiet", "--end-of-options", "refs/heads/"+p.Branch); derr == nil {
		return refuse() // the branch as it stands is the card's: Stage checks it out
	}
	sha, err = s.git(ctx, 0, "-C", kept, "rev-parse", "HEAD")
	if err != nil || !shaRE.MatchString(sha) {
		return refuse()
	}
	oldBranch, berr := s.git(ctx, 0, "-C", kept, "rev-parse", "--abbrev-ref", "HEAD")
	if berr != nil || oldBranch == "" || oldBranch == "HEAD" {
		return refuse()
	}
	job := JobDir(s.Dir, p.Job)
	checkout := filepath.Join(job, "repo")
	if err = os.MkdirAll(job, 0o755); err != nil {
		return refuse()
	}
	if _, merr := s.git(ctx, 0, "-C", mirror, "worktree", "move", kept, checkout); merr != nil {
		return refuse()
	}
	if _, cerr := s.git(ctx, 0, "-C", checkout, "checkout", "--quiet", "-b", p.Branch); cerr != nil {
		// moved back, as it was: Stage stages afresh, and a later rework may take it.
		// A tree that cannot be moved back stops the stage, so a second worktree is not added beside it.
		if _, merr := s.git(ctx, 0, "-C", mirror, "worktree", "move", checkout, kept); merr != nil {
			return "", "", false, fmt.Errorf("%s/%s: the last worktree moved and its branch could not be created, and it could not be moved back: %v", JobsDir, p.Job, cerr)
		}
		return refuse()
	}
	if werr := atomicfile.WriteFile(keptFromPath(job), []byte(prev+"\n"), 0o644); werr != nil {
		_, _ = s.git(ctx, 0, "-C", checkout, "checkout", "--quiet", oldBranch)  // ignored: best effort; the error returned is the move back failing
		_, _ = s.git(ctx, 0, "-C", mirror, "branch", "--quiet", "-D", p.Branch) // ignored: the branch was just created empty; a delete that fails leaves a name the next keep refuses
		if _, merr := s.git(ctx, 0, "-C", mirror, "worktree", "move", checkout, kept); merr != nil {
			return "", "", false, fmt.Errorf("%s/%s: the last worktree moved and its record could not be written, and it could not be moved back: %v", JobsDir, p.Job, werr)
		}
		_ = os.Remove(keptFromPath(job)) // ignored: the next stage writes it again, or leaves a note of a move that was undone
		return refuse()
	}
	return sha, prev, true, nil
}

// laneJobsKey carries the jobs a lane still holds into a stage. It is the set pruneStep puts
// in live, taken on the loop before the stage runs.
type laneJobsKey struct{}

func withLaneJobs(ctx context.Context, jobs map[string]bool) context.Context {
	if ctx == nil || len(jobs) == 0 {
		return ctx
	}
	return context.WithValue(ctx, laneJobsKey{}, jobs)
}

func laneJobsOf(ctx context.Context) map[string]bool {
	if ctx == nil {
		return nil
	}
	jobs, _ := ctx.Value(laneJobsKey{}).(map[string]bool)
	return jobs
}

// laneJobs is every job a lane still holds a card for.
func laneJobs(l *loop) map[string]bool {
	if l == nil || l.lanes == nil {
		return nil
	}
	jobs := map[string]bool{}
	for _, ln := range l.lanes.lanes {
		if ln == nil || ln.card == nil {
			continue
		}
		if j := filepath.Base(ln.card.Outbox); validJob(j) {
			jobs[j] = true
		}
		if ln.card.Brief != "" {
			if j := filepath.Base(filepath.Dir(ln.card.Brief)); validJob(j) {
				jobs[j] = true
			}
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	return jobs
}

// laneInCheckout says a lane is still in job's checkout. A report in the outbox and a brief
// gone from the inbox do not say so: the lane writes the report before its turn leaves the
// tree. The loop's set counts, and so does a fresh running lane mark on the job.
func laneInCheckout(ctx context.Context, dir, job string, now time.Time) bool {
	if laneJobsOf(ctx)[job] {
		return true
	}
	m, found := ReadLaneMark(dir, job)
	if !found || m.Ended {
		return false
	}
	return m.heldBy("", now) != ""
}

// PreviousCard is the work card of the attempt before a card's: <primary>.w<attempt-1> for
// <primary>.w<attempt> (sprint.WorkCardID); "" when the card is no work card of that attempt.
func PreviousCard(card string, attempt int) string {
	primary, n, ok := workCardOf(card)
	if !ok || n != attempt || attempt < 2 {
		return ""
	}
	return sprint.WorkCardID(primary, attempt-1)
}

// workCardOf reads a work card's id, <primary>.w<attempt> (sprint.WorkCardID).
func workCardOf(card string) (primary string, attempt int, ok bool) {
	i := strings.LastIndex(card, ".w")
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(card[i+2:])
	if err != nil || n < 1 || strconv.Itoa(n) != card[i+2:] {
		return "", 0, false
	}
	return card[:i], n, true
}

// lastJob is the newest job of a card under jobs/ that holds a checkout (its highest epoch,
// then generation; ParseJob); "" when there is none.
func lastJob(dir, card string) string {
	if card == "" {
		return ""
	}
	entries, err := os.ReadDir(filepath.Join(dir, JobsDir))
	if err != nil {
		return ""
	}
	best, bestEpoch, bestGen := "", -1, 0
	for _, e := range entries {
		id, epoch, gen, ok := ParseJob(e.Name())
		if !ok || id != card || !e.IsDir() || !exists(filepath.Join(dir, JobsDir, e.Name(), "repo")) {
			continue
		}
		if epoch > bestEpoch || epoch == bestEpoch && gen > bestGen {
			best, bestEpoch, bestGen = e.Name(), epoch, gen
		}
	}
	return best
}

// stageScratch is where a job's worktree is added before it is moved in, under its job dir.
const stageScratch = ".staging"

// dropScratch removes a job's scratch: the worktree a stage added there, from the mirror and
// from the disk. Each step is best effort; what is left is removed by the next stage first.
func (s *Stager) dropScratch(ctx context.Context, mirror, job, scratch string) {
	if entries, err := os.ReadDir(scratch); err == nil {
		for _, e := range entries {
			_, _ = s.git(ctx, 0, "-C", mirror, "worktree", "remove", "--force", filepath.Join(scratch, e.Name())) // ignored: removed from the disk below and pruned
		}
	}
	_ = safepath.RemoveUnder(job, scratch)                  // ignored: the next stage removes it first
	_, _ = s.git(ctx, 0, "-C", mirror, "worktree", "prune") // ignored: a stale entry is pruned by the next stage
}

// mirrorFetch is a mirror's refspecs: origin's branches as its remote-tracking refs, so the
// mirror's own branches are the jobs' alone and a fetch never moves one a worktree holds.
var mirrorFetch = []string{"+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*"}

// mirror is the repository's full bare mirror, fetched unless it was within MirrorFreshFor;
// the first stage of a repository makes it (git init --bare, then a full fetch: never
// shallow, never blob-less, which broke clones with 'pack has unresolved deltas'). A mirror of
// the layout before worktrees (origin's branches as its own) is converted in place. A fetch
// that fails is her account not reaching the repository.
func (s *Stager) mirror(ctx context.Context, repo string) (string, error) {
	owner, name, _ := strings.Cut(repo, "/")
	mirror := filepath.Join(s.Dir, MirrorsDir, owner, name+".git")
	url := s.url(repo)
	unreachable := func(err error) error {
		return &NotStageable{Repo: repo, Why: fmt.Sprintf("git could not fetch %s: %s", url, oneLine(err.Error(), 300)),
			Remedy: fmt.Sprintf("give her account read and push access to %s (on GitHub, a collaborator invitation she accepts, or her key added with write), or take the card(s) back and deal them to a friend who can reach it", repo)}
	}
	if _, err := os.Lstat(mirror); errors.Is(err, fs.ErrNotExist) {
		tmp := mirror + ".cloning"
		if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
			return "", err
		}
		if err := safepath.RemoveUnder(filepath.Dir(mirror), tmp); err != nil {
			return "", err
		}
		steps := [][]string{{"init", "--quiet", "--bare", "--", tmp}, {"-C", tmp, "remote", "add", "origin", url}}
		for i, spec := range mirrorFetch {
			op := "--add"
			if i == 0 {
				op = "--replace-all"
			}
			steps = append(steps, []string{"-C", tmp, "config", op, "remote.origin.fetch", spec})
		}
		for _, argv := range steps {
			if _, err := s.git(ctx, 0, argv...); err != nil {
				_ = safepath.RemoveUnder(filepath.Dir(mirror), tmp) // ignored: the next stage removes it first
				return "", err
			}
		}
		if _, err := s.git(ctx, MirrorCloneBudget, "-C", tmp, "fetch", "--quiet", "origin"); err != nil {
			_ = safepath.RemoveUnder(filepath.Dir(mirror), tmp) // ignored: the next stage removes it first
			return "", unreachable(err)
		}
		if err := os.Rename(tmp, mirror); err != nil {
			return "", err
		}
		s.mu.Lock()
		s.fetched[repo] = time.Now()
		s.mu.Unlock()
		return mirror, nil
	} else if err != nil {
		return "", err
	}
	s.mu.Lock()
	fresh := time.Since(s.fetched[repo]) < MirrorFreshFor
	s.mu.Unlock()
	if fresh {
		return mirror, nil
	}
	if err := s.mirrorLayout(ctx, mirror, url); err != nil {
		return "", err
	}
	if _, err := s.git(ctx, MirrorCloneBudget, "-C", mirror, "fetch", "--quiet", "--prune", "origin"); err != nil {
		return "", unreachable(err)
	}
	s.mu.Lock()
	s.fetched[repo] = time.Now()
	s.mu.Unlock()
	return mirror, nil
}

// mirrorLayout makes a mirror's origin url and refspecs the stager's. A mirror that held
// origin's branches as its own (the layout before worktrees, when every job was a clone) has
// those branches deleted, but never one a worktree has checked out; the fetch after brings
// them back as remote-tracking refs.
func (s *Stager) mirrorLayout(ctx context.Context, mirror, url string) error {
	if got, _ := s.git(ctx, 0, "-C", mirror, "config", "--get", "remote.origin.url"); got != url { // ignored: no url is a url to set
		if _, err := s.git(ctx, 0, "-C", mirror, "config", "remote.origin.url", url); err != nil {
			return err
		}
	}
	specs, _ := s.git(ctx, 0, "-C", mirror, "config", "--get-all", "remote.origin.fetch") // ignored: no refspec is a layout to set
	if specs == strings.Join(mirrorFetch, "\n") {
		return nil
	}
	held := map[string]bool{}
	list, err := s.git(ctx, 0, "-C", mirror, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	for _, l := range strings.Split(list, "\n") {
		if ref, ok := strings.CutPrefix(l, "branch "); ok {
			held[ref] = true
		}
	}
	heads, err := s.git(ctx, 0, "-C", mirror, "for-each-ref", "--format=%(refname)", "refs/heads/")
	if err != nil {
		return err
	}
	var del strings.Builder
	for _, ref := range strings.Split(heads, "\n") {
		if ref != "" && !held[ref] {
			fmt.Fprintf(&del, "delete %s\n", ref)
		}
	}
	if del.Len() > 0 {
		if _, err := gitrun.Output(ctx, gitrun.Options{Env: s.env(), OwnRepo: true, Stdin: strings.NewReader(del.String())}, "-C", mirror, "update-ref", "--stdin"); err != nil {
			return err
		}
	}
	for i, spec := range mirrorFetch {
		op := "--add"
		if i == 0 {
			op = "--replace-all"
		}
		if _, err := s.git(ctx, 0, "-C", mirror, "config", op, "remote.origin.fetch", spec); err != nil {
			return err
		}
	}
	return nil
}

// base is the commit p's base names in the mirror: its pin when it has one, else a branch,
// else a tag, else a full sha. A base the repository does not hold is the card's judgment; a
// pin it does not hold is never read as its ref.
func (s *Stager) base(ctx context.Context, mirror string, p Packet) (string, error) {
	refs := []string{"refs/remotes/origin/" + p.Base, "refs/tags/" + p.Base}
	if shaRE.MatchString(p.Base) {
		refs = append(refs, p.Base)
	}
	if p.BaseSha != "" {
		refs = []string{p.BaseSha}
	}
	for _, ref := range refs {
		if sha, err := s.git(ctx, 0, "-C", mirror, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}"); err == nil && shaRE.MatchString(sha) {
			return sha, nil
		}
	}
	named := p.Base
	if p.BaseSha != "" {
		named = p.Base + "@" + p.BaseSha
	}
	return "", &NotStageable{Repo: p.Repo, Card: p.Card, Why: fmt.Sprintf("its base %s is no branch, tag or commit of %s", named, s.url(p.Repo)),
		Remedy: fmt.Sprintf("push %s to %s, or rework the card onto a base it holds", named, p.Repo)}
}

// FinishedJobsKept is how many finished jobs' worktrees the daemon's cleanup keeps, the newest
// staged; the rest are pruned (Stager.Prune), at most PrunePerPass a cleanup, so the loop that
// runs it is held a few seconds at most.
const (
	FinishedJobsKept = 8
	PrunePerPass     = 4
)

// ReworkKeptMax caps how many cards' last worktrees a friend keeps for a rework, the newest
// staged kept; a longer list is finished like any other. A card's last worktree (the newest job
// of its newest attempt held here) is kept until the card lands or is dropped, never for a fixed
// time: this count, never a clock, bounds them, so a rework after any delay still finds its tree
// (Stager.keep; tla/JobWorktrees.tla LastKept, Spared).
const ReworkKeptMax = 24

// Prune removes the worktrees of finished jobs past kept, the oldest staged first (by its
// JOB.md), at most PrunePerPass of them, and answers the jobs it removed; a job whose mirror a
// stage holds is left for the next pass, never waited on. A job is finished when it is not live (held on her
// row, run by a lane, being staged: the caller's live) and its brief is not in her inbox (the
// inbox cleanup retired it); only a job whose checkout is a worktree of one of her mirrors is
// ever pruned, never a clone or anything another hand staged. A pruned job is gone whole
// (jobs/<job>), its worktree removed from the mirror; its branch stays in the mirror, so any
// commit on it is kept, and a stage of the job again takes the branch as it stands. The last
// worktree of a card is kept until its card lands or is dropped, however long that is, at most
// ReworkKeptMax of them (lastWorktrees; tla/JobWorktrees.tla LastKept): it is neither pruned nor
// counted among the finished.
func (s *Stager) Prune(ctx context.Context, live map[string]bool, kept int) ([]string, error) {
	jobs := filepath.Join(s.Dir, JobsDir)
	entries, err := os.ReadDir(jobs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	type finished struct {
		job, repo, mirror string
		at                time.Time
	}
	var done []finished
	spared := lastWorktrees(s.Dir, entries)
	for _, e := range entries {
		job := e.Name()
		if !e.IsDir() || !validJob(job) || live[job] || spared[job] || exists(filepath.Join(s.Dir, "inbox", job)) {
			continue
		}
		repo, mirror, ok := s.worktreeOf(filepath.Join(jobs, job, "repo"))
		if !ok {
			continue
		}
		at := time.Time{}
		if fi, err := os.Lstat(filepath.Join(jobs, job, JobFile)); err == nil {
			at = fi.ModTime()
		}
		done = append(done, finished{job: job, repo: repo, mirror: mirror, at: at})
	}
	sort.Slice(done, func(i, j int) bool {
		if !done[i].at.Equal(done[j].at) {
			return done[i].at.Before(done[j].at)
		}
		return done[i].job < done[j].job
	})
	var pruned []string
	var firstErr error
	for _, f := range done[:max(len(done)-max(kept, 0), 0)] {
		if len(pruned) == PrunePerPass {
			break
		}
		lock := s.repoLock(f.repo)
		if !lock.TryLock() {
			continue // a stage holds the mirror: the next pass
		}
		err := s.pruneJob(ctx, f.mirror, f.job)
		lock.Unlock()
		if err != nil {
			firstErr = cmpErr(firstErr, fmt.Errorf("%s/%s: %w", JobsDir, f.job, err))
			continue
		}
		pruned = append(pruned, f.job)
	}
	return pruned, firstErr
}

// lastWorktrees is the jobs that hold the last worktree of their card: each the newest job (by
// attempt, then epoch, then generation) of a work card's primary that holds a checkout. A card's
// last worktree is kept until the card lands or is dropped, with no time bound, so the newest
// ReworkKeptMax of them (by the time their attempt ended, its outbox REPORT.md, else its JOB.md)
// are spared by a prune; a longer list is finished like any other. No clock takes one: a last
// worktree staged a year ago is kept while it is among the newest ReworkKeptMax.
func lastWorktrees(dir string, entries []fs.DirEntry) map[string]bool {
	type last struct {
		job                 string
		attempt, epoch, gen int
		at                  time.Time
	}
	newest := map[string]last{}
	for _, e := range entries {
		id, epoch, gen, ok := ParseJob(e.Name())
		if !ok || !e.IsDir() || !exists(filepath.Join(dir, JobsDir, e.Name(), "repo")) {
			continue
		}
		primary, attempt, ok := workCardOf(id)
		if !ok {
			continue
		}
		b, seen := newest[primary]
		if !seen || attempt > b.attempt || attempt == b.attempt && (epoch > b.epoch || epoch == b.epoch && gen > b.gen) {
			newest[primary] = last{job: e.Name(), attempt: attempt, epoch: epoch, gen: gen}
		}
	}
	all := make([]last, 0, len(newest))
	for _, l := range newest {
		fi, err := os.Lstat(filepath.Join(dir, "outbox", l.job, "REPORT.md"))
		if err != nil {
			fi, err = os.Lstat(filepath.Join(JobDir(dir, l.job), JobFile))
		}
		if err == nil {
			l.at = fi.ModTime()
		}
		all = append(all, l)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].at.Equal(all[j].at) {
			return all[i].at.After(all[j].at) // newest first
		}
		return all[i].job > all[j].job
	})
	spared := map[string]bool{}
	for i, l := range all {
		if i >= ReworkKeptMax {
			break
		}
		spared[l.job] = true
	}
	return spared
}

// pruneJob removes a finished job, its mirror's lock held: its worktree from the mirror, then
// the job's directory.
func (s *Stager) pruneJob(ctx context.Context, mirror, job string) error {
	dir := JobDir(s.Dir, job)
	if _, err := s.git(ctx, 0, "-C", mirror, "worktree", "remove", "--force", "--force", filepath.Join(dir, "repo")); err != nil {
		return err
	}
	if err := safepath.RemoveUnder(filepath.Join(s.Dir, JobsDir), dir); err != nil {
		return err
	}
	_, err := s.git(ctx, 0, "-C", mirror, "worktree", "prune")
	return err
}

// worktreeOf is the repository (owner/name) and the mirror whose worktree checkout is: its
// .git a file naming a worktree under one of her mirrors; false for anything else.
func (s *Stager) worktreeOf(checkout string) (repo, mirror string, ok bool) {
	raw, err := os.ReadFile(filepath.Join(checkout, ".git"))
	if err != nil {
		return "", "", false
	}
	gitdir, found := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir: ")
	if !found || !filepath.IsAbs(gitdir) {
		return "", "", false
	}
	mirrors, err := filepath.EvalSymlinks(filepath.Join(s.Dir, MirrorsDir))
	if err != nil {
		return "", "", false
	}
	if real, err := filepath.EvalSymlinks(gitdir); err == nil {
		gitdir = real
	}
	rel, err := filepath.Rel(mirrors, gitdir)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 4 || parts[3] == "" || parts[2] != "worktrees" || !strings.HasSuffix(parts[1], ".git") {
		return "", "", false
	}
	repo = parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
	if !repoRE.MatchString(repo) || strings.Contains(repo, "..") {
		return "", "", false
	}
	return repo, filepath.Join(mirrors, parts[0], parts[1]), true
}

// JobText is a staged job's JOB.md: the card-contract shape (docs/SPEC-CARD-CONTRACT.md), the
// checkout, the branch, the outbox report and the finish.
func JobText(dir string, p Packet, sha string) string {
	attempt := max(p.Attempt, 1)
	checkout := filepath.Join(JobDir(dir, p.Job), "repo")
	outbox := filepath.Join(dir, "outbox", p.Job)
	at := p.Base
	if p.Carry != "" {
		at = fmt.Sprintf("the head attempt %d pushed", p.CarryAttempt)
	}
	return fmt.Sprintf("# JOB: work %s, attempt %d\n\n"+
		"The staged checkout: %s (a git worktree of %s at %s, %s, on branch %s).\n"+
		"Work there; commit as the brief says; push the branch (git push -u origin %s).\n"+
		"Finish: write %s with 'Verdict: LAND|HOLD|FAIL' and 'Head: <sha>' on the first two lines, then the report; RESULT.md beside it with the same head. The coordinator syncs the outbox.\n"+
		"If the push is refused, leave the report with Verdict: HOLD naming 'no push' and the coordinator pushes from this checkout.\n",
		p.Card, attempt, checkout, p.Repo, at, sha, p.Branch, p.Branch, filepath.Join(outbox, "REPORT.md"))
}

// KeptJobText is the JOB.md of a rework started in the last worktree (keep): the title, the
// fix, the kept checkout, the branch and the finish, as JobText says them. The fix is the line
// the lane is handed first (KeptFix).
func KeptJobText(dir string, p Packet, prev, sha string) string {
	checkout := filepath.Join(JobDir(dir, p.Job), "repo")
	outbox := filepath.Join(dir, "outbox", p.Job)
	return fmt.Sprintf("# JOB: work %s, attempt %d\n\n"+
		"%s%s\n"+
		"%s%s (the git worktree of the attempt before, job %s, moved here whole; at %s, on branch %s, a new branch at that commit; origin is %s).\n"+
		"Make the fix there: the tree, its build and its history are as the last attempt left them, so there is nothing to re-learn and nothing to clone. Fetch origin first if the brief asks for its base's tip.\n"+
		"Commit; push the branch (git push -u origin %s).\n"+
		"Finish: write %s with 'Verdict: LAND|HOLD|FAIL' and 'Head: <sha>' on the first two lines, then the report; RESULT.md beside it with the same head. The coordinator syncs the outbox.\n"+
		"If the push is refused, leave the report with Verdict: HOLD naming 'no push' and the coordinator pushes from this checkout.\n",
		p.Card, max(p.Attempt, 1), KeptFixKey, p.Fix, KeptCheckoutKey, checkout, prev, sha, p.Branch, p.Repo, p.Branch, filepath.Join(outbox, "REPORT.md"))
}

// The lines of a kept job's JOB.md that KeptFix reads, and the file that names the job the
// checkout was moved from. KEPT is written before JOB.md, so a stage that dies between the
// move and JOB.md writes the kept text on the next try.
const (
	KeptFixKey      = "The fix: "
	KeptCheckoutKey = "The kept checkout: "
	KeptFromFile    = "KEPT"
)

func keptFromPath(jobDir string) string { return filepath.Join(jobDir, KeptFromFile) }

func readKeptFrom(jobDir string) (string, bool) {
	raw, err := os.ReadFile(keptFromPath(jobDir))
	if err != nil {
		return "", false
	}
	prev := strings.TrimSpace(string(raw))
	if !validJob(prev) {
		return "", false
	}
	return prev, true
}

// KeptFix is the fix of a job staged in the last worktree, read from its JOB.md; "" for any
// other job. A lane hands it as the first line of the card (CardText, LanePrompt).
func KeptFix(dir, job string) string {
	if !validJob(job) {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(JobDir(dir, job), JobFile))
	if err != nil {
		return ""
	}
	fix, kept := "", false
	for _, l := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(l, KeptFixKey); ok && fix == "" {
			fix = strings.TrimSpace(v)
		}
		kept = kept || strings.HasPrefix(l, KeptCheckoutKey)
	}
	if !kept {
		return ""
	}
	return fix
}

func (s *Stager) writeJob(p Packet, sha string) error {
	return s.writeJobText(p, JobText(s.Dir, p, sha))
}

func (s *Stager) writeJobText(p Packet, text string) error {
	err := atomicfile.WriteFile(filepath.Join(JobDir(s.Dir, p.Job), JobFile), []byte(text), 0o644, atomicfile.NoReplace())
	if errors.Is(err, fs.ErrExist) {
		return nil // staged by another hand between the look and the write
	}
	return err
}

// stageResult is one stage's end, handed from its goroutine to the loop.
type stageResult struct {
	p       Packet
	sha     string
	err     error
	stopped bool // the daemon stopped while it ran: nothing is said, and the next loop stages it again
}

// stageStep is the daemon's staging, once a reconcile: every stage that ended is said (a
// judgment once while it stands, to the coordinator), and every held work card whose brief is
// in her inbox and whose job is not staged is staged, each on a goroutine of its own, so the
// loop beats on while a mirror is cloned. A job whose stage failed waits StageRetryEvery.
func (l *loop) stageStep(cards []HeldCard, now time.Time) {
	d := l.d
	at := now.UTC().Format(time.RFC3339)
	d.stageMu.Lock()
	done := d.stageDone
	d.stageDone = nil
	d.stageMu.Unlock()
	for _, r := range done {
		delete(d.staging, r.p.Job)
		var ns *NotStageable
		switch {
		case r.stopped:
		case r.err == nil:
			delete(d.stageRetry, r.p.Job)
			delete(d.stageSaid, "repo "+r.p.Repo)
			delete(d.stageSaid, "card "+r.p.Card)
			switch {
			case r.sha != "" && KeptFix(d.Dir, r.p.Job) != "":
				d.Record(fmt.Sprintf("%s stage: kept the last worktree as %s/%s/repo (%s, %s, on %s, a rework) and its %s", at, JobsDir, r.p.Job, r.p.Repo, r.sha, r.p.Branch, JobFile))
			case r.sha != "":
				d.Record(fmt.Sprintf("%s stage: staged %s/%s/repo (%s at %s, %s, on %s) and its %s", at, JobsDir, r.p.Job, r.p.Repo, r.p.Base, r.sha, r.p.Branch, JobFile))
			}
			if line, ok := d.stageDealt[r.p.Job]; ok && l.mode == ModeBatch {
				l.dealt = append(l.dealt, line)
			}
			delete(d.stageDealt, r.p.Job)
		case errors.As(r.err, &ns):
			d.stageRetry[r.p.Job] = now.Add(StageRetryEvery)
			if !d.stageSaid[ns.key()] {
				d.stageSaid[ns.key()] = true
				d.Record(fmt.Sprintf("%s stage: not staged %s/%s: %s; told the coordinator", at, JobsDir, r.p.Job, oneLine(ns.Error(), 400)))
				subject, body := StageJudgmentText(d.Friend, ns)
				l.tellKind(bus.KindBlocker, subject, body, now)
			}
		default:
			d.stageRetry[r.p.Job] = now.Add(StageRetryEvery)
			if key := "job " + r.p.Job + ": " + r.err.Error(); !d.stageSaid[key] {
				d.stageSaid[key] = true
				d.Record(fmt.Sprintf("%s stage: not staged %s/%s: %s; tried again in %s", at, JobsDir, r.p.Job, oneLine(r.err.Error(), 400), StageRetryEvery))
			}
		}
	}
	for _, h := range cards {
		p, ok := PacketOf(h)
		if !ok || !validJob(p.Job) || d.staging[p.Job] || now.Before(d.stageRetry[p.Job]) {
			continue
		}
		if !exists(filepath.Join(d.Dir, "inbox", p.Job, "BRIEF.md")) || Staged(d.Dir, p.Job) {
			continue
		}
		d.staging[p.Job] = true
		d.stageWG.Add(1)
		ctx := withLaneJobs(l.ctx, laneJobs(l))
		go func() {
			defer d.stageWG.Done()
			sha, err := d.Stage(ctx, p)
			d.stageMu.Lock()
			d.stageDone = append(d.stageDone, stageResult{p: p, sha: sha, err: err, stopped: err != nil && l.ctx.Err() != nil})
			d.stageMu.Unlock()
		}()
	}
}

// pruneStep is the daemon's cleanup of finished jobs, run in the loop after each inbox
// cleanup, so what is live cannot change under it (a prune on a goroutine of its own, handed a
// snapshot, could remove a job dealt to her again and handed to a lane meanwhile: the reversed
// witness "async" of tla/JobWorktrees.tla). Live is every job held on her row, run by a lane
// (keep) or being staged; Stager.Prune never touches one, nor a job whose brief is in her
// inbox, and removes at most PrunePerPass, never waiting on a mirror a stage holds. Each job
// removed is said, and a failure once while it stands.
func (l *loop) pruneStep(held []HeldCard, keep map[string]bool, now time.Time) {
	d := l.d
	if d.Prune == nil {
		return
	}
	live := map[string]bool{}
	for _, h := range held {
		live[h.Job] = true
	}
	for job := range keep {
		live[job] = true
	}
	for job := range d.staging {
		live[job] = true
	}
	pruned, err := d.Prune(l.ctx, live)
	at := now.UTC().Format(time.RFC3339)
	for _, job := range pruned {
		d.Record(fmt.Sprintf("%s prune: removed %s/%s and its worktree: its card is finished (%d finished kept)", at, JobsDir, job, FinishedJobsKept))
	}
	switch {
	case err == nil:
		d.pruneSaid = ""
	case l.ctx.Err() != nil:
	case err.Error() != d.pruneSaid:
		d.pruneSaid = err.Error()
		d.Record(fmt.Sprintf("%s prune: not pruned: %s; tried again at the next cleanup", at, oneLine(err.Error(), 400)))
	}
}

// stageOwed says a held card is not handed to a lane yet: the daemon stages jobs, the card is
// one it stages, and its JOB.md is not there.
func (d *Daemon) stageOwed(h HeldCard) bool {
	if d.Stage == nil {
		return false
	}
	_, ok := PacketOf(h)
	return ok && !Staged(d.Dir, h.Job)
}

// TipBudget bounds the one ls-remote of Tip.
const TipBudget = 10 * time.Second

// Tip is origin's tip of branch in repo (owner/name): one git ls-remote of the one ref,
// bounded by TipBudget; "" when origin has no such branch.
func (s *Stager) Tip(ctx context.Context, repo, branch string) (string, error) {
	if !repoRE.MatchString(repo) || strings.Contains(repo, "..") {
		return "", fmt.Errorf("REPO %q is no owner/name", repo)
	}
	if !validRef(branch) {
		return "", fmt.Errorf("branch %q is no branch name", branch)
	}
	ref := "refs/heads/" + branch
	out, err := s.git(ctx, TipBudget, "ls-remote", "--", s.url(repo), ref)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == ref {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", nil
}
