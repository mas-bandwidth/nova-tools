package release

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/provbalance"
)

// TagTime makes the fake forge a TagTimer: every tag was made a day before the cut.
func (f *fakeForge) TagTime(_ context.Context, _, tag string) (time.Time, error) {
	return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC), nil
}

// noSpend is a spend gate with nothing to compare: a store that recorded no spend and
// knows no provider, the cut tests' own that are not about spend.
func noSpend() *SpendSources {
	return &SpendSources{Store: fakeRecorded{}, Receipts: fakeReceipts{}}
}

// fakeRecorded is what the store recorded over the window, as the sprint's reader answers it
// (cmd/nova-sprint, spend_store.go, whose test reads these figures off a store's cards).
type fakeRecorded struct {
	usd    map[string]float64
	tokens map[string]int64
}

func (r fakeRecorded) Providers(context.Context, SpendWindow) ([]string, error) {
	return slices.Sorted(maps.Keys(r.usd)), nil
}
func (r fakeRecorded) Spend(_ context.Context, provider string, _ SpendWindow) (float64, error) {
	return r.usd[provider], nil
}
func (r fakeRecorded) Tokens(context.Context, SpendWindow) (map[string]int64, error) {
	return maps.Clone(r.tokens), nil
}

// fakeProvider is a provider's own count: a figure, or an error that it cannot be read.
type fakeProvider struct {
	name string
	usd  float64
	err  error
	got  []SpendWindow
}

func (p *fakeProvider) Provider() string { return p.name }
func (p *fakeProvider) Spend(_ context.Context, w SpendWindow) (float64, error) {
	p.got = append(p.got, w)
	return p.usd, p.err
}

// fakeReceipts is the friends' harness receipts.
type fakeReceipts map[string]int64

func (r fakeReceipts) Tokens(context.Context, SpendWindow) (map[string]int64, error) { return r, nil }

// spendStore is the store's read over the window (since the previous tag's day,
// 2026-09-17): $836 of openrouter, and a subscription friend's run of 1000 tokens.
func spendStore(t *testing.T) fakeRecorded {
	t.Helper()
	return fakeRecorded{usd: map[string]float64{"openrouter": 836}, tokens: map[string]int64{"alex": 1000}}
}

// cutWithSpend runs a cut against the fake forge with the spend sources given.
func cutWithSpend(t *testing.T, src *SpendSources, extra ...string) (int, string, string, *fakeForge) {
	t.Helper()
	f := cutForge()
	deps := cutDeps(t, f)
	deps.Spend = src
	var out, errs bytes.Buffer
	args := append([]string{"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "main",
		"--version", "v0.16.0", "--changelog", filepath.Join(t.TempDir(), "CHANGELOG.md")}, extra...)
	code := Run("nova-update", args, &out, &errs, deps)
	return code, out.String(), errs.String(), f
}

