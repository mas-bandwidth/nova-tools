package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// The night of 2026-10-09 (UTC 2026-10-10), as the run loop's balance lines read it:
// openrouter's balance $356.84 at 00:54:15Z, $230.67 at 01:44:29Z and $190.77 at 01:54:32Z,
// when the poll rested every openrouter route as "not over one hour of its spend ($234.82 an
// hour)": the two-read rate of the last ten minutes, frozen there for as long as the rest
// held. The balance then read $148.54 from 02:24:58Z on, flat: a resting provider spends
// nothing, and the frozen $234.82 never let the rest end.
var (
	nightRest   = time.Date(2026, 10, 10, 1, 54, 32, 0, time.UTC)
	nightRoutes = []Route{
		{Name: "flash-glm53-openrouter", Tier: "flash", Provider: "openrouter", Model: "openrouter/z-ai/glm-5.3-flash", Enabled: true},
		{Name: "pro-grok47-openrouter", Tier: "pro", Provider: "openrouter", Model: "openrouter/x-ai/grok-4.7", Enabled: true},
		{Name: "flash-deepseek41-direct", Tier: "pro", Provider: "deepseek", Model: "deepseek/deepseek-flash", Enabled: true},
	}
)

// nightWork is a work table whose primaries carry openrouter's records of the hour up to
// 01:54:32Z, $166.07 in all (the provider's own count fell $356.84 to $190.77 over it), and
// records that are not that hour's or not openrouter's: one before the hour, one after now,
// one with only a predicted price, one of another provider, and a copy of the hour's last
// record on a second card under the same key.
func nightWork(t *testing.T, now time.Time) *Table {
	t.Helper()
	w := NewTable(Work)
	w.SetRows([]string{"s1"})
	at := func(d time.Duration) string { return stamp(now.Add(-d)) }
	usd := func(v string) cardcost.Usage {
		return cardcost.ParseUsage("input=1000 actual_usd=" + v + " actual_by=harness")
	}
	last := Consumer{Kind: "work", Card: "s1-2.w1", Key: "s1-2.w1#g1", Route: "pro-grok47-openrouter", End: "ok", At: at(4 * time.Minute), Usage: usd("39.90")}
	a := &Card{ID: "s1-1", Row: "s1", Col: Working, Fields: map[string]string{}}
	book(a,
		Consumer{Kind: "work", Card: "s1-1.w1", Key: "s1-1.w1#g1", Route: "flash-glm53-openrouter", End: "ok", At: at(56 * time.Minute), Usage: usd("0.57")},
		Consumer{Kind: "work", Card: "s1-1.w1", Key: "s1-1.w1#g2", Route: "flash-glm53-openrouter", End: "no result", At: at(44 * time.Minute), Usage: usd("25.65")},
		Consumer{Kind: "read", Card: "s1-1.r1", Key: "s1-1.r1#v", Model: "openrouter/x-ai/grok-4.7", End: "ok", At: at(34 * time.Minute), Usage: usd("29.70")},
		Consumer{Kind: "work", Card: "s1-1.w2", Key: "s1-1.w2#g1", Route: "pro-grok47-openrouter", End: "failed", At: at(24 * time.Minute), Usage: usd("33.30")},
		Consumer{Kind: "work", Card: "s1-1.w2", Key: "s1-1.w2#g2", Route: "pro-grok47-openrouter", End: "ok", At: at(14 * time.Minute), Usage: usd("36.95")},
		// not the hour's, or not openrouter's, or no actual_usd
		Consumer{Kind: "work", Card: "s1-1.w0", Key: "s1-1.w0#g1", Route: "flash-glm53-openrouter", End: "ok", At: at(61 * time.Minute), Usage: usd("50")},
		Consumer{Kind: "work", Card: "s1-1.w3", Key: "s1-1.w3#g1", Route: "flash-glm53-openrouter", End: "ok", At: stamp(now.Add(time.Minute)), Usage: usd("7")},
		Consumer{Kind: "read", Card: "s1-1.r2", Key: "s1-1.r2#v", Model: "openrouter/z-ai/glm-5.3-flash", End: "ok", At: at(10 * time.Minute), Usage: cardcost.ParseUsage("input=1000 predicted_usd=10")},
		Consumer{Kind: "work", Card: "s1-1.w4", Key: "s1-1.w4#g1", Route: "flash-deepseek41-direct", End: "ok", At: at(10 * time.Minute), Usage: usd("3")},
		last,
	)
	w.Put(a)
	b := &Card{ID: "s1-2", Row: "s1", Col: Working, Fields: map[string]string{}}
	book(b, last) // the same record under the same key: counted once
	w.Put(b)
	return w
}

