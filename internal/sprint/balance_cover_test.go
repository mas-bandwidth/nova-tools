package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBalanceCoverPaid pins paid: the poll saw a payment to a provider resting
// since it refused a take: the read b is strictly higher than the read before it
// (was) or than the balance at the refusal (balance.go).
func TestBalanceCoverPaid(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		rest RouteRest
		was  ProviderBalance
		b    ProviderBalance
		want bool
	}{
		{
			name: "main: balance higher than the read before",
			rest: RouteRest{Provider: "p"},
			was:  ProviderBalance{Provider: "p", Known: true, Balance: 5},
			b:    ProviderBalance{Provider: "p", Known: true, Balance: 10},
			want: true,
		},
		{
			name: "main: balance higher than the balance at the refusal",
			rest: RouteRest{Provider: "p", HasBalance: true, Balance: 0.30},
			was:  ProviderBalance{Provider: "p", Known: false, Note: "no previous read"},
			b:    ProviderBalance{Provider: "p", Known: true, Balance: 1},
			want: true,
		},
		{
			name: "main: neither mark known, a read over zero (nova-tools#5220)",
			rest: RouteRest{Provider: "p"},
			was:  ProviderBalance{Provider: "p", Known: false, Note: "credits endpoint answered 503"},
			b:    ProviderBalance{Provider: "p", Known: true, Balance: 942.68},
			want: true,
		},
		{
			name: "refusal: neither mark known, a read at zero",
			rest: RouteRest{Provider: "p"},
			was:  ProviderBalance{Provider: "p"},
			b:    ProviderBalance{Provider: "p", Known: true, Balance: 0},
			want: false,
		},
		{
			name: "refusal: no payment seen",
			rest: RouteRest{Provider: "p", HasBalance: true, Balance: 10},
			was:  ProviderBalance{Provider: "p", Known: true, Balance: 5},
			b:    ProviderBalance{Provider: "p", Known: true, Balance: 3},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, paid(tc.rest, tc.was, tc.b))
		})
	}
}

// TestBalanceCoverLow pins ProviderBalance.Low: a balance calls for the coordinator's
// judgment at or under zero, or not over one hour of the spend; it rests nothing.
func TestBalanceCoverLow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		b    ProviderBalance
		want bool
	}{
		{
			name: "main: a known balance over an hour of spend",
			b:    ProviderBalance{Known: true, Balance: 100, SpendHour: 60},
			want: false,
		},
		{
			name: "refusal: an unknown balance is not low",
			b:    ProviderBalance{Known: false, Balance: 0, SpendHour: 60},
			want: false,
		},
		{
			name: "refusal: out of credit at zero",
			b:    ProviderBalance{Known: true, Balance: 0, SpendHour: 60},
			want: true,
		},
		{
			name: "refusal: low on funds under an hour of spend",
			b:    ProviderBalance{Known: true, Balance: 50, SpendHour: 60},
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.b.Low())
		})
	}
}

// TestBalanceCoverUntilSaid pins untilSaid: a rest's end as a line says it
// (balance.go).
func TestBalanceCoverUntilSaid(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		at   time.Time
		want string
	}{
		{
			name: "main: a rest's end as a time",
			at:   coverT0,
			want: "2030-01-02T03:00:00Z",
		},
		{
			name: "refusal: an open rest ends when paid",
			at:   OpenUntil,
			want: "paid",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, untilSaid(tc.at))
		})
	}
}

// TestBalanceCoverProviderRows pins ProviderRows: the providers table (balance.go),
// one row per provider the routes name, its balance from the fleet table's
// properties and its state from its routes' rests at now.
func TestBalanceCoverProviderRows(t *testing.T) {
	t.Parallel()
	routes := []Route{
		{Name: "r1", Tier: "flash", Provider: "p", Enabled: true},
		{Name: "r2", Tier: "flash", Provider: "q", Enabled: true},
		{Name: "r3", Tier: "flash", Provider: "r", Enabled: true},
	}
	f := NewTable(Fleet)
	f.SetProps(map[string]string{
		PropProviderBalance("p"): ProviderBalance{Known: true, Balance: 40, At: coverT0, SpendHour: 60, HasUsed: true, Used: 50}.value(),
		PropProviderRest("p"):    RouteRest{At: coverT0, Until: coverT0.Add(30 * time.Minute), Cause: RestCoordinator, Why: "rested by boss: low on funds"}.value(),
		PropProviderBalance("q"): ProviderBalance{Known: true, Balance: 100, At: coverT0, SpendHour: 60, HasUsed: true, Used: 50}.value(),
		// a balance poll's rest of before is retired: it holds no route
		PropProviderRest("q"): RouteRest{At: coverT0, Until: OpenUntil, Cause: RestBalance, Why: "low on funds"}.value(),
	})
	now := coverT0.Add(15 * time.Minute)
	rows := ProviderRows(routes, f, now)
	require.Len(t, rows, 3)

	assert.Equal(t, "p", rows[0].Name)
	assert.Equal(t, "$40.00", rows[0].Balance)
	assert.Equal(t, "2030-01-02T03:00:00Z", rows[0].BalanceAt)
	assert.InDelta(t, 60.0, rows[0].SpendHour, 1e-9)
	assert.Contains(t, rows[0].State, "resting until ")
	assert.Contains(t, rows[0].State, "coordinator: rested by boss: low on funds")
	assert.Empty(t, rows[0].Note)

	assert.Equal(t, "q", rows[1].Name)
	assert.Equal(t, "$100.00", rows[1].Balance)
	assert.Equal(t, "2030-01-02T03:00:00Z", rows[1].BalanceAt)
	assert.Equal(t, "serving", rows[1].State)
	assert.Empty(t, rows[1].Note)

	assert.Equal(t, "r", rows[2].Name)
	assert.Equal(t, "unknown", rows[2].Balance)
	assert.Empty(t, rows[2].BalanceAt)
	assert.Equal(t, "serving", rows[2].State)
	assert.Equal(t, "not polled yet", rows[2].Note)
}

