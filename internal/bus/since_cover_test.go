package bus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The incremental check is driven from a change set rather than from a walk, and the unit
// tier reaches none of it: Scope.Mode, CheckSince and checkReceipts are the three
// functions a per-function coverage run names at zero. These tests pin Mode's two
// branches, CheckSince's main path and its unowned-lane finding, and checkReceipts both
// ways, all against the package's own roster and note helpers and no store.

// TestSinceCoverMode pins the output word for each of the two scopes a run can have.
func TestSinceCoverMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		scope Scope
		want  string
	}{
		{"a full walk says full", Scope{Full: true, Changed: 9}, "full"},
		{"an incremental run says since", Scope{From: "deadbeef", Changed: 2}, "since"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.scope.Mode(), "Mode() = %q, want %q", tc.scope.Mode(), tc.want)
		})
	}
}

// sinceCoverReceipts is the valid RECEIPTS line every main-path case resolves: its target
// is the id an INDEX entry in from-bo names, so idx.resolves answers without a note parse.
const sinceCoverReceipts = "2026-09-09T12:34:56Z bo-abcdef012345\n"

// sinceCoverBus is a bus with one indexed note from Bo to Ada, so CheckSince has a note
// to validate and a target checkReceipts can resolve.
func sinceCoverBus(t *testing.T) string {
	t.Helper()
	return writeBus(t, map[string]string{
		"from-bo/new.md": "From: Bo\nTo: Ada\nDate: Mon Sep  8 00:01:00 UTC 2026\nId: bo-abcdef012345\nSubject: new\n\nA question?\n",
		"from-bo/INDEX": IndexLine(IndexEntry{
			ID: "bo-abcdef012345", Path: "from-bo/new.md", Date: "2026-09-08T00:01:00Z",
			To: []string{"Ada"}, Lane: "from-bo",
		}) + "\n",
		"from-ada/RECEIPTS": sinceCoverReceipts,
	})
}

// TestSinceCoverCheckSinceMain pins CheckSince's main path: a changed note is validated
// against the catalogue with no finding, a changed RECEIPTS file is counted and resolved,
// and the stats name what the run looked at.
func TestSinceCoverCheckSinceMain(t *testing.T) {
	t.Parallel()
	root := sinceCoverBus(t)
	c, err := LoadConfig(root)
	require.NoError(t, err)
	idx, err := ReadIndex(root, c)
	require.NoError(t, err)

	ps, stats := CheckSince(root, c, idx, []string{"from-bo/new.md", "from-ada/RECEIPTS"}, CheckOptions{})
	assert.Empty(t, ps, "a valid note and receipt produced findings: %+v", ps)
	assert.Equal(t, CheckStats{Notes: 1, Lanes: 2, Receipts: 1}, stats,
		"stats = %+v, want one note, two lanes and one receipt", stats)
}

// TestSinceCoverCheckSinceRefuses pins CheckSince's refusal: a change set naming a lane no
// roster entry owns is one finding against the lane, and the note inside it is never
// parsed.
func TestSinceCoverCheckSinceRefuses(t *testing.T) {
	t.Parallel()
	root := writeBus(t, map[string]string{"from-zed/note.md": "not read\n"})
	c, err := LoadConfig(root)
	require.NoError(t, err)
	idx, err := ReadIndex(root, c)
	require.NoError(t, err)

	ps, stats := CheckSince(root, c, idx, []string{"from-zed/note.md"}, CheckOptions{})
	require.Len(t, ps, 1, "an unowned lane produced %+v, want exactly one finding", ps)
	assert.Equal(t, "from-zed", ps[0].Where)
	assert.Contains(t, ps[0].Reason, "owns this lane")
	assert.Equal(t, CheckStats{Lanes: 1}, stats, "an unowned lane is still a lane the run saw: %+v", stats)
}

// TestSinceCoverCheckReceipts pins checkReceipts both ways through CheckSince: a receipt
// line that parses and resolves is counted with no finding, and one whose stamp is not a
// UTC instant and whose target is on no note is refused by name.
func TestSinceCoverCheckReceipts(t *testing.T) {
	t.Parallel()
	root := sinceCoverBus(t)
	c, err := LoadConfig(root)
	require.NoError(t, err)
	idx, err := ReadIndex(root, c)
	require.NoError(t, err)
	ps, stats := CheckSince(root, c, idx, []string{"from-ada/RECEIPTS"}, CheckOptions{})
	assert.Empty(t, ps, "a receipt that resolves produced findings: %+v", ps)
	assert.Equal(t, 1, stats.Receipts, "a valid receipt line was not counted: %+v", stats)

	write(t, root, "from-ada/RECEIPTS", "not-a-stamp bo-000000000000\n")
	ps, stats = CheckSince(root, c, idx, []string{"from-ada/RECEIPTS"}, CheckOptions{})
	assert.Equal(t, 1, stats.Receipts, "the malformed line was not counted: %+v", stats)
	require.Len(t, ps, 2, "a bad stamp and a target on no note are two findings: %+v", ps)
	all := ps[0].Reason + "\n" + ps[1].Reason
	assert.Contains(t, all, "not a UTC stamp", "the bad stamp was not refused by name: %+v", ps)
	assert.Contains(t, all, "neither an id on this bus nor a note that exists", "the unknown target was not refused by name: %+v", ps)
}
