package main

import (
	"bytes"
	"strings"
	"testing"
)

// docs/SPEC-VERSION.md, snapshot/diff rule 8: every binary answers `version`
// and `--version` with the identical line, nova-wake among them, and a second
// argument is a refusal at exit 2. nova-wake was the one binary still missing
// the double-dash spelling.
func TestVersionAndDoubleDashVersionAgree(t *testing.T) {
	var single, singleErr, doubled, doubledErr bytes.Buffer
	if code := run([]string{"version"}, &single, &singleErr); code != 0 {
		t.Fatalf("version exit %d: %s", code, singleErr.String())
	}
	if code := run([]string{"--version"}, &doubled, &doubledErr); code != 0 {
		t.Fatalf("--version exit %d: %s", code, doubledErr.String())
	}
	if single.String() != doubled.String() {
		t.Fatalf("version and --version disagree:\nversion:   %q\n--version: %q", single.String(), doubled.String())
	}
	line := strings.TrimSuffix(single.String(), "\n")
	if strings.Count(single.String(), "\n") != 1 || !strings.HasPrefix(line, "nova-wake ") || len(strings.Fields(line)) != 4 {
		t.Fatalf("not the four-token one-line version shape: %q", single.String())
	}
	for _, verb := range []string{"version", "--version"} {
		var out, errb bytes.Buffer
		if code := run([]string{verb, "extra"}, &out, &errb); code != 2 {
			t.Errorf("%s extra: exit %d, want 2", verb, code)
		}
		if out.Len() != 0 {
			t.Errorf("%s extra: a refusal printed a version line: %q", verb, out.String())
		}
	}
}
