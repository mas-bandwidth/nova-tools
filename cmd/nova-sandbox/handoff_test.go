package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// handoff_test.go is the red-test contract of the handoff, docs/SPEC-SANDBOX.md
// "The handoff: what leaves the disposable place". The copy itself is tested
// over two ordinary directories -- no volume, no diskutil, no child -- and the
// ORDER, which is the part that cannot be got wrong twice, is tested through
// runDisposable with a fake whose Delete really removes the mount: an artifact
// that arrives after the delete arrives from nothing, and this test would see
// an empty --out.

// writeOn puts one file on the fake volume, making its parents.
func writeOn(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// handoffDirs is a work directory standing in for the volume's work/ and an out
// directory standing in for --out.
func handoffDirs(t *testing.T) (work, out string) {
	t.Helper()
	work, out = t.TempDir(), t.TempDir()
	if r, err := filepath.EvalSymlinks(work); err == nil {
		work = r
	}
	return work, out
}

// 1. The default set is the card's receipt, its token row and its bundle, each
// taken IF PRESENT. A read card writes no bundle and that is not a failure.
func TestHandoffTakesTheDefaultsThatArePresent(t *testing.T) {
	work, out := handoffDirs(t)
	writeOn(t, work, "RESULT.md", "RESULT card1 sha=abc\nDONE\n")
	writeOn(t, work, "usage.tsv", "in\tout\n10\t20\n")
	writeOn(t, work, "notes.txt", "not an artifact")

	res, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 {
		t.Fatalf("files = %d, want 2 (repo.bundle is not there and is not a failure)", res.Files)
	}
	if res.Bytes != int64(len("RESULT card1 sha=abc\nDONE\n")+len("in\tout\n10\t20\n")) {
		t.Errorf("bytes = %d", res.Bytes)
	}
	if got := res.Line(); got != "SANDBOX OUT name=card1 files=2 bytes="+itoa(res.Bytes) {
		t.Errorf("OUT line = %q", got)
	}
	for _, name := range []string{"RESULT.md", "usage.tsv"} {
		if _, err := os.Stat(filepath.Join(out, "card1", name)); err != nil {
			t.Errorf("%s did not arrive: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "card1", "notes.txt")); err == nil {
		t.Errorf("a file nobody named left the volume; only named artifacts leave")
	}
}

// 2. An artifact the CALLER named and did not write is a refusal, not a silent
// skip -- the run verb's rule 5, the same way --read refuses a named path that
// is not there.
func TestHandoffRefusesANamedArtifactTheCardNeverWrote(t *testing.T) {
	work, out := handoffDirs(t)
	writeOn(t, work, "RESULT.md", "x\n")
	_, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1",
		Artifacts: []string{"RESULT.md", "report.json"}, Named: true})
	if err == nil {
		t.Fatal("a named artifact that is not on the volume was not refused")
	}
	if !strings.Contains(err.Error(), "report.json") || !strings.Contains(err.Error(), "--artifact") {
		t.Errorf("the refusal does not name the flag and the file: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, "card1")); statErr == nil {
		t.Errorf("a refused handoff still made the out directory; nothing is written unless the whole set resolves")
	}
}

// 3. Nothing escapes the volume. An absolute path, a `..` and a symlink pointing
// off the volume are each refused by shape, before any copy.
func TestHandoffRefusesAnythingThatWouldReachOffTheVolume(t *testing.T) {
	work, out := handoffDirs(t)
	writeOn(t, work, "RESULT.md", "x\n")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(work, "link.txt")); err != nil {
		t.Skipf("this filesystem has no symlinks: %v", err)
	}

	for _, rel := range []string{"/etc/passwd", "../secret.txt", "a/../../b", "link.txt", "", "."} {
		if _, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1",
			Artifacts: []string{rel}, Named: true}); err == nil {
			t.Errorf("--artifact %q was not refused", rel)
		}
	}
}

// 4. The set is measured BEFORE a byte is written and refused over the cap: a
// handoff is a door, not a backup, and a truncated artifact is worse than none.
func TestHandoffRefusesOverTheByteCapAndWritesNothing(t *testing.T) {
	work, out := handoffDirs(t)
	writeOn(t, work, "RESULT.md", strings.Repeat("x", 4096))
	_, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1", MaxBytes: 1024})
	if err == nil {
		t.Fatal("4096 bytes under a 1024-byte cap was not refused")
	}
	if !strings.Contains(err.Error(), "--out-max-bytes") {
		t.Errorf("the refusal does not name the flag: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, "card1", "RESULT.md")); statErr == nil {
		t.Errorf("a refused handoff wrote a file anyway")
	}
	// The default ceiling is 64 MiB and the same set goes under it.
	if res, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1"}); err != nil || res.Files != 1 {
		t.Fatalf("the same set under the default cap: files=%d err=%v", res.Files, err)
	}
}

