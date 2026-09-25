package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/sprintline"
)

// TestEvaluateOutputChangesXYAndAHandMarkedReceiptDoesNot is the #2679
// done-when for the numbers. x and y move when the evaluate stdout moves.
// A work-set whose :status marks a receipt done does not move them, and the
// same holds when the stdout is produced by running nova-work rather than
// by a file already in hand.
func TestEvaluateOutputChangesXYAndAHandMarkedReceiptDoesNot(t *testing.T) {
	cal := filepath.Join("testdata", "calibration.txt")
	open := filepath.Join("testdata", "open.tsv")

	marked := writeSet(t, "marked.sexp", `
(unit "a12-receipt" :status "done" :evidence "done receipt 2026-09-22")
(unit "tools-2547" :status "landed" :evidence "commit:abc")
(unit "gate-fix-holds" :status "open")
`)
	unmarked := writeSet(t, "open.sexp", `
(unit "a12-receipt" :status "open" :evidence "not a done receipt")
(unit "tools-2547" :status "open")
(unit "gate-fix-holds" :status "open")
`)
	if statusMarks(mustRead(t, marked)) == statusMarks(mustRead(t, unmarked)) {
		t.Fatal("the two work-sets do not differ under a :status done|landed count, so this test would not catch sprint-xy")
	}

	evalA := writeSet(t, "eval-a.txt", "SET OK units=42 ready=7 blocked=9 owned=0\nSET DONE done=26 percent=61\n")
	evalB := writeSet(t, "eval-b.txt", "SET OK units=40 ready=7 blocked=9 owned=0\nSET DONE done=27 percent=99\n")

	lineA := xy(t, "--evaluate-out", evalA, "--calibration-out", cal, "--open", open, "--set", marked)
	lineB := xy(t, "--evaluate-out", evalB, "--calibration-out", cal, "--open", open, "--set", marked)
	lineC := xy(t, "--evaluate-out", evalA, "--calibration-out", cal, "--open", open, "--set", unmarked)

	if lineA != "26/42 61% -> ~3h" {
		t.Fatalf("evaluate A printed %q, want 26/42 61%% -> ~3h", lineA)
	}
	if lineB != "27/40 99% -> ~3h" {
		t.Fatalf("evaluate output did not change x and y (and the tool's percent): got %q", lineB)
	}
	if lineA != lineC {
		t.Fatalf("a hand-marked receipt changed the line:\n marked   %s\n unmarked %s", lineA, lineC)
	}
	formula := (42-26)*10/3 + 12
	if strings.Contains(lineA, "~"+itoa(formula)+"m") {
		t.Fatalf("eta is the hand formula ~%dm: %s", formula, lineA)
	}

	// The same pair through the tool, not a saved stdout. The fake's text
	// is the evaluate output; rewriting the sexp must not move the line,
	// and rewriting what the fake prints must.
	bin := buildFake(t)
	log := filepath.Join(t.TempDir(), "log")
	t.Setenv("FAKE_LOG", log)
	status := filepath.Join("testdata", "status.txt")
	t.Setenv("FAKE_EVAL", evalA)
	before := mustRead(t, marked)
	fromTool := xy(t,
		"--set", marked,
		"--calibration-out", cal,
		"--open", status,
		"--nova-work", bin,
	)
	if fromTool != lineA {
		t.Fatalf("nova-work's evaluate stdout printed %q, want %q", fromTool, lineA)
	}
	if mustRead(t, marked) != before {
		t.Fatal("xy rewrote the work-set; sprint-xy flipped :status in place")
	}
	logged := mustRead(t, log)
	if !strings.Contains(logged, "--evaluate") || !strings.Contains(logged, marked) {
		t.Fatalf("nova-work was not run as set check --evaluate --file the sexp:\n%s", logged)
	}
	if strings.Contains(logged, "--write-status") {
		t.Fatalf("xy asked nova-work to rewrite :status:\n%s", logged)
	}
	t.Setenv("FAKE_EVAL", evalB)
	if got := xy(t, "--set", unmarked, "--calibration-out", cal, "--open", status, "--nova-work", bin); got != lineB {
		t.Fatalf("a new evaluate stdout with an open receipt printed %q, want %q", got, lineB)
	}
}

