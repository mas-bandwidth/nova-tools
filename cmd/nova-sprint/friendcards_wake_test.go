package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Friend sync wakes a friend at most once per pass (docs/FRIENDS.md, the pass
// wake). These tests use the in-memory sprint and the injected bus: no socket,
// and the clock is the test app's, never the wall.

func sentBus(ta *testApp) []bus.Message {
	ta.mu.Lock()
	defer ta.mu.Unlock()
	return append([]bus.Message(nil), ta.sent...)
}

// passWakeApp is a sprint with amy up (batch, the default) and her working root.
func passWakeApp(t *testing.T) (*testApp, *config.Mem, string) {
	t.Helper()
	ta, cfg := friendApp(t, "amy")
	ta.a.tip = tipIs(t, landHead)
	root := t.TempDir()
	ta.ok("friend sync --root " + root)
	ta.beatUp("amy")
	return ta, cfg, root
}

// addAmyCards deals n cards to amy and does not deliver them.
func addAmyCards(t *testing.T, ta *testApp, n int) {
	t.Helper()
	dir := t.TempDir()
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("s1-%d", i)
		body := passingBrief(id + ": a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: only friend amy")
		require.NoError(t, os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644))
	}
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("start")
	ta.ok("tick")
}

func passSubjectIDs(t *testing.T, subject string) (int, []string) {
	t.Helper()
	rest, ok := strings.CutPrefix(subject, "cards dealt: ")
	require.True(t, ok, subject)
	nText, list, ok := strings.Cut(rest, " (")
	require.True(t, ok, subject)
	n, err := strconv.Atoi(nText)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(list, ")"), subject)
	list = strings.TrimSuffix(list, ")")
	if list == "" {
		return n, nil
	}
	return n, strings.Split(list, ", ")
}

func TestPassWakeSubjectCutsIdsAtTen(t *testing.T) {
	t.Parallel()
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}
	assert.Equal(t, "cards dealt: 3 (a, b, c)", passWakeSubject([]string{"a", "b", "c"}))
	assert.Equal(t, "cards dealt: 10 ("+strings.Join(ids[:10], ", ")+")", passWakeSubject(ids[:10]))
	assert.Equal(t, "cards dealt: 12 ("+strings.Join(ids[:10], ", ")+" and 2 more)", passWakeSubject(ids))
}

func TestFriendSyncWakesABatchFriendOnceForThreeCards(t *testing.T) {
	t.Parallel()
	ta, _, root := passWakeApp(t)
	addAmyCards(t, ta, 3)
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "delivered=3")
	sent := sentBus(ta)
	require.Len(t, sent, 1, "one message for the pass, not one per card")
	m := sent[0]
	assert.Equal(t, "coordinator", m.From)
	assert.Equal(t, []string{"amy"}, m.To)
	assert.Equal(t, bus.KindStatus, m.Kind)
	n, ids := passSubjectIDs(t, m.Subject)
	assert.Equal(t, 3, n)
	assert.ElementsMatch(t, []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"}, ids)
	assert.Equal(t, filepath.Join(root, "amy-working", "inbox")+"\n"+friendWakeWork, m.Body)
	assert.NotContains(t, m.Body, "BRIEF.md", "the body names the inbox directory, not each brief")

	ta.ok("friend sync --root " + root)
	assert.Len(t, sentBus(ta), 1, "a pass that delivers nothing sends nothing")
	ta.clean()
}

func TestFriendSyncWakesAOneShotFriendOncePerCard(t *testing.T) {
	t.Parallel()
	ta, cfg, root := passWakeApp(t)
	addAmyCards(t, ta, 3)
	_, _, err := cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"mode": config.FriendModeOneShot}, "t")
	require.NoError(t, err)
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "delivered=3")
	sent := sentBus(ta)
	require.Len(t, sent, 3, "one-shot keeps one message per card")
	got := map[string]bus.Message{}
	for _, m := range sent {
		assert.Equal(t, "coordinator", m.From)
		assert.Equal(t, []string{"amy"}, m.To)
		assert.Empty(t, m.Kind, "today's per-card message sets no kind")
		assert.NotContains(t, m.Subject, "cards dealt:")
		id, _, ok := strings.Cut(strings.TrimPrefix(m.Subject, "card "), " dealt:")
		require.True(t, ok, m.Subject)
		got[id] = m
	}
	for _, id := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"} {
		m, ok := got[id]
		require.True(t, ok, "no message for %s in %v", id, sent)
		assert.Contains(t, m.Subject, "FRIEND-CARD DELIVERED friend=amy card="+id)
		assert.Contains(t, m.Body, filepath.Join(root, "amy-working", "inbox", id, "BRIEF.md"))
		assert.Contains(t, m.Body, friendWakeWork)
	}
	ta.clean()
}

func TestFriendSyncSendsNoWakeWhenNoCardWasDelivered(t *testing.T) {
	t.Parallel()
	ta, _, root := passWakeApp(t)
	assert.Empty(t, sentBus(ta), "the roster sync delivered nothing")
	ta.ok("friend sync --root " + root)
	assert.Empty(t, sentBus(ta))
	ta.clean()
}

func TestFriendSyncNotesAFailedPassWakeOnce(t *testing.T) {
	t.Parallel()
	ta, _, root := passWakeApp(t)
	addAmyCards(t, ta, 3)
	ta.a.bus = func(context.Context, bus.Message, func(string)) error {
		return errors.New("dial tcp: connection refused")
	}
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "delivered=3")
	assert.Equal(t, 1, strings.Count(out, "FRIEND-CARD NOTE"), out)
	assert.Contains(t, out, "pass=3(")
	assert.Contains(t, out, "the bus message to her was not sent (dial tcp: connection refused); the inbox files stand, tell her by hand")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "FRIEND-CARD NOTE") {
			assert.NotContains(t, line, "card=", "the line names the pass, not each card")
			assert.Contains(t, line, "pass=3(")
		}
	}
	for _, id := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"} {
		_, err := os.Stat(filepath.Join(root, "amy-working", "inbox", id, "BRIEF.md"))
		require.NoError(t, err, "the delivery stands")
		assert.Contains(t, out, id)
	}
	notes, _, err := ta.m.NotesSince(context.Background(), "", 1000)
	require.NoError(t, err)
	var woken []sprint.Note
	for _, n := range notes {
		if n.Type == sprint.NFriendNotWoken {
			woken = append(woken, n)
		}
	}
	require.Len(t, woken, 1, "one note for the pass")
	assert.Contains(t, woken[0].What, "this pass")
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		assert.Contains(t, woken[0].Primaries, id)
	}
	story := ta.ok("card s1-1")
	assert.Equal(t, 1, strings.Count(story, sprint.NFriendNotWoken), story)
	assert.Contains(t, story, "nova-bus send --as coordinator --to amy")
	ta.clean()
}
