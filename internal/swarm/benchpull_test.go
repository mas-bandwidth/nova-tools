package swarm

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The bench fakes: an `ssh` and an `scp` on PATH that record their argv and work against a
// directory on this machine standing in for the bench's disk. The fake ssh runs the command
// it was given through a shell, exactly as a real one hands its joined arguments to the
// remote shell, so `test -f`, a glob and a redirect behave here as they do there. The fake
// scp strips the `host:` and copies one named file, and fails when that file is not there --
// which is the whole reason the pull is scp per file rather than one filtered rsync.
//
// unreachable=true makes both of them exit 255, which is ssh's own code for "could not
// reach the host" and never a remote command's answer.
type benchFake struct {
	// unreachable makes ssh and scp exit 255, which is ssh's own code for a host it could
	// not reach and never a remote command's answer.
	unreachable bool
	// appearAfter, appearPath and appearBody are how the fake bench holds a file back: the
	// file is written by the ssh that asks for it the appearAfter-th time, so "the pull
	// waited" is a property of the fake's own counting rather than of a sleep racing a
	// poll on a loaded machine.
	appearAfter int
	appearPath  string
	appearBody  string
}

func fakeBenchBin(t *testing.T, dir string, f benchFake) (sshLog, scpLog string) {
	t.Helper()
	bin := filepath.Join(dir, "benchbin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	sshLog = filepath.Join(dir, "ssh.log")
	scpLog = filepath.Join(dir, "scp.log")
	fail := ""
	if f.unreachable {
		fail = "exit 255\n"
	}
	appear := ""
	if f.appearAfter > 0 {
		counter := filepath.Join(dir, "asks")
		appear = "case \"$*\" in *\"test -f " + f.appearPath + "\"*)\n" +
			"  n=$(cat \"" + counter + "\" 2>/dev/null || echo 0); n=$((n+1)); echo \"$n\" > \"" + counter + "\"\n" +
			"  if [ \"$n\" -ge " + strconv.Itoa(f.appearAfter) + " ]; then mkdir -p \"$(dirname \"" + f.appearPath + "\")\"; printf '%s' '" + f.appearBody + "' > \"" + f.appearPath + "\"; fi\n" +
			"  ;;\nesac\n"
	}
	ssh := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + sshLog + "\"\n" + fail + appear +
		"shift\nsh -c \"$*\"\n"
	scp := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + scpLog + "\"\n" + fail +
		"src=\"${1#*:}\"; dst=\"$2\"\n" +
		"[ -f \"$src\" ] || exit 1\n" +
		"mkdir -p \"$(dirname \"$dst\")\"\ncp \"$src\" \"$dst\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(ssh), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "scp"), []byte(scp), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return sshLog, scpLog
}

func writeAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// THE FILE IS NOT THERE THE MOMENT THE SHELL RETURNS. On 2026-09-15 the pull copied the
// instant the remote slot's ssh came back, RESULT.md landed a moment later, and the batch
// read the card as one that abstained. The pull waits for the file, bounded, and only then
// copies -- one explicit scp per file, so a file that did not come back is a copy that
// failed rather than a filter that matched nothing and exited 0.
func TestPullWaitsForResult(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	remoteJob := filepath.Join(dir, "bench", "3", "jobs", "a")
	localJob := filepath.Join(dir, "root", "b2-3", "jobs", "a")
	// The result is not on the bench when the slot's shell returns: it appears on the
	// THIRD ask, so a pull that asks once and copies gets nothing, whatever the machine's
	// load is doing.
	sshLog, scpLog := fakeBenchBin(t, dir, benchFake{
		appearAfter: 3,
		appearPath:  filepath.Join(remoteJob, "RESULT.md"),
		appearBody:  "RESULT: a\nall green\n",
	})
	writeAt(t, filepath.Join(remoteJob, "usage.tsv"), "tokens_in\ttokens_out\tusd\n10\t20\t0.0100\n")
	writeAt(t, filepath.Join(remoteJob, "native.log"), "the card's own log\n")

	var notes strings.Builder
	if err := pullFromBench(benchPull{
		host: "b2", remoteJob: remoteJob, localJob: localJob,
		wait: 30 * time.Second, poll: 50 * time.Millisecond, notes: &notes,
	}); err != nil {
		t.Fatalf("the pull failed on a bench that answered: %v", err)
	}

	for name, want := range map[string]string{
		"RESULT.md":  "RESULT: a\nall green\n",
		"usage.tsv":  "tokens_in\ttokens_out\tusd\n10\t20\t0.0100\n",
		"native.log": "the card's own log\n",
	} {
		raw, err := os.ReadFile(filepath.Join(localJob, name))
		if err != nil {
			t.Fatalf("%s did not come back from the bench: %v", name, err)
		}
		if string(raw) != want {
			t.Fatalf("%s came back changed:\n%q", name, raw)
		}
	}

	asks := 0
	for _, l := range readLines(t, sshLog) {
		if strings.Contains(l, "test -f") && strings.HasSuffix(l, "RESULT.md") {
			asks++
		}
	}
	if asks < 3 {
		t.Fatalf("the pull asked for RESULT.md %d time(s): it copied without waiting for the file to exist", asks)
	}
	copies := readLines(t, scpLog)
	if len(copies) != 3 {
		t.Fatalf("three files come back, one explicit scp each; scp saw %d:\n%v", len(copies), copies)
	}
	for _, l := range copies {
		// The filter check reads ARGUMENTS, not substrings of the whole line: a copy
		// command names paths, and a temp path can hold "-r" or a glob character, so a
		// substring match on the line would refuse a copy of one file because of where
		// the work directory happens to live.
		//
		// EACH ARGUMENT, not a substring of the whole line: the copy's paths are absolute
		// and a temp directory whose name merely contains "-r" (for example one under
		// ".../swarm-root/...") tripped the old whole-line check while naming a single
		// file. The intent is unchanged: no argument is a filter or a glob.
		for _, arg := range strings.Fields(l) {
			// The argv of the copy is what names a filter or a pattern; a substring of the
			// whole line is not, because "-r" is inside plenty of paths (a temp root under
			// .../swarm-root, for one) and says nothing about how the copy was asked.
			if strings.HasPrefix(arg, "-") || strings.Contains(arg, "*") ||
				arg == "--include" || arg == "--exclude" || arg == "-r" ||
				strings.ContainsAny(arg, "*?[") ||
				strings.HasPrefix(arg, "--include=") || strings.HasPrefix(arg, "--exclude=") {
				t.Fatalf("a copy names a filter or a pattern rather than one file, which is how a copy of nothing exits 0: %q", l)
			}
		}
	}
	for _, want := range []string{"RESULT.md", "usage.tsv", "native.log"} {
		found := false
		for _, l := range copies {
			if strings.Contains(l, "/"+want+" ") {
				found = true
			}
		}
		if !found {
			t.Fatalf("no scp names %s:\n%v", want, copies)
		}
	}
}

// THE CARD WROTE IT IN THE WRONG PLACE, and the file is plainly there. A card that writes
// RESULT.md inside the repository it cloned is still a card whose work came back on
// 2026-09-15 as nothing at all. The pull looks under repo/ and one level below, copies the
// file up into the job, and says so on stderr -- the card is wrong and a person reading the
// packet is told where its result was found.
func TestPullCopiesResultUpFromRepo(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	_, _ = fakeBenchBin(t, dir, benchFake{})
	remoteJob := filepath.Join(dir, "bench", "1", "jobs", "a")
	localJob := filepath.Join(dir, "root", "b2-1", "jobs", "a")
	buried := filepath.Join(remoteJob, "repo", "nova-tools", "RESULT.md")
	writeAt(t, buried, "RESULT: a\nall green\n")

	var notes strings.Builder
	if err := pullFromBench(benchPull{
		host: "b2", remoteJob: remoteJob, localJob: localJob, label: "a",
		wait: 200 * time.Millisecond, poll: 50 * time.Millisecond, notes: &notes,
	}); err != nil {
		t.Fatalf("the pull failed on a bench that answered: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(localJob, "RESULT.md"))
	if err != nil {
		t.Fatalf("the result was under repo/ and did not come back: %v", err)
	}
	if string(raw) != "RESULT: a\nall green\n" {
		t.Fatalf("the result came back changed:\n%q", raw)
	}
	want := "BATCH NOTE a RESULT.md copied up from " + buried
	if !strings.Contains(notes.String(), want) {
		t.Fatalf("the pull did not say where it found the result:\nwant: %s\ngot: %s", want, notes.String())
	}
	if _, err := os.Stat(filepath.Join(remoteJob, "RESULT.md")); err != nil {
		t.Fatalf("the result was not copied up into the job on the bench: %v", err)
	}
}

// A BENCH THAT COULD NOT BE REACHED IS NOT A CARD THAT ABSTAINED ON ITS OWN WORK. It is its
// own reason token, so a batch's packet says the machine was unreachable rather than
// blaming the model for a result nobody could go and get.
func TestPullScoresBenchUnreachable(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t-\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone")
	_, _ = fakeBenchBin(t, dir, benchFake{unreachable: true})
	// The card still has to cross to the bench, and rsync is the copy out; the fake here
	// answers for it the way the remote-run tests' one does.
	writeAt(t, filepath.Join(dir, "benchbin", "rsync"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(dir, "benchbin", "rsync"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:1\tmodel\t"+a+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2",
		PullWait: 150 * time.Millisecond, PullPoll: 50 * time.Millisecond,
		Stdout: &out, Stderr: &errb,
	})
	if code != 1 {
		t.Fatalf("a batch whose only card could not be pulled says NO; exit %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "ABSTAIN reason=bench-unreachable") {
		t.Fatalf("an unreachable bench scores its card ABSTAIN reason=bench-unreachable, not a plain abstain:\n%s", out.String())
	}
	if strings.Contains(out.String(), "stalled (no output") {
		t.Fatalf("an unreachable bench is not a stalled card:\n%s", out.String())
	}
}
