package fn

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

var localDecl = regexp.MustCompile(`^local\s+(function\s+[A-Za-z_][A-Za-z0-9_]*|[A-Za-z_][A-Za-z0-9_]*(\s*,\s*[A-Za-z_][A-Za-z0-9_]*)*)`)

// countLocals returns the most locals the assembled chunk's main function
// holds at once: the column-0 locals outside every file block, plus the
// largest count inside one block (a block's locals leave scope at its end).
// sum is what the chunk would hold with no blocks, for the log line.
func countLocals(t *testing.T, source string) (active, sum int, largest string) {
	t.Helper()
	outside, most := 0, 0
	block, inBlock, header := 0, "", ""
	for i, line := range strings.Split(source, "\n") {
		prev := header
		header = ""
		switch {
		case strings.HasPrefix(line, fileHeader+"lua/"):
			header = strings.TrimPrefix(line, fileHeader)
			continue
		case line == blockOpen && prev != "":
			if inBlock != "" {
				t.Fatalf("line %d: block %q opens inside %q", i+1, prev, inBlock)
			}
			inBlock, block = prev, 0
			continue
		case strings.HasPrefix(line, blockClose):
			if name := strings.TrimPrefix(line, blockClose); name != inBlock {
				t.Fatalf("line %d: block %q closes, open is %q", i+1, name, inBlock)
			}
			if block > most {
				most, largest = block, inBlock
			}
			inBlock = ""
			continue
		}
		m := localDecl.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n := 1
		if !strings.HasPrefix(m[1], "function") {
			n = strings.Count(m[1], ",") + 1
		}
		sum += n
		if inBlock != "" {
			block += n
		} else {
			outside += n
		}
	}
	if inBlock != "" {
		t.Fatalf("block %q never closes", inBlock)
	}
	return outside + most, sum, largest
}

// TestLibraryLocalsUnderLimit is the guard for dev red at 8d12fb19: #3487
// took the concatenated library from 199 to 206 top-level locals and Redis
// refused it ("main function has more than 200 local variables"). It counts
// the locals the assembled chunk's main function holds at once and fails
// above MaxLocals (180), so a merge that nears the limit fails here, in the
// package that owns the chunk, not at FUNCTION LOAD on the fleet.
func TestLibraryLocalsUnderLimit(t *testing.T) {
	t.Parallel()

	source, err := Source()
	if err != nil {
		t.Fatal(err)
	}
	active, sum, largest := countLocals(t, source)
	t.Logf("active locals %d (largest file %s), unscoped sum %d, limit %d", active, largest, sum, MaxLocals)
	if active > MaxLocals {
		t.Fatalf("nova_sprint main function holds %d locals at once (largest file %s), over %d (Lua refuses above 200); move helpers into a table or split the file", active, largest, MaxLocals)
	}
	if active == sum && sum > 1 {
		t.Fatalf("no file is block-scoped (active %d = sum %d); Source must wrap each file in its own do-block", active, sum)
	}
}

// TestCountLocalsSeesTheOldShape is the guard's own control: the same files
// concatenated with no blocks (the shape that broke dev) count over the limit.
func TestCountLocalsSeesTheOldShape(t *testing.T) {
	t.Parallel()

	source, err := Source()
	if err != nil {
		t.Fatal(err)
	}
	var flat []string
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		if line == blockOpen && i > 0 && strings.HasPrefix(lines[i-1], fileHeader+"lua/") {
			continue
		}
		if strings.HasPrefix(line, blockClose+"lua/") {
			continue
		}
		flat = append(flat, line)
	}
	active, _, _ := countLocals(t, strings.Join(flat, "\n"))
	if active <= MaxLocals {
		t.Fatalf("unblocked chunk counts %d locals, want > %d (the guard must see the pre-fix shape)", active, MaxLocals)
	}
}

type fakeRedisLoader struct {
	redis.UniversalClient

	keys              []string
	info              string
	loadReplaceCalled int
	loadCalled        int
}

func (f *fakeRedisLoader) Keys(ctx context.Context, pattern string) *redis.StringSliceCmd {
	return redis.NewStringSliceResult(f.keys, nil)
}

func (f *fakeRedisLoader) Info(ctx context.Context, section ...string) *redis.StringCmd {
	return redis.NewStringResult(f.info, nil)
}

func (f *fakeRedisLoader) FunctionLoadReplace(ctx context.Context, code string) *redis.StringCmd {
	f.loadReplaceCalled++
	return redis.NewStringResult(Library, nil)
}

func (f *fakeRedisLoader) FunctionLoad(ctx context.Context, code string) *redis.StringCmd {
	f.loadCalled++
	return redis.NewStringResult(Library, nil)
}

