package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRouteRestCoverUntilSaid pins RouteRest.UntilSaid: a timed rest says the
// RFC3339 instant its rest ends, an open one (UntilSaid of OpenUntil) says
// "paid", never a clock, as the provider's funds rest is written (route_rest.go).
func TestRouteRestCoverUntilSaid(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		r    RouteRest
		want string
	}{
		{
			name: "main: a timed rest says its stamp",
			r:    RouteRest{Until: coverT0.Add(RouteRestFor)},
			want: "2030-01-02T03:30:00Z",
		},
		{
			name: "refusal: an open rest says paid, no clock",
			r:    RouteRest{Until: OpenUntil},
			want: "paid",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.r.UntilSaid())
			assert.Equal(t, tc.want == "paid", tc.r.Open(), "Open agrees with UntilSaid")
		})
	}
}

// TestRouteRestCoverFunds pins RouteRest.Funds: the rest is the provider's
// want of funds exactly when its cause is out-of-credit or balance, never a
// rest for its key or rule 3's no-result.
func TestRouteRestCoverFunds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		cause string
		want  bool
	}{
		{"main: out of credit", RestCredit, true},
		{"main: low on funds", RestBalance, true},
		{"refusal: a rest for its key", RestAuth, false},
		{"refusal: rule 3's no-result", RestNoResult, false},
		{"refusal: no cause at all", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, (RouteRest{Cause: tc.cause}).Funds())
		})
	}
}

// TestRouteRestCoverSaid pins RouteRest.Said: the rest's reason as a line says
// it is the provider's words, and with none it says rule 3's.
func TestRouteRestCoverSaid(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		r    RouteRest
		want string
	}{
		{
			name: "main: the provider's words",
			r:    RouteRest{Cause: RestCredit, Why: "out of credit: provider p balance $0.00"},
			want: "out of credit: provider p balance $0.00",
		},
		{
			name: "refusal: no words say rule 3's",
			r:    RouteRest{Cause: RestNoResult},
			want: "its children ended with no result",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.r.Said())
		})
	}
}
