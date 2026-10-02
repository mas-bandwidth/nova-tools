//go:build functional

package swarm

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// Each child writes a private overlay beside its checkout. PrepareLispJobCache seeds it
// from the shared exact-tip output before launch.
func TestCacheEnvCarriesAPrivateLispOverlay(t *testing.T) {
	t.Parallel()

	source, _ := prewarmFixture(t)
	root := t.TempDir()
	if err := EnsureCacheDirs(root); err != nil {
		t.Fatal(err)
	}
	env := CacheEnv(root, source)
	if !envHasPrefix(env, "ASDF_OUTPUT_TRANSLATIONS=") {
		t.Fatalf("CacheEnv has no ASDF_OUTPUT_TRANSLATIONS: %v", env)
	}
	if value := cacheEnvValue(t, env, "ASDF_OUTPUT_TRANSLATIONS"); !strings.Contains(value, filepath.ToSlash(JobLispCacheDir(source))) || !strings.Contains(value, filepath.ToSlash(source)) {
		t.Fatalf("ASDF_OUTPUT_TRANSLATIONS=%q, want source %s mapped to private overlay %s", value, source, JobLispCacheDir(source))
	}
}

func prewarmFixture(t *testing.T) (string, string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source")
	mustWrite(t, filepath.Join(source, "go.mod"), "module example.com/prewarm\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(source, "main.go"), "package prewarm\n")
	gitT(t, "", "init", "-q", "-b", "dev", source)
	gitT(t, source, "add", "-A")
	gitT(t, source, "commit", "-q", "-m", "fixture")
	return source, gitT(t, source, "rev-parse", "HEAD")
}

func envHasPrefix(env []string, prefix string) bool {
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_TERMINAL_PROMPT=0",
	)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, errb.String())
	}
	return strings.TrimSpace(out.String())
}
