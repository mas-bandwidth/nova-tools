package pulse

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// fakeSwarm puts a fake nova-swarm on PATH that records each invocation's argv, one line
// per run, and exits 0. It was a `#!/bin/sh` script, which Windows does not execute: the
// lookup fell through to a nova-swarm that is not installed on a runner and the launch
// refused with "executable file not found".
func fakeSwarm(t *testing.T, argvLog string) {
	t.Helper()
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-swarm", fakeSpec{Log: argvLog})
}

// writeCards writes cards.tsv with n cards under a root, one model, and returns the card
// paths in order.
func writeCards(t *testing.T, root string, n int) (string, []string) {
	t.Helper()
	cardsDir := filepath.Join(root, "src")
	if err := os.MkdirAll(cardsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	paths := make([]string, n)
	for i := 0; i < n; i++ {
		label := "card-" + strings.Repeat("x", 0) + string(rune('a'+i))
		path := filepath.Join(cardsDir, label)
		if err := os.WriteFile(path, []byte("RESULT "+label+" sha=000000000000\nbody "+label+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths[i] = path
		sb.WriteString(label + "\t-\tpro\t" + path + "\n")
	}
	cards := filepath.Join(root, "cards.tsv")
	if err := os.WriteFile(cards, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return cards, paths
}

func runLaunch(t *testing.T, in LaunchInput) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	in.Stdout = &out
	in.Stderr = &errb
	code := Launch(in)
	return code, out.String(), errb.String()
}

func pulseID(t *testing.T, out string) string {
	t.Helper()
	for _, f := range strings.Fields(out) {
		if strings.HasPrefix(f, "id=") {
			return strings.TrimPrefix(f, "id=")
		}
	}
	t.Fatalf("no id= field in %q", out)
	return ""
}

func TestLaunchRefusesUnderSlots(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 8)

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Now: func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
	})

	if code != 2 {
		t.Fatalf("exit=%d, want 2; stderr=%s", code, errb)
	}
	if errb != "PULSE REFUSED UNDER-SLOTS cards=8 free=4 (pass --queue, or wait)\n" {
		t.Fatalf("stderr=%q", errb)
	}
	if out != "" {
		t.Fatalf("stdout=%q, want empty", out)
	}
	if raw, err := os.ReadFile(argvLog); err == nil && strings.TrimSpace(string(raw)) != "" {
		t.Fatalf("zero batch runs, got %q", raw)
	}
	if _, err := os.Stat(filepath.Join(root, "queue.tsv")); err == nil {
		t.Fatalf("no queue.tsv written on a refusal")
	}
}

// Red for issue #630: launch admits the cards.tsv through the CARD form of nova-swarm
// batch -- `--id <pulse> --cards <tsv> --deadline <s> --runner <cmd> --root <dir>` -- and
// prints a PULSE line. The POOL form it used to call (`--pool --tasks --label`) wants
// --files and --tokens, which no launch flag supplies, and the swarm refuses it with the
// two lines the issue quotes. The fake below answers the pool form with exactly those
// lines on stderr (matching the real binary's own wantCount/tokens refusals) and the
// cards form with exit 0.
func TestLaunchAdmitsThroughCardsForm(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-swarm", fakeSpec{
		Log: argvLog,
		Rules: []fakeRule{
			{
				Arg:    2,
				Equals: "--pool",
				Stderr: "nova-swarm batch: --files is required and is at least 1, got 0; it wants the file budget every job in this batch carries; refusing to guess\n" +
					"nova-swarm batch: --tokens is required; it wants a token budget for this job, or the word `unmetered` when this provider has no live accounting and the deadline is the only stop; refusing to guess",
				Exit: 2,
			},
		},
		Default: fakeRule{Exit: 0},
	})
	cards, _ := writeCards(t, root, 6)

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 6, Deadline: "600",
		Now: func() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) },
	})

	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	id := pulseID(t, out)
	if !strings.Contains(out, "PULSE OK id="+id+" n=6 free-before=6 queued=0 batches=1 deadline=600") {
		t.Fatalf("PULSE line wrong: %q", out)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))
	argv := strings.Fields(line)
	if argv[0] != "nova-swarm" || argv[1] != "batch" {
		t.Fatalf("the fake nova-swarm recorded %q; want it to lead `nova-swarm batch`", line)
	}
	if !strings.Contains(line, "--id "+id) {
		t.Fatalf("argv lacks --id %s: %q", id, line)
	}
	if !strings.Contains(line, "--cards ") {
		t.Fatalf("argv lacks --cards: %q", line)
	}
	if !strings.Contains(line, "--deadline 600") {
		t.Fatalf("argv lacks --deadline 600: %q", line)
	}
	if !strings.Contains(line, "--runner "+nativeRunner) {
		t.Fatalf("argv lacks --runner %s: %q", nativeRunner, line)
	}
	if !strings.Contains(line, "--root "+root) {
		t.Fatalf("argv lacks --root %s: %q", root, line)
	}
	// The --cards file is the admitted cards as their own TSV: all six rows, in order.
	cardsTSV := ""
	for i, a := range argv {
		if a == "--cards" && i+1 < len(argv) {
			cardsTSV = argv[i+1]
		}
	}
	if cardsTSV == "" {
		t.Fatalf("no --cards tsv in argv: %q", line)
	}
	got, err := os.ReadFile(cardsTSV)
	if err != nil {
		t.Fatalf("--cards %s unreadable: %v", cardsTSV, err)
	}
	src, err := os.ReadFile(cards)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimRight(string(src), "\n") + "\n"; string(got) != want {
		t.Fatalf("--cards tsv does not carry the cards in order:\n%s", got)
	}
}

