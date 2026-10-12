package member

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTheDarwinMeterReturnsASample: the member's meter on darwin reads top's CPU
// usage line over a second, which answers on both arm64 and amd64; iostat's columns
// differ between the two and on amd64 Peak reported !ok, so the beat carried no
// sample and, before v1.2.11, no load. A Step is top's own second, and the sample is
// a percent in range. It runs on the host's own architecture, skipping only when the
// OS is not darwin.
func TestTheDarwinMeterReturnsASample(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("the darwin meter runs only on darwin")
	}
	m := NewMeter()
	var pct float64
	var ok bool
	require.Eventually(t, func() bool {
		pct, ok = m.Step()
		return ok
	}, 15*time.Second, 100*time.Millisecond, "the meter never returned a sample on %s", hostArch())
	require.GreaterOrEqual(t, pct, 0.0, "the sample is a percent, not negative: %v", pct)
	require.LessOrEqual(t, pct, 100.0, "the sample is a percent, at most 100: %v", pct)
	t.Logf("METER-SAMPLE %.1f%%", pct)
}

// hostArch is the running architecture, for the report.
func hostArch() string {
	return runtime.GOARCH
}

// TestTheDarwinMeterSamplesOnRun keeps Run's goroutine covered: a sampler fed
// by top's CPU usage line adds a sample once a second until its context ends, and
// Peak then reports it.
func TestTheDarwinMeterSamplesOnRun(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("the darwin meter runs only on darwin")
	}
	m := NewMeter()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	var ok bool
	require.Eventually(t, func() bool {
		_, _, ok = m.Peak(0)
		return ok
	}, 5*time.Second, 50*time.Millisecond, "Run never sampled the machine")
}
