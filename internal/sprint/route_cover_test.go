package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/stretchr/testify/assert"
)

// TierRoutes is how many enabled routes each tier has, as `where` shows it: "flash=1 pro=3",
// "" when the store has none. A disabled route counts for no tier.
func TestRouteCoverTierRoutes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		routes []Route
		want   string
	}{
		{"a store with no route at all", nil, ""},
		{"every tier counted, the disabled left out", []Route{
			{Name: "flash-a", Tier: cardhdr.RouteFlash, Enabled: true},
			{Name: "flash-off", Tier: cardhdr.RouteFlash, Enabled: false},
			{Name: "pro-a", Tier: cardhdr.RoutePro, Enabled: true},
			{Name: "pro-b", Tier: cardhdr.RoutePro, Enabled: true},
			{Name: "pro-off", Tier: cardhdr.RoutePro, Enabled: false},
		}, "flash=1 pro=2 heavy=0"},
		{"a tier with no route counts zero", []Route{
			{Name: "pro-a", Tier: cardhdr.RoutePro, Enabled: true},
		}, "flash=0 pro=1 heavy=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, TierRoutes(tc.routes))
		})
	}
}

// DecidedWords is a work card's attempt decision as `card` prints it on its ATTEMPT line,
// " decided=<class>:<p>", with " decided_used=yes" when it routed the finish; "" when the
// card carries none or the line is not a decision.
func TestRouteCoverDecidedWords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		line string
		used bool
		want string
	}{
		{"a decision prints two decimals", "no_result p=0.5 op=op1", false, " decided=no_result:0.50"},
		{"a decision that routed the finish says so", "nothing_to_do p=1.0 op=op2", true, " decided=nothing_to_do:1.00 decided_used=yes"},
		{"a line that is not a decision prints nothing", "not a decision", false, ""},
		{"an empty line prints nothing", "", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, DecidedWords(tc.line, tc.used))
		})
	}
}

// AttemptLine is one attempt's record as `card <id>` prints it: the work card's route,
// model and tier, its member, its stamps, how it ended, the head it pushed and what it spent.
func TestRouteCoverAttemptLine(t *testing.T) {
	t.Parallel()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	base := func() map[string]string {
		return map[string]string{
			"attempt": "1", "gen": "1",
			FieldRoute: "flash-a", FieldModel: "p/m", FieldTier: cardhdr.RouteFlash,
			"member": "m1", "dealt": "2030-01-02T03:00:00Z",
			"taken": "2030-01-02T03:00:10Z", "finished": "2030-01-02T03:00:20Z",
			FieldUsage: "123", "head": sha,
		}
	}
	for _, tc := range []struct {
		name    string
		fields  map[string]string
		col     string
		want    []string
		wantSuf string
	}{
		{
			name:   "a finished ok attempt carries its head and decision",
			fields: map[string]string{"ok": "yes", FieldDecided: "no_result p=0.5 op=op1", FieldDecidedUsed: "yes"},
			col:    DoneOK,
			want: []string{
				"ATTEMPT 1", "card=p1.w1", "gen=1", "route=flash-a", "model=p/m",
				"tier=flash", "member=m1", "dealt=2030-01-02T03:00:00Z",
				"head=" + sha, "decided=no_result:0.50 decided_used=yes",
				"usage=123", "end=ok",
			},
		},
		{
			name:    "a failed attempt's report is cut at 160 columns",
			fields:  map[string]string{"ok": "no", "report": strings.Repeat("x", 300)},
			col:     DoneFailed,
			wantSuf: strings.Repeat("x", 152),
		},
		{
			name:   "a withdrawn attempt",
			fields: map[string]string{},
			col:    Withdrawn,
			want:   []string{"end=withdrawn"},
		},
		{
			name:   "an unplaced attempt is retired",
			fields: map[string]string{},
			col:    "",
			want:   []string{"end=retired"},
		},
		{
			name:   "an attempt still working is in flight",
			fields: map[string]string{},
			col:    Working,
			want:   []string{"end=in flight (working)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := base()
			for k, v := range tc.fields {
				f[k] = v
			}
			line := AttemptLine(&Card{ID: "p1.w1", Row: "m1", Col: tc.col, Fields: f})
			for _, w := range tc.want {
				assert.Contains(t, line, w)
			}
			if tc.wantSuf != "" {
				assert.True(t, strings.HasSuffix(line, tc.wantSuf), "the report is cut: %s", line)
			}
		})
	}
}

// AttemptLines is the work card's lines as `card <id>` prints them: one for each take the
// provider failed or its member refused at staging, then the card's own line.
func TestRouteCoverAttemptLines(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		fields  map[string]string
		wantLen int
		want    []string
	}{
		{
			name: "a card with no take prints only its own line",
			fields: map[string]string{
				"attempt": "1", FieldRoute: "flash-a", FieldModel: "p/m",
			},
			wantLen: 1,
			want:    []string{"ATTEMPT 1", "route=flash-a"},
		},
		{
			name: "a provider failure and a staging refusal each print a take line before the card's",
			fields: map[string]string{
				"attempt": "2", "gen": "2", FieldRoute: "flash-a", FieldModel: "p/m",
				FieldProviderTake + "1": ProviderTake{Route: "flash-a", Model: "p/m", Member: "m1", Finished: "2030-01-02T03:00:00Z", Usage: "5", Error: "server_error"}.String(),
				FieldStagingTake + "1":  ProviderTake{Route: "flash-a", Model: "p/m", Member: "m2", Finished: "2030-01-02T03:01:00Z", Error: "no room"}.String(),
			},
			want: []string{
				"take=1", "route=flash-a", "end=provider failure: server_error",
				"end=staging refused: no room",
			},
		},
		{
			name: "a take with no result keeps its own end word",
			fields: map[string]string{
				"attempt": "1", FieldRoute: "flash-a",
				FieldProviderTake + "1": ProviderTake{Route: "flash-a", Error: cardhdr.EndNoResult + ": no RESULT.md"}.String(),
			},
			want: []string{"end=" + cardhdr.EndNoResult + ": no RESULT.md"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lines := AttemptLines(&Card{ID: "p1.w1", Row: "m1", Col: DoneFailed, Fields: tc.fields})
			assert.NotEmpty(t, lines)
			joined := strings.Join(lines, "\n")
			for _, w := range tc.want {
				assert.Contains(t, joined, w)
			}
			if tc.wantLen > 0 {
				assert.Len(t, lines, tc.wantLen, "a card with no take prints one line, its own")
			}
		})
	}
}

