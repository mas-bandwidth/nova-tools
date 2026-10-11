//go:build unix || windows

package filelock

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProperty_StampRoundtrip(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(42))
	baseTime := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)

	hostCharset := []rune("abcdefghijklmnopqrstuvwxyz0123456789-.")
	labelCharset := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_. =+/:αβγδε日本語🚀")

	randomString := func(chars []rune, maxLen int) string {
		var b strings.Builder
		for range rng.Intn(maxLen) + 1 {
			b.WriteRune(chars[rng.Intn(len(chars))])
		}
		return b.String()
	}

	const iterations = 5000
	for i := range iterations {
		pid := rng.Intn(10_000_000) + 1
		host := randomString(hostCharset, 20)
		offsetSec := rng.Int63n(365 * 24 * 3600 * 5)
		offsetNano := rng.Int63n(1_000_000_000)
		started := baseTime.Add(time.Duration(offsetSec)*time.Second + time.Duration(offsetNano)).UTC()
		text, err := started.MarshalText()
		require.NoError(t, err)
		var cleanTime time.Time
		require.NoError(t, cleanTime.UnmarshalText(text))

		rawLabel := randomString(labelCharset, 40)
		cleanLabel := strings.ReplaceAll(strings.ReplaceAll(rawLabel, "\r", " "), "\n", " ")

		original := Stamp{
			PID:     pid,
			Host:    host,
			Started: cleanTime,
			Label:   rawLabel,
		}

		formatted := original.Format()
		parsed, err := ParseStamp(formatted)
		require.NoError(t, err, "iteration %d: ParseStamp failed on formatted text %q: %v", i, formatted, err)
		require.Equal(t, original.PID, parsed.PID, "iteration %d: PID = %d, want %d", i, parsed.PID, original.PID)
		require.Equal(t, original.Host, parsed.Host, "iteration %d: Host = %q, want %q", i, parsed.Host, original.Host)
		require.True(t, parsed.Started.Equal(original.Started), "iteration %d: Started = %v, want %v", i, parsed.Started, original.Started)
		require.Equal(t, cleanLabel, parsed.Label, "iteration %d: Label = %q, want %q", i, parsed.Label, cleanLabel)
	}
}

func TestProperty_JitterBounds(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(43))
	const iterations = 10000

	for i := range iterations {
		d := time.Duration(rng.Int63n(int64(time.Hour))) + time.Millisecond
		jittered := defaultJitter(d)
		require.True(t, jittered >= d && jittered <= d+d/2+1, "iteration %d: defaultJitter(%v) = %v; want [%v, %v]", i, d, jittered, d, d+d/2+1)
	}
}
