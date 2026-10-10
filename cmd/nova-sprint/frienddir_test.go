package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	nfriend "github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A friend's working directory is her nova-config row's dir (frienddir.go): amy's
// row names a real directory, /x/amy/working
// under t.TempDir(), and no <root>/amy-working is there at all; friend sync delivers
// her card there, friend reconcile and the run loop's tick collect her reports there,
// nova-sprint collect finishes her card from there, friend clean cleans there and the
// seat's inbox is there. A --dir that is a symlink, or no directory, is refused by
// nova-config. bob's row has no dir: he falls back to <root>/bob-working, said once a
// run as a NOTE line.
func TestAFriendsDirectoryComesFromHerRowNotASymlink(t *testing.T) {
	t.Parallel()
	friend, ok := config.Lookup(config.KindFriend)
	require.True(t, ok)
	dir := filepath.Join(t.TempDir(), "x", "amy", "working")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	// a symlinked --dir, one that is not there, one that is a file, a relative
	// path and an unclean path are refused on set and on add; an unset --dir '' is taken
	link := filepath.Join(t.TempDir(), "amy-working")
	require.NoError(t, os.Symlink(dir, link))
	file := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	for _, tc := range []struct{ dir, says string }{
		{link, "is a symlink, and a friend's dir is her real directory: want --dir " + dir},
		{filepath.Join(dir, "nowhere"), "is not an existing directory on this machine"},
		{file, "is not a directory"},
		{"x/amy/working", "a friend's dir is an absolute path"},
		{dir + "/", "a friend's dir is an absolute path"},
		{dir + "/../working", "a friend's dir is an absolute path"},
	} {
		_, err := friend.Changes(map[string]string{"dir": tc.dir})
		require.Error(t, err, "set --dir %s", tc.dir)
		assert.Contains(t, err.Error(), tc.says, "set --dir %s", tc.dir)
		_, err = friend.NewRow("amy", map[string]string{"slots": "2", "tiers": "flash", "dir": tc.dir})
		require.Error(t, err, "add --dir %s", tc.dir)
		assert.Contains(t, err.Error(), tc.says, "add --dir %s", tc.dir)
	}
	changes, err := friend.Changes(map[string]string{"dir": ""})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"dir": ""}, changes, "--dir '' unsets it")
	changes, err = friend.Changes(map[string]string{"dir": dir})
	require.NoError(t, err)

	ta, cfg := friendApp(t)
	ta.a.tip = tipIs(t, landHead)
	ctx := context.Background()
	for _, n := range []string{"amy", "bob"} {
		row, err := friend.NewRow(n, map[string]string{"slots": "2", "tiers": "flash"})
		require.NoError(t, err)
		_, err = cfg.Insert(ctx, config.KindFriend, row, "t")
		require.NoError(t, err)
	}
	_, _, err = cfg.Update(ctx, config.KindFriend, "amy", changes, "t")
	require.NoError(t, err)
	rows, err := cfg.List(ctx, config.KindFriend)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, dir, config.FriendDir(rows[0]), "amy's row holds her dir")
	assert.Contains(t, config.RowLine(friend, rows[0]), " dir="+dir, "friend list shows it")
	assert.Equal(t, "", config.FriendDir(rows[1]), "bob's row has none")

	root := t.TempDir()
	amyAlias, bobDir := filepath.Join(root, "amy-working"), filepath.Join(root, "bob-working")
	sync := func() (string, string) {
		t.Helper()
		code, out, errs := ta.do("friend sync --root " + root)
		require.Equal(t, 0, code, "friend sync: %s%s", out, errs)
		return out, errs
	}
	_, errs := sync()
	note := "NOTE friend=bob has no dir on her nova-config row, so her working directory is " + bobDir + "; run: nova-config friend set bob --dir <her real working directory>\n"
	assert.Equal(t, note, errs, "bob's fallback is said, amy's dir is not")
	ta.beatUp("amy")
	ta.beatUp("bob")

	briefs := t.TempDir()
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		require.NoError(t, os.WriteFile(filepath.Join(briefs, id+".md"), []byte(passingBrief(id+": a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy")), 0o644))
	}
	ta.ok("add --stream s1 --brief-dir " + briefs)
	ta.ok("start")
	ta.ok("tick")

	// sync: her cards are delivered into her row's dir, and the fallback is not said again
	out, errs := sync()
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-1.w1")
	assert.Empty(t, errs, "the fallback is said once a run")
	for _, j := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"} {
		assert.FileExists(t, filepath.Join(dir, "inbox", j, "BRIEF.md"), "%s is delivered into her dir", j)
	}
	assert.NoDirExists(t, amyAlias, "nothing is written to <root>/amy-working")
	text, err := os.ReadFile(filepath.Join(dir, "inbox", "s1-1.w1", "BRIEF.md"))
	require.NoError(t, err)
	assert.Contains(t, string(text), "Work in "+dir+"/jobs/s1-1.w1/: every clone, worktree and build output goes inside it, GOCACHE="+dir+"/.cache/go-build is your own build cache, warm across your cards: keep it, and the report goes to "+dir+"/outbox/s1-1.w1/REPORT.md.\n", "her brief names her real directory")
	assert.NotContains(t, string(text), "amy-working", "and never the symlink")

	// her worker view names the brief in her dir (her cards are ready until she starts one)
	var wv workerView
	ta.json("view worker --as amy", &wv)
	require.NotEmpty(t, wv.Cards)
	assert.Equal(t, filepath.Join(dir, "inbox", "s1-1.w1", "BRIEF.md"), wv.Cards[0].Brief)
	assert.Contains(t, wv.Next, "its brief is "+dir+"/inbox/s1-1.w1/BRIEF.md")

	// friend cards, the brief her daemon writes, names her dir as friend sync's does
	held, err := nfriend.ParseHeld("amy", ta.ok("friend cards amy --json"))
	require.NoError(t, err)
	require.NotEmpty(t, held)
	assert.Contains(t, held[0].Brief, "Work in "+dir+"/jobs/"+held[0].Job+"/", "the daemon's brief names her real directory")
	assert.NotContains(t, held[0].Brief, "amy-working", "and never the symlink")

	// sync collects a report from her dir's outbox
	report := func(job string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", job), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "outbox", job, "REPORT.md"), []byte("Verdict: LAND\nHead: "+landHead+"\n\nPushed and green.\n"), 0o644))
	}
	report("s1-1.w1")
	out, _ = sync()
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=amy card=s1-1.w1 result=ok")

	// sync reads her start in her dir: a write under jobs/<job> after its staging
	begin := func(job string) {
		t.Helper()
		jobDir := filepath.Join(dir, nfriend.JobsDir, job)
		require.NoError(t, os.MkdirAll(jobDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(jobDir, nfriend.JobFile), nil, 0o644))
		staged := time.Now().Add(-time.Hour)
		require.NoError(t, os.Chtimes(filepath.Join(jobDir, nfriend.JobFile), staged, staged))
		require.NoError(t, os.WriteFile(filepath.Join(jobDir, "work.go"), nil, 0o644))
		out, _ := sync()
		assert.Contains(t, out, "FRIEND-CARD STARTED friend=amy card="+job)
	}
	begin("s1-3.w1")

	// reconcile, the verb: her QUEUE.json and outbox are read in her dir, with --root
	// naming a directory where she has none
	report("s1-2.w1")
	writeTestQueueFile(t, ta, filepath.Dir(dir), "amy", `{"tasks":[{"id":"s1-2.w1","state":"done"},{"id":"s1-3.w1","state":"working"}]}`)
	require.NoError(t, os.Rename(filepath.Join(filepath.Dir(dir), "amy-working", "inbox", "QUEUE.json"), filepath.Join(dir, "inbox", "QUEUE.json")))
	out = ta.ok("friend reconcile amy --root " + root)
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=amy card=s1-2.w1 result=ok")
	assert.Contains(t, out, "FRIEND-RECONCILE OK friend=amy collected=1 kept=1 returned=0")

	// reconcile, the run loop's tick: HOME holds no amy-working, and her dir is read
	homeIs(ta, root)
	report("s1-3.w1")
	ticked := runTicks(t, ta, 1)
	assert.Contains(t, ticked, "FRIEND-CARD FINISHED friend=amy card=s1-3.w1 result=ok", "the tick collects from her dir")
	assert.NotContains(t, ticked, "SKIPPED friend=amy", "her dir is known")
	assert.NotContains(t, ticked+runTicks(t, ta, 2), "NOTE friend=bob", "bob's fallback was said once this run, by the first sync")

	// collect: a card of a stream added since is finished from her dir's outbox, with
	// --root naming a directory where she has none
	more := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(more, "s2-1.md"), []byte(passingBrief("s2-1: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy")), 0o644))
	ta.ok("add --stream s2 --brief-dir " + more)
	ta.ok("tick")
	out, _ = sync()
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s2-1.w1")
	begin("s2-1.w1")
	report("s2-1.w1")
	out = ta.ok("collect amy --root " + root)
	assert.Contains(t, out, "COLLECT s2-1.w1 LAND "+landHead+"\n", "collect reads her dir's outbox")
	assert.Contains(t, out, "landed=1")
	assert.NoDirExists(t, amyAlias, "collect needs no <root>/amy-working")

	// clean: her dir is the one cleaned, and bob uses the fallback directory
	out = ta.ok("friend clean --root " + root + " --dry-run")
	assert.Contains(t, out, "FRIENDS-CLEAN FRIEND amy dir="+dir+" jobs=")
	assert.Contains(t, out, "FRIENDS-CLEAN FRIEND bob dir="+bobDir)

	// the seat's inbox: hers is inbox/sprint-judgments in her dir
	p := &pushTarget{a: ta.a, seen: map[string]map[string]bool{}}
	var sout, serr bytes.Buffer
	require.Equal(t, 0, p.follow("amy", true, &sout, &serr), "follow: %s%s", sout.String(), serr.String())
	assert.Equal(t, filepath.Join(dir, "inbox", "sprint-judgments"), p.dirOf("amy", sprint.Group{}))
	assert.NoDirExists(t, amyAlias, "and still nothing at <root>/amy-working")
}

