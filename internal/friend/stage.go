package friend

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
// card whose brief is in her inbox, the daemon clones REPO at BASE on the card's branch into
// jobs/<job>/repo, from a bare mirror of the repository it keeps under mirrors/ (so a stage
// is a fetch and a local clone, seconds), and writes jobs/<job>/JOB.md in the card-contract
// shape (docs/SPEC-CARD-CONTRACT.md). No lane is handed the card until JOB.md is there. A
// repository her account cannot reach is one judgment to the coordinator with the remedy,
// never a lane that discovers it (docs/SPEC-FRIEND.md, staging). The machine is modelled in
// tla/FriendStage.tla (MCFriendStage*: a lane handed only a staged card, one judgment while it
// stands, a failed job staged again).

// The directories under her working directory that staging writes.
const (
	JobsDir    = "jobs"
	MirrorsDir = "mirrors"
	JobFile    = "JOB.md"
)

// StageRetryEvery is how long a job whose stage failed waits before it is staged again; a
// judgment stands, said once, until a stage of its repository or card succeeds.
// MirrorFreshFor is how long a fetched mirror serves stages without fetching again, so a
// burst of cards on one repository is one fetch. MirrorCloneBudget bounds the first clone of
// a repository's mirror, the one slow step (every later stage fetches only what is new).
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
}

