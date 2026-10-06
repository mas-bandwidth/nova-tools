package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEnv is an Env over a table: programs on PATH with the output of
// `<tool> version`, files rooted in a temp dir, dials by address, a fixed clock.
type fakeEnv struct {
	root     string
	programs map[string]string // name -> output of `version`; "" with an entry in broken is an error
	broken   map[string]bool
	dials    map[string]error
	now      time.Time
	ran      []string
}

func (e *fakeEnv) LookPath(name string) (string, error) {
	if _, ok := e.programs[name]; ok {
		return "/fake/bin/" + name, nil
	}
	return "", errors.New("not found")
}

func (e *fakeEnv) Exec(_ context.Context, name string, args ...string) (string, error) {
	base := filepath.Base(name)
	e.ran = append(e.ran, base+" "+strings.Join(args, " "))
	if e.broken[base] {
		return "", errors.New("exit status 2")
	}
	return e.programs[base], nil
}

func (e *fakeEnv) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(filepath.Join(e.root, path))
}
func (e *fakeEnv) Dial(_ context.Context, _, addr string) error { return e.dials[addr] }
func (e *fakeEnv) Now() time.Time                               { return e.now }

func versionLine(tool, v string) string { return tool + " " + v + " linux/amd64 go1.26.0\n" }

func fixed(name string, s Status, ev, fix string, fleet bool) Check {
	return Check{Name: name, Covers: name + " dependency", Fleet: fleet,
		Run: func(context.Context, Env) Result { return Result{Status: s, Evidence: ev, Fix: fix} }}
}

func TestDoctorRunsEveryRegisteredCheckAndExitsByTheWorst(t *testing.T) {
	t.Parallel()
	env := &fakeEnv{root: t.TempDir()}
	cases := []struct {
		name   string
		checks []Check
		opts   Options
		exit   int
		lines  []string
	}{
		{"all ok exits 0", []Check{fixed("a", OK, "fine", "", false), fixed("b", OK, "fine too", "", false)},
			Options{}, 0, []string{"DOCTOR a ok fine", "DOCTOR b ok fine too"}},
		{"warn exits 0 without strict", []Check{fixed("a", Warn, "slow", "nova-x fix", false)},
			Options{}, 0, []string{"DOCTOR a warn slow fix: nova-x fix"}},
		{"warn exits 1 with strict", []Check{fixed("a", Warn, "slow", "nova-x fix", false)},
			Options{Strict: true}, 1, []string{"DOCTOR a warn slow fix: nova-x fix"}},
		{"fail exits 2 and outranks a strict warn", []Check{fixed("a", Warn, "slow", "w", false), fixed("b", Fail, "gone", "nova-y fix", false)},
			Options{Strict: true}, 2, []string{"DOCTOR a warn slow fix: w", "DOCTOR b fail gone fix: nova-y fix"}},
		{"every check runs after a fail", []Check{fixed("a", Fail, "gone", "f", false), fixed("b", OK, "fine", "", false)},
			Options{}, 2, []string{"DOCTOR a fail gone fix: f", "DOCTOR b ok fine"}},
		{"a failing check with no fix still names one", []Check{fixed("a", Fail, "gone", "", false)},
			Options{}, 2, []string{"DOCTOR a fail gone fix: run nova-doctor --check a --json for the full evidence"}},
		{"evidence stays on one line", []Check{fixed("a", OK, "two\nlines", "", false)},
			Options{}, 0, []string{"DOCTOR a ok two lines"}},
		{"--check selects by name", []Check{fixed("a", Fail, "gone", "f", false), fixed("b", OK, "fine", "", false)},
			Options{Only: []string{"b"}}, 0, []string{"DOCTOR b ok fine"}},
		{"--local skips a fleet check and says so", []Check{fixed("a", Fail, "gone", "f", true), fixed("b", OK, "fine", "", false)},
			Options{Local: true}, 0, []string{"DOCTOR a skip skipped: only a fleet needs this check and --local was given", "DOCTOR b ok fine"}},
		{"a fleet check runs without --local", []Check{fixed("a", Fail, "gone", "f", true)},
			Options{}, 2, []string{"DOCTOR a fail gone fix: f"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg := &Registry{}
			for _, c := range tc.checks {
				reg.Register(c)
			}
			rep, err := reg.Run(context.Background(), env, tc.opts)
			require.NoError(t, err)
			var buf bytes.Buffer
			rep.Text(&buf)
			assert.Equal(t, tc.lines, strings.Split(strings.TrimSpace(buf.String()), "\n"))
			assert.Equal(t, tc.exit, rep.Exit)
		})
	}
}

func TestAnUnknownCheckNameIsRefusedWithTheNamesThereAre(t *testing.T) {
	t.Parallel()
	reg := &Registry{}
	reg.Register(fixed("a", OK, "x", "", false))
	reg.Register(fixed("b", OK, "x", "", false))
	_, err := reg.Run(context.Background(), &fakeEnv{}, Options{Only: []string{"nope", "a"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
	assert.Contains(t, err.Error(), "a, b")
}

func TestARegistryRefusesAnIncompleteOrDuplicateCheck(t *testing.T) {
	t.Parallel()
	reg := &Registry{}
	assert.Panics(t, func() { reg.Register(Check{Name: "a"}) })
	reg.Register(fixed("a", OK, "x", "", false))
	assert.Panics(t, func() { reg.Register(fixed("a", OK, "x", "", false)) })
}

func TestTheDefaultRegistryHoldsTheSelfCheckFromItsOwnFile(t *testing.T) {
	t.Parallel()
	assert.Contains(t, Default.Names(), "self")
}

func TestTheToolPrintsLinesAndExitsByTheWorstAndPrintsJSON(t *testing.T) {
	t.Parallel()
	reg := &Registry{}
	reg.Register(fixed("a", Warn, "slow", "nova-x fix", false))
	reg.Register(fixed("f", Fail, "gone", "nova-y fix", true))
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := Tool("v0", &fakeEnv{}, reg).Run(args, strings.NewReader(""), &out, &errb)
		return code, out.String(), errb.String()
	}

	code, out, _ := run("--local")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "DOCTOR a warn slow fix: nova-x fix\n")
	assert.Contains(t, out, "DOCTOR f skip ")

	code, _, _ = run("--local", "--strict")
	assert.Equal(t, 1, code)

	code, _, _ = run()
	assert.Equal(t, 2, code)

	code, out, _ = run("--check", "a", "--json")
	assert.Equal(t, 0, code)
	var rep Report
	require.NoError(t, json.Unmarshal([]byte(out), &rep))
	require.Len(t, rep.Lines, 1)
	assert.Equal(t, "a", rep.Lines[0].Check)
	assert.Equal(t, Warn, rep.Lines[0].Status)

	code, _, errs := run("--check", "nope")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "nope")
}

func TestDoctorToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, Tool("v0", &fakeEnv{}, &Registry{}).Problems())
}
