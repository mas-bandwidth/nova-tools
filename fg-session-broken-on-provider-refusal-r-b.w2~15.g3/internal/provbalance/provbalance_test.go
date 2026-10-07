package provbalance

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeKey is a key-shaped value that must never reach a note.
const fakeKey = "sk-or-v1-fakefakefakefakefakefakefake0123"

// fake is a transport that answers every request with status and body, or err, and keeps
// the requests it saw: no socket is opened.
type fake struct {
	status int
	body   string
	err    error
	seen   []*http.Request
}

func (f *fake) RoundTrip(r *http.Request) (*http.Response, error) {
	f.seen = append(f.seen, r)
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{StatusCode: f.status, Body: io.NopCloser(strings.NewReader(f.body)), Header: http.Header{}, Request: r}, nil
}

func env(key string) func(string) string {
	return func(k string) string {
		if k == "OPENROUTER_API_KEY" {
			return key
		}
		return ""
	}
}

// openrouter's credits, read with the seat's key as a bearer token: the balance is
// total_credits minus total_usage (the morning of 2026-10-03: 1250 and 1250.51), the
// usage its count used.
func TestOpenRouterBalanceIsCreditsLessUsage(t *testing.T) {
	t.Parallel()
	f := &fake{status: 200, body: `{"data":{"total_credits":1250,"total_usage":1250.51}}`}
	rd := Read(context.Background(), f, "openrouter", env(fakeKey))
	require.Len(t, f.seen, 1)
	assert.Equal(t, http.MethodGet, f.seen[0].Method)
	assert.Equal(t, OpenRouterURL, f.seen[0].URL.String())
	assert.Equal(t, "Bearer "+fakeKey, f.seen[0].Header.Get("Authorization"))
	assert.True(t, rd.Known)
	assert.InDelta(t, -0.51, rd.Balance, 1e-9)
	assert.True(t, rd.HasUsed)
	assert.InDelta(t, 1250.51, rd.Used, 1e-9)
	assert.Equal(t, "openrouter", rd.Provider)
}

// Every balance that cannot be read is unknown, and says why, and never says the key.
func TestAnUnreadableBalanceIsUnknownAndSaysWhy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, provider, key string
		f                   *fake
		why                 string
		asked               bool
	}{
		{"no key", "openrouter", "", &fake{status: 200}, "OPENROUTER_API_KEY is not in the run loop's environment: run it under nova-secrets exec --only OPENROUTER_API_KEY", false},
		{"refused", "openrouter", fakeKey, &fake{status: 401, body: `{"error":{"message":"No auth credentials found ` + fakeKey + `"}}`}, "GET " + OpenRouterURL + " answered 401", true},
		{"not the shape", "openrouter", fakeKey, &fake{status: 200, body: `{"data":{}}`}, "answered no data.total_credits and data.total_usage", true},
		{"down", "openrouter", fakeKey, &fake{err: errors.New("connection refused")}, "connection refused", true},
		{"opencode has no endpoint", "opencode", fakeKey, &fake{status: 200}, "opencode Zen publishes no balance endpoint (anomalyco/opencode#44189", false},
		{"another provider", "deepseek", fakeKey, &fake{status: 200}, "no balance endpoint is known for provider deepseek", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rd := Read(context.Background(), tc.f, tc.provider, env(tc.key))
			assert.False(t, rd.Known)
			assert.Contains(t, rd.Note, tc.why)
			assert.NotContains(t, rd.Note, fakeKey, "the key is never said")
			assert.Equal(t, tc.asked, len(tc.f.seen) > 0)
		})
	}
}

// The usage read: openrouter's key endpoint answers the UTC day's usage, read through the
// seat's key and never said; a provider with no endpoint, no key, an error status or an
// answer without usage_daily is unknown with why.
func TestReadUsageIsTheProvidersCountOfTheDay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fake{status: 200, body: `{"data":{"label":"sk-or-v1-abc...","usage":1250.5,"usage_daily":11.25,"usage_weekly":80}}`}
	rd := ReadUsage(ctx, f, "openrouter", "2026-10-05", env(fakeKey))
	require.True(t, rd.Known, "%+v", rd)
	assert.Equal(t, "2026-10-05", rd.Day)
	assert.InDelta(t, 11.25, rd.Used, 1e-9)
	require.Len(t, f.seen, 1)
	assert.Equal(t, OpenRouterKeyURL, f.seen[0].URL.String())
	assert.Equal(t, "Bearer "+fakeKey, f.seen[0].Header.Get("Authorization"))

	for name, c := range map[string]struct {
		rt       http.RoundTripper
		provider string
		key      string
		says     string
	}{
		"no endpoint":   {&fake{status: 200, body: "{}"}, "opencode", fakeKey, "publishes no balance endpoint"},
		"unknown":       {&fake{status: 200, body: "{}"}, "acme", fakeKey, "no usage endpoint is known for provider acme"},
		"no key":        {&fake{status: 200, body: "{}"}, "openrouter", "", "OPENROUTER_API_KEY is not in this environment"},
		"refused":       {&fake{status: 401, body: "{}"}, "openrouter", fakeKey, "answered 401"},
		"no daily":      {&fake{status: 200, body: `{"data":{"usage":3}}`}, "openrouter", fakeKey, "no data.usage_daily"},
		"no connection": {&fake{err: errors.New("dial tcp: refused")}, "openrouter", fakeKey, "failed"},
	} {
		rd := ReadUsage(ctx, c.rt, c.provider, "2026-10-05", env(c.key))
		assert.False(t, rd.Known, "%s: %+v", name, rd)
		assert.Contains(t, rd.Note, c.says, name)
		assert.NotContains(t, rd.Note, fakeKey, "%s: the key is never said", name)
	}
}