// 5. A directory named as an artifact is taken whole, one row per regular file,
// with its shape kept under <out>/<name>/.
func TestHandoffTakesADirectoryWhole(t *testing.T) {
	work, out := handoffDirs(t)
	writeOn(t, work, "art/one.txt", "1")
	writeOn(t, work, "art/deep/two.txt", "22")
	res, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1",
		Artifacts: []string{"art"}, Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 || res.Bytes != 3 {
		t.Fatalf("files=%d bytes=%d, want 2 and 3", res.Files, res.Bytes)
	}
	if _, err := os.Stat(filepath.Join(out, "card1", "art", "deep", "two.txt")); err != nil {
		t.Errorf("the directory's shape was not kept: %v", err)
	}
}

// deletingVolumes is the disk seam with a Delete that REALLY removes the mount,
// so the order of the handoff and the delete is proved rather than asserted: an
// artifact copied after the delete is copied from nothing.
type deletingVolumes struct {
	mount string
	calls []string
}

func (d *deletingVolumes) Container() (string, error) { return "disk3", nil }
func (d *deletingVolumes) Exists(string) (bool, error) {
	return false, nil
}
func (d *deletingVolumes) List() ([]diskVolume, error) { return nil, nil }
func (d *deletingVolumes) Create(_, name, _ string) (diskVolume, error) {
	d.calls = append(d.calls, "create")
	return diskVolume{Name: name, Disk: "disk3s9", Mount: d.mount}, nil
}
func (d *deletingVolumes) Used(string) (int64, error) { return 4096, nil }
func (d *deletingVolumes) Delete(string) error {
	d.calls = append(d.calls, "delete")
	return os.RemoveAll(d.mount)
}

