package sprint_test

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// policyNumbers is every policy number the card names (the brief's START), by its setting.
// FriendObservedDownAfter, which the card names, left the code before this card
// (presence.go): a friend is up on her session's evidence, a wake ping answered within
// FriendPongWindow or a card finished within FriendFinishWindow, and those two are its
// settings.
var policyNumbers = []string{"deal_ahead", "friend_stall_after", "friend_stall_step", "friend_idle", "friend_read_deadline",
	"read_lease", "max_redeals", "max_read_reasks", "overload_timeouts", "overload_window", "promote_age", "promote_cards",
	"member_down_after", "friend_pong_window", "friend_finish_window", "remind_every", "readers_window", "balance_poll_every",
	"critical_behind", "flash_gate_bound", "rollback_window", "loop_silence"}

// policyRead is the number's effective value on the snapshot, as its setting is written.
func policyRead(s *sprint.Snapshot, p config.Policy) string {
	if p.Type == config.PolicyCount {
		return strconv.Itoa(s.PolicyCount(p.Name))
	}
	return s.PolicyDuration(p.Name).String()
}

// canonical is a value as policyRead writes it.
func canonical(t *testing.T, p config.Policy, v string) string {
	t.Helper()
	if p.Type == config.PolicyCount {
		n, err := p.Count(v)
		require.NoError(t, err)
		return strconv.Itoa(n)
	}
	d, err := p.Duration(v)
	require.NoError(t, err)
	return d.String()
}

