//go:build functional

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServerReviewLandingSetupFailureIsVisible(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
	var out bytes.Buffer
	code := r.a.landRound(context.Background(), "mem:0", []string{"--repo-dir", filepath.Join(t.TempDir(), "missing"), "--base", "main"}, &out)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out.String(), "not a directory", "a pre-batch land refusal must not disappear from the loop log")
}
