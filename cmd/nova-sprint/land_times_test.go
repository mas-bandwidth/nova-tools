package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// landTwo queues a two-card stream on main in the rig, its heads by id.
func landTwo(r *landRig) map[string]string {
	r.t.Helper()
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{}
	for _, id := range []string{"s1-1", "s1-2"} {
		heads[id] = r.head(id, "main", id+".txt", id+"\n")
	}
	r.queued(heads, "s1-1", "s1-2")
	return heads
}

// The landing's fetch is one exchange: the base to its remote-tracking ref and the
// batch's heads by their whole ids, in queue order, and nothing else (no refspec of
// every branch, no tags).
func TestLandFetchIsTheBaseAndTheHeadsByIDInOneCommand(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	heads := landTwo(r)
	trace := filepath.Join(r.dir, "git-trace")
	r.a.gitEnv = append(slices.Clone(r.env), "GIT_TRACE="+trace)
	out := r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
	b, err := os.ReadFile(trace)
	require.NoError(t, err)
	var fetches []string
	for _, line := range strings.Split(string(b), "\n") {
		if _, cmd, ok := strings.Cut(line, "trace: built-in: git fetch "); ok {
			fetches = append(fetches, strings.ReplaceAll(cmd, "'", ""))
		}
	}
	assert.Equal(t, []string{"--no-tags origin +refs/heads/main:refs/remotes/origin/main " + heads["s1-1"] + " " + heads["s1-2"]}, fetches)
}

// A landed batch's line and its --json item carry each step's seconds: the fetch, the
// merges, the check, the queue read again, the push and the report.
func TestLandSaysHowLongEachStepOfALandedBatchTook(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	landTwo(r)
	out := r.ok("land --repo-dir " + r.clone + " --base main --check true")
	assert.Regexp(t, regexp.MustCompile(`LAND OK stream=s1 cards=2 base=main .* fetch=\d+\.\ds merge=\d+\.\ds check=\d+\.\ds queue=\d+\.\ds push=\d+\.\ds report=\d+\.\ds`), out)

	r = newLandRig(t)
	landTwo(r)
	var v struct {
		Items []landBatch `json:"items"`
	}
	r.json("land --repo-dir "+r.clone+" --base main", &v)
	require.Len(t, v.Items, 1)
	require.NotNil(t, v.Items[0].Times, "a landed batch's item has its times")
	assert.Equal(t, "ok", v.Items[0].Status)
	assert.Positive(t, v.Items[0].Times.Fetch)
	assert.Positive(t, v.Items[0].Times.Merge)
	assert.Positive(t, v.Items[0].Times.Push)
	assert.Positive(t, v.Items[0].Times.Report)
}
