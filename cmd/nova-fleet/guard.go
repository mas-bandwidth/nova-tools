package main

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const guardHelp = `nova-fleet commit-guard [--rules FILE] [--repo DIR] <revision>...
nova-fleet commit-guard [--rules FILE] [--repo DIR] --pre-push <remote> [<url>]

Refuse a commit whose author or committer address is a guarded person's and whose message carries
an AI marker: an AI made it under that person's identity. The person's own unmarked commits pass,
as does every commit under another identity. The commits are git log <revision>... in --repo
(default the current directory), such as origin/main..HEAD; with --pre-push, those a push sends:
stdin is the pre-push hook's "<local ref> <local sha> <remote ref> <remote sha>" lines, less what
the remote's old sha or any remote-tracking branch reaches. The push-guard play installs
the hook (nova-fleet help push-guard).

--rules FILE (default ~/.config/nova-fleet/commit-guard, which push-guard writes), one rule a line,
# a comment, at least one of each kind; both compare without case:
  person <address>   a guarded person's commit address
  marker <text>      a message line holding <text> marks the commit (Co-Authored-By: Claude)

Each refused commit is one line on stderr, then exit 1:
  nova-fleet commit-guard: commit <sha> (author <name> <address>, committer <name> <address>) carries the AI marker "<line>" under a guarded address; amend it as the seat's own identity (git config user.email <the seat's address>), run: git rebase --exec 'git commit --amend --no-edit --reset-author' <sha>~1
(--root for a root commit; it resets every later commit's author too; the commit at HEAD alone:
git commit --amend --no-edit --reset-author).
Otherwise COMMIT-GUARD OK commits=<n>. Exit: 0 every commit passes; 1 a commit is refused; 2 could
not run (rules missing or malformed, a bad argument, git failed).
`

// guardRules are the guarded addresses (lower case) and the AI markers (lower case) of a rules file.
type guardRules struct {
	people  map[string]bool
	markers []string
}

// parseRules reads a rules file's text: person and marker lines, comments and blank lines.
func parseRules(text string) (guardRules, error) {
	r := guardRules{people: map[string]bool{}}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kind, value, _ := strings.Cut(line, " ")
		value = strings.ToLower(strings.TrimSpace(value))
		switch {
		case value == "":
			return r, fmt.Errorf("line %d: %s has no value", i+1, kind)
		case kind == "person":
			r.people[value] = true
		case kind == "marker":
			r.markers = append(r.markers, value)
		default:
			return r, fmt.Errorf("line %d: %q is neither person nor marker", i+1, kind)
		}
	}
	if len(r.people) == 0 || len(r.markers) == 0 {
		return r, fmt.Errorf("want at least one person and one marker line, have %d and %d", len(r.people), len(r.markers))
	}
	return r, nil
}

// guardCommit is one commit as git log prints it with guardFormat.
type guardCommit struct {
	sha, parents, authorName, authorMail, committerName, committerMail, body string
}

// guardFormat separates a commit's fields by 0x1f; git log -z ends each commit with a NUL. The body is last, so a
// 0x1f inside it stays in the body.
const guardFormat = "--format=%H%x1f%P%x1f%an%x1f%ae%x1f%cn%x1f%ce%x1f%B"

