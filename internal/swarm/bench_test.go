package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeBenches writes a benches table: one header row then one tab-separated row per bench,
// the seven columns name host root cores harness auth wall.
func writeBenches(t *testing.T, dir string, rows ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("name\thost\troot\tcores\tharness\tauth\twall\n")
	for _, r := range rows {
		b.WriteString(r + "\n")
	}
	path := filepath.Join(dir, "benches.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeBin writes a fake ssh and a fake rsync on a temp PATH: ssh records its full argv and
// exits 0 without running native, rsync records its argv and copies the source locally. The
// recorded argv lands in ssh.log and rsync.log under dir.
func fakeBin(t *testing.T, dir string) (sshLog, rsyncLog string) {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	sshLog = filepath.Join(dir, "ssh.log")
	rsyncLog = filepath.Join(dir, "rsync.log")
	ssh := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + strconvQuote(sshLog) + "\n"
	rsync := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + strconvQuote(rsyncLog) + "\n" +
		"src=\"$1\"; dst=\"$2\"; dst=\"${dst#*:}\"\n" +
		"mkdir -p \"$(dirname \"$dst\")\"\n" +
		"cp \"$src\" \"$dst\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(ssh), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "rsync"), []byte(rsync), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return sshLog, rsyncLog
}

func strconvQuote(s string) string {
	return "\"" + s + "\""
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// TestBatchPinsSlotToCore: slot 3 on cores=1-15 runs under taskset -c 3, and every remote
// argv carries taskset. The fake ssh records each argv; none of them run native.
func TestBatchPinsSlotToCore(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t1-15\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone")
	sshLog, _ := fakeBin(t, dir)
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	b := writeCard(t, dir, "b.card", "RESULT: b\ndone and clean")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:3\tmodel\t"+a+"\nb\tb2:4\tmodel\t"+b+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "B1", Deadline: 5 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", Stdout: &out, Stderr: &errb,
	})
	if code == 2 {
		t.Fatalf("the batch refused admission: %s", errb.String())
	}
	lines := readLines(t, sshLog)
	if len(lines) == 0 {
		t.Fatalf("the fake ssh saw nothing; the remote card never ran")
	}
	for _, l := range lines {
		if !strings.Contains(l, "taskset") {
			t.Fatalf("every remote argv carries taskset, got %q", l)
		}
	}
	// Matched against the SET of recorded argvs, not against lines[0] and
	// lines[1]. The two cards run concurrently and both append to one log, so
	// which lands first is a race the slots do not control: on a loaded runner
	// slot 4 won it and the test read "slot 3 pins to core 4" out of an argv
	// that was correct (run 35019905236, test (3/8 studio)). What is under test
	// is that each slot carries ITS OWN core, which the order never spoke for.
	for _, want := range []string{"taskset -c 3", "taskset -c 4"} {
		found := false
		for _, l := range lines {
			if strings.Contains(l, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no remote argv runs under %q; slot 3 pins to core 3 and slot 4 to core 4:\n%s", want, strings.Join(lines, "\n"))
		}
	}
}

// TestBatchRefusesMoreSlotsThanCores: 16 slots on 1-15 is ADMIT REFUSED bench=b2 slots=16
// cores=15 and no card starts.
func TestBatchRefusesMoreSlotsThanCores(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t1-15\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone")
	sshLog, _ := fakeBin(t, dir)
	var b strings.Builder
	for i := 0; i < 16; i++ {
		label := string(rune('a' + i))
		card := writeCard(t, dir, label+".card", "RESULT: "+label+"\nall green")
		b.WriteString(label + "\t-\tmodel\t" + card + "\n")
	}
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "B1", Deadline: 5 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("16 slots on 15 cores is a refusal, got exit %d; stderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "ADMIT REFUSED bench=b2 slots=16 cores=15") {
		t.Fatalf("the refusal names the bench, slots and cores:\n%s", errb.String())
	}
	if lines := readLines(t, sshLog); len(lines) != 0 {
		t.Fatalf("no card may start on a refused bench, ssh saw %d runs", len(lines))
	}
}

// TestBatchCopiesCardOnly: exactly one file crosses before the run, and it is the card.
func TestBatchCopiesCardOnly(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t-\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone")
	_, rsyncLog := fakeBin(t, dir)
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:1\tmodel\t"+a+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "B1", Deadline: 5 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", Stdout: &out, Stderr: &errb,
	})
	if code == 2 {
		t.Fatalf("the batch refused admission: %s", errb.String())
	}
	lines := readLines(t, rsyncLog)
	if len(lines) != 1 {
		t.Fatalf("exactly one file crosses before the run, rsync saw %d:\n%v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], a+" ") {
		t.Fatalf("the one file copied is the card: %q", lines[0])
	}
	if strings.Contains(lines[0], "runner") || strings.Contains(lines[0], "auth") || strings.Contains(lines[0], "config") {
		t.Fatalf("no runner script, config or key crosses: %q", lines[0])
	}
}
