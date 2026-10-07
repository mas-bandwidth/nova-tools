package testkit

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Call is one expected call of a Script: the transport method's name, its
// arguments, and the result and error the method returns.
type Call struct {
	Name   string
	Args   []any
	Result any
	Err    error
}

// Script is a strict fake for a transport interface: the calls a test declares,
// consulted in order as the code under test makes them. An unexpected call, a
// call out of order, or a declared call the run never made fails the test
// naming it (docs/STANDARD.md section 8: "A fake is strict like the real tool:
// it refuses what the real one refuses"). Every call is declared: there is no
// matcher and no wildcard.
//
// A package wraps a Script for its own transport interface: its fake holds the
// Script and each interface method passes its own name and arguments to Called,
// then returns the declared result and error. The example is
// TestScriptAnswersDeclaredCallsInOrder in script_test.go:
//
//	type fakeStore struct{ *testkit.Script }
//
//	func (f fakeStore) Get(key string) (string, error) {
//		got, err := f.Called("Get", key)
//		if err != nil {
//			return "", err
//		}
//		return got.(string), nil
//	}
//
//	s := testkit.NewScript(t, testkit.Call{Name: "Get", Args: []any{"k"}, Result: "v"})
type Script struct {
	t     testing.TB
	mu    sync.Mutex
	calls []Call
	next  int
}

// NewScript declares the calls a fake may receive, in order, and arms the
// leftover check at cleanup: every declared call the run never makes fails t
// then, naming it (the AssertExpectations step of the transport design). A
// caller that wants the check earlier calls AssertExpectations itself.
func NewScript(t testing.TB, calls ...Call) *Script {
	t.Helper()
	s := &Script{t: t, calls: calls}
	t.Cleanup(func() { s.AssertExpectations(t) })
	return s
}

// Called is one call the fake received. It checks the call against the next
// declared one and fails t naming both when they differ, so a wrong call is
// never answered: the declared result and error are returned only for the
// declared call. A wrapper passes the transport method's name and arguments,
// and reads the two values at the arity its method returns.
func (s *Script) Called(name string, args ...any) (any, error) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next >= len(s.calls) {
		require.FailNowf(s.t, "unexpected call",
			"%s(%s): the script declared %d call(s) and all of them were made", name, argsText(args), len(s.calls))
		return nil, nil
	}
	want := s.calls[s.next]
	if want.Name != name || !reflect.DeepEqual(want.Args, args) {
		require.FailNowf(s.t, "unexpected call",
			"%s(%s): the next declared call is %s(%s)", name, argsText(args), want.Name, argsText(want.Args))
		return nil, nil
	}
	s.next++
	return want.Result, want.Err
}

// AssertExpectations fails t for every declared call the run never made, naming
// them in order. NewScript arms it at cleanup, so a test names its calls and
// does not call this itself; a helper that has to check before cleanup calls it.
func (s *Script) AssertExpectations(t testing.TB) {
	t.Helper()
	s.mu.Lock()
	left := append([]Call(nil), s.calls[s.next:]...)
	s.mu.Unlock()
	names := make([]string, len(left))
	for i, c := range left {
		names[i] = c.Name + "(" + argsText(c.Args) + ")"
	}
	require.Empty(t, left, "the script declared calls the run never made: %s", strings.Join(names, ", "))
}

// argsText is a call's arguments as a failure prints them, `%v` each.
func argsText(args []any) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strings.TrimSpace(strings.ReplaceAll(fmt.Sprint(a), "\n", " ")))
	}
	return b.String()
}
