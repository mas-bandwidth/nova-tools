package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// balanceCoverBrokenRoutes is the poll's store whose routes read fails whole, as a store
// that did not answer refuses: the balance poll's one read fails, and nothing is written.
type balanceCoverBrokenRoutes struct {
	store.Backend
	err error
}

func (b balanceCoverBrokenRoutes) Routes(context.Context) (store.RouteSet, int64, error) {
	return store.RouteSet{}, 0, b.err
}

// balanceLoop (balance.go:26, the finding's 0.0%; nova-tools#5199) polls at once, rests on
// its own clock between polls (a.after), and ends when its context is done. The clock is
// the test's: the first rest ends at once for a second poll, and the rest that ends the
// loop is the one the test cancels at, so the loop runs in the caller's goroutine and
// waits for nothing.
func TestBalanceCoverLoopPollsEachRestUntilItsContextIsDone(t *testing.T) {
	t.Parallel()
	ta, fake, _ := balanceApp(t,
		sprint.Route{Name: "flash-or", Tier: "flash", Provider: "openrouter", Model: "m", Enabled: true})
	st, err := ta.a.store(common{redis: "mem:0", actor: sprint.MachineActor})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waits := 0
	ta.a.after = func(time.Duration) <-chan time.Time {
		waits++
		fired := make(chan time.Time, 1)
		if waits == 1 {
			fired <- ta.a.now() // the first rest ends at once: a second poll
			return fired
		}
		cancel() // the second rest is the loop's end: done at the rest
		return fired
	}
	var out bytes.Buffer
	ta.a.balanceLoop(ctx, st, &out)
	assert.Equal(t, 2, waits, "a rest between the polls, and the one the loop ends at")
	assert.Equal(t, []string{
		"Bearer sk-or-v1-fakefakefakefakefakefakefake0123",
		"Bearer sk-or-v1-fakefakefakefakefakefakefake0123",
	}, fake.keys, "each poll reads openrouter through the seat's key")
	assert.Equal(t, 1, strings.Count(out.String(), "BALANCE every 10m0s: "), out.String())
	assert.Equal(t, 2, strings.Count(out.String(), " BALANCE openrouter=-$0.51 notes="), out.String())
	assert.NotContains(t, out.String(), "sk-or-v1", "the key is never said")
}

// The loop with its context done before it begins says its header alone: no poll, no read,
// no rest, and the store is never touched (nil stands for it, and a touch fails the test).
func TestBalanceCoverLoopDoneBeforeItsFirstPollPollsNothing(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ta.a.after = func(time.Duration) <-chan time.Time {
		t.Error("the loop rested though its context was done before the first poll")
		return make(chan time.Time)
	}
	var out bytes.Buffer
	ta.a.balanceLoop(ctx, nil, &out)
	assert.Equal(t, "BALANCE every 10m0s: each provider's balance is read through the seat's key and written to the fleet table\n", out.String())
}

// A poll whose routes cannot be read says why once a poll, writes nothing, and reads no
// provider, and the loop goes on polling: the reads stay unmade and the loop ends at its
// own rest, so a store that does not answer is never a mystery failure (nova-tools#5199).
func TestBalanceCoverLoopSaysWhyAPollWroteNothingWhenTheRoutesCannotBeRead(t *testing.T) {
	t.Parallel()
	ta, fake, _ := balanceApp(t,
		sprint.Route{Name: "flash-or", Tier: "flash", Provider: "openrouter", Model: "m", Enabled: true})
	st := &store.Store{B: balanceCoverBrokenRoutes{Backend: ta.m, err: errors.New("the store did not answer")}, Now: ta.a.now}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ta.a.after = func(time.Duration) <-chan time.Time {
		cancel() // the poll is done; the loop ends at its rest
		return make(chan time.Time)
	}
	var out bytes.Buffer
	ta.a.balanceLoop(ctx, st, &out)
	assert.Equal(t, 1, strings.Count(out.String(), " BALANCE FAIL the routes could not be read: "), out.String())
	assert.Contains(t, out.String(), "the store did not answer", out.String())
	assert.NotContains(t, out.String(), " notes=", "nothing was written")
	assert.Empty(t, fake.keys, "no read")
}
