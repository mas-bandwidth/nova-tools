package swarm

// Red tests for nova-tools #607 (re-cut against current main): remote deadline/idle and
// the BENCH line per bench, from SPEC-SWARM "Benches".
//
// 9.  `deadline-kills-remote-group` — at the deadline the local ssh child's group gets
//     SIGTERM, the fake ssh sees `pkill -TERM -g <pgid>` with the pgid from the `RUN
//     pgid=` line, then `-KILL`, and the card is `ABSTAIN reason=deadline`.
// 12. `remote-idle-watch-reads-size` — the idle poll is `stat -c %s`, at most once per
//     `--idle/3`, and an unreachable bench leaves the card `unknown` until the deadline,
//     then `ABSTAIN reason=bench-unreachable`.
// 8.  `batch-line-has-bench-lines` — `benches=2` and two `BENCH` lines whose `done` sum
//     to the `BATCH` line's.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRunBin writes a fake ssh, rsync and scp on a temp PATH for the bench replays.
// The fake ssh records its full argv into ssh.log and dispatches on its remote command:
//   - `pkill` records and exits 0 (the kill landed);
//   - `stat` follows statMode: "size" echoes 100 bytes, "unreachable" exits 255;
//   - `test` exits 1 (no RESULT.md on the bench) and `ls` prints nothing (no result
//     under repo/ either), so the pull finds nothing and scores the missing result;
//   - anything else is a native run: it prints `RUN pgid=<pgid>` and sleeps, a card
//     that never ends on its own so the batch deadline ends it.
//
// The fake rsync copies the card locally; the fake scp records and exits 1 so no copy
// ever touches the network.
func fakeRunBin(t *testing.T, dir, statMode string, pgid string) string {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	sshLog := filepath.Join(dir, "ssh.log")
	stat := "echo 100; exit 0"
	if statMode == "unreachable" {
		stat = "exit 255"
	}
	ssh := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"" + sshLog + "\"\n" +
		"if [ \"$2\" = \"pkill\" ]; then exit 0; fi\n" +
		"if [ \"$2\" = \"stat\" ]; then " + stat + "; fi\n" +
		"if [ \"$2\" = \"test\" ]; then exit 1; fi\n" +
		"if [ \"$2\" = \"ls\" ]; then exit 0; fi\n" +
		"echo 'RUN pgid=" + pgid + "'\n" +
		"sleep 30\n"
	rsync := "#!/bin/sh\n" +
		"src=\"$1\"; dst=\"$2\"; dst=\"${dst#*:}\"\n" +
		"mkdir -p \"$(dirname \"$dst\")\"\n" +
		"cp \"$src\" \"$dst\"\n"
	scp := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + filepath.Join(dir, "scp.log") + "\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(ssh), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "rsync"), []byte(rsync), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "scp"), []byte(scp), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return sshLog
}

func TestBatchRemoteDeadlineKillsGroup(t *testing.T) {
	windowsIsNotABench(t)
	oldDelay := remoteKillDelay
	remoteKillDelay = 50 * time.Millisecond
	defer func() { remoteKillDelay = oldDelay }()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t-\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tsandbox")
	sshLog := fakeRunBin(t, dir, "size", "424242")
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:1\tmodel\t"+a+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "B1", Deadline: 1 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", PullWait: 200 * time.Millisecond, PullPoll: 20 * time.Millisecond,
		Stdout: &out, Stderr: &errb,
	})
	if code != 1 {
		t.Fatalf("a batch whose card is killed at the deadline exits 1, got %d; stderr: %s", code, errb.String())
	}
	lines := strings.Join(readLines(t, sshLog), "\n")
	if !strings.Contains(lines, "pkill -TERM -g 424242") {
		t.Fatalf("the remote group named by RUN pgid= gets pkill -TERM, ssh saw:\n%s", lines)
	}
	if !strings.Contains(lines, "pkill -KILL -g 424242") {
		t.Fatalf("the remote group gets pkill -KILL after the delay, ssh saw:\n%s", lines)
	}
	if !strings.Contains(out.String(), "a slot=1: ABSTAIN reason=deadline") {
		t.Fatalf("a deadline-killed remote card scores ABSTAIN reason=deadline:\n%s", out.String())
	}
}

func TestBatchRemoteIdleWatchReadsSize(t *testing.T) {
	windowsIsNotABench(t)
	oldDelay := remoteKillDelay
	remoteKillDelay = 50 * time.Millisecond
	defer func() { remoteKillDelay = oldDelay }()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t-\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tsandbox")
	sshLog := fakeRunBin(t, dir, "unreachable", "424242")
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:1\tmodel\t"+a+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	// Deadline 2s, idle 3s: the idle watch polls stat (unreachable) but never fires a
	// kill before the batch deadline ends the card as unreachable.
	code := Batch(BatchInput{
		ID: "B1", Deadline: 2 * time.Second, Idle: 3 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", PullWait: 200 * time.Millisecond, PullPoll: 20 * time.Millisecond,
		Stdout: &out, Stderr: &errb,
	})
	if code != 1 {
		t.Fatalf("an unreachable bench at the deadline exits 1, got %d; stderr: %s", code, errb.String())
	}
	lines := readLines(t, sshLog)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "stat -c %s") {
		t.Fatalf("the idle watch measures the remote log's byte size, ssh saw:\n%s", joined)
	}
	stats := 0
	for _, l := range lines {
		if strings.Contains(l, "stat -c %s") {
			stats++
		}
	}
	// idle/3 is 1s over a 2s run: at most 3 polls. A poll per tick would be ~20.
	if stats > 3 {
		t.Fatalf("the idle watch asks at most once per --idle/3, asked %d times:\n%s", stats, joined)
	}
	if !strings.Contains(out.String(), "a slot=1: ABSTAIN reason=bench-unreachable") {
		t.Fatalf("a card whose bench went unreachable scores bench-unreachable:\n%s", out.String())
	}
}

func TestBatchLineHasBenchLines(t *testing.T) {
	windowsIsNotABench(t)
	oldDelay := remoteKillDelay
	remoteKillDelay = 50 * time.Millisecond
	defer func() { remoteKillDelay = oldDelay }()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir,
		"b2\tb2\t"+benchRoot+"\t-\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tsandbox",
		"b3\tb3\t"+benchRoot+"\t-\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tsandbox")
	fakeRunBin(t, dir, "size", "424242")
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	b := writeCard(t, dir, "b.card", "RESULT: b\ndone and clean")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:1\tmodel\t"+a+"\nb\tb3:1\tmodel\t"+b+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "B1", Deadline: 2 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2,b3", PullWait: 200 * time.Millisecond, PullPoll: 20 * time.Millisecond,
		Stdout: &out, Stderr: &errb,
	})
	if code == 2 {
		t.Fatalf("the batch refused admission: %s", errb.String())
	}
	packet := out.String()
	if !strings.Contains(packet, "benches=2") {
		t.Fatalf("the BATCH line gains benches=2 with --bench:\n%s", packet)
	}
	for _, want := range []string{
		"BENCH b2 slots=1 done=0 abstain=1 in=- out=- usd=0.0000",
		"BENCH b3 slots=1 done=0 abstain=1 in=- out=- usd=0.0000",
	} {
		if !strings.Contains(packet, want) {
			t.Fatalf("the packet carries one BENCH line per bench, missing %q:\n%s", want, packet)
		}
	}
	if strings.Index(packet, "BENCH b2") > strings.Index(packet, "a slot=") {
		t.Fatalf("the BENCH lines come before the first card line:\n%s", packet)
	}
}
