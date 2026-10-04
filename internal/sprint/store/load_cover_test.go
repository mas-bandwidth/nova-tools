package store

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadCoverMovedError covers movedError's Error and Unwrap (load.go): the
// error of a read that saw its table change between exchanges. Error names the
// table, and Unwrap is errMoved, so errors.Is finds the change through the
// naming (workColumn's retry loop and Load's errors.As both ride on it).
func TestLoadCoverMovedError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		table string
		want  string
	}{
		{name: "the work table", table: "t-work", want: "table t-work changed while it was read"},
		{name: "the readers table", table: "t-readers", want: "table t-readers changed while it was read"},
		{name: "no table named", table: "", want: "table  changed while it was read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := &movedError{table: tc.table}
			assert.Equal(t, tc.want, e.Error(), "Error names the table that moved: %q", tc.table)
			assert.Equal(t, errMoved, e.Unwrap(), "Unwrap is errMoved")
			require.True(t, errors.Is(e, errMoved), "errors.Is sees the change through Unwrap")
		})
	}
}

// otherRefusal is a sentinel no movedError is: the refusal side of the pair.
var otherRefusal = errors.New("another refusal")

// TestLoadCoverMovedErrorRefusals covers what a movedError refuses to be: not
// errCleared (epoch.go, another epoch), not any other sentinel, and not a type
// errors.As is not asked for. Each check walks the one-link chain, so it rides
// on Unwrap and says no.
func TestLoadCoverMovedErrorRefusals(t *testing.T) {
	t.Parallel()
	e := &movedError{table: "t-work"}
	assert.False(t, errors.Is(e, errCleared), "a moved read is not a cleared one")
	assert.False(t, errors.Is(e, otherRefusal), "a moved read is no other sentinel")
	var asOther *otherError
	assert.False(t, errors.As(error(e), &asOther), "a movedError is not an otherError")
	assert.Nil(t, asOther, "the refused errors.As wrote no target")
}

// otherError is a type errors.As must refuse a movedError against.
type otherError struct{}

func (*otherError) Error() string { return "other" }
