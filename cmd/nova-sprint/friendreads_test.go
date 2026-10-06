package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A read asked of a friend is delivered like a card: her reader room is her
// read slots, the three files land under inbox/reads/<id>/ with a bus note,
// and a recorded read removes the directory.
func TestAReadAskedOfAFriendIsDeliveredToHerLikeACard(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t, "amy")
	_, _, err := cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"width": "8", "read_slots": "2", "read_wait": "900"}, "t")
	require.NoError(t, err)

	ta.ok("friend sync")
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	snap, err := st.Load(context.Background(), []string{sprint.Fleet}, nil)
	require.NoError(t, err)
	require.Equal(t, 2, snap.ReaderWidth("reader-amy"), "room is her read slots, not her width 8")
	spec, err := st.FriendSpecOf(context.Background(), "amy")
	require.NoError(t, err)
	require.Equal(t, 8, spec.Width)
	require.Equal(t, 2, spec.ReadSlots)
	require.Equal(t, 900, spec.ReadWait)
	waits, ok := snap.Fleet.Prop(sprint.PropFriendReadWait)
	require.True(t, ok, "friend sync writes her read wait on the fleet")
	require.JSONEq(t, `{"amy":900}`, waits)
	again := ta.ok("friend sync")
	require.Contains(t, again, "nothing to do", "a second sync writes nothing: %s", again)

	ta.ok("reader add reader-amy")
	var w whereView
	ta.json("where", &w)
	require.Equal(t, "2", cellText(w.Tables[sprint.Readers]["reader-amy"][sprint.FieldWidth]), "where draws her reader at her read slots")
	require.Equal(t, "8", cellText(w.Tables[sprint.Friends]["amy"][sprint.FieldWidth]), "her card width stays her row's")
	require.Equal(t, "2", cellText(w.Tables[sprint.Friends]["amy"][sprint.ReadSlots]), "her read slots stand beside her width in her friends row")
	require.Equal(t, "friends | ready | working | width | read_slots", strings.Join(strings.Fields(strings.SplitN(tableOf(ta.frame(), sprint.Friends), "\n", 2)[0])[:9], " "), "where's text draws read_slots right after width")
	ta.ok("reader away reader-a")
	ta.ok("reader away reader-b")
	ta.ok("add --stream s1 --count 1 --one")
	ta.deal(1)
	holder := ""
	for _, m := range []string{"m1", "m2"} {
		var q struct{ Cards []queueCard }
		ta.json("queue --as "+m, &q)
		if len(q.Cards) > 0 {
			holder = m
			break
		}
	}
	require.NotEmpty(t, holder, "the work card was dealt")
	ta.ok("take --as " + holder + " s1-1.w1@1")
	ta.ok("finish --as " + holder + " s1-1.w1@1")
	ta.ok("ask s1-1")

	id := sprint.ReadCardID("s1-1", 1, "reader-amy")
	var asked struct {
		Epoch uint64      `json:"epoch"`
		Cards []queueCard `json:"cards"`
	}
	ta.json("queue --as reader-amy", &asked)
	require.NotEmpty(t, asked.Cards, "the read was asked of her")
	require.Equal(t, id, asked.Cards[0].ID)

	before := len(ta.sent)
	out := ta.ok("friend sync")
	require.Contains(t, out, "reads=1")
	home := ta.a.getenv("HOME")
	dir := filepath.Join(home, "amy-working", "inbox", "reads", id)
	read, err := os.ReadFile(filepath.Join(dir, "READ.md"))
	require.NoError(t, err)
	epoch := asked.Epoch
	for _, want := range []string{
		fmt.Sprintf("nova-sprint read --as reader-amy --begin %s --epoch %d", id, epoch),
		"Clone the brief's REPO at the head ",
		"Judge the merge-base diff against the brief",
		"Run the touched packages' vet and tests on a Linux bench",
		fmt.Sprintf("nova-sprint read --as reader-amy --ok %s --epoch %d", id, epoch),
		fmt.Sprintf("nova-sprint read --as reader-amy --broken %s --epoch %d --finding '<file:line, and what to change>'", id, epoch),
		fmt.Sprintf("nova-sprint read --as reader-amy --return %s --reason '<why there is no verdict>' --epoch %d", id, epoch),
	} {
		require.Contains(t, string(read), want)
	}
	_, err = os.Stat(filepath.Join(dir, "BRIEF.md"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "WORKER-REPORT.txt"))
	require.NoError(t, err)
	sent := append([]bus.Message(nil), ta.sent...)
	require.Greater(t, len(sent), before)
	found := false
	for _, m := range sent[before:] {
		if strings.Contains(m.Subject, "read asked") && m.To[0] == "amy" {
			found = true
		}
	}
	require.True(t, found, "a bus note says read asked: %+v", sent[before:])

	again = ta.ok("friend sync")
	require.Contains(t, again, "nothing to do", "a delivered read is not delivered twice: %s", again)

	ta.ok(fmt.Sprintf("read --as reader-amy --ok %s --epoch %d", id, epoch))
	out = ta.ok("friend sync")
	require.Contains(t, out, "reads_removed=1")
	_, err = os.Stat(dir)
	require.True(t, os.IsNotExist(err), "a recorded read removes the directory")
}

// A symlink where a read directory would be is refused and left in place.
func TestASymlinkUnderInboxReadsIsLeftAlone(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")
	home := ta.a.getenv("HOME")
	reads := filepath.Join(home, "amy-working", "inbox", "reads")
	require.NoError(t, os.MkdirAll(filepath.Join(home, "amy-working", "inbox"), 0o755))
	target := filepath.Join(home, "elsewhere")
	require.NoError(t, os.MkdirAll(target, 0o755))
	link := filepath.Join(reads, "s1-1.r1.reader-amy")
	require.NoError(t, os.MkdirAll(reads, 0o755))
	require.NoError(t, os.Symlink(target, link))
	code, _, errs := ta.do("friend sync")
	require.NotEqual(t, 0, code, errs)
	require.Contains(t, errs, "symlink")
	fi, err := os.Lstat(link)
	require.NoError(t, err)
	require.True(t, fi.Mode()&os.ModeSymlink != 0, "the symlink is still there")
	_, err = os.Stat(target)
	require.NoError(t, err)
}
