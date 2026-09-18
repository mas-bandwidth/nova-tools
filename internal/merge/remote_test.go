package merge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The ssh seam's own tests. They open no connection: what is under test here is the argv, the
// script and the guard on a path that crosses to another machine. The whole verb is exercised
// against a fake bench in cmd/nova-merge.

// THE ARGV IS THE CREDENTIAL STORY. BatchMode=yes is what makes a machine this caller cannot
// reach non-interactively a refusal in seconds instead of a verb hanging on a prompt nobody
// is watching, and `--` is what makes a target beginning with a dash a target.
func TestSSHArgvIsBatchModeBoundedAndEndsItsOptions(t *testing.T) {
	t.Parallel()
	argv := SSHArgv("hulk", "echo hello")
	joined := strings.Join(argv, " ")
	for _, want := range []string{"-o BatchMode=yes", "-o ConnectTimeout=", "-- hulk echo hello"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the ssh argv must carry %q, got %v", want, argv)
		}
	}
	// THE SCRIPT IS ONE ARGUMENT. ssh joins its remaining arguments with spaces before the
	// far side's shell sees them, so a script split across two would arrive as a different
	// command every time one of its words held a space.
	if argv[len(argv)-1] != "echo hello" {
		t.Errorf("the script is the last argument, whole: %v", argv)
	}
	if argv[len(argv)-2] != "hulk" {
		t.Errorf("the target comes immediately before the script: %v", argv)
	}
}

// A file comes back as `cat` over the same one ssh -- not scp, not rsync: a second tool is a
// second thing to be missing on a bench.
func TestSSHGetArgvCatsThePathQuoted(t *testing.T) {
	t.Parallel()
	argv := SSHGetArgv("hulk", "~/nova-bench/integration/x/batch.bundle")
	last := argv[len(argv)-1]
	if !strings.HasPrefix(last, "cat -- ") {
		t.Fatalf("a file comes back through cat: %q", last)
	}
	// The tilde is OUTSIDE the quotes, because the home it names is the BENCH's.
	if !strings.Contains(last, `"$HOME"/'nova-bench/integration/x/batch.bundle'`) {
		t.Errorf("the path must reach the machine with its ~ unexpanded and the rest quoted: %q", last)
	}
}

// THE PRELUDE IS THE BENCH TOOLCHAIN AND internal/goenv's Clean, WRITTEN AS THE SHELL. A
// non-interactive ssh gets whatever the machine's rc file happens to export; a gate that
// ran against the distribution's go1.22 because a login shell was not involved is a gate
// that refuses a tree CI builds.
func TestRemoteScriptPutsTheBenchToolchainFirstAndDropsGOFLAGS(t *testing.T) {
	t.Parallel()
	script := RemoteScript("~/nova-bench/integration/x/repo", []string{"TMPDIR=/tmp/x"}, "go build ./...")
	for _, want := range []string{
		`"$HOME"/sdk/*/bin`, // the provisioning standard's toolchain root (#1364)
		"sort -r",           // newest by name wins, as the local gate's lookInSDK does
		"unset GOFLAGS",     // CI's own `make test` exports GOFLAGS=-json
		"TMPDIR='/tmp/x'",   // the batch's private temp directory, quoted
		"export TMPDIR",     //
		`cd "$HOME"/'nova-bench/integration/x/repo'`, // the tilde answered by the machine
		"&& go build ./...",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the remote script must carry %q:\n%s", want, script)
		}
	}
	// ONE LINE, always: it is an argument to ssh, and a script with a newline in it is a
	// script whose second half a shell could read as something else entirely.
	if strings.Contains(script, "\n") {
		t.Errorf("the remote script is one line:\n%s", script)
	}
}