func TestEveryPolicyNumberIsASettingWithItsDefault(t *testing.T) {
	t.Parallel()

	t.Run("every number named is a setting", func(t *testing.T) {
		t.Parallel()
		var named []string
		for _, p := range config.SprintPolicies {
			named = append(named, p.Name)
		}
		slices.Sort(named)
		want := slices.Clone(policyNumbers)
		slices.Sort(want)
		require.Equal(t, want, named)
		require.Len(t, sprint.PolicyDefaults, len(config.SprintPolicies), "one default per setting")
	})

	t.Run("each reads its default, today's constant, with no row", func(t *testing.T) {
		t.Parallel()
		for _, p := range config.SprintPolicies {
			d, ok := sprint.PolicyDefaults[p.Name]
			require.True(t, ok, "%s has no default", p.Name)
			require.Equal(t, p.Default, fmt.Sprint(d), "%s: nova-config's default and the sprint's constant", p.Name)
			require.NoError(t, p.Check(p.Default), "%s: the default is in its own range", p.Name)
			for _, s := range []*sprint.Snapshot{nil, {}, {Policy: sprint.PolicyValues{}}} {
				assert.Equal(t, p.Default, policyRead(s, p), "%s with no row", p.Name)
			}
		}
		all := (&sprint.Snapshot{}).PolicySettings()
		require.Len(t, all, len(policyNumbers))
		for _, l := range all {
			assert.Equal(t, sprint.SourceDefault, l.Source, "%s", l)
		}
	})

	t.Run("each reads the row's value when set, and its default when the row is out of range", func(t *testing.T) {
		t.Parallel()
		for _, p := range config.SprintPolicies {
			s := &sprint.Snapshot{Policy: sprint.PolicyValues{p.Name: p.Max}}
			assert.Equal(t, canonical(t, p, p.Max), policyRead(s, p), "%s set to %s", p.Name, p.Max)
			assert.NotEqual(t, p.Default, policyRead(s, p), "%s: the test's value is not its default", p.Name)
			bad := &sprint.Snapshot{Policy: sprint.PolicyValues{p.Name: "-1"}}
			assert.Equal(t, p.Default, policyRead(bad, p), "%s applied out of range is its default", p.Name)
		}
		for _, l := range (&sprint.Snapshot{Policy: sprint.PolicyValues{"deal_ahead": "3"}}).PolicySettings() {
			if l.Name == "deal_ahead" {
				assert.Equal(t, "3", l.Value)
				assert.Equal(t, sprint.SourceConfig, l.Source)
				assert.Equal(t, "2", l.Default)
			}
		}
	})

	t.Run("nova-config's row comes before nova-sprint set's property", func(t *testing.T) {
		t.Parallel()
		work := sprint.NewTable(sprint.Work)
		work.SetProps(map[string]string{sprint.PropFriendIdle: "40m"})
		s := &sprint.Snapshot{Work: work}
		assert.Equal(t, 40*time.Minute, s.FriendIdleAfter())
		s.Policy = sprint.PolicyValues{"friend_idle": "1h"}
		assert.Equal(t, time.Hour, s.FriendIdleAfter())
	})

	t.Run("a bad value is refused naming the setting and its range", func(t *testing.T) {
		t.Parallel()
		k, ok := config.Lookup(config.KindSprint)
		require.True(t, ok)
		for _, p := range config.SprintPolicies {
			for _, v := range []string{"lots", "-1", "100000h", "0"} {
				if v == "0" && p.Min == "0" {
					continue
				}
				err := p.Check(v)
				require.Error(t, err, "%s=%s", p.Name, v)
				assert.Contains(t, err.Error(), p.Name+" wants "+p.Range(), "%s=%s", p.Name, v)
				err = k.Check(config.Row{Name: config.KindSprint, Fields: map[string]string{p.Name: v}})
				require.Error(t, err, "the sprint row refuses %s=%s", p.Name, v)
				assert.Contains(t, err.Error(), p.Name+" wants "+p.Range())
			}
			require.NoError(t, k.Check(config.Row{Name: config.KindSprint, Fields: map[string]string{p.Name: p.Max}}), "%s=%s", p.Name, p.Max)
			require.NoError(t, k.Check(config.Row{Name: config.KindSprint, Fields: map[string]string{p.Name: ""}}), "%s empty is its default", p.Name)
			require.True(t, slices.ContainsFunc(k.Fields, func(f config.Field) bool { return f.Name == p.Name }), "the sprint row has a field %s", p.Name)
		}
	})

	t.Run("critical_behind decides which card is critical and starts on pro", func(t *testing.T) {
		t.Parallel()
		c := &sprint.Card{ID: "c", Fields: map[string]string{sprint.FieldBehind: "5"}}
		_, ceiling := sprint.CardTiers(nil, c)
		assert.Equal(t, "flash", ceiling, "five behind is under the default ten")
		assert.False(t, (*sprint.Snapshot)(nil).IsCritical(c))
		s := &sprint.Snapshot{Policy: sprint.PolicyValues{"critical_behind": "5"}}
		assert.True(t, s.IsCritical(c), "critical_behind 5: five behind is critical")
		level, source := sprint.CardPriorityIn(s, c)
		assert.Equal(t, sprint.PriorityCritical, level)
		assert.Equal(t, "computed", source)
		_, ceiling = sprint.CardTiers(s, c)
		assert.Equal(t, "pro", ceiling, "a critical card's ceiling is pro")
		g := sprint.Inbox(sprint.InboxReq{Now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC), Policy: s.Policy, Weights: map[string]int{"c": 5},
			Open: []sprint.Open{{Key: sprint.OpenKey("n1", "c"), Note: sprint.Note{ID: "n1", Type: sprint.NWorkFailed, Stream: "s1"}}}})
		require.NotEmpty(t, g)
		assert.Equal(t, 5, g[0].Behind)
		assert.True(t, g[0].Critical, "the inbox marks the group by the setting: %+v", g[0])
		assert.False(t, sprint.Inbox(sprint.InboxReq{Weights: map[string]int{"c": 5},
			Open: []sprint.Open{{Key: sprint.OpenKey("n1", "c"), Note: sprint.Note{ID: "n1", Type: sprint.NWorkFailed, Stream: "s1"}}}})[0].Critical, "and not by the default")
	})

	t.Run("friend_pong_window and friend_finish_window are how long her evidence keeps her up", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		m := store.NewMem()
		now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		st := &store.Store{B: m, Names: sprint.Names{Prefix: "p-"}, Actor: "coordinator",
			Now: func() time.Time { return now }, NewID: func() string { return "1" }, Sleep: func(time.Duration) {}}
		require.NoError(t, st.Init(ctx))
		require.NoError(t, m.SetCoordinator(ctx, st.Actor))
		_, _, _, err := st.SyncFriends(ctx, []store.FriendSpec{{Name: "amy", Width: 2}})
		require.NoError(t, err)
		_, status, _, err := st.FriendHealth(ctx, "amy", "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: now, Generation: sprint.FirstSeatGeneration}, "")
		require.NoError(t, err)
		require.Equal(t, sprint.Up, status)
		statusAt := func(at time.Time) string {
			rows, err := st.FriendRows(ctx, at)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			return rows[0].Status
		}
		later := now.Add(15 * time.Minute)
		assert.Equal(t, sprint.Down, statusAt(later), "fifteen minutes is past the default pong window, ten")
		m.SetPolicy("friend_pong_window", "20m")
		assert.Equal(t, sprint.Up, statusAt(later), "with the setting at twenty minutes she is up")
		assert.Equal(t, sprint.Down, statusAt(now.Add(20*time.Minute)), "and down at twenty")
		seats, err := st.FriendSeats(ctx, later)
		require.NoError(t, err)
		require.Len(t, seats, 1)
		assert.Equal(t, sprint.Up, seats[0].Status, "the deal's seats read it too")

		// a finish keeps her up for friend_finish_window
		fin := sprint.FriendPresence{Finished: now}
		at := now.Add(45 * time.Minute)
		assert.Equal(t, sprint.Down, sprint.FriendStatus(fin, at), "forty-five minutes is past the default finish window, thirty")
		fin.Windows = (&sprint.Snapshot{Policy: sprint.PolicyValues{"friend_finish_window": "1h"}}).PresenceWindows()
		assert.Equal(t, sprint.Up, sprint.FriendStatus(fin, at), "with the setting at an hour she is up")
		_, why := sprint.FriendEvidence(sprint.FriendPresence{Windows: fin.Windows}, at)
		assert.Contains(t, why, "no card finished within 1h0m0s", "her row says the window it judged by")
	})

	t.Run("the tick reads the store's value each pass, with no rebuild", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		m := store.NewMem()
		now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		st := &store.Store{B: m, Names: sprint.Names{Prefix: "p-"}, Actor: "coordinator",
			Now: func() time.Time { return now }, NewID: func() string { return "1" }, Sleep: func(time.Duration) {}}
		require.NoError(t, st.Init(ctx))
		// a part of a tick (a route cache, no routes of its own) and a dealing step both plan with it
		read := func(mode string) (int, time.Duration) {
			var n int
			var d time.Duration
			step := store.Step{Verb: "probe", Load: []string{sprint.Work}, Routes: mode == "routes", Plan: func(s *sprint.Snapshot) sprint.Plan {
				n, d = s.PolicyCount(sprint.PolicyDealAhead), s.PolicyDuration(sprint.PolicyRemindEvery)
				return sprint.Plan{}
			}}
			if mode == "tick cache" {
				step.RouteCache = &store.RouteCache{}
			}
			_, err := st.Run(ctx, step)
			require.NoError(t, err)
			return n, d
		}
		for _, mode := range []string{"routes", "tick cache", "standalone"} {
			n, d := read(mode)
			assert.Equal(t, sprint.DealAhead, n, "mode=%s: no row is the default", mode)
			assert.Equal(t, sprint.RemindEvery, d)
		}
		m.SetPolicy("deal_ahead", "4")
		m.SetPolicy("remind_every", "1h")
		for _, mode := range []string{"routes", "tick cache", "standalone"} {
			n, d := read(mode)
			assert.Equal(t, 4, n, "mode=%s: the next pass reads the row", mode)
			assert.Equal(t, time.Hour, d)
		}
		m.SetPolicy("deal_ahead", "")
		n, _ := read("standalone")
		assert.Equal(t, sprint.DealAhead, n, "a row taken off is the default again")
		got, err := st.Policy(ctx)
		require.NoError(t, err)
		assert.Equal(t, sprint.PolicyValues{"remind_every": "1h"}, got)
	})
}

// The member's policy changes the tick's presence decision, not just seat check output.
func TestMemberDownSettingControlsTickPresence(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, tc := range []struct {
		name, value, want string
		age               time.Duration
	}{
		{"default last window", "", sprint.Up, 45 * time.Second},
		{"default expired", "", sprint.Down, 46 * time.Second},
		{"longer window", "2m", sprint.Up, time.Minute},
		{"shorter window", "15s", sprint.Down, 16 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &sprint.Snapshot{Now: now, Policy: sprint.PolicyValues{"member_down_after": tc.value}}
			ctl := &sprint.Card{Fields: map[string]string{}}
			beat := sprint.Beat{At: now.Add(-tc.age)}
			assert.Equal(t, tc.want, s.PolicyMemberStatus(ctl, beat, now))
			ctl.Fields["held"] = "true"
			assert.Equal(t, sprint.Held, s.PolicyMemberStatus(ctl, beat, now))
		})
	}
}
