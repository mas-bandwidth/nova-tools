package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// A read at any tier is dealt to any unit with room at or above that tier, a
// friend or a paid reader, by the one ask (docs/SPEC-SPRINT.md, a read asked of
// any unit with room at or above the read tier). Amy is flash and one-shot:
// she takes the flash read, and the pro read, which she is below, goes to a
// paid reader in the same tick. Her working cell is that read. Her LAND and
// the reader's ok each close their own card.
func TestAnyUnitWithRoomAtOrAboveTheReadTierServesARead(t *testing.T) {
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
	_, err := cfg.Insert(context.Background(), config.KindFriend, config.Row{
		Name: "amy", Fields: map[string]string{"slots": "2", "tiers": "flash", "mode": "one-shot", "width": "1"},
	}, "t")
	require.NoError(t, err)
	ta.a.tip = tipIs(t, landHead)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	root := t.TempDir()
	ta.ok("friend sync --root " + root) // roster only: she stays down, so the flash card is not dealt to her as work

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s1-1.md"), []byte(passingBrief("s1-1: the flash card (s1) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nAS A READ\nread the flash card\n")), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s1-2.md"), []byte(passingBrief("s1-2: the pro card (s1) tier: pro\nREPO: mas-bandwidth/nova-tools\n\nAS A READ\nread the pro card\n")), 0o644))
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 2")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1 --report done")
	ta.beatUp("amy")
	ta.ok("tick")

	var w whereView
	ta.json("where", &w)
	require.Equal(t, "1", cellText(w.Tables[sprint.Friends]["amy"]["working"]), "the friends table counts her read")
	asked := 0
	for _, row := range w.Tables[sprint.Readers] {
		n, err := strconv.Atoi(cellText(row[sprint.Asked]))
		require.NoError(t, err)
		asked += n
	}
	require.Equal(t, 2, asked, "the readers table is the other view of the same ask: the pro card's two reads, asked together")

	var flash, pro cardView
	ta.json("card s1-1", &flash)
	ta.json("card s1-2", &pro)
	require.Len(t, flash.Reads, 1, "the flash read is on her fleet row, not the readers table")
	require.Equal(t, sprint.FriendRow("amy"), flash.Reads[0].Row)
	require.Len(t, pro.Reads, 2, "no friend at or above pro has room: paid readers have the pro card's two reads")
	require.Equal(t, sprint.Asked, pro.Reads[0].Col)

	out := ta.ok("friend sync --root " + root)
	require.Contains(t, out, "FRIEND-READ DELIVERED")
	inbox := filepath.Join(root, "amy-working", "inbox")
	entries, err := os.ReadDir(inbox)
	require.NoError(t, err)
	var readID string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "s1-1.r") {
			readID = e.Name()
		}
	}
	require.NotEmpty(t, readID, "her inbox is the flash read: %v", entries)
	text, err := os.ReadFile(filepath.Join(inbox, readID, "BRIEF.md"))
	require.NoError(t, err)
	require.Contains(t, string(text), "WHO: friend amy\n")
	// a read asked the old way (read cards off) keeps the old brief and its inbox path
	deadline := ta.now.Add(sprint.FriendReadDeadline).UTC().Format(time.RFC3339)
	require.Contains(t, string(text), "deadline: "+deadline+"\n")

	outboxReport(t, root, "amy", readID, "Verdict: LAND\n")
	out = ta.ok("friend sync --root " + root)
	require.Contains(t, out, "FRIEND-READ finished friend=amy card="+readID)
	ta.json("where", &w)
	require.Equal(t, "0", cellText(w.Tables[sprint.Friends]["amy"]["working"]), "her verdict retired the read")

	rd := pro.Reads[0].F("reader")
	require.NotEmpty(t, rd)
	ta.ok("read --as " + rd + " --ok " + pro.Reads[0].ID)
	ta.json("where", &w)
	okN := 0
	for _, row := range w.Tables[sprint.Readers] {
		n, err := strconv.Atoi(cellText(row[sprint.OK]))
		require.NoError(t, err)
		okN += n
	}
	require.Equal(t, 1, okN, "a reader's verdict closes the other read")
}
