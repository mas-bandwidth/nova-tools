//go:build functional

package fn

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// TestSprintProfileLoadsOnTheStore: FUNCTION LOAD of the sprint profile
// succeeds on a real Redis (fact F12 at load: TestSprintFilesTouchNoGlobalAtLoad
// and the sandbox are its host-side checks), and the library holds the
// sprint's two functions. The reversed witness: the same profile with a part
// registered twice is refused, naming sprint_registration_refused, and leaves
// no library behind.
func TestSprintProfileLoadsOnTheStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testredis.Start(t)})
	defer c.Close()

	src, err := TSetSource(TSetSprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.FunctionLoad(ctx, src+DoubledSprintPart).Err(); err == nil || !strings.Contains(err.Error(), "sprint_registration_refused") {
		t.Fatalf("a profile registering a part twice: %v, want FUNCTION LOAD refused naming sprint_registration_refused", err)
	}
	if libs, err := c.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: Library}).Result(); err != nil || len(libs) != 0 {
		t.Fatalf("after the refused load: %v %+v, want no library", err, libs)
	}

	if err := LoadTSet(ctx, c, TSetSprint); err != nil {
		t.Fatalf("FUNCTION LOAD of the sprint profile: %v", err)
	}
	libs, err := c.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: Library}).Result()
	if err != nil || len(libs) != 1 {
		t.Fatalf("function list: %v %+v", err, libs)
	}
	var names []string
	for _, f := range libs[0].Functions {
		names = append(names, f.Name)
	}
	for _, want := range SprintFunctions {
		if !slices.Contains(names, want) {
			t.Errorf("the loaded library holds %v, not %s", names, want)
		}
	}
}
