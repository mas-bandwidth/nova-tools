package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// stream set <s> --release <name> and where --release <name> end to end on the
// twin (the coordinator's list of 2026-10-04, item 20; docs/SPEC-SPRINT.md
// section 11): the release is written on each stream's control card, where
// --release prints the release's streams and the cards left in each, and their
// sum, in text and JSON; none takes a stream out; a release no stream is in is
// refused naming the releases there are, and a stream set naming nothing to set
// is refused; a clear starts the next epoch with none.
func TestWhereReleaseCountsTheCardsLeftOfEachStreamOfTheRelease(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.ok("add --stream s2 --count 2")
	ta.ok("add --stream s3 --count 1")

	out := ta.ok("stream set s1 s2 --release v1.2.0")
	assert.Contains(t, out, "stream s1 release v1.2.0")
	assert.Contains(t, out, "stream s2 release v1.2.0")
	ta.ok("stream set s3 --release nova-sprint-v1.0.0 --read-tier pro")

	assert.Equal(t, "RELEASE v1.2.0  left=5  streams=2\n  s1  left=3\n  s2  left=2\n", ta.ok("where --release v1.2.0"))
	var rel sprint.ReleaseCount
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --release nova-sprint-v1.0.0 --json")), &rel))
	assert.Equal(t, sprint.ReleaseCount{Release: "nova-sprint-v1.0.0", Left: 1, Streams: []sprint.ReleaseStream{{Stream: "s3", Left: 1}}}, rel)

	code, _, errs := ta.do("where --release v9")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "no stream is in release v9 (the releases: nova-sprint-v1.0.0, v1.2.0)")

	ta.ok("stream set s2 --release none")
	assert.Equal(t, "RELEASE v1.2.0  left=3  streams=1\n  s1  left=3\n", ta.ok("where --release v1.2.0"))

	code, _, errs = ta.do("stream set s1")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--read-tier <flash|pro|default> or --release <name|none>")
	code, _, errs = ta.do("where --release v1.2.0 --all")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "give it without --all and --cards")

	// a clear starts the next epoch with no release, as with every control card's field
	ta.ok("clear --confirm sprint")
	code, _, errs = ta.do("where --release v1.2.0")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "no stream is in release v1.2.0 (no stream is in any release)")
}
