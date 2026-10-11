package testkit

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// scriptRecorder is the failure-path tests' testing.TB with the failure calls
// replaced: Errorf records instead of failing the test, and FailNow panics so a
// helper's failure path can be asserted on.
type scriptRecorder struct {
	testing.TB
	failed bool
	msg    string
}

func (r *scriptRecorder) Helper() {}
func (r *scriptRecorder) Errorf(format string, args ...any) {
	r.failed = true
	r.msg += fmt.Sprintf(format, args...)
}
func (r *scriptRecorder) FailNow() { panic(r) }

// scriptRuns calls f, stopping at the recorder's FailNow as a test would stop.
func scriptRuns(r *scriptRecorder, f func()) {
	defer func() {
		if p := recover(); p != nil && p != any(r) {
			panic(p)
		}
	}()
	f()
}

// TestScriptFailsTheTestOnAnUnexpectedCall pins the strict fake's first refusal:
// a call the script does not declare next fails the test naming the call and
// the call that was declared next.
func TestScriptFailsTheTestOnAnUnexpectedCall(t *testing.T) {
	t.Parallel()
	rec := &scriptRecorder{TB: t}
	s := &Script{t: rec, calls: []Call{{Name: "Get", Args: []any{"k"}, Result: "v"}}}
	scriptRuns(rec, func() { _, _ = s.Called("Put", "k", "w") })
	assert.True(t, rec.failed, "Script answered a call the script did not declare next")
	assert.Contains(t, rec.msg, "Put")
	assert.Contains(t, rec.msg, "Get")
}

// TestScriptFailsTheTestOnACallOutOfOrder pins the order: a declared call made
// before the call declared before it fails the test, since the script consults
// its list in order.
func TestScriptFailsTheTestOnACallOutOfOrder(t *testing.T) {
	t.Parallel()
	rec := &scriptRecorder{TB: t}
	s := &Script{t: rec, calls: []Call{
		{Name: "Get", Args: []any{"k"}, Result: "v"},
		{Name: "Put", Args: []any{"k", "w"}},
	}}
	scriptRuns(rec, func() { _, _ = s.Called("Put", "k", "w") })
	assert.True(t, rec.failed, "Script answered a call out of the declared order")
	assert.Contains(t, rec.msg, "Get")
}

// TestScriptFailsTheTestOnACallLeftOver pins the leftover refusal: a declared
// call the run never makes fails the test naming it.
func TestScriptFailsTheTestOnACallLeftOver(t *testing.T) {
	t.Parallel()
	rec := &scriptRecorder{TB: t}
	s := &Script{t: rec, calls: []Call{{Name: "Get", Args: []any{"k"}, Result: "v"}}}
	scriptRuns(rec, func() { s.AssertExpectations(rec) })
	assert.True(t, rec.failed, "Script passed a declared call the run never made")
	assert.Contains(t, rec.msg, "Get")
}
