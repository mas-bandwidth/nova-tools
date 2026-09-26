package swarm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultStageTimeout is the hard timeout for card staging (120 s).
const DefaultStageTimeout = 120 * time.Second

// ErrStageTimeout is returned when card staging exceeds the hard timeout.
var ErrStageTimeout = errors.New("stage-timeout")

// CardBase is what a card's header says to stage into <job>/repo (nova-tools#3711).
type CardBase struct {
	Repo  string // the clone URL (or a local path) to stage; "" when the card names none that can be read
	Sha   string // base-sha: (or the sha of BASE: <ref>@<sha40>); "" when absent
	Ref   string // BASE: <ref> without @; the ref checked out when there is no sha
	Named string // the raw value of the card's base-repo: or REPO: line, even one Repo could not be read from
}

// cardRepoNameRE is the owner/name a REPO: line carries.
var cardRepoNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// cardLabelRE is the card id line 1 carries (RESULT: <label> sha=<sha12>).
var cardLabelRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// CardRepoURL is the clone URL a REPO: value names: a URL or a local path as written,
// an owner/name as the forge's https URL ending .git (the shape base-repo: lines and the
// URL fallback carry, so FindBenchMirror resolves all three to ~/nova-bench/mirror/<name>.git).
// "" when the value is none of those.
func CardRepoURL(value string) string {
	v := strings.TrimSpace(value)
	switch {
	case v == "" || v == "-" || strings.EqualFold(v, "none"):
		return ""
	case isRemoteRepo(v) || strings.HasPrefix(v, "/") || strings.Contains(v, "://"):
		return v
	case cardRepoNameRE.MatchString(v):
		return defaultProbeBase + "/" + strings.TrimSuffix(v, ".git") + ".git"
	}
	return ""
}

// ReadCardBase is THE reader of which repository a card works in, at which sha: staging
// (StageCard) and the push-time lint (internal/nsprint/card) both call it, so the repo a card
// is admitted with is the repo it is staged from. It reads the first 40 lines. Precedence:
// `BASE-REPO: <url>`, then `REPO: <owner>/<name>` (the header every pushed card carries), then
// the first github clone URL anywhere in the card (CardCloneRepos). The sha is `BASE-SHA:`,
// else the sha of `BASE: <ref>@<sha40>`; the ref is BASE:'s value before any @. The retired
// lowercase spellings (base-repo:, base-sha:) are still read, so a card cut before
// nova-tools#4352 A stages.
func ReadCardBase(card []byte) CardBase {
	lines := strings.Split(string(card), "\n")
	if len(lines) > 40 {
		lines = lines[:40]
	}
	var b CardBase
	var baseRepo, repoLine string
	var sawRepoLine bool
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "BASE-REPO:"):
			baseRepo = strings.TrimSpace(strings.TrimPrefix(trimmed, "BASE-REPO:"))
		case strings.HasPrefix(trimmed, "base-repo:"): // the retired spelling, still read (#4352 A)
			baseRepo = strings.TrimSpace(strings.TrimPrefix(trimmed, "base-repo:"))
		case strings.HasPrefix(trimmed, "BASE-SHA:"):
			b.Sha = strings.TrimSpace(strings.TrimPrefix(trimmed, "BASE-SHA:"))
		case strings.HasPrefix(trimmed, "base-sha:"): // the retired spelling, still read
			b.Sha = strings.TrimSpace(strings.TrimPrefix(trimmed, "base-sha:"))
		case strings.HasPrefix(trimmed, "REPO:") && !sawRepoLine:
			sawRepoLine = true
			repoLine = strings.TrimSpace(strings.TrimPrefix(trimmed, "REPO:"))
		case strings.HasPrefix(trimmed, "BASE:") && b.Ref == "":
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "BASE:"))
			ref, sha, hasAt := strings.Cut(val, "@")
			b.Ref = strings.TrimSpace(ref)
			if hasAt && b.Sha == "" {
				if s := strings.TrimSpace(sha); len(s) >= 40 {
					b.Sha = s[:40]
				}
			}
		}
	}
	switch {
	case baseRepo != "":
		b.Repo, b.Named = baseRepo, baseRepo
	case CardRepoURL(repoLine) != "":
		b.Repo, b.Named = CardRepoURL(repoLine), repoLine
	default:
		if repos := CardCloneRepos(string(card)); len(repos) > 0 {
			b.Repo = defaultProbeBase + "/" + repos[0] + ".git"
			b.Named = repos[0]
		} else if repoLine != "-" && !strings.EqualFold(repoLine, "none") {
			// A REPO: line no reader can resolve still NAMES a repo: staging must refuse
			// it (no-repo-staged), never launch the model into an empty job dir.
			b.Named = repoLine
		}
	}
	return b
}