// A long-running inbox --wait --push seat rereads the friend rows at every look
// (seat.go follow, inboxwait.go's wait callback): after nova-config friend set
// <holder> --dir <new path> the next look swaps the push to the new directory and
// says the seat's NOTE once, so the push does not silently stop writing to the old
// path when it goes away. The new directory's keys are read on their first use.
func TestTheSeatPushFollowsTheHoldersNewDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, dir := range []string{first, second} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	}

	ta := newTestApp(t)
	dir := first
	ta.a.friends = func(context.Context, string) ([]config.Row, error) {
		return []config.Row{{Name: "amy", Fields: map[string]string{"dir": dir}}}, nil
	}

	p := &pushTarget{a: ta.a, seen: map[string]map[string]bool{}}
	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, p.follow("amy", true, &stdout, &stderr), "first look: %s%s", stdout.String(), stderr.String())
	require.Equal(t, filepath.Join(first, "inbox", "sprint-judgments"), p.dirOf("amy", sprint.Group{}))

	// the row's dir moves between two looks with the same holder
	dir = second
	stdout.Reset()
	stderr.Reset()
	require.Equal(t, 0, p.follow("amy", false, &stdout, &stderr), "second look: %s%s", stdout.String(), stderr.String())
	assert.Equal(t, "NOTE the seat is amy's: pushing to "+filepath.Join(second, "inbox", "sprint-judgments")+"\n", stdout.String())
	assert.Empty(t, stderr.String(), "an unchanged holder with a new dir is not a failure")
	assert.Equal(t, filepath.Join(second, "inbox", "sprint-judgments"), p.dirOf("amy", sprint.Group{}), "the next note lands in the new directory")
	assert.NotContains(t, p.seen, filepath.Join(second, "inbox", "sprint-judgments"), "the new directory's keys are read on first use")
}

