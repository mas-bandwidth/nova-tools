package main

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
)

// The base-gate count lives in the store (found by hand, 2026-10-04: the server
// refused stream sprint-next 62 times from 12:04 PM because its base rowan/bus-rename failed
// the tree gate at its tip, and the stream never stopped: no judgment, where showed it
// merging with 5 queued, and resume refused it as not stopped). The lander's own record of a
// red base is its process's memory, which a hand land starts empty every run and the
// server's every start; the refusals are counted per stream and base on the stream's control
// card, the third stops the stream with one judgment naming the base, the gate and the first
// refusal, resume clears it, and a pass that merges resets the count. docs/SPEC-SPRINT.md
// section 8, land-base-gate-stops-stream.
func TestBaseGateRefusedThreeTimesStopsTheStreamWithAJudgment(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.files("the base's change", map[string]string{"bad.go": vetRed})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{"s1-1": r.card("s1-1", map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"}), "s1-2": r.card("s1-2", map[string]string{"NOTES.md": "fine too\n"})}
	r.queued(heads, "s1-1", "s1-2")
	// each pass a fresh process, as a hand land is and as the server is after a start: no
	// record of the base's earlier failures is held anywhere but the store
	land := func() (int, string) {
		r.a.baseGateCache, r.a.baseGateFails = nil, nil
		code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		return code, out + errs
	}
	ctl := func(field string) string {
		st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
		require.NoError(t, err)
		s, err := st.Load(context.Background(), []string{sprint.Merge}, nil)
		require.NoError(t, err)
		return s.StreamCtl("s1").F(field)
	}
	first := r.a.now().UTC().Format("15:04:05 MST")

	code, out := land()
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, "fails the tree gate at its tip")
	assert.Equal(t, "merging", r.streamState("s1"), "no stop on the first refusal")
	assert.Equal(t, "1", ctl(sprint.FieldBaseGateRefused), "the refusal is counted in the store")
	assert.Equal(t, "main", ctl(sprint.FieldBaseGateBase))
	r.after(sprint.BaseGateRetries[0])
	_, out = land()
	assert.Equal(t, "merging", r.streamState("s1"), out)
	assert.Equal(t, "2", ctl(sprint.FieldBaseGateRefused), "counted across passes, not in the lander's memory")
	r.after(sprint.BaseGateRetries[1])
	_, out = land()
	assert.Contains(t, out, "fact=base")
	assert.Equal(t, "stopped base", r.streamState("s1"), "the third refusal stops the stream")
	assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"), "no card is blamed")
	// a pass over a stopped stream raises nothing more
	r.after(sprint.BaseGateRetries[1])
	land()
	inbox := r.ok("inbox")
	assert.Equal(t, 1, strings.Count(inbox, sprint.NBaseRed), "one judgment: "+inbox)
	assert.Contains(t, inbox, "the base main fails its tree gate")
	assert.Contains(t, inbox, "go vet")
	assert.Contains(t, inbox, "first refused at "+first)

	// resume works, and the count starts again
	r.ok("resume --stream s1 --did 'the base is green again'")
	assert.Equal(t, "merging", r.streamState("s1"))
	assert.Equal(t, "", ctl(sprint.FieldBaseGateRefused), "resume clears the count")
	r.after(sprint.BaseGateRetries[1])
	land()
	assert.Equal(t, "1", ctl(sprint.FieldBaseGateRefused))

	// the base passes its gate again: the pass merges, and the count is reset
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the base's fix", map[string]string{"bad.go": "package main\n\nfunc bad() {}\n"})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	code, out = land()
	assert.Equal(t, 0, code, out)
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	assert.Equal(t, "", ctl(sprint.FieldBaseGateRefused), "a pass that merges resets the count")
	assert.Equal(t, "", ctl(sprint.FieldBaseGateBase))
}