func parseLog(out string) ([]guardCommit, error) {
	var cs []guardCommit
	for rec := range strings.SplitSeq(out, "\x00") {
		rec = strings.TrimPrefix(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\x1f", 7)
		if len(f) != 7 {
			return nil, fmt.Errorf("git log printed a record of %d fields, want 7", len(f))
		}
		cs = append(cs, guardCommit{f[0], f[1], f[2], f[3], f[4], f[5], f[6]})
	}
	return cs, nil
}

// judge is the marker line that refuses c under r, or "" when c passes.
func (r guardRules) judge(c guardCommit) string {
	if !r.people[strings.ToLower(c.authorMail)] && !r.people[strings.ToLower(c.committerMail)] {
		return ""
	}
	for line := range strings.SplitSeq(c.body, "\n") {
		low := strings.ToLower(line)
		for _, m := range r.markers {
			if strings.Contains(low, m) {
				return strings.TrimSpace(line)
			}
		}
	}
	return ""
}

// refusal is the stderr line for a refused commit: who made it, the marker, and the rebase that rewrites it.
func (c guardCommit) refusal(marker string) string {
	base := c.sha + "~1"
	if c.parents == "" {
		base = "--root"
	}
	return fmt.Sprintf("nova-fleet commit-guard: commit %s (author %s <%s>, committer %s <%s>) carries the AI marker %q under a guarded address; "+
		"amend it as the seat's own identity (git config user.email <the seat's address>), run: git rebase --exec 'git commit --amend --no-edit --reset-author' %s",
		c.sha, c.authorName, c.authorMail, c.committerName, c.committerMail, marker, base)
}

const zeroSHA = "0000000000000000000000000000000000000000"

// prePushRevs turns the pre-push hook's stdin into git log arguments: every pushed sha, less what the remote had
// (each old sha) and what any remote-tracking branch reaches, so a push to a URL or a remote never fetched does not
// check history already published elsewhere. A deletion pushes nothing; none left is nil.
func prePushRevs(lines string) ([]string, error) {
	var want, have []string
	for line := range strings.SplitSeq(lines, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 4 {
			return nil, fmt.Errorf("pre-push line %q has %d fields, want 4", line, len(f))
		}
		local, old := f[1], f[3]
		if strings.Trim(local, "0") == "" {
			continue
		}
		want = append(want, local)
		if strings.Trim(old, "0") != "" {
			have = append(have, "^"+old)
		}
	}
	if len(want) == 0 {
		return nil, nil
	}
	revs := append([]string{"--ignore-missing"}, want...)
	revs = append(revs, have...)
	return append(revs, "--not", "--remotes"), nil
}

func runCommitGuard(args []string, stdout, stderr io.Writer) int {
	return commitGuard(args, os.Stdin, outCmd, stdout, stderr)
}

// commitGuard is the verb with its seams: the hook's stdin and the program runner git goes through.
func commitGuard(args []string, stdin io.Reader, git cmdFunc, stdout, stderr io.Writer) int {
	refuse := func(code int, format string, a ...any) int {
		fmt.Fprintf(stderr, "nova-fleet commit-guard: %s\n", fmt.Sprintf(format, a...))
		return code
	}
	var rules, repo string
	var revs []string
	prePush := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-h", "--help":
			fmt.Fprint(stdout, guardHelp)
			return 0
		case "--rules", "--repo", "--pre-push":
			if i+1 >= len(args) || args[i+1] == "" {
				return refuse(2, "%s wants a value; run: nova-fleet commit-guard -h", a)
			}
			i++
			switch a {
			case "--rules":
				rules = args[i]
			case "--repo":
				repo = args[i]
			default:
				prePush = true
			}
		default:
			if strings.HasPrefix(a, "-") {
				return refuse(2, "unknown flag %s; run: nova-fleet commit-guard -h", a)
			}
			revs = append(revs, a)
		}
	}
	switch {
	case prePush && len(revs) > 1:
		return refuse(2, "--pre-push takes the hook's <remote> [<url>], not revisions (%s); run: nova-fleet commit-guard -h", strings.Join(revs[1:], " "))
	case !prePush && len(revs) == 0:
		return refuse(2, "want a revision such as origin/main..HEAD, or --pre-push <remote>; run: nova-fleet commit-guard -h")
	}
	if rules == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return refuse(2, "no home directory for the default rules (%v); run: nova-fleet commit-guard --rules <file> ...", err)
		}
		rules = filepath.Join(home, ".config", "nova-fleet", "commit-guard")
	}
	text, err := os.ReadFile(rules)
	if err != nil {
		return refuse(2, "the rules cannot be read (%v); the push-guard play writes them, run: nova-fleet help push-guard", err)
	}
	r, err := parseRules(string(text))
	if err != nil {
		return refuse(2, "rules %s: %v; run: nova-fleet commit-guard -h", rules, err)
	}
	if prePush {
		in, err := io.ReadAll(stdin)
		if err != nil {
			return refuse(2, "reading the pre-push lines from stdin: %v; run: nova-fleet commit-guard -h", err)
		}
		if revs, err = prePushRevs(string(in)); err != nil {
			return refuse(2, "%v; run: nova-fleet commit-guard -h", err)
		}
	}
	var commits []guardCommit
	if len(revs) > 0 {
		at := []string{"--no-replace-objects"}
		if repo != "" {
			at = append(at, "-C", repo)
		}
		out, why, code := git("git", slices.Concat(at, []string{"log", "-z", guardFormat}, revs), os.Environ())
		if code != 0 {
			return refuse(2, "git log exit %d: %s; run: %s", code, cmp.Or(oneLine(why), "(nothing on stderr)"), shellJoin(slices.Concat([]string{"git"}, at, []string{"log", "--oneline"}, revs)))
		}
		if commits, err = parseLog(out); err != nil {
			return refuse(2, "%v; run: git --version", err)
		}
	}
	refused := 0
	for _, c := range commits {
		if m := r.judge(c); m != "" {
			fmt.Fprintln(stderr, c.refusal(m))
			refused++
		}
	}
	if refused > 0 {
		return 1
	}
	fmt.Fprintf(stdout, "COMMIT-GUARD OK commits=%d\n", len(commits))
	return 0
}

// shellJoin is argv as one pasteable shell line: a word of plain characters as it is, any other single-quoted.
func shellJoin(argv []string) string {
	words := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@^~+,") == "" {
			words[i] = a
		} else {
			words[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(words, " ")
}
