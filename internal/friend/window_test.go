package friend

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// osa is osascript behind the Exec seam: it answers word and records the call.
type osa struct {
	word  string
	exit  int
	calls [][]string
}

func (o *osa) exec(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
	o.calls = append(o.calls, append([]string{name}, args...))
	return o.word + "\n", o.exit, nil
}

func TestReachWindowRefusesWithoutAccessibilityPermission(t *testing.T) {
	t.Parallel()
	o := &osa{word: "NOT-TRUSTED"}
	err := appSend(context.Background(), o.exec, "com.example.chat", "REACH n1: hi")
	require.ErrorIs(t, err, ErrNoAccessibility)
	assert.Contains(t, err.Error(), "Privacy & Security > Accessibility", "the remedy a person acts on")
	require.Len(t, o.calls, 1, "the permission is read, never asked for, and nothing is typed")
	assert.Equal(t, []string{"osascript", "-l", "JavaScript", "-e", appScript, "com.example.chat", "REACH n1: hi"}, o.calls[0])
	assert.Contains(t, appScript, "AXIsProcessTrusted()", "checked without the prompting form")
}

func TestReachWindowAppAnswers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, word string
		exit       int
		says       string
	}{
		{"typed and submitted", "OK", 0, ""},
		{"no such app", "NOT-RUNNING", 0, "no running app has the bundle id com.example.chat"},
		{"osascript fails", "execution error", 1, "osascript exited 1: execution error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := appSend(context.Background(), (&osa{word: c.word, exit: c.exit}).exec, "com.example.chat", "x")
			if c.says == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, c.says)
		})
	}
}

func TestReachWindowTmuxTypesThenSubmits(t *testing.T) {
	t.Parallel()
	var calls []string
	run := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", 0, nil
	}
	require.NoError(t, TmuxSend(context.Background(), run, "bob:0.1", "REACH n1: hi"))
	assert.Equal(t, []string{"tmux send-keys -t bob:0.1 -l REACH n1: hi", "tmux send-keys -t bob:0.1 Enter"}, calls)

	fail := func(context.Context, string, string, []string, string) (string, int, error) {
		return "can't find pane: bob:9", 1, nil
	}
	require.ErrorContains(t, TmuxSend(context.Background(), fail, "bob:9", "x"), "tmux send-keys -t bob:9 exited 1: can't find pane: bob:9")
}
