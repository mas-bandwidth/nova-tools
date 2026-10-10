package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexDeliversIntoTheOpenChatOrDefers(t *testing.T) {
	t.Parallel()
	type answer struct {
		exit int
		err  error
	}
	for _, tc := range []struct {
		name     string
		held     bool
		answers  []answer
		methods  []string
		deferred bool
	}{
		{"open queues", true, []answer{{0, nil}}, []string{"queue"}, false},
		{"closed resumes", false, []answer{{0, nil}}, []string{"exec"}, false},
		{"queue refusal resumes", true, []answer{{1, nil}, {0, nil}}, []string{"queue", "exec"}, false},
		{"resume refusal queues", false, []answer{{1, nil}, {0, nil}}, []string{"exec", "queue"}, false},
		{"queue exec error resumes", true, []answer{{0, errors.New("queue unavailable")}, {0, nil}}, []string{"queue", "exec"}, false},
		{"resume exec error queues", false, []answer{{0, errors.New("resume unavailable")}, {0, nil}}, []string{"exec", "queue"}, false},
		{"both fail open", true, []answer{{1, nil}, {0, errors.New("active writer")}}, []string{"queue", "exec"}, true},
		{"both fail closed", false, []answer{{1, nil}, {2, nil}}, []string{"exec", "queue"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fe := &fakeExec{out: "actionable receipt\n" + strings.Repeat("x", OutputKept*2)}
			run := func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
				out, _, _ := fe.run(ctx, dir, name, args, stdin)
				require.LessOrEqual(t, len(fe.calls), len(tc.answers))
				r := tc.answers[len(fe.calls)-1]
				return out, r.exit, r.err
			}
			var record strings.Builder
			var probed string
			c := &Codex{Dir: "/project", Session: "thread-1", Home: "/codex", Program: "/bin/codex", Run: run, Out: &record, Held: func(lock string) bool { probed = lock; return tc.held }}
			text := "literal `text` [and] $shell"
			exit, err := c.Deliver(context.Background(), text)
			assert.Zero(t, exit)
			if tc.deferred {
				var d Deferred
				require.ErrorAs(t, err, &d)
				assert.Contains(t, d.Reason, "queue")
				assert.Contains(t, d.Reason, "exec")
				assert.Contains(t, d.Reason, "actionable receipt")
				assert.NotContains(t, d.Reason, strings.Repeat("x", OutputKept+1))
				assert.Empty(t, record.String())
			} else {
				require.NoError(t, err)
				assert.Contains(t, record.String(), "receipt")
				if tc.methods[len(tc.methods)-1] == "queue" {
					assert.Contains(t, record.String(), "queued for open chat")
				} else {
					assert.Contains(t, record.String(), ResumeLabel)
				}
			}
			assert.Equal(t, filepath.Join("/codex", "thread-writer-locks", "thread-1.lock"), probed)
			require.Len(t, fe.calls, len(tc.methods))
			for i, method := range tc.methods {
				want := []string{"/project", "/bin/codex"}
				if method == "queue" {
					want = append(want, "queue", "--thread", "thread-1", "--message", text)
				} else {
					want = append(want, "exec", "resume", "--skip-git-repo-check", "thread-1", text)
				}
				assert.Equal(t, want, fe.calls[i])
			}
		})
	}
}

func TestCodexWithoutAThreadUsesTheNewestOfTheDirectory(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{}
	home := codexSessions(t)
	dir := filepath.Join(home, "project")
	c := &Codex{Dir: dir, Home: home, Run: fe.run, Held: func(string) bool { return true }}
	exit, err := c.Deliver(context.Background(), "hello")
	require.NoError(t, err)
	assert.Zero(t, exit)
	assert.Equal(t, [][]string{{dir, "codex", "queue", "--thread", "old", "--message", "hello"}}, fe.calls)
}

func TestCodexHomeIsCodexHomeThenTheUsersDotCodex(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "/elsewhere", (&Codex{Env: func(string) string { return "/elsewhere" }}).home())
	h, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(h, ".codex"), (&Codex{Env: func(string) string { return "" }}).home())
	assert.Equal(t, "/given", (&Codex{Home: "/given"}).home())
}
