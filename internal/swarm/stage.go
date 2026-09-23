package swarm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultStageTimeout is the hard timeout for card staging (120 s).
const DefaultStageTimeout = 120 * time.Second

// ErrStageTimeout is returned when card staging exceeds the hard timeout.
var ErrStageTimeout = errors.New("stage-timeout")

// ParseCardBase extracts base-repo and base-sha (or ref) from the card text.
// It checks the first 40 lines for lowercase base-repo: and base-sha:, matching SPEC-CARD reader 5.
func ParseCardBase(card []byte) (baseRepo, baseSha string, ok bool) {
	lines := strings.Split(string(card), "\n")
	if len(lines) > 40 {
		lines = lines[:40]
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "base-repo:") {
			baseRepo = strings.TrimSpace(strings.TrimPrefix(trimmed, "base-repo:"))
		} else if strings.HasPrefix(trimmed, "base-sha:") {
			baseSha = strings.TrimSpace(strings.TrimPrefix(trimmed, "base-sha:"))
		}
	}
	if baseRepo != "" {
		return baseRepo, baseSha, true
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "BASE:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "BASE:"))
			if at := strings.Index(val, "@"); at != -1 {
				shaCandidate := strings.TrimSpace(val[at+1:])
				if len(shaCandidate) >= 40 {
					baseSha = shaCandidate[:40]
				}
			}
		}
	}
	repos := CardCloneRepos(string(card))
	if len(repos) > 0 {
		return "https://github.com/" + repos[0] + ".git", baseSha, true
	}
	return "", "", false
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
	BaseSha  string
	Mirror   string
	Staged   bool
	TimedOut bool
	Wall     time.Duration
}

// StageCard stages the repository for a card into TargetDir using the bench mirror.
// If the card carries no base-repo or clone URLs, staging is skipped.
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
	baseRepo, baseSha, ok := ParseCardBase(opts.Card)
	if !ok || baseRepo == "" {
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
	cloneArgs := []string{"clone", "-q"}
	if mirror != "" {
		cloneArgs = append(cloneArgs, "--reference", mirror, "--dissociate")
	}
	cloneArgs = append(cloneArgs, cloneSource, opts.TargetDir)

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

	// Fetch and checkout the named ref / baseSha
	if baseSha != "" {
		catCmd := stageGit(ctx, "-C", opts.TargetDir, "cat-file", "-e", baseSha+"^{commit}")
		if err := catCmd.Run(); err != nil {
			// Commit not present locally, fetch from origin
			fetchCmd := stageGit(ctx, "-C", opts.TargetDir, "fetch", "-q", "origin", baseSha)
			if out, ferr := fetchCmd.CombinedOutput(); ferr != nil {
				if stageTimedOut(ctx, ferr) {
					_, _ = WriteStageTimeoutResult(opts.JobDir, bench, secs)
					return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Mirror: mirror, TimedOut: true, Wall: time.Since(start)}, ErrStageTimeout
				}
				return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Mirror: mirror, Wall: time.Since(start)}, fmt.Errorf("git fetch failed: %s (%w)", strings.TrimSpace(string(out)), ferr)
			}
		}

		coCmd := stageGit(ctx, "-C", opts.TargetDir, "checkout", "-q", baseSha)
		if out, cerr := coCmd.CombinedOutput(); cerr != nil {
			if stageTimedOut(ctx, cerr) {
				_, _ = WriteStageTimeoutResult(opts.JobDir, bench, secs)
				return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Mirror: mirror, TimedOut: true, Wall: time.Since(start)}, ErrStageTimeout
			}
			return StageResult{BaseRepo: baseRepo, BaseSha: baseSha, Mirror: mirror, Wall: time.Since(start)}, fmt.Errorf("git checkout failed: %s (%w)", strings.TrimSpace(string(out)), cerr)
		}
	}

	_ = exec.Command("git", "-C", opts.TargetDir, "config", "user.name", "Rowan").Run()
	_ = exec.Command("git", "-C", opts.TargetDir, "config", "user.email", "rowan@mas-bandwidth.com").Run()

	return StageResult{
		BaseRepo: baseRepo,
		BaseSha:  baseSha,
		Mirror:   mirror,
		Staged:   true,
		Wall:     time.Since(start),
	}, nil
}
