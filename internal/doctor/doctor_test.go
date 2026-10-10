package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixed is a check that answers one result without touching the Env.
func fixed(name string, fleet bool, status Status, fix string) Check {
	return Check{Name: name, Dependency: name + "-dep", Fleet: fleet,
		Run: func(context.Context, Env) Result {
			return Result{Status: status, Evidence: name + " evidence", Fix: fix}
		}}
}

// The frame runs every registered check, prints one DOCTOR line each, and exits by the
// worst: 0 for ok, 1 for a warn only under --strict, 2 for a fail (docs/SPEC-DOCTOR.md,
// "The exit").
func TestDoctorRunsEveryRegisteredCheckAndExitsByTheWorst(t *testing.T) {
	t.Parallel()
	// run is the run verb over args; with none it is `nova-doctor run`, since a
	// bare `nova-doctor` is refused (the subtest below).
	run := func(t *testing.T, reg *Registry, args ...string) (int, string, string) {
		t.Helper()
		if len(args) == 0 {
			args = []string{"run"}
		}
		var out, errb bytes.Buffer
		code := Main(reg, fakeEnv{}, "", args, strings.NewReader(""), &out, &errb)
		return code, out.String(), errb.String()
	}
	reg := func(cs ...Check) *Registry {
		r := NewRegistry()
		for _, c := range cs {
			r.Register(c)
		}
		return r
	}

	t.Run("a bare nova-doctor runs no check and is refused naming the door", func(t *testing.T) {
		t.Parallel()
		ran := false
		r := reg(Check{Name: "a", Dependency: "d", Run: func(context.Context, Env) Result { ran = true; return Result{Status: OK} }})
		var out, errb bytes.Buffer
		code := Main(r, fakeEnv{}, "", nil, strings.NewReader(""), &out, &errb)
		assert.Equal(t, 2, code)
		assert.Empty(t, out.String(), "a refusal is on stderr")
		assert.Contains(t, errb.String(), "REFUSED")
		assert.Contains(t, errb.String(), "run: nova-doctor help")
		assert.False(t, ran, "no check ran")
		code, out2, _ := run(t, r, "--local")
		assert.Equal(t, 0, code, "a flag alone is still a run: run is the default verb")
		assert.Contains(t, out2, "DOCTOR a ok")
	})
	t.Run("every check runs and is printed", func(t *testing.T) {
		t.Parallel()
		code, out, _ := run(t, reg(fixed("b", false, OK, ""), fixed("a", false, OK, "")))
		assert.Equal(t, 0, code)
		assert.Equal(t, "DOCTOR a ok a evidence\nDOCTOR b ok b evidence\n", out, "sorted by name")
	})
	t.Run("a warn exits 0, and 1 only under --strict", func(t *testing.T) {
		t.Parallel()
		r := reg(fixed("a", false, OK, ""), fixed("w", false, Warn, "nova x fix"))
		code, out, _ := run(t, r)
		assert.Equal(t, 0, code)
		assert.Contains(t, out, "DOCTOR w warn w evidence fix: nova x fix\n")
		code, _, _ = run(t, r, "--strict")
		assert.Equal(t, 1, code)
	})
	t.Run("a fail exits 2, strict or not, and the worst wins", func(t *testing.T) {
		t.Parallel()
		r := reg(fixed("w", false, Warn, "f1"), fixed("f", false, Fail, "f2"), fixed("a", false, OK, ""))
		for _, args := range [][]string{nil, {"--strict"}} {
			code, out, _ := run(t, r, args...)
			assert.Equal(t, 2, code, args)
			assert.Contains(t, out, "DOCTOR f fail f evidence fix: f2\n")
			assert.Contains(t, out, "DOCTOR a ok a evidence\n", "a fail does not stop the others")
		}
	})
	t.Run("--check selects by name, repeatable, and an unknown name is refused", func(t *testing.T) {
		t.Parallel()
		r := reg(fixed("a", false, OK, ""), fixed("b", false, Fail, "f"), fixed("c", false, OK, ""))
		code, out, _ := run(t, r, "--check", "a", "--check", "c")
		assert.Equal(t, 0, code)
		assert.NotContains(t, out, "DOCTOR b")
		code, out, errb := run(t, r, "--check", "nope")
		assert.Equal(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, errb, `no check named "nope"`)
		assert.Contains(t, errb, "a, b, c")
	})
	t.Run("--local skips the fleet checks and says so", func(t *testing.T) {
		t.Parallel()
		r := reg(fixed("a", false, OK, ""), fixed("fleet", true, Fail, "f"))
		code, out, _ := run(t, r, "--local")
		assert.Equal(t, 0, code)
		assert.NotContains(t, out, "DOCTOR fleet ")
		assert.Contains(t, out, "DOCTOR local skipped=fleet")
		code, _, _ = run(t, r)
		assert.Equal(t, 2, code)
	})
	t.Run("--json is the same results as one object", func(t *testing.T) {
		t.Parallel()
		code, out, _ := run(t, reg(fixed("w", false, Warn, "f1")), "--json", "--strict")
		assert.Equal(t, 1, code)
		var got struct {
			Exit    int
			Results []struct{ Check, Dependency, Status, Evidence, Fix string }
		}
		require.NoError(t, json.Unmarshal([]byte(out), &got), out)
		assert.Equal(t, 1, got.Exit)
		require.Len(t, got.Results, 1)
		assert.Equal(t, "w", got.Results[0].Check)
		assert.Equal(t, "warn", got.Results[0].Status)
		assert.Equal(t, "f1", got.Results[0].Fix)
	})
	t.Run("a check that panics is a fail, not a crash", func(t *testing.T) {
		t.Parallel()
		r := reg(Check{Name: "boom", Dependency: "d", Run: func(context.Context, Env) Result { panic("x") }})
		code, out, _ := run(t, r)
		assert.Equal(t, 2, code)
		assert.Contains(t, out, "DOCTOR boom fail ")
	})
	t.Run("a result that is not ok names its fix, or the check is a fail", func(t *testing.T) {
		t.Parallel()
		code, out, _ := run(t, reg(fixed("w", false, Warn, "")))
		assert.Equal(t, 2, code)
		assert.Contains(t, out, "DOCTOR w fail ")
		assert.Contains(t, out, "no fix line")
	})
	t.Run("a duplicate or unnamed registration panics", func(t *testing.T) {
		t.Parallel()
		r := reg(fixed("a", false, OK, ""))
		assert.Panics(t, func() { r.Register(fixed("a", false, OK, "")) })
		assert.Panics(t, func() { r.Register(Check{Dependency: "d", Run: func(context.Context, Env) Result { return Result{} }}) })
	})
	t.Run("the definition meets the tool standard", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, Tool(Default, fakeEnv{}, "").Problems())
	})
	t.Run("the default registry holds the self check", func(t *testing.T) {
		t.Parallel()
		assert.Contains(t, Default.Names(), "self")
	})
}

