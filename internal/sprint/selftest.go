package sprint

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// SelftestLandOption configures SelftestLand.
type SelftestLandOption func(*selftestLandConfig)

type selftestLandConfig struct {
	binary  string
	scratch string
	lander  func(ctx context.Context, repoDir string) error
	stdout  io.Writer
	stderr  io.Writer
}

// WithSelftestBinary sets the binary to test with selftest land.
func WithSelftestBinary(bin string) SelftestLandOption {
	return func(c *selftestLandConfig) { c.binary = bin }
}

// WithSelftestScratch sets the scratch directory for selftest land.
func WithSelftestScratch(dir string) SelftestLandOption {
	return func(c *selftestLandConfig) { c.scratch = dir }
}

// WithSelftestOutput sets the stdout and stderr destinations.
func WithSelftestOutput(stdout, stderr io.Writer) SelftestLandOption {
	return func(c *selftestLandConfig) {
		c.stdout = stdout
		c.stderr = stderr
	}
}

// SelftestLand lands a canned card on a scratch clone with binary (docs/SPEC-SPRINT.md section 14).
// Run by any install before switching: green on a good binary and red on a broken lander (item 14).
func SelftestLand(ctx context.Context, opts ...SelftestLandOption) error {
	cfg := &selftestLandConfig{
		stdout: io.Discard,
		stderr: io.Discard,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	scratch := cfg.scratch
	cleanScratch := false
	if scratch == "" {
		tmp, err := os.MkdirTemp("", "nova-sprint-selftest-*")
		if err != nil {
			return fmt.Errorf("selftest land: cannot create scratch directory: %w", err)
		}
		scratch = tmp
		cleanScratch = true
	} else {
		if err := os.MkdirAll(scratch, 0o755); err != nil {
			return fmt.Errorf("selftest land: cannot create scratch directory: %w", err)
		}
	}
	if cleanScratch {
		defer func() {
			// ignored: best-effort scratch cleanup
			_ = safepath.RemoveUnder(filepath.Dir(scratch), scratch)
		}()
	}

	originDir := filepath.Join(scratch, "origin.git")
	workDir := filepath.Join(scratch, "work")

	// Set up git environment isolated from host
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=selftest",
		"GIT_AUTHOR_EMAIL=selftest@example.com",
		"GIT_COMMITTER_NAME=selftest",
		"GIT_COMMITTER_EMAIL=selftest@example.com",
	)

	gitRun := func(dir string, args ...string) (string, error) {
		cmd := subproc.Context(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	// 1. Initialize bare origin repository
	if out, err := gitRun(scratch, "init", "-q", "--bare", "-b", "main", originDir); err != nil {
		return fmt.Errorf("selftest land: git init bare failed: %s: %w", out, err)
	}

	// 2. Clone origin into work directory
	if out, err := gitRun(scratch, "clone", "-q", originDir, workDir); err != nil {
		return fmt.Errorf("selftest land: git clone failed: %s: %w", out, err)
	}

	// 3. Create initial commit on main
	readmePath := filepath.Join(workDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("selftest land base\n"), 0o644); err != nil {
		return fmt.Errorf("selftest land: write README.md failed: %w", err)
	}
	if out, err := gitRun(workDir, "add", "README.md"); err != nil {
		return fmt.Errorf("selftest land: git add failed: %s: %w", out, err)
	}
	if out, err := gitRun(workDir, "commit", "-q", "-m", "initial base commit"); err != nil {
		return fmt.Errorf("selftest land: git commit failed: %s: %w", out, err)
	}
	if out, err := gitRun(workDir, "push", "-q", "origin", "HEAD:main"); err != nil {
		return fmt.Errorf("selftest land: git push main failed: %s: %w", out, err)
	}

	// 4. Create canned card branch and commit
	cardBranch := "sprint/selftest-canned-1"
	if out, err := gitRun(workDir, "switch", "-q", "-c", cardBranch); err != nil {
		return fmt.Errorf("selftest land: git switch -c failed: %s: %w", out, err)
	}
	cannedPath := filepath.Join(workDir, "canned.txt")
	if err := os.WriteFile(cannedPath, []byte("canned card content\n"), 0o644); err != nil {
		return fmt.Errorf("selftest land: write canned.txt failed: %w", err)
	}
	if out, err := gitRun(workDir, "add", "canned.txt"); err != nil {
		return fmt.Errorf("selftest land: git add canned.txt failed: %s: %w", out, err)
	}
	if out, err := gitRun(workDir, "commit", "-q", "-m", "canned card work"); err != nil {
		return fmt.Errorf("selftest land: git commit canned card failed: %s: %w", out, err)
	}
	headSha, err := gitRun(workDir, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("selftest land: git rev-parse failed: %s: %w", headSha, err)
	}
	if out, err := gitRun(workDir, "push", "-q", "origin", "HEAD:"+cardBranch); err != nil {
		return fmt.Errorf("selftest land: git push canned card failed: %s: %w", out, err)
	}

	// Switch work clone back to main
	if out, err := gitRun(workDir, "switch", "-q", "main"); err != nil {
		return fmt.Errorf("selftest land: git switch main failed: %s: %w", out, err)
	}

	// 5. Land the canned card
	if cfg.lander != nil {
		if err := cfg.lander(ctx, workDir); err != nil {
			return fmt.Errorf("selftest land: lander failed: %w", err)
		}
	} else {
		bin := cfg.binary
		if bin == "" {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("selftest land: cannot determine executable: %w", err)
			}
			bin = exe
		}
		absBin, err := filepath.Abs(bin)
		if err != nil {
			return fmt.Errorf("selftest land: cannot make binary path absolute: %w", err)
		}
		bin = absBin

		twinFile := filepath.Join(scratch, "selftest.twin")
		redisAddr := "mem:" + twinFile

		runBin := func(args ...string) (string, error) {
			cmd := subproc.Context(ctx, bin, args...)
			cmd.Dir = workDir
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			return strings.TrimSpace(string(out)), err
		}

		briefPath := filepath.Join(scratch, "canned-brief.md")
		cannedBrief := "tier: flash\nPATHS: canned.txt\n\nCanned card for selftest land.\n\nRULES.\nWork only in the job directory this card names.\nNever force-push or rebase a shared branch.\nNever kill a process you did not start.\nNever start a server on this machine.\nNo `rm -rf` outside the job directory.\nReport what was not done.\n"
		if err := os.WriteFile(briefPath, []byte(cannedBrief), 0o644); err != nil {
			return fmt.Errorf("selftest land: write canned-brief.md failed: %w", err)
		}

		// Run through standard coordinator/worker lifecycle to queue canned card in merge
		commands := [][]string{
			{"init", "--readers", "reader-a", "--members", "m1:1", "--redis", redisAddr, "--actor", "boss"},
			{"add", "--stream", "selftest", "--count", "1", "--brief-file", briefPath, "--redis", redisAddr, "--actor", "boss"},
			{"start", "--redis", redisAddr, "--actor", "boss"},
			{"tick", "--redis", redisAddr, "--actor", "boss"},
			{"tick", "--redis", redisAddr, "--actor", "boss"},
			{"take", "--as", "m1", "--epoch", "0", "--redis", redisAddr, "--actor", "m1"},
			{"finish", "--as", "m1", "selftest-1.w1@1", "--epoch", "0", "--head", headSha, "--branch", cardBranch, "--report", "canned done", "--redis", redisAddr, "--actor", "m1"},
			{"tick", "--redis", redisAddr, "--actor", "boss"},
			{"read", "--as", "reader-a", "--begin", "--epoch", "0", "--redis", redisAddr, "--actor", "reader-a"},
			{"read", "--as", "reader-a", "--ok", "--epoch", "0", "--redis", redisAddr, "--actor", "reader-a"},
			{"tick", "--redis", redisAddr, "--actor", "boss"},
			{"accept", "--read-ok", "--redis", redisAddr, "--actor", "boss"},
			{"land", "--stream", "selftest", "--repo-dir", workDir, "--base", "main", "--redis", redisAddr, "--actor", "boss", "--check", "true"},
		}

		for _, argv := range commands {
			out, err := runBin(argv...)
			if err != nil {
				return fmt.Errorf("selftest land: command %v failed: %s: %w", argv, out, err)
			}
		}
	}

	// Verify that the landing commit is on main in origin
	logOut, err := gitRun(originDir, "log", "--format=%s", "main")
	if err != nil {
		return fmt.Errorf("selftest land: git log origin main failed: %s: %w", logOut, err)
	}
	if cfg.lander == nil && !strings.Contains(logOut, "land selftest-1") {
		return fmt.Errorf("selftest land: landing commit not found in origin main: %s", logOut)
	}

	return nil
}
