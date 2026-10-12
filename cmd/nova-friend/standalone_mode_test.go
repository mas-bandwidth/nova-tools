package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCurrentRowResolutionSemantics pins the daemon's current row resolution:
// the explicit --mode override always wins; else the row her beat last said;
// else batch before any beat. The batch default is what keeps a daemon whose
// sprint beats are missing from running with a blank mode or waiting on one.
func TestCurrentRowResolutionSemantics(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name               string
		modeFlag, beatMode string
		beatWidth          int
		wantMode           string
		wantWidth          int
	}{
		{"omitted mode and no beat default to batch", "", "", 0, friend.ModeBatch, 0},
		{"omitted mode and no beat keep the width absent", "", "", 0, friend.ModeBatch, 0},
		{"omitted mode takes the beat's one-shot row", "", "one-shot", 3, "one-shot", 3},
		{"omitted mode takes the beat's batch row", "", "batch", 2, "batch", 2},
		{"omitted mode takes the beat's width", "", "batch", 5, "batch", 5},
		{"the explicit override beats the row", "one-shot", "batch", 2, "one-shot", 2},
		{"the explicit batch override beats a one-shot row", "batch", "one-shot", 2, "batch", 2},
		{"the explicit override keeps the beat's width", "one-shot", "batch", 4, "one-shot", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mode, width := resolveCurrentRow(tc.modeFlag, tc.beatMode, tc.beatWidth)
			assert.Equal(t, tc.wantMode, mode)
			assert.Equal(t, tc.wantWidth, width)
		})
	}
}

// TestStandaloneMissingSprintBeatsDefaultToBatch runs the daemon in a world
// whose beat never carries a row (no sprint beats yet): it must not hang, and
// its delivery mode must read batch, never a blank row.
func TestStandaloneMissingSprintBeatsDefaultToBatch(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	beats := 0
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		beats++
		if beats == 3 {
			cancel()
		}
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z", nil
	}
	dir := t.TempDir()
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", dir}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	s, _, err := friend.ReadStatus(friend.StateDirIn(dir))
	require.NoError(t, err)
	assert.Equal(t, "batch", s.Mode, "missing sprint beats default the daemon to batch, never a blank row")
}
