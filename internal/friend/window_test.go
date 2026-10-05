package friend

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTmuxSendKeysOnlyIntoAnIdlePane(t *testing.T) {
	t.Parallel()
	var calls []string
	run := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch args[0] {
		case "list-panes":
			return "%7\tbob\topencode\n", 0, nil
		case "display-message":
			return "0\n", 0, nil
		default:
			return "", 0, nil
		}
	}
	target, err := FindWindow(context.Background(), run, "opencode", "bob")
	require.NoError(t, err)
	assert.Equal(t, WindowTarget{Kind: "tmux", Pane: "%7"}, target)
	require.NoError(t, SubmitWindow(context.Background(), run, target, "hello"))
	assert.Contains(t, calls, "tmux send-keys -t %7 -l -- hello")
	assert.Contains(t, calls, "tmux send-keys -t %7 Enter")
}

func TestTmuxDoesNotTypeIntoAPaneInAMode(t *testing.T) {
	t.Parallel()
	var calls []string
	run := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if args[0] == "list-panes" {
			return "%7\tbob\topencode\n", 0, nil
		}
		return "1\n", 0, nil
	}
	_, err := FindWindow(context.Background(), run, "opencode", "bob")
	var skip WindowSkip
	require.ErrorAs(t, err, &skip)
	assert.Equal(t, "pane-not-idle", skip.Reason)
	for _, c := range calls {
		assert.NotContains(t, c, "send-keys")
	}
}

func TestTmuxSkipsWhenTheFriendHasNoPane(t *testing.T) {
	t.Parallel()
	run := func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		if args[0] == "list-panes" {
			return "%1\tother\tcodex\n", 0, nil
		}
		return "", 0, nil
	}
	_, err := FindWindow(context.Background(), run, "opencode", "bob")
	var skip WindowSkip
	require.ErrorAs(t, err, &skip)
	assert.Equal(t, "no-tmux-pane", skip.Reason)
}

func TestGUIRefusesWithoutAccessibilityAndRunsNothing(t *testing.T) {
	t.Parallel()
	ran := false
	run := func(context.Context, string, string, []string, string) (string, int, error) {
		ran = true
		return "", 0, nil
	}
	_, err := FindWindow(context.Background(), run, "antigravity", "bob")
	require.ErrorIs(t, err, ErrNoAccessibility)
	assert.False(t, ran, "an untrusted GUI step ran a command")
	assert.Equal(t, "com.google.antigravity-ide", GUIBundles["antigravity"])
}

func TestWindowDarwinNeverPromptsForAccessibility(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("window_darwin.go")
	require.NoError(t, err)
	src := string(b)
	assert.Contains(t, src, "AXIsProcessTrusted")
	assert.NotContains(t, src, "kAXTrustedCheckOptionPrompt")
}
