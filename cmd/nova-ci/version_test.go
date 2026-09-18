package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

// The shape, asserted field by field. `nova-ci <version> <goos>/<goarch> <go version>` is
// the one line every shipped binary prints, so a release assertion can read the identity
// out of field two without knowing which tool wrote it -- and asserting only that the
// output "contains" the version would pass over a line broken in two.
func TestVersionLineShape(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cmdVersion(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("wrote to stderr: %q", stderr.String())
	}
	line := stdout.String()
	if !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1 {
		t.Fatalf("want exactly one terminated line, got %q", line)
	}
	fields := strings.Fields(strings.TrimSuffix(line, "\n"))
	if len(fields) != 4 {
		t.Fatalf("want 4 fields, got %d: %q", len(fields), line)
	}
	if fields[0] != "nova-ci" {
		t.Errorf("field 1 is the binary's name: got %q", fields[0])
	}
	if fields[1] == "" {
		t.Errorf("field 2 is the version and is never empty: %q", line)
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; fields[2] != want {
		t.Errorf("field 3: got %q, want %q", fields[2], want)
	}
	if fields[3] != runtime.Version() {
		t.Errorf("field 4: got %q, want %q", fields[3], runtime.Version())
	}
}

// The stamp is the ONE field that comes from outside the toolchain: a release's ${TAG} is
// a shell variable, and a build stamped with a newline must not make this line say two
// things.
func TestVersionLineHoldsWhateverTheStampContains(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "v1.2.3\nnova-ci v9.9.9 linux/amd64 go1.0 extra"

	var stdout, stderr bytes.Buffer
	if code := cmdVersion(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, stderr.String())
	}
	line := stdout.String()
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

// Both spellings reach the verb through the dispatch, from the day the verb lands.
func TestVersionVerbIsReachableFromTheDispatch(t *testing.T) {
	for _, verb := range []string{"version", "--version"} {
		t.Run(verb, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{verb}, strings.NewReader(""), &stdout, &stderr); code != 0 {
				t.Fatalf("exit %d, want 0\nstderr: %s", code, stderr.String())
			}
			if !strings.HasPrefix(stdout.String(), "nova-ci ") {
				t.Errorf("not the version line: %q", stdout.String())
			}
			if strings.Count(stdout.String(), "\n") != 1 {
				t.Errorf("not one line: %q", stdout.String())
			}
		})
	}
}

func TestVersionRefusesFlagsAndArguments(t *testing.T) {
	for _, args := range [][]string{{"--budget", "60"}, {"extra"}} {
		var stdout, stderr bytes.Buffer
		if code := cmdVersion(args, &stdout, &stderr); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
		if stdout.Len() != 0 {
			t.Errorf("%v: a refusal printed a version line anyway: %q", args, stdout.String())
		}
		if !strings.Contains(stderr.String(), "takes no flags and no arguments") {
			t.Errorf("%v: refusal does not say why: %q", args, stderr.String())
		}
	}
}

// The banner names it, because a verb a reader cannot find is a verb that answers nobody.
func TestVersionIsInTheBanner(t *testing.T) {
	if !strings.Contains(usage, "nova-ci version") {
		t.Error("the usage block does not list the version verb")
	}
}
