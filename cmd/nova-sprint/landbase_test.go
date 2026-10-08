package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Each land pass re-checks the tip of a base that stopped a stream, and a green tip resumes it
// by rule (docs/SPEC-SPRINT.md section 8, v11-base-red-auto-resume-now; the coordinator,
// 2026-10-04: eight streams resumed by hand once the base was fixed). Two streams land on a
// red main: the first to meet it three times stops with the one judgment naming the failing
// test, the other is refused under it and never stops. The base is fixed: the next pass finds
// it green and records it, the other stream lands in the same pass, and the tick resumes the
// stopped one by rule, which then lands. On a twin repository and the twin store, each pass a
// fresh process as a hand land is.
func TestALandPassFindsTheBaseGreenAndItsStreamResumesByRule(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.files("the base's red test", map[string]string{"internal/docs/port_test.go": "package docs\n\nimport \"testing\"\n\nfunc TestPortInUse(t *testing.T) { t.Fatal(\"bind: address already in use\") }\n"})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 1 --one")
	r.ok("add --stream s2 --count 1 --one")
	heads := map[string]string{"s1-1": r.card("s1-1", map[string]string{"one.go": "package main\n\nfunc one() {}\n"}), "s2-1": r.card("s2-1", map[string]string{"NOTES.md": "two\n"})}
	r.queued(heads, "s1-1", "s2-1")
	land := func() string {
		r.a.baseGateCache, r.a.baseGateFails = nil, nil
		_, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		return out + errs
	}
	land()
	r.after(sprint.BaseGateRetries[0])
	land()
	r.after(sprint.BaseGateRetries[1])
	out := land()
	assert.Equal(t, "stopped base", r.streamState("s1"), out)
	assert.Equal(t, "merging", r.streamState("s2"), "refused under the judgment, never stopped: "+out)
	inbox := r.ok("inbox")
	assert.Equal(t, 1, strings.Count(inbox, sprint.NBaseRed), "one judgment: "+inbox)
	assert.Contains(t, inbox, "TestPortInUse")

	// still red: the pass re-checks it and resumes nothing
	r.after(sprint.BaseGateRetries[1])
	out = land()
	assert.Contains(t, out, "the base main still fails its tree gate")
	r.ok("start")
	r.ok("tick --answer-rules")
	assert.Equal(t, "stopped base", r.streamState("s1"))

	// the base is fixed: the pass finds it green, and the other stream lands with it
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the base's fix", map[string]string{"internal/docs/port_test.go": "package docs\n\nimport \"testing\"\n\nfunc TestPortInUse(t *testing.T) {}\n"})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	out = land()
	assert.Contains(t, out, "the base main passes its tree gate again at")
	assert.Contains(t, out, "LAND OK stream=s2 cards=1 base=main")
	assert.Equal(t, map[string]string{"s2-1": "landed/merged"}, r.places("s2-1"), out)
	assert.Equal(t, "stopped base", r.streamState("s1"), "the rule resumes it, at the tick")

	r.ok("tick --answer-rules")
	assert.Equal(t, "merging", r.streamState("s1"), "resumed by rule")
	assert.Contains(t, r.ok("log"), "answered by rule base-gate: the base main passes its tree gate again at")
	out = land()
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged"}, r.places("s1-1"), out)
}

// A pass with every stream stopped still re-checks the base (the coordinator, 2026-10-05: a
// stopped stream gets no land pass, so a pass that only walked its streams never saw the base
// turn green). The one stream stops on a red main; the base is fixed; a pass with nothing to
// land finds it green, and the tick resumes the stream by rule.
func TestALandPassWithEveryStreamStoppedFindsTheBaseGreen(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.files("the base's red test", map[string]string{"internal/docs/port_test.go": "package docs\n\nimport \"testing\"\n\nfunc TestPortInUse(t *testing.T) { t.Fatal(\"bind: address already in use\") }\n"})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 1 --one")
	heads := map[string]string{"s1-1": r.card("s1-1", map[string]string{"one.go": "package main\n\nfunc one() {}\n"})}
	r.queued(heads, "s1-1")
	land := func() string {
		r.a.baseGateCache, r.a.baseGateFails = nil, nil
		_, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		return out + errs
	}
	land()
	r.after(sprint.BaseGateRetries[0])
	land()
	r.after(sprint.BaseGateRetries[1])
	out := land()
	assert.Equal(t, "stopped base", r.streamState("s1"), out)

	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the base's fix", map[string]string{"internal/docs/port_test.go": "package docs\n\nimport \"testing\"\n\nfunc TestPortInUse(t *testing.T) {}\n"})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	out = land()
	assert.Contains(t, out, "the base main passes its tree gate again at", "no stream to land, the base re-checked")
	assert.NotContains(t, out, "LAND OK", out)
	r.ok("start")
	r.ok("tick --answer-rules")
	assert.Equal(t, "merging", r.streamState("s1"), "resumed by rule")
	assert.Contains(t, r.ok("log"), "answered by rule base-gate: the base main passes its tree gate again at")
	out = land()
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
}