// A seat whose app has the real friend reader, and no config store named, keeps
// ~/<name>-working. Tests of inbox --push seat and the folder proof stand up
// that fixture and never stub friends (seat_test.go, pushproof_test.go).
func TestSeatInboxKeepsTheLegacyPathWhenNoConfigStoreIsNamed(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	home := t.TempDir()
	ta.a.home = func() (string, error) { return home, nil }
	require.NoError(t, os.MkdirAll(filepath.Join(home, "amy-working", "inbox"), 0o755))
	p := &pushTarget{a: ta.a, seen: map[string]map[string]bool{}}
	var stdout, stderr bytes.Buffer
	code := p.follow("amy", true, &stdout, &stderr)
	assert.Equal(t, 0, code, "no config store keeps the legacy inbox: %s", stderr.String())
	assert.NotContains(t, stderr.String(), "--pg is required")
	assert.Equal(t, filepath.Join(home, "amy-working", "inbox", "sprint-judgments"), p.dirOf("amy", sprint.Group{}))
	assert.Empty(t, stdout.String())
}

func TestSeatInboxDoesNotFallBackWhenFriendRowsCannotBeRead(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.a.friends = func(context.Context, string) ([]config.Row, error) {
		return nil, errors.New("config store unavailable")
	}
	var stdout, stderr bytes.Buffer
	p := &pushTarget{a: ta.a, seen: map[string]map[string]bool{}}
	code := p.follow("amy", true, &stdout, &stderr)
	assert.Equal(t, 2, code, "a failed config read cannot select HOME/<friend>-working")
	assert.Contains(t, stderr.String(), "friend rows cannot be read: config store unavailable")
	assert.NotContains(t, stderr.String(), "amy-working")
	assert.Empty(t, stdout.String())
	assert.Empty(t, p.holder, "the failed move is retried on the next look")
	_, err := p.unseen(inboxLook{holder: "amy"})
	assert.Error(t, err, "the wait callback surfaces the read failure")
}

