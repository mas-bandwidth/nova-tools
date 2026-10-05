package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// TestLandloopCoverRounds runs landLoop's main path and its one refusal. sleep is
// the seam that ends the context after one round, so no wall clock is read and no
// test waits; the backend is the in-memory store, or a fake that fails to read the
// merge queue.
func TestLandloopCoverRounds(t *testing.T) {
	t.Parallel()
	outage := errors.New("injected store outage")
	cases := []struct {
		name    string
		backend func(context.Context, string, sprint.Names) (store.Backend, error)
		want    []string
	}{
		{
			name:    "an idle round lands nothing",
			backend: nil,
			want:    []string{"LANDING every " + LandEvery.String()},
		},
		{
			name:    "an unreadable merge queue is surfaced as a failed round",
			backend: func(context.Context, string, sprint.Names) (store.Backend, error) { return nil, outage },
			want:    []string{"LAND FAILED", "injected store outage"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta := newTestApp(t)
			ta.ok("init --readers reader-a,reader-b --members m1")
			if tc.backend != nil {
				ta.a.backend = tc.backend
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			slept := 0
			ta.a.sleep = func(time.Duration) { slept++; cancel() }
			var out bytes.Buffer
			ta.a.landLoop(ctx, "mem:0", 0, &out)
			assert.Equal(t, 1, slept, "landLoop sleeps once, then sees the context is done")
			for _, want := range tc.want {
				assert.Contains(t, out.String(), want)
			}
		})
	}
}