func (n *NotStageable) Error() string {
	if n.Card == "" {
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

// repoLock is the lock of one repository's mirror: its fetch and the clones taken from it
// run one at a time.
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

// Stage stages p's job: the mirror of its repository fetched (cloned the first time), a
// clone of it at the base on the card's branch at jobs/<job>/repo with origin the repository
// itself, and jobs/<job>/JOB.md. It answers the commit the checkout is at. A job already
// staged is left as it is; a checkout is never half there (it is cloned beside and moved in
// whole), and JOB.md is written last, so a lane never meets a checkout not ready.
//
// A rework starts in the last worktree when it may (keep): the previous attempt's checkout on
// this friend, moved whole into the job on the card's new branch, so the lane edits and pushes
// without re-learning the tree. When it may not, the clone starts at the head the last
// attempt pushed (Carry) when the mirror holds it, else at the base.
func (s *Stager) Stage(ctx context.Context, p Packet) (string, error) {
	if err := p.check(); err != nil {
		return "", &NotStageable{Repo: p.Repo, Card: p.Card, Why: err.Error(), Remedy: "rework the card with a packet that names its repository (owner/name), its base and its branch"}
	}
	job := JobDir(s.Dir, p.Job)
	checkout := filepath.Join(job, "repo")
	if Staged(s.Dir, p.Job) {
		return "", nil
	}
	if _, err := os.Lstat(checkout); err == nil {
		// moved in by a stage that ended before its JOB.md: it is the card's when it is on its branch
		branch, err := s.git(ctx, 0, "-C", checkout, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil || branch != p.Branch {
			return "", fmt.Errorf("%s/%s/repo is there with no %s and is not on %s (%q); it is left as found", JobsDir, p.Job, JobFile, p.Branch, branch)
		}
		sha, err := s.git(ctx, 0, "-C", checkout, "rev-parse", "HEAD")
		if err != nil {
			return "", err
		}
		return sha, s.writeJob(p, sha)
	}
	if sha, prev, ok := s.keep(ctx, p); ok {
		return sha, s.writeJobText(p, KeptJobText(s.Dir, p, prev, sha))
	}
	lock := s.repoLock(p.Repo)
	lock.Lock()
	defer lock.Unlock()
	mirror, err := s.mirror(ctx, p.Repo)
	if err != nil {
		return "", err
	}
	sha, err := s.base(ctx, mirror, p)
	if err != nil {
		return "", err
	}
	carried := false
	if p.Carry != "" {
		// the last attempt's pushed head, when the mirror holds it: the work goes on from there.
		// A mirror fetched within MirrorFreshFor may predate that push: it is fetched once more.
		holds := func() bool {
			c, err := s.git(ctx, 0, "-C", mirror, "rev-parse", "--verify", "--quiet", "--end-of-options", p.Carry+"^{commit}")
			return err == nil && c == p.Carry
		}
		if !holds() {
			if _, err := s.git(ctx, 0, "-C", mirror, "fetch", "--quiet", "--prune", "origin"); err == nil {
				s.mu.Lock()
				s.fetched[p.Repo] = time.Now()
				s.mu.Unlock()
			}
		}
		if holds() {
			sha, carried = p.Carry, true
		}
	}
	tmp := filepath.Join(job, ".repo.staging")
	if err := os.MkdirAll(job, 0o755); err != nil {
		return "", err
	}
	if err := safepath.RemoveUnder(job, tmp); err != nil { // a stage that ended part way: its own scratch, never the checkout
		return "", err
	}
	steps := [][]string{
		{"clone", "--quiet", "--no-checkout", "--", mirror, tmp},
		{"-C", tmp, "remote", "set-url", "origin", s.url(p.Repo)},
		{"-C", tmp, "checkout", "--quiet", "-b", p.Branch, sha},
	}
	for _, argv := range steps {
		if _, err := s.git(ctx, 0, argv...); err != nil {
			_ = safepath.RemoveUnder(job, tmp) // ignored: the next stage removes it first
			return "", err
		}
	}
	if err := os.Rename(tmp, checkout); err != nil {
		return "", err
	}
	if !carried {
		p.Carry = ""
	}
	return sha, s.writeJob(p, sha)
}

// keep is a rework started in the last worktree (internal/friend/tla/ReworkWorktree.tla:
// KeptOnlyWhenSafe, KeptWhenItMay, OneJobPerTree). For a later attempt whose fix names no file
// outside the card's PATHS, the checkout of the attempt before it on this friend (the newest
// job of <primary>.w<n-1>), when its attempt ended (its outbox REPORT.md is there), its origin
// is the repository, no tracked file has an uncommitted change, and it holds the carried head,
// is put on the card's new branch at its own HEAD and moved whole into this job. It answers the
// commit the checkout is at and the job it came from; false when any of that is not so, and
// Stage clones afresh, the old tree untouched (or, after a rename that failed, on a branch no
// later keep takes again). The daemon keeps every job's worktree until a later attempt takes
// it, so the last one is there until the card lands.
func (s *Stager) keep(ctx context.Context, p Packet) (sha, prev string, ok bool) {
	if p.Attempt < 2 || p.Fix == "" || len(p.NewFiles) > 0 {
		return "", "", false
	}
	prev = lastJob(s.Dir, PreviousCard(p.Card, p.Attempt))
	if prev == "" || !exists(filepath.Join(s.Dir, "outbox", prev, "REPORT.md")) {
		return "", "", false
	}
	kept := filepath.Join(JobDir(s.Dir, prev), "repo")
	if url, err := s.git(ctx, 0, "-C", kept, "remote", "get-url", "origin"); err != nil || url != s.url(p.Repo) {
		return "", "", false
	}
	if dirty, err := s.git(ctx, 0, "-C", kept, "status", "--porcelain", "--untracked-files=no"); err != nil || dirty != "" {
		return "", "", false
	}
	if p.Carry != "" {
		if _, err := s.git(ctx, 0, "-C", kept, "merge-base", "--is-ancestor", p.Carry, "HEAD"); err != nil {
			return "", "", false // the tree is not where the last attempt's pushed work is
		}
	}
	sha, err := s.git(ctx, 0, "-C", kept, "rev-parse", "HEAD")
	if err != nil || !shaRE.MatchString(sha) {
		return "", "", false
	}
	job := JobDir(s.Dir, p.Job)
	if err := os.MkdirAll(job, 0o755); err != nil {
		return "", "", false
	}
	// the branch first, in place: a rename that fails leaves the kept tree on a branch no
	// later stage can take again (checkout -b refuses it), so that stage clones afresh
	if _, err := s.git(ctx, 0, "-C", kept, "checkout", "--quiet", "-b", p.Branch); err != nil {
		return "", "", false
	}
	if err := os.Rename(kept, filepath.Join(job, "repo")); err != nil {
		return "", "", false
	}
	return sha, prev, true
}

// PreviousCard is the work card of the attempt before a card's: <primary>.w<attempt-1> for
// <primary>.w<attempt> (sprint.WorkCardID); "" when the card is no work card of that attempt.
func PreviousCard(card string, attempt int) string {
	i := strings.LastIndex(card, ".w")
	if i <= 0 || attempt < 2 || card[i+2:] != strconv.Itoa(attempt) {
		return ""
	}
	return card[:i] + ".w" + strconv.Itoa(attempt-1)
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

// mirror is the repository's bare mirror, fetched unless it was within MirrorFreshFor; the
// first stage of a repository clones it. A clone or fetch that fails is her account not
// reaching the repository.
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
		if _, err := s.git(ctx, MirrorCloneBudget, "clone", "--quiet", "--bare", "--", url, tmp); err != nil {
			_ = safepath.RemoveUnder(filepath.Dir(mirror), tmp) // ignored: the next stage removes it first
			return "", unreachable(err)
		}
		for _, spec := range [][]string{
			{"-C", tmp, "config", "--replace-all", "remote.origin.fetch", "+refs/heads/*:refs/heads/*"},
			{"-C", tmp, "config", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*"},
		} {
			if _, err := s.git(ctx, 0, spec...); err != nil {
				return "", err
			}
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
	if _, err := s.git(ctx, 0, "-C", mirror, "fetch", "--quiet", "--prune", "origin"); err != nil {
		return "", unreachable(err)
	}
	s.mu.Lock()
	s.fetched[repo] = time.Now()
	s.mu.Unlock()
	return mirror, nil
}

// base is the commit p's base names in the mirror: its pin when it has one, else a branch,
// else a tag, else a full sha. A base the repository does not hold is the card's judgment; a
// pin it does not hold is never read as its ref.
func (s *Stager) base(ctx context.Context, mirror string, p Packet) (string, error) {
	refs := []string{"refs/heads/" + p.Base, "refs/tags/" + p.Base}
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
		"The staged checkout: %s (a clone of %s at %s, %s, on branch %s).\n"+
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
		"%s%s (the worktree of the attempt before, job %s, kept by the daemon and moved here whole; at %s, on branch %s, a new branch at that commit; origin is %s).\n"+
		"Make the fix there: the tree, its build and its history are as the last attempt left them, so there is nothing to re-learn and nothing to clone. Fetch origin first if the brief asks for its base's tip.\n"+
		"Commit; push the branch (git push -u origin %s).\n"+
		"Finish: write %s with 'Verdict: LAND|HOLD|FAIL' and 'Head: <sha>' on the first two lines, then the report; RESULT.md beside it with the same head. The coordinator syncs the outbox.\n"+
		"If the push is refused, leave the report with Verdict: HOLD naming 'no push' and the coordinator pushes from this checkout.\n",
		p.Card, max(p.Attempt, 1), KeptFixKey, p.Fix, KeptCheckoutKey, checkout, prev, sha, p.Branch, p.Repo, p.Branch, filepath.Join(outbox, "REPORT.md"))
}

// The lines of a kept job's JOB.md that KeptFix reads.
const (
	KeptFixKey      = "The fix: "
	KeptCheckoutKey = "The kept checkout: "
)

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
		go func() {
			defer d.stageWG.Done()
			sha, err := d.Stage(l.ctx, p)
			d.stageMu.Lock()
			d.stageDone = append(d.stageDone, stageResult{p: p, sha: sha, err: err, stopped: err != nil && l.ctx.Err() != nil})
			d.stageMu.Unlock()
		}()
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