// TestBalanceCoverBalance pins Balance: the poll's step (balance.go) — each read
// written to its provider's property; it writes no rest, and ends a refused take's
// rest only on a payment seen.
func TestBalanceCoverBalance(t *testing.T) {
	t.Parallel()
	routes := []Route{
		{Name: "r1", Tier: "flash", Provider: "p", Enabled: true},
	}
	balWas := ProviderBalance{Known: true, Balance: 100, At: coverT0, HasUsed: true, Used: 90, SpendHour: 0}
	refusedRest := RouteRest{At: coverT0, Until: OpenUntil, Cause: RestCredit, Cards: []string{"c1"}, Why: "out of credit: provider p refused card c1"}
	type tc struct {
		name         string
		props        map[string]string
		reads        []ProviderRead
		wantProps    int
		wantNotes    int
		wantRestProp bool
		wantBalance  string
	}
	cases := []tc{
		{
			name: "main: a provider low on funds rests nothing",
			props: map[string]string{
				PropProviderBalance("p"): balWas.value(),
			},
			reads:        []ProviderRead{{Provider: "p", Known: true, Balance: 0.5, HasUsed: true, Used: 100}},
			wantProps:    1,
			wantNotes:    0,
			wantRestProp: false,
			wantBalance:  "0.5 ",
		},
		{
			name: "main: a payment seen ends a refused take's rest",
			props: map[string]string{
				PropProviderBalance("p"): balWas.value(),
				PropProviderRest("p"):    refusedRest.value(),
			},
			reads:        []ProviderRead{{Provider: "p", Known: true, Balance: 150}},
			wantProps:    2,
			wantNotes:    1,
			wantRestProp: true,
			wantBalance:  "150 ",
		},
		{
			name:         "refusal: an unknown balance writes no rest",
			props:        nil,
			reads:        []ProviderRead{{Provider: "p", Known: false, Note: "no balance endpoint"}},
			wantProps:    1,
			wantNotes:    0,
			wantRestProp: false,
			wantBalance:  "unknown",
		},
		{
			name: "refusal: a refused rest with no payment holds",
			props: map[string]string{
				// $10 at the refusal: $5 is no payment
				PropProviderRest("p"): RouteRest{At: coverT0, Until: OpenUntil, Cause: RestCredit, Cards: []string{"c1"}, Balance: 10, HasBalance: true, Why: "out of credit"}.value(),
			},
			reads:        []ProviderRead{{Provider: "p", Known: true, Balance: 5}},
			wantProps:    1,
			wantNotes:    0,
			wantRestProp: false,
			wantBalance:  "5 ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := NewTable(Fleet)
			f.SetProps(tc.props)
			s := &Snapshot{Now: coverT0.Add(10 * time.Minute), Fleet: f, Routes: routes, Coordinator: Coordinator}
			p := Balance(s, BalanceReq{Reads: tc.reads, Who: Coordinator})
			require.Empty(t, p.Refused, "Balance does not refuse")
			require.Len(t, p.Props, tc.wantProps)
			assert.Equal(t, Fleet, p.Props[0].Table)
			assert.Equal(t, PropProviderBalance("p"), p.Props[0].Name)
			assert.Contains(t, p.Props[0].Value, tc.wantBalance, "the balance is written to the property")
			if tc.wantRestProp {
				require.Len(t, p.Props, 2, "a rest is written alongside the balance")
				assert.Equal(t, PropProviderRest("p"), p.Props[1].Name)
				assert.Contains(t, p.Props[1].Value, "ended: a payment seen", "the rest ends on the payment")
				assert.Contains(t, p.Props[1].Value, stamp(s.Now)+" c1 "+RestCredit, "the rest ends now")
			}
			require.Len(t, p.Notes, tc.wantNotes)
			require.Len(t, p.Units, 1, "one balance unit")
			assert.Equal(t, "balance", p.Units[0].Key)
			assert.Contains(t, p.Units[0].Moved, "provider balances:")
		})
	}
}
