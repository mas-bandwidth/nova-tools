package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lazyCount.fail is the cleaner's record of an old entry that would not go: it names the
// first failure once and counts every one, so one round's line is stable whatever follows.
func TestLazycleanCoverFail(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		fails      [][2]string
		wantFailed int
		wantWhy    string
	}{
		{name: "no failure records nothing", wantFailed: 0, wantWhy: ""},
		{name: "the first failure records its path and reason", fails: [][2]string{{"/slots/a", "permission denied"}}, wantFailed: 1, wantWhy: "/slots/a: permission denied"},
		{name: "a later failure keeps the first reason", fails: [][2]string{{"/slots/a", "permission denied"}, {"/slots/b", "is a directory"}}, wantFailed: 2, wantWhy: "/slots/a: permission denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var c lazyCount
			for _, f := range tc.fails {
				c.fail(f[0], errors.New(f[1]))
			}
			assert.Equal(t, tc.wantFailed, c.failed)
			assert.Equal(t, tc.wantWhy, c.why)
			if tc.wantFailed == 0 {
				assert.Empty(t, c.failures(), "no failure says nothing")
			} else {
				assert.Contains(t, c.failures(), fmt.Sprintf("%d not removed", tc.wantFailed))
			}
		})
	}
}

// noteOldFailed marks a path whose listing or removal failed: the set is made on the first
// path, a marked path is left marked, and later paths join the same set.
func TestLazycleanCoverNoteOldFailed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		paths []string
	}{
		{name: "a first path allocates the set", paths: []string{"/slots/a"}},
		{name: "a marked path is set once", paths: []string{"/slots/a", "/slots/a"}},
		{name: "later paths join the same set", paths: []string{"/slots/a", "/slots/b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &nativeRunner{}
			for _, path := range tc.paths {
				r.noteOldFailed(path)
			}
			require.NotNil(t, r.oldFailed)
			want := map[string]bool{}
			for _, path := range tc.paths {
				want[path] = true
			}
			assert.Equal(t, want, r.oldFailed)
		})
	}
}

// cleanOld's own refusal: a root whose listing fails is marked through noteOldFailed and
// counted through fail, and the next round refuses to touch the marked root again, so a
// broken root is not listed every lazyEvery.
func TestLazycleanCoverACleanOldListingRefusalIsNotRetried(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	notDir := filepath.Join(root, "slots")
	write(t, notDir, "a file where the slots directory should be\n")
	r := &nativeRunner{root: root, slots: notDir}
	r.Epoch(5)

	c := r.cleanOld(time.Now())
	assert.Equal(t, 1, c.failed)
	assert.Contains(t, c.why, notDir)
	assert.NotEmpty(t, c.failures())
	assert.True(t, r.oldFailed[notDir], "the root whose listing failed is marked")

	next := r.cleanOld(time.Now())
	assert.Zero(t, next.failed, "a marked root is not tried again")
	assert.Zero(t, next.removed)
}
