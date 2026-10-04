package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coverRoster loads the roster printSendDraft resolves recipients against, from a
// participants.json in the test's own temp dir: files only, no store and no network.
func coverRoster(t *testing.T) *bus.Config {
	t.Helper()
	dir := t.TempDir()
	raw := `{"participants":[` +
		`{"name":"Ada","lane":"from-ada","git_name":"Ada","git_email":"ada@example.com"},` +
		`{"name":"Bo","lane":"from-bo","git_name":"Bo","git_email":"bo@example.com"},` +
		`{"name":"Dana","lane":"from-dana","git_name":"Dana","git_email":"dana@example.com"},` +
		`{"name":"Rowan"}]}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, bus.ConfigName), []byte(raw), 0o644))
	c, err := bus.LoadConfig(dir)
	require.NoError(t, err)
	return c
}

// TestSendCoverIsBusIDMintsOneShape pins isBusID's shape: a sender slug, one hyphen, and
// twelve lower-case hex digits. Every refusal row is a token one byte away from the shape
// the tool mints, which a looser matcher would send out as an id.
func TestSendCoverIsBusIDMintsOneShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		tok  string
		want bool
	}{
		{"the minted shape", "ada-0123456789ab", true},
		{"a hyphenated sender slug", "from-the-west-bench-0123456789ab", true},
		{"no hyphen", "0123456789ab", false},
		{"no sender half", "-0123456789ab", false},
		{"eleven hex digits", "ada-0123456789a", false},
		{"thirteen hex digits", "ada-0123456789abc", false},
		{"upper-case hex", "ada-0123456789AB", false},
		{"a digit past the hex alphabet", "ada-0123456789ag", false},
		{"the hyphen last", "ada-0123456789ab-", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isBusID(tc.tok), "isBusID(%q)", tc.tok)
		})
	}
}

// TestSendCoverPrintSendDraftFramesTheShapedNote pins `send --dry-run`'s output: the
// summary line naming every field (recipients resolved against the roster, the date
// RFC3339, the rendered length), then the note verbatim between two id lines, and the
// no-Re branch that prints re=none. A fixed now, a bytes.Buffer, a roster on disk: no
// clock, no subprocess, no store.
func TestSendCoverPrintSendDraftFramesTheShapedNote(t *testing.T) {
	t.Parallel()
	c := coverRoster(t)
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	cases := []struct {
		name, draft, id, path, wantLine1, wantNote string
	}{
		{
			name: "the shaped note with a Re line",
			draft: "From: Ada\n" +
				"To: Bo; Dana\n" +
				"Cc: Rowan\n" +
				"Date: 2026-03-04T05:06Z\n" +
				"Re: bo-0123456789ab\n" +
				"Subject: Covering the dry-run frame\n" +
				"\n" +
				"The body as written.\n",
			id:   "ada-0123456789ab",
			path: "from-ada/20260304T0506-cover-0123456789ab.md",
			wantLine1: "SEND DRAFT id=ada-0123456789ab path=from-ada/20260304T0506-cover-0123456789ab.md " +
				"to=2 cc=1 re=bo-0123456789ab subject=Covering\\x20the\\x20dry-run\\x20frame " +
				"date=2026-03-04T05:06:07Z bytes=156",
			wantNote: "From: Ada\n" +
				"To: Bo; Dana\n" +
				"Cc: Rowan\n" +
				"Date: 2026-03-04T05:06Z\n" +
				"Id: ada-0123456789ab\n" +
				"Re: bo-0123456789ab\n" +
				"Subject: Covering the dry-run frame\n" +
				"\n" +
				"The body as written.\n",
		},
		{
			name: "a note with no Re and no Cc prints re=none",
			draft: "From: Ada\n" +
				"To: Bo\n" +
				"Date: 2026-03-04T05:06Z\n" +
				"Subject: The other frame\n" +
				"\n" +
				"A shorter body.\n",
			id:   "ada-9abcdef01234",
			path: "from-ada/20260304T0506-other-9abcdef01234.md",
			wantLine1: "SEND DRAFT id=ada-9abcdef01234 path=from-ada/20260304T0506-other-9abcdef01234.md " +
				"to=1 cc=0 re=none subject=The\\x20other\\x20frame " +
				"date=2026-03-04T05:06:07Z bytes=104",
			wantNote: "From: Ada\n" +
				"To: Bo\n" +
				"Date: 2026-03-04T05:06Z\n" +
				"Id: ada-9abcdef01234\n" +
				"Subject: The other frame\n" +
				"\n" +
				"A shorter body.\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n, err := bus.ParseNote(tc.path, tc.draft)
			require.NoError(t, err, "the draft must parse; printSendDraft receives a prepared note")
			n.Header.ID = tc.id
			require.Equal(t, tc.wantNote, n.Render(), "Render changed under the test; the frame below pins it verbatim")
			var out bytes.Buffer
			printSendDraft(&out, bus.Prepared{Note: n, Path: tc.path}, c, now)
			want := tc.wantLine1 + "\n" +
				"SEND DRAFT id=" + tc.id + "\n" +
				tc.wantNote +
				"SEND DRAFT END id=" + tc.id + "\n"
			assert.Equal(t, want, out.String(), "printSendDraft's dry-run frame")
		})
	}
}