func TestStatusFractionIsNotXY(t *testing.T) {
	// status.txt leads with the sprint store's own fraction. The line's x/y
	// stay the evaluate count.
	bin := buildFake(t)
	t.Setenv("FAKE_LOG", filepath.Join(t.TempDir(), "log"))
	t.Setenv("FAKE_EVAL", mustAbs(t, filepath.Join("testdata", "evaluate.txt")))
	got := xy(t, "--set", mustAbs(t, filepath.Join("testdata", "set.sexp")), "--calibration-out", filepath.Join("testdata", "calibration.txt"), "--open", filepath.Join("testdata", "status.txt"), "--nova-work", bin)
	if strings.HasPrefix(got, "14/23 ") || strings.Contains(got, "~9h") {
		t.Fatalf("the sprint status fraction became the line: %s", got)
	}
	if got != "26/42 61% -> ~3h" {
		t.Fatalf("got %q, want the evaluate count and the calibrated wall", got)
	}
}

// TestProducerStatusVerboseOutputSuppliesOpenTasks feeds the stdout of
// a verbose sprint status as the producer on #2624 printed it, given as --open
// (the fraction, then sprint.Verbose): C/O/W rows, not TASK lines. Each row
// carries kind= and depends=. x/y stay the evaluate count. eta charges
// SUGGEST fix for the two fix tasks and waits on the cross-lane edge, so
// emma finishes at 180. The status line's own ~2.5h is not that.
func TestProducerStatusVerboseOutputSuppliesOpenTasks(t *testing.T) {
	status := mustAbs(t, filepath.Join("testdata", "status-verbose.txt"))
	raw := mustRead(t, status)
	if strings.Contains(raw, "TASK ") {
		t.Fatal("status-verbose.txt contains TASK lines; it must be the producer's verbose status, not a hand-written TASK fixture")
	}
	for _, want := range []string{
		"C=1 O=3 W=1",
		"Open gate-fix-holds ",
		"Working wait-verb ",
		"route=bench:space",
		"route=bench:vision",
		"kind=fix",
		"depends=gate-fix-holds",
		"depends=-",
		"splittable wait-verb ",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("producer output missing %q", want)
		}
	}
	tasks, err := sprintline.ParseStatus(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 4 {
		t.Fatalf("parsed %d tasks, want the four open rows and not the closed task or the splittable line: %+v", len(tasks), tasks)
	}
	wantTasks := []sprintline.Task{
		{ID: "gate-fix-holds", Kind: "fix", Owner: "johnny", State: "open", EstMinutes: 120},
		{ID: "wait-verb", Kind: "fix", Owner: "emma", State: "working", EstMinutes: 120, DependsOn: []string{"gate-fix-holds"}},
		{ID: "handed-space", Kind: "read", Route: "bench:space", State: "open", EstMinutes: 45},
		{ID: "handed-vision", Kind: "read", Route: "bench:vision", State: "open", EstMinutes: 45},
	}
	for i, w := range wantTasks {
		g := tasks[i]
		if g.ID != w.ID || g.Kind != w.Kind || g.Owner != w.Owner || g.Route != w.Route || g.State != w.State || g.EstMinutes != w.EstMinutes || strings.Join(g.DependsOn, ",") != strings.Join(w.DependsOn, ",") {
			t.Fatalf("task %d = %+v, want %+v", i, g, w)
		}
	}
	wall, err := sprintline.WallMinutes(tasks, map[string]int{"fix": 90})
	if err != nil {
		t.Fatal(err)
	}
	if wall != 180 {
		t.Fatalf("wall = %d, want 180 (fix charged at 90, emma waiting on johnny); the status line's uncalibrated ~2.5h is 150", wall)
	}

	bin := buildFake(t)
	t.Setenv("FAKE_LOG", filepath.Join(t.TempDir(), "log"))
	t.Setenv("FAKE_EVAL", mustAbs(t, filepath.Join("testdata", "evaluate.txt")))
	got := xy(t, "--set", mustAbs(t, filepath.Join("testdata", "set.sexp")), "--calibration-out", filepath.Join("testdata", "calibration.txt"), "--open", status, "--nova-work", bin)
	if strings.HasPrefix(got, "1/5 ") || strings.Contains(got, "~2.5h") {
		t.Fatalf("the sprint status line became the eta: %s", got)
	}
	if got != "26/42 61% -> ~3h" {
		t.Fatalf("got %q, want 26/42 61%% -> ~3h from evaluate and the calibrated cross-lane wall", got)
	}
}

