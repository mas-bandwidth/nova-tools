package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

	// One batch run, holding exactly the first four cards in cards.tsv order.
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
	tasksDir := ""
	for i, a := range argv {
		if a == "--tasks" && i+1 < len(argv) {
			tasksDir = argv[i+1]
		}
	}
	if tasksDir == "" {
		t.Fatalf("no --tasks dir in argv: %q", lines[0])
	}
	var names []string
	entries, _ := os.ReadDir(tasksDir)
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 4 {
		t.Fatalf("--tasks dir holds %d files, want 4: %v", len(names), names)
	}
	for i, p := range paths[:4] {
		want, _ := os.ReadFile(p)
		got, err := os.ReadFile(filepath.Join(tasksDir, names[i]))
		if err != nil || !bytes.Equal(want, got) {
			t.Fatalf("task %d does not carry card %d", i, i)
		}
	}
	if !strings.Contains(lines[0], "--label pulse-"+id) {
		t.Fatalf("argv lacks --label pulse-%s: %q", id, lines[0])
	}
	if !strings.Contains(lines[0], "--then nova-pulse harvest --id "+id+" --root "+root) {
		t.Fatalf("argv lacks --then harvest: %q", lines[0])
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
	if !strings.Contains(string(pulses), "pulse-"+id+"\tpro\t4") {
		t.Fatalf("pulses/%s.tsv does not name the batch: %q", id, pulses)
	}
}

// launch-carries-the-file-budget (issue #869): `nova-swarm batch` refuses a pool admission
// that carries no budget -- "--files is required and is at least 1, got 0" -- so the wired
// launch names both budgets the verb requires, the file budget from the configuration and the
// explicit `unmetered` the shim's native runner has always meant. The mutation that matters:
// the flag dropped, which is the refusal that stopped the first tick of the switch.
func TestLaunchCarriesTheFileBudget(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 2)

	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120", Files: 40,
		Now: func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))
	for _, want := range []string{"--files 40", "--tokens unmetered", "--pool ", "--tasks ", "--label pulse-", "--deadline 120", "--then nova-pulse harvest"} {
		if !strings.Contains(line, want) {
			t.Errorf("argv lacks %q: %s", want, line)
		}
	}
}

// launch-makes-the-pool-on-first-use (issue #878): the switch to nova-swarm batch was made
// with the pool empty, so the launch path never ran once before the loop was trusted, and
// every tick refused: `nova-swarm batch: --pool wants a directory that exists: stat
// <root>/pool: no such file or directory`. The verb that makes one is nova-swarm quickstart;
// the launch should make the pool itself on first use -- the nine directories quickstart
// makes -- log one line, and attempt the batch. The mutation that matters: the pool made, or
// the batch never attempted.
func TestLaunchMakesThePoolOnFirstUse(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 1)

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Now: func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	if !strings.Contains(errb, "PULSE POOL MADE root="+root) {
		t.Fatalf("no PULSE POOL MADE line on a root with no pool: %q", errb)
	}
	if !strings.Contains(out, "PULSE OK") {
		t.Fatalf("no PULSE OK line: %q", out)
	}
	pool := filepath.Join(root, "pool")
	for _, d := range []string{"pending", "running", "done", "failed", "aborted", "slots", "usage", "reports", "scratch"} {
		if fi, err := os.Stat(filepath.Join(pool, d)); err != nil || !fi.IsDir() {
			t.Fatalf("pool subdir %s is not a directory after the launch made the pool: %v", d, err)
		}
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); !strings.HasPrefix(got, "nova-swarm batch ") {
		t.Fatalf("the batch was never attempted: %q", got)
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
		Now: func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	raw, _ := os.ReadFile(argvLog)
	if want := "--files " + strconv.Itoa(DefaultLaunchFiles); !strings.Contains(string(raw), want) {
		t.Fatalf("argv lacks %q: %s", want, raw)
	}
}
