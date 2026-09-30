//go:build functional

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// THE BANNER'S FIRST-SEND RECIPE RUNS AS PRINTED. `nova-bus help` carries a recipe from
// nothing to a first send (the ROSTER AND LANES paragraph). A recipe that is prose rots
// the day a rule changes, and the two that rotted first were a draft written inside the
// checkout (an untracked file, so send refused) and an inbox read as a sender-less name.
// This test reads the recipe out of the banner, line by line, and RUNS it in a temp
// directory: git lines through git, nova-bus lines through the tool, the roster the
// paragraph prints saved as participants.json where the recipe says, the draft's
// placeholder body replaced as the recipe says. Every command must exit 0.
//
// It needs git and nothing else; nothing reaches a network (--no-push), everything is
// under t.TempDir().
func TestTheBannersFirstSendRecipeRunsAsPrinted(t *testing.T) {
	t.Parallel()
	lines := strings.Split(invoke(t, "", "help").stdout, "\n")
	var roster string
	var recipe []string
	for i := 0; i < len(lines); i++ {
		text := strings.TrimSpace(lines[i])
		if roster == "" && strings.HasPrefix(text, `{"participants":[`) {
			cur, end := text, i
			for strings.Count(cur, "{") != strings.Count(cur, "}") || !strings.HasSuffix(cur, "}") {
				end++
				cur += strings.TrimSpace(lines[end])
			}
			// the paragraph's roster (with its group) is the one the recipe says to save
			if strings.Contains(cur, `"groups"`) {
				roster, i = cur, end
			}
		}
		if strings.HasPrefix(text, "From nothing to a first send") {
			// the recipe's own sentence runs on to its first indented command
			for i++; i < len(lines) && !strings.HasPrefix(lines[i], "  git "); i++ {
			}
			for ; i < len(lines) && strings.HasPrefix(lines[i], "  ") && strings.TrimSpace(lines[i]) != ""; i++ {
				recipe = append(recipe, strings.TrimSpace(lines[i]))
			}
		}
	}
	if roster == "" {
		t.Fatal("the banner's roster paragraph has no roster")
	}
	if len(recipe) < 4 {
		t.Fatalf("the banner's recipe has %d lines, want its five commands: %q", len(recipe), recipe)
	}

	root := t.TempDir()
	cwd := root
	// abs resolves a path the recipe writes against the directory the recipe is in, as the
	// shell would: nothing the recipe names is ever relative to this package's directory.
	abs := func(arg string) string {
		if filepath.IsAbs(arg) {
			return arg
		}
		return filepath.Join(cwd, arg)
	}
	ident := append(os.Environ(), "GIT_AUTHOR_NAME=Ada", "GIT_AUTHOR_EMAIL=ada@example.com", "GIT_COMMITTER_NAME=Ada", "GIT_COMMITTER_EMAIL=ada@example.com")
	var sawSend, sawInbox bool
	for _, line := range recipe {
		if i := strings.Index(line, "  ("); i >= 0 {
			line = strings.TrimSpace(line[:i]) // a trailing parenthesis is the recipe's comment
		}
		for _, part := range strings.Split(line, " && ") {
			fields := strings.Fields(part)
			out := ""
			for i, f := range fields {
				if f == ">" && i+1 < len(fields) {
					out = abs(fields[i+1])
					fields = fields[:i]
					break
				}
			}
			switch fields[0] {
			case "cd":
				cwd = abs(filepath.Join(cwd, fields[1]))
			case "git":
				cmd := exec.Command("git", fields[1:]...)
				cmd.Dir, cmd.Env = cwd, ident
				if b, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%q: %v\n%s", part, err, b)
				}
				if fields[1] == "init" {
					// the recipe says to save the roster as participants.json in the new directory
					if err := os.WriteFile(filepath.Join(root, "bus", "participants.json"), []byte(roster), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			case "nova-bus":
				args := append([]string(nil), fields[1:]...)
				for i := 0; i+1 < len(args); i++ {
					if args[i] == "--bus" || args[i] == "--file" || args[i] == "--out" {
						args[i+1] = abs(args[i+1])
					}
				}
				var stdout, stderr bytes.Buffer
				if code := run(args, strings.NewReader(""), &stdout, &stderr, now()); code != 0 {
					t.Fatalf("%q: exit %d\nstdout: %s\nstderr: %s", part, code, stdout.String(), stderr.String())
				}
				if out != "" {
					body := strings.ReplaceAll(stdout.String(), "<the note goes here>", "Hello Bo, the recipe runs.")
					if err := os.WriteFile(out, []byte(body), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				sawSend = sawSend || strings.Contains(stdout.String(), "SEND OK")
				sawInbox = sawInbox || strings.Contains(stdout.String(), "INBOX OK")
			default:
				t.Fatalf("the recipe runs %q, which this test does not know how to run", part)
			}
		}
	}
	if !sawSend || !sawInbox {
		t.Errorf("the recipe ran without printing SEND OK (%v) and INBOX OK (%v)", sawSend, sawInbox)
	}
}
