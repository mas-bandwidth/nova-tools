//go:build functional

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadBriefPRAndPostFileVerbs is the verb wiring of nova-tools #4335 on
// a throwaway redis-server: `read brief --pr 3 --no-github` prints the one
// screen (the hand PR's body is a GAP, exit 1), and
// `read post --file` posts each row with a ROW receipt and prints the
// refusal of the row it cannot post. Run with -tags functional.
func TestReadBriefPRAndPostFileVerbs(t *testing.T) {
	t.Parallel()
	mirror, addr, head := readFixtureStore(t)

	code, stdout, stderr := runSprint("read", "brief", "--pr", "3", "--mirror", mirror, "--no-github", "--redis", addr)
	if code != 1 || stderr != "" {
		t.Fatalf("brief --pr: exit %d stderr %q\n%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"READ BRIEF mas-bandwidth/nova-tools#3 head=" + head,
		"== FILES 1 (+1 -0) ", "a.txt", "record: true",
		"READ BRIEF GAP mas-bandwidth/nova-tools#3 pr body: pr:nova-tools:3 has no pr_body",
		"READ BRIEF INCOMPLETE mas-bandwidth/nova-tools#3", "github_calls=0",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("brief --pr lacks %q:\n%s", want, stdout)
		}
	}

	file := filepath.Join(t.TempDir(), "scores.tsv")
	rows := "nova-tools\t3\tHOLD who=ann head=" + head + " score=6/10\nnova-tools\t3\tSCORE who=bob score=9/10\n"
	if err := os.WriteFile(file, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runSprint("read", "post", "--file", file, "--mirror", mirror, "--no-github", "--redis", addr)
	if code != 1 || !strings.Contains(stdout, "ROW 1 READ POST repo=nova-tools n=3 kind=HOLD") ||
		!strings.Contains(stdout, "READ POST FILE file="+file+" rows=2 posted=1 refused=1 github_calls=0") ||
		!strings.Contains(stderr, "ROW 2 READ POST REFUSED repo=nova-tools n=3 why=line has no head=") {
		t.Fatalf("post --file: exit %d\nstdout %s\nstderr %s", code, stdout, stderr)
	}
}
