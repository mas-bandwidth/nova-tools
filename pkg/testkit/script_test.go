package testkit_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStore is the example of a package wrapping a Script for its own transport
// interface: each method passes its own name and arguments to Called and
// returns what the declared call returns. A method returning only an error
// ignores the result; one returning only a value ignores the error.
type fakeStore struct{ *testkit.Script }

func (f fakeStore) Get(key string) (string, error) {
	got, err := f.Called("Get", key)
	if err != nil {
		return "", err
	}
	return got.(string), nil
}

func (f fakeStore) Put(key, value string) error {
	_, err := f.Called("Put", key, value)
	return err
}

// TestScriptAnswersDeclaredCallsInOrder pins the strict fake's main path: the
// declared calls are answered in the order declared, each with its own result
// and error, and a run that makes them all passes the leftover check at
// cleanup.
func TestScriptAnswersDeclaredCallsInOrder(t *testing.T) {
	t.Parallel()
	s := testkit.NewScript(t,
		testkit.Call{Name: "Get", Args: []any{"k"}, Result: "v"},
		testkit.Call{Name: "Put", Args: []any{"k", "w"}},
	)
	f := fakeStore{s}
	got, err := f.Get("k")
	require.NoError(t, err)
	assert.Equal(t, "v", got)
	require.NoError(t, f.Put("k", "w"))
}
