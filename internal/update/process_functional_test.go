//go:build functional

package update

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/stretchr/testify/require"
)

// process_functional_test.go holds the tests of this package that stage a real
// child process against a real deadline, so they are the functional tier
// (nova-tools #4328: a real process is functional), not a unit test paying the
// wall clock. Measured 2026-09-26: TestHeldPipePastGraceIsNamedAndEchoesNoContent
// waits out killGrace (2 s) and ran 2.0 s on run 36261817989's space shard 1/4.

// Past the grace the refusal must name the pipe rather than blame the version
// command, and it must say so without echoing a byte the child wrote.
func TestHeldPipePastGraceIsNamedAndEchoesNoContent(t *testing.T) {
	secret := "x 9.9.9-secret"
	e := Entry{Name: "x", Kind: "tool", Installed: mustArgv(t, command(t, "linger", base64.StdEncoding.EncodeToString([]byte(secret+"\n")), (killGrace+time.Second).String()))}
	r := Installed(context.Background(), e, killGrace+5*time.Second, false)
	if r.Known() || r.Reason != "output_not_closed" {
		require.Failf(t, "", "reason=%q remedy=%q", r.Reason, r.Remedy)
	}
	if r.Remedy != leakRemedy {
		require.EqualValuesf(t, leakRemedy, r.Remedy, "remedy=%q", r.Remedy)
	}
	if strings.Contains(r.Reason, "9.9.9") || strings.Contains(r.Remedy, "9.9.9") {
		require.Failf(t, "", "diagnostic echoed child content: %q %q", r.Reason, r.Remedy)
	}
}

// buildTreeBinaries builds this tree's nova-update into the test's own
// temporary directory and returns that directory, for a test that needs a real
// reporter process. The directory goes when the test ends.
func buildTreeBinaries(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	pkg := "./cmd/nova-update"
	build := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(dir, exeName(filepath.Base(pkg))), pkg)
	build.Dir = filepath.Join("..", "..")
	build.Env = goenv.Clean(os.Environ())
	out, err := build.CombinedOutput()
	require.NoErrorf(t, err, "go build %s: %v\n%s", pkg, err, out)
	return dir
}

func exeName(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}
