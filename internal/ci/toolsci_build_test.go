package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// redisInstallCall is the workflow line that installs redis-server: the verb, called
// on the binary every job builds once.
const redisInstallCall = `"$RUNNER_TEMP/ci" install-redis-server`

// ciExe is the binary's name on a job whose runner may be Windows: Go names a
// Windows executable with .exe, so the build and every call name it that way
// rather than leaning on the shell to find ci.exe for "ci".
const ciExe = `"$RUNNER_TEMP/ci${{ runner.os == 'Windows' && '.exe' || '' }}"`

// TestToolsCIIsBuiltOnceAndNeverRun holds every workflow to the one way a job
// reaches tools/ci: the job builds the verbs ONCE, in an early step after its Go
// setup, and every later step calls the binary at "$RUNNER_TEMP/ci". `go run
// ./tools/ci` compiles and links the tool again on every step that names it, and
// a runner under load pays for each. On a GitHub-hosted runner the job also
// restores and saves the Go build cache (actions/cache, or setup-go's own), so
// the build is a link and not a compile; a self-hosted runner is persistent and
// keeps its cache on disk.
//
// A job that calls the binary and never builds it, builds it after a step that
// calls it, or builds it twice, is red; so is any `go run ./tools/ci`.
func TestToolsCIIsBuiltOnceAndNeverRun(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	files, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	called := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		var wf struct {
			Jobs map[string]struct {
				RunsOn   any `yaml:"runs-on"`
				Strategy struct {
					Matrix struct {
						OS any `yaml:"os"`
					} `yaml:"matrix"`
				} `yaml:"strategy"`
				Steps    []struct {
					Name string         `yaml:"name"`
					Uses string         `yaml:"uses"`
					Run  string         `yaml:"run"`
					With map[string]any `yaml:"with"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		require.NoError(t, yaml.Unmarshal(raw, &wf), f)
		for name, job := range wf.Jobs {
			where := filepath.Base(f) + " job " + name
			builds, firstCall := 0, -1
			// A job whose runner can be Windows names the binary with ciExe.
			mayBeWindows := strings.Contains(asText(job.RunsOn), "windows") ||
				(strings.Contains(asText(job.RunsOn), "matrix.os") && strings.Contains(asText(job.Strategy.Matrix.OS), "windows"))
			cacheRestore, cacheSave := false, false
			for i, s := range job.Steps {
				assert.NotContains(t, s.Run, "go run ./tools/ci", "%s step %q runs tools/ci with go run; call the binary built once", where, s.Name)
				for _, line := range strings.Split(s.Run, "\n") {
					l := strings.TrimSpace(line)
					switch {
					case strings.HasPrefix(l, `go build -o "$RUNNER_TEMP/ci" ./tools/ci`), strings.HasPrefix(l, `go -C .revert-tool build -o "$RUNNER_TEMP/ci" ./tools/ci`),
						strings.HasPrefix(l, `go build -o `+ciExe+` ./tools/ci`):
						builds++
						assert.Equal(t, mayBeWindows, strings.Contains(l, ciExe), "%s builds tools/ci as %q; a job that may run on Windows names it %s, any other \"$RUNNER_TEMP/ci\"", where, l, ciExe)
						assert.Equal(t, -1, firstCall, "%s builds tools/ci after step %d already calls it", where, firstCall)
					case strings.Contains(l, `"$RUNNER_TEMP/ci" `), strings.Contains(l, ciExe+` `):
						assert.Equal(t, mayBeWindows, strings.Contains(l, ciExe), "%s calls tools/ci as %q; a job that may run on Windows calls %s", where, l, ciExe)
						if firstCall < 0 {
							firstCall = i
						}
						called++
					}
				}
				switch {
				case strings.HasPrefix(s.Uses, "actions/cache/restore@"), strings.HasPrefix(s.Uses, "actions/cache@"):
					cacheRestore = cacheRestore || builds == 0
				}
				if strings.HasPrefix(s.Uses, "actions/cache/save@") || strings.HasPrefix(s.Uses, "actions/cache@") {
					cacheSave = true
				}
				if strings.HasPrefix(s.Uses, "actions/setup-go@") && s.With["cache"] != false {
					cacheRestore, cacheSave = true, true // setup-go's own cache restores and saves
				}
			}
			if firstCall < 0 && builds == 0 {
				continue
			}
			assert.Equal(t, 1, builds, "%s builds tools/ci %d times, want once", where, builds)
			assert.GreaterOrEqual(t, firstCall, 0, "%s builds tools/ci and never calls it", where)
			if !strings.Contains(asText(job.RunsOn), "self-hosted") {
				assert.True(t, cacheRestore && cacheSave, "%s runs on a GitHub-hosted runner and builds tools/ci without restoring and saving the Go build cache (actions/cache, or setup-go's own)", where)
			}
		}
	}
	assert.Positive(t, called, "no workflow calls the built tools/ci; the walk is reading the wrong place")
}

// asText is a runs-on value as one string.
func asText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var parts []string
		for _, e := range x {
			parts = append(parts, asText(e))
		}
		return strings.Join(parts, " ")
	}
	return ""
}
