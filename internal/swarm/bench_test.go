package swarm

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The bench slice uses a fake ssh and a fake rsync on PATH, each a shell script that runs
// the real command locally and records its argv: ssh drops its host token and executes the
// rest, rsync drops -a and the host: prefix and copies the file home. They stand in for the
// wire so pull-and-gather is proved without a second machine.

// fakeSSH writes an ssh script that drops its host argument and runs the rest locally,
// appending every argv to sshLog.
func fakeSSH(t *testing.T, dir, sshLog string) {
	t.Helper()
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + shellQuote(sshLog) + "\n" +
		"shift\nexec \"$@\"\n"
	writeExec(t, filepath.Join(dir, "ssh"), body)
}

// fakeRsync writes an rsync script that drops -a and a host: prefix and copies the file
// home, appending every argv to rsyncLog. When failMarker names an existing file it fails
// once (and removes the marker), so a test can force the one retry.
func fakeRsync(t *testing.T, dir, rsyncLog, failMarker string) {
	t.Helper()
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + shellQuote(rsyncLog) + "\n" +
		"src=\"\"; dest=\"\"\n" +
		"for a in \"$@\"; do case \"$a\" in -a) ;; *) if [ -z \"$src\" ]; then src=\"$a\"; else dest=\"$a\"; fi ;; esac; done\n" +
		"src=\"${src#*:}\"\n" +
		"if [ -n \"" + failMarker + "\" ] && [ -e \"" + failMarker + "\" ]; then\n" +
		"  rm -f \"" + failMarker + "\"\n" +
		"  exit 1\n" +
		"fi\n" +
		"cp \"$src\" \"$dest\"\n"
	writeExec(t, filepath.Join(dir, "rsync"), body)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// remoteRunner is a runner script that, like a harness on a bench, writes RESULT.md (the
// card's first two lines) and a usage.tsv into the root it is handed, so rsync has both
// files to pull home.
func remoteRunner(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "remote-runner.sh")
	body := "#!/bin/sh\n" +
		"label=\"$1\"; slot=\"$2\"; card=\"$4\"; root=\"$5\"\n" +
		"job=\"$root/$slot/jobs/$label\"\n" +
		"mkdir -p \"$job\"\n" +
		"line1=$(sed -n 1p \"$card\")\n" +
		"line2=$(sed -n 2p \"$card\")\n" +
		"printf '%s\\n%s\\n' \"$line1\" \"$line2\" > \"$job/RESULT.md\"\n" +
		"printf 'tokens_in\\ttokens_out\\tusd\\n100\\t200\\t0.0034\\n' > \"$job/usage.tsv\"\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeRemoteCard writes one card file and a cards TSV whose slot column is <bench>:<n>.
func writeRemoteCard(t *testing.T, dir, label, bench string, slot int, body string) string {
	t.Helper()
	card := writeCard(t, dir, label+".card", body)
	tsv := label + "\t" + bench + ":" + strconv.Itoa(slot) + "\tmodel\t" + card + "\n"
	path := filepath.Join(dir, label+".tsv")
	if err := os.WriteFile(path, []byte(tsv), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// prependPath puts dir ahead of PATH so the fake ssh and rsync are found first.
func prependPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestBatchPullsResultAndUsage(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	remoteRoot := filepath.Join(dir, "remote")
	root := filepath.Join(dir, "root")
	sshLog := filepath.Join(dir, "ssh.log")
	rsyncLog := filepath.Join(dir, "rsync.log")
	fakeSSH(t, bin, sshLog)
	fakeRsync(t, bin, rsyncLog, "")
	prependPath(t, bin)
	runner := remoteRunner(t, dir)
	tsv := writeRemoteCard(t, dir, "a", "b2", 1, "RESULT: a\ndone and clean")

	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		ID: "B1", Deadline: 5 * time.Second, Cards: tsv, Root: root, Runner: runner,
		Benches: []Bench{{Name: "b2", Host: "b2host", Root: remoteRoot}},
		Stdout:  &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("a clean bench pull exits 0, got %d; stderr: %s", code, errb.String())
	}
	resPath := filepath.Join(root, "b2-1", "jobs", "a", "RESULT.md")
	if raw, err := os.ReadFile(resPath); err != nil || string(raw) != "RESULT: a\ndone and clean\n" {
		t.Fatalf("RESULT.md not pulled home: %q, %v", raw, err)
	}
	usePath := filepath.Join(root, "b2-1", "jobs", "a", "usage.tsv")
	if _, err := os.Stat(usePath); err != nil {
		t.Fatalf("usage.tsv not pulled home: %v", err)
	}
	if !strings.Contains(out.String(), "a slot=1: done and clean") {
		t.Fatalf("gather did not fold the pulled card; output: %s", out.String())
	}
}

func TestBatchLineHasBenchLines(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	remoteRoot := filepath.Join(dir, "remote")
	root := filepath.Join(dir, "root")
	fakeSSH(t, bin, filepath.Join(dir, "ssh.log"))
	fakeRsync(t, bin, filepath.Join(dir, "rsync.log"), "")
	prependPath(t, bin)
	runner := remoteRunner(t, dir)
	var sb strings.Builder
	for i, c := range [][2]string{{"a", "b1"}, {"b", "b2"}} {
		body := "RESULT: " + c[0] + "\nall green"
		card := writeCard(t, dir, c[0]+".card", body)
		sb.WriteString(c[0] + "\t" + c[1] + ":" + strconv.Itoa(i+1) + "\tmodel\t" + card + "\n")
	}
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		ID: "B1", Deadline: 5 * time.Second, Cards: tsv, Root: root, Runner: runner,
		Benches: []Bench{
			{Name: "b1", Host: "b1host", Root: remoteRoot},
			{Name: "b2", Host: "b2host", Root: remoteRoot},
		},
		Stdout: &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("a clean bench batch exits 0, got %d; stderr: %s", code, errb.String())
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if !strings.HasPrefix(lines[0], "BATCH B1 ") || !strings.Contains(lines[0], "benches=2") {
		t.Fatalf("BATCH line wants benches=2; got %q", lines[0])
	}
	if !strings.Contains(lines[1], "BENCH b1 slots=1 done=1 abstain=0") {
		t.Fatalf("first BENCH line wrong: %q", lines[1])
	}
	if !strings.Contains(lines[2], "BENCH b2 slots=1 done=1 abstain=0") {
		t.Fatalf("second BENCH line wrong: %q", lines[2])
	}
	if !strings.Contains(lines[0], "done=2") {
		t.Fatalf("BATCH done wants 2: %q", lines[0])
	}
}

func TestGatherRetriesPullOnce(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	remoteRoot := filepath.Join(dir, "remote")
	root := filepath.Join(dir, "root")
	rsyncLog := filepath.Join(dir, "rsync.log")
	fakeSSH(t, bin, filepath.Join(dir, "ssh.log"))
	fakeRsync(t, bin, rsyncLog, filepath.Join(dir, "fail-once"))
	prependPath(t, bin)
	runner := remoteRunner(t, dir)
	tsv := writeRemoteCard(t, dir, "a", "b2", 1, "RESULT: a\ndone and clean")

	old := pullRetryInterval
	pullRetryInterval = time.Millisecond
	defer func() { pullRetryInterval = old }()

	if err := os.WriteFile(filepath.Join(dir, "fail-once"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		ID: "B1", Deadline: 5 * time.Second, Cards: tsv, Root: root, Runner: runner,
		Benches: []Bench{{Name: "b2", Host: "b2host", Root: remoteRoot}},
		Stdout:  &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("a retried pull still folds done, got %d; stderr: %s, out: %s", code, errb.String(), out.String())
	}
	if !strings.Contains(out.String(), "a slot=1: done and clean") {
		t.Fatalf("retried pull did not fold done; out: %s", out.String())
	}
	log, _ := os.ReadFile(rsyncLog)
	if n := strings.Count(string(log), "RESULT.md "); n != 2 {
		t.Fatalf("the pull is retried once: RESULT.md was pulled %d times, want 2 (log: %s)", n, strings.TrimSpace(string(log)))
	}
}
