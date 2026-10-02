package typedrec_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

func TestParseTableCheck(t *testing.T) {
	t.Parallel()
	got, err := typedrec.ParseTableCheck([]any{"CHECK", "18446744073709551615", "9007199254740993", "2", "3"})
	require.NoError(t, err, "report=%+v %v", got, err)
	require.Equal(t, uint64(18446744073709551615), got.Epoch, "report=%+v %v", got, err)
	require.Equal(t, uint64(9007199254740993), got.Revision, "report=%+v %v", got, err)
	require.Equal(t, uint64(2), got.Members, "report=%+v %v", got, err)
	require.Equal(t, uint64(3), got.Cells, "report=%+v %v", got, err)

	for _, reply := range [][]any{
		nil, {"CHECK"}, {"OK", "0", "0", "0", "0"}, {"CHECK", int64(0), "0", "0", "0"},
		{"CHECK", "0", "18446744073709551616", "0", "0"}, {"CHECK", "0", "0", "-1", "0"},
		{"CHECK", "0", "0", "0", "01"}, {"CHECK", "+1", "0", "0", "0"},
		{"CHECK", "0", "0", "0", "0", "extra"},
	} {
		got, err := typedrec.ParseTableCheck(reply)
		assert.Error(t, err, "malformed reply %v produced %+v %v", reply, got, err)
		assert.Equal(t, typedrec.TableCheck{}, got, "malformed reply %v produced %+v %v", reply, got, err)
	}
}
