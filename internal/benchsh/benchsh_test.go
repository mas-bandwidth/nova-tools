package benchsh_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/benchsh"
)

// TestBenchshArgvIsBashS (#2932 control 3): the argv after the target is
// exactly `bash -s --` plus the quoted args, the script is on stdin, and
// there is no -n.
func TestBenchshArgvIsBashS(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv")
	stdinLog := filepath.Join(dir, "stdin")
	ssh := filepath.Join(dir, "ssh")
	fake := "#!/bin/bash\nprintf '%s\\n' \"$@\" > " + strconv.Quote(argvLog) + "\ncat > " + strconv.Quote(stdinLog) + "\nexit 7\n"
	if err := os.WriteFile(ssh, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "set -eu\necho \"$1 $2\"\n"
	target := benchsh.Target{Host: "bench.example", User: "nova", SSH: ssh}
	res, err := benchsh.Run(context.Background(), target, script, "/srv/results/it's", "$x:refs/y")
	var ee *benchsh.ExitError
	if !errors.As(err, &ee) || ee.Code != 7 || res.Exit != 7 {
		t.Fatalf("run = %+v, %v; want the fake's exit 7 as an ExitError", res, err)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	want := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "nova@bench.example",
		`bash -s -- '/srv/results/it'\''s' '$x:refs/y'`}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("ssh argv =\n%q\nwant\n%q", got, want)
	}
	for _, a := range got {
		if a == "-n" {
			t.Fatalf("argv %q carries -n: the script must reach stdin", got)
		}
	}
	after := got[len(got)-1]
	if !strings.HasPrefix(after, "bash -s -- ") {
		t.Fatalf("remote command %q is not bash -s --", after)
	}
	stdin, err := os.ReadFile(stdinLog)
	if err != nil || string(stdin) != script {
		t.Fatalf("stdin = %q (%v), want the script", stdin, err)
	}
	if strings.Contains(strings.Join(got, " "), "echo") {
		t.Fatalf("the script leaked into argv: %q", got)
	}
	if _, err := benchsh.Run(context.Background(), benchsh.Target{SSH: ssh}, script); err == nil {
		t.Fatal("a target with no host must be refused before ssh")
	}
}

// localSSH is a fake ssh that plays the bench locally: it drops the options
// and the target and hands the remote command word to a login shell's
// stand-in (`bash -c`), with stdin passed through as sshd would.
func localSSH(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	ssh := filepath.Join(t.TempDir(), "ssh")
	fake := `#!/bin/bash
while [ "$1" = "-o" ]; do shift 2; done
shift
exec bash -c "$1"
`
	if err := os.WriteFile(ssh, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	return ssh
}

// TestBenchshInputReachesScript (#3350): with input, bash -s reads only the
// one InputLine off the pipe and the script it execs reads the rest of stdin
// byte for byte (binary, newlines, no trailing newline), with the args as
// $1.. -- the form the deal batch, the secret's value and the release tar
// stream ride.
func TestBenchshInputReachesScript(t *testing.T) {
	ssh := localSSH(t)
	out := filepath.Join(t.TempDir(), "it's out")
	data := make([]byte, 0, 1<<20)
	for i := 0; len(data) < 1<<20; i++ {
		data = append(data, byte(i*7+i/251), '\n', 0, '\'', '"', '$')
	}
	data = append(data, "tail with no newline"...)
	script := "set -eu\numask 077\ncat > \"$1\"\nprintf 'n=%s\\n' \"$#\"\n"
	target := benchsh.Target{Host: "bench.example", SSH: ssh}
	res, err := benchsh.RunInput(context.Background(), target, script, strings.NewReader(string(data)), out, "$x:refs/y")
	if err != nil {
		t.Fatalf("run: %v (%s)", err, res.Output)
	}
	if res.Output != "n=2\n" {
		t.Fatalf("output %q, want n=2 (the args reach the script)", res.Output)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("the script read %d bytes, want the %d bytes of input exactly", len(got), len(data))
	}
	if fi, err := os.Stat(out); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v (%v), want 0600: the script ran, not the login shell", fi.Mode().Perm(), err)
	}
}

// TestBenchshCommandShape (#3350): Options follow BatchMode and
// ConnectTimeout (rounded up to whole seconds), Line joins argv as ssh did,
// and a Command child's exit code reads through Code.
func TestBenchshCommandShape(t *testing.T) {
	target := benchsh.Target{Host: "h", User: "u", ConnectTimeout: 1500 * time.Millisecond, Options: []string{"ForwardAgent=no"}}
	got := strings.Join(benchsh.Argv(target, "a b"), "\x00")
	want := strings.Join([]string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=2", "-o", "ForwardAgent=no", "u@h", `bash -s -- 'a b'`}, "\x00")
	if got != want {
		t.Fatalf("argv %q, want %q", got, want)
	}
	if l := benchsh.Line("cd", "/x", "&&", "ls", "*.md", "2>/dev/null"); l != "cd /x && ls *.md 2>/dev/null" {
		t.Fatalf("Line = %q", l)
	}
	ssh := localSSH(t)
	cmd, err := benchsh.Command(context.Background(), benchsh.Target{Host: "h", SSH: ssh}, "echo out; echo err >&2; exit 3", nil)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if benchsh.Code(err) != 3 || stdout.String() != "out\n" || stderr.String() != "err\n" {
		t.Fatalf("code %d stdout %q stderr %q; want 3, out, err apart", benchsh.Code(err), stdout.String(), stderr.String())
	}
	if _, err := benchsh.Command(context.Background(), benchsh.Target{SSH: ssh}, "true", nil); err == nil {
		t.Fatal("a target with no host must be refused before ssh")
	}
}

// TestBenchshInputLineIsOneLine (#3350): the exec line is one physical line
// (a reader of stdin sees exactly one line before the data) and the script's
// quotes, backslashes, tabs and control bytes survive it.
func TestBenchshInputLineIsOneLine(t *testing.T) {
	script := "x='it'\\''s'\t# tab, then a comment \x01\nprintf '%s\\n' \"$x\" 'back\\slash' \"$1\"\ncat\n"
	line := benchsh.InputLine(script)
	if strings.Count(line, "\n") != 1 || !strings.HasSuffix(line, "\n") {
		t.Fatalf("InputLine = %q, want one line", line)
	}
	ssh := localSSH(t)
	res, err := benchsh.RunInput(context.Background(), benchsh.Target{Host: "h", SSH: ssh}, script, strings.NewReader("in"), "arg")
	if err != nil {
		t.Fatalf("run: %v (%s)", err, res.Output)
	}
	if want := "it's\nback\\slash\narg\nin"; res.Output != want {
		t.Fatalf("output %q, want %q", res.Output, want)
	}
}
