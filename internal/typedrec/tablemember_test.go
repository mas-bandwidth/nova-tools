package typedrec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTableMemberValidatesTheCompleteReply(t *testing.T) {
	t.Parallel()
	valid := []any{"MEMBER", "18446744073709551615", "9007199254740993", "placed", "stream: build", "done"}
	got, err := ParseTableMember(valid)
	require.NoError(t, err)
	assert.Equal(t, uint64(18446744073709551615), got.Epoch)
	assert.Equal(t, uint64(9007199254740993), got.Revision)
	assert.Equal(t, "stream: build", got.Row)
	assert.Equal(t, "done", got.Column)

	for _, bad := range [][]any{
		nil, {"MEMBER", "0"}, {"MEMBER", "00", "1", "missing", "", ""},
		{"MEMBER", "0", "18446744073709551616", "missing", "", ""},
		{"MEMBER", "0", "1", "unknown", "", ""},
		{"MEMBER", "0", "1", "missing", "r", "c"},
		{"MEMBER", "0", "1", "placed", "", "c"},
		{"MEMBER", "0", int64(1), "unplaced", "", ""},
	} {
		_, err := ParseTableMember(bad)
		assert.Error(t, err, "accepted malformed %v", bad)
	}
}
