package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The inbox files are the record. A pass sends one status message for the cards
// it delivers, not one per card; a one-shot friend gets her per-card wake only
// for a single delivered card, and any other pass with a delivery gets the one
// pass notice (docs/FRIENDS.md, docs/SPEC-SPRINT.md section 1). A failed send is
// one line and one note, and the files stay.
func TestFriendSyncWakesOncePerPassInBatchMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, mode      string
		cards, messages int
		fail            bool
	}{
		{name: "default batch", cards: 3, messages: 1},
		{name: "explicit batch", mode: config.FriendModeBatch, cards: 3, messages: 1},
		{name: "one-shot single", mode: config.FriendModeOneShot, cards: 1, messages: 1},
		{name: "one-shot multi", mode: config.FriendModeOneShot, cards: 3, messages: 1},
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
			// Cards already on her row stay there after a mode change. Sync
			// reads the store row as it is now, not the mode at deal time.
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
			if tc.mode == config.FriendModeOneShot && tc.cards == 1 && len(sent) == 1 {
				m := sent[0]
				assert.Contains(t, m.Subject, " dealt: FRIEND-CARD DELIVERED")
				assert.Contains(t, m.Body, "BRIEF.md")
			}
			if (tc.mode != config.FriendModeOneShot || (tc.mode == config.FriendModeOneShot && tc.cards > 1)) && tc.cards > 0 && len(sent) == 1 {
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
					assert.NotContains(t, m.Subject, "s1-12.w1")
					assert.NotContains(t, m.Subject, "s1-13.w1")
				}
				inbox := filepath.Join(root, "amy-working", "inbox")
				assert.Contains(t, m.Body, inbox)
				assert.Contains(t, m.Body, "Read the inbox briefs and start; each STATUS line says where to push and where to report.")
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
				assert.Equal(t, 1, strings.Count(out, "FRIEND-CARD NOTE"))
				if len(failed) == 1 {
					assert.Equal(t, tc.cards, failed[0].Count)
					assert.Contains(t, failed[0].What, "pass")
					assert.NotContains(t, failed[0].What, "card s1-01.w1 dealt:")
					assert.Empty(t, failed[0].Primaries)
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

// A deal pass sends a friend one notice when it gave her a new card, and none
// otherwise: a pass that re-deals only cards she holds sends nothing, and a
// pass that deals her three new cards sends one notice naming three.
func TestADealThatAddsNoCardSendsNoNotice(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		mode string
	}{
		{name: "batch", mode: config.FriendModeBatch},
		{name: "one-shot", mode: config.FriendModeOneShot},
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
			for i := range 3 {
				id := fmt.Sprintf("s1-%02d", i+1)
				require.NoError(t, os.WriteFile(filepath.Join(briefs, id+".md"), []byte(passingBrief(id+": a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: only friend amy")), 0o644))
			}
			ta.ok("add --stream s1 --brief-dir " + briefs)
			ta.ok("start")
			ta.ok("tick")

			if tc.mode != "" {
				_, _, err = cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"mode": tc.mode}, "t")
				require.NoError(t, err)
			}

			var sent []bus.Message
			ta.a.bus = func(_ context.Context, m bus.Message, _ func(string)) error {
				sent = append(sent, m)
				return nil
			}

			// A pass that deals her three new cards sends one notice naming three.
			out := ta.ok("friend sync --root " + root)
			assert.Equal(t, 3, strings.Count(out, "FRIEND-CARD DELIVERED"))
			require.Len(t, sent, 1, "a pass that deals her three new cards sends one notice naming three")
			assert.Contains(t, sent[0].Subject, "cards dealt: 3 (")
			for i := range 3 {
				assert.Contains(t, sent[0].Subject, fmt.Sprintf("s1-%02d.w1", i+1))
			}

			// A pass that re-deals only cards she holds sends nothing.
			ta.ok("tick")
			ta.ok("friend sync --root " + root)
			assert.Len(t, sent, 1, "a pass that re-deals only cards she holds sends nothing")
			ta.clean()
		})
	}
}
