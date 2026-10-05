package update

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"

	"github.com/stretchr/testify/require"
)

// pathProcess is the real child adapter with argv[0] looked up in dirs first:
// the per-test seam for a fake binary on PATH, handed to the value under test as
// Environment.Process, so no test sets the process-wide PATH. A name that no dir
// holds goes to the real adapter, or is not_found when only is set (a trap dir
// that nothing else may be reached beside).
func pathProcess(only bool, dirs ...string) processFunc {
	return func(ctx context.Context, args []string, input io.Reader, cap int) ProcessResult {
		if len(args) > 0 && !strings.ContainsRune(args[0], filepath.Separator) {
			for _, d := range dirs {
				p := filepath.Join(d, args[0])
				if runtime.GOOS == "windows" {
					p += ".exe"
				}
				if st, err := os.Stat(p); err == nil && !st.IsDir() {
					return process(ctx, append([]string{p}, args[1:]...), input, cap)
				}
			}
			if only {
				return ProcessResult{Reason: "not_found"}
			}
		}
		return process(ctx, args, input, cap)
	}
}

// busFake is one test's nova-bus: a copy of the test binary named nova-bus in its
// own directory, which TestMain turns into the fake bus when the directory holds
// the mode file. The call log and the mode live in that directory, so no test
// shares them and none sets an environment variable.
type busFake struct {
	dir, log, modeFile string
}

func newFakeBus(t *testing.T) *busFake {
	t.Helper()
	dir := t.TempDir()
	name := "nova-bus"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	raw, e := os.Executable()
	require.NoError(t, e)
	require.NoError(t, testbin.Place(raw, filepath.Join(dir, name)))
	b := &busFake{dir: dir, log: filepath.Join(dir, "calls"), modeFile: filepath.Join(dir, busModeFile)}
	b.mode(t, "ok")
	return b
}

// mode sets what the bus answers a send with: ok, uncertain, send-fail,
// send-alien, send-shouty or hang.
func (b *busFake) mode(t *testing.T, m string) {
	t.Helper()
	require.NoError(t, os.WriteFile(b.modeFile, []byte(m), 0600))
}

// env is the Environment whose children find this bus as nova-bus.
func (b *busFake) env(e Environment) Environment {
	e.Process = pathProcess(false, b.dir)
	return e
}
