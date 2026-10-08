package release

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/provbalance"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TagTime makes the fake forge a TagTimer: every tag was made a day before the cut.
func (f *fakeForge) TagTime(_ context.Context, _, tag string) (time.Time, error) {
	return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC), nil
}

// noSpend is a spend gate with nothing to compare: a store that recorded no spend and
// knows no provider, the cut tests' own that are not about spend.
func noSpend() *SpendSources {
	return &SpendSources{Store: SnapshotSpend{S: &sprint.Snapshot{}}, Receipts: fakeReceipts{}}
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

// spendStore is the store's read: an openrouter route and one card whose records over the
// window (since the previous tag's day, 2026-09-17) hold $836 of openrouter in a take, a
// take with no result and a read, $50 of it before the window, and a subscription friend's
// run of 1000 tokens.
func spendStore(t *testing.T) *sprint.Snapshot {
	t.Helper()
	s := &sprint.Snapshot{Now: at(t), Work: sprint.NewTable(sprint.Work), Fleet: sprint.NewTable(sprint.Fleet)}
	s.Routes = []sprint.Route{{Name: "pro-or", Tier: "pro", Provider: "openrouter", Model: "m"}}
	s.Work.SetRows([]string{"s1"})
	pr := &sprint.Card{ID: "s1-1", Row: "s1", Col: sprint.Working, Fields: map[string]string{}}
	in, before := "2026-09-17T18:00:00Z", "2026-09-16T18:00:00Z"
	for _, record := range []struct{ key, line string }{
		{"a", "kind=work card=s1-1 attempt=0 take=0 gen=0 who=- on_route=pro-or on_model=- on_tier=- end=ok at=" + in + " input=10 actual_usd=500 actual_by=harness"},
		{"b", "kind=work card=s1-1 attempt=0 take=0 gen=0 who=- on_route=pro-or on_model=- on_tier=- end=no-result at=" + in + " input=10 actual_usd=300 actual_by=harness"},
		{"c", "kind=read card=s1-1.r1 attempt=0 take=0 gen=0 who=- on_route=- on_model=openrouter/m on_tier=- end=ok at=" + in + " input=10 predicted_usd=36"},
		{"d", "kind=work card=s1-1 attempt=0 take=0 gen=0 who=- on_route=pro-or on_model=- on_tier=- end=failed at=" + before + " input=10 actual_usd=50 actual_by=harness"},
		{"e", "kind=read card=s1-1.r2 attempt=0 take=0 gen=0 who=alex on_route=- on_model=- on_tier=- end=ok at=" + in + " input=600 output=400 " + sprint.UsageSubscription},
	} {
		pr.Fields[sprint.FieldCostRecord+record.key] = record.line
	}
	s.Work.Put(pr)
	return s
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
		code, out, errs, f := cutWithSpend(t, &SpendSources{Store: SnapshotSpend{S: spendStore(t)}, Providers: []ProviderSpend{or}, Receipts: fakeReceipts{"alex": 1000}})
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
		code, out, errs, f := cutWithSpend(t, &SpendSources{Store: SnapshotSpend{S: spendStore(t)}, Providers: []ProviderSpend{or}, Receipts: fakeReceipts{"alex": 1000}})
		require.Equal(t, 0, code, "out=%s errs=%s", out, errs)
		assert.Contains(t, errs, "SPEND provider=openrouter store=$836.00 provider_usd=$861.86 gap=$25.86 share=3.0% verdict=ok")
		assert.Contains(t, errs, "SPEND friend=alex store_tokens=1000 receipt_tokens=1000 gap=0 share=0.0% verdict=ok")
		assert.Contains(t, out, "spend=ok")
		assert.Len(t, f.tagged, 1)
	})

	t.Run("an unreadable provider refuses", func(t *testing.T) {
		t.Parallel()
		or := &fakeProvider{name: "openrouter", err: errors.New("GET activity answered 401")}
		code, _, errs, f := cutWithSpend(t, &SpendSources{Store: SnapshotSpend{S: spendStore(t)}, Providers: []ProviderSpend{or}, Receipts: fakeReceipts{"alex": 1000}})
		require.Equal(t, 2, code, errs)
		assert.Contains(t, errs, "SPEND provider=openrouter unread verdict=refuse: GET activity answered 401")
		assert.Contains(t, errs, "refused=1 providers=openrouter friends=-")
		assert.Empty(t, f.tagged)

		// a provider the store knows of and no readout is given for is unread too
		code, _, errs, _ = cutWithSpend(t, &SpendSources{Store: SnapshotSpend{S: spendStore(t)}, Receipts: fakeReceipts{"alex": 1000}})
		require.Equal(t, 2, code, errs)
		assert.Contains(t, errs, "SPEND provider=openrouter unread verdict=refuse: no readout of openrouter's own spend is known")
	})

	t.Run("subscription tokens are compared the same way", func(t *testing.T) {
		t.Parallel()
		or := &fakeProvider{name: "openrouter", usd: 836}
		code, _, errs, _ := cutWithSpend(t, &SpendSources{Store: SnapshotSpend{S: spendStore(t)}, Providers: []ProviderSpend{or}, Receipts: fakeReceipts{"alex": 2500, "emma": 300}})
		require.Equal(t, 2, code, errs)
		assert.Contains(t, errs, "SPEND friend=alex store_tokens=1000 receipt_tokens=2500 gap=1500 share=60.0% verdict=refuse")
		assert.Contains(t, errs, "SPEND friend=emma store_tokens=0 receipt_tokens=300 gap=300 share=100.0% verdict=refuse")
		assert.Contains(t, errs, "refused=2 providers=- friends=alex,emma")

		code, _, errs, _ = cutWithSpend(t, &SpendSources{Store: SnapshotSpend{S: spendStore(t)}, Providers: []ProviderSpend{or}})
		require.Equal(t, 2, code, errs)
		assert.Contains(t, errs, "SPEND friend=alex unread verdict=refuse: no harness receipts were given")
	})

	t.Run("the waiver goes into the changelog", func(t *testing.T) {
		t.Parallel()
		f := cutForge()
		deps := cutDeps(t, f)
		deps.Spend = &SpendSources{Store: SnapshotSpend{S: spendStore(t)}, Providers: []ProviderSpend{&fakeProvider{name: "openrouter", usd: 2250}}, Receipts: fakeReceipts{"alex": 1000}}
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
