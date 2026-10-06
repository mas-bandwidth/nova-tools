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
// attempt.
type Packet struct {
	Card, Job, Repo, Base, BaseSha, Branch string
	Attempt                                int
}

var (
	statusBranchRE  = regexp.MustCompile(`push your work to the branch ([^\s;]+);`)
	statusAttemptRE = regexp.MustCompile(`, attempt (\d+);`)
	repoRE          = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*/[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	refRE           = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./~-]*$`)
	shaRE           = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

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
// process that runs it (the daemon's: her account's). Mirrors is where the bare mirrors are
// kept, one per repository ("": Dir/mirrors; the daemon's default is under its state dir,
// nova-friend run --mirrors); a mirror there must be a full one, never a blob-less partial
// clone, whose local clones fail with "pack has unresolved deltas". URL names a repository's
// remote (nil: GitHubURL); Env is git's whole environment (nil: the daemon's own, with
// GIT_TERMINAL_PROMPT=0, so git never waits on a prompt no one answers).
type Stager struct {
	Dir     string
	Mirrors string
	URL     func(repo string) string
	Env     []string

	mu      sync.Mutex
	repos   map[string]*sync.Mutex
	fetched map[string]time.Time
	full    map[string]bool // the mirrors read as full ones
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
		s.repos, s.fetched, s.full = map[string]*sync.Mutex{}, map[string]time.Time{}, map[string]bool{}
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

// StageMark is the file a stage leaves in the job directory it made, until its JOB.md is
// written: a job directory without it and without a JOB.md was made by another hand (a runner
// that stages its own jobs, or a coordinator by hand), and is never staged over. StageLock is
// the job's lock beside it, jobs/.<job>.lock, held while a stage runs.
const StageMark = ".nova-friend-stage"

func stageLock(dir, job string) string { return filepath.Join(dir, JobsDir, "."+job+".lock") }

// StartedElsewhere is a job another hand has: its directory there with no JOB.md and no
// StageMark (a runner that stages its own jobs made it), or its lock held by another stage
// (a second daemon). Nothing is written; the job is the other hand's, and its brief is
// written once its JOB.md is there.
type StartedElsewhere struct{ Job, Why string }

func (e *StartedElsewhere) Error() string {
	return fmt.Sprintf("%s/%s was started elsewhere: %s; left to that hand", JobsDir, e.Job, e.Why)
}

// Stage stages p's job: the mirror of its repository fetched (cloned the first time), a
// clone of it at the base on the card's branch at jobs/<job>/repo with origin the repository
// itself, and jobs/<job>/JOB.md. It answers the commit the checkout is at. A job already
// staged is left as it is; a checkout is never half there (it is cloned beside and moved in
// whole), and JOB.md is written last, so a lane never meets a checkout not ready. The job is
// claimed first: its lock taken (held by another stage: StartedElsewhere), and its directory
// made with the StageMark in it, or found with the mark of a stage that ended part way; a
// directory found without the mark is another hand's (StartedElsewhere), never staged twice.
func (s *Stager) Stage(ctx context.Context, p Packet) (string, error) {
	if err := p.check(); err != nil {
		return "", &NotStageable{Repo: p.Repo, Card: p.Card, Why: err.Error(), Remedy: "rework the card with a packet that names its repository (owner/name), its base and its branch"}
	}
	if Staged(s.Dir, p.Job) {
		return "", nil
	}
	job := JobDir(s.Dir, p.Job)
	if err := os.MkdirAll(filepath.Dir(job), 0o755); err != nil {
		return "", err
	}
	release, ok, err := tryLock(stageLock(s.Dir, p.Job))
	if err != nil {
		return "", err
	}
	if !ok {
		return "", &StartedElsewhere{Job: p.Job, Why: "another stage holds its lock " + JobsDir + "/." + p.Job + ".lock"}
	}
	defer release()
	if Staged(s.Dir, p.Job) { // staged by the stage that held the lock before
		return "", nil
	}
	if err := s.owned(p.Job); err != nil {
		return "", err
	}
	checkout := filepath.Join(job, "repo")
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
		return sha, s.finish(p, sha)
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
	if err := s.claim(p.Job); err != nil {
		return "", err
	}
	tmp := filepath.Join(job, ".repo.staging")
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
	return sha, s.finish(p, sha)
}

// owned is nil when the job's directory is not there or is a stage's (its StageMark there),
// else StartedElsewhere. Its caller holds the job's lock.
func (s *Stager) owned(job string) error {
	fi, err := os.Lstat(JobDir(s.Dir, job))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !fi.IsDir():
		return &StartedElsewhere{Job: job, Why: "it is a symlink or a file, not a directory"}
	case !exists(filepath.Join(JobDir(s.Dir, job), StageMark)):
		return &StartedElsewhere{Job: job, Why: fmt.Sprintf("its directory is there with no %s and no %s (a runner that stages its own jobs, or a hand)", JobFile, StageMark)}
	}
	return nil
}

// claim makes the job's directory with the StageMark in it, or finds it a stage's. Its caller
// holds the job's lock.
func (s *Stager) claim(job string) error {
	dir := JobDir(s.Dir, job)
	if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	} else if err != nil {
		return s.owned(job) // made since the look: the mark says whose it is
	}
	return os.WriteFile(filepath.Join(dir, StageMark), []byte("staged by nova-friend; removed when JOB.md is written\n"), 0o644)
}

// finish writes the job's JOB.md and takes its StageMark and its lock file away (the lock
// still held: a stage that meets the lock after finds the job staged).
func (s *Stager) finish(p Packet, sha string) error {
	if err := s.writeJob(p, sha); err != nil {
		return err
	}
	for _, f := range []string{filepath.Join(JobDir(s.Dir, p.Job), StageMark), stageLock(s.Dir, p.Job)} {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// MirrorDir is the bare mirror of repo (owner/name) under mirrors, the Stager's Mirrors.
func MirrorDir(mirrors, repo string) string {
	owner, name, _ := strings.Cut(repo, "/")
	return filepath.Join(mirrors, owner, name+".git")
}

func (s *Stager) mirrors() string {
	if s.Mirrors != "" {
		return s.Mirrors
	}
	return filepath.Join(s.Dir, MirrorsDir)
}

// mirror is the repository's bare mirror, fetched unless it was within MirrorFreshFor; the
// first stage of a repository clones it. A clone or fetch that fails is her account not
// reaching the repository.
func (s *Stager) mirror(ctx context.Context, repo string) (string, error) {
	mirror := MirrorDir(s.mirrors(), repo)
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
	if err := s.fullMirror(ctx, repo, mirror); err != nil {
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

// fullMirror refuses a mirror that is a partial clone (a blob-less one: its promisor remote or
// its partialclone extension set), whose local clones fail with "pack has unresolved deltas";
// a mirror read full once is not read again.
func (s *Stager) fullMirror(ctx context.Context, repo, mirror string) error {
	s.mu.Lock()
	full := s.full[repo]
	s.mu.Unlock()
	if full {
		return nil
	}
	for _, key := range []string{"extensions.partialclone", "remote.origin.promisor"} {
		if v, err := s.git(ctx, 0, "-C", mirror, "config", "--get", key); err == nil && v != "" && v != "false" {
			return fmt.Errorf("the mirror %s is a partial clone (%s=%s), and a clone from it fails with \"pack has unresolved deltas\"; remove it and the next stage clones a full one, or point --mirrors at a full mirror", mirror, key, v)
		}
	}
	s.mu.Lock()
	s.full[repo] = true
	s.mu.Unlock()
	return nil
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
	return fmt.Sprintf("# JOB: work %s, attempt %d\n\n"+
		"The staged checkout: %s (a clone of %s at %s, %s, on branch %s).\n"+
		"Work there; commit as the brief says; push the branch (git push -u origin %s).\n"+
		"Finish: write %s with 'Verdict: LAND|HOLD|FAIL' and 'Head: <sha>' on the first two lines, then the report; RESULT.md beside it with the same head. The coordinator syncs the outbox.\n"+
		"If the push is refused, leave the report with Verdict: HOLD naming 'no push' and the coordinator pushes from this checkout.\n",
		p.Card, attempt, checkout, p.Repo, p.Base, sha, p.Branch, p.Branch, filepath.Join(outbox, "REPORT.md"))
}

func (s *Stager) writeJob(p Packet, sha string) error {
	err := atomicfile.WriteFile(filepath.Join(JobDir(s.Dir, p.Job), JobFile), []byte(JobText(s.Dir, p, sha)), 0o644, atomicfile.NoReplace())
	if errors.Is(err, fs.ErrExist) {
		return nil // staged by another hand between the look and the write
	}
	return err
}

// stageResult is one delivery's end that staged (or tried to), handed from its goroutine to
// the loop.
type stageResult struct {
	h       HeldCard
	p       Packet
	o       Delivered
	stopped bool // the daemon stopped while it ran: nothing is said, and the next loop delivers it again
}

// delivery is the daemon's Delivery: her working directory, and its Stage (nil: her runner
// stages its own jobs, and the daemon writes briefs alone).
func (d *Daemon) delivery() Delivery { return Delivery{Dir: d.Dir, Stage: d.Stage} }

// stageStep is the daemon's delivery of the cards it stages, once a reconcile: every one that
// ended is said (a staged job and its brief written; a judgment once while it stands, to the
// coordinator; a job another hand started), and every held card owed a stage (Delivery.Owed)
// is delivered in order, its job staged and then its brief written (Delivery.One), each on a
// goroutine of its own, so the loop beats on while a mirror is cloned. A job whose stage
// failed waits StageRetryEvery.
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
		var away *StartedElsewhere
		switch err := r.o.Err; {
		case r.stopped:
		case err == nil:
			delete(d.stageRetry, r.p.Job)
			delete(d.stageSaid, "repo "+r.p.Repo)
			delete(d.stageSaid, "card "+r.p.Card)
			if r.o.Sha != "" {
				d.Record(fmt.Sprintf("%s stage: staged %s/%s/repo (%s at %s, %s, on %s) and its %s", at, JobsDir, r.p.Job, r.p.Repo, r.p.Base, r.o.Sha, r.p.Branch, JobFile))
			}
			if r.o.What == DeliverStaged && r.o.Why == "" {
				line := wroteLine(at, r.h)
				d.Record(line)
				if _, rest, _ := strings.Cut(line, " inbox: wrote "); l.mode == ModeBatch {
					l.dealt = append(l.dealt, rest)
				}
			}
		case errors.As(err, &ns):
			d.stageRetry[r.p.Job] = now.Add(StageRetryEvery)
			if !d.stageSaid[ns.key()] {
				d.stageSaid[ns.key()] = true
				d.Record(fmt.Sprintf("%s stage: not staged %s/%s: %s; told the coordinator", at, JobsDir, r.p.Job, oneLine(ns.Error(), 400)))
				subject, body := StageJudgmentText(d.Friend, ns)
				l.tellKind(bus.KindBlocker, subject, body, now)
			}
		case errors.As(err, &away):
			d.stageRetry[r.p.Job] = now.Add(StageRetryEvery)
			if key := "away " + r.p.Job; !d.stageSaid[key] {
				d.stageSaid[key] = true
				d.Record(fmt.Sprintf("%s stage: skipped %s/%s: %s; its brief is written once its %s is there", at, JobsDir, r.p.Job, oneLine(away.Error(), 400), JobFile))
			}
		default:
			d.stageRetry[r.p.Job] = now.Add(StageRetryEvery)
			if key := "job " + r.p.Job + ": " + err.Error(); !d.stageSaid[key] {
				d.stageSaid[key] = true
				d.Record(fmt.Sprintf("%s stage: not staged %s/%s: %s; tried again in %s", at, JobsDir, r.p.Job, oneLine(err.Error(), 400), StageRetryEvery))
			}
		}
	}
	dl := d.delivery()
	for _, h := range cards {
		p, ok := PacketOf(h)
		if !ok || !validJob(p.Job) || d.staging[p.Job] || now.Before(d.stageRetry[p.Job]) || !dl.Owed(h) {
			continue
		}
		d.staging[p.Job] = true
		d.stageWG.Add(1)
		go func() {
			defer d.stageWG.Done()
			o := dl.One(l.ctx, h)
			d.stageMu.Lock()
			var away *StartedElsewhere // another hand's job is said whenever it is met
			d.stageDone = append(d.stageDone, stageResult{h: h, p: p, o: o, stopped: o.Err != nil && l.ctx.Err() != nil && !errors.As(o.Err, &away)})
			d.stageMu.Unlock()
		}()
	}
}