// THE SCRIPT REALLY RUNS. The prelude is shell, and shell that is only ever asserted as a
// substring is shell nobody has run: this executes one against /bin/sh and reads what it
// printed, which is also the proof that a machine with no ~/sdk loses nothing.
func TestRemoteScriptRunsAndLeavesPATHUsableWithoutAnSDK(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell on this machine; the remote script is one")
	}
	dir := t.TempDir()
	home := t.TempDir() // no sdk under it at all, which is a Mac bench's shape
	script := RemoteScript(dir, []string{"NOVA_TEST_VALUE=beech"}, "echo $NOVA_TEST_VALUE; pwd")
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+home, "GOFLAGS=-json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the remote script did not run: %v\n%s\n%s", err, script, out)
	}
	got := string(out)
	if !strings.Contains(got, "beech") {
		t.Errorf("the script must export the caller's variables: %q", got)
	}
	// pwd RESOLVED on both sides: a temp dir on a Mac is under /var, itself a link to
	// /private/var, so the comparison is between two spellings of one directory otherwise.
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(strings.TrimSpace(strings.Split(got, "\n")[1]))
	if err != nil {
		t.Fatal(err)
	}
	if real != want {
		t.Errorf("the script must cd into the directory it was given: %q, want %q", real, want)
	}
}

// RemoteQuote takes every character literally EXCEPT a leading ~, which is the whole reason
// it is not strconv.Quote: a bench root is `~/nova-bench/...` and the home it names is the
// bench's, never the /Users/glenn this process would expand it to.
func TestRemoteQuoteLeavesTheTildeToTheMachine(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, want string }{
		{"~/nova-bench/x", `"$HOME"/'nova-bench/x'`},
		{"/data/x", `'/data/x'`},
		{"dev", `'dev'`},
		{"it's", `'it'\''s'`},
	} {
		if got := RemoteQuote(c.in); got != c.want {
			t.Errorf("RemoteQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// THE GUARD ON A PATH THAT CROSSES. The gate REMOVES its own working directory on the
// machine before it rebuilds it, and Glenn, 2026-09-17, on exactly that: "it is just one
// mistake away from deleting the whole disk".
func TestValidRemotePathRefusesEverythingThatCouldClimbOrRunSomething(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{
		"~/nova-bench/integration/dogfood-on",
		"/data/nova/integration",
		"/var/folders/z2/0d2ymgl1363d6gbhv4443p8c0000gn/T/TestX337468988/001/bench",
	} {
		if err := ValidRemotePath(ok); err != nil {
			t.Errorf("ValidRemotePath(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []struct{ path, why string }{
		{"", "empty"},
		{"nova-bench/x", "relative to a directory on some machine or other"},
		{"~/x", "one element under the home, which is too shallow to rm -rf"},
		{"/x", "one element under the root, which is too shallow to rm -rf"},
		{"~/a/../../etc", "climbs"},
		{"/a/b/../c", "climbs"},
		{"/a//b", "an empty element is two readings of one path"},
		{"/a/b c", "a space"},
		{"/a/$HOME", "a variable the far side would expand"},
		{"/a/b;rm -rf ~", "a second command"},
		{"/a/`whoami`", "a substitution"},
		{"/a/*", "a glob"},
		{"~/a/'b", "a quote"},
	} {
		if err := ValidRemotePath(bad.path); err == nil {
			t.Errorf("ValidRemotePath(%q) = nil; it must be refused: %s", bad.path, bad.why)
		}
	}
}

// RemoteJoin spells a path the way the MACHINE does, always with a slash: a gate driven from
// a Windows laptop would otherwise send a bench a path with backslashes in it.
func TestRemoteJoinAlwaysUsesSlashes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		elems []string
		want  string
	}{
		{[]string{"~/nova-bench/integration", "x"}, "~/nova-bench/integration/x"},
		{[]string{"/data", "x", "repo"}, "/data/x/repo"},
		{[]string{"~/a/", "/b/"}, "~/a/b"},
	} {
		if got := RemoteJoin(c.elems...); got != c.want {
			t.Errorf("RemoteJoin(%v) = %q, want %q", c.elems, got, c.want)
		}
	}
}
