package release

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeUsage is a provider's own readout with a figure or an error.
type fakeUsage struct {
	name, unit string
	reported   float64
	err        error
}

func (f fakeUsage) Name() string { return f.name }
func (f fakeUsage) Unit() string { return f.unit }
func (f fakeUsage) Reported(context.Context, SpendWindow) (float64, error) {
	return f.reported, f.err
}

// fakeStore is the store's record by provider.
type fakeStore map[string]float64

func (s fakeStore) Recorded(_ context.Context, provider, _ string, _ SpendWindow) (float64, error) {
	return s[provider], nil
}

func cutWithSpend(t *testing.T, c *SpendCheck, extra ...string) (code int, out, errs string, tagged []string) {
	t.Helper()
	f := cutForge()
	deps := cutDeps(t, f)
	deps.Spend = c
	var o, e bytes.Buffer
	args := cutArgs(filepath.Join(t.TempDir(), "CHANGELOG.md"), extra...)
	code = Run("nova-update", args, &o, &e, deps)
	return code, o.String(), e.String(), f.tagged
}

func spendOf(store fakeStore, ps ...fakeUsage) *SpendCheck {
	c := &SpendCheck{Store: store}
	for _, p := range ps {
		c.Providers = append(c.Providers, p)
	}
	return c
}

func TestAReleaseIsRefusedWhenRecordedSpendMissesTheProvidersOwn(t *testing.T) {
	t.Parallel()
	// 2026-10-04: OpenRouter's account showed about $2,250 and the cost panel $836.
	c := spendOf(fakeStore{"openrouter": 836}, fakeUsage{name: "openrouter", unit: SpendUSD, reported: 2250})
	code, _, errs, tagged := cutWithSpend(t, c, "--spend-since", "2026-10-01")
	assert.Equal(t, 2, code, errs)
	assert.Empty(t, tagged, "a refused release tags nothing")
	assert.Contains(t, errs, "provider=openrouter")
	assert.Contains(t, errs, "store=$836.00")
	assert.Contains(t, errs, "provider-reported=$2250.00")
	assert.Contains(t, errs, "gap=$1414.00")
	assert.Contains(t, errs, "reason=spend-gate")
}

func TestAThreePercentGapPasses(t *testing.T) {
	t.Parallel()
	c := spendOf(fakeStore{"openrouter": 970}, fakeUsage{name: "openrouter", unit: SpendUSD, reported: 1000})
	code, out, errs, tagged := cutWithSpend(t, c, "--spend-since", "2026-10-01")
	assert.Equal(t, 0, code, errs)
	assert.Len(t, tagged, 1)
	assert.Contains(t, out, "spend=ok")
}

func TestAnUnreadableProviderRefusesAndIsNamed(t *testing.T) {
	t.Parallel()
	c := spendOf(fakeStore{"openrouter": 100, "inception": 5},
		fakeUsage{name: "openrouter", unit: SpendUSD, reported: 100},
		fakeUsage{name: "inception", unit: SpendUSD, err: errors.New("GET usage answered 401")})
	code, _, errs, tagged := cutWithSpend(t, c, "--spend-since", "2026-10-01")
	assert.Equal(t, 2, code, errs)
	assert.Empty(t, tagged)
	assert.Contains(t, errs, "provider=inception")
	assert.Contains(t, errs, "unreadable=")
	assert.Contains(t, errs, "refused=1")
}

func TestSubscriptionTokensAreComparedTheSameWay(t *testing.T) {
	t.Parallel()
	over := spendOf(fakeStore{"claude": 400000}, fakeUsage{name: "claude", unit: SpendTokens, reported: 1000000})
	code, _, errs, _ := cutWithSpend(t, over, "--spend-since", "2026-10-01")
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "provider=claude unit=tokens store=400000 provider-reported=1000000 gap=600000")

	within := spendOf(fakeStore{"claude": 980000}, fakeUsage{name: "claude", unit: SpendTokens, reported: 1000000})
	code, _, errs, _ = cutWithSpend(t, within, "--spend-since", "2026-10-01")
	assert.Equal(t, 0, code, errs)
}

func TestARecordedFigureWithNothingReportedIsAGap(t *testing.T) {
	t.Parallel()
	f := SpendFigure{Recorded: 3}
	assert.True(t, f.Refused())
	assert.False(t, SpendFigure{}.Refused(), "nothing against nothing agrees")
}

func TestTheSpendGateNeedsAWindowAndAWaiverNeedsAReason(t *testing.T) {
	t.Parallel()
	c := spendOf(fakeStore{"openrouter": 1000}, fakeUsage{name: "openrouter", unit: SpendUSD, reported: 1000})
	code, _, errs, _ := cutWithSpend(t, c)
	assert.Equal(t, 2, code, "no window and nothing to find one: a refusal, never a pass; %s", errs)
	assert.Contains(t, errs, "--spend-since")

	bad := spendOf(fakeStore{"openrouter": 1}, fakeUsage{name: "openrouter", unit: SpendUSD, reported: 1000})
	code, _, errs, _ = cutWithSpend(t, bad, "--no-spend-gate")
	assert.Equal(t, 2, code, errs)
	code, out, errs, tagged := cutWithSpend(t, bad, "--no-spend-gate", "--reason", "readout down")
	require.Equal(t, 0, code, errs)
	assert.Len(t, tagged, 1)
	assert.Contains(t, out, "RELEASE CUT SPEND WAIVED reason=readout")
}

func TestAWindowFoundFromThePreviousTagIsUsed(t *testing.T) {
	t.Parallel()
	c := spendOf(fakeStore{"openrouter": 1000}, fakeUsage{name: "openrouter", unit: SpendUSD, reported: 1000})
	var seen string
	c.Window = func(_ context.Context, previous string, _ time.Time) (SpendWindow, error) {
		seen = previous
		return SpendWindow{From: at(t).AddDate(0, 0, -3), To: at(t)}, nil
	}
	code, _, errs, _ := cutWithSpend(t, c)
	assert.Equal(t, 0, code, errs)
	assert.Equal(t, "v0.15.10", seen)
}

func TestNoReadoutsWiredIsSkippedNotPassed(t *testing.T) {
	t.Parallel()
	code, out, errs, _ := cutWithSpend(t, nil)
	assert.Equal(t, 0, code, errs)
	assert.Contains(t, errs, "spend-gate=skipped")
	assert.Contains(t, out, "spend=skipped")
}
