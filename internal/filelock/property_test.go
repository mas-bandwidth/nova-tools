//go:build unix || windows

package filelock

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
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
		n := rng.Intn(maxLen) + 1
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteRune(chars[rng.Intn(len(chars))])
		}
		return b.String()
	}

	const iterations = 5000
	for i := 0; i < iterations; i++ {
		pid := rng.Intn(10_000_000) + 1
		host := randomString(hostCharset, 20)
		offsetSec := rng.Int63n(365 * 24 * 3600 * 5)
		offsetNano := rng.Int63n(1_000_000_000)
		started := baseTime.Add(time.Duration(offsetSec)*time.Second + time.Duration(offsetNano)).UTC()
		text, err := started.MarshalText()
		if err != nil {
			require.NoError(t, err, err)
		}
		var cleanTime time.Time
		if err := cleanTime.UnmarshalText(text); err != nil {
			require.NoError(t, err, err)
		}

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
		if err != nil {
			require.NoError(t, err, "iteration %d: ParseStamp failed on formatted text %q: %v", i, formatted, err)
		}

		if parsed.PID != original.PID {
			require.Equal(t, original.PID, parsed.PID, "iteration %d: PID = %d, want %d", i, parsed.PID, original.PID)
		}
		if parsed.Host != original.Host {
			require.Equal(t, original.Host, parsed.Host, "iteration %d: Host = %q, want %q", i, parsed.Host, original.Host)
		}
		if !parsed.Started.Equal(original.Started) {
			require.Fail(t, fmt.Sprintf("iteration %d: Started = %v, want %v", i, parsed.Started, original.Started))
		}
		if parsed.Label != cleanLabel {
			require.Equal(t, cleanLabel, parsed.Label, "iteration %d: Label = %q, want %q", i, parsed.Label, cleanLabel)
		}
	}
}

func TestProperty_JitterBounds(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(43))
	const iterations = 10000

	for i := 0; i < iterations; i++ {
		d := time.Duration(rng.Int63n(int64(time.Hour))) + time.Millisecond
		jittered := defaultJitter(d)
		min := d
		max := d + d/2 + 1
		if jittered < min || jittered > max {
			require.Fail(t, fmt.Sprintf("iteration %d: defaultJitter(%v) = %v; want [%v, %v]", i, d, jittered, min, max))
		}
	}
}

func TestProperty_ProbeAbsentNeverCreates(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(45))
	dir := t.TempDir()

	const iterations = 200
	for i := 0; i < iterations; i++ {
		name := fmt.Sprintf("random_%d_%d.lock", rng.Int63(), i)
		path := filepath.Join(dir, name)

		state, stamp, err := Probe(path)
		if err != nil {
			require.NoError(t, err, "iteration %d: Probe(%s) err = %v", i, path, err)
		}
		if state != StateAbsent {
			require.Equal(t, StateAbsent, state, "iteration %d: Probe(%s) state = %s, want %s", i, path, state, StateAbsent)
		}
		if !stamp.IsZero() {
			require.Fail(t, fmt.Sprintf("iteration %d: stamp = %+v, want zero", i, stamp))
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			require.ErrorIs(t, err, os.ErrNotExist, "iteration %d: Probe created file at %s: %v", i, path, err)
		}
	}
}
