package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A worker's RESULT.md that is not in the contract's shape fails the finish as before, and
// the finish's report now says what is wrong with it (the sample is main-report-schema-new-leg-2.w4.g1.e20's
// own RESULT.md, read from hetzner2 on 2026-10-13), so the next attempt does not guess.
func TestAChildWhoseResultIsNotInShapeReportsWhatIsWrongWithIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "results", "RESULT.md"), "RESULT: main-report-schema-new-leg sha=121f606fb9d9 tier: pro\nDONE\nAttempt 1 had already ported the change.\n")
	done := make(chan struct{})
	close(done)
	c := &nativeChild{card: "c1", logPath: filepath.Join(dir, "c1.native.log"), results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: done}
	r := c.Result()
	assert.False(t, r.Shaped)
	assert.Contains(t, r.Report, "RESULT.md shape fault: none of the six")
	assert.Contains(t, r.Report, "`head: <full sha>` on line 1")
}

// The RESULT.md native writes for a card it ended (an idle reap) is no worker's shape: the
// report keeps native's words and blames no shape.
func TestAChildNativeEndedIsNotBlamedForItsShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "results", "RESULT.md"), "RESULT: BLOCKED c1\nCARD IDLE task=c1 step=- idle=300s\nwritten-by: nova-swarm native (the card published no report of its own)\n")
	done := make(chan struct{})
	close(done)
	c := &nativeChild{card: "c1", logPath: filepath.Join(dir, "c1.native.log"), results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: done}
	r := c.Result()
	assert.False(t, r.Shaped)
	assert.NotContains(t, r.Report, "shape fault")
}

// A worker's RESULT.md with no report line, every other key and a body, is shaped, and its
// report is the body's first line (fixed-cpp-appends-and-deprecations-read-clean.w1.g9.e20).
func TestAChildWhoseResultHasNoReportLineFinishesShaped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "results", "RESULT.md"), "head: 03e2ea0c4e2f1e43ab9c021efd2eed4c4d8bd6c9\nbranch: sprint/c1.w1.g9.e20\nverdict: ok\ngate: go test ./x\noutput: -\n\n## Body\n\nThe cell was red for cpp.\nMore.\n")
	done := make(chan struct{})
	close(done)
	c := &nativeChild{card: "c1", logPath: filepath.Join(dir, "c1.native.log"), results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: done}
	r := c.Result()
	assert.True(t, r.Shaped)
	assert.Equal(t, "The cell was red for cpp.", r.Report)
	assert.Equal(t, "ok", r.Verdict)
	assert.Equal(t, "03e2ea0c4e2f1e43ab9c021efd2eed4c4d8bd6c9", r.Head)
}

// A read's RESULT.md may still be in an older shape and its report is the reader's own words:
// the shape fault is a work card's, and a read's report is left as the reader wrote it.
func TestAReadsReportIsNotPrefixedWithAShapeFault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "results", "RESULT.md"), "rev: abc\nverdict: ok\n## One line\nlooks right\n")
	done := make(chan struct{})
	close(done)
	c := &nativeChild{card: "c1", read: true, logPath: filepath.Join(dir, "c1.native.log"), results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: done}
	r := c.Result()
	assert.Equal(t, "looks right", r.Report)
}