func TestFriendDirectoryFallbackCanBeNotedAfterSilentLookup(t *testing.T) {
	t.Parallel()
	a := &app{}
	// The fallback path uses the layout package
	home, err := os.UserHomeDir()
	if err != nil {
		home = t.TempDir()
	}
	fallback := filepath.Join(home, "nova", "ai", "amy", "working")
	assert.Equal(t, fallback, a.friendDir("amy", "", nil), "a nil writer does not consume the note")
	var note bytes.Buffer
	assert.Equal(t, fallback, a.friendDir("amy", "", &note))
	assert.Equal(t, "NOTE friend=amy has no dir on her nova-config row, so her working directory is "+fallback+"; run: nova-config friend set amy --dir <her real working directory>\n", note.String())
	var second bytes.Buffer
	a.friendDir("amy", "", &second)
	assert.Empty(t, second.String(), "the note is written once")
}

// The folder proof uses the row directory too (docs/FRIENDS.md), and cannot
// quietly deliver to the legacy path after a config read failure.
func TestPushJudgmentsUsesTheFriendRowDirectory(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t, "amy")
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "working")
	inbox := filepath.Join(dir, "inbox", "sprint-judgments")
	require.NoError(t, os.MkdirAll(inbox, 0o755))
	_, _, err := cfg.Update(ctx, config.KindFriend, "amy", map[string]string{"dir": dir}, "t")
	require.NoError(t, err)
	st, err := ta.a.store(common{redis: "mem:0", actor: "amy"})
	require.NoError(t, err)
	require.NoError(t, writePush(ctx, st, sprint.PushRecord{Name: "amy", Harness: "claude", Adapter: sprint.AdapterFolder, Target: inbox}))
	var out bytes.Buffer
	ta.a.pushJudgments(ctx, &storeSource{st: st}, "amy", []string{"JUDGMENT one"}, false, &out)
	assert.Contains(t, out.String(), "PUSH OK name=amy")
	files, err := os.ReadDir(inbox)
	require.NoError(t, err)
	assert.Empty(t, files, "the existing judgments are not written twice")
	ta.a.friends = func(context.Context, string) ([]config.Row, error) {
		return nil, errors.New("config store unavailable")
	}
	out.Reset()
	ta.a.pushJudgments(ctx, &storeSource{st: st}, "amy", []string{"JUDGMENT two"}, false, &out)
	assert.Contains(t, out.String(), "PUSH DOWN name=amy")
	assert.Contains(t, out.String(), "friend rows cannot be read: config store unavailable")
	files, err = os.ReadDir(inbox)
	require.NoError(t, err)
	assert.Empty(t, files, "a failed row read writes no duplicate")
}

// Review briefs name the reader's directory while preserving the quoted card
// verbatim (docs/FRIENDS.md, the card's brief).
func TestFriendReviewBriefNamesHerRowDirectory(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	st, err := ta.a.store(common{redis: "mem:0", actor: "amy"})
	require.NoError(t, err)
	dir := t.TempDir()
	p := sprint.Packet{Card: "s1-1.r1", Primary: "s1-1", Brief: "REPO: example/tools\nKeep ~/amy-working in the quoted evidence.\n"}
	text := friendReadTextAtDir(st, "amy", dir, p, nil)
	assert.Contains(t, text, "Work in "+dir+"/jobs/")
	assert.Contains(t, text, "the report goes to "+dir+"/outbox/")
	assert.Contains(t, text, "Keep ~/amy-working in the quoted evidence.")
}

func TestFriendCleanDoesNotNeedALegacyRootWhenEveryRowHasDir(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t, "amy")
	dir := t.TempDir()
	_, _, err := cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"dir": dir}, "t")
	require.NoError(t, err)
	out := ta.ok("friend clean --root " + filepath.Join(t.TempDir(), "absent") + " --dry-run")
	assert.Contains(t, out, "FRIENDS-CLEAN FRIEND amy dir="+dir)
}
