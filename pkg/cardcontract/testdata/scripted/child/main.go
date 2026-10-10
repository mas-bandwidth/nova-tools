// The scripted child of a card-contract family (docs/SPEC-CARD-CONTRACT.md, Writing a
// profile): what the family's models do with a card, as a Go test binary the end-to-end
// test places under the family's name (claude, openai or plain) and hands the member as
// its harness. The words it is called with are the harness's and it reads none of them.
//
//	claude  reads JOB.md, clones the repository it names into a directory of its own,
//	        branches, commits, pushes and opens a pull request.
//	openai  works in a linked worktree of <job>/repo, commits, records a push and
//	        finishes with a pull request whose body is a file.
//	plain   works in the staged checkout <job>/repo, commits, pushes, and writes
//	        RESULT.md in the shape.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	family := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	steps := map[string]func() error{"claude": claude, "openai": openai, "plain": plain}
	step, ok := steps[family]
	if !ok {
		fmt.Fprintf(os.Stderr, "scripted child: no family named %q; place this binary as claude, openai or plain\n", family)
		os.Exit(2)
	}
	if err := step(); err != nil {
		fmt.Fprintln(os.Stderr, "scripted child:", err)
		os.Exit(1)
	}
	fmt.Println("scripted child: done")
}

// do runs one command in dir with the child's own environment and output.
func do(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// out runs one command in dir and returns its output without the final newline.
func out(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Stderr = dir, os.Stderr
	b, err := cmd.Output()
	return strings.TrimRight(string(b), "\n"), err
}

// appendChange adds the change to the file f in dir.
func appendChange(dir string) error {
	f, err := os.OpenFile(filepath.Join(dir, "f"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString("the change\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

var checkout = regexp.MustCompile(`(?m)^You are in a checkout of (\S+) on branch`)

func claude() error {
	job, err := os.ReadFile("JOB.md")
	if err != nil {
		return err
	}
	m := checkout.FindSubmatch(job)
	if m == nil {
		return fmt.Errorf("JOB.md names no checkout")
	}
	tmp, err := os.MkdirTemp("", "child.")
	if err != nil {
		return err
	}
	work := filepath.Join(tmp, "work")
	if err := do("", "git", "clone", string(m[1]), work); err != nil {
		return err
	}
	for _, step := range [][]string{{"checkout", "-q", "-b", "my-feature"}} {
		if err := do(work, "git", step...); err != nil {
			return err
		}
	}
	if err := appendChange(work); err != nil {
		return err
	}
	for _, step := range [][]string{{"commit", "-q", "-am", "the change"}, {"push", "-q", "-u", "origin", "my-feature"}} {
		if err := do(work, "git", step...); err != nil {
			return err
		}
	}
	return do(work, "gh", "pr", "create", "--title", "The change", "--body", "the body, line one\nline two")
}

func openai() error {
	job, err := os.Getwd()
	if err != nil {
		return err
	}
	repo, work, body := filepath.Join(job, "repo"), filepath.Join(job, "worktree"), filepath.Join(job, "pull-request.md")
	if err := do("", "git", "-C", repo, "worktree", "add", "-b", "openai/change", work, "HEAD"); err != nil {
		return err
	}
	if err := appendChange(work); err != nil {
		return err
	}
	for _, step := range [][]string{{"add", "f"}, {"commit", "-q", "-m", "the change"}, {"push", "-u", "origin", "HEAD"}} {
		if err := do(work, "git", step...); err != nil {
			return err
		}
	}
	if err := os.WriteFile(body, []byte("## Summary\n\nthe body, line two\n"), 0o644); err != nil {
		return err
	}
	return do(work, "gh", "pr", "create", "--title", "The change", "--body-file", body)
}

func plain() error {
	job, err := os.Getwd()
	if err != nil {
		return err
	}
	repo := filepath.Join(job, "repo")
	if err := appendChange(repo); err != nil {
		return err
	}
	for _, step := range [][]string{{"commit", "-q", "-am", "the change"}, {"push"}} {
		if err := do(repo, "git", step...); err != nil {
			return err
		}
	}
	head, err := out(repo, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	branch, err := out(repo, "git", "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return err
	}
	result := fmt.Sprintf("head: %s\nbranch: %s\nverdict: ok\ngate: -\noutput: -\nreport: done plainly\n", head, branch)
	return os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(result), 0o644)
}