// TestKindCalibrationAndCrossLaneDependencyChangeETA is the #2679 eta
// contract on producer rows. Both tasks store ~2h. kind=fix is charged the
// 90m SUGGEST, and depends= names the other lane, so the wall is 180 (~3h).
// Calibration without that edge is the parallel 90 (~1.5h). The edge without
// a measured kind keeps the stored 120+120 (~4h).
func TestKindCalibrationAndCrossLaneDependencyChangeETA(t *testing.T) {
	bin := buildFake(t)
	t.Setenv("FAKE_LOG", filepath.Join(t.TempDir(), "log"))
	t.Setenv("FAKE_EVAL", mustAbs(t, filepath.Join("testdata", "evaluate.txt")))
	set := mustAbs(t, filepath.Join("testdata", "set.sexp"))

	both := producerRows("fix", "gate-fix-holds")
	noEdge := producerRows("fix", "-")
	noCal := producerRows("read", "gate-fix-holds")
	for _, raw := range []string{both, noEdge, noCal} {
		if strings.Contains(raw, "TASK ") {
			t.Fatalf("the case is TASK lines, not producer rows:\n%s", raw)
		}
	}
	if got := xyStatus(t, bin, set, both); got != "26/42 61% -> ~3h" {
		t.Fatalf("kind calibration and the cross-lane dependency printed %q, want 26/42 61%% -> ~3h", got)
	}
	if got := xyStatus(t, bin, set, noEdge); got != "26/42 61% -> ~1.5h" {
		t.Fatalf("calibration without the dependency printed %q, want ~1.5h", got)
	}
	if got := xyStatus(t, bin, set, noCal); got != "26/42 61% -> ~4h" {
		t.Fatalf("the dependency without calibration printed %q, want ~4h", got)
	}
}

// producerRows is two Open rows the sprint status producer prints, plus the
// fraction and the C/O/W line xy must not count. kind is both tasks' kind.
// secondDepends is what the emma task waits on; "-" is no edge.
func producerRows(kind, secondDepends string) string {
	return "14/23 61% -> ~9h\n" +
		"C=0 O=2 W=0\n" +
		"Open gate-fix-holds mas-bandwidth/nova-tools#1 owner=johnny route=- est=~2h kind=" + kind + " depends=-\n" +
		"Open wait-verb mas-bandwidth/nova-tools#2 owner=emma route=- est=~2h kind=" + kind + " depends=" + secondDepends + "\n"
}

func xyStatus(t *testing.T, bin, set, status string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "status.txt")
	if err := os.WriteFile(path, []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	return xy(t, "--set", set, "--calibration-out", filepath.Join("testdata", "calibration.txt"), "--open", path, "--nova-work", bin)
}

