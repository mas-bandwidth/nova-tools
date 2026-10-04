package main

import (
	"errors"
	"math/big"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// An outage bench: a sampler on a fake clock whose reads a test scripts, one answer or
// refusal per read, and the usage source never opened. The loop never starts (interval 0):
// the test takes each sample itself (readOnce).
type outageBench struct {
	s      *liveSampler
	now    time.Time
	reads  int
	answer func(n int) (swarm.ProviderUsage, error) // the n-th read's answer, from 1
	took   time.Duration                            // how long each answered read takes on the clock
}

func newOutageBench(t *testing.T, cfg nativeRunConfig) *outageBench {
	t.Helper()
	b := &outageBench{now: time.Unix(1000, 0)}
	b.s = startLiveSampler("", 0, cfg, "")
	b.s.dataHome = "a data home"
	b.s.now = func() time.Time { return b.now }
	b.s.read = func(dataHome string, limit time.Duration) (swarm.ProviderUsage, error) {
		b.reads++
		u, err := b.answer(b.reads)
		if err == nil {
			b.now = b.now.Add(b.took)
		}
		return u, err
	}
	b.s.answered = b.now
	return b
}

// usageOf is an answered read's figures: tokens spent, and the harness's cost.
func usageOf(tokens int, cost string) swarm.ProviderUsage {
	v := map[string]string{"tokens_in": strconv.Itoa(tokens), "tokens_out": "0", "reasoning": "0", "cost": cost}
	if cost == "" {
		delete(v, "cost")
	}
	return swarm.ProviderUsage{Observed: true, Values: v}
}

// fired is the stop word the sampler sent, "" when none.
func (b *outageBench) fired() string {
	select {
	case w := <-b.s.fired:
		return w
	default:
		return ""
	}
}

// A read that fails is tried once more at once, and however many fail in a row, none ends
// the card by itself: the figures stay at the last answer and the error is the last read's.
func TestAFailedReadIsRetriedOnceAndNeverEndsTheCardByItself(t *testing.T) {
	t.Parallel()
	b := newOutageBench(t, nativeRunConfig{tokens: 1000})
	refused := errors.New("the usage source x could not be read: sqlite3 did not answer within 5s")
	b.answer = func(n int) (swarm.ProviderUsage, error) {
		if n <= 2 {
			return usageOf(200, ""), nil // twice the same figure: the spend never rose
		}
		return swarm.ProviderUsage{}, refused
	}
	b.s.readOnce()
	b.s.readOnce()
	require.Equal(t, 2, b.reads, "an answered read is one launch")
	for i := 0; i < 10; i++ {
		b.s.readOnce()
		b.now = b.now.Add(time.Hour)
	}
	assert.Equal(t, 22, b.reads, "a failed read is one launch more, at once")
	spent, observed, _, failures, err := b.s.Observed()
	assert.Equal(t, 200, spent, "the last answer's figure is kept")
	assert.True(t, observed)
	assert.Equal(t, 10, failures)
	assert.Same(t, refused, err)
	assert.Equal(t, refused.Error(), b.s.Why())
	assert.Empty(t, b.fired(), "ten failed reads over ten hours end nothing: the spend never rose")
	assert.Empty(t, b.s.StopWordAtFinal(200, true, ""))
}

// An outage ends the card only past UnverifiableAfter, and only when the last answer plus the
// most the spend rose between two answers, once per failed sample, is at the ceiling: the
// token budget's, or the dollar budget's.
func TestAnOutageEndsTheCardOnlyPastTheBoundWithTheExtrapolatedSpendAtTheCeiling(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cfg  nativeRunConfig
		cost func(n int) string
	}{
		{"tokens", nativeRunConfig{tokens: 1000}, func(int) string { return "" }},
		{"dollars", nativeRunConfig{tokens: 1 << 30, usd: big.NewRat(1, 1)}, func(n int) string { return big.NewRat(int64(n), 10).FloatString(2) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := newOutageBench(t, tc.cfg)
			b.answer = func(n int) (swarm.ProviderUsage, error) {
				if n <= 3 {
					return usageOf(100*n, tc.cost(n)), nil // 100, 200, 300: a rise of 100 (and $0.10) a sample
				}
				return swarm.ProviderUsage{}, errors.New("refused")
			}
			for i := 0; i < 3; i++ {
				b.s.readOnce()
			}
			// seven failed samples inside the bound: 300 + 7*100 = 1000, at the ceiling,
			// but the source has not been silent for UnverifiableAfter yet
			for i := 0; i < 7; i++ {
				b.now = b.now.Add(10 * time.Second)
				b.s.readOnce()
			}
			require.Empty(t, b.fired(), "at the ceiling inside the bound: the source may answer yet")
			// past the bound with the figure under the ceiling: an answer that lowers the
			// rise... cannot happen; a fresh bench shows the bound alone ends nothing
			c := newOutageBench(t, tc.cfg)
			c.answer = b.answer
			for i := 0; i < 3; i++ {
				c.s.readOnce()
			}
			c.now = c.now.Add(UnverifiableAfter)
			for i := 0; i < 6; i++ {
				c.s.readOnce() // 300 + 6*100 = 900, under
			}
			require.Empty(t, c.fired(), "past the bound under the ceiling: the deadline's to end")
			c.s.readOnce() // 300 + 7*100 = 1000: at it
			assert.Equal(t, stoppedUnverifiable, c.fired())
			assert.Equal(t, stoppedUnverifiable, c.s.StopWordAtFinal(300, true, ""), "the word is kept")
			assert.Equal(t, "refused", c.s.Why())
		})
	}
}

