package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coverT0 is a fixed instant; these tests take no clock and touch no store.
var coverT0 = time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)

// coverRestValue is the fleet table's property value of a provider's rest at
// coverT0, open (resting until paid) for the given cause, as RouteRest.value
// writes it and parseRest reads it back (route_rest.go).
func coverRestValue(cause string, cards ...string) string {
	r := RouteRest{At: coverT0, Until: OpenUntil, Cause: cause, Cards: cards,
		Why: "out of credit: provider p refused card c1"}
	return r.value()
}

// TestProviderFundsCoverSubject pins ProviderSubject: the stream word a provider's
// judgment is filed under. No stream id has a colon, so it is never a stream's.
func TestProviderFundsCoverSubject(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, provider, want string
	}{
		{"a provider's judgment", "openrouter", "provider:openrouter"},
		{"refusal: no provider name", "", "provider:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ProviderSubject(tc.provider))
		})
	}
}

// TestProviderFundsCoverPropBalance pins PropProviderBalance: the fleet table's
// property name that holds a provider's balance the poll last read.
func TestProviderFundsCoverPropBalance(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, provider, want string
	}{
		{"a provider's balance", "openrouter", "provider_balance_openrouter"},
		{"refusal: no provider name", "", "provider_balance_"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, PropProviderBalance(tc.provider))
		})
	}
}

// TestProviderFundsCoverValue pins ProviderBalance.value: the balance as the
// property holds it (`<balance|unknown> <at> <spend/hour> <used|-> <note>`),
// read back by ProviderBalances so the two cannot drift.
func TestProviderFundsCoverValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		b       ProviderBalance
		want    string
		known   bool
		balance float64
		hasUsed bool
		used    float64
		spend   float64
		note    string
	}{
		{
			name:    "main: a known balance with used",
			b:       ProviderBalance{Provider: "p", Known: true, Balance: 100, At: coverT0, SpendHour: 10.5, HasUsed: true, Used: 50, Note: "a poll note"},
			want:    "100 2030-01-02T03:00:00Z 10.5 50 a poll note",
			known:   true,
			balance: 100,
			hasUsed: true,
			used:    50,
			spend:   10.5,
			note:    "a poll note",
		},
		{
			name:    "refusal: an unknown balance",
			b:       ProviderBalance{Provider: "p", Known: false, Balance: 0, At: coverT0, SpendHour: 0, HasUsed: false, Used: 0, Note: "no balance endpoint"},
			want:    "unknown 2030-01-02T03:00:00Z 0 - no balance endpoint",
			known:   false,
			balance: 0,
			hasUsed: false,
			used:    0,
			spend:   0,
			note:    "no balance endpoint",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := NewTable(Fleet)
			f.SetProp(PropProviderBalance("p"), tc.b.value())
			got := ProviderBalances(f)["p"]
			assert.Equal(t, tc.want, tc.b.value())
			assert.Equal(t, tc.known, got.Known, "known reads back")
			assert.InDelta(t, tc.balance, got.Balance, 1e-9)
			assert.Equal(t, tc.hasUsed, got.HasUsed, "hasUsed reads back")
			assert.InDelta(t, tc.used, got.Used, 1e-9)
			assert.InDelta(t, tc.spend, got.SpendHour, 1e-9)
			assert.Equal(t, tc.note, got.Note, "note reads back")
		})
	}
}

// TestProviderFundsCoverSaid pins ProviderBalance.Said: the balance as a line
// says it (dollars and cents rounded up, and when it was read).
func TestProviderFundsCoverSaid(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		b    ProviderBalance
		want string
	}{
		{
			name: "main: a known balance",
			b:    ProviderBalance{Provider: "p", Known: true, Balance: 100, At: coverT0},
			want: "$100.00 at 2030-01-02T03:00:00Z",
		},
		{
			name: "main: a rounded-up negative balance",
			b:    ProviderBalance{Provider: "p", Known: true, Balance: -0.51, At: coverT0},
			want: "-$0.51 at 2030-01-02T03:00:00Z",
		},
		{
			name: "refusal: not polled yet",
			b:    ProviderBalance{Provider: "p", Known: false, At: time.Time{}},
			want: "unknown: not polled yet",
		},
		{
			name: "refusal: an unknown balance",
			b:    ProviderBalance{Provider: "p", Known: false, At: coverT0, Note: "no balance endpoint"},
			want: "unknown: no balance endpoint",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.b.Said())
		})
	}
}

