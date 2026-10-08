package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamsListsEveryStreamByRepoInOneCall pins the streams verb (the card's STOP):
// three streams over two repositories, one stream mixing both. One add per card records
// the REPO: and BASE: of its brief on the stream's control card, and one `nova-sprint
// streams --cards` call lists every stream with its repositories, bases, release, open
// and landed counts and every card's id, state, tier, title and needs; a stream whose
// cards name more than one repository keeps them all and is named as a finding.
func TestStreamsListsEveryStreamByRepoInOneCall(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")

	widgets := writeBrief(t, "RESULT: c sha=0123456789ab\nREPO: acme/widgets\nBASE: sprint/base\n\nTHE TASK. Fix the widget parser.")
	gears := writeBrief(t, "RESULT: c sha=0123456789ab\nREPO: acme/gears\nBASE: sprint/base\n\nTHE TASK. Fix the gear cutter.")

	// s1: two cards over acme/widgets, s1-2 needs s1-1
	ta.ok("add --stream s1 s1-1 --one --brief-file " + widgets)
	ta.ok("add --stream s1 s1-2 --one --brief-file " + widgets + " --needs s1-1")
	// s2: one card over acme/gears
	ta.ok("add --stream s2 s2-1 --one --brief-file " + gears)
	// s3: one card over each repository
	ta.ok("add --stream s3 s3-1 --one --brief-file " + widgets)
	ta.ok("add --stream s3 s3-2 --one --brief-file " + gears)
	// s2 belongs to the next release
	ta.ok("stream set s2 --release v1.0.0 --actor coordinator")

	out := ta.ok("streams --cards")
	// every stream is one STREAM line with its repos, bases, release, open and landed
	require.Contains(t, out, "STREAM s1 repos=acme/widgets bases=sprint/base release=- open=2 landed=0")
	require.Contains(t, out, "STREAM s2 repos=acme/gears bases=sprint/base release=v1.0.0 open=1 landed=0")
	require.Contains(t, out, "STREAM s3 repos=acme/gears,acme/widgets bases=sprint/base release=- open=2 landed=0")
	// the mixed stream is the finding
	assert.Contains(t, out, "NOTE stream s3 names more than one repository (acme/gears, acme/widgets)")
	// every card, with its state, tier, title and needs
	assert.Contains(t, out, "CARD s1-1 stream=s1 state=ready tier=- title=Fix the widget parser needs=-")
	assert.Contains(t, out, "CARD s1-2 stream=s1 state=waiting tier=- title=Fix the widget parser needs=s1-1")
	assert.Contains(t, out, "CARD s3-2 stream=s3 state=ready tier=- title=Fix the gear cutter needs=-")
	// one call: the whole listing is this one command's output
	assert.Equal(t, 3, strings.Count(out, "\nSTREAM "))

	// --repo narrows to the streams recording it; a mixed stream is named by both
	out = ta.ok("streams --repo acme/gears")
	assert.Contains(t, out, "STREAM s2 ")
	assert.Contains(t, out, "STREAM s3 ")
	assert.NotContains(t, out, "STREAM s1 ")

	// --release narrows to the streams of a release
	out = ta.ok("streams --release v1.0.0")
	assert.Contains(t, out, "STREAM s2 ")
	assert.NotContains(t, out, "STREAM s1 ")

	// --json is the same value
	var v streamsView
	ta.json("streams", &v)
	require.Len(t, v.Streams, 3)
	byName := map[string]streamsRow{}
	for _, s := range v.Streams {
		byName[s.Stream] = s
	}
	assert.Equal(t, []string{"acme/widgets"}, byName["s1"].Repos)
	assert.Equal(t, []string{"acme/gears", "acme/widgets"}, byName["s3"].Repos)
	assert.Equal(t, []string{"s3"}, v.Mixed)

	// drop and hold take --repo, and refuse without --expect
	code, _, errs := ta.do("drop --repo acme/gears --reason obsolete")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--expect")
	code, _, errs = ta.do("hold --repo acme/gears --reason 'the gears solver is out'")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--expect")

	// with the count they printed, one call acts on every stream the repository names
	assert.Contains(t, ta.ok("hold --repo acme/gears --expect 2 --reason 'the gears work is paused'"), "HOLD OK")
	assert.Contains(t, ta.ok("unhold --repo acme/gears --expect 2"), "UNHOLD OK")
	out = ta.ok("drop --repo acme/gears --expect 2 --reason 'not for the next release'")
	assert.Contains(t, out, "DROP OK")
	out = ta.ok("streams")
	assert.Contains(t, out, "STREAM s2 repos=acme/gears bases=sprint/base release=v1.0.0 open=0 landed=0")
	assert.Contains(t, out, "STREAM s3 repos=acme/gears,acme/widgets bases=sprint/base release=- open=0 landed=0")
}
