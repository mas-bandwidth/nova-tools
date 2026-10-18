package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestACoordinatorArmedPushNamesThePlaybook pins R-PLAYBOOK on every ARMED.
func TestACoordinatorArmedPushNamesThePlaybook(t *testing.T) {
	t.Parallel()
	checkArmedPlaybook(t, "coordinator", "nova-sprint help playbook")
}

// TestAFriendArmedPushNamesTheWorkerPlaybook pins R-WORKER-PLAYBOOK on every ARMED.
func TestAFriendArmedPushNamesTheWorkerPlaybook(t *testing.T) {
	t.Parallel()
	checkArmedPlaybook(t, "friend", "nova-sprint help worker-playbook")
}

func TestOtherHookResultsDoNotNameAPlaybook(t *testing.T) {
	t.Parallel()
	checkArmedPlaybook(t, "comms", "")
}

func checkArmedPlaybook(t *testing.T, role, command string) {
	t.Helper()
	for _, joined := range []bool{false, true} {
		name := "first arm"
		if joined {
			name = "joined record"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			r.clock = time.Unix(1000, 0)
			r.h.MR.SetTime(r.now())
			if joined {
				_, _, err := r.h.Store.HookAs(context.Background(), "ada", hook.Role(role), "test")
				require.NoError(t, err)
			}
			w := r.world()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
			cs := &clockStore{r: r}
			flushed := false
			cs.event = func(time.Time) {
				if !flushed {
					flushed = true
					r.h.MR.FlushAll()
					r.advance(hook.RenewEvery)
				} else {
					cancel()
				}
			}
			w.waitOn = func(wt bus.Waiter) bus.Waiter { cs.Waiter = wt; return cs }
			out := &streamOut{each: func(string) {}}
			var errb strings.Builder
			code := run([]string{"hook", "--as", "ada", "--role", role}, strings.NewReader(""), out, &errb, w)
			require.Zero(t, code, out.all.String()+errb.String())
			result, armed, notes := "", 0, 0
			perArm := []int{}
			for _, line := range strings.Split(out.all.String(), "\n") {
				if strings.HasPrefix(line, "HOOK ") && !strings.HasPrefix(line, "HOOK NOTE ") {
					result = strings.Fields(line)[1]
					if result == "ARMED" {
						armed++
						perArm = append(perArm, 0)
					}
				}
				if strings.Contains(line, "nova-sprint help ") {
					notes++
					if result == "ARMED" {
						perArm[len(perArm)-1]++
					}
					assert.Equal(t, "ARMED", result, line)
					assert.Contains(t, line, "read now: "+command)
				}
			}
			assert.Equal(t, 2, armed, out.all.String())
			wantNotes := armed
			if command == "" {
				wantNotes = 0
			}
			assert.Equal(t, wantNotes, notes, out.all.String())
			for _, count := range perArm {
				want := 1
				if command == "" {
					want = 0
				}
				assert.Equal(t, want, count, "one note per ARMED")
			}
			assert.NotContains(t, r.cli().Run("hook", "--as", "ada", "--role", role, "--once").Stdout, "nova-sprint help ")
		})
	}
}
