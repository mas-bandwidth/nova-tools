package main

// The docs/TESTS.md `### First run` transcript for nova-pulse, EXECUTED rather than
// read. This package had no such test: its transcript was written by hand from real
// runs and then drifted -- `POOL OK` grew a `prs=` and a `work=` field and the
// document went on showing the old line, which is exactly the claim about a message
// that has since moved that docs/ONBOARDING.md point 5(c) exists to close.
//
// Nothing here reaches a network, a model or another machine. The fixture pulse root
// is copied into t.TempDir() with its stub `nova-swarm` on PATH, the queue, roots,
// home and page directories are empty ones this test makes, and `status --html` runs
// against the injected bench reader that status_html_test.go already pins. The one
// verb family with no transcript -- `fleet` past `registry` -- is the one whose only
// seam is the `--ssh` program path; see the note in docs/TESTS.md and the history at
// the head of fleet_survey_test.go.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// pulseWorld is the one directory the whole transcript's `./x` paths point at: a
// copy of the example-pulse fixture -- cards.tsv, the three cards, and bin/ with
// the stub nova-swarm -- plus the empty queue, roots, home and page directories
// and the two small tables the newer verbs read. It is a COPY because `launch`
// and `cut` write, and a transcript must not add a file to testdata.
func pulseWorld(t *testing.T) string {
	t.Helper()
	world := t.TempDir()
	src, err := filepath.Abs(filepath.Join("testdata", "example-pulse"))
	if err != nil {
		t.Fatal(err)
	}
	if err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(world, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		// The mode comes along: bin/nova-swarm is the stub launch execs, and a
		// copy without its exec bit would make `launch` a test of PATH instead.
		return os.WriteFile(target, raw, info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"queue", "roots", "home", "page"} {
		if err := os.MkdirAll(filepath.Join(world, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(world, "machines.tsv"), strings.Join([]string{
		"# name\tssh\tos/arch\troles\tseat\tcores\tnotes",
		"hulk\thulk\tlinux/x64\tbench\tswarm-hulk\t64\t-",
		"batman\tbatman\tdarwin/amd64\trunner\t-\t8\t2019 iMac Pro; CI-only",
		"",
	}, "\n"))
	write(t, filepath.Join(world, "benches.tsv"), "alpha\tfake-alpha\t/tmp/alpha\t-\nbeta\tfake-beta\t/tmp/beta\t-\n")
	return world
}

// localizePulse rewrites one documented command line into this run's world.
// Three shapes are rewritten and nothing else: a `./x` path becomes world/x; a
// bare `.` after `--root` becomes the world itself, which is what `--root .`
// means to a reader standing in their own pulse root; and `/Users/me/bench` is
// the absolute `--home` that `hygiene` refuses to take relative. Paths under
// `cmd/nova-pulse/testdata/` are left exactly as written, because the test runs
// from the repository root and that is where a reader types them from.
func localizePulse(world string, fields []string) []string {
	out := append([]string(nil), fields...)
	for i, a := range out {
		switch {
		case a == "/Users/me/bench":
			out[i] = filepath.Join(world, "home")
		case a == "." && i > 0 && out[i-1] == "--root":
			out[i] = world
		default:
			if rest, ok := strings.CutPrefix(a, "./"); ok {
				out[i] = filepath.Join(world, rest)
			}
		}
	}
	return out
}

// TestTESTSFirstRunMatchesWhatTheToolPrints runs every `$ nova-pulse` line of the
// transcript and compares each line under it by SHAPE -- the two-token event
// prefix and the field names in order. Values are a run's own business and are
// deliberately not compared, so the document stays a document.
func TestTESTSFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	// The fixture's stub nova-swarm -- the one `launch` hands its batch to -- is a
	// `#!/bin/sh` script, so the launch lines cannot run on Windows. This package
	// already skips its other shell-backed tests there for the same reason.
	if runtime.GOOS == "windows" {
		t.Skip("the example-pulse fixture's stub nova-swarm is a POSIX shell script; skipping on windows")
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(repo, "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-pulse")
	if err != nil {
		t.Fatal(err)
	}
	world := pulseWorld(t)
	// The two benches of benches.tsv, in file order, with the counts the page and
	// the metrics row fold. No ssh starts: this is status_html_test.go's seam.
	withFleetReader(t, testFleetReader(t,
		pulse.BenchReading{Name: "alpha", Live: 3, Cores: 8, Load: 1, FreeGB: 80, MemGB: 60, Allowed: 5},
		pulse.BenchReading{Name: "beta", Live: 1, Cores: 4, Load: 2, FreeGB: 40, MemGB: 20, Allowed: 2},
	))
	// The stub nova-swarm `launch` hands its batch to, ahead of any real one.
	t.Setenv("PATH", filepath.Join(world, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	var printed map[string]bool
	seen := map[string]int{}
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ nova-pulse "); ok {
			fields, err := onboarding.Fields(cmd)
			if err != nil {
				t.Fatalf("cannot split the TESTS.md command %q: %v", line, err)
			}
			// Where a reader is standing when they type this line. The `cut` and
			// `pool` lines name paths under cmd/nova-pulse/testdata/, which are
			// written from the repository root -- and `pool` reads a sources.tsv
			// whose roadmap path is relative to it too. Every other line is typed
			// in a pulse root of your own, and it MUST be the world here: the
			// fixture's stub nova-swarm appends its argv to
			// ./.nova-swarm-argv.log, so a `launch` run from the repository root
			// leaves that file in the tree -- the defect class docs/SPEC-CI.md
			// already names once, and one this test found in itself.
			cwd := world
			for _, a := range fields {
				if strings.HasPrefix(a, "cmd/nova-pulse/testdata/") {
					cwd = repo
					break
				}
			}
			if err := os.Chdir(cwd); err != nil {
				t.Fatal(err)
			}
			_, stdout, stderr := invokePulse(t, localizePulse(world, fields)...)
			// The exit code is NOT the test here: `launch` into too few slots is
			// exit 2 and is the first line of the transcript on purpose, a
			// refusal a first run should meet. What a line may never do is print
			// nothing at all.
			printed = map[string]bool{}
			for _, out := range strings.Split(stdout+"\n"+stderr, "\n") {
				if s := onboarding.Shape(out); s != "" {
					printed[s] = true
				}
			}
			if len(printed) == 0 {
				t.Fatalf("the TESTS.md command %q printed no event line.\nstdout: %s\nstderr: %s", line, stdout, stderr)
			}
			continue
		}
		s := onboarding.Shape(line)
		if s == "" {
			continue
		}
		if printed == nil {
			t.Fatalf("transcript line before any command: %q", line)
		}
		if !printed[s] {
			t.Errorf("TESTS.md line\n  %s\nhas shape %q, which this tool never prints. Re-run the command and paste what it said.", line, s)
		}
		seen[strings.Join(strings.Fields(s)[:2], " ")]++
	}
	// One entry per verb the transcript is expected to carry, so a verb that
	// loses its transcript fails here instead of quietly going undocumented.
	for prefix, want := range map[string]int{
		"PULSE REFUSED": 1, "PULSE OK": 2, "CUT ROUTE": 2, "CUT OK": 1, "POOL OK": 1,
		"PROGRESS cards=0": 1, "ESTIMATE remaining_cards=0": 1, "REAP roots=1": 1,
		"HYGIENE bench-a": 1, "MACHINE hulk": 2, "MACHINE batman": 1, "STATUS HTML": 1,
	} {
		if seen[prefix] != want {
			t.Errorf("the TESTS.md First run shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}
