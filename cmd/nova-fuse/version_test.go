package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The SHAPE, asserted field by field. `nova-fuse <identity> <goos>/<goarch> <go version>` is
// what a person is asked to paste when two lines disagree about what they are running, so
// a run of it has to be one line and four tokens -- and asserting only that the output
// "contains" a version would pass over a line broken in two, which is the failure the
// escaping in internal/buildinfo exists to prevent.
//
// Before this verb existed the same invocation was:
//
//	nova-fuse: unknown subcommand "version"; run: nova-fuse help
//
// on stderr at exit 2.
func TestVersionLineShape(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if code := cmdVersion(nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("wrote to stderr: %q", errOut.String())
	}
	line := out.String()
	if !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1 {
		t.Fatalf("want exactly one terminated line, got %q", line)
	}
	fields := strings.Fields(strings.TrimSuffix(line, "\n"))
	if len(fields) != 4 {
		t.Fatalf("want 4 fields, got %d: %q", len(fields), line)
	}
	if fields[0] != "nova-fuse" {
		t.Errorf("field 1 is the binary's name: got %q", fields[0])
	}
	if fields[1] == "" {
		t.Errorf("field 2 is the build identity and is never empty: %q", line)
	}
	if strings.ContainsAny(fields[1], " \t") {
		t.Errorf("field 2 is one token: %q", line)
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; fields[2] != want {
		t.Errorf("field 3: got %q, want %q", fields[2], want)
	}
	if fields[3] != runtime.Version() {
		t.Errorf("field 4: got %q, want %q", fields[3], runtime.Version())
	}
}

// The stamp is the ONE field of this line that comes from outside the toolchain, and a
// release workflow's ${TAG} is a shell variable.
func TestVersionLineHoldsWhateverTheStampContains(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "v1.2.3\nnova-fuse v9.9.9 linux/amd64 go1.0 extra"

	var out, errOut bytes.Buffer
	if code := cmdVersion(nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	line := out.String()
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("a stamped newline broke the line in two: %q", line)
	}
	if fields := strings.Fields(strings.TrimSuffix(line, "\n")); len(fields) != 4 {
		t.Fatalf("want 4 fields whatever the stamp holds, got %d: %q", len(fields), line)
	}
	if !strings.Contains(line, `v1.2.3\x0a`) {
		t.Errorf("the stamp is escaped rather than dropped or printed raw: %q", line)
	}
}

// A stamped build says the tag and an unstamped one says what the toolchain recorded:
// either way field two is an identity, never a dotted number this file made up.
func TestVersionIdentityIsTheStampWhenThereIsOne(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "v9.9.9"
	var out, errOut bytes.Buffer
	if code := cmdVersion(nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	if got := strings.Fields(out.String())[1]; got != "v9.9.9" {
		t.Errorf("field 2 is the stamp: got %q", got)
	}
}

func TestVersionRefusesFlagsAndArguments(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--short"}, {"extra"}, {"--box", "."}} {
		var out, errOut bytes.Buffer
		if code := cmdVersion(args, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
		if out.Len() != 0 {
			t.Errorf("%v: a refusal printed a version line anyway: %q", args, out.String())
		}
		if !strings.Contains(errOut.String(), "takes no flags and no arguments") {
			t.Errorf("%v: refusal does not say why: %q", args, errOut.String())
		}
	}
}

// The wiring, which lives in main.go's dispatch and is one line there: this test is what
// will catch it if that line is ever removed.
func TestVersionVerbIsReachableFromTheDispatch(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"version", "--version"} {
		var out, errOut bytes.Buffer
		if code := run([]string{verb}, &out, &errOut, time.Now().UTC()); code != 0 {
			t.Errorf("%s: exit %d, want 0\nstderr: %s", verb, code, errOut.String())
			continue
		}
		if !strings.HasPrefix(out.String(), "nova-fuse ") || strings.Count(out.String(), "\n") != 1 {
			t.Errorf("%s: not the version line: %q", verb, out.String())
		}
	}
}
