package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFriendBeatCarriesSpaceToTheRow(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")
	ta.pong("amy")
	out := ta.ok("friend beat amy --jobs-bytes 21474836480 --free-bytes 0 --free-inodes 123")
	assert.Contains(t, out, "jobs_bytes=21474836480")
	f := whereFriends(ta)["amy"]
	require.NotNil(t, f.Report)
	assert.Equal(t, int64(21474836480), *f.Report.JobsBytes)
	assert.Equal(t, int64(0), *f.Report.FreeBytes)
	assert.Equal(t, int64(123), *f.Report.FreeInodes)
	var view whereView
	ta.json("where", &view)
	row := view.Tables[sprint.Friends]["amy"]
	assert.Equal(t, "21474836480", row["jobs_bytes"])
	assert.Equal(t, "0", row["free_bytes"])
	assert.Equal(t, "123", row["free_inodes"])
	_, _, why := workerVerb([]string{"friend", "beat", "amy", "--jobs-bytes", "21474836480", "--free-bytes", "0", "--free-inodes", "123"})
	assert.Empty(t, why)
	ta.ok("friend beat amy --capacity-error measurement-unavailable")
	require.NotNil(t, whereFriends(ta)["amy"].Report)
	assert.Equal(t, "measurement-unavailable", whereFriends(ta)["amy"].Report.CapacityError)
	for _, bad := range []string{"--jobs-bytes -1", "--free-bytes invalid", "--free-inodes 1.5"} {
		code, _, errs := ta.do("friend beat amy " + bad)
		assert.Equal(t, 2, code)
		assert.Contains(t, errs, "wants")
	}
}
