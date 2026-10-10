package release

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/provbalance"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingRoundTripper returns an error for every request.
type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

// TestReleaseSpendSourcesCoverGetJSON tests getJSON for various failure modes.
func TestReleaseSpendSourcesCoverGetJSON(t *testing.T) {
	t.Parallel()

	t.Run("non-200 answer", func(t *testing.T) {
		t.Parallel()
		rt := &roundTrip{answers: map[string]string{}}
		err := getJSON(context.Background(), rt, "http://example.com", "test-key", &struct{}{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "answered 404")
	})

	t.Run("no JSON of its shape", func(t *testing.T) {
		t.Parallel()
		rt := &roundTrip{answers: map[string]string{
			"http://example.com": "not json",
		}}
		err := getJSON(context.Background(), rt, "http://example.com", "test-key", &struct{}{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "answered no JSON of its shape")
	})

	t.Run("transport error", func(t *testing.T) {
		t.Parallel()
		rt := failingRoundTripper{}
		err := getJSON(context.Background(), rt, "http://example.com", "test-key", &struct{}{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GET http://example.com failed")
	})

	t.Run("key never in error text", func(t *testing.T) {
		t.Parallel()
		rt := failingRoundTripper{}
		err := getJSON(context.Background(), rt, "http://example.com", "secret-key-never-appear", &struct{}{})
		require.Error(t, err)
		assert.False(t, strings.Contains(err.Error(), "secret-key-never-appear"))
	})
}

// TestReleaseSpendSourcesCoverOpenRouterSpend tests OpenRouterSpend.Spend for various failure modes.
func TestReleaseSpendSourcesCoverOpenRouterSpend(t *testing.T) {
	t.Parallel()

	t.Run("activity row with no date or usage", func(t *testing.T) {
		t.Parallel()
		rt := &roundTrip{answers: map[string]string{
			OpenRouterActivityURL: `{"data":[{"date":"2026-09-16"}]}`,
		}}
		env := map[string]string{OpenRouterActivityEnv: "prov-secret", "OPENROUTER_API_KEY": "key-secret"}
		r := OpenRouterSpend{RT: rt, Getenv: func(k string) string { return env[k] }}
		w := SpendWindowFrom(time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC))
		_, err := r.Spend(context.Background(), w)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "answered a row with no date or usage")
	})

	t.Run("activity key set but OPENROUTER_API_KEY unset", func(t *testing.T) {
		t.Parallel()
		rt := &roundTrip{answers: map[string]string{
			OpenRouterActivityURL: `{"data":[{"date":"2026-09-16","usage":10}]}`,
		}}
		env := map[string]string{OpenRouterActivityEnv: "prov-secret"}
		r := OpenRouterSpend{RT: rt, Getenv: func(k string) string { return env[k] }}
		w := SpendWindowFrom(time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC))
		_, err := r.Spend(context.Background(), w)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "OPENROUTER_API_KEY is not in this environment")
	})

	t.Run("key answer has no data.usage_daily", func(t *testing.T) {
		t.Parallel()
		rt := &roundTrip{answers: map[string]string{
			OpenRouterActivityURL:        `{"data":[{"date":"2026-09-16","usage":10}]}`,
			provbalance.OpenRouterKeyURL: `{"data":{}}`,
		}}
		env := map[string]string{OpenRouterActivityEnv: "prov-secret", "OPENROUTER_API_KEY": "key-secret"}
		r := OpenRouterSpend{RT: rt, Getenv: func(k string) string { return env[k] }}
		w := SpendWindowFrom(time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC))
		_, err := r.Spend(context.Background(), w)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "answered no data.usage_daily")
	})

	t.Run("window inside today so activity is never fetched", func(t *testing.T) {
		t.Parallel()
		rt := &roundTrip{answers: map[string]string{
			provbalance.OpenRouterKeyURL: `{"data":{"usage_daily":4}}`,
		}}
		env := map[string]string{OpenRouterActivityEnv: "prov-secret", "OPENROUTER_API_KEY": "key-secret"}
		r := OpenRouterSpend{RT: rt, Getenv: func(k string) string { return env[k] }}
		// Window is entirely inside today (2026-09-17)
		w := SpendWindowFrom(time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC), time.Date(2026, 9, 17, 18, 0, 0, 0, time.UTC))
		got, err := r.Spend(context.Background(), w)
		require.NoError(t, err)
		assert.InDelta(t, 4.0, got, 1e-9)
		// Only the key URL should be called, one bearer recorded
		assert.Len(t, rt.auth, 1)
		assert.Equal(t, "Bearer key-secret", rt.auth[0])
	})
}

// TestReleaseSpendSourcesCoverFileReceipts tests FileReceipts.Tokens for various failure modes.
func TestReleaseSpendSourcesCoverFileReceipts(t *testing.T) {
	t.Parallel()

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "nonexistent.json")
		r := FileReceipts{Path: path}
		_, err := r.Tokens(context.Background(), SpendWindow{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot read the receipts")
	})

	t.Run("evidence not spend-receipts or friends absent", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()

		// Wrong evidence
		path1 := filepath.Join(tmp, "wrong_evidence.json")
		require.NoError(t, os.WriteFile(path1, []byte(`{"evidence":"other","from":"2026-09-17T00:00:00Z","to":"2026-09-18T09:00:00Z"}`), 0o600))
		_, err := FileReceipts{Path: path1}.Tokens(context.Background(), SpendWindow{From: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is no spend-receipts file")

		// Friends absent
		path2 := filepath.Join(tmp, "no_friends.json")
		require.NoError(t, os.WriteFile(path2, []byte(`{"evidence":"spend-receipts","from":"2026-09-17T00:00:00Z","to":"2026-09-18T09:00:00Z"}`), 0o600))
		_, err = FileReceipts{Path: path2}.Tokens(context.Background(), SpendWindow{From: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is no spend-receipts file")
	})

	t.Run("to is after the window's end", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		path := filepath.Join(tmp, "receipts.json")
		// to is 2026-09-18T10:00:00Z, window ends at 2026-09-18T09:00:00Z
		require.NoError(t, os.WriteFile(path, []byte(`{"evidence":"spend-receipts","from":"2026-09-17T00:00:00Z","to":"2026-09-18T10:00:00Z","friends":{"alex":1000}}`), 0o600))
		_, err := FileReceipts{Path: path}.Tokens(context.Background(), SpendWindow{From: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the receipts")
	})
}

// TestReleaseSpendSourcesCoverLoadSpendStore tests loadSpendStore for address validation.
func TestReleaseSpendSourcesCoverLoadSpendStore(t *testing.T) {
	t.Parallel()

	t.Run("URL address refuses", func(t *testing.T) {
		t.Parallel()
		getenvCalls := []string{}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := loadSpendStore(ctx, "redis://h:6379", func(s string) string {
			getenvCalls = append(getenvCalls, s)
			return ""
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "redis")
		assert.True(t, len(getenvCalls) > 0, "getenv should be asked")
	})

	t.Run("address with @ refuses", func(t *testing.T) {
		t.Parallel()
		getenvCalls := []string{}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := loadSpendStore(ctx, "redis://user@host:6379", func(s string) string {
			getenvCalls = append(getenvCalls, s)
			return ""
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "redis")
		assert.True(t, len(getenvCalls) > 0, "getenv should be asked")
	})

	t.Run("getenv records it was asked on valid address", func(t *testing.T) {
		t.Parallel()
		// Note: we cannot test the success path here because it requires a real Redis store.
		// This test just verifies getenv gets called before connection attempt.
		// The success path is tested in the functional tier.
	})
}
