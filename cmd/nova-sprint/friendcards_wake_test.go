package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The inbox files are the record; docs/FRIENDS.md limits a batch friend's
// courtesy wake to one message for the pass, even when the send fails.
func TestFriendSyncWakesOncePerPassInBatchMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, mode      string
		cards, messages int
		fail            bool
	}{
		{name: "default batch", cards: 3, messages: 1},
		{name: "explicit batch", mode: config.FriendModeBatch, cards: 3, messages: 1},
		{name: "one-shot", mode: config.FriendModeOneShot, cards: 3, messages: 3},
		{name: "no delivery"},
		{name: "failed batch wake", cards: 3, messages: 1, fail: true},
		{name: "bounded ids", cards: 13, messages: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta, cfg := friendApp(t)
			_, err := cfg.Insert(context.Background(), config.KindFriend, config.Row{Name: "amy", Fields: map[string]string{"width": "16", "tiers": "flash"}}, "t")
			require.NoError(t, err)
			root := t.TempDir()
			ta.ok("friend sync --root " + root)
			ta.beatUp("amy")
			briefs := t.TempDir()
			for i := range tc.cards {
				id := fmt.Sprintf("s1-%02d", i+1)
				require.NoError(t, os.WriteFile(filepath.Join(briefs, id+".md"), []byte(passingBrief(id+": a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: only friend amy")), 0o644))
			}
			if tc.cards > 0 {
				ta.ok("add --stream s1 --brief-dir " + briefs)
				ta.ok("start")
				ta.ok("tick")
			}
			// The cards already on her row stay delivered after a mode change;
			// sync reads the current store row, rather than the mode at deal time.
			if tc.mode != "" {
				_, _, err = cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"mode": tc.mode}, "t")
				require.NoError(t, err)
			}
			var sent []bus.Message
			ta.a.bus = func(_ context.Context, m bus.Message, _ func(string)) error {
				sent = append(sent, m)
				if tc.fail {
					return errors.New("injected send failure")
				}
				return nil
			}
			out := ta.ok("friend sync --root " + root)
			assert.Len(t, sent, tc.messages)
			assert.Equal(t, tc.cards, strings.Count(out, "FRIEND-CARD DELIVERED"))
			for i := range tc.cards {
				_, err := os.Stat(filepath.Join(root, "amy-working", "inbox", fmt.Sprintf("s1-%02d.w1", i+1), "BRIEF.md"))
				assert.NoError(t, err, "the delivered file stands")
			}
			if tc.mode != config.FriendModeOneShot && tc.cards > 0 && len(sent) == 1 {
				m := sent[0]
				assert.Equal(t, bus.KindStatus, m.Kind)
				assert.Equal(t, "coordinator", m.From)
				assert.Equal(t, []string{"amy"}, m.To)
				assert.Contains(t, m.Subject, fmt.Sprintf("cards dealt: %d (", tc.cards))
				for i := range min(tc.cards, 10) {
					assert.Contains(t, m.Subject, fmt.Sprintf("s1-%02d.w1", i+1))
				}
				if tc.cards > 10 {
					assert.Contains(t, m.Subject, "and 3 more")
					assert.NotContains(t, m.Subject, "s1-11.w1")
				}
				assert.Contains(t, m.Body, filepath.Join(root, "amy-working", "inbox"))
				assert.Contains(t, m.Body, "Read")
			}
			notes, _, err := ta.m.NotesSince(context.Background(), "", 100)
			require.NoError(t, err)
			var failed []sprint.Note
			for _, n := range notes {
				if n.Type == sprint.NFriendNotWoken {
					failed = append(failed, n)
				}
			}
			if tc.fail {
				assert.Len(t, failed, 1)
				assert.Equal(t, 1, strings.Count(out, "injected send failure"))
				if len(failed) == 1 {
					assert.Contains(t, failed[0].What, "pass")
				}
			} else {
				assert.Empty(t, failed)
			}
			ta.ok("friend sync --root " + root)
			assert.Len(t, sent, tc.messages, "a pass with no new files has no wake")
			ta.clean()
		})
	}
}
