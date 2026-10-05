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
var policyNumbers = []string{"deal_ahead", "friend_stall_after", "friend_stall_step", "friend_idle", "friend_read_deadline",
	"read_lease", "max_redeals", "max_read_reasks", "overload_timeouts", "overload_window", "promote_age", "promote_cards",
	"member_down_after", "friend_observed_down_after", "remind_every", "readers_window", "balance_poll_every",
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

	t.Run("every number named is a setting or names what blocks it", func(t *testing.T) {
		t.Parallel()
		var named []string
		for _, p := range config.SprintPolicies {
			named = append(named, p.Name)
		}
		for _, c := range sprint.PolicyCompiled {
			require.NotContains(t, named, c.Name, "%s is compiled and a setting both", c.Name)
			require.NotEmpty(t, c.Why, "%s is compiled and names nothing that blocks it", c.Name)
			named = append(named, c.Name)
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
			require.Equal(t, p.Default, fmt.Sprint(d), "%s: the table's default and the sprint's constant", p.Name)
			require.NoError(t, p.Check(p.Default), "%s: the default is in its own range", p.Name)
			for _, s := range []*sprint.Snapshot{nil, {}, {Policy: sprint.PolicyValues{}}} {
				assert.Equal(t, p.Default, policyRead(s, p), "%s with no row", p.Name)
			}
		}
		for _, l := range (&sprint.Snapshot{}).PolicySettings() {
			want := sprint.SourceDefault
			if !slices.ContainsFunc(config.SprintPolicies, func(p config.Policy) bool { return p.Name == l.Name }) {
				want = sprint.SourceCompiled
			}
			assert.Equal(t, want, l.Source, "%s", l)
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

	t.Run("the store's value comes before nova-sprint set's property", func(t *testing.T) {
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
		for _, p := range config.SprintPolicies {
			for _, v := range []string{"lots", "-1", "100000h", "0"} {
				if v == "0" && p.Min == "0" {
					continue
				}
				err := p.Check(v)
				require.Error(t, err, "%s=%s", p.Name, v)
				assert.Contains(t, err.Error(), p.Name+" wants "+p.Range(), "%s=%s", p.Name, v)
			}
			require.NoError(t, p.Check(p.Min), "%s=%s", p.Name, p.Min)
			require.NoError(t, p.Check(p.Max), "%s=%s", p.Name, p.Max)
			require.NoError(t, p.Check(""), "%s empty is its default", p.Name)
		}
	})

	t.Run("the tick reads the value each pass, with no rebuild", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		m := store.NewMem()
		var ids int
		st := &store.Store{B: m, Names: sprint.Names{Prefix: "p-"}, Actor: "coordinator",
			Now: func() time.Time { return now }, NewID: func() string { ids++; return strconv.Itoa(ids) }, Sleep: func(time.Duration) {}}
		require.NoError(t, st.Init(ctx))
		require.NoError(t, m.SetCoordinator(ctx, st.Actor))
		// a part of a tick and a dealing step both plan with the snapshot's value
		read := func(routes bool) (int, time.Duration) {
			var n int
			var d time.Duration
			_, err := st.Run(ctx, store.Step{Verb: "probe", Load: []string{sprint.Work}, Routes: routes, Plan: func(s *sprint.Snapshot) sprint.Plan {
				n, d = s.PolicyCount(sprint.PolicyDealAhead), s.PolicyDuration(sprint.PolicyFriendStallAfter)
				return sprint.Plan{}
			}})
			require.NoError(t, err)
			return n, d
		}
		set := func(v string) {
			res, err := st.Run(ctx, store.SetStep(sprint.SetReq{FriendStallAfter: v, Who: st.Actor}))
			require.NoError(t, err)
			require.Empty(t, res.Refused, "set --friend-stall-after %s", v)
		}
		for _, routes := range []bool{false, true} {
			n, d := read(routes)
			assert.Equal(t, sprint.DealAhead, n, "routes=%v: nothing set is the default", routes)
			assert.Equal(t, sprint.FriendStallAfterDefault, d)
		}
		set("45m")
		for _, routes := range []bool{false, true} {
			_, d := read(routes)
			assert.Equal(t, 45*time.Minute, d, "routes=%v: the next pass reads it", routes)
		}
		set(sprint.ReadTierDefault)
		_, d := read(false)
		assert.Equal(t, sprint.FriendStallAfterDefault, d, "taken off is the default again")
	})
}
