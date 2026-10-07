package decide

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errBackend is a Backend whose Ask always reports a failure: the seam that makes
// Make wrap a backend failure in a BackendError (firstread.go:96), so a caller can
// recover the cause through BackendError.Unwrap (firstread.go:81).
type errBackend struct{ err error }

func (e errBackend) Name() string { return "err" }

func (e errBackend) Ask(context.Context, Schema, string) (map[string]Answer, Usage, error) {
	return nil, Usage{}, e.err
}

var errBackendFailed = errors.New("the backend could not answer the read")

// TestFirstreadCoverBackendErrorUnwrap reaches, through Make and a failing backend,
// the BackendError the read produces when Ask cannot answer, and exercises Unwrap
// (firstread.go:81) -- the read's one function at 0.0% coverage -- through the
// standard library's error traversal (errors.As, errors.Is, errors.Unwrap).
func TestFirstreadCoverBackendErrorUnwrap(t *testing.T) {
	t.Parallel()
	record := filepath.Join(t.TempDir(), "read.jsonl")
	_, _, err := Make(context.Background(), errBackend{err: errBackendFailed}, ReadSchema(), "the card", record, "op1", nil, at)
	require.Error(t, err)
	var be *BackendError
	require.True(t, errors.As(err, &be), "Make wraps a backend failure in *BackendError")
	assert.Equal(t, "err", be.Backend, "the failed backend is named in the decision")

	for _, tc := range []struct {
		name    string
		err     error
		target  error // for errors.Is
		wantIs  bool
		unwraps error // expected errors.Unwrap result
	}{
		{
			name:    "main path: Unwrap hands back the cause Ask reported",
			err:     be,
			target:  errBackendFailed,
			wantIs:  true,
			unwraps: errBackendFailed,
		},
		{
			name:    "refusal: it is not an error it was not handed",
			err:     be,
			target:  errors.New("not the backend's failure"),
			wantIs:  false,
			unwraps: errBackendFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.unwraps, errors.Unwrap(tc.err), "Unwrap returns the inner error")
			assert.Equal(t, tc.wantIs, errors.Is(tc.err, tc.target))
		})
	}
}
