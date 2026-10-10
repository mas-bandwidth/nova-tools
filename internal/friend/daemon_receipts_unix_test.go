//go:build unix

package friend

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Deliverer does not call TurnAccepted. realExec does, once the delivery
// command is running, and the daemon stamps read before that command ends.
func TestReadIsStampedWhileTheDeliveryCommandRuns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	fifo := filepath.Join(dir, "hold")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("fifo unavailable: %v", err)
	}
	r := newRig(t)
	r.d.Deliver = runningDeliver{r: r, started: started, fifo: fifo}
	r.hold = make(chan struct{})
	// The fake clock moves a second a beat; the delivery command is a real
	// subprocess that needs real time to start, so the beat budget is wide
	// and the silence watch is pushed past it: the turn must still be
	// running, never stopped, when read is stamped.
	r.d.SilentStop = 24 * time.Hour
	r.releaseAt = 1 << 20 // Pause returns while the command is still blocked
	m := r.send(t, "ada", "running", "hold")
	var sawRead atomic.Bool
	var early atomic.Bool
	var released atomic.Bool
	for beat := 3; beat <= 20000; beat++ {
		r.at[beat] = func() {
			if released.Load() {
				return
			}
			if _, err := os.Stat(started); err != nil {
				return
			}
			r.mu.Lock()
			n := len(r.delivered)
			r.mu.Unlock()
			if n != 0 {
				early.Store(true)
				return
			}
			got, _, err := r.bus.Stages(context.Background(), "bob", m.ID)
			if err != nil || len(got) == 0 || got[0].State != bus.Read {
				return
			}
			sawRead.Store(true)
			if released.CompareAndSwap(false, true) {
				f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
				if err == nil {
					_ = f.Close()
				}
			}
		}
	}
	r.run(t, 20002)
	require.False(t, early.Load(), "read is stamped before Deliver returns")
	require.True(t, sawRead.Load(), "read stamped while the delivery command still ran")
	require.Len(t, r.delivered, 1)
	got, _, err := r.bus.Stages(context.Background(), "bob", m.ID)
	require.NoError(t, err)
	require.NotEmpty(t, got)
	assert.Equal(t, bus.Acted, got[0].State)
}

// runningDeliver is a production-shaped Deliverer: it runs a command through
// realExec and never calls TurnAccepted itself.
type runningDeliver struct {
	r             *rig
	started, fifo string
}

func (h runningDeliver) Deliver(ctx context.Context, text string) (int, error) {
	script := `printf x > "$1" && /bin/cat "$2"`
	_, exit, err := realExec(ctx, time.Second, "", "/bin/sh", []string{"-c", script, "sh", h.started, h.fifo}, "")
	h.r.mu.Lock()
	h.r.delivered = append(h.r.delivered, text)
	h.r.owed++
	h.r.mu.Unlock()
	return exit, err
}

// A probe context does not accept a command that runs. A delivery context
// accepts one that starts and then exits non-zero.
func TestRealExecAcceptsAStartedCommandAndNotAProbe(t *testing.T) {
	t.Parallel()
	var n atomic.Int64
	ctx := WithTurnAccepted(context.Background(), func() { n.Add(1) })
	_, exit, err := realExec(withoutTurnAcceptance(ctx), time.Second, t.TempDir(), "/bin/sh", []string{"-c", "exit 0"}, "")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	assert.Equal(t, int64(0), n.Load())
	_, exit, err = realExec(ctx, time.Second, t.TempDir(), "/bin/sh", []string{"-c", "exit 7"}, "")
	require.NoError(t, err)
	assert.Equal(t, 7, exit)
	assert.Equal(t, int64(1), n.Load())
}