// 6. Through the whole verb: the artifacts leave BEFORE the volume is deleted,
// the OUT line is printed before the DONE line, and the command's own status is
// what the verb returns.
func TestRunCopiesTheArtifactsOutBeforeTheVolumeIsDeleted(t *testing.T) {
	mount := t.TempDir()
	if r, err := filepath.EvalSymlinks(mount); err == nil {
		mount = r
	}
	out := t.TempDir()
	vols := &deletingVolumes{mount: mount}

	oldVols, oldExec, oldSigs := runVolumes, runExec, runSignals
	t.Cleanup(func() { runVolumes, runExec, runSignals = oldVols, oldExec, oldSigs })
	runVolumes = vols
	runSignals = func() (<-chan os.Signal, func()) { return make(chan os.Signal), func() {} }
	// The "command": it writes the card's receipt and its bundle on the volume,
	// which is what a real card's last steps do.
	runExec = func(p *sandbox.Policy, env []string, stdin io.Reader, stdout, stderr io.Writer) (startedRun, error) {
		writeOn(t, filepath.Join(mount, "work"), "RESULT.md", "RESULT card1 sha=abc\nDONE\n")
		writeOn(t, filepath.Join(mount, "work"), "repo.bundle", "PACK\n")
		done := make(chan int, 1)
		done <- 0
		return startedRun{done: done, kill: func(syscall.Signal) {}, pid: 4242}, nil
	}

	args := append([]string{"--name", "card1", "--size", "64m", "--out", out, "--"}, shellOf(t)...)
	var stdout, stderr bytes.Buffer
	code := runDisposable(parseRun(args), 0, nil, &stdout, &stderr, []string{"PATH=" + os.Getenv("PATH")})
	errOut := stderr.String()
	if code != 0 {
		t.Fatalf("exit = %d, want the command's own 0\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "SANDBOX OUT name=card1 files=2 bytes=31") {
		t.Fatalf("the OUT receipt is not there or does not count what left:\n%s", errOut)
	}
	if strings.Index(errOut, "SANDBOX OUT") > strings.Index(errOut, "SANDBOX DONE") {
		t.Errorf("the OUT line comes after the DONE line; the copy must happen before the delete:\n%s", errOut)
	}
	if strings.Join(vols.calls, " ") != "create delete" {
		t.Errorf("calls = %v", vols.calls)
	}
	// The proof: the volume is gone and the artifacts are not.
	if _, err := os.Stat(filepath.Join(mount, "work", "RESULT.md")); err == nil {
		t.Errorf("the volume survived the run; this test proves nothing")
	}
	for _, name := range []string{"RESULT.md", "repo.bundle"} {
		if _, err := os.Stat(filepath.Join(out, "card1", name)); err != nil {
			t.Errorf("%s did not leave the volume: %v", name, err)
		}
	}
}

// 7. A handoff that fails after a command that SUCCEEDED turns the run into a
// refusal: a zero exit would tell the caller the artifacts are in --out when
// they are not. The volume is still deleted.
func TestRunRefusesWhenTheHandoffFailsAfterACleanCommand(t *testing.T) {
	mount := t.TempDir()
	if r, err := filepath.EvalSymlinks(mount); err == nil {
		mount = r
	}
	out := t.TempDir()
	vols := &deletingVolumes{mount: mount}
	oldVols, oldExec, oldSigs := runVolumes, runExec, runSignals
	t.Cleanup(func() { runVolumes, runExec, runSignals = oldVols, oldExec, oldSigs })
	runVolumes = vols
	runSignals = func() (<-chan os.Signal, func()) { return make(chan os.Signal), func() {} }
	runExec = func(p *sandbox.Policy, env []string, stdin io.Reader, stdout, stderr io.Writer) (startedRun, error) {
		done := make(chan int, 1)
		done <- 0
		return startedRun{done: done, kill: func(syscall.Signal) {}, pid: 4242}, nil
	}

	args := append([]string{"--name", "card1", "--size", "64m", "--out", out,
		"--artifact", "RESULT.md", "--"}, shellOf(t)...)
	var stdout, stderr bytes.Buffer
	code := runDisposable(parseRun(args), 0, nil, &stdout, &stderr, []string{"PATH=" + os.Getenv("PATH")})
	errOut := stderr.String()
	if code != sandbox.ExitRefused {
		t.Fatalf("exit = %d, want %d: a clean command whose artifacts did not leave is not a clean run\n%s",
			code, sandbox.ExitRefused, errOut)
	}
	if !strings.Contains(errOut, "reason=out_failed") {
		t.Errorf("the refusal does not name itself:\n%s", errOut)
	}
	if strings.Join(vols.calls, " ") != "create delete" {
		t.Errorf("a failed handoff left the volume: %v", vols.calls)
	}
}

// 8. The flags are checked before a volume is made: --artifact without --out,
// a bad --out-max-bytes, a `..` in an artifact, and --out on windows.
func TestRunValidatesTheHandoffFlagsBeforeAnythingIsMade(t *testing.T) {
	cases := []struct {
		name string
		goos string
		args []string
		want string
	}{
		{"artifact without out", "darwin", []string{"--artifact", "RESULT.md"}, "no_out"},
		{"cap without out", "darwin", []string{"--out-max-bytes", "64m"}, "no_out"},
		{"bad cap", "darwin", []string{"--out", "/tmp/x", "--out-max-bytes", "lots"}, "bad_out_max"},
		{"dotdot artifact", "darwin", []string{"--out", "/tmp/x", "--artifact", "../x"}, "bad_artifact"},
		{"out on windows", "windows", []string{"--out", `C:\h`, "--scratch", `C:\nova`}, "no_out"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"--name", "j1"}, c.args...)
			if c.goos != "windows" {
				args = append(args, "--size", "64m")
			}
			args = append(args, "--", "/bin/sh", "-c", "true")
			f := parseRun(args)
			_, bad := validateRun(&f, c.goos)
			var reasons []string
			for _, r := range bad {
				reasons = append(reasons, r.Reason)
			}
			found := false
			for _, r := range reasons {
				if r == c.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("reasons = %v, want one %s", reasons, c.want)
			}
		})
	}
}

// 9. The banner answers the question. `run --help` names the door, the default
// artifacts and the bundle that carries a commit out.
func TestRunUsageNamesTheHandoffAndTheBundle(t *testing.T) {
	for _, want := range []string{"--out <dir>", "--artifact <p>", "--out-max-bytes", "repo.bundle", "git bundle create"} {
		if !strings.Contains(runUsage, want) {
			t.Errorf("run --help does not name %q", want)
		}
	}
}

// itoa keeps the expected OUT line honest without importing strconv for one call.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
