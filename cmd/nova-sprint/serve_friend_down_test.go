package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A friend's daemon beats down through the sprint's server while her harness is at its
// limit (friend beat <me> --until <RFC3339> --reason <text>, limits-mean-down-w-r7.w1~15):
// the server runs it, her row reads down with the pair, and a beat without --until ends
// that word: her row reads down then only for want of her session's evidence, a beat being
// none (presence-from-session-only-wb-t-r5.w1~15). An --until that is no time and a --reason that is empty or more than one line
// are refused, exit 2, and change nothing.
func TestTheServerTakesAFriendsDownBeat(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, "nova-sprint init --readers reader-a,reader-b --members m1:2")
	r.a.friends = friendRows("amy")
	r.boss("nova-sprint friend sync --root " + t.TempDir())

	until := r.a.now().Add(time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	active := r.a.now().UTC().Truncate(time.Second).Format(time.RFC3339)
	res := r.one("friend", "beat", "amy", "--until", until, "--reason", "harness limit: You've hit your usage limit", "--active", active)
	require.Equal(t, 0, res.Code, res.Stderr)
	assert.Contains(t, res.Stdout, "FRIEND-BEAT OK amy")
	assert.Contains(t, res.Stdout, " down=true until="+until)
	f := r.friendRow("amy")
	assert.Equal(t, "down", f.Status, "a fresh beat that says down is down")
	require.NotNil(t, f.Report)
	assert.Equal(t, until, f.Report.Until.Format(time.RFC3339))
	assert.Equal(t, "harness limit: You've hit your usage limit", f.Report.Reason)

	for name, argv := range map[string][]string{
		"an until that is no time": {"friend", "beat", "amy", "--until", "soon", "--reason", "limit"},
		"an until with no value":   {"friend", "beat", "amy", "--reason", "limit", "--until"},
		"an empty reason":          {"friend", "beat", "amy", "--until", until, "--reason", ""},
		"a reason of two lines":    {"friend", "beat", "amy", "--until", until, "--reason", "limit\nFRIEND-BEAT OK bob"},
		"a second until":           {"friend", "beat", "amy", "--until", until, "--until", until, "--reason", "limit"},
	} {
		res := r.one(argv...)
		assert.Equal(t, 2, res.Code, name)
		assert.Contains(t, res.Stderr, "nothing was changed", name)
	}

	res = r.one("friend", "beat", "amy")
	require.Equal(t, 0, res.Code, res.Stderr)
	assert.NotContains(t, res.Stdout, "down=true")
	f = r.friendRow("amy")
	assert.Equal(t, "down", f.Status, "a beat is no evidence: she is up only on her session's")
	assert.Contains(t, f.Evidence, "no session evidence", "a beat without --until ends the down word")
	assert.NotContains(t, f.Evidence, "her beat says down")
}

// friendRow is the named friend's row as where --json gives it, read on the server's line.
func (r *serverRig) friendRow(name string) store.FriendRow {
	r.t.Helper()
	var w struct {
		Friends []store.FriendRow `json:"friends"`
	}
	require.NoError(r.t, json.Unmarshal([]byte(r.boss("nova-sprint where --json")), &w))
	for _, f := range w.Friends {
		if f.Name == name {
			return f
		}
	}
	r.t.Fatalf("where --json has no friend %s", name)
	return store.FriendRow{}
}