// ParseCardBase extracts the base repo and base sha from the card text (ReadCardBase).
// ok is false when the card names no repo that can be staged.
func ParseCardBase(card []byte) (baseRepo, baseSha string, ok bool) {
	b := ReadCardBase(card)
	if b.Repo == "" {
		return "", "", false
	}
	return b.Repo, b.Sha, true
}

// CardNamesRepo reports whether the card names a repository at all, readable or not: such
// a card that ends with nothing staged is a staging failure (STAGE FAIL reason=no-repo-staged).
func CardNamesRepo(card []byte) bool {
	return ReadCardBase(card).Named != ""
}

// CardStageBranch is the branch the staged checkout is on, so the card commits on a named
// branch rather than a detached HEAD: rowan/<label> from line 1 (RESULT: <label> sha=...),
// rowan/card when line 1 names no label.
func CardStageBranch(card []byte) string {
	first, _, _ := strings.Cut(string(card), "\n")
	first = strings.TrimSpace(first)
	first = strings.TrimPrefix(first, "RESULT:")
	first = strings.TrimPrefix(first, "RESULT")
	if f := strings.Fields(first); len(f) > 0 && cardLabelRE.MatchString(f[0]) {
		return "rowan/" + f[0]
	}
	return "rowan/card"
}

// FindBenchMirror finds the path to the bench's local mirror for baseRepo.
// Candidate locations:
// - baseRepo itself, if it is a directory on disk that exists
// - $HOME/nova-bench/mirror/<repo>.git
// - $HOME/nova-bench/mirror/<repo>
// - /tmp/<repo>-mirror.git
// - /tmp/<repo>.git
func FindBenchMirror(benchHome, baseRepo string) string {
	if baseRepo == "" {
		return ""
	}
	if fi, err := os.Stat(baseRepo); err == nil && fi.IsDir() {
		if isGitDir(baseRepo) {
			return baseRepo
		}
	}
	if benchHome == "" {
		benchHome = os.Getenv("HOME")
		if benchHome == "" {
			benchHome, _ = os.UserHomeDir()
		}
	}
	repoName := filepath.Base(baseRepo)
	repoName = strings.TrimSuffix(repoName, ".git")
	repoName = strings.TrimSuffix(repoName, "-mirror")

	var candidates []string
	if benchHome != "" {
		candidates = append(candidates,
			filepath.Join(benchHome, "nova-bench", "mirror", repoName+".git"),
			filepath.Join(benchHome, "nova-bench", "mirror", repoName),
		)
	}
	candidates = append(candidates,
		filepath.Join("/tmp", repoName+"-mirror.git"),
		filepath.Join("/tmp", repoName+".git"),
	)
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			if isGitDir(c) {
				return c
			}
		}
	}
	return ""
}

// MirrorCloneArgs is the git argv of the one staging convention, for cards (StageCard) and
// for ci run (internal/nsprint/ci): a local clone of the bench mirror that borrows its
// objects and dissociates, so staging never reads the network and a later gc of the
// mirror cannot take objects from under the clone. noCheckout leaves the worktree empty
// for a caller that checks out an exact sha next.
func MirrorCloneArgs(mirror, target string, noCheckout bool) []string {
	args := []string{"clone", "-q"}
	if noCheckout {
		args = append(args, "--no-checkout")
	}
	return append(args, "--reference", mirror, "--dissociate", mirror, target)
}

func isGitDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	return false
}

func isRemoteRepo(repo string) bool {
	return strings.HasPrefix(repo, "https://") ||
		strings.HasPrefix(repo, "http://") ||
		strings.HasPrefix(repo, "git@") ||
		strings.HasPrefix(repo, "ssh://")
}

