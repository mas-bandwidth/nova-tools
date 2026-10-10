package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// The dashboard's rows say how many of their cards are reads, and the levels of the rest
// (sprint.RowCardCounts): a friend working a read and two work cards, a blocker and a
// critical, has working 3 of which reads_working 1, blocker_working 1 and critical_working 1,
// summing to working; m1 works nothing; read_cards counts the epoch's read cards.
func TestTheDashboardRowsSayHowManyAreReads(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	home, prev := t.TempDir(), ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == "HOME" {
			return home
		}
		return prev(k)
	}
	cfg := config.NewMem()
	ta.a.friends = func(ctx context.Context, _ string) ([]config.Row, error) {
		return cfg.List(ctx, config.KindFriend)
	}
	_, err := cfg.Insert(context.Background(), config.KindFriend, config.Row{Name: "fred",
		Fields: map[string]string{"slots": "4", "tiers": "flash", "width": "4", "roles": "builder,reader"}}, "t")
	require.NoError(t, err)
	ta.a.tip = tipIs(t, landHead)
	ta.ok("init --members m1:8")
	ta.ok("set --read-cards on")
	root := t.TempDir()
	ta.ok("friend sync --root " + root) // roster only: fred is down, so m1 is dealt every card
	dir := t.TempDir()
	for id, head := range map[string]string{"s1-1": "", "s1-2": "PRIORITY: blocker\n", "s1-3": "PRIORITY: critical\n"} {
		brief := passingBrief(id + ": a card (s1) tier: flash\n" + head + "REPO: mas-bandwidth/nova-tools\n\nHOW THIS CARD IS JUDGED\nit works\n")
		require.NoError(t, os.WriteFile(filepath.Join(dir, id+".md"), []byte(brief), 0o644))
	}
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --report done --head " + landHead)
	ta.ok("fleet down m1") // its two dealt ahead go back, to be dealt to fred
	ta.beatUp("fred")
	for range 3 { // the withdrawn two dealt to fred, and the read of s1-1, each taken
		ta.ok("tick")
		ta.ok("take --as " + sprint.FriendRow("fred") + " --limit 10")
	}
	ta.ok("stop --reason test --until 90m") // a STOPPED tick deals nothing and counts the record
	ta.ok("tick")                           // a take moves the fleet table alone: the record follows it
	var v whereView
	ta.json("where", &v)
	fred := v.Tables[sprint.Friends]["fred"]
	require.NotNil(t, fred)
	require.Equal(t, "3", fred["working"])
	for k, want := range map[string]string{"blocker_working": "1", "critical_working": "1", "high_working": "0", "reads_working": "1", "normal_working": "0", "low_working": "0", "reads_ready": "0"} {
		require.Equal(t, want, fred[k], k)
	}
	for _, k := range sprint.RowCardFields {
		require.Equal(t, "0", v.Tables[sprint.Fleet]["m1"][k], "m1 %s", k)
	}
	require.Equal(t, sprint.ReadCardCounts{Working: 1}, v.ReadCards)
}

// withoutRowCards is a row of tables without its card counts (sprint.RowCardFields), for a
// test of its other cells.
func withoutRowCards(row map[string]any) map[string]any {
	for _, k := range sprint.RowCardFields {
		delete(row, k)
	}
	return row
}