// The spend an hour is the sprint's own records of the last hour, each counted once: on the
// night's records it is $166.07, the provider's own count of that hour, where the two-read
// rate the poll kept said $234.82 (the last ten minutes, $39.33 used, scaled up six times).
func TestSpendLastHourIsTheHoursRecordsCountedOnce(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: nightRest, Work: nightWork(t, nightRest), Routes: nightRoutes}
	assert.InDelta(t, 166.07, SpendLastHour(s, "openrouter"), 1e-9, "$0.57 + $25.65 + $29.70 + $33.30 + $36.95 + $39.90")
	assert.InDelta(t, 3.0, SpendLastHour(s, "deepseek"), 1e-9)
	assert.InDelta(t, 0.0, SpendLastHour(s, "inception"), 1e-9)
	assert.InDelta(t, 0.0, SpendLastHour(&Snapshot{Now: nightRest, Routes: nightRoutes}, "openrouter"), 1e-9, "no work table: nothing measured")
	later := &Snapshot{Now: nightRest.Add(2 * time.Hour), Work: s.Work, Routes: nightRoutes}
	assert.InDelta(t, 0.0, SpendLastHour(later, "openrouter"), 1e-9, "two hours on, with nothing spent since, the hour holds nothing")
}

// The night's balance poll at 03:45:30Z on the code that measures the hour: the rest the
// poll wrote at 01:54:32Z is a retired rule's and holds no route; the poll writes the
// balance with the hour's measured spend ($0.00: the routes rested, nothing ended), writes
// no rest, and the tick raises no judgment: $148.54 is over an hour of $0.00. Every
// openrouter route serves.
func TestTheNightsBalanceRestLiftsOnTheNextPoll(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 3, 45, 30, 0, time.UTC)
	f := NewTable(Fleet)
	f.SetProps(map[string]string{
		PropProviderBalance("openrouter"): "148.54 2026-10-10T03:35:23Z 234.81083562476707 1101.46",
		PropProviderRest("openrouter"):    "2026-10-10T01:54:32Z open - balance low on funds: provider openrouter balance $190.77 at 2026-10-10T01:54:32Z is not over one hour of its spend ($234.82 an hour)",
	})
	s := &Snapshot{Now: now, Fleet: f, Work: nightWork(t, nightRest), Routes: nightRoutes, Coordinator: Coordinator}
	for name, r := range RouteRests(nightRoutes, f) {
		assert.False(t, r.Resting(now), "%s: a balance poll's rest of before holds no route", name)
	}

	p := Balance(s, BalanceReq{Reads: []ProviderRead{{Provider: "openrouter", Known: true, Balance: 148.54, HasUsed: true, Used: 1101.46}}, Who: MachineActor})
	require.Empty(t, p.Refused)
	require.Len(t, p.Props, 1, "the balance only: the poll writes no rest")
	assert.Equal(t, PropProviderBalance("openrouter"), p.Props[0].Name)
	assert.Equal(t, "148.54 2026-10-10T03:45:30Z 0 1101.46", p.Props[0].Value, "the hour's measured spend, not the $234.82 kept from 01:54:32Z")
	assert.Empty(t, p.Notes)

	f.SetProp(p.Props[0].Name, p.Props[0].Value)
	ws, _ := s.withRests()
	conds, stop := providerConds(ws)
	assert.Empty(t, conds, "$148.54 is over an hour of $0.00: nothing to judge")
	assert.Empty(t, stop)
	for _, r := range nightRoutes {
		_, resting := ws.resting(r.Name)
		assert.False(t, resting, "%s serves", r.Name)
	}
}