func (f *fakeRedisLoader) FunctionList(ctx context.Context, q redis.FunctionListQuery) *redis.FunctionListCmd {
	cmd := redis.NewFunctionListCmd(ctx, "function", "list")
	cmd.SetVal(nil)
	return cmd
}

func TestLoadRefusesNonDevSprint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Single non-dev sprint key
	fake := &fakeRedisLoader{
		keys: []string{"prod-sprint:epoch"},
		info: "cluster_enabled:0",
	}
	err := Load(ctx, fake)
	if err == nil {
		t.Fatal("Load = nil, want refusal for non-dev sprint key")
	}
	want := "this server holds a sprint that is not a dev- sprint (prod-sprint:epoch); nothing was loaded; load this library only on a bench server"
	if err.Error() != want {
		t.Fatalf("Load error = %q, want %q", err.Error(), want)
	}
	if fake.loadReplaceCalled != 0 {
		t.Fatalf("FunctionLoadReplace was called %d times; want 0", fake.loadReplaceCalled)
	}

	// Multiple keys: verify sorting selects the first offending key
	fakeSorted := &fakeRedisLoader{
		keys: []string{"prod-sprint:epoch", "alpha-sprint:epoch", "dev-test:sprint:epoch"},
		info: "cluster_enabled:0",
	}
	err = Load(ctx, fakeSorted)
	if err == nil {
		t.Fatal("Load = nil, want refusal for non-dev sprint key")
	}
	wantSorted := "this server holds a sprint that is not a dev- sprint (alpha-sprint:epoch); nothing was loaded; load this library only on a bench server"
	if err.Error() != wantSorted {
		t.Fatalf("Load error = %q, want %q", err.Error(), wantSorted)
	}
	if fakeSorted.loadReplaceCalled != 0 {
		t.Fatalf("FunctionLoadReplace was called %d times; want 0", fakeSorted.loadReplaceCalled)
	}

	// LoadMissing also refuses
	fakeMissing := &fakeRedisLoader{
		keys: []string{"prod-sprint:epoch"},
		info: "cluster_enabled:0",
	}
	err = LoadMissing(ctx, fakeMissing)
	if err == nil {
		t.Fatal("LoadMissing = nil, want refusal for non-dev sprint key")
	}
	if err.Error() != want {
		t.Fatalf("LoadMissing error = %q, want %q", err.Error(), want)
	}
	if fakeMissing.loadCalled != 0 {
		t.Fatalf("FunctionLoad was called %d times; want 0", fakeMissing.loadCalled)
	}
}

func TestLoadRefusesCluster(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fake := &fakeRedisLoader{
		keys: []string{"dev-bench:sprint:epoch"},
		info: "# Cluster\r\ncluster_enabled:1\r\n",
	}
	err := Load(ctx, fake)
	if err == nil {
		t.Fatal("Load = nil, want refusal for cluster")
	}
	want := "this server is a cluster; the library supports a standalone server only; nothing was loaded"
	if err.Error() != want {
		t.Fatalf("Load error = %q, want %q", err.Error(), want)
	}
	if fake.loadReplaceCalled != 0 {
		t.Fatalf("FunctionLoadReplace was called %d times; want 0", fake.loadReplaceCalled)
	}

	// LoadMissing also refuses
	fakeMissing := &fakeRedisLoader{
		keys: []string{"dev-bench:sprint:epoch"},
		info: "# Cluster\r\ncluster_enabled:1\r\n",
	}
	err = LoadMissing(ctx, fakeMissing)
	if err == nil {
		t.Fatal("LoadMissing = nil, want refusal for cluster")
	}
	if err.Error() != want {
		t.Fatalf("LoadMissing error = %q, want %q", err.Error(), want)
	}
	if fakeMissing.loadCalled != 0 {
		t.Fatalf("FunctionLoad was called %d times; want 0", fakeMissing.loadCalled)
	}
}

func TestLoadCleanServer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, keys := range [][]string{
		nil,
		{},
		{"dev-one:sprint:epoch", "dev-two:sprint:epoch"},
	} {
		fake := &fakeRedisLoader{
			keys: keys,
			info: "# Cluster\r\ncluster_enabled:0\r\n",
		}
		if err := Load(ctx, fake); err != nil {
			t.Fatalf("Load with keys %v: %v", keys, err)
		}
		if fake.loadReplaceCalled != 1 {
			t.Fatalf("FunctionLoadReplace called %d times, want 1", fake.loadReplaceCalled)
		}

		fakeMissing := &fakeRedisLoader{
			keys: keys,
			info: "# Cluster\r\ncluster_enabled:0\r\n",
		}
		if err := LoadMissing(ctx, fakeMissing); err != nil {
			t.Fatalf("LoadMissing with keys %v: %v", keys, err)
		}
		if fakeMissing.loadCalled != 1 {
			t.Fatalf("FunctionLoad called %d times, want 1", fakeMissing.loadCalled)
		}
	}
}
