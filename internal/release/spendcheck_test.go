package release

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// spendT0 is the in-memory store's clock: every run it records ends on 2030-01-02 (UTC).
var spendT0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

// fakeSpend is a provider's own readout, or a readout that cannot be read.
type fakeSpend struct {
	name string
	sub  bool
	got  SpendReadout
	err  error
	// asked is the window the gate asked about.
	asked [2]time.Time
}

func (f *fakeSpend) Name() string       { return f.name }
func (f *fakeSpend) Subscription() bool { return f.sub }
func (f *fakeSpend) Spend(_ context.Context, from, to time.Time) (SpendReadout, error) {
	f.asked = [2]time.Time{from, to}
	return f.got, f.err
}

// spendStore is a running sprint on the in-memory store (store.Mem) in which member m1 took
// and finished one card per usage line, ok or not: each finish books the run's record, at
// its charged figure, on the card's primary, ended at spendT0.
func spendStore(t *testing.T, usages ...string) *store.Store {
	t.Helper()
	ctx := context.Background()
	m := store.NewMem()
	var mu sync.Mutex
	now, n := spendT0, 0
	st := &store.Store{B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
		NewID: func() string { mu.Lock(); defer mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, st.Init(ctx))
	require.NoError(t, m.SetCoordinator(ctx, "coordinator"))
	must := func(step store.Step) store.Result {
		t.Helper()
		res, err := st.Run(ctx, step)
		require.NoError(t, err, step.Verb)
		require.Empty(t, res.Refused, "%s refused", step.Verb)
		return res
	}
	zero := 0.0
	beat := func() {
		t.Helper()
		_, err := st.Beat(ctx, "m1", &zero, hostload.Source{})
		require.NoError(t, err)
	}
	beat()
	must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: len(usages) + 1}))
	must(store.AddStep(sprint.AddReq{Stream: "s1", Count: len(usages)}))
	_, _, _, err := st.SetMachine(ctx, true)
	require.NoError(t, err)
	for i, u := range usages {
		var ready []*sprint.Card
		for range 5 {
			mu.Lock()
			now = now.Add(time.Second)
			mu.Unlock()
			beat()
			_, err := st.Tick(ctx)
			require.NoError(t, err)
			s, err := st.Load(ctx, store.All, nil)
			require.NoError(t, err)
			if ready = s.Fleet.Cell("m1", sprint.Ready); len(ready) > 0 {
				break
			}
		}
		require.NotEmpty(t, ready, "no card was dealt to m1 for run %d", i)
		id := ready[0].ID
		must(store.TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{IDs: []string{id}}, Gens: map[string]int{id: ready[0].Int("gen")}, Who: "m1"}))
		s, err := st.Load(ctx, store.All, nil)
		require.NoError(t, err)
		// every other run ends with no result: a paid call is priced whatever its end
		must(store.FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{id}}, Gens: map[string]int{id: s.Fleet.Card(id).Int("gen")},
			Failed: i%2 == 1, Usage: u, Who: "m1"}))
	}
	// the ticks after the last finish book its record on the primary, as the machine does
	for range 3 {
		mu.Lock()
		now = now.Add(time.Second)
		mu.Unlock()
		beat()
		_, err := st.Tick(ctx)
		require.NoError(t, err)
	}
	return st
}

// storeSpend is the gate's store side over the in-memory store's own load.
func storeSpend(st *store.Store) SnapshotSpend {
	return func(ctx context.Context) (*sprint.Snapshot, error) { return st.Load(ctx, store.All, nil) }
}

// spendCut runs `cut` over the window 2030-01-02 with the gate given; its exit, its stderr
// and the tags it made.
func spendCut(t *testing.T, g *SpendGate) (int, string, []string) {
	t.Helper()
	f := cutForge()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	require.NoError(t, os.WriteFile(path, []byte("# nova-tools changelog\n"), 0o644))
	deps := cutDeps(t, f)
	deps.Spend = g
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "main",
		"--version", "v0.16.0", "--changelog", path, "--spend-since", "2030-01-02", "--spend-until", "2030-01-03"}, &out, &errs, deps)
	return code, errs.String(), f.tagged
}

