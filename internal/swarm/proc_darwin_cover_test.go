package swarm

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// proc_darwin_cover_test.go reaches StartStamp in proc_darwin.go, the one
// function the unit tier's per-function coverage table held at 0.0%: no unit
// test called it. proc_darwin.go compiles only under GOOS=darwin, so the
// assertions run on the darwin leg and skip elsewhere with the reason written,
// the shape internal/sandbox/wrap_darwin_cover_test.go uses for its darwin
// body. Every test is named TestProcDarwinCoverSomething so
// `-run TestProcDarwinCover` selects them.
//
// StartStamp reads the native kinfo_proc birth time; missing processes still
// have no signal authority. Nothing here sleeps, opens a socket or store.

// TestProcDarwinCoverStartStamp pins the live and refusal paths on Darwin.
func TestProcDarwinCoverStartStamp(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" {
		t.Skipf("skipped on %s: proc_darwin.go's StartStamp is the darwin body's; %s builds its own", runtime.GOOS, runtime.GOOS)
	}

	assert.NotEqual(t, "-", StartStamp(os.Getpid()))
	assert.Equal(t, "-", StartStamp(0))
	assert.Equal(t, "-", StartStamp(1<<30))
}
