package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
)

// commitFormat is one record of git log: a marker, the sha, the subject, the
// committer time. The time is what --no-walk=sorted orders by; the parser does
// not need it once git has ordered the records.
const commitFormat = "COMMIT%x1f%H%x1f%s%x1f%ct"

// shaish is a commit sha as a list file may write it.
var shaish = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// testFuncLine is a Go test function at the start of a line.
var testFuncLine = regexp.MustCompile(`^func (Test[A-Za-z0-9_]*)\(`)

// commitsInRange is the commits reachable from b and not from a, oldest first.
// glob, when set, keeps only commits that touch a matching path.
func commitsInRange(dir, spec, glob string) ([]cardgen.Commit, []string, error) {
	if strings.Count(spec, "..") != 1 || strings.Contains(spec, "...") {
		return nil, nil, fmt.Errorf("--range %q; want a..b, commits reachable from b and not from a", spec)
	}
	args := []string{"log", "--reverse", "--name-status", "--format=" + commitFormat, spec}
	if glob != "" {
		args = append(args, "--", pathspec(glob))
	}
	return readCommits(dir, args)
}

// commitsInFile is the commits named by a list file, one sha per line (a subject
// may follow it; a blank line or a # comment is skipped), oldest first whatever
// order the file used.
func commitsInFile(dir, file string) ([]cardgen.Commit, []string, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read %s: %s", file, err.Error())
	}
	var shas []string
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	n := 0
	for sc.Scan() {
		n++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		sha := strings.Fields(text)[0]
		if !shaish.MatchString(sha) {
			return nil, nil, fmt.Errorf("%s:%d: %q is not a commit sha", file, n, sha)
		}
		shas = append(shas, sha)
	}
	if len(shas) == 0 {
		return nil, nil, nil
	}
	args := []string{"log", "--reverse", "--no-walk=sorted", "--name-status", "--format=" + commitFormat}
	args = append(args, shas...)
	return readCommits(dir, args)
}

func gitArgs(args ...string) []string {
	return append([]string{"-c", "color.ui=never", "-c", "core.quotepath=false", "--no-pager"}, args...)
}

func pathspec(glob string) string {
	if strings.Contains(glob, ":(") {
		return glob
	}
	return ":(glob)" + glob
}

func readCommits(dir string, args []string) ([]cardgen.Commit, []string, error) {
	out, err := gitOut(dir, args...)
	if err != nil {
		return nil, nil, err
	}
	var commits []cardgen.Commit
	var notes []string
	for _, raw := range parseCommitLog(out) {
		if len(raw.files) == 0 {
			short := raw.sha
			if len(short) > 12 {
				short = short[:12]
			}
			notes = append(notes, short+": no files")
			continue
		}
		test, err := testOf(dir, raw.sha, raw.files)
		if err != nil {
			return nil, nil, err
		}
		commits = append(commits, cardgen.Commit{SHA: raw.sha, Subject: raw.subject, Files: raw.files, Test: test})
	}
	return commits, notes, nil
}

type rawCommit struct {
	sha, subject string
	files        []string
}

func parseCommitLog(text string) []rawCommit {
	var out []rawCommit
	var cur *rawCommit
	flush := func() {
		if cur == nil {
			return
		}
		out = append(out, *cur)
		cur = nil
	}
	for _, line := range strings.Split(text, "\n") {
		rest, ok := strings.CutPrefix(line, "COMMIT\x1f")
		if ok {
			flush()
			parts := strings.Split(rest, "\x1f")
			if len(parts) < 2 || parts[0] == "" {
				continue
			}
			cur = &rawCommit{sha: parts[0], subject: parts[1]}
			continue
		}
		if cur == nil || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		cur.files = append(cur.files, fields[1:]...)
	}
	flush()
	return out
}

// testOf is the test a commit already has: a Test function the diff adds, else
// the first Test function in a test file it touches, else the first in one of
// its packages at that commit. "" when there is none.
func testOf(dir, sha string, files []string) (string, error) {
	diff, err := gitOut(dir, "show", "--format=", "--unified=0", sha)
	if err != nil {
		return "", err
	}
	if pkg, name := addedTest(diff); name != "" {
		return pkg + " " + name, nil
	}
	pkg, name, err := grepTest(dir, sha, testFiles(files))
	if err != nil || name != "" {
		if name == "" {
			return "", err
		}
		return pkg + " " + name, nil
	}
	var globs []string
	seen := map[string]bool{}
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		pkgDir := pathDir(f)
		if pkgDir == "" || pkgDir == "." || seen[pkgDir] {
			continue
		}
		seen[pkgDir] = true
		globs = append(globs, ":(glob)"+pkgDir+"/*_test.go")
	}
	pkg, name, err = grepTest(dir, sha, globs)
	if err != nil || name == "" {
		return "", err
	}
	return pkg + " " + name, nil
}

func testFiles(files []string) []string {
	var out []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	return out
}

func pathDir(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "."
	}
	return p[:i]
}

// addedTest is the first test function a unified diff adds, and the package of
// the file it was added to.
func addedTest(diff string) (pkg, name string) {
	file := ""
	for _, line := range strings.Split(diff, "\n") {
		if rest, ok := strings.CutPrefix(line, "+++ "); ok {
			file = ""
			if path, ok := strings.CutPrefix(rest, "b/"); ok && path != "" {
				file = strings.Fields(path)[0]
			}
			continue
		}
		if !strings.HasPrefix(line, "+") || !strings.HasSuffix(file, "_test.go") {
			continue
		}
		if m := testFuncLine.FindStringSubmatch(strings.TrimPrefix(line, "+")); m != nil {
			return pathDir(file), m[1]
		}
	}
	return "", ""
}

// grepTest runs git grep for a Test function at sha, limited to paths. No paths,
// or a grep that finds nothing, is ("", "", nil).
func grepTest(dir, sha string, paths []string) (pkg, name string, err error) {
	if len(paths) == 0 {
		return "", "", nil
	}
	args := []string{"grep", "-n", "-e", "^func Test", sha, "--"}
	args = append(args, paths...)
	res, runErr := gitrun.Run(context.Background(), gitrun.Options{C: dir, OwnRepo: true}, gitArgs(args...)...)
	if runErr != nil {
		if strings.TrimSpace(string(res.Stdout)) == "" {
			return "", "", nil
		}
		return "", "", fmt.Errorf("git %s in %s: %s", strings.Join(args, " "), dir, strings.TrimSpace(string(res.Stderr)))
	}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		// sha:path:lineno:func TestX
		parts := strings.SplitN(line, ":", 4)
		if len(parts) < 4 {
			continue
		}
		if m := testFuncLine.FindStringSubmatch(parts[3]); m != nil {
			return pathDir(parts[1]), m[1], nil
		}
	}
	return "", "", nil
}

func gitOut(dir string, args ...string) (string, error) {
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: dir, OwnRepo: true}, gitArgs(args...)...)
	if err != nil {
		msg := strings.TrimSpace(string(res.Stderr))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s in %s: %s", strings.Join(args, " "), dir, msg)
	}
	return string(res.Stdout), nil
}