// A release is refused when the spend the store recorded over the release's window misses a
// paid provider's own count of it by more than 5%, naming the provider, both figures and the
// gap; a gap within 5% passes; a provider whose readout cannot be read refuses, never passes;
// and the subscription friends' tokens are set beside their harness receipts the same way
// (the owner, 2026-10-05: "We should not make a release without verifying that we capture
// actual spend, not < 1/2 of it.").
func TestAReleaseIsRefusedWhenRecordedSpendMissesTheProvidersOwn(t *testing.T) {
	t.Parallel()

	t.Run("2250 against 836 refuses naming the provider", func(t *testing.T) {
		t.Parallel()
		or := &fakeProvider{name: "openrouter", usd: 2250}
		code, out, errs, f := cutWithSpend(t, &SpendSources{Store: spendStore(t), Providers: []ProviderSpend{or}, Receipts: fakeReceipts{"alex": 1000}})
		require.Equal(t, 2, code, "out=%s errs=%s", out, errs)
		assert.Contains(t, errs, "SPEND provider=openrouter store=$836.00 provider_usd=$2250.00 gap=$1414.00 share=62.8% verdict=refuse")
		assert.Contains(t, errs, "CUT REFUSED reason=spend-gate window=2026-09-17T00:00:00Z..2026-09-18T09:00:00Z refused=1 providers=openrouter friends=-")
		assert.Empty(t, f.tagged, "a release whose spend was not captured was tagged")
		require.Len(t, or.got, 1)
		assert.Equal(t, SpendWindow{From: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC), To: at(t)}, or.got[0], "the window is since the previous tag's UTC day")
	})

	t.Run("a 3% gap passes", func(t *testing.T) {
		t.Parallel()
		or := &fakeProvider{name: "openrouter", usd: 836 / 0.97}
		code, out, errs, f := cutWithSpend(t, &SpendSources{Store: spendStore(t), Providers: []ProviderSpend{or}, Receipts: fakeReceipts{"alex": 1000}})
		require.Equal(t, 0, code, "out=%s errs=%s", out, errs)
		assert.Contains(t, errs, "SPEND provider=openrouter store=$836.00 provider_usd=$861.86 gap=$25.86 share=3.0% verdict=ok")
		assert.Contains(t, errs, "SPEND friend=alex store_tokens=1000 receipt_tokens=1000 gap=0 share=0.0% verdict=ok")
		assert.Contains(t, out, "spend=ok")
		assert.Len(t, f.tagged, 1)
	})

	t.Run("an unreadable provider refuses", func(t *testing.T) {
		t.Parallel()
		or := &fakeProvider{name: "openrouter", err: errors.New("GET activity answered 401")}
		code, _, errs, f := cutWithSpend(t, &SpendSources{Store: spendStore(t), Providers: []ProviderSpend{or}, Receipts: fakeReceipts{"alex": 1000}})
		require.Equal(t, 2, code, errs)
		assert.Contains(t, errs, "SPEND provider=openrouter unread verdict=refuse: GET activity answered 401")
		assert.Contains(t, errs, "refused=1 providers=openrouter friends=-")
		assert.Empty(t, f.tagged)

		// a provider the store knows of and no readout is given for is unread too
		code, _, errs, _ = cutWithSpend(t, &SpendSources{Store: spendStore(t), Receipts: fakeReceipts{"alex": 1000}})
		require.Equal(t, 2, code, errs)
		assert.Contains(t, errs, "SPEND provider=openrouter unread verdict=refuse: no readout of openrouter's own spend is known")
	})

	t.Run("subscription tokens are compared the same way", func(t *testing.T) {
		t.Parallel()
		or := &fakeProvider{name: "openrouter", usd: 836}
		code, _, errs, _ := cutWithSpend(t, &SpendSources{Store: spendStore(t), Providers: []ProviderSpend{or}, Receipts: fakeReceipts{"alex": 2500, "emma": 300}})
		require.Equal(t, 2, code, errs)
		assert.Contains(t, errs, "SPEND friend=alex store_tokens=1000 receipt_tokens=2500 gap=1500 share=60.0% verdict=refuse")
		assert.Contains(t, errs, "SPEND friend=emma store_tokens=0 receipt_tokens=300 gap=300 share=100.0% verdict=refuse")
		assert.Contains(t, errs, "refused=2 providers=- friends=alex,emma")

		code, _, errs, _ = cutWithSpend(t, &SpendSources{Store: spendStore(t), Providers: []ProviderSpend{or}})
		require.Equal(t, 2, code, errs)
		assert.Contains(t, errs, "SPEND friend=alex unread verdict=refuse: no harness receipts were given")
	})

	t.Run("the waiver goes into the changelog", func(t *testing.T) {
		t.Parallel()
		f := cutForge()
		deps := cutDeps(t, f)
		deps.Spend = &SpendSources{Store: spendStore(t), Providers: []ProviderSpend{&fakeProvider{name: "openrouter", usd: 2250}}, Receipts: fakeReceipts{"alex": 1000}}
		path := filepath.Join(t.TempDir(), "CHANGELOG.md")
		var out, errs bytes.Buffer
		code := Run("nova-update", []string{"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "main", "--version", "v0.16.0", "--changelog", path,
			"--no-spend-gate", "--reason", "the owner cut it knowing"}, &out, &errs, deps)
		require.Equal(t, 0, code, errs.String())
		assert.Contains(t, out.String(), "spend=waived")
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Contains(t, string(body), SpendWaiverPrefix+"the owner cut it knowing")
		assert.Contains(t, string(body), "SPEND provider=openrouter store=$836.00 provider_usd=$2250.00")
	})

	t.Run("no store refuses", func(t *testing.T) {
		t.Parallel()
		f := cutForge()
		deps := cutDeps(t, f)
		deps.Spend = nil // production: --spend-store names none
		var out, errs bytes.Buffer
		code := Run("nova-update", []string{"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "main", "--version", "v0.16.0",
			"--changelog", filepath.Join(t.TempDir(), "CHANGELOG.md")}, &out, &errs, deps)
		require.Equal(t, 2, code, errs.String())
		assert.Contains(t, errs.String(), "CUT REFUSED reason=spend-gate")
		assert.Contains(t, errs.String(), "--spend-store <addr> names no sprint store")
		assert.Empty(t, f.tagged)

		// a store named to a build with no sprint store reader is unread, never a pass
		out.Reset()
		errs.Reset()
		code = Run("nova-update", []string{"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "main", "--version", "v0.16.0",
			"--changelog", filepath.Join(t.TempDir(), "CHANGELOG.md"), "--spend-store", "127.0.0.1:1"}, &out, &errs, deps)
		require.Equal(t, 2, code, errs.String())
		assert.Contains(t, errs.String(), "this build reads no sprint store")
		assert.Empty(t, f.tagged)
	})
}

