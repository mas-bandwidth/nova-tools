package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The seed of 2026-10-02 (tools/notes20261002): the reasons the first real
// sprint disabled eight routes, and why one machine is held, written as notes
// through nova-config. The program runs with a stand-in nova-config (a Go
// binary) that records each call and answers `route show` from a state file;
// the calls it recorded are then replayed through the real grammar and the
// in-process store, so every flag the program passes is the tool's own and every
// refusal the tool's.

const standInSource = `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	f, _ := os.OpenFile("calls.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	fmt.Fprintln(f, strings.Join(os.Args[1:], "\x1f")+"\x1f")
	f.Close()
	if len(os.Args) > 3 && os.Args[1] == "route" && os.Args[2] == "show" {
		raw, _ := os.ReadFile("state.txt")
		state := ""
		for _, l := range strings.Split(string(raw), "\n") {
			if n, st, ok := strings.Cut(l, " "); ok && n == os.Args[3] {
				state = st
			}
		}
		switch state {
		case "disabled":
			fmt.Printf("ROUTE name=%s enabled=false note=-\n", os.Args[3])
		case "enabled":
			fmt.Printf("ROUTE name=%s enabled=true note=-\n", os.Args[3])
		default:
			fmt.Fprintf(os.Stderr, "nova-config route show REFUSED: route %s not found\n", os.Args[3])
			os.Exit(1)
		}
	}
}
`

var (
	builtOnce        sync.Once
	builtNotes       string
	builtStandIn     string
	builtNotesErr    error
	builtNotesFolder string
)

// builtBinaries builds the notes program and the stand-in once for the package's tests.
func builtBinaries(t *testing.T) (notes, standIn string) {
	t.Helper()
	builtOnce.Do(func() {
		dir, err := os.MkdirTemp("", "notes-bins-")
		if err != nil {
			builtNotesErr = err
			return
		}
		builtNotesFolder = dir
		builtNotes = filepath.Join(dir, "notes20261002")
		builtStandIn = filepath.Join(dir, "nova-config")
		src := filepath.Join(dir, "standin.go")
		if builtNotesErr = os.WriteFile(src, []byte(standInSource), 0o644); builtNotesErr != nil {
			return
		}
		for _, b := range []struct{ dir, out, pkg string }{{"../..", builtNotes, "./tools/notes20261002"}, {dir, builtStandIn, "standin.go"}} {
			cmd := exec.Command("go", "build", "-buildvcs=false", "-o", b.out, b.pkg)
			cmd.Dir = b.dir
			cmd.Env = goenv.Clean(os.Environ())
			if out, err := cmd.CombinedOutput(); err != nil {
				builtNotesErr = fmt.Errorf("go build %s: %v\n%s", b.pkg, err, out)
				return
			}
		}
	})
	require.NoError(t, builtNotesErr)
	return builtNotes, builtStandIn
}

// runSeed runs the program in dir against the stand-in and returns its output,
// its exit code and the calls it made, each as its arguments.
func runSeed(t *testing.T, dir string, args ...string) (string, int, [][]string) {
	t.Helper()
	notes, standIn := builtBinaries(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "calls.log"), nil, 0o644))
	cmd := exec.Command(notes, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "NOVA_CONFIG=" + standIn, "NOTES_CONN=--pg " + dsn, "NOTES_AS=a1"}
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else {
		require.NoError(t, err, string(out))
	}
	raw, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	require.NoError(t, err)
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if line != "" {
			calls = append(calls, strings.Split(strings.TrimSuffix(line, "\x1f"), "\x1f"))
		}
	}
	return string(out), code, calls
}

// writes is the calls that write: every one but a `route show`.
func writes(calls [][]string) [][]string {
	var out [][]string
	for _, c := range calls {
		if len(c) < 2 || c[0]+" "+c[1] != "route show" {
			out = append(out, c)
		}
	}
	return out
}

