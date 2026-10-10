package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Libraries considered: the existing ShellWord renderer and SplitShell parser
// preserve POSIX argv; no custom quoting or command parser is needed.
func TestPushPongCommandPreservesItsRouteAndActor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, actor, nonce, redis, server string
		want                              []string
	}{
		{"direct", "emma", "n1", "127.0.0.1:16380", "wrong-server:6390", []string{"nova-sprint", "seat", "pong", "n1", "--actor", "emma", "--redis", "127.0.0.1:16380"}},
		{"quoted socket", "emma's canary", "n'$(false);", "/tmp/emma's canary/redis.sock", "", []string{"nova-sprint", "seat", "pong", "n'$(false);", "--actor", "emma's canary", "--redis", "/tmp/emma's canary/redis.sock"}},
		{"server", "emma", "n2", "", "127.0.0.1:16390", []string{"env", "NOVA_SPRINT_SERVER=127.0.0.1:16390", "nova-sprint", "seat", "pong", "n2", "--actor", "emma"}},
		{"quoted server", "emma", "n3", "", "server';$(false)", []string{"env", "NOVA_SPRINT_SERVER=server';$(false)", "nova-sprint", "seat", "pong", "n3", "--actor", "emma"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command := PushPongCommand(tc.actor, tc.nonce, tc.redis, tc.server)
			words, err := onboarding.SplitShell(command)
			require.NoError(t, err)
			assert.Equal(t, tc.want, words)
			assert.Contains(t, PushCheckText(tc.nonce, command), "end this turn: "+command+"\n")
		})
	}
}
