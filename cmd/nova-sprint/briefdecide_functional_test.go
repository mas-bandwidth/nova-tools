//go:build functional

package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// A card's end attaches to the brief decision it stores: a drop attaches dropped with
// its reason, a landing attaches landed at its attempt; a card that stores no decision
// attaches nothing (decide's TestAttachBriefsAttachesByTheExactOp: an op the record
// lacks is named, never matched to another decision).
func TestACardsEndAttachesToItsBrief(t *testing.T) {
	t.Parallel()
	ta, _, record := briefTestApp(t, "")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a1", "Fix a1. converges=0.8", "")
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("add --stream s1 --count 1")
	out := ta.ok("drop a1 s1-1 --reason obsolete")
	assert.NotContains(t, out, "brief decision")
	assert.Equal(t, map[string]string{"a1": "dropped: obsolete"}, endsOf(t, record))

	r := newLandRig(t)
	env := r.a.getenv
	r.a.getenv = func(k string) string {
		if k == decide.JevSecret {
			return "k-test"
		}
		return env(k)
	}
	r.a.decideBackend = func(string) decide.Backend { return &briefBackend{} }
	r.a.briefBar = func(context.Context) (string, error) { return "", nil }
	r.a.briefRecord = func() (string, error) { return record, nil }
	r.ok("add --stream s2 s2-1 s2-2 --brief-file " + writeNeedsBrief(t, t.TempDir(), "x", "Do it. converges=0.6", ""))
	r.queued(map[string]string{"s2-1": r.head("s2-1", "main", "one.txt", "one\n"), "s2-2": r.head("s2-2", "main", "two.txt", "two\n")}, "s2-1", "s2-2")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	require.Equal(t, 0, code, out+errs)
	assert.NotContains(t, out, "brief decision")
	assert.Equal(t, map[string]string{"a1": "dropped: obsolete", "s2-1": "landed: landed at attempt 1", "s2-2": "landed: landed at attempt 1"}, endsOf(t, record))
}