// A balance not over one hour of the spend is the coordinator's judgment, never a rest (the
// owner, 2026-10-10: "it should raise it to you as a thing to do, but not do it
// automatically."): at 02:04:43Z the balance read $162.20 against $201.07 spent over the
// last hour; the poll writes the balance and no rest, every route still serves, and the tick
// opens ONE judgment, a provider is low on funds, with the balance, the spend, the hours
// left and the verbs (routes rest, wait, ack). It never stops the sprint.
func TestALowBalanceIsAJudgmentNeverARest(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 2, 4, 43, 0, time.UTC)
	work := nightWork(t, nightRest)
	extra := &Card{ID: "s1-3", Row: "s1", Col: Working, Fields: map[string]string{}}
	book(extra, Consumer{Kind: "work", Card: "s1-3.w1", Key: "s1-3.w1#g1", Route: "flash-glm53-openrouter", End: "ok", At: stamp(now.Add(-time.Minute)), Usage: cardcost.ParseUsage("input=1 actual_usd=28.57 actual_by=harness")})
	work.Put(extra)
	f := NewTable(Fleet)
	s := &Snapshot{Now: now, Fleet: f, Work: work, Routes: nightRoutes, Coordinator: Coordinator}
	// the hour up to 02:04:43Z: the night's hour less its first record ($0.57, now over an
	// hour old), with the $7.00 that ended at 01:55:32Z and $28.57 more
	require.InDelta(t, 201.07, SpendLastHour(s, "openrouter"), 1e-9)

	p := Balance(s, BalanceReq{Reads: []ProviderRead{{Provider: "openrouter", Known: true, Balance: 162.20}}, Who: MachineActor})
	require.Len(t, p.Props, 1, "the balance only: a low balance rests nothing")
	assert.Empty(t, p.Notes)
	f.SetProp(p.Props[0].Name, p.Props[0].Value)
	assert.Empty(t, RestsDue(s), "the tick rests nothing for a balance either")
	assert.Empty(t, RouteRests(nightRoutes, f))

	ws, _ := s.withRests()
	conds, stop := providerConds(ws)
	assert.Empty(t, stop, "a low balance never stops the sprint")
	require.Len(t, conds, 1, "one judgment of the provider")
	c := conds[0]
	assert.Equal(t, NProviderLow, c.typ)
	assert.Equal(t, ProviderSubject("openrouter"), c.stream)
	assert.Equal(t, []string{"routes rest openrouter", "wait", "ack"}, c.decisions)
	for _, want := range []string{
		"balance $162.20 at 2026-10-10T02:04:43Z",
		"spent $201.07 over the last hour",
		"about 0.8 hours left",
		"its routes flash-glm53-openrouter, pro-grok47-openrouter STILL SERVE",
		"nova-sprint routes rest openrouter --reason",
		"nova-sprint wait <note> --until",
	} {
		assert.Contains(t, c.what, want)
	}
}

