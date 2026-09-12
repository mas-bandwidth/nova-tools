//go:build darwin

package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rule 1: a machine with no backend REFUSES, and the command does not run. The seam is
// the `available` variable, because the behaviour under test is the refusal, not the
// platform — on a Mac sandbox-exec is always there, so without the seam this rule has
// no test at all on the only platform whose body is built.
func TestNoSandboxRefusesOnDarwin(t *testing.T) {
	saved := available
	available = func() (string, bool) { return "", false }
	defer func() { available = saved }()

	write := t.TempDir()
	home := filepath.Join(write, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	p, bad := Build(in(t, write, t.TempDir(), home, "/bin/echo"))
	if len(bad) > 0 {
		t.Fatalf("refused at build: %v", bad)
	}
	var out, errb bytes.Buffer
	code, err := Run(p, os.Environ(), strings.NewReader(""), &out, &errb, nil)
	if code != ExitRefused {
		t.Errorf("exit %d, want %d", code, ExitRefused)
	}
	var r Refusal
	if !asRef(err, &r) || r.Reason != "no_sandbox" {
		t.Fatalf("err = %v, want a no_sandbox refusal", err)
	}
	if out.Len() != 0 {
		t.Errorf("the command produced output; it must not have run: %q", out.String())
	}
}

func asRef(err error, out *Refusal) bool {
	r, ok := err.(Refusal)
	if ok {
		*out = r
	}
	return ok
}

// Revision 7, test 20 rewritten: the wrap writes NO profile file. The first --write holds
// no .nova-sandbox-*.sb at any point, because the text goes to sandbox-exec with -p.
func TestNoProfileFileIsWritten(t *testing.T) {
	write := t.TempDir()
	home := filepath.Join(write, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	p, bad := Build(in(t, write, t.TempDir(), home, "/bin/echo"))
	if len(bad) > 0 {
		t.Fatalf("refused: %v", bad)
	}
	var out, errb bytes.Buffer
	if _, err := Run(p, ChildEnv(append(os.Environ(), "HOME="+home), p.Tmp), strings.NewReader(""), &out, &errb, nil); err != nil {
		t.Fatalf("run: %v (%s)", err, errb.String())
	}
	entries, err := os.ReadDir(write)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), profileFilePrefx) && strings.HasSuffix(e.Name(), ".sb") {
			t.Errorf("the wrap left a profile file in the write set: %s", e.Name())
		}
	}
}