// launch-carries-the-file-budget (issue #869): `nova-swarm batch` refuses an admission that
// carries no file budget -- "--files is required and is at least 1, got 0" -- so the wired
// launch names the configured budget on the batch argv. The mutation that matters: the flag
// dropped, which is the refusal that stopped the first tick of the switch.
func TestLaunchCarriesTheFileBudget(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 2)

	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120", Files: 12,
		Now: func() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))
	if !strings.Contains(line, "--files 12") {
		t.Errorf("argv lacks the configured file budget --files 12: %s", line)
	}
}

// A launch given no budget at all still names one: the documented default, never a zero the
// swarm reads as "refusing to guess".
func TestLaunchWithoutAConfiguredBudgetUsesTheDefault(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 1)

	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 2, Deadline: "60",
		Now: func() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))
	if want := "--files " + strconv.Itoa(DefaultLaunchFiles); !strings.Contains(line, want) {
		t.Errorf("argv lacks the default file budget %q: %s", want, line)
	}
}

func TestLaunchQueuesRemainder(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, paths := writeCards(t, root, 8)

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120", Queue: true,
		Now: func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
	})

	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	id := pulseID(t, out)
	if !strings.Contains(out, "n=8 free-before=4 queued=4 batches=1 deadline=120") {
		t.Fatalf("PULSE OK line wrong: %q", out)
	}

	// One batch run in the CARD form, holding exactly the first four cards in cards.tsv
	// order: the admitted cards become their own cards.tsv under <root>/cards/<id>/.
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("one batch run, got %d:\n%s", len(lines), raw)
	}
	argv := strings.Fields(lines[0])
	if argv[0] != "nova-swarm" || argv[1] != "batch" {
		t.Fatalf("the fake nova-swarm recorded %q; want it to lead `nova-swarm batch`", lines[0])
	}
	if !strings.Contains(lines[0], "--id "+id) {
		t.Fatalf("argv lacks --id %s: %q", id, lines[0])
	}
	if !strings.Contains(lines[0], "--deadline 120") {
		t.Fatalf("argv lacks --deadline 120: %q", lines[0])
	}
	if !strings.Contains(lines[0], "--runner "+nativeRunner) {
		t.Fatalf("argv lacks --runner %s: %q", nativeRunner, lines[0])
	}
	if !strings.Contains(lines[0], "--root "+root) {
		t.Fatalf("argv lacks --root %s: %q", root, lines[0])
	}
	if !strings.Contains(lines[0], "--then nova-pulse harvest --id "+id+" --root "+root) {
		t.Fatalf("argv lacks --then harvest: %q", lines[0])
	}
	cardsTSV := ""
	for i, a := range argv {
		if a == "--cards" && i+1 < len(argv) {
			cardsTSV = argv[i+1]
		}
	}
	if cardsTSV == "" {
		t.Fatalf("no --cards tsv in argv: %q", lines[0])
	}
	got, err := os.ReadFile(cardsTSV)
	if err != nil {
		t.Fatalf("--cards %s unreadable: %v", cardsTSV, err)
	}
	src, err := os.ReadFile(cards)
	if err != nil {
		t.Fatal(err)
	}
	srcLines := strings.Split(strings.TrimRight(string(src), "\n"), "\n")
	if len(srcLines) != 8 {
		t.Fatalf("source cards.tsv holds %d rows, want 8", len(srcLines))
	}
	if want := strings.Join(srcLines[:4], "\n") + "\n"; string(got) != want {
		t.Fatalf("--cards tsv does not hold the first four cards in order:\n%s", got)
	}

	// queue.tsv holds the other four cards.
	q, err := os.ReadFile(filepath.Join(root, "queue.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	qLines := strings.Split(strings.TrimSpace(string(q)), "\n")
	if len(qLines) != 4 {
		t.Fatalf("queue.tsv holds %d rows, want 4: %q", len(qLines), q)
	}
	for i, p := range paths[4:] {
		if !strings.Contains(qLines[i], p) {
			t.Fatalf("queue.tsv row %d lacks card %s", i, p)
		}
	}

	// pulses/<id>.tsv names the batch.
	pulses, err := os.ReadFile(filepath.Join(root, "pulses", id+".tsv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pulses), "pulse-"+id+"\t4") {
		t.Fatalf("pulses/%s.tsv does not name the batch: %q", id, pulses)
	}
}

// realSwarm builds the nova-swarm binary from this revision and returns a directory
// holding a shim named nova-swarm that records each invocation's argv before exec'ing
// the real thing, and a runner shim that writes each card's RESULT.md. The bundled
// example's fake exits 0 for any argv, so only the real binary can say whether the
// wired launch's argv is one the swarm accepts.
func realSwarm(t *testing.T) (binDir, argvLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the argv-recording shim is a shell script; the real binary is exercised on unix legs")
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "nova-swarm.real")
	build := exec.Command("go", "build", "-o", real, "../../cmd/nova-swarm")
	build.Env = goenv.Clean(os.Environ())
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the real nova-swarm: %v\n%s", err, out)
	}
	argvLog = filepath.Join(dir, "argv.log")
	shim := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + argvLog + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "nova-swarm"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	// The card form starts one runner process per card. The runner writes the card's
	// own line 1 as its RESULT.md so the real binary's gather scores the card done,
	// and drops a marker the test can see.
	runner := "#!/bin/sh\nhead -1 \"$4\" > \"$NOVA_SWARM_JOB/RESULT.md\"\ntouch \"$5/card-ran\"\n"
	if err := os.WriteFile(filepath.Join(dir, "nova-native-runner.sh"), []byte(runner), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, argvLog
}

// launch-composes-with-real-swarm (issue #534): the launched argv ran the pool/tasks
// admission mode the swarm does not accept with the deadline form that mode requires.
// The bundled example's fake exits 0 for any argv, so only the real binary catches the
// mismatch. The card form carries whole seconds of deadline and the gather mode's
// --then. The mutation that matters: the interface the release actually ships.
func TestLaunchComposesWithRealSwarm(t *testing.T) {
	root := t.TempDir()
	binDir, argvLog := realSwarm(t)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cards, _ := writeCards(t, root, 1)

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 2, Deadline: "120",
		Now: func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	if !strings.Contains(out, "PULSE OK") {
		t.Fatalf("no PULSE OK: %q", out)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))
	if !strings.Contains(line, "--cards ") {
		t.Errorf("the real swarm was not handed the card form: %s", line)
	}
	if !strings.Contains(line, "--then nova-pulse harvest --id ") {
		t.Errorf("the card form does not carry the gather mode's --then: %s", line)
	}
	if !strings.Contains(line, "--deadline 120") {
		t.Errorf("the deadline is not the whole seconds the card form requires: %s", line)
	}
	if _, err := os.Stat(filepath.Join(root, "card-ran")); err != nil {
		t.Fatalf("the real swarm never ran the card's runner: %v", err)
	}
}
