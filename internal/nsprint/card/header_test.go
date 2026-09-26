package card_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// TestCardPushStoresStreamAndDoneWhen (#2932): card push leaves the card's
// STREAM line on the record (stream, CARD.create's) and its DONE-WHEN line
// (done_when, ns_card_header in the same pipeline), so harvest's PR body and
// PR record carry them without the card file. A card with no STREAM line
// stores no stream; a repeat push writes nothing; a label conflict writes
// nothing.
func TestCardPushStoresStreamAndDoneWhen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	for _, tc := range []struct {
		label  string
		lines  []string
		stream string
	}{
		{"header-stream", []string{"STREAM: nova-sprint"}, "nova-sprint"},
		{"header-nostream", nil, ""},
	} {
		f := validCard(srv.URL + "/acme/public.git")
		f.label = tc.label
		body := withHeader(f, tc.lines...)
		res := card.Push(ctx, client, sprint, body)
		if res.Code != 0 {
			t.Fatalf("%s: exit %d stderr %q, want exit 0", tc.label, res.Code, res.Stderr)
		}
		// A card with no STREAM line has no stream field: read absent as "".
		str := func(v any) string { s, _ := v.(string); return s }
		got, err := client.HMGet(ctx, keyCard(tc.label), "stream", "done_when").Result()
		if err != nil || str(got[0]) != tc.stream || str(got[1]) != f.done {
			t.Fatalf("%s: stream, done_when = %v (%v), want %q and the DONE-WHEN line", tc.label, got, err, tc.stream)
		}
		// The same card again: exists, and the header fields are untouched.
		if res := card.Push(ctx, client, sprint, body); res.Code != 0 || !strings.Contains(res.Stdout, "place=exists") {
			t.Fatalf("%s repeat: exit %d stdout %q stderr %q", tc.label, res.Code, res.Stdout, res.Stderr)
		}
		// The label with another payload: a conflict that writes nothing.
		other := f
		other.done = "another sentence a test can fail"
		if res := card.Push(ctx, client, sprint, withHeader(other, "STREAM: elsewhere")); res.Code != 4 {
			t.Fatalf("%s conflict: exit %d stderr %q, want 4", tc.label, res.Code, res.Stderr)
		}
		again, _ := client.HMGet(ctx, keyCard(tc.label), "stream", "done_when").Result()
		if str(again[0]) != tc.stream || str(again[1]) != f.done {
			t.Fatalf("%s: a repeat or a conflict rewrote stream, done_when: %v", tc.label, again)
		}
	}
}
