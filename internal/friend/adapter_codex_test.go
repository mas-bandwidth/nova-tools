package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexResumesTheNamedThreadAndLabelsTheAnswerAsResume(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{exit: 0, out: "got it, sent on the bus.\n"}
	var rec strings.Builder
	c := &Codex{Dir: "/w/stella", Session: "01a104a7-04b1-7003-ad3d-781200c5ff5d", Run: fe.run, Held: func(string) bool { return false }, Out: &rec}
	exit, err := c.Deliver(context.Background(), "ping: run the pong line")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	require.Len(t, fe.calls, 1)
	assert.Equal(t, []string{"/w/stella", "codex", "exec", "resume", "--skip-git-repo-check", "01a104a7-04b1-7003-ad3d-781200c5ff5d", "ping: run the pong line"}, fe.calls[0])
	assert.Equal(t, "answered by resume, not by the open chat: codex exec resume --skip-git-repo-check 01a104a7-04b1-7003-ad3d-781200c5ff5d <text>\ngot it, sent on the bus.\n", rec.String())
}

func TestCodexWithoutAThreadResumesTheNewestOfTheDirectory(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{exit: 7}
	var rec strings.Builder
	home := codexSessions(t)
	dir := filepath.Join(home, "project")
	c := &Codex{Dir: dir, Home: home, Run: fe.run, Program: "/opt/codex", Held: func(string) bool { return false }, Out: &rec}
	exit, err := c.Deliver(context.Background(), "hello")
	require.NoError(t, err)
	assert.Equal(t, 7, exit)
	assert.Equal(t, [][]string{{dir, "/opt/codex", "exec", "resume", "--skip-git-repo-check", "old", "hello"}}, fe.calls)
	assert.Equal(t, "not answered: codex exec resume exited 7 (a thread open in the Codex app refuses a resume; or no such thread)\n", rec.String())
}

func TestCodexRefusesWhileTheAppHoldsTheThreadWithoutRunningCodex(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{}
	var probed string
	c := &Codex{Dir: "/w/stella", Session: "t1", Home: "/h/.codex", Run: fe.run, Held: func(lock string) bool { probed = lock; return true }}
	exit, err := c.Deliver(context.Background(), "hello")
	assert.Equal(t, 0, exit)
	var deferred Deferred
	require.ErrorAs(t, err, &deferred)
	assert.Contains(t, deferred.Reason, "thread t1")
	assert.Equal(t, filepath.Join("/h/.codex", "thread-writer-locks", "t1.lock"), probed)
	assert.Empty(t, fe.calls)
}

func TestCodexHomeIsCodexHomeThenTheUsersDotCodex(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "/elsewhere", (&Codex{Env: func(string) string { return "/elsewhere" }}).home())
	h, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(h, ".codex"), (&Codex{Env: func(string) string { return "" }}).home())
	assert.Equal(t, "/given", (&Codex{Home: "/given"}).home())
}