// A read that answers after failures resumes the budget on what it says: the failures are
// forgotten, the outage's clock starts again, and the ceiling fires on the figure itself.
func TestAReadThatAnswersAfterFailuresResumesTheBudget(t *testing.T) {
	t.Parallel()
	b := newOutageBench(t, nativeRunConfig{tokens: 1000})
	b.answer = func(n int) (swarm.ProviderUsage, error) {
		switch {
		case n == 1:
			return usageOf(100, ""), nil
		case n <= 7:
			return swarm.ProviderUsage{}, errors.New("refused")
		case n == 8:
			return usageOf(400, ""), nil
		}
		return usageOf(1000, ""), nil
	}
	b.s.readOnce()
	b.now = b.now.Add(UnverifiableAfter)
	for i := 0; i < 3; i++ {
		b.s.readOnce()
	}
	_, _, _, failures, _ := b.s.Observed()
	require.Equal(t, 3, failures)
	require.Empty(t, b.fired(), "100 + 3*0: the spend never rose before the outage")
	b.s.readOnce() // the eighth read answers
	spent, _, _, failures, err := b.s.Observed()
	assert.Equal(t, 400, spent)
	assert.Equal(t, 0, failures, "an answer forgets the failures")
	assert.NoError(t, err)
	assert.Empty(t, b.s.Why())
	b.s.readOnce()
	assert.Equal(t, stoppedTokens, b.fired(), "the budget fires on the figure the source says")
}

// The limit a read is given follows the slowest read that answered: four times it, never
// under swarm.LiveSampleLimit and never over LiveSampleMost; a read that failed raises it
// not at all.
func TestTheReadLimitFollowsTheSlowestAnsweredRead(t *testing.T) {
	t.Parallel()
	b := newOutageBench(t, nativeRunConfig{tokens: 1000})
	var limits []time.Duration
	b.s.read = func(dataHome string, limit time.Duration) (swarm.ProviderUsage, error) {
		limits = append(limits, limit)
		b.reads++
		if b.reads == 3 {
			return swarm.ProviderUsage{}, errors.New("refused")
		}
		b.now = b.now.Add(b.took)
		return usageOf(10, ""), nil
	}
	b.took = time.Second // wall-ok: a fake clock advanced by the fake reader, no real time
	b.s.readOnce()
	b.took = 3 * time.Second // wall-ok: the fake clock again
	b.s.readOnce()
	b.took = time.Minute // the third read fails and its retry answers, a minute long
	b.s.readOnce()
	b.s.readOnce()
	assert.Equal(t, []time.Duration{swarm.LiveSampleLimit, swarm.LiveSampleLimit, 12 * time.Second, 12 * time.Second, LiveSampleMost}, limits, "one limit per sample, its retry given the same; the minute-long answer raises it to the most")
}
