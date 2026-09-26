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

	"github.com/mas-bandwidth/nova-tools/internal/benchsh"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// TestBenchshArgvIsBashS (#2932 control 3): the argv after the target is
// exactly `bash -s --` plus the quoted args, the script is on stdin, and
// there is no -n.
func TestBenchshArgvIsBashS(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv")
	stdinLog := filepath.Join(dir, "stdin")
	ssh := filepath.Join(dir, "ssh")
	fake := "#!/bin/bash\nprintf '%s\\n' \"$@\" > " + strconv.Quote(argvLog) + "\ncat > " + strconv.Quote(stdinLog) + "\nexit 7\n"
	if err := testbin.WriteExecutable(ssh, []byte(fake), 0o755); err != nil {
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