// WriteStageTimeoutResult writes the RESULT.md for a stage timeout:
// RESULT: BLOCKED stage-timeout <bench> <secs>
func WriteStageTimeoutResult(jobDir, bench string, secs int) (string, error) {
	if jobDir == "" {
		return "", nil
	}
	dest := filepath.Join(jobDir, "RESULT.md")
	body := fmt.Sprintf("RESULT: BLOCKED stage-timeout %s %d\nblocked: staging timed out after %ds\nwritten-by: nova-swarm native (the card published no report of its own)\n",
		bench, secs, secs)
	if err := os.WriteFile(dest, []byte(body), 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

// stageWaitDelay bounds how long a staging git call waits for its pipes after the deadline
// kill. git clone runs helpers (git-remote-https, index-pack) that inherit the output pipe;
// killing git alone left them holding it, which is how hulk had 193 clones stuck 43-65
// minutes (#2882). Each call leads its own process group, the deadline kills the group, and
// WaitDelay closes the pipes a bounded time after that, so the 120 s timeout is hard.
const stageWaitDelay = 2 * time.Second

// stageGit builds one staging git call under ctx: its own process group, killed as a group
// when ctx ends, no terminal prompt.
func stageGit(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	ownGroup(cmd)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			KillGroup(cmd.Process.Pid, "")
		}
		return nil
	}
	cmd.WaitDelay = stageWaitDelay
	return cmd
}

// stageTimedOut reports whether a staging call ended because the deadline passed.
func stageTimedOut(ctx context.Context, err error) bool {
	return ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, exec.ErrWaitDelay)
}

// StageOptions describes a card staging request.
type StageOptions struct {
	Card      []byte
	TargetDir string // e.g. <jobDir>/repo
	JobDir    string // <jobDir>, where RESULT.md is written on timeout
	BenchHome string
	BenchName string
	Timeout   time.Duration
}

// StageResult is the outcome of a staging operation.
type StageResult struct {
	BaseRepo string
	BaseSha  string // the card's base sha; once staged, the full sha <job>/repo's HEAD is at
	Ref      string // the card's BASE: ref, checked out when it names no sha
	Branch   string // the branch the staged checkout is on (CardStageBranch)
	Mirror   string
	Staged   bool
	TimedOut bool
	Wall     time.Duration
}

