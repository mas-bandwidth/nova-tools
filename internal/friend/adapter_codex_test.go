package friend

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func codexUnavailable(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("app unavailable")
}

func TestCodexAdmissionAndDeferralNeverSpawnHeadless(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                 string
		unavailable, invalid bool
	}{
		{name: "admitted"}, {name: "unavailable", unavailable: true}, {name: "unconfirmed", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fe := &fakeExec{}
			var record strings.Builder
			adapter, err := NewDeliverer("codex", "/project", "thread-1", fe.run, &record)
			require.NoError(t, err)
			c := adapter.(*Codex)
			c.Home = "/codex"
			conn := &codexConn{t: t}
			c.Dial = conn.dial
			if tc.unavailable {
				c.Dial = codexUnavailable
			}
			if tc.invalid {
				conn.change = func(method string, reply map[string]any) {
					if method == "thread-follower-start-turn" {
						reply["result"] = map[string]any{}
					}
				}
			}
			exit, err := c.Deliver(context.Background(), "hello")
			assert.Zero(t, exit)
			if tc.unavailable || tc.invalid {
				var deferred Deferred
				require.ErrorAs(t, err, &deferred)
				assert.Contains(t, deferred.Reason, "thread thread-1")
				assert.Empty(t, record.String())
			} else {
				require.NoError(t, err)
				assert.Equal(t, "delivered to open chat: thread=thread-1 turn=turn-1 (admitted, not answered)\n", record.String())
			}
			assert.Empty(t, fe.calls)
		})
	}
}

func TestCodexHomeIsCodexHomeThenTheUsersDotCodex(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "/elsewhere", (&Codex{Env: func(string) string { return "/elsewhere" }}).home())
	h, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(h, ".codex"), (&Codex{Env: func(string) string { return "" }}).home())
	assert.Equal(t, "/given", (&Codex{Home: "/given"}).home())
}
