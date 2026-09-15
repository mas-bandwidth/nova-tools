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

// fakeRemoteBin writes a fake ssh and a fake rsync on a temp PATH. The fake ssh records its
// full argv into ssh.log; a native run prints "RUN pgid=99999" and sleeps forever (a card
// that never ends on its own); a pkill records and exits 0. When statFail is set, ssh stat
// exits 1 -- an unreachable bench. The fake rsync records its argv and copies the source
// locally so a card still lands where remoteRun puts it.
func fakeRemoteBin(t *testing.T, dir string, statFail bool) (sshLog, rsyncLog string) {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	sshLog = filepath.Join(dir, "ssh.log")
	rsyncLog = filepath.Join(dir, "rsync.log")
	stat := "echo 100; exit 0"
	if statFail {
		stat = "exit 1"
	}
	ssh := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"" + sshLog + "\"\n" +
		"if [ \"$2\" = \"pkill\" ]; then exit 0; fi\n" +
		"if [ \"$2\" = \"stat\" ]; then " + stat + "; fi\n" +
		"echo 'RUN pgid=99999'\n" +
		"sleep 30\n"
	rsync := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"" + rsyncLog + "\"\n" +
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

// TestDeadlineKillsRemoteGroup: at the deadline the local ssh child's group gets SIGTERM, the
// fake ssh sees pkill -TERM -g <pgid> with the pgid native printed on its RUN pgid= line, then
// -KILL after the delay, and the card scores ABSTAIN -- deadline.
func TestDeadlineKillsRemoteGroup(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t-\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tsandbox")
	sshLog, _ := fakeRemoteBin(t, dir, false)
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:1\tmodel\t"+a+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "B1", Deadline: 1 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", Stdout: &out, Stderr: &errb,
	})
	if code != 1 {
		t.Fatalf("a batch whose card is killed at the deadline exits 1, got %d; stderr: %s", code, errb.String())
	}
	lines := strings.Join(readLines(t, sshLog), "\n")
	if !strings.Contains(lines, "pkill -TERM -g 99999") {
		t.Fatalf("the remote group named by RUN pgid= gets pkill -TERM, ssh saw:\n%s", lines)
	}
	if !strings.Contains(lines, "pkill -KILL -g 99999") {
		t.Fatalf("the remote group gets pkill -KILL after the delay, ssh saw:\n%s", lines)
	}
	if !strings.Contains(out.String(), "a slot=1: ABSTAIN -- deadline") {
		t.Fatalf("a deadline-killed remote card scores ABSTAIN -- deadline:\n%s", out.String())
	}
}

// TestRemoteIdleWatchReadsSize: the idle watch asks ssh stat -c %s, no more than once per
// --idle/3, and a bench unreachable at a poll leaves the card unknown until the deadline, then
// ABSTAIN -- bench-unreachable.
func TestRemoteIdleWatchReadsSize(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t-\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tsandbox")
	sshLog, _ := fakeRemoteBin(t, dir, true)
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:1\tmodel\t"+a+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	// Deadline 2s, idle 3s: the idle watch polls stat (unreachable) but is never allowed to
	// fire a kill before the batch deadline ends the card as unreachable.
	start := time.Now()
	code := Batch(BatchInput{
		ID: "B1", Deadline: 2 * time.Second, Idle: 3 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", Stdout: &out, Stderr: &errb,
	})
	if code != 1 {
		t.Fatalf("an unreachable bench at the deadline exits 1, got %d; stderr: %s", code, errb.String())
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the unreachable card is scored at the deadline, not left to burn it")
	}
	lines := strings.Join(readLines(t, sshLog), "\n")
	if !strings.Contains(lines, "stat -c %s") {
		t.Fatalf("the idle watch measures the remote log's byte size, ssh saw:\n%s", lines)
	}
	if !strings.Contains(out.String(), "a slot=1: ABSTAIN -- bench-unreachable") {
		t.Fatalf("a card whose bench went unreachable scores bench-unreachable, not a hang:\n%s", out.String())
	}
}

// TestUnwalledBenchNeedsNoWallAndMarksResult: wall=none without --no-wall is refused naming
// the bench and the flag; with --no-wall the RUN UNSANDBOXED line prints, native gets
// --no-wall, and the card line carries wall=none.
func TestUnwalledBenchNeedsNoWallAndMarksResult(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t-\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone")
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:1\tmodel\t"+a+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _ = fakeRemoteBin(t, dir, false)
	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "B1", Deadline: 1 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("a wall=none bench without --no-wall is refused, got exit %d:\n%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "BATCH REFUSED") || !strings.Contains(errb.String(), "b2") || !strings.Contains(errb.String(), "--no-wall") {
		t.Fatalf("the refusal names the bench and the flag:\n%s", errb.String())
	}

	sshLog2, _ := fakeRemoteBin(t, dir, false)
	out.Reset()
	errb.Reset()
	code = Batch(BatchInput{
		ID: "B1", Deadline: 1 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", NoWall: true, Stdout: &out, Stderr: &errb,
	})
	if code != 1 {
		t.Fatalf("a wall=none bench with --no-wall runs to the deadline, got %d; stderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "RUN UNSANDBOXED id=a slot=b2:1") {
		t.Fatalf("the unwalled card prints RUN UNSANDBOXED on stderr before it starts:\n%s", errb.String())
	}
	lines := strings.Join(readLines(t, sshLog2), "\n")
	if !strings.Contains(lines, "--no-wall") {
		t.Fatalf("native on a wall=none bench gets --no-wall, ssh saw:\n%s", lines)
	}
	if !strings.Contains(out.String(), "a slot=1: ABSTAIN -- deadline wall=none") {
		t.Fatalf("the card line carries wall=none:\n%s", out.String())
	}
}

// TestBatchPinsSlotToCore: slot 3 on cores=1-15 runs under taskset -c 3, and every remote
// argv carries taskset. The fake ssh records each argv; none of them run native.
func TestBatchPinsSlotToCore(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t1-15\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tsandbox")
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
	if !strings.Contains(lines[0], "taskset -c 3") {
		t.Fatalf("slot 3 on 1-15 runs under taskset -c 3, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "taskset -c 4") {
		t.Fatalf("slot 4 on 1-15 runs under taskset -c 4, got %q", lines[1])
	}
}

// TestBatchRefusesMoreSlotsThanCores: 16 slots on 1-15 is ADMIT REFUSED bench=b2 slots=16
// cores=15 and no card starts.
func TestBatchRefusesMoreSlotsThanCores(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t1-15\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tsandbox")
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
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t-\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tsandbox")
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