// A release is refused when the spend the store recorded over its window misses a paid
// provider's own count by more than five percent (2026-10-04: openrouter counted $2,250 and
// the sprint's records held $836), the refusal naming the provider, both figures and the gap;
// a 3% gap passes; a provider whose readout cannot be read refuses, as does a provider the
// store recorded with no readout and a store that cannot be read; and a subscription friend's
// recorded tokens are set beside its harness's receipts the same way.
func TestAReleaseIsRefusedWhenRecordedSpendMissesTheProvidersOwn(t *testing.T) {
	t.Parallel()
	paid := []string{
		"input=1000 output=100 model=openrouter/m actual_usd=500 actual_by=harness",
		"input=1000 output=100 model=openrouter/m actual_usd=336 actual_by=harness", // a run with no result, priced
	}

	t.Run("a 2,250 against 836 gap refuses naming the provider", func(t *testing.T) {
		t.Parallel()
		or := &fakeSpend{name: "openrouter", got: SpendReadout{USD: 2250}}
		code, errs, tagged := spendCut(t, &SpendGate{Providers: []SpendProvider{or}, Store: storeSpend(spendStore(t, paid...))})
		assert.Equal(t, 2, code, errs)
		assert.Empty(t, tagged, "no tag past the spend gate")
		assert.Contains(t, errs, "RELEASE CUT SPEND provider=openrouter unit=usd store=$836.00 provider_says=$2250.00 gap=$1414.00 share=62.8% bound=5% verdict=refused")
		assert.Contains(t, errs, "RELEASE CUT REFUSED reason=spend-gate window=2030-01-02..2030-01-03 over=1 unreadable=0 store=read named=openrouter")
		assert.Equal(t, [2]time.Time{time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC), time.Date(2030, 1, 3, 0, 0, 0, 0, time.UTC)}, or.asked, "the provider is asked the window's whole UTC days")
	})

	t.Run("a 3% gap passes and the tag is cut", func(t *testing.T) {
		t.Parallel()
		or := &fakeSpend{name: "openrouter", got: SpendReadout{USD: 861.86}} // $836 is 3.0% short
		code, errs, tagged := spendCut(t, &SpendGate{Providers: []SpendProvider{or}, Store: storeSpend(spendStore(t, paid...))})
		require.Equal(t, 0, code, errs)
		assert.Equal(t, []string{"v0.16.0 abc123abc123def"}, tagged)
		assert.Contains(t, errs, "RELEASE CUT SPEND provider=openrouter unit=usd store=$836.00 provider_says=$861.86 gap=$25.86 share=3.0% bound=5% verdict=ok")
		assert.Contains(t, errs, "RELEASE CUT NOTE spend-gate=ok window=2030-01-02..2030-01-03 providers=1")
	})

	t.Run("an unreadable provider refuses naming it", func(t *testing.T) {
		t.Parallel()
		or := &fakeSpend{name: "openrouter", got: SpendReadout{USD: 836}}
		inc := &fakeSpend{name: "inception", err: errors.New("the usage endpoint answered 503")}
		code, errs, tagged := spendCut(t, &SpendGate{Providers: []SpendProvider{or, inc}, Store: storeSpend(spendStore(t, paid...))})
		assert.Equal(t, 2, code, errs)
		assert.Empty(t, tagged)
		assert.Contains(t, errs, `RELEASE CUT SPEND provider=inception unit=usd store=$0.00 provider_says=unreadable verdict=refused why="the usage endpoint answered 503"`)
		assert.Contains(t, errs, "verdict=ok", "openrouter itself matched")
		assert.Contains(t, errs, "over=0 unreadable=1 store=read named=inception")
	})

	t.Run("a provider the store recorded with no readout refuses", func(t *testing.T) {
		t.Parallel()
		or := &fakeSpend{name: "openrouter", got: SpendReadout{USD: 836}}
		st := spendStore(t, append(paid[:2:2], "input=10 output=1 model=opencode/m actual_usd=5 actual_by=harness")...)
		code, errs, tagged := spendCut(t, &SpendGate{Providers: []SpendProvider{or}, Store: storeSpend(st)})
		assert.Equal(t, 2, code, errs)
		assert.Empty(t, tagged)
		assert.Contains(t, errs, "RELEASE CUT SPEND provider=opencode unit=usd store=$5.00 provider_says=unreadable verdict=refused")
		assert.Contains(t, errs, "named=opencode")
	})

	t.Run("a store that cannot be read refuses", func(t *testing.T) {
		t.Parallel()
		or := &fakeSpend{name: "openrouter", got: SpendReadout{USD: 0}}
		down := SnapshotSpend(func(context.Context) (*sprint.Snapshot, error) { return nil, errors.New("connection refused") })
		code, errs, tagged := spendCut(t, &SpendGate{Providers: []SpendProvider{or}, Store: down})
		assert.Equal(t, 2, code, errs)
		assert.Empty(t, tagged)
		assert.Contains(t, errs, `RELEASE CUT SPEND store=unreadable verdict=refused why="the store's records could not be read: connection refused"`)
		assert.Contains(t, errs, "store=unreadable named=store")
	})

	t.Run("subscription token counts are compared the same way", func(t *testing.T) {
		t.Parallel()
		sub := []string{
			"input=60000 output=10000 reasoning=5000 model=zai/glm",
			"input=20000 cache_read=5000 model=zai/glm", // 100,000 tokens recorded
		}
		over := &fakeSpend{name: "zai", sub: true, got: SpendReadout{Tokens: 150000}}
		code, errs, tagged := spendCut(t, &SpendGate{Providers: []SpendProvider{over}, Store: storeSpend(spendStore(t, sub...))})
		assert.Equal(t, 2, code, errs)
		assert.Empty(t, tagged)
		assert.Contains(t, errs, "RELEASE CUT SPEND provider=zai unit=tokens store=100000 provider_says=150000 gap=50000 share=33.3% bound=5% verdict=refused")

		within := &fakeSpend{name: "zai", sub: true, got: SpendReadout{Tokens: 102000}}
		code, errs, tagged = spendCut(t, &SpendGate{Providers: []SpendProvider{within}, Store: storeSpend(spendStore(t, sub...))})
		require.Equal(t, 0, code, errs)
		assert.Len(t, tagged, 1)
		assert.Contains(t, errs, "RELEASE CUT SPEND provider=zai unit=tokens store=100000 provider_says=102000 gap=2000 share=2.0% bound=5% verdict=ok")
	})
}

