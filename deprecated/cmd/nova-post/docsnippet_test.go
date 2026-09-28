package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDocsCLISnippetRunsAsPrinted: the ```sh block under `## nova-post` in docs/CLI.md runs, line
// by line and as printed, in a clean temp dir, and every line exits 0 (nova-tools #1455, the
// #1920 TestHelpExampleLinesRunAsPrinted pattern: an example exiting 2 is a broken example).
//
// The block is read AS SOURCE from docs/CLI.md, so the lines under test are the ones a reader
// pastes. Shell setup lines (anything not starting `nova-post `) run under `sh -c`; nova-post
// lines run in process through run() with the temp dir as the working directory. The one
// placeholder, `<hash-from-draft>`, is the hash the draft line just printed, which is what the
// prose tells the reader to paste. Remove the setup line and the draft line refuses with
// bad-drafts at exit 2, so this test goes red.
func TestDocsCLISnippetRunsAsPrinted(t *testing.T) {
	lines := docsCLISnippet(t, filepath.Join("..", "..", "docs", "CLI.md"), "## nova-post")
	var draft bool
	for _, l := range lines {
		if strings.HasPrefix(l, "nova-post draft ") {
			draft = true
		}
		if strings.HasSuffix(l, `\`) {
			t.Fatalf("docs/CLI.md nova-post line ends in a continuation backslash, which does not paste: %q", l)
		}
	}
	if !draft {
		t.Fatalf("the docs/CLI.md nova-post block holds no `nova-post draft` line; this test would pass by running nothing: %q", lines)
	}

	t.Chdir(t.TempDir())
	hashRE := regexp.MustCompile(`hash=([0-9a-f]{64})`)
	hash := ""
	for _, line := range lines {
		if !strings.HasPrefix(line, "nova-post ") {
			sh, err := exec.LookPath("sh")
			if err != nil {
				t.Skipf("no sh on PATH to run the setup line %q", line)
			}
			out, err := exec.Command(sh, "-c", line).CombinedOutput()
			if err != nil {
				t.Fatalf("the docs/CLI.md setup line fails (%v):\n  %s\n%s", err, line, out)
			}
			continue
		}
		if strings.Contains(line, "<hash-from-draft>") {
			if hash == "" {
				t.Fatalf("the line %q needs the draft's hash, but no earlier line printed one", line)
			}
			line = strings.ReplaceAll(line, "<hash-from-draft>", hash)
		}
		code, stdout, stderr := cli(strings.Fields(line)[1:]...)
		if code != 0 {
			t.Fatalf("the docs/CLI.md line `%s` exits %d, want 0 -- a line a stranger pastes must run as printed:\n%s%s",
				line, code, stdout, stderr)
		}
		if m := hashRE.FindStringSubmatch(stdout + stderr); m != nil && hash == "" {
			hash = m[1]
		}
	}
	if hash == "" {
		t.Fatal("the draft line printed no hash=<sha256>, so the show line was never exercised")
	}
}

// docsCLISnippet returns the non-blank lines of the first ```sh block under heading in the file.
func docsCLISnippet(t *testing.T, path, heading string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	inSection, inBlock := false, false
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		switch {
		case !inSection:
			inSection = l == heading
		case inBlock && strings.HasPrefix(l, "```"):
			return out
		case inBlock:
			if strings.TrimSpace(l) != "" {
				out = append(out, strings.TrimSpace(l))
			}
		case strings.HasPrefix(l, "## "):
			t.Fatalf("%s: the %q section holds no ```sh block", path, heading)
		case strings.HasPrefix(l, "```sh"):
			inBlock = true
		}
	}
	t.Fatalf("%s: no %q section with a closed ```sh block", path, heading)
	return nil
}
