package swarm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testgit"
)

// mirrorOrigin makes a throwaway origin with one commit on dev and returns its path;
// commitTo adds one more and returns the new tip.
func mirrorOrigin(t *testing.T, root string) string {
	t.Helper()
	src := filepath.Join(root, "origin")
	require.NoError(t, os.MkdirAll(src, 0o755))
	mirrorExec(t, src, "git", "init", "-q")
	mirrorExec(t, src, "git", "checkout", "-q", "-b", "dev")
	mirrorExec(t, src, "git", "config", "user.name", "test")
	mirrorExec(t, src, "git", "config", "user.email", "test@example.com")
	mirrorCommit(t, src, "one\n")
	return src
}

func mirrorCommit(t *testing.T, src, body string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(src, "file.txt"), []byte(body), 0o644))
	mirrorExec(t, src, "git", "add", "file.txt")
	mirrorExec(t, src, "git", "commit", "-q", "-m", "commit "+body)
	return strings.TrimSpace(mirrorExec(t, src, "git", "rev-parse", "HEAD"))
}

// recorder is a CmdRunner that records each command and runs none.
type recorder struct{ calls []string }

func (r *recorder) run(_ context.Context, dir string, env []string, name string, args ...string) error {
	gocache := ""
	for _, e := range env {
		if strings.HasPrefix(e, "GOCACHE=") {
			gocache = e
		}
	}
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " ")+" ["+gocache+"]")
	return nil
}

// TestJobsCloneFromAWarmMirror: with a temporary origin the mirror is created and then
// refreshed, a job's stage references it and holds the tip, the cache is warmed once per
// new tip, and without a mirror the card's clone step falls back to the remote
// (docs/SPEC-WORKER.md, the warm clones and caches card).
func TestJobsCloneFromAWarmMirror(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	origin := mirrorOrigin(t, root)
	benchHome := filepath.Join(root, "home")
	rec := &recorder{}
	k := MirrorKeeper{
		Origin: origin, Mirror: MirrorPath(benchHome, "nova-tools"), Ref: "dev",
		WarmDir: filepath.Join(root, "warm"), GoCache: filepath.Join(root, "gocache"), Run: rec.run,
	}
	ctx := context.Background()

	t.Run("the mirror is created and keeps its objects", func(t *testing.T) {
		res, err := k.Refresh(ctx)
		require.NoError(t, err)
		assert.True(t, res.Created)
		assert.Equal(t, strings.TrimSpace(mirrorExec(t, origin, "git", "rev-parse", "dev")), res.Tip)
		assert.Equal(t, "0", strings.TrimSpace(mirrorExec(t, k.Mirror, "git", "config", "gc.auto")))
		assert.Equal(t, k.Mirror, FindBenchMirror(benchHome, "mas-bandwidth/nova-tools"))
	})
	t.Run("the cache is warmed at the tip, low priority, in the member's cache", func(t *testing.T) {
		require.Len(t, rec.calls, 2, "%q", rec.calls)
		assert.Equal(t, "nice -n 19 go build ./... [GOCACHE="+k.GoCache+"]", rec.calls[0])
		assert.Equal(t, "nice -n 19 go vet ./... [GOCACHE="+k.GoCache+"]", rec.calls[1])
	})
	t.Run("a refresh with no new commit warms nothing", func(t *testing.T) {
		res, err := k.Refresh(ctx)
		require.NoError(t, err)
		assert.False(t, res.Created)
		assert.False(t, res.Moved)
		assert.Len(t, rec.calls, 2)
	})
	t.Run("a refresh fetches a landing and warms again", func(t *testing.T) {
		tip := mirrorCommit(t, origin, "two\n")
		res, err := k.Refresh(ctx)
		require.NoError(t, err)
		assert.True(t, res.Moved)
		assert.Equal(t, tip, res.Tip)
		assert.Equal(t, tip, strings.TrimSpace(mirrorExec(t, k.Mirror, "git", "rev-parse", "dev")))
		assert.Len(t, rec.calls, 4)
	})
	t.Run("a job's stage references the mirror and has the tip", func(t *testing.T) {
		tip := strings.TrimSpace(mirrorExec(t, origin, "git", "rev-parse", "dev"))
		jobDir := filepath.Join(root, "jobs", "card-1")
		target := filepath.Join(jobDir, "repo")
		card := []byte("RESULT: warm-card sha=000000000000\nREPO: mas-bandwidth/nova-tools\nBASE: dev\n")
		res, err := StageCard(StageOptions{Card: card, TargetDir: target, JobDir: jobDir, BenchHome: benchHome, BenchName: "b", Timeout: 30 * time.Second})
		require.NoError(t, err)
		assert.Equal(t, k.Mirror, res.Mirror)
		assert.Equal(t, tip, strings.TrimSpace(mirrorExec(t, target, "git", "rev-parse", "HEAD")))
		alt, err := os.ReadFile(filepath.Join(target, ".git", "objects", "info", "alternates"))
		require.NoError(t, err)
		assert.Contains(t, string(alt), filepath.Join(k.Mirror, "objects"))
	})
	t.Run("the card a member hands its harness names the bench mirror, not the harness's HOME", func(t *testing.T) {
		jobDir := filepath.Join(root, "step-with")
		step1Clone(t, jobDir, origin, benchHome)
		alt, err := os.ReadFile(filepath.Join(jobDir, ".git", "objects", "info", "alternates"))
		require.NoError(t, err)
		assert.Contains(t, string(alt), filepath.Join(k.Mirror, "objects"))
		assert.Equal(t, "no home to rewrite", PointCardAtMirrors("no home to rewrite", ""))
	})
	t.Run("without a mirror the card's clone step is a plain clone of the remote", func(t *testing.T) {
		jobDir := filepath.Join(root, "step-without")
		step1Clone(t, jobDir, origin, "")
		_, err := os.Stat(filepath.Join(jobDir, ".git", "objects", "info", "alternates"))
		assert.True(t, os.IsNotExist(err), "no mirror, no alternates: %v", err)
		assert.Equal(t, strings.TrimSpace(mirrorExec(t, origin, "git", "rev-parse", "dev")), strings.TrimSpace(mirrorExec(t, jobDir, "git", "rev-parse", "HEAD")))
	})
}

