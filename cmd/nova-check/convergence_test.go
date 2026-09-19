package main

// The convergence verb at the command line: the refusals a first run hits, the
// lines one tick prints, and the streak that is the only exit 1. The forge and
// the checkout are fakes named by --gh and --git, so no test here touches a
// network, and the clock is --now, so no test here waits for one.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBin writes one executable fake and returns its path. Fakes are shell
// scripts, so the tests that use one are skipped where there is no shell --
// the verb's own machinery is covered by internal/converge either way.
func fakeBin(t *testing.T, dir, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the forge and git fakes are shell scripts; internal/converge covers the same paths with Go fakes")
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// convFixture is one whole reading's worth of sources at the command line.
type convFixture struct {
	dir  string
	args []string
}

const convNow = "2026-09-18T12:00:00Z"
const convSince = "2026-09-18T00:00:00Z"

func newConvFixture(t *testing.T) *convFixture {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	ledger := write("ledger.md", "| Tool | Case | Result |\n|---|---|---|\n| a | one | PASS |\n| b | two | TODO |\n")
	retired := write("retired.md", "Retired 2026-09-18 by Rowan.\n\n| script | what |\n| --- | --- |\n| `board.sh` | a board |\n")
	// One open edge, found before the window opened, so EDGES reads flat and the
	// streak test below can move one stream at a time; and one clean receipt
	// inside the window, so there is a round to divide by.
	write("receipts/a.json", `{"tool":"nova-check","verb":"links","by":"Stella","at":"2026-09-17T09:00:00Z","ok":true,"notes":"real work. Edges: one"}`)
	write("receipts/b.json", `{"tool":"nova-check","verb":"kernel","by":"Rowan","at":"2026-09-18T09:00:00Z","ok":true,"notes":"real work, nothing found"}`)
	write("bin/one.sh", "echo 1\n")
	write("bin/two.sh", "echo 2\n")

	// The fake forge: two open pull requests, one merged batch, in the shape
	// `gh pr list --json` answers in.
	openJSON := `[{"number":1,"title":"open one","body":"","state":"OPEN","createdAt":"2026-09-17T08:00:00Z","closedAt":"","mergedAt":""}]`
	closedJSON := `[{"number":3,"title":"integration-11a: a batch","body":"round 2","state":"MERGED",` +
		`"createdAt":"2026-09-18T01:00:00Z","closedAt":"2026-09-18T03:00:00Z","mergedAt":"2026-09-18T03:00:00Z"},` +
		`{"number":5,"title":"integration-10a: before","body":"round 5","state":"MERGED",` +
		`"createdAt":"2026-09-17T14:00:00Z","closedAt":"2026-09-17T18:00:00Z","mergedAt":"2026-09-17T18:00:00Z"}]`
	gh := fakeBin(t, dir, "gh", `
case "$*" in
  *"--state open"*) cat <<'EOF'
`+openJSON+`
EOF
  ;;
  *) cat <<'EOF'
`+closedJSON+`
EOF
  ;;
esac
`)

	return &convFixture{dir: dir, args: []string{"convergence",
		"--repo", "mas-bandwidth/nova-tools",
		"--ledger", ledger,
		"--receipts", filepath.Join(dir, "receipts"),
		"--retired", retired,
		"--bin", filepath.Join(dir, "bin"),
		"--since", convSince,
		"--now", convNow,
		"--gh", gh,
	}}
}

func (f *convFixture) run(t *testing.T, extra ...string) (int, string, string) {
	t.Helper()
	return runCheck(t, append(append([]string(nil), f.args...), extra...)...)
}

// 17
func TestConvergenceRefusesAMissingFlag(t *testing.T) {
	required := []string{"repo", "ledger", "receipts", "retired", "since"}
	for _, missing := range required {
		f := newConvFixture(t)
		var args []string
		for i := 0; i < len(f.args); i++ {
			if f.args[i] == "--"+missing {
				i++
				continue
			}
			args = append(args, f.args[i])
		}
		exit, stdout, stderr := runCheck(t, args...)
		if exit != 2 {
			t.Errorf("--%s omitted exited %d, want 2", missing, exit)
		}
		if stdout != "" {
			t.Errorf("--%s omitted printed a reading: %q", missing, stdout)
		}
		if !strings.Contains(stderr, "--"+missing+" is required; refusing to guess") {
			t.Errorf("--%s omitted said: %q", missing, stderr)
		}
	}

	exit, _, stderr := runCheck(t, "convergence")
	if exit != 2 {
		t.Fatalf("a bare convergence exited %d, want 2", exit)
	}
	for _, missing := range required {
		if !strings.Contains(stderr, "--"+missing+" is required") {
			t.Errorf("one run must name every missing flag; %s was not named:\n%s", missing, stderr)
		}
	}
	// The --ledger hint is this verb's own: the pit-stop ledger, not the corpus
	// ledger the same flag names on `corpus`.
	if !strings.Contains(stderr, "pit-stop ledger") {
		t.Errorf("the --ledger hint names the wrong document:\n%s", stderr)
	}
}

// 18
func TestConvergenceRefusesASinceItCannotRead(t *testing.T) {
	for _, bad := range []string{"yesterday", "2026-13-40T00:00:00Z", "2026-09-19T00:00:00Z"} {
		f := newConvFixture(t)
		args := append([]string(nil), f.args...)
		for i := range args {
			if args[i] == convSince {
				args[i] = bad
			}
		}
		exit, stdout, stderr := runCheck(t, args...)
		if exit != 2 {
			t.Errorf("--since %s exited %d, want 2", bad, exit)
		}
		if strings.Contains(stdout, "CONVERGENCE") {
			t.Errorf("--since %s printed a stream line: %q", bad, stdout)
		}
		if !strings.Contains(stderr, "--since") {
			t.Errorf("--since %s said: %q", bad, stderr)
		}
	}
}

// One tick, end to end, through the fakes.
func TestConvergencePrintsATickAtTheCommandLine(t *testing.T) {
	f := newConvFixture(t)
	exit, stdout, stderr := f.run(t)
	if exit != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 8 {
		t.Fatalf("want seven stream lines and one verdict, got %d:\n%s", len(lines), stdout)
	}
	for _, want := range []string{"CONVERGENCE LANDING ", "CONVERGENCE SCRIPTS ", "CONVERGENCE PRS ",
		"CONVERGENCE EDGES ", "CONVERGENCE LEDGER "} {
		if !strings.Contains(stdout, want) {
			t.Errorf("no %q in:\n%s", want, stdout)
		}
	}
	// CLASSES and FLEET were given no source, so they are absent and named.
	if !strings.Contains(stdout, "trend=absent measure=class-test-index-entries source=--repo-dir") {
		t.Errorf("CLASSES was not absent:\n%s", stdout)
	}
	if !strings.Contains(stdout, "absent=CLASSES,FLEET") {
		t.Errorf("the verdict does not name the absent streams:\n%s", stdout)
	}
	// One batch at two rounds now, one at five before --since.
	if !strings.Contains(stdout, "CONVERGENCE LANDING now=2 before=5 ratio=0.40 trend=contracting") {
		t.Errorf("the LANDING line is not the reading of the fixture:\n%s", stdout)
	}
}

// --json is the same reading, and only the object.
func TestConvergenceJSONIsTheWholeReading(t *testing.T) {
	f := newConvFixture(t)
	exit, stdout, stderr := f.run(t, "--json")
	if exit != 0 {
		t.Fatalf("exit %d: %s", exit, stderr)
	}
	if strings.Contains(stdout, "CONVERGENCE") {
		t.Fatalf("--json printed a line as well as the object:\n%s", stdout)
	}
	var back struct {
		Streams []struct {
			Name  string   `json:"stream"`
			Now   *float64 `json:"now"`
			Trend string   `json:"trend"`
		} `json:"streams"`
		Verdict string   `json:"verdict"`
		Absent  []string `json:"absent"`
	}
	if err := json.Unmarshal([]byte(stdout), &back); err != nil {
		t.Fatalf("--json did not print one JSON object: %v\n%s", err, stdout)
	}
	if len(back.Streams) != 7 {
		t.Errorf("the object holds %d streams, want 7", len(back.Streams))
	}
	if len(back.Absent) != 2 {
		t.Errorf("absent=%v, want the two streams with no source", back.Absent)
	}
}

// 15, at the command line: the streak is the only exit 1, and it lives in --state.
func TestConvergenceExitsOneOnTheSecondConsecutiveWidening(t *testing.T) {
	f := newConvFixture(t)
	state := filepath.Join(f.dir, "state.json")

	// Tick one remembers the reading.
	if exit, _, stderr := f.run(t, "--state", state); exit != 0 {
		t.Fatalf("the first tick exited %d: %s", exit, stderr)
	}
	// A row is added to the ledger and nobody has closed it: LEDGER, whose
	// before comes from the remembered tick, widens once -- a WARN, not a red.
	owe := func(row string) {
		t.Helper()
		fh, err := os.OpenFile(filepath.Join(f.dir, "ledger.md"), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fh.WriteString(row); err != nil {
			t.Fatal(err)
		}
		if err := fh.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// Each tick is a tick of the clock: the same instant read twice is one tick,
	// so the ticks below are an hour apart.
	owe("| c | three | TODO |\n")
	exit, stdout, _ := f.run(t, "--state", state, "--now", "2026-09-18T13:00:00Z")
	if exit != 0 {
		t.Fatalf("one widening tick exited %d, want 0 with a WARN:\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, "CONVERGENCE WARN") || !strings.Contains(stdout, "widening=LEDGER") {
		t.Fatalf("one widening tick did not warn:\n%s", stdout)
	}
	// And another: the same stream widening twice running is the red.
	owe("| d | four | TODO |\n")
	exit, stdout, _ = f.run(t, "--state", state, "--now", "2026-09-18T14:00:00Z")
	if exit != 1 {
		t.Fatalf("two consecutive widening ticks exited %d, want 1:\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, "widening=LEDGER") {
		t.Errorf("the red does not name the stream:\n%s", stdout)
	}

	// With no --state nothing is remembered, so the same two ticks are both 0.
	for i, when := range []string{"2026-09-18T15:00:00Z", "2026-09-18T16:00:00Z"} {
		if exit, _, _ := f.run(t, "--now", when); exit != 0 {
			t.Errorf("tick %d with no --state exited %d", i, exit)
		}
	}

	// And the same tick read twice is one tick: a second invocation over the
	// same window must not turn a WARN into a red on a reading nobody took.
	same := filepath.Join(f.dir, "same.json")
	for i := 0; i < 3; i++ {
		exit, stdout, _ := f.run(t, "--state", same, "--now", "2026-09-18T17:00:00Z")
		if exit != 0 {
			t.Fatalf("reading one tick %d times went red:\n%s", i+1, stdout)
		}
	}
}

// The CLASSES stream through a fake git.
func TestConvergenceReadsClassesThroughAFakeGit(t *testing.T) {
	f := newConvFixture(t)
	repoDir := filepath.Join(f.dir, "repo")
	spec := func(n int) string {
		var b strings.Builder
		b.WriteString("## The class tests\n\n")
		for i := 0; i < n; i++ {
			b.WriteString("### `class-" + strings.Repeat("x", i%3+1) + strings.Repeat("y", i) + "` — a rule\n\n")
		}
		return b.String()
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "docs", "SPEC-CI.md"), []byte(spec(4)), 0o644); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(f.dir, "older-spec.md")
	if err := os.WriteFile(older, []byte(spec(2)), 0o644); err != nil {
		t.Fatal(err)
	}
	git := fakeBin(t, f.dir, "git", `
case "$*" in
  *rev-list*) echo abc123456789 ;;
  *show*) cat `+older+` ;;
esac
`)
	exit, stdout, stderr := f.run(t, "--repo-dir", repoDir, "--git", git)
	if exit != 0 {
		t.Fatalf("exit %d: %s", exit, stderr)
	}
	if !strings.Contains(stdout, "CONVERGENCE CLASSES now=4 before=2 ratio=2 trend=contracting") {
		t.Errorf("the CLASSES line is not the reading of the fixture:\n%s", stdout)
	}
	if !strings.Contains(stdout, "rev=abc123456789") {
		t.Errorf("the CLASSES line does not name the revision it read:\n%s", stdout)
	}
}

// A forge that will not answer is exit 2 and prints no reading.
func TestConvergenceRefusesAForgeThatWillNotAnswer(t *testing.T) {
	f := newConvFixture(t)
	bad := fakeBin(t, f.dir, "gh-broken", "echo 'gh: could not resolve to a Repository' 1>&2\nexit 1\n")
	exit, stdout, stderr := f.run(t, "--gh", bad)
	if exit != 2 {
		t.Fatalf("a forge that refused exited %d, want 2", exit)
	}
	if strings.Contains(stdout, "CONVERGENCE") {
		t.Errorf("a partial reading was printed:\n%s", stdout)
	}
	if !strings.Contains(stderr, "nova-check convergence:") || !strings.Contains(stderr, "gh pr list") {
		t.Errorf("the refusal does not name the child: %q", stderr)
	}
}

// A --timeout of zero or less is a wait with no end.
func TestConvergenceRefusesATimeoutThatIsNotOne(t *testing.T) {
	f := newConvFixture(t)
	for _, bad := range []string{"0", "-5"} {
		exit, _, stderr := f.run(t, "--timeout", bad)
		if exit != 2 || !strings.Contains(stderr, "--timeout") {
			t.Errorf("--timeout %s exited %d saying %q", bad, exit, stderr)
		}
	}
}
