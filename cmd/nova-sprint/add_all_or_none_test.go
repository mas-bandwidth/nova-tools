package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeBriefDir writes n passing briefs a00.md, a01.md ... into a new directory.
func writeBriefDir(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := 0; i < n; i++ {
		writeNeedsBrief(t, dir, fmt.Sprintf("a%02d", i), fmt.Sprintf("Fix a%02d.", i), "")
	}
	return dir
}

// addLine is the result line of an add: the one that starts with the verb's token.
func addLine(out, errs string) string {
	for _, l := range strings.Split(out+errs, "\n") {
		if strings.HasPrefix(l, "ADD ") {
			return l
		}
	}
	return ""
}

// add --brief-dir of 10 briefs adds the 10 cards, and says ADD OK moved=10.
func TestAddBriefDirOfTenAddsTen(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := writeBriefDir(t, 10)
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 0, code, out+errs)
	require.Contains(t, addLine(out, errs), "ADD OK moved=10 ")
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, int64(10), w.All)
	ta.clean()
}

// One bad brief among 10 adds none of the 10, and the result line names the file.
func TestAddBriefDirOneBadAmongTenAddsNone(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := writeBriefDir(t, 10)
	bad := filepath.Join(dir, "a05.md")
	require.NoError(t, os.WriteFile(bad, []byte("handle the empty case\n"), 0o600))
	before := ta.applies()
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 2, code)
	assert.NotContains(t, out, "MOVED")
	line := addLine(out, errs)
	assert.True(t, strings.HasPrefix(line, "ADD REFUSED "+bad+": "), "result line %q", line)
	assert.Equal(t, before, ta.applies(), "a refused add wrote")
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, int64(0), w.All)
}

// A brief the table refuses (a Needs naming no card) among 10 adds none, and the
// result line names the file, never FAIL.
func TestAddBriefDirOneRefusedByTheTableAddsNone(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := writeBriefDir(t, 10)
	bad := writeNeedsBrief(t, dir, "a05", "Fix a05.", "ghost")
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 1, code)
	line := addLine(out, errs)
	assert.True(t, strings.HasPrefix(line, "ADD REFUSED "+bad+": "), "result line %q", line)
	assert.Contains(t, line, "ghost")
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, int64(0), w.All)
}

// The write committed and the display cells then failed to sync (the 2026-10-01
// 07:45 failure: ADD FAIL moved=10 changed=no over ten cards that were in the
// table): the result line says what the table holds, ADD OK moved=10, and the
// exit is 0; the sync's own error is still said, on its own line.
func TestAddResultLineMatchesTheTableWhenTheDisplaySyncFails(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := writeBriefDir(t, 10)
	var committed atomic.Bool
	ta.m.Fail = func(point string) error {
		if point == "release" {
			committed.Store(true)
		}
		if committed.Load() && strings.HasPrefix(point, "readset") {
			return errors.New("the display cells cannot be read")
		}
		return nil
	}
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	ta.m.Fail = nil
	line := addLine(out, errs)
	var w whereView
	ta.json("where", &w)
	require.Equal(t, int64(10), w.All, "the table holds the cards")
	assert.NotContains(t, line, "FAIL", "a FAIL over cards in the table: %s", line)
	assert.Contains(t, line, "ADD OK moved=10 ")
	assert.NotContains(t, line, "changed=no")
	assert.Equal(t, 0, code, out+errs)
	assert.Contains(t, errs, "display cells")
}
