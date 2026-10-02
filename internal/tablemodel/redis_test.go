package tablemodel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGuardTurnsAFailedCheckIntoAnErrorAndNothingElse(t *testing.T) {
	t.Parallel()
	err := guard(func() { assert(1 == 2, "one is not %d", 2) })
	var f *Failure
	require.ErrorAs(t, err, &f, "error = %v", err)
	require.Equal(t, "one is not 2", err.Error(), "error = %v", err)
	err = guard(func() { assert(true, "never") })
	require.NoError(t, err, "a passing check gave %v", err)
	defer func() {
		r := recover()
		if tassert.NotNil(t, r, "a panic that is not a failed check was swallowed: %v", r) {
			tassert.Equal(t, "a programming error", r, "a panic that is not a failed check was swallowed: %v", r)
		}
	}()
	_ = guard(func() { panic("a programming error") })
}

func TestTypedReadsRefuseTheWrongShape(t *testing.T) {
	t.Parallel()
	require.Equal(t, "x", str("x"), "a right-shaped reply was read wrong")
	require.Equal(t, 3, integer(int64(3)), "a right-shaped reply was read wrong")
	require.Equal(t, 4, integer("4"), "a right-shaped reply was read wrong")
	require.Len(t, list([]any{1}), 1, "a right-shaped reply was read wrong")
	for name, read := range map[string]func(){
		"a string that is an int": func() { str(int64(1)) },
		"an int that is text":     func() { integer("many") },
		"an int that is a list":   func() { integer([]any{}) },
		"a list that is nil":      func() { list(nil) },
		"an odd pair list":        func() { pairs([]any{"a"}) },
		"a pair with an int":      func() { pairs([]any{"a", int64(1)}) },
	} {
		err := guard(read)
		tassert.Error(t, err, "%s was read", name)
	}
}

func TestPairsKeepReplyOrderAndMapsKeepTheLast(t *testing.T) {
	t.Parallel()
	flat := []any{"b", "2", "a", "1"}
	got := pairs(flat)
	require.Equal(t, []pair{{"b", "2"}, {"a", "1"}}, got, "pairs = %v", got)
	gotMap := pairMap(flat)
	require.Equal(t, map[string]string{"a": "1", "b": "2"}, gotMap, "map = %v", gotMap)
	got = pairs([]any{})
	require.Empty(t, got, "pairs of nothing = %v", got)
}

func TestFailureMessagesNameTheCheck(t *testing.T) {
	t.Parallel()
	err := guard(func() { fail("receipt replay diverged at step %d (%s)", 3, "cell_add") })
	require.ErrorContains(t, err, "step 3 (cell_add)", "error = %v", err)
}

func TestAStoreProgramThatIsNotThereCannotRun(t *testing.T) {
	t.Parallel()
	_, err := StartServer(context.Background(), filepath.Join(t.TempDir(), "no-redis-here"), t.TempDir(), time.Second)
	var c *CannotRun
	require.ErrorAs(t, err, &c, "error = %v, want CannotRun", err)
}

func TestASocketPathThatIsTooLongIsRefusedBeforeAnythingStarts(t *testing.T) {
	t.Parallel()
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 60), strings.Repeat("e", 60))
	err := os.MkdirAll(long, 0o755)
	require.NoError(t, err)
	_, err = StartServer(context.Background(), "redis-server", long, time.Second)
	var c *CannotRun
	require.ErrorAs(t, err, &c, "error = %v, want CannotRun naming a shorter directory", err)
	require.ErrorContains(t, err, "shorter directory", "error = %v, want CannotRun naming a shorter directory", err)
	m, _ := filepath.Glob(filepath.Join(long, "tablemodel-*"))
	require.Empty(t, m, "the refused store left %v", m)
}