// TestProviderFundsCoverOutOfCredit pins Snapshot.OutOfCredit: the words with
// which the tick refuses to start the machine when every enabled route rests
// because its provider is out of credit.
func TestProviderFundsCoverOutOfCredit(t *testing.T) {
	t.Parallel()
	routes := []Route{
		{Name: "a", Tier: "flash", Provider: "p", Enabled: true},
		{Name: "b", Tier: "pro", Provider: "p", Enabled: true},
	}
	credit := coverRestValue(RestCredit, "c1")
	for _, tc := range []struct {
		name      string
		props     map[string]string
		more      map[string]string
		routes    []Route
		wantEmpty bool
		contains  string
	}{
		{
			name:     "main: every provider is out of credit",
			props:    map[string]string{PropProviderRest("p"): credit},
			routes:   routes,
			contains: FundsCause + " (p)",
		},
		{
			name:      "refusal: the coordinator's rest of a provider with money",
			props:     map[string]string{PropProviderRest("p"): coverRestValue(RestCoordinator)},
			routes:    routes,
			wantEmpty: true,
		},
		{
			name:      "refusal: an enabled route with no rest",
			props:     nil,
			routes:    routes,
			wantEmpty: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := NewTable(Fleet)
			f.SetProps(tc.props)
			s := &Snapshot{Now: coverT0, Fleet: f, Routes: tc.routes}
			got := s.OutOfCredit()
			if tc.wantEmpty {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, tc.contains)
			assert.Contains(t, got, "a payment is the owner's")
		})
	}
}

// TestProviderFundsCoverFunded pins Funded: the coordinator's word that a provider
// was paid ends the rest of its funds holding now.
func TestProviderFundsCoverFunded(t *testing.T) {
	t.Parallel()
	routes := []Route{
		{Name: "a", Tier: "flash", Provider: "p", Enabled: true},
		{Name: "b", Tier: "pro", Provider: "p", Enabled: true},
	}
	openCredit := coverRestValue(RestCredit, "c1")
	for _, tc := range []struct {
		name        string
		props       map[string]string
		req         FundedReq
		wantRefused string
		was         string
	}{
		{
			name:  "main: the provider funded",
			props: map[string]string{PropProviderRest("p"): openCredit},
			req:   FundedReq{Provider: "p", Reason: "paid $100 in the console", Who: Coordinator},
			was:   openCredit,
		},
		{
			name:        "refusal: no reason given",
			props:       map[string]string{PropProviderRest("p"): openCredit},
			req:         FundedReq{Provider: "p", Reason: "", Who: Coordinator},
			wantRefused: "funded wants --reason: the payment made, in a few words",
		},
		{
			name:        "refusal: the provider does not rest for its funds",
			props:       nil,
			req:         FundedReq{Provider: "p", Reason: "paid", Who: Coordinator},
			wantRefused: "provider p does not rest for its funds",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := NewTable(Fleet)
			f.SetProps(tc.props)
			s := &Snapshot{Now: coverT0, Fleet: f, Routes: routes, Coordinator: Coordinator}
			p := Funded(s, tc.req)
			if tc.wantRefused != "" {
				require.Len(t, p.Refused, 1)
				assert.Contains(t, p.Refused[0].Why, tc.wantRefused)
				assert.Empty(t, p.Props)
				assert.Empty(t, p.Notes)
				assert.Empty(t, p.Units)
				return
			}
			require.Empty(t, p.Refused)
			require.Len(t, p.Props, 1, "one property write of the provider's rest")
			assert.Equal(t, Fleet, p.Props[0].Table)
			assert.Equal(t, PropProviderRest("p"), p.Props[0].Name)
			assert.Equal(t, tc.was, p.Props[0].Was, "guarded on the value read")
			assert.Contains(t, p.Props[0].Value, coverT0.UTC().Format(time.RFC3339), "the rest ends at now")
			assert.Contains(t, p.Props[0].Value, "ended: funded by "+Coordinator+": paid $100 in the console")
			require.Len(t, p.Notes, 1, "one happened note of the provider")
			assert.Equal(t, NProviderFunded, p.Notes[0].Type)
			assert.Equal(t, ProviderSubject("p"), p.Notes[0].Stream)
			assert.Equal(t, Coordinator, p.Notes[0].To)
			assert.Equal(t, Coordinator, p.Notes[0].Who)
			assert.Contains(t, p.Notes[0].What, "provider p serves again, its routes a, b")
			assert.Contains(t, p.Notes[0].What, "it was paid (paid $100 in the console, by "+Coordinator+")")
			require.Len(t, p.Units, 1, "one unit of the provider")
			assert.Equal(t, "p", p.Units[0].Key)
			assert.Contains(t, p.Units[0].Moved, "provider p funded")
			assert.Contains(t, p.Units[0].Moved, "serve again")
		})
	}
}