// fakeEnv is an Env with nothing behind it: every method that is not overridden
// answers "not there". The fields are the fakes a test fills in.
type fakeEnv struct {
	rootAbsolute bool // keep absolute fixture reads inside root when requested
	env          map[string]string
	root         string // files are read under this directory (a t.TempDir())
	exec         func(name string, args ...string) (string, error)
	dial         func(addr string) error
	clock        time.Time
}

func (f fakeEnv) Getenv(k string) string { return f.env[k] }
func (f fakeEnv) Now() time.Time         { return f.clock }
func (f fakeEnv) ReadFile(p string) ([]byte, error) {
	if filepath.IsAbs(p) && !f.rootAbsolute {
		return os.ReadFile(p)
	}
	if f.root == "" {
		return nil, fs.ErrNotExist
	}
	return os.ReadFile(filepath.Join(f.root, p))
}
func (f fakeEnv) ReadDir(p string) ([]fs.DirEntry, error) {
	if filepath.IsAbs(p) && !f.rootAbsolute {
		return os.ReadDir(p)
	}
	if f.root == "" {
		return nil, fs.ErrNotExist
	}
	return os.ReadDir(filepath.Join(f.root, p))
}
func (f fakeEnv) Exec(_ context.Context, name string, args ...string) (string, error) {
	if f.exec == nil {
		return "", errors.New("no exec")
	}
	return f.exec(name, args...)
}
func (f fakeEnv) Dial(_ context.Context, _, addr string) error {
	if f.dial == nil {
		return errors.New("no network")
	}
	return f.dial(addr)
}