// TestXYRefusesTheDeletedNovaPulseSource is #3801: nova-pulse is deleted, so
// xy never runs it. --store, --name and --nova-pulse are usage errors, and the
// calibration and the open rows come from files only.
func TestXYRefusesTheDeletedNovaPulseSource(t *testing.T) {
	for _, flag := range [][]string{
		{"--store", "example.invalid:6399"},
		{"--name", "s1"},
		{"--nova-pulse", "nova-pulse"},
	} {
		args := append([]string{"xy", "--evaluate-out", filepath.Join("testdata", "evaluate.txt")}, flag...)
		code, _, stderr := runCLI(args...)
		if code != 2 {
			t.Fatalf("xy %s exit %d, want 2 (the nova-pulse source is deleted); stderr %s", flag[0], code, stderr)
		}
	}
	code, _, stderr := runCLI("xy", "--evaluate-out", filepath.Join("testdata", "evaluate.txt"))
	if code != 2 || strings.Contains(stderr, "nova-pulse") {
		t.Fatalf("xy without --calibration-out/--open exit %d, stderr %q; want 2 naming the two files and not nova-pulse", code, stderr)
	}
}

func TestXYRefusesWithoutItsSources(t *testing.T) {
	code, _, stderr := runCLI("xy")
	if code != 2 {
		t.Fatalf("xy with no sources exit %d, want 2; stderr %s", code, stderr)
	}
	for _, want := range []string{"--evaluate-out", "--calibration-out", "--open", "refusing to count :status", "nova-sprint help"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("refusal %q does not name %q", stderr, want)
		}
	}
	if strings.Count(stderr, "\n") != 1 {
		t.Errorf("refusal is not one line:\n%s", stderr)
	}
}

// TestTheCommandReferenceXYExampleRuns runs the xy example under
// `### xy` in docs/CLI.md from the repo root and checks the line it prints.
func TestTheCommandReferenceXYExampleRuns(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, tail, ok := strings.Cut(string(raw), "\n### xy\n")
	if !ok {
		t.Fatal("docs/CLI.md has no ### xy subsection")
	}
	var cmd, want string
	for _, line := range strings.Split(tail, "\n") {
		if cmd == "" && strings.HasPrefix(line, "nova-sprint xy ") {
			cmd = line
			continue
		}
		if cmd != "" && line != "" && !strings.HasPrefix(line, "#") {
			want = line
			break
		}
	}
	if cmd == "" || want == "" {
		t.Fatalf("### xy example not found (cmd %q, want %q)", cmd, want)
	}
	t.Chdir(root)
	if got := xy(t, strings.Fields(cmd)[2:]...); got != want {
		t.Fatalf("%s printed %q, docs/CLI.md says %q", cmd, got, want)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func xy(t *testing.T, args ...string) string {
	t.Helper()
	code, stdout, stderr := runCLI(append([]string{"xy"}, args...)...)
	if code != 0 {
		t.Fatalf("xy %s exit %d\nstderr: %s", strings.Join(args, " "), code, stderr)
	}
	return strings.TrimSuffix(stdout, "\n")
}

func runCLI(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func writeSet(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func statusMarks(sexp string) int {
	return strings.Count(sexp, `:status "done"`) + strings.Count(sexp, `:status "landed"`)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// buildFake compiles one stdlib program and returns its path. The test
// points --nova-work at it; it answers set check from FAKE_EVAL.
func buildFake(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module example.invalid/fake\n\ngo 1.26.6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(fakeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "nova-sprint-fake")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = src
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building the nova-work fake: %v\n%s", err, out)
	}
	return bin
}

const fakeSource = `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	if log := os.Getenv("FAKE_LOG"); log != "" {
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Fprintf(f, "%s\n", strings.Join(os.Args[1:], " "))
		f.Close()
	}
	switch {
	case len(os.Args) >= 3 && os.Args[1] == "set" && os.Args[2] == "check":
		dump(os.Getenv("FAKE_EVAL"))
	default:
		fmt.Fprintf(os.Stderr, "fake: unexpected %s\n", strings.Join(os.Args[1:], " "))
		os.Exit(2)
	}
}

func dump(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Stdout.Write(b)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		os.Stdout.Write([]byte("\n"))
	}
}
`
