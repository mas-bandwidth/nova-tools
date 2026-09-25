package dogfood

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ParseAuthors reads a verb→author mapping. One verb per line:
//
//	nova-check links = Rowan
//	nova-fuse lift quarantine = Stella
//	# blank lines and # comments are ignored
//
// The left of the `=` is the verb exactly as docs/CLI.md spells it, tool
// included; the right is the person who wrote it. A line without an `=`, an
// empty name, or a verb named twice is refused with the line number, because a
// mapping that is half wrong makes the gate's answer half wrong in a direction
// nobody can see.
func ParseAuthors(path string) (Authors, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("authors: %w", err)
	}
	defer f.Close()

	authors := Authors{}
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, name, found := strings.Cut(text, "=")
		if !found {
			return nil, fmt.Errorf("authors: %s:%d: no `=`; a line is `<tool> <verb> = <who wrote it>`", path, line)
		}
		key = normalizeKey(key)
		name = strings.TrimSpace(name)
		if key == "" || name == "" {
			return nil, fmt.Errorf("authors: %s:%d: a line is `<tool> <verb> = <who wrote it>`; one side is empty", path, line)
		}
		if _, dup := authors[key]; dup {
			return nil, fmt.Errorf("authors: %s:%d: %q is mapped twice; one verb, one author", path, line, key)
		}
		authors[key] = name
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("authors: %w", err)
	}
	return authors, nil
}

// Runner runs one command and returns its standard output. It is the only way
// this package reaches outside the process, and it is a parameter so the tests
// can answer without a git of their own. Nothing here reaches the network.
type Runner func(ctx context.Context, dir string, args ...string) (string, error)

// GitRunner runs git in a directory, with the context's deadline.
func GitRunner(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Progress is told how far along a slow read is. The CLI prints it on stderr
// once the read has taken longer than a tenth of a second: a program says what
// it is doing when what it is doing takes long enough to look like a hang.
type Progress func(done, total int)

// AuthorsFromGit works out who wrote each verb by asking the repository. For
// each verb it takes the FIRST commit that changed the number of times the
// verb's own word appears under `cmd/<tool>` — the commit that introduced the
// verb — and reads that commit's author name.
//
// It is the second-best source and says so: the mapping file is exact, this is
// evidence. A verb git cannot place has no author, and every receipt for it
// then counts as a non-author's, which is the safe direction: the gate can be
// wrong by asking for one more pass, never by passing a verb nobody ran.
func AuthorsFromGit(ctx context.Context, repo string, verbs []Verb, run Runner, progress Progress) (Authors, error) {
	if strings.TrimSpace(repo) == "" {
		return nil, fmt.Errorf("authors: no --repo given; refusing to guess")
	}
	if run == nil {
		run = GitRunner
	}
	authors := Authors{}
	for i, v := range verbs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("authors: reading %s: %w", v.Key(), err)
		}
		word := strings.Fields(v.Verb)[0]
		out, err := run(ctx, repo,
			"log", "--reverse", "--format=%an", "-S", `"`+word+`"`, "--", "cmd/"+v.Tool)
		if err != nil {
			// A tool with no directory of its own, a shallow clone, a verb
			// whose word never appeared as a literal: no author, not an error.
			// The whole run failing over one unplaceable verb would make the
			// ledger unusable on exactly the repositories that need it most.
			if progress != nil {
				progress(i+1, len(verbs))
			}
			continue
		}
		if name := firstLine(out); name != "" {
			authors.Set(v.Key(), name)
		}
		if progress != nil {
			progress(i+1, len(verbs))
		}
	}
	return authors, nil
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// NewProgress returns a Progress that says what a slow read is doing and stays
// silent on a fast one: nothing at all until the read has been running longer
// than `after`, then one line at most every `every`, and one at the end.
//
// The clock is a parameter so the policy can be tested against a fake one.
// Nothing here waits: a progress reporter that made its own tests take a second
// each would be the slow suite this family refuses.
func NewProgress(now func() time.Time, after, every time.Duration, print func(done, total int)) Progress {
	if now == nil {
		now = time.Now
	}
	start := now()
	var last time.Time
	return func(done, total int) {
		at := now()
		if at.Sub(start) < after {
			return
		}
		if !last.IsZero() && at.Sub(last) < every && done != total {
			return
		}
		last = at
		print(done, total)
	}
}
