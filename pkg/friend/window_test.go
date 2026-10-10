package friend

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWindowRefusesWithoutPermission(t *testing.T) {
	t.Parallel()
	called := false
	g := GUIWindow{
		Bundle:    AppBundles["codex"],
		Permitted: func(context.Context) (bool, error) { return false, nil },
		Run: func(context.Context, string, string, []string, string) (string, int, error) {
			called = true
			return "", 0, nil
		},
	}
	err := g.Deliver(context.Background(), "hello")
	var refused WindowRefused
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, AccessibilityRemedy, refused.Remedy)
	assert.False(t, called)
}

func TestWindowTypesWhenTrusted(t *testing.T) {
	t.Parallel()
	var got []string
	g := GUIWindow{
		Bundle:    AppBundles["codex"],
		Permitted: func(context.Context) (bool, error) { return true, nil },
		Run: func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
			got = append(got, name)
			got = append(got, args...)
			return "", 0, nil
		},
	}
	require.NoError(t, g.Deliver(context.Background(), "hello"))
	script := strings.Join(got, "\n")
	assert.Contains(t, script, AppBundles["codex"])
	assert.NotContains(t, script, "AXIsProcessTrusted")
	assert.NotContains(t, script, "WithOptions")
	assert.NotContains(t, strings.ToLower(script), "with prompt")
	assert.False(t, accessibilityAsks(script))
}

func TestAccessibilityCheckDoesNotAsk(t *testing.T) {
	t.Parallel()
	assert.Contains(t, AccessibilityCheckScript, "AXIsProcessTrusted")
	assert.NotContains(t, AccessibilityCheckScript, "AXIsProcessTrustedWithOptions")
	assert.NotContains(t, strings.ToLower(AccessibilityCheckScript), "prompt")
	assert.False(t, accessibilityAsks(AccessibilityCheckScript))
	assert.True(t, accessibilityAsks("AXIsProcessTrustedWithOptions"))
	assert.True(t, accessibilityAsks("WITH PROMPT"))
}

func TestPlatformPermittedDoesNotAsk(t *testing.T) {
	t.Parallel()
	called := false
	run := func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		called = true
		script := strings.Join(args, "\n")
		assert.False(t, accessibilityAsks(script), "the check asks: %s", script)
		return "false\n", 0, nil
	}
	ok, err := PlatformPermitted(context.Background(), run)
	require.NoError(t, err)
	assert.False(t, ok)
	if runtime.GOOS == "darwin" {
		assert.True(t, called)
	} else {
		assert.False(t, called)
	}
}