// A take that ended with no result is the model's output on that card: however many of
// them a route's window holds, the tick rests nothing (flash-deepseek41-direct, 2026-10-10
// 03:38:45Z, rested twenty minutes for "no-result: its children ended with no result").
// Three 429s rest the route; one refusal for credit rests the provider.
func TestANoResultNeverRestsAndA429Does(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 3, 38, 45, 0, time.UTC)
	ended := func(id, route string, m int, err string) *Card {
		take := ProviderTake{Route: route, Finished: stamp(now.Add(time.Duration(-m) * time.Minute)), Taken: stamp(now.Add(time.Duration(-m-5) * time.Minute)), Error: err}
		return &Card{ID: id, Row: "m1", Col: Withdrawn, Fields: map[string]string{FieldProviderTake + "1": take.String()}}
	}
	for _, tc := range []struct {
		name  string
		cards []*Card
		want  []RouteRest
	}{
		{"five no results on the direct route", []*Card{
			ended("c1", "flash-deepseek41-direct", 9, "no result: its children ended with no result"),
			ended("c2", "flash-deepseek41-direct", 8, "no result: no RESULT.md shape"),
			ended("c3", "flash-deepseek41-direct", 7, "no result: the child exited 0 with no result"),
			ended("c4", "flash-deepseek41-direct", 6, "no result: no RESULT.md shape"),
			ended("c5", "flash-deepseek41-direct", 5, "no result: no RESULT.md shape"),
		}, nil},
		{"three 429s on the direct route", []*Card{
			ended("c1", "flash-deepseek41-direct", 9, "provider: class=rate-limited status=429 msg=Rate limit reached"),
			ended("c2", "flash-deepseek41-direct", 8, "provider: class=rate-limited status=429 msg=Rate limit reached"),
			ended("c3", "flash-deepseek41-direct", 7, "provider: class=provider-5xx status=502 msg=bad gateway"),
		}, []RouteRest{{Route: "flash-deepseek41-direct", At: now, Until: now.Add(RouteRestFor), Cards: []string{"c1", "c2", "c3"}, Cause: RestProvider}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := NewTable(Fleet)
			f.SetRows([]string{"m1"})
			for _, c := range tc.cards {
				f.Put(c)
			}
			s := &Snapshot{Now: now, Fleet: f, Routes: nightRoutes, Coordinator: Coordinator}
			due := RestsDue(s)
			assert.Equal(t, tc.want, due)
			if len(due) == 0 {
				return
			}
			var p Plan
			restWrites(&p, s, due, MachineActor)
			require.Len(t, p.Notes, 1)
			assert.Equal(t, NRouteRested, p.Notes[0].Type)
			assert.Contains(t, p.Notes[0].What, "its provider failed 3 of its last 10 ended takes or fewer (a rate limit, a 5xx or a timeout: c1, c2, c3)")
		})
	}
}