// RouteStats is every route's record over the fleet table's work cards, the routes in name
// order, then each pinned model (pin:<model>) and each route the store no longer holds.
func TestRouteCoverRouteStats(t *testing.T) {
	t.Parallel()
	flash := Route{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "m", Enabled: true}
	pro := Route{Name: "pro-a", Tier: cardhdr.RoutePro, Enabled: true}
	t0 := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	fleet := func(cards ...*Card) *Table {
		f := NewTable(Fleet)
		f.SetRows([]string{"m1"})
		for _, c := range cards {
			f.Put(c)
		}
		return f
	}
	for _, tc := range []struct {
		name   string
		routes []Route
		fleet  *Table
		want   []RouteStat
	}{
		{
			name:  "a store with no route and no card",
			fleet: fleet(),
			want:  []RouteStat{},
		},
		{
			name:   "a route with no card keeps a zero row",
			routes: []Route{flash},
			fleet:  fleet(),
			want: []RouteStat{
				{Route: flash, MeanWall: "-"},
			},
		},
		{
			name:   "ok, failed, pinned and a provider take each count on their route",
			routes: []Route{flash, pro},
			fleet: fleet(
				&Card{ID: "p1.w1", Row: "m1", Col: DoneOK, Fields: map[string]string{
					FieldRoute: "flash-a", FieldModel: "p/m", "ok": "yes",
					"taken": stamp(t0), "finished": stamp(t0.Add(2 * time.Minute)),
				}},
				&Card{ID: "p2.w1", Row: "m1", Col: DoneFailed, Fields: map[string]string{
					FieldRoute: "pro-a", "ok": "no",
				}},
				&Card{ID: "p3.w1", Row: "m1", Col: DoneOK, Fields: map[string]string{
					FieldRoute: RoutePin, FieldModel: "q/m", "ok": "yes",
				}},
				&Card{ID: "p4.w1", Row: "m1", Col: DoneFailed, Fields: map[string]string{
					FieldRoute: "pro-a", "ok": "no",
					FieldProviderTake + "1": ProviderTake{Route: "flash-a", Error: "server_error"}.String(),
				}},
			),
			want: []RouteStat{
				{Route: flash, Attempts: 2, OK: 1, Failed: 1, Provider: 1, MeanWall: "2m0s"},
				{Route: pro, Attempts: 2, Failed: 2, MeanWall: "-"},
				{Route: Route{Name: "pin:q/m", Provider: "q", Model: "m"}, Pinned: true, Attempts: 1, OK: 1, MeanWall: "-"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, RouteStats(tc.routes, tc.fleet))
		})
	}
}

// The tier a card's reads are asked at is never weaker than the writer's (nova-tools#5096
// item 27), but for the interim rule (the owner, 2026-10-06 7:41 PM ET: "let pro do it"):
// a heavy read collapses to pro, so a heavy card, a frontier card (read on heavy, which
// no route serves) and a read tier raised to heavy are read on pro; a read tier set for
// the stream or the sprint raises a flash or pro card's reads and lowers none.
func TestReadTierOfIsNeverWeakerThanTheWriter(t *testing.T) {
	t.Parallel()
	card := func(brief string) *Card {
		return &Card{ID: "s1-1", Row: "s1", Fields: map[string]string{"kind": "primary", "brief": brief}}
	}
	for _, tc := range []struct {
		name, brief, sprintSet, want string
	}{
		{"a flash card", "", "", "flash"},
		{"a pro card", "tier: pro", "", "pro"},
		{"a heavy card, on pro (the interim rule)", "tier: heavy", "", "pro"},
		{"a heavy card, a flash setting does not lower it", "tier: heavy", "flash", "pro"},
		{"a heavy card, a pro setting", "tier: heavy", "pro", "pro"},
		{"a frontier card, on heavy, on pro (the interim rule)", "tier: frontier", "", "pro"},
		{"a frontier card, a lower setting does not lower it below pro", "tier: frontier", "flash", "pro"},
		{"a pro card raised by the sprint to heavy, on pro (the interim rule)", "tier: pro", "heavy", "pro"},
		{"a flash card raised by the sprint", "", "pro", "pro"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &Snapshot{Work: NewTable(Work), Merge: NewTable(Merge)}
			if tc.sprintSet != "" {
				s.Work.SetProps(map[string]string{PropReadTier: tc.sprintSet})
			}
			assert.Equal(t, tc.want, s.readTierOf(card(tc.brief)))
		})
	}
}
