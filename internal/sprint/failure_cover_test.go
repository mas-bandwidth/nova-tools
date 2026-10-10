package sprint

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/stretchr/testify/assert"
)

// providerOf is the part of a model id before the "/": the route's provider, or the
// whole id when the deal wrote no provider.
func TestFailureCoverProviderOf(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, model, want string
	}{
		{"a routed model", "provider/model", "provider"},
		{"a model with no provider", "gpt-4", "gpt-4"},
		{"an empty model", "", ""},
		{"only the separator", "/model", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, providerOf(tc.model))
		})
	}
}

// liftSpent says the provider's return has lifted the bound on tier before: the tier is
// one of the comma list FieldFailureBack holds, and none when the list is empty.
func TestFailureCoverLiftSpent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, back, tier string
		want             bool
	}{
		{"the tier was lifted", "flash,pro", "flash", true},
		{"the tier was not lifted", "flash,pro", "frontier", false},
		{"nothing was lifted", "", "flash", false},
		{"spaces around an item are ignored", " flash , pro ", "pro", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pr := &Card{ID: "p", Fields: map[string]string{FieldFailureBack: tc.back}}
			assert.Equal(t, tc.want, liftSpent(pr, tc.tier))
		})
	}
}

// dealtAbove is the tiers of the ladder above tier a deal draws from: frontier is never
// dealt, so it is dropped from every list, and heavy is the last tier dealt.
func TestFailureCoverDealtAbove(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, tier string
		want       []string
	}{
		{"flash deals pro and heavy", cardhdr.RouteFlash, []string{cardhdr.RoutePro, cardhdr.RouteHeavy}},
		{"pro deals heavy", cardhdr.RoutePro, []string{cardhdr.RouteHeavy}},
		{"heavy deals nothing (frontier is never dealt)", cardhdr.RouteHeavy, []string{}},
		{"frontier deals nothing", cardhdr.RouteFrontier, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, dealtAbove(tc.tier))
		})
	}
}

// identicalWorkWhat is the bound's judgment on a second identical failure, in the words
// both the finish and the tick use.
func TestFailureCoverIdenticalWorkWhat(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"p.w1: attempts 2 and 3 failed the same way (no result), the second identical failure: not reworked on the same tier a third time; rework it with a fix on the next tier, or drop it; its history: nova-sprint log --card p",
		identicalWorkWhat("p.w1", 3, "no result", "p"))
}

// providerBack says the provider is back for the attempt whose bound wc is: the bound was
// a provider failure and an ok take on one of the providers its failed takes ran on, of
// any card, finished after the attempt's last take ended. The tier is not the provider's,
// and no finished time, no model or a not-provider bound is no return.
func TestFailureCoverProviderBack(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	bound := func(finished, err, model string) *Card {
		return &Card{ID: "p.w1", Fields: map[string]string{
			FieldProviderTake + "1": ProviderTake{Route: "r", Model: model, Finished: finished, Error: err}.String(),
		}}
	}
	fleet := func(model, finished string) *Table {
		f := NewTable(Fleet)
		f.SetRows([]string{"m1"})
		f.Put(&Card{ID: "ok1", Row: "m1", Col: DoneOK, Fields: map[string]string{FieldModel: model, "finished": finished}})
		return f
	}
	for _, tc := range []struct {
		name  string
		wc    *Card
		fleet *Table
		want  bool
	}{
		{"the provider is back after the failed take", bound(stamp(t0), "server_error", "p/m"), fleet("p/other", stamp(t0.Add(time.Minute))), true},
		{"the ok take ended before the failure", bound(stamp(t0), "server_error", "p/m"), fleet("p/other", stamp(t0.Add(-time.Minute))), false},
		{"another provider's ok take is not the provider's", bound(stamp(t0), "server_error", "p/m"), fleet("q/other", stamp(t0.Add(time.Minute))), false},
		{"the bound is not a provider failure", bound(stamp(t0), "no result: no RESULT.md shape", "p/m"), fleet("p/other", stamp(t0.Add(time.Minute))), false},
		{"the failed take kept no finished time", bound("not a time", "server_error", "p/m"), fleet("p/other", stamp(t0.Add(time.Minute))), false},
		{"the failed take names no model", bound(stamp(t0), "server_error", ""), fleet("p/other", stamp(t0.Add(time.Minute))), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &Snapshot{Fleet: tc.fleet}
			assert.Equal(t, tc.want, providerBack(s, tc.wc))
		})
	}
}