// step1Clone runs the pulse fix card's own STEP 1 line in dir with the remote swapped for
// origin, the card written the way a member writes it (PointCardAtMirrors with benchHome; ""
// leaves $HOME in it) and run in a bare environment whose HOME is a directory with no mirror
// in it, as a harness's HOME is the slot's.
func step1Clone(t *testing.T, dir, origin, benchHome string) {
	t.Helper()
	var line string
	for _, l := range strings.Split(PointCardAtMirrors(pulseFix, benchHome), "\n") {
		if strings.HasPrefix(l, "STEP 1. ") {
			line = strings.TrimPrefix(l, "STEP 1. ")
		}
	}
	require.NotEmpty(t, line)
	// the card's mkdir of scratch is not the clone's concern: the clone lands in an empty dir
	for _, word := range strings.Fields(line) {
		if strings.HasPrefix(word, "https://") { // the card's remote, swapped for the throwaway origin
			line = strings.Replace(line, word, origin, 1)
		}
	}
	line = strings.NewReplacer("mkdir -p scratch && ", "", "<source>", "mas-bandwidth/nova-tools", "<branch>", "work").Replace(line)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	cmd := exec.Command("sh", "-c", line)
	cmd.Dir = dir
	cmd.Env = testgit.Environ("HOME="+t.TempDir(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s\n%s", line, out)
}

func mirrorExec(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = testgit.Environ("GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s %s in %s: %v\n%s", name, strings.Join(args, " "), dir, err, string(out))
	return string(out)
}
