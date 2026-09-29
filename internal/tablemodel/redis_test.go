package tablemodel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGuardTurnsAFailedCheckIntoAnErrorAndNothingElse(t *testing.T) {
	t.Parallel()
	err := guard(func() { assert(1 == 2, "one is not %d", 2) })
	var f *Failure
	if !errors.As(err, &f) || err.Error() != "one is not 2" {
		t.Fatalf("error = %v", err)
	}
	if err := guard(func() { assert(true, "never") }); err != nil {
		t.Fatalf("a passing check gave %v", err)
	}
	defer func() {
		if r := recover(); r == nil || r.(string) != "a programming error" {
			t.Errorf("a panic that is not a failed check was swallowed: %v", r)
		}
	}()
	_ = guard(func() { panic("a programming error") })
}

func TestTypedReadsRefuseTheWrongShape(t *testing.T) {
	t.Parallel()
	if str("x") != "x" || integer(int64(3)) != 3 || integer("4") != 4 || len(list([]any{1})) != 1 {
		t.Fatal("a right-shaped reply was read wrong")
	}
	for name, read := range map[string]func(){
		"a string that is an int": func() { str(int64(1)) },
		"an int that is text":     func() { integer("many") },
		"an int that is a list":   func() { integer([]any{}) },
		"a list that is nil":      func() { list(nil) },
		"an odd pair list":        func() { pairs([]any{"a"}) },
		"a pair with an int":      func() { pairs([]any{"a", int64(1)}) },
	} {
		if err := guard(read); err == nil {
			t.Errorf("%s was read", name)
		}
	}
}

func TestPairsKeepReplyOrderAndMapsKeepTheLast(t *testing.T) {
	t.Parallel()
	flat := []any{"b", "2", "a", "1"}
	if got := pairs(flat); !reflect.DeepEqual(got, []pair{{"b", "2"}, {"a", "1"}}) {
		t.Fatalf("pairs = %v", got)
	}
	if got := pairMap(flat); !reflect.DeepEqual(got, map[string]string{"a": "1", "b": "2"}) {
		t.Fatalf("map = %v", got)
	}
	if got := pairs([]any{}); len(got) != 0 {
		t.Fatalf("pairs of nothing = %v", got)
	}
}

func TestFailureMessagesNameTheCheck(t *testing.T) {
	t.Parallel()
	err := guard(func() { fail("receipt replay diverged at step %d (%s)", 3, "cell_add") })
	if err == nil || !strings.Contains(err.Error(), "step 3 (cell_add)") {
		t.Fatalf("error = %v", err)
	}
}

func TestAStoreProgramThatIsNotThereCannotRun(t *testing.T) {
	t.Parallel()
	_, err := StartServer(context.Background(), filepath.Join(t.TempDir(), "no-redis-here"), t.TempDir(), time.Second)
	var c *CannotRun
	if !errors.As(err, &c) {
		t.Fatalf("error = %v, want CannotRun", err)
	}
}

func TestASocketPathThatIsTooLongIsRefusedBeforeAnythingStarts(t *testing.T) {
	t.Parallel()
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 60), strings.Repeat("e", 60))
	if err := os.MkdirAll(long, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := StartServer(context.Background(), "redis-server", long, time.Second)
	var c *CannotRun
	if !errors.As(err, &c) || !strings.Contains(err.Error(), "shorter directory") {
		t.Fatalf("error = %v, want CannotRun naming a shorter directory", err)
	}
	if m, _ := filepath.Glob(filepath.Join(long, "tablemodel-*")); len(m) != 0 {
		t.Fatalf("the refused store left %v", m)
	}
}
