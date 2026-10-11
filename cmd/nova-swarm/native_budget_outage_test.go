package main

import (
	"errors"
	"math/big"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
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
			// six failed samples inside the bound: 300 + 6*100 = 900, under the ceiling
			for i := 0; i < 6; i++ {
				b.now = b.now.Add(10 * time.Second)
				b.s.readOnce()
			}
			require.Empty(t, b.fired(), "under the ceiling inside the bound: ends nothing")
			// seventh failed sample: 300 + 7*100 = 1000, at the ceiling inside the bound
			b.now = b.now.Add(10 * time.Second)
			b.s.readOnce()
			assert.Equal(t, stoppedUnverifiable, b.fired(), "at the ceiling inside the bound: fires on atCeiling alone")
			assert.Equal(t, stoppedUnverifiable, b.s.StopWordAtFinal(300, true, ""), "the word is kept")
			assert.Equal(t, "refused", b.s.Why())
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

// A source that never answers (sqlite3 missing, wrong data home: observed false) ends
// unverifiable once no answer ever has arrived within UnverifiableAfter.
func TestASourceThatNeverAnswersEndsUnverifiableAfterTheBound(t *testing.T) {
	t.Parallel()
	b := newOutageBench(t, nativeRunConfig{tokens: 1000})
	refused := errors.New("the usage source /j/opencode.db could not be read: sqlite3 missing")
	b.answer = func(n int) (swarm.ProviderUsage, error) {
		return swarm.ProviderUsage{}, refused
	}
	// Before UnverifiableAfter, failed reads do not end the card
	b.now = b.now.Add(UnverifiableAfter - time.Second)
	b.s.readOnce()
	assert.Empty(t, b.fired(), "before UnverifiableAfter, no answer ever ends nothing yet")
	assert.Empty(t, b.s.StopWordAtFinal(0, false, ""))

	// At or past UnverifiableAfter with no answer ever, the card ends unverifiable
	b.now = b.now.Add(2 * time.Second)
	b.s.readOnce()
	assert.Equal(t, stoppedUnverifiable, b.fired(), "no answer ever within UnverifiableAfter is unverifiable")
	assert.Equal(t, stoppedUnverifiable, b.s.StopWordAtFinal(0, false, ""), "the word is kept at final")
	assert.Equal(t, refused.Error(), b.s.Why())
}

// An outage right after the first answer extrapolates from SampleRiseFloor until two answers
// exist: the ceiling is enforced against the floor's extrapolation and ends unverifiable.
func TestAnOutageAfterTheFirstAnswerExtrapolatesFromRiseFloor(t *testing.T) {
	t.Parallel()
	b := newOutageBench(t, nativeRunConfig{tokens: 1000})
	b.answer = func(n int) (swarm.ProviderUsage, error) {
		if n == 1 {
			return usageOf(100, ""), nil // one answer only: rise seeded from SampleRiseFloor
		}
		return swarm.ProviderUsage{}, errors.New("refused")
	}
	b.s.readOnce() // first answer
	spent, observed, _, failures, _ := b.s.Observed()
	assert.Equal(t, 100, spent)
	assert.True(t, observed)
	assert.Equal(t, 0, failures)

	// An outage occurs right after the first answer.
	// With SampleRiseFloor = 100: 100 + 8*100 = 900, under ceiling
	for i := 0; i < 8; i++ {
		b.s.readOnce()
	}
	require.Empty(t, b.fired(), "under ceiling: ends nothing yet")

	// 9th failed read: 100 + 9*100 = 1000, at ceiling!
	b.s.readOnce()
	assert.Equal(t, stoppedUnverifiable, b.fired(), "at ceiling: fires on atCeiling from seeded rise floor")
	assert.Equal(t, stoppedUnverifiable, b.s.StopWordAtFinal(100, true, ""), "the word is kept at final")
}

// The at-once retry is capped so one sample never exceeds the read limit: a failed read that
// took part of the limit leaves only the remainder for the retry, and a read that exhausted
// the limit is not retried.
func TestTheRetryIsCappedSoOneSampleNeverExceedsTheReadLimit(t *testing.T) {
	t.Parallel()
	b := newOutageBench(t, nativeRunConfig{tokens: 1000})
	b.s.slowest = 3 * time.Second // 4*slowest = 12s read limit

	var limits []time.Duration
	b.s.read = func(dataHome string, limit time.Duration) (swarm.ProviderUsage, error) {
		limits = append(limits, limit)
		b.reads++
		b.now = b.now.Add(8 * time.Second) // read 1 takes 8s of the 12s limit
		return swarm.ProviderUsage{}, errors.New("timeout")
	}

	b.s.readOnce()
	require.Len(t, limits, 2, "a failed read with remaining time is retried once")
	assert.Equal(t, 12*time.Second, limits[0], "first read gets the full limit")
	assert.Equal(t, 4*time.Second, limits[1], "retry is capped to the remaining limit (12s - 8s)")

	// When the first read exhausts the full limit, no retry is attempted.
	limits = nil
	b.s.read = func(dataHome string, limit time.Duration) (swarm.ProviderUsage, error) {
		limits = append(limits, limit)
		b.reads++
		b.now = b.now.Add(12 * time.Second) // takes all 12s
		return swarm.ProviderUsage{}, errors.New("timeout")
	}

	b.s.readOnce()
	require.Len(t, limits, 1, "a read that exhausted the limit is not retried")
	assert.Equal(t, 12*time.Second, limits[0])
}