// routes rest and routes wake (route_coordinator.go): the coordinator rests a provider or a
// route, until a time or until woken, and wakes any rest (a refused take's too) with a reason
// that is not a payment; refused with no reason, a name that is neither, nothing resting, or
// a route whose provider's rest holds it.
func TestTheCoordinatorRestsAndWakesRoutes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC)
	snap := func(props map[string]string) *Snapshot {
		f := NewTable(Fleet)
		f.SetProps(props)
		return &Snapshot{Now: now, Fleet: f, Routes: nightRoutes, Coordinator: Coordinator}
	}
	apply := func(s *Snapshot, p Plan) *Snapshot {
		for _, w := range p.Props {
			s.Fleet.SetProp(w.Name, w.Value)
		}
		return &Snapshot{Now: now.Add(time.Minute), Fleet: s.Fleet, Routes: s.Routes, Coordinator: s.Coordinator}
	}
	resting := func(s *Snapshot) []string {
		var out []string
		for name, r := range RouteRests(s.Routes, s.Fleet) {
			if r.Resting(s.Now) {
				out = append(out, name)
			}
		}
		return out
	}

	// a provider, until woken
	s := snap(nil)
	p := RestRoutes(s, RouteRestReq{Target: "openrouter", Reason: "balance $40", Who: "rowan"})
	require.Empty(t, p.Refused)
	require.Len(t, p.Props, 1)
	assert.Equal(t, PropProviderRest("openrouter"), p.Props[0].Name)
	assert.True(t, p.Props[0].WasAbsent)
	require.Len(t, p.Notes, 1)
	assert.Equal(t, NRouteRestedByCoordinator, p.Notes[0].Type)
	s = apply(s, p)
	assert.ElementsMatch(t, []string{"flash-glm53-openrouter", "pro-grok47-openrouter"}, resting(s))
	rest := ProviderRests(s.Fleet)["openrouter"]
	assert.Equal(t, RestCoordinator, rest.Cause)
	assert.True(t, rest.Open())
	assert.Equal(t, "rested by rowan: balance $40", rest.Why)
	ws, _ := s.withRests()
	conds, stop := providerConds(ws)
	assert.Empty(t, conds, "the coordinator's own rest is no judgment")
	assert.Empty(t, stop, "nor a stop: it is not out of credit")

	p = WakeRoutes(s, RouteWakeReq{Target: "openrouter", Reason: "the balance holds", Who: "rowan"})
	require.Empty(t, p.Refused)
	require.Len(t, p.Notes, 1)
	assert.Equal(t, NRouteWoken, p.Notes[0].Type)
	s = apply(s, p)
	assert.Empty(t, resting(s))
	assert.Contains(t, ProviderRests(s.Fleet)["openrouter"].Why, "; ended: woken by rowan: the balance holds")

	// one route, for a time
	s = snap(nil)
	p = RestRoutes(s, RouteRestReq{Target: "flash-deepseek41-direct", Reason: "slow tonight", Until: now.Add(time.Hour), Who: "rowan"})
	require.Empty(t, p.Refused)
	assert.Equal(t, PropRule3Rest("deepseek"), p.Props[0].Name)
	s = apply(s, p)
	assert.Equal(t, []string{"flash-deepseek41-direct"}, resting(s))
	s = apply(s, WakeRoutes(s, RouteWakeReq{Target: "flash-deepseek41-direct", Reason: "fine again", Who: "rowan"}))
	assert.Empty(t, resting(s))

	// a refused take's credit rest wakes without a payment, and a route under it does not
	refused := "2026-10-10T03:00:00Z open c9 out-of-credit out of credit: provider openrouter refused card c9"
	s = snap(map[string]string{PropProviderRest("openrouter"): refused})
	p = WakeRoutes(s, RouteWakeReq{Target: "pro-grok47-openrouter", Reason: "try", Who: "rowan"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "rests with its provider openrouter")
	p = WakeRoutes(s, RouteWakeReq{Target: "openrouter", Reason: "the 402 was one request's estimate", Who: "rowan"})
	require.Empty(t, p.Refused)
	s = apply(s, p)
	assert.Empty(t, resting(s))

	// refusals
	for _, tc := range []struct {
		name string
		plan Plan
		word string
	}{
		{"rest with no reason", RestRoutes(snap(nil), RouteRestReq{Target: "openrouter", Who: "rowan"}), "wants --reason"},
		{"rest a name that is neither", RestRoutes(snap(nil), RouteRestReq{Target: "nobody", Reason: "r", Who: "rowan"}), "names no route and no provider"},
		{"rest until a time gone", RestRoutes(snap(nil), RouteRestReq{Target: "openrouter", Reason: "r", Until: now.Add(-time.Minute), Who: "rowan"}), "is not after now"},
		{"wake with no reason", WakeRoutes(snap(nil), RouteWakeReq{Target: "openrouter", Who: "rowan"}), "wants --reason"},
		{"wake what does not rest", WakeRoutes(snap(nil), RouteWakeReq{Target: "openrouter", Reason: "r", Who: "rowan"}), "does not rest"},
	} {
		require.Len(t, tc.plan.Refused, 1, tc.name)
		assert.True(t, strings.Contains(tc.plan.Refused[0].Why, tc.word), "%s: %s", tc.name, tc.plan.Refused[0].Why)
		assert.Empty(t, tc.plan.Props, tc.name)
	}
}
