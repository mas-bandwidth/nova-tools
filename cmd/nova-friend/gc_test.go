package main

import (
	"context"
	"errors"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An unavailable measurement must preserve presence while reporting why it is unknown.
func TestUnknownJobCapacityStillBuildsAPresenceBeat(t *testing.T) {
	t.Parallel()
	args := capacityArgs([]string{"friend", "beat", "amy"}, friend.JobCapacity{Jobs: -1, Free: -1, Error: "filesystem measurement unavailable"})
	assert.Equal(t, []string{"friend", "beat", "amy", "--capacity-error", "filesystem measurement unavailable"}, args)
}

func TestGCDryRunMatchesTheDocumentedEmptyJobs(t *testing.T) {
	t.Parallel()
	const example = "nova-friend gc --as bob --dir ./bob --dry-run"
	raw := testkit.ReadFile(t, filepath.Join("..", "..", "docs", "CLI.md"))
	_, tail, ok := strings.Cut(raw, "$ "+example+"\n")
	require.True(t, ok)
	want, _, ok := strings.Cut(tail, "```")
	require.True(t, ok)
	steps, err := onboarding.Steps("nova-friend", strings.Split("$ "+example+"\n"+want, "\n"))
	require.NoError(t, err)
	w := newRig(t).world()
	w.stage = func(dir string) *friend.Stager { return &friend.Stager{Dir: dir} }
	w.cards = func(context.Context, string, []string) (string, error) { return "", nil }
	var out, errb strings.Builder
	code := run([]string{"gc", "--as", "bob", "--dir", filepath.Join(t.TempDir(), "bob"), "--dry-run"}, strings.NewReader(""), &out, &errb, w)
	assert.Empty(t, onboarding.CompareTranscript(steps, []onboarding.Result{{Code: code, Stdout: out.String(), Stderr: errb.String()}}, nil))
}

func TestMeasurementFailureDoesNotSuppressPresence(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	w.stage = func(dir string) *friend.Stager { return &friend.Stager{Dir: dir} }
	w.cards = func(context.Context, string, []string) (string, error) {
		return "FRIEND-CARDS OK mode=batch width=0\n", nil
	}
	sampled := make(chan struct{}, 1)
	w.measureCapacity = func(string) (friend.JobCapacity, error) {
		select {
		case sampled <- struct{}{}:
		default:
		}
		return friend.JobCapacity{}, errors.New("measurement unavailable")
	}
	beats := 0
	reported := false
	w.capacityBeat = func(_ context.Context, _, _ string, _, _ time.Time, c friend.JobCapacity) (string, error) {
		beats++
		if c.Error == "measurement unavailable" {
			reported = true
			cancel()
		}
		return "", nil
	}
	sleep := w.sleep
	w.sleep = func(ctx context.Context, d time.Duration) {
		select {
		case <-sampled:
		default:
		}
		sleep(ctx, d)
	}
	stopAfter(&w, &cancel, 5*time.Minute)
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", t.TempDir()}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Positive(t, beats)
	assert.True(t, reported, "the failed sample reaches a live beat")
}
