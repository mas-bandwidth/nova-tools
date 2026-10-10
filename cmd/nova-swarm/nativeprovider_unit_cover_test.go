package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

func TestNovaSwarmNativeproviderCoverProviderLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		label  string
		wall   float64
		model  string
		cause  swarm.ProviderCause
		expect string
	}{
		{
			name:   "provider-5xx with status and message",
			label:  "c1",
			wall:   450.0,
			model:  "a/b",
			cause:  swarm.ProviderCause{Class: swarm.Cause5xx, Status: 503, Message: "stream error"},
			expect: "NATIVE PROVIDER-FAIL label=c1 wall=450.00s route=a/b reason=provider: class=provider-5xx status=503 msg=stream error",
		},
		{
			name:   "zero cause",
			label:  "c1",
			wall:   10.0,
			model:  "x/y",
			cause:  swarm.ProviderCause{},
			expect: "NATIVE PROVIDER-FAIL label=c1 wall=10.00s route=x/y reason=provider: class=other status=- msg=-",
		},
		{
			name:   "out-of-credit",
			label:  "c2",
			wall:   100.0,
			model:  "openai/gpt-4",
			cause:  swarm.ProviderCause{Class: swarm.CauseCredit, Status: 402, Message: "Insufficient credits"},
			expect: "NATIVE PROVIDER-FAIL label=c2 wall=100.00s route=openai/gpt-4 reason=provider: class=out-of-credit status=402 msg=Insufficient credits",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := providerLine(tc.label, tc.wall, tc.model, tc.cause)
			assert.Equal(t, tc.expect, got)
		})
	}
}

func TestNovaSwarmNativeproviderCoverPrintedHarnessError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		capture []byte
		expect  string
	}{
		{
			name:    "empty capture",
			capture: []byte{},
			expect:  "",
		},
		{
			name:    "INFO lines only",
			capture: []byte("timestamp=t level=INFO run=r message=hello\n"),
			expect:  "",
		},
		{
			name:    "ERROR line with statusCode=503",
			capture: []byte("timestamp=t level=ERROR run=r message=\"stream error\" statusCode=503\n"),
			expect:  `message="stream error" statusCode=503`,
		},
		{
			name:    "ERROR line matching no provider pattern",
			capture: []byte("timestamp=t level=ERROR run=r message=failed error=\"file not found\"\n"),
			expect:  "",
		},
		{
			name: "ProviderModelNotFoundError wins over earlier provider error",
			capture: []byte("timestamp=t level=ERROR run=r message=\"stream error\" statusCode=503\n" +
				"timestamp=t level=ERROR run=r error=\"ProviderModelNotFoundError: Model not found: x/y. Did you mean z?\" error=x\n"),
			expect: `timestamp=t level=ERROR run=r error="ProviderModelNotFoundError: Model not found: x/y. Did you mean z?" error=x`,
		},
		{
			name:    "ProviderModelNotFoundError with error=",
			capture: []byte("timestamp=t level=ERROR run=r error=\"ProviderModelNotFoundError: Model not found: a/b\" error=reason here\n"),
			expect:  `timestamp=t level=ERROR run=r error="ProviderModelNotFoundError: Model not found: a/b" error=reason here`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := printedHarnessError(tc.capture)
			assert.Equal(t, tc.expect, got)
		})
	}
}

func TestNovaSwarmNativeproviderCoverProviderEnd(t *testing.T) {
	t.Parallel()
	since := time.Now()
	offset := int64(0)

	t.Run("headless claude with credit error", func(t *testing.T) {
		t.Parallel()
		capture := []byte("{\"type\":\"result\",\"is_error\":true,\"result\":\"Credit balance is too low\"}")
		cause, ok := providerEnd("claude", "", offset, since, 0, capture)
		require.True(t, ok)
		assert.Equal(t, swarm.CauseCredit, cause.Class)
	})

	t.Run("headless claude with non-error result", func(t *testing.T) {
		t.Parallel()
		capture := []byte("{\"type\":\"result\",\"is_error\":false}")
		cause, ok := providerEnd("claude", "", offset, since, 0, capture)
		require.False(t, ok)
		assert.Equal(t, swarm.ProviderCause{}, cause)
	})

	t.Run("rc -1 answers not ok", func(t *testing.T) {
		t.Parallel()
		dataHome := t.TempDir()
		capture := []byte("timestamp=t level=ERROR run=r message=\"stream error\" statusCode=503\n")
		cause, ok := providerEnd("", dataHome, offset, since, -1, capture)
		require.False(t, ok)
		assert.Equal(t, swarm.ProviderCause{}, cause)
	})

	t.Run("empty data home with ERROR statusCode=503 gives provider-5xx", func(t *testing.T) {
		t.Parallel()
		dataHome := t.TempDir()
		capture := []byte("timestamp=t level=ERROR run=r message=\"stream error\" statusCode=503\n")
		cause, ok := providerEnd("", dataHome, offset, since, 0, capture)
		require.True(t, ok)
		assert.Equal(t, swarm.Cause5xx, cause.Class)
		assert.Equal(t, 503, cause.Status)
	})

	t.Run("empty capture and log with rate limit line gives rate-limited", func(t *testing.T) {
		t.Parallel()
		dataHome := t.TempDir()
		logDir := filepath.Join(dataHome, "opencode", "log")
		require.NoError(t, os.MkdirAll(logDir, 0o755))
		logPath := filepath.Join(logDir, "opencode.log")
		logContent := "timestamp=old level=INFO run=r message=hello\n" +
			"timestamp=now level=ERROR run=r message=\"rate limit reached\" statusCode=429\n"
		require.NoError(t, os.WriteFile(logPath, []byte(logContent), 0o644))
		capture := []byte{}
		cause, ok := providerEnd("", dataHome, offset, since, 0, capture)
		require.True(t, ok)
		assert.Equal(t, swarm.CauseRateLimited, cause.Class)
	})

	t.Run("nothing printed or logged answers not ok for rc=0", func(t *testing.T) {
		t.Parallel()
		dataHome := t.TempDir()
		_, ok := providerEnd("", dataHome, offset, since, 0, []byte{})
		require.False(t, ok)
	})

	t.Run("nothing printed or logged answers not ok for rc=1", func(t *testing.T) {
		t.Parallel()
		dataHome := t.TempDir()
		_, ok := providerEnd("", dataHome, offset, since, 1, []byte{})
		require.False(t, ok)
	})
}

func TestNovaSwarmNativeproviderCoverEndedOnATool(t *testing.T) {
	t.Parallel()
	dataHome := t.TempDir()
	since := time.Now()
	ok := endedOnATool(dataHome, since)
	require.False(t, ok)
}

func TestNovaSwarmNativeproviderCoverSessionProviderError(t *testing.T) {
	t.Parallel()
	dataHome := t.TempDir()
	since := time.Now()
	cause, ok := sessionProviderError(dataHome, since)
	require.False(t, ok)
	assert.Equal(t, swarm.ProviderCause{}, cause)
}

func TestNovaSwarmNativeproviderCoverSessionQuery(t *testing.T) {
	t.Parallel()
	dataHome := t.TempDir()
	out, ok := sessionQuery(dataHome, "SELECT 1")
	require.False(t, ok)
	assert.Equal(t, "", out)
}
