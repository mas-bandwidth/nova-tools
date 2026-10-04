//go:build functional

package friend

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAntigravityOnTheLiveHarness needs Antigravity open with a conversation
// on NOVA_FRIEND_ANTIGRAVITY_DIR: it finds the language server, the newest
// root conversation of that directory and the port that answers, through
// the real commands. It delivers one line only with
// NOVA_FRIEND_ANTIGRAVITY_DELIVER set to the text: a real turn in a
// friend's session, never run by a build.
func TestAntigravityOnTheLiveHarness(t *testing.T) {
	dir := os.Getenv("NOVA_FRIEND_ANTIGRAVITY_DIR")
	if dir == "" {
		t.Skip("NOVA_FRIEND_ANTIGRAVITY_DIR names no friend's directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	a := &Antigravity{Dir: dir, Run: RealExec}
	ps, _, err := RealExec(ctx, dir, "ps", []string{"-axo", "pid=,args="}, "")
	require.NoError(t, err)
	pid, token, err := LanguageServer(ps)
	require.NoError(t, err)
	listing, _, err := RealExec(ctx, dir, "lsof", []string{"-nP", "-a", "-p", pid, "-iTCP", "-sTCP:LISTEN", "-Fn"}, "")
	require.NoError(t, err)
	ports := ListenPorts(listing)
	require.NotEmpty(t, ports)
	rows, _, err := RealExec(ctx, dir, "sqlite3", []string{"-json", "file:" + a.home() + "/" + AntigravityData + "/conversation_summaries.db?mode=ro&immutable=1", antigravitySummaries}, "")
	require.NoError(t, err)
	session, err := NewestConversation(rows, dir)
	require.NoError(t, err)
	answered := ""
	for _, p := range ports {
		if _, err := a.agentapi(ctx, p, token, "get-conversation-metadata", session); err == nil {
			answered = p
		}
	}
	require.NotEmpty(t, answered, "no port answered for %s", session)
	t.Logf("language server pid %s, port %s, conversation %s of %s", pid, answered, session, dir)

	text := os.Getenv("NOVA_FRIEND_ANTIGRAVITY_DELIVER")
	if text == "" {
		return
	}
	var record strings.Builder
	a.Out = &record
	exit, err := a.Deliver(ctx, text)
	require.NoError(t, err)
	require.Equal(t, 0, exit)
	t.Log(record.String())
}
