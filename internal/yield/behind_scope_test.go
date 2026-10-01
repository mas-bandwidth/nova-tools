package yield

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestScopePlanFindsTheSliceBesideTheRunners: the scope goes in the slice that
// holds this process's unit under a user manager (where the runners' user units
// are its siblings); a process under no user manager, in no slice of one, or with
// no cgroup v2 path has no place, and the reason says which.
func TestScopePlanFindsTheSliceBesideTheRunners(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, cgroup, slice, why string
	}{
		{"a member's loop unit", "0::/user.slice/user-1000.slice/user@1000.service/app.slice/nova-loop-member-m.service\n", "app.slice", ""},
		{"a unit in another user slice", "0::/user.slice/user-1000.slice/user@1000.service/background.slice/x.service", "background.slice", ""},
		{"a system unit (a runner left as one)", "0::/system.slice/nova-runner-1.service", "", "not under a systemd user manager"},
		{"an ssh session", "0::/user.slice/user-1000.slice/session-7.scope", "", "not under a systemd user manager"},
		{"the user manager itself", "0::/user.slice/user-1000.slice/user@1000.service/init.scope", "", "in no slice of the user manager"},
		{"cgroup v1 only", "12:cpu,cpuacct:/user.slice\n", "", "no cgroup v2 path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, slice, why := scopePlan(c.cgroup)
			assert.Equal(t, c.slice, slice)
			if c.why == "" {
				assert.Empty(t, why)
			} else {
				assert.Contains(t, why, c.why)
			}
		})
	}
}

// TestScopeNameIsAUnitName: a launch's word becomes a unit name (anything a unit
// name does not take is '_'), with the pid so two live launches never share one.
func TestScopeNameIsAUnitName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "nova-card-c1.g2.e3-77.scope", scopeName("c1.g2.e3", 77))
	assert.Equal(t, "nova-card-a_b_c-1.scope", scopeName("a b/c", 1))
}

// fakeManager is a user manager for behindScope: what /proc/self/cgroup says
// before and after the move, what cpu.idle reads, and which call fails.
type fakeManager struct {
	before, after, idle string
	failCall            string // "busctl" or "systemctl": that call fails
	neverMoves          bool
	moved               bool
	calls               []string
}

func (f *fakeManager) deps() scopeDeps {
	return scopeDeps{
		pid: 42,
		read: func(path string) ([]byte, error) {
			switch {
			case path == "/proc/self/cgroup" && f.moved:
				return []byte(f.after), nil
			case path == "/proc/self/cgroup":
				return []byte(f.before), nil
			case strings.HasSuffix(path, "/cpu.idle"):
				return []byte(f.idle), nil
			}
			return nil, errors.New("no such file")
		},
		run: func(name string, args ...string) ([]byte, error) {
			f.calls = append(f.calls, name+" "+strings.Join(args, " "))
			if name == f.failCall {
				return []byte("Failed to start transient scope unit: Access denied\n"), errors.New("exit status 1")
			}
			if name == "busctl" && !f.neverMoves {
				f.moved = true
			}
			return []byte("o \"/org/freedesktop/systemd1/job/9\"\n"), nil
		},
		wait: func(time.Duration) {},
	}
}

// TestBehindScopeEveryPath: the scope is made in the unit's slice with this pid,
// set idle and read back; every refusal and every miss is a reason, never an
// error, and nothing is asked of a manager that is not there.
func TestBehindScopeEveryPath(t *testing.T) {
	t.Parallel()
	const before = "0::/user.slice/user-1000.slice/user@1000.service/app.slice/nova-loop-member-m.service\n"
	const after = "0::/user.slice/user-1000.slice/user@1000.service/app.slice/nova-card-c1-42.scope\n"
	cases := []struct {
		name  string
		f     fakeManager
		want  string // "" is done; else a substring of the reason
		calls int
	}{
		{"made, moved, idle", fakeManager{before: before, after: after, idle: "1\n"}, "", 2},
		{"no user manager: nothing asked", fakeManager{before: "0::/system.slice/nova-loop-member-m.service\n"}, "not under a systemd user manager", 0},
		{"the manager refuses the scope", fakeManager{before: before, after: after, failCall: "busctl"}, "refused the scope nova-card-c1-42.scope: Failed to start transient scope unit: Access denied", 1},
		{"the move never happens", fakeManager{before: before, after: after, neverMoves: true}, "did not move this process", 1},
		{"the manager refuses idle", fakeManager{before: before, after: after, failCall: "systemctl"}, "refused CPUWeight=idle", 2},
		{"cpu.idle reads 0", fakeManager{before: before, after: after, idle: "0\n"}, "cpu.idle is 0, not 1", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := c.f
			got := behindScope("c1", f.deps())
			if c.want == "" {
				assert.Empty(t, got)
			} else {
				assert.Contains(t, got, c.want)
			}
			assert.Len(t, f.calls, c.calls, "%v", f.calls)
			if c.calls > 0 {
				assert.Contains(t, f.calls[0], "StartTransientUnit ssa(sv)a(sa(sv)) nova-card-c1-42.scope fail 3 PIDs au 1 42 Slice s app.slice CollectMode s inactive-or-failed 0")
			}
			if c.calls > 1 {
				assert.Equal(t, "systemctl --user set-property --runtime nova-card-c1-42.scope CPUWeight=idle", f.calls[1])
			}
		})
	}
}
