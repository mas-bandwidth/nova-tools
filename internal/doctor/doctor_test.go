package doctor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
)

// fakeEnv is every way a check reaches the machine, held in memory.
type fakeEnv struct {
	env   map[string]string
	dirs  map[string][]string
	execs map[string]string
	now   time.Time
}

func (f fakeEnv) Getenv(k string) string { return f.env[k] }
func (f fakeEnv) ReadDir(p string) ([]string, error) {
	if n, ok := f.dirs[p]; ok {
		return n, nil
	}
	return nil, fs.ErrNotExist
}
func (f fakeEnv) Exec(_ context.Context, name string, _ ...string) (string, error) {
	if out, ok := f.execs[name]; ok {
		return out, nil
	}
	return "", errors.New("no such program")
}
func (f fakeEnv) ReadFile(string) ([]byte, error) { return nil, fs.ErrNotExist }
func (f fakeEnv) Dial(context.Context, string, string) error {
	return errors.New("no network in tests")
}
func (f fakeEnv) Now() time.Time { return f.now }

func fixed(status doctor.Status, evidence, fix string) func(context.Context, doctor.Env) doctor.Result {
	return func(context.Context, doctor.Env) doctor.Result {
		return doctor.Result{Status: status, Evidence: evidence, Fix: fix}
	}
}

func registry(t *testing.T, checks ...doctor.Check) *doctor.Registry {
	t.Helper()
	r := &doctor.Registry{}
	for _, c := range checks {
		r.Register(c)
	}
	return r
}

func run(t *testing.T, r *doctor.Registry, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := doctor.Tool("v0", fakeEnv{now: time.Unix(0, 0)}, r).Run(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

// TestDoctorRunsEveryRegisteredCheckAndExitsByTheWorst pins the frame's
// contract (docs/SPEC-DOCTOR.md): one DOCTOR line per registered check, the
// exit by the worst result, --strict, --check, --local and --json.
func TestDoctorRunsEveryRegisteredCheckAndExitsByTheWorst(t *testing.T) {
	t.Parallel()
	good := doctor.Check{Name: "alpha", Covers: "a", Run: fixed(doctor.OK, "all there", "")}
	warn := doctor.Check{Name: "beta", Covers: "b", Run: fixed(doctor.Warn, "old", "nova-update apply")}
	bad := doctor.Check{Name: "gamma", Covers: "c", Run: fixed(doctor.Fail, "missing", "nova-redis serve")}
	fleet := doctor.Check{Name: "delta", Covers: "d", Fleet: true, Run: fixed(doctor.Fail, "no fleet", "nova-config apply")}

	t.Run("every check prints one line and a fail exits 2", func(t *testing.T) {
		t.Parallel()
		code, out, _ := run(t, registry(t, bad, good, warn), "run")
		assert.Equal(t, 2, code)
		assert.Equal(t, "DOCTOR alpha ok all there\n"+
			"DOCTOR beta warn old fix: nova-update apply\n"+
			"DOCTOR gamma fail missing fix: nova-redis serve\n", out)
	})
	t.Run("a warn alone exits 0, and 1 under --strict", func(t *testing.T) {
		t.Parallel()
		r := registry(t, good, warn)
		code, _, _ := run(t, r, "run")
		assert.Equal(t, 0, code)
		code, _, _ = run(t, r, "--strict")
		assert.Equal(t, 1, code)
	})
	t.Run("all ok exits 0 even under --strict", func(t *testing.T) {
		t.Parallel()
		code, _, _ := run(t, registry(t, good), "--strict")
		assert.Equal(t, 0, code)
	})
	t.Run("--check runs only the named checks, repeatable", func(t *testing.T) {
		t.Parallel()
		r := registry(t, good, warn, bad)
		code, out, _ := run(t, r, "--check", "alpha", "--check", "beta")
		assert.Equal(t, 0, code)
		assert.Contains(t, out, "DOCTOR alpha ok")
		assert.Contains(t, out, "DOCTOR beta warn")
		assert.NotContains(t, out, "gamma")
	})
	t.Run("--check naming no check is refused with the names there are", func(t *testing.T) {
		t.Parallel()
		code, _, errb := run(t, registry(t, good, bad), "--check", "nope")
		assert.Equal(t, 2, code)
		assert.Contains(t, errb, "nope")
		assert.Contains(t, errb, "alpha")
		assert.Contains(t, errb, "gamma")
	})
	t.Run("--local skips the fleet checks and says so", func(t *testing.T) {
		t.Parallel()
		r := registry(t, good, fleet)
		code, out, _ := run(t, r, "--local")
		assert.Equal(t, 0, code)
		assert.NotContains(t, out, "DOCTOR delta")
		assert.Contains(t, out, "NOTE --local skipped 1 fleet check: delta")
		code, _, _ = run(t, r, "run")
		assert.Equal(t, 2, code)
	})
	t.Run("--json is the same results as one object", func(t *testing.T) {
		t.Parallel()
		code, out, _ := run(t, registry(t, good, bad), "--json")
		assert.Equal(t, 2, code)
		var got struct {
			Exit   int `json:"exit"`
			Checks []struct {
				Name, Covers, Status, Evidence, Fix string
			} `json:"checks"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		assert.Equal(t, 2, got.Exit)
		require.Len(t, got.Checks, 2)
		assert.Equal(t, "gamma", got.Checks[1].Name)
		assert.Equal(t, "fail", got.Checks[1].Status)
		assert.Equal(t, "nova-redis serve", got.Checks[1].Fix)
	})
	t.Run("evidence stays on one line", func(t *testing.T) {
		t.Parallel()
		multi := doctor.Check{Name: "multi", Covers: "m", Run: fixed(doctor.OK, "a\nb\tc", "")}
		_, out, _ := run(t, registry(t, multi), "run")
		assert.Equal(t, "DOCTOR multi ok a b c\n", out)
	})
	t.Run("a bare command is refused, naming the verb that runs", func(t *testing.T) {
		t.Parallel()
		code, out, errb := run(t, registry(t, good))
		assert.Equal(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, errb, "run")
	})
	t.Run("the tool meets the skeleton's definition", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, doctor.Tool("v0", fakeEnv{}, registry(t, good)).Problems())
	})
}

func TestRegisterRefusesWhatCannotRun(t *testing.T) {
	t.Parallel()
	r := &doctor.Registry{}
	r.Register(doctor.Check{Name: "a", Covers: "a", Run: fixed(doctor.OK, "", "")})
	assert.Panics(t, func() { r.Register(doctor.Check{Name: "a", Covers: "a", Run: fixed(doctor.OK, "", "")}) })
	assert.Panics(t, func() { r.Register(doctor.Check{Name: "b", Covers: "b"}) })
	assert.Panics(t, func() { r.Register(doctor.Check{Covers: "c", Run: fixed(doctor.OK, "", "")}) })
}