func TestTheSeedProgramWritesTheDaysReasonsAsNotesAndTouchesNothingElse(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_FRIEND"] = "a1"
	disabled := []string{"flash-mimo26pro-openrouter", "flash-luna6-opencode", "flash-luna6-openrouter", "flash-mercury-openrouter",
		"flash-nemotron-openrouter", "flash-gemini31lite-openrouter"}
	state := "flash-mimo26-openrouter enabled\n" // on: the program skips it; flash-luna56-opencode has no row: it is missing
	for _, name := range disabled {
		state += name + " disabled\n"
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "state.txt"), []byte(state), 0o644))

	for _, args := range [][]string{
		{"machine", "add", "m7", "--user", "u", "--seat", "s", "--slots", "8", "--width", "8", "--pg", dsn},
		{"route", "add", "flash-mimo26-openrouter", "--tier", "flash", "--provider", "p", "--model", "m", "--deadline", "60", "--pg", dsn},
	} {
		code, _, errs := h.run(t, args...)
		require.Equal(t, 0, code, errs)
	}
	for _, name := range disabled {
		code, _, errs := h.run(t, "route", "add", name, "--tier", "flash", "--provider", "p", "--model", "m", "--deadline", "60", "--enabled", "false", "--note", "by hand", "--pg", dsn)
		require.Equal(t, 0, code, errs)
	}

	// a dry run: the same calls, each carrying --dry-run, and the store unchanged
	out, code, calls := runSeed(t, dir, "m7", "--dry-run")
	assert.Equal(t, 1, code, "a route with no row ends the program 1: %s", out)
	require.Len(t, writes(calls), 7, "six routes and the machine: %v", calls)
	for _, c := range writes(calls) {
		assert.Contains(t, c, "--dry-run")
		code, plan, errs := h.run(t, c...)
		require.Equal(t, 0, code, "%v: %s", c, errs)
		assert.Contains(t, plan, "CONFIG DRY-RUN op=set kind=")
	}
	code, shown, errs := h.run(t, "route", "show", "flash-luna6-opencode", "--pg", dsn)
	require.Equal(t, 0, code, errs)
	assert.Contains(t, shown, " note=by\\x20hand ", "the dry run wrote nothing")

	// the real run
	out, code, calls = runSeed(t, dir, "m7")
	assert.Equal(t, 1, code, "one route is no row, so the program ends 1 after writing the rest: %s", out)
	assert.Contains(t, out, "NOTES SKIP route=flash-mimo26-openrouter reason=enabled")
	assert.Contains(t, out, "NOTES DONE routes_written=6 routes_skipped=1 routes_missing=1 machine=m7 dry_run=false")
	assert.Contains(t, out, "NOTES MISSING route=flash-luna56-opencode")
	require.Len(t, writes(calls), 7)
	for _, c := range writes(calls) {
		assert.Equal(t, "set", c[1], "the program only sets notes: %v", c)
		assert.Equal(t, "--note", c[3], "%v", c)
		assert.NotContains(t, c, "--enabled", "the program enables and disables nothing: %v", c)
		assert.Equal(t, []string{"--as", "a1", "--pg", dsn}, c[5:], "the actor and the store flags ride every call: %v", c)
		code, wrote, errs := h.run(t, c...)
		require.Equal(t, 0, code, "%v: %s", c, errs)
		assert.Contains(t, wrote, "changed=note\n", "%v", c)
	}
	for name, want := range map[string][]string{
		"flash-luna6-opencode":          {"2\\x20ok\\x20of\\x2012"},
		"flash-luna6-openrouter":        {"4\\x20ok\\x20of\\x2014"},
		"flash-mimo26pro-openrouter":    {"3\\x20ok\\x20of\\x207", "1200\\x20s\\x20deadline"},
		"flash-nemotron-openrouter":     {"4\\x20ok\\x20of\\x2052", "48\\x20ended\\x20with\\x20no\\x20result"},
		"flash-gemini31lite-openrouter": {"34\\x20ok\\x20of\\x2058"},
		"flash-mercury-openrouter":      {"use\\x20mercury\\x20only\\x20direct", "10\\x20ok\\x20of\\x2016"},
	} {
		code, shown, errs := h.run(t, "route", "show", name, "--pg", dsn)
		require.Equal(t, 0, code, errs)
		assert.Contains(t, shown, " enabled=false ", "%s stays disabled", name)
		assert.NotContains(t, shown, "by\\x20hand", "%s: the note is replaced", name)
		assert.Contains(t, shown, "nova-tools#5101", name)
		for _, w := range want {
			assert.Contains(t, shown, w, name)
		}
	}
	_, shown, _ = h.run(t, "route", "show", "flash-mimo26-openrouter", "--pg", dsn)
	assert.Contains(t, shown, " enabled=true ")
	assert.Contains(t, shown, " note=- ", "a route that is on got no note")
	_, shown, _ = h.run(t, "machine", "show", "m7", "--pg", dsn)
	assert.Contains(t, shown, "held\\x201:46\\x20PM\\x20ET\\x202026-10-02:\\x20reads\\x20kernel-bound")
	assert.Contains(t, shown, " width=8 ", "the program changes no width")
	_, hist, _ := h.run(t, "route", "history", "flash-luna6-opencode", "--pg", dsn)
	assert.Equal(t, 2, strings.Count(hist, "HISTORY id="), "the add (disabled by hand, with its note) and the program's note, and no row from the dry run: %s", hist)
}

func TestTheSeedProgramRefusesWithoutTheMachineOrWithAnUnknownArgument(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for args, want := range map[string]string{"": "want the held machine's name first", "--dry-run": "want the held machine's name first"} {
		out, code, calls := runSeed(t, dir, strings.Fields(args)...)
		assert.Equal(t, 2, code, args)
		assert.Contains(t, out, want)
		assert.Empty(t, calls, "a refused run calls nothing")
	}
	out, code, calls := runSeed(t, dir, "m7", "--now")
	assert.Equal(t, 2, code)
	assert.Contains(t, out, "REFUSED: unknown argument --now")
	assert.Empty(t, calls)
}