// roundTrip is a transport answering each url from a map, recording the bearer it was sent.
type roundTrip struct {
	answers map[string]string
	auth    []string
}

func (r *roundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	r.auth = append(r.auth, req.Header.Get("Authorization"))
	body, ok := r.answers[req.URL.String()]
	if !ok {
		return &http.Response{StatusCode: 404, Body: http.NoBody, Request: req}, nil
	}
	return &http.Response{StatusCode: 200, Body: readCloser(body), Request: req}, nil
}

type stringBody struct{ *strings.Reader }

func (stringBody) Close() error { return nil }

func readCloser(s string) stringBody { return stringBody{strings.NewReader(s)} }

// openrouter's own count of a window is the activity's completed days in it and the key's
// count of today; a key not in the environment, or a window past the activity's reach, is
// unread with why, and the key is never in the why.
func TestOpenRouterSpendIsTheActivityDaysAndToday(t *testing.T) {
	t.Parallel()
	rt := &roundTrip{answers: map[string]string{
		OpenRouterActivityURL:        `{"data":[{"date":"2026-09-16","usage":100},{"date":"2026-09-17","usage":2},{"date":"2026-09-17","usage":3}]}`,
		provbalance.OpenRouterKeyURL: `{"data":{"usage_daily":4}}`,
	}}
	env := map[string]string{OpenRouterActivityEnv: "prov-secret", "OPENROUTER_API_KEY": "key-secret"}
	r := OpenRouterSpend{RT: rt, Getenv: func(k string) string { return env[k] }}
	w := SpendWindowFrom(time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC), at(t))
	got, err := r.Spend(context.Background(), w)
	require.NoError(t, err)
	assert.InDelta(t, 9.0, got, 1e-9, "2026-09-17's two rows and today's 4, never the day before the window")
	assert.Equal(t, []string{"Bearer prov-secret", "Bearer key-secret"}, rt.auth)

	_, err = OpenRouterSpend{RT: rt, Getenv: func(string) string { return "" }}.Spend(context.Background(), w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), OpenRouterActivityEnv+" is not in this environment")

	_, err = r.Spend(context.Background(), SpendWindowFrom(at(t).Add(-40*24*time.Hour), at(t)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "last 30 completed days")

	for _, p := range SpendReaders(rt, func(string) string { return "" }) {
		if p.Provider() == "openrouter" {
			continue
		}
		_, err := p.Spend(context.Background(), w)
		assert.Error(t, err, p.Provider()+" has no readout and must say so")
	}
}

// The receipts file is read only for the window it covers.
func TestReceiptsAreReadOnlyForTheirWindow(t *testing.T) {
	t.Parallel()
	w := SpendWindowFrom(time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC), at(t))
	path := filepath.Join(t.TempDir(), "receipts.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"evidence":"spend-receipts","from":"2026-09-17T00:00:00Z","to":"2026-09-18T08:30:00Z","friends":{"alex":1000}}`), 0o600))
	got, err := FileReceipts{Path: path}.Tokens(context.Background(), w)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"alex": 1000}, got)

	require.NoError(t, os.WriteFile(path, []byte(`{"evidence":"spend-receipts","from":"2026-09-16T00:00:00Z","to":"2026-09-18T08:30:00Z","friends":{"alex":1000}}`), 0o600))
	_, err = FileReceipts{Path: path}.Tokens(context.Background(), w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "and the window is 2026-09-17T00:00:00Z..2026-09-18T09:00:00Z")
}
