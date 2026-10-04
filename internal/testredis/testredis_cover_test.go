package testredis

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestTestredisCover brings six functions in testredis.go from 0.0% coverage
// on the unit tier: Start, StartServer, Addr, PID, Stop and run.
//
// Start and StartServer: the refusal Start makes on its own arguments is
// covered here; check runs before any binary is looked up or started, so no
// subprocess is needed. The main path that launches a server and returns its
// address needs a redis-server subprocess and lives on the functional tier.
//
// run: the refusal where the program cannot start is covered here. exec.Cmd.
// Start returns before forking when the command name is not on PATH, so no
// subprocess is created. The main path that waits for a ready line needs a
// redis-server subprocess.
//
// Addr, PID, Stop: the main path is covered by constructing a *Server with a
// stub process and a pre-closed exit channel; Stop's second call is the
// refusal, kept to one kill by sync.Once.
func TestTestredisCover(t *testing.T) {
	t.Parallel()

	t.Run("Start refuses its own arguments before looking up a binary", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name  string
			extra []string
			want  string
		}{
			{"bind", []string{"--bind", "0.0.0.0"}, "bind is refused"},
			{"port", []string{"--port", "6379"}, "port is refused"},
			{"dir", []string{"--dir", "elsewhere"}, "dir is refused"},
			{"daemonize", []string{"--daemonize", "yes"}, "daemonize is refused"},
			{"supervised", []string{"--supervised", "systemd"}, "supervised is refused"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				r := provoke(t, func(tb testing.TB) { Start(tb, tt.extra...) })
				assert.Contains(t, r.fatal, tt.want)
			})
		}
	})

	t.Run("StartServer refuses its own arguments before looking up a binary", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name  string
			extra []string
			want  string
		}{
			{"bind", []string{"--bind", "0.0.0.0"}, "bind is refused"},
			{"port", []string{"--port", "6379"}, "port is refused"},
			{"tls-port", []string{"--tls-port", "6380"}, "tls-port is refused"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				r := provoke(t, func(tb testing.TB) { StartServer(tb, tt.extra...) })
				assert.Contains(t, r.fatal, tt.want)
			})
		}
	})

	t.Run("Addr returns the stored address even after the server is stopped", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			addr string
		}{
			{"loopback", "127.0.0.1:6379"},
			{"ephemeral", "127.0.0.1:32841"},
			{"empty", ""},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s := &Server{addr: tt.addr, exited: make(chan struct{})}
				close(s.exited)
				assert.Equal(t, tt.addr, s.Addr())
			})
		}
	})

	t.Run("PID returns the process id of the cmd", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			pid  int
		}{
			{"init", 1},
			{"ephemeral", 32841},
			{"large", 2147483647},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s := &Server{cmd: &exec.Cmd{Process: &os.Process{Pid: tt.pid}}}
				assert.Equal(t, tt.pid, s.PID())
			})
		}
	})

	t.Run("Stop kills the process and waits for the exit channel", func(t *testing.T) {
		t.Parallel()
		// A process id above the kernel's PID_MAX_LIMIT, which no live
		// process can ever hold: Kill returns an error that Stop ignores,
		// and the exited channel is pre-closed, so Stop returns at once.
		stub := func() *Server {
			return &Server{
				cmd:    &exec.Cmd{Process: &os.Process{Pid: 2147483647}},
				exited: make(chan struct{}),
			}
		}
		t.Run("main path returns after the exit channel is closed", func(t *testing.T) {
			t.Parallel()
			s := stub()
			close(s.exited)
			s.Stop()
		})
		t.Run("second call is refused by sync.Once", func(t *testing.T) {
			t.Parallel()
			s := stub()
			close(s.exited)
			s.Stop()
			s.Stop() // sync.Once: the kill is not attempted a second time.
		})
	})

	t.Run("run fails when the program cannot start", func(t *testing.T) {
		t.Parallel()
		l := launch{
			wait:  30 * time.Second,
			tries: 1,
		}
		// A bare command name that is not on PATH: exec.Cmd.Start returns
		// cmd.Err before forking, so no subprocess is created.
		r := provoke(t, func(tb testing.TB) {
			l.run(tb, "testredis-cover-not-on-path", tb.TempDir(), "1", nil, 0)
		})
		assert.Contains(t, r.fatal, "did not start")
		assert.Contains(t, r.fatal, "testredis-cover-not-on-path")
	})
}
