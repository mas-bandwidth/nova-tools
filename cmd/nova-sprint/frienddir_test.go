package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAFriendsDirectoryComesFromHerRowNotASymlink(t *testing.T) {
	t.Parallel()

	// 1. Config validation: nova-config friend add/set --dir checks
	k, ok := config.Lookup(config.KindFriend)
	require.True(t, ok)

	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "real-working")
	require.NoError(t, os.MkdirAll(realDir, 0o755))

	symlinkDir := filepath.Join(tmp, "symlink-working")
	require.NoError(t, os.Symlink(realDir, symlinkDir))

	file := filepath.Join(tmp, "regular-file")
	require.NoError(t, os.WriteFile(file, []byte("hello"), 0o644))

	// Refuses relative path
	err := k.Check(config.Row{Name: "amy", Fields: map[string]string{"slots": "2", "tiers": "flash", "dir": "relative/path"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--dir wants an absolute path")

	// Refuses non-existent path
	err = k.Check(config.Row{Name: "amy", Fields: map[string]string{"slots": "2", "tiers": "flash", "dir": filepath.Join(tmp, "does-not-exist")}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such file or directory")

	// Refuses symlink
	err = k.Check(config.Row{Name: "amy", Fields: map[string]string{"slots": "2", "tiers": "flash", "dir": symlinkDir}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a symlink: friends want real directories")

	// Refuses file
	err = k.Check(config.Row{Name: "amy", Fields: map[string]string{"slots": "2", "tiers": "flash", "dir": file}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a directory")

	// Accepts real directory
	err = k.Check(config.Row{Name: "amy", Fields: map[string]string{"slots": "2", "tiers": "flash", "dir": realDir}})
	require.NoError(t, err)

	// 2. Sprint app setup
	ta := newTestApp(t)
	root := t.TempDir()
	home := t.TempDir()
	prevGetenv := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == "HOME" {
			return home
		}
		return prevGetenv(k)
	}
	ta.a.home = func() (string, error) {
		return home, nil
	}

	// Create a decoy symlink at <root>/amy-working pointing to decoyDir
	decoyDir := filepath.Join(tmp, "decoy")
	require.NoError(t, os.MkdirAll(decoyDir, 0o755))
	symlinkAmy := filepath.Join(root, "amy-working")
	require.NoError(t, os.Symlink(decoyDir, symlinkAmy))

	cfg := config.NewMem()
	ta.a.friends = func(ctx context.Context, _ string) ([]config.Row, error) {
		return cfg.List(ctx, config.KindFriend)
	}

	// Add amy with dir: realDir, and bob without dir
	_, err = cfg.Insert(context.Background(), config.KindFriend, config.Row{Name: "amy", Fields: map[string]string{"slots": "2", "tiers": "flash", "dir": realDir}}, "t")
	require.NoError(t, err)
	_, err = cfg.Insert(context.Background(), config.KindFriend, config.Row{Name: "bob", Fields: map[string]string{"slots": "2", "tiers": "flash"}}, "t")
	require.NoError(t, err)

	ta.a.tip = tipIs(t, landHead)
	ta.ok("init --readers reader-a,reader-b --members m1,m2")

	// Friend sync: amy has dir, bob does not.
	// bob prints fallback note once.
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "NOTE friend=bob: her row has no dir; falling back to "+filepath.Join(root, "bob-working"))
	assert.NotContains(t, out, "NOTE friend=amy:")

	// Second friend sync: fallback note is not printed again!
	out2 := ta.ok("friend sync --root " + root)
	assert.NotContains(t, out2, "NOTE friend=bob: her row has no dir")

	ta.ok("friend beat amy")
	ta.ok("friend beat bob")

	// Add a card dealt to amy
	briefFile := filepath.Join(t.TempDir(), "s1-1.md")
	require.NoError(t, os.WriteFile(briefFile, []byte(passingBrief("s1-1: test card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy")), 0o644))
	ta.ok("add --stream s1 --brief-dir " + filepath.Dir(briefFile))
	ta.ok("start")
	ta.ok("tick")

	// friend sync delivers card to amy's realDir, not under the symlink
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-1.w1")

	// Check realDir has BRIEF.md and decoy has none
	amyBrief := filepath.Join(realDir, "inbox", "s1-1.w1", "BRIEF.md")
	_, err = os.Stat(amyBrief)
	require.NoError(t, err, "brief should be written to realDir")
	_, err = os.Stat(filepath.Join(decoyDir, "inbox", "s1-1.w1", "BRIEF.md"))
	require.True(t, os.IsNotExist(err), "decoy should have no brief")

	// Check brief content has realDir
	briefContent, err := os.ReadFile(amyBrief)
	require.NoError(t, err)
	assert.Contains(t, string(briefContent), "Work in "+realDir+"/jobs/s1-1.w1/:")
	assert.Contains(t, string(briefContent), "GOCACHE="+realDir+"/.cache/go-build")
	assert.Contains(t, string(briefContent), "report goes to "+realDir+"/outbox/s1-1.w1/REPORT.md")

	// Check friends table in where shows amy's dir and bob's "-"
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, realDir, w.Tables[sprint.Friends]["amy"]["dir"])
	assert.Equal(t, "-", w.Tables[sprint.Friends]["bob"]["dir"])

	// Check worker view
	var wv workerView
	ta.json("view worker --as amy", &wv)
	assert.Equal(t, realDir, wv.Dir)
	require.Len(t, wv.Cards, 1)
	assert.Equal(t, amyBrief, wv.Cards[0].Brief)
	assert.Contains(t, wv.Next, "write "+filepath.Join(realDir, "outbox", "s1-1.w1", "REPORT.md"))

	// Reconcile: write QUEUE.json in realDir/inbox/
	queueJSON := filepath.Join(realDir, "inbox", "QUEUE.json")
	require.NoError(t, os.WriteFile(queueJSON, []byte(`{"tasks":[{"id":"s1-1.w1","state":"working"}]}`), 0o644))
	recOut := ta.ok("friend reconcile amy --root " + root)
	assert.Contains(t, recOut, "FRIEND-RECONCILE OK friend=amy")

	// Seat inbox: amy resolves to realDir, bob falls back to home/bob-working
	amySeatInbox, amyParent, amyOK := ta.a.seatInbox("amy")
	assert.Equal(t, filepath.Join(realDir, "inbox", "sprint-judgments"), amySeatInbox)
	assert.Equal(t, filepath.Join(realDir, "inbox"), amyParent)
	assert.True(t, amyOK)

	bobSeatInbox, bobParent, bobOK := ta.a.seatInbox("bob")
	assert.Equal(t, filepath.Join(home, "bob-working", "inbox", "sprint-judgments"), bobSeatInbox)
	assert.Equal(t, filepath.Join(home, "bob-working", "inbox"), bobParent)
	assert.False(t, bobOK) // parent does not exist yet

	// Write report to realDir
	reportDir := filepath.Join(realDir, "outbox", "s1-1.w1")
	require.NoError(t, os.MkdirAll(reportDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(reportDir, "REPORT.md"), []byte("Verdict: LAND\nHead: "+landHead+"\n\nPushed.\n"), 0o644))

	// Sync finishes card from realDir
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=amy card=s1-1.w1 result=ok")

	// Check friend clean cleans realDir
	// Create a dummy done job in realDir older than 3 days
	oldJob := "s1-old.w1"
	require.NoError(t, os.MkdirAll(filepath.Join(realDir, "jobs", oldJob, "build"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(realDir, "jobs", oldJob, "build", "out.bin"), []byte("build"), 0o644))
	oldOutbox := filepath.Join(realDir, "outbox", oldJob)
	require.NoError(t, os.MkdirAll(oldOutbox, 0o755))
	oldReport := filepath.Join(oldOutbox, "REPORT.md")
	require.NoError(t, os.WriteFile(oldReport, []byte("Verdict: LAND\nHead: "+landHead+"\n"), 0o644))
	fourDaysAgo := time.Now().Add(-4 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(oldReport, fourDaysAgo, fourDaysAgo))

	cleanOut := ta.ok("friend clean --root " + root)
	assert.Contains(t, cleanOut, "FRIENDS-CLEAN FRIEND amy dir="+realDir)

	// Check symlink was NOT removed
	_, err = os.Lstat(symlinkAmy)
	require.NoError(t, err, "symlink must not be removed by friend clean or friend sync")
}