// StageCard stages the repository for a card into TargetDir using the bench mirror.
// The repo, sha and ref are ReadCardBase's (base-repo:, REPO:, or a clone URL; base-sha:
// or BASE:). If the card names no repo that can be read, staging is skipped and Staged is
// false; the caller refuses such a card when CardNamesRepo says it named one (#3711).
// The checkout is at the sha (else the ref, else the clone's default head) on the branch
// CardStageBranch names, so the card commits on a branch, not a detached HEAD.
// If base-repo is remote and no bench mirror is found, staging fails without contacting GitHub.
// Staging runs git clone --reference <mirror> and git fetch/checkout <base-sha> with a hard timeout (default 120s).
// If the timeout expires, it writes RESULT.md:
//
//	RESULT: BLOCKED stage-timeout <bench> <secs>
//
// and returns ErrStageTimeout.
func StageCard(opts StageOptions) (StageResult, error) {
	if len(opts.Card) == 0 {
		return StageResult{}, nil
	}
	cb := ReadCardBase(opts.Card)
	baseRepo, baseSha := cb.Repo, cb.Sha
	if baseRepo == "" {
		return StageResult{}, nil
	}
	if isGitDir(opts.TargetDir) {
		return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Staged: true}, nil
	}

	bench := opts.BenchName
	if bench == "" {
		if h, err := os.Hostname(); err == nil {
			if idx := strings.Index(h, "."); idx != -1 {
				h = h[:idx]
			}
			bench = h
		}
	}
	if bench == "" {
		bench = "bench"
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultStageTimeout
	}
	secs := int(timeout.Seconds())
	if secs <= 0 {
		secs = 1
	}

	mirror := FindBenchMirror(opts.BenchHome, baseRepo)
	if mirror == "" && isRemoteRepo(baseRepo) {
		return StageResult{BaseRepo: baseRepo, BaseSha: baseSha}, fmt.Errorf("staging refused: no bench mirror for %s: a card may not clone directly from github without a bench mirror", baseRepo)
	}

	cloneSource := mirror
	if cloneSource == "" {
		cloneSource = baseRepo
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	cloneArgs := []string{"clone", "-q", cloneSource, opts.TargetDir}
	if mirror != "" {
		cloneArgs = MirrorCloneArgs(mirror, opts.TargetDir, false)
	}

	cloneCmd := stageGit(ctx, cloneArgs...)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		if stageTimedOut(ctx, err) {
			_, _ = WriteStageTimeoutResult(opts.JobDir, bench, secs)
			return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Mirror: mirror, TimedOut: true, Wall: time.Since(start)}, ErrStageTimeout
		}
		return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Mirror: mirror, Wall: time.Since(start)}, fmt.Errorf("git clone failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	// Update remote origin to baseRepo if cloned from mirror
	if cloneSource != baseRepo {
		remCmd := stageGit(ctx, "-C", opts.TargetDir, "remote", "set-url", "origin", baseRepo)
		_ = remCmd.Run()
	}

	fail := func(what string, out []byte, err error) (StageResult, error) {
		if stageTimedOut(ctx, err) {
			_, _ = WriteStageTimeoutResult(opts.JobDir, bench, secs)
			return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Ref: cb.Ref, Mirror: mirror, TimedOut: true, Wall: time.Since(start)}, ErrStageTimeout
		}
		return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Ref: cb.Ref, Mirror: mirror, Wall: time.Since(start)}, fmt.Errorf("git %s failed: %s (%w)", what, strings.TrimSpace(string(out)), err)
	}

	// Fetch and checkout baseSha (else the BASE: ref) on the card's branch.
	branch := CardStageBranch(opts.Card)
	switch {
	case baseSha != "":
		catCmd := stageGit(ctx, "-C", opts.TargetDir, "cat-file", "-e", baseSha+"^{commit}")
		if err := catCmd.Run(); err != nil {
			// Commit not present locally, fetch from origin
			fetchCmd := stageGit(ctx, "-C", opts.TargetDir, "fetch", "-q", "origin", baseSha)
			if out, ferr := fetchCmd.CombinedOutput(); ferr != nil {
				return fail("fetch", out, ferr)
			}
		}
		coCmd := stageGit(ctx, "-C", opts.TargetDir, "checkout", "-q", "-B", branch, baseSha)
		if out, cerr := coCmd.CombinedOutput(); cerr != nil {
			return fail("checkout", out, cerr)
		}
	case cb.Ref != "":
		// The clone's remote-tracking ref first (the mirror's branch), then the ref as
		// written (a tag or a sha the clone holds).
		coCmd := stageGit(ctx, "-C", opts.TargetDir, "checkout", "-q", "-B", branch, "origin/"+cb.Ref)
		if out, cerr := coCmd.CombinedOutput(); cerr != nil {
			if stageTimedOut(ctx, cerr) {
				return fail("checkout", out, cerr)
			}
			coCmd = stageGit(ctx, "-C", opts.TargetDir, "checkout", "-q", "-B", branch, cb.Ref)
			if out, cerr := coCmd.CombinedOutput(); cerr != nil {
				return fail("checkout", out, cerr)
			}
		}
	default:
		coCmd := stageGit(ctx, "-C", opts.TargetDir, "checkout", "-q", "-B", branch)
		if out, cerr := coCmd.CombinedOutput(); cerr != nil {
			return fail("checkout", out, cerr)
		}
	}
	headCmd := stageGit(ctx, "-C", opts.TargetDir, "rev-parse", "HEAD")
	headOut, herr := headCmd.Output()
	if herr != nil {
		return fail("rev-parse", headOut, herr)
	}
	head := strings.TrimSpace(string(headOut))

	_ = exec.Command("git", "-C", opts.TargetDir, "config", "user.name", "Rowan").Run()
	_ = exec.Command("git", "-C", opts.TargetDir, "config", "user.email", "rowan@mas-bandwidth.com").Run()

	return StageResult{
		BaseRepo: baseRepo,
		BaseSha:  head,
		Ref:      cb.Ref,
		Branch:   branch,
		Mirror:   mirror,
		Staged:   true,
		Wall:     time.Since(start),
	}, nil
}
