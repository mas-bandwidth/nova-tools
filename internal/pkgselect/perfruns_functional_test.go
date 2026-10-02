//go:build functional

package pkgselect

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// realRun is a Runner over real processes, bounded.
func realRun(dir string, env []string, argv ...string) (Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(goenv.Clean(os.Environ()), env...)
	cmd.WaitDelay = 5 * time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return Result{Stdout: out.String(), Stderr: errb.String(), Code: exitErr.ExitCode()}, nil
	}
	return Result{Stdout: out.String(), Stderr: errb.String()}, err
}

// The perf job runs what PerfRuns schedules, by name. Run over this tree, it schedules
// every tool's help budget: each package whose help test goes through testverbhelp has
// TestEveryVerbsHelpIsWithinTheBudget in its run regexp. A budget the selector does not
// schedule is a gate that looks present and never fires.
func TestThePerfJobSchedulesEveryToolsHelpBudget(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	runs, notes, err := PerfRuns(realRun, root)
	require.NoError(t, err)
	scheduled := map[string]string{}
	for _, r := range runs {
		scheduled[r.Package] = r.Run
	}
	dirs, err := filepath.Glob(filepath.Join(root, "cmd", "*", "verbhelp_test.go"))
	require.NoError(t, err)
	require.NotEmpty(t, dirs)
	for _, f := range dirs {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		if !strings.Contains(string(b), "testverbhelp.Check(") {
			continue
		}
		pkg := "github.com/mas-bandwidth/nova-tools/cmd/" + filepath.Base(filepath.Dir(f))
		assert.Contains(t, scheduled[pkg], "TestEveryVerbsHelpIsWithinTheBudget", "%s's help budget is not scheduled by the perf job; notes: %q", pkg, notes)
	}
}