// The production gate (Main's) refuses while nova-update has no reader of the store: a
// release is never cut on spend nobody checked.
func TestTheProductionSpendGateRefusesWithoutAStoreReader(t *testing.T) {
	t.Parallel()
	g := ProductionSpend(func(string) string { return "" })
	v := CheckSpend(context.Background(), *g, spendT0, spendT0.Add(24*time.Hour))
	assert.True(t, v.Refused())
	assert.Contains(t, v.StoreErr, "no reader of the sprint store")
	require.Len(t, v.Lines, 1)
	assert.Equal(t, "openrouter", v.Lines[0].Provider)
	assert.Contains(t, v.Lines[0].Unreadable, OpenRouterActivityKeyEnv+" is not in this environment")
}

// The window is since the previous tag's day when none is given, and with no previous tag
// and no --spend-since the cut refuses rather than guess one.
func TestTheSpendWindowIsSinceThePreviousTag(t *testing.T) {
	t.Parallel()
	f := &timedForge{fakeForge: cutForge(), at: map[string]time.Time{"v0.15.10": time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)}}
	or := &fakeSpend{name: "openrouter"}
	deps := cutDeps(t, f)
	deps.Spend = &SpendGate{Providers: []SpendProvider{or}, Store: SnapshotSpend(func(context.Context) (*sprint.Snapshot, error) { return &sprint.Snapshot{}, nil })}
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "r/n", "--from", "main", "--version", "v0.16.0", "--changelog", path}, &out, &errs, deps)
	require.Equal(t, 0, code, errs.String())
	assert.Equal(t, [2]time.Time{time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)}, or.asked)

	none := cutForge()
	none.tags = nil
	deps = cutDeps(t, none)
	deps.Spend = &SpendGate{Providers: []SpendProvider{or}}
	errs.Reset()
	code = Run("nova-update", []string{"cut", "--repo", "r/n", "--from", "main", "--version", "v0.16.0", "--changelog", path}, &out, &errs, deps)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs.String(), "no previous release tag to measure the spend window from")
}

type timedForge struct {
	*fakeForge
	at map[string]time.Time
}

func (f *timedForge) TagTime(_ context.Context, _, tag string) (time.Time, error) {
	t, ok := f.at[tag]
	if !ok {
		return time.Time{}, fmt.Errorf("no tag %s", tag)
	}
	return t, nil
}

// roundTrip is a transport that answers one body.
type roundTrip struct {
	status int
	body   string
	auth   string
}

func (r *roundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	r.auth = req.Header.Get("Authorization")
	return &http.Response{StatusCode: r.status, Body: io.NopCloser(strings.NewReader(r.body)), Header: http.Header{}, Request: req}, nil
}

// openrouter's activity is summed over the window's days only; a window past its 30 days,
// no key, or an answer not of the shape cannot be read, and the key is never in the why.
func TestOpenRouterActivityIsSummedOverTheWindow(t *testing.T) {
	t.Parallel()
	now := func() time.Time { return spendT0 }
	key := func(k string) string {
		if k == OpenRouterActivityKeyEnv {
			return "sk-secret"
		}
		return ""
	}
	rt := &roundTrip{status: 200, body: `{"data":[{"date":"2029-12-31","usage":7},{"date":"2030-01-01","usage":1.5},{"date":"2030-01-01","usage":2.25},{"date":"2030-01-02","usage":100}]}`}
	a := &OpenRouterActivity{Getenv: key, Transport: rt, Now: now}
	day := func(d int) time.Time { return time.Date(2030, 1, d, 0, 0, 0, 0, time.UTC) }
	got, err := a.Spend(context.Background(), day(1), day(2))
	require.NoError(t, err)
	assert.InDelta(t, 3.75, got.USD, 1e-9)
	assert.Equal(t, "Bearer sk-secret", rt.auth)

	_, err = a.Spend(context.Background(), day(1).AddDate(0, 0, -40), day(2))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reaches back 30 days")

	_, err = (&OpenRouterActivity{Getenv: func(string) string { return "" }, Transport: rt, Now: now}).Spend(context.Background(), day(1), day(2))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nova-secrets exec --only "+OpenRouterActivityKeyEnv)

	for _, bad := range []*roundTrip{{status: 401, body: `{}`}, {status: 200, body: `{"data":[{"date":"2030-01-01"}]}`}, {status: 200, body: `nope`}} {
		_, err = (&OpenRouterActivity{Getenv: key, Transport: bad, Now: now}).Spend(context.Background(), day(1), day(2))
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "sk-secret")
	}
}
