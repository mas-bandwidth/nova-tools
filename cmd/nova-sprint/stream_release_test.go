package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamSetReleaseAndWhereReleaseCLI tests the stream set and where --release CLI commands:
// 1. stream set <s> --release <name> records the release on the stream's control card.
// 2. where --release prints the cards left per release from the stream rows.
// 3. where --release <name> prints the cards left for that release.
// 4. where --json carries the releases map.
// (docs/SPEC-SPRINT.md section 11).
func TestStreamSetReleaseAndWhereReleaseCLI(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1:16 --coordinator lead")

	// Add streams with cards:
	// s1: 3 cards (release v1.0.0)
	// s2: 2 cards (release v1.0.0)
	// s3: 5 cards (release v1.2.0)
	ta.ok("add --stream s1 --count 3 --actor lead")
	ta.ok("add --stream s2 --count 2 --actor lead")
	ta.ok("add --stream s3 --count 5 --actor lead")

	// Set releases:
	out := ta.ok("stream set s1 s2 --release v1.0.0 --actor lead")
	assert.Contains(t, out, "STREAM-SET OK")
	assert.Contains(t, out, "MOVED stream s1 release v1.0.0")
	assert.Contains(t, out, "MOVED stream s2 release v1.0.0")

	out = ta.ok("stream set s3 --release v1.2.0 --actor lead")
	assert.Contains(t, out, "STREAM-SET OK")
	assert.Contains(t, out, "MOVED stream s3 release v1.2.0")

	// Test where --release (all releases):
	out = ta.ok("where --release")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "RELEASE v1.0.0 cards=5", lines[0])
	assert.Equal(t, "RELEASE v1.2.0 cards=5", lines[1])

	// Test where --release <name> (specific release):
	out = ta.ok("where --release v1.0.0")
	assert.Equal(t, "RELEASE v1.0.0 cards=5\n", out)

	out = ta.ok("where --release v1.2.0")
	assert.Equal(t, "RELEASE v1.2.0 cards=5\n", out)

	out = ta.ok("where --release v9.9.9")
	assert.Equal(t, "RELEASE v9.9.9 cards=0\n", out)

	// Test where --json:
	var w whereView
	ta.json("where", &w)
	require.NotNil(t, w.Releases)
	assert.Equal(t, int64(5), w.Releases["v1.0.0"])
	assert.Equal(t, int64(5), w.Releases["v1.2.0"])

	// Clear s2's release with default:
	out = ta.ok("stream set s2 --release default --actor lead")
	assert.Contains(t, out, "MOVED stream s2 release none")

	// Now v1.0.0 only has s1 (3 cards):
	out = ta.ok("where --release v1.0.0")
	assert.Equal(t, "RELEASE v1.0.0 cards=3\n", out)

	// Refusal: a non-coordinator cannot set release:
	code, _, errs := ta.do("stream set s1 --release v2.0.0 --actor worker")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "is the coordinator's alone")

	// Refusal: nonexistent stream:
	code, _, errs = ta.do("stream set nonexistent --release v2.0.0 --actor lead")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no stream nonexistent")
}
