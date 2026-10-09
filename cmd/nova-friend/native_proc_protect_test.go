package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProductionWorldWiresNativeParentProtection(t *testing.T) {
	t.Parallel()
	assert.NotNil(t, realWorld().protectParent)
}

func TestOpenCodeRunRefusesFailedParentProtectionBeforeNativeTurn(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	protected, beats := 0, 0
	w.protectParent = func() error {
		protected++
		return errors.New("synthetic guard failure")
	}
	nativeChecks := 0
	w.wall = func(_ friend.Wall, _ friend.Exec) friend.Exec {
		return func(context.Context, string, string, []string, string) (string, int, error) {
			nativeChecks++
			return "", 0, nil
		}
	}
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		beats++
		return "", nil
	}
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", t.TempDir()}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 1, code)
	assert.Equal(t, 1, protected)
	assert.Zero(t, beats, "the daemon must not launch or beat after parent protection fails")
	assert.Zero(t, nativeChecks, "the native CLI check must not launch after parent protection fails")
	assert.Contains(t, errb.String(), "protect daemon environment: synthetic guard failure")
}

func TestOpenCodeProtectsParentBeforeNativeCLICheck(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	protected, nativeChecks := false, 0
	w.protectParent = func() error { protected = true; return nil }
	w.wall = func(_ friend.Wall, _ friend.Exec) friend.Exec {
		return func(context.Context, string, string, []string, string) (string, int, error) {
			assert.True(t, protected, "the daemon must be nondumpable before the first OpenCode CLI check")
			nativeChecks++
			return "", 0, nil
		}
	}
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx, cancel
	}
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", t.TempDir()}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.True(t, protected)
	assert.GreaterOrEqual(t, nativeChecks, 2, "version and run help both use the native runner")
}
