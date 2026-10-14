package doctor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFriendLiveReadsDaemonDownAsDownAndNeverSuggestsKickstart pins that a
// status that answered daemon=down at exit 0 is a down daemon, not a status that
// did not answer, and that the fix never sends a stranger to launchctl
// kickstart (docs/SPEC-DOCTOR.md, "friend-live").
func TestFriendLiveReadsDaemonDownAsDownAndNeverSuggestsKickstart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFriendPlist(t, root, "ada", "--as", "ada", "--harness", "claude")
	fake := friendEnv{goos: "darwin", fakeEnv: fakeEnv{env: map[string]string{"HOME": "home"}, root: root, clock: time.Now(),
		exec: func(name string, _ ...string) (string, error) {
			switch name {
			case "nova-friend":
				return `{"daemon":"down","status_age":"3m0s","last_beat":""}`, nil
			}
			return "", errors.New("unexpected " + name)
		}}}

	r := checkFriendLive(context.Background(), fake)
	require.Equal(t, Fail, r.Status, "a daemon that answered daemon=down is a failure")
	assert.Contains(t, r.Evidence, "daemon=down", "the evidence names the status fact")
	assert.NotContains(t, r.Fix, "kickstart", "a daemon that answered is never sent to launchctl kickstart")
}
