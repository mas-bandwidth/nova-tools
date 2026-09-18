package dogfood

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// HelpRunner runs one binary's `help` and returns what it printed. It is a
// parameter so the tests can answer without binaries of their own, and it is
// the only place this file leaves the process. Nothing here reaches the
// network.
type HelpRunner func(ctx context.Context, bin string) (string, error)

// RunHelp is the real one: `<bin> help`, under the context's deadline, stdout
// and stderr together, because a tool that prints its usage to stderr is still
// telling you its verbs and the parser ignores every line that is not a
// command anyway.
func RunHelp(ctx context.Context, bin string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, "help")
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return "", err
	}
	return string(out), nil
}

// VerbsFromTools asks the binaries themselves what verbs they have.
//
// docs/CLI.md is a document somebody keeps up to date; a binary's `help` is
// what the binary does. The 2026-09-18 dogfood pass ran `nova-work ask` and
// `nova-work asks` — two verbs the reference's pasted help block does not
// carry — and both receipts were stranded against a reference that had gone
// stale. So when `--tools` names a directory of built binaries, they are the
// authoritative list and the reference is the fallback for tools that are not
// in it.
//
// Only files named `nova-*` that are regular and executable are run, each under
// the caller's deadline. A binary that cannot answer is one named failure, not
// a dead run: a half-built directory should cost the tools it holds, not the
// whole ledger.
func VerbsFromTools(ctx context.Context, dir string, run HelpRunner, progress Progress) ([]Verb, []Failure, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil, fmt.Errorf("tools: no directory given; refusing to guess")
	}
	if run == nil {
		run = RunHelp
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("tools: %w", err)
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "nova-") || !isToolName(name) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names) // two runs read the same

	var (
		verbs    []Verb
		failures []Failure
		seen     = map[string]bool{}
	)
	for i, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("tools: reading %s: %w", name, err)
		}
		bin := filepath.Join(dir, name)
		out, err := run(ctx, bin)
		if err != nil {
			failures = append(failures, Failure{
				Subject: bin,
				Reason:  fmt.Sprintf("its own help could not be read: %v", err),
			})
		} else {
			for _, v := range ParseHelp(out) {
				// A binary speaks for itself and for nobody else: its help may
				// print another tool's line as an example, and that line is not
				// this binary's verb list.
				if v.Tool != name {
					continue
				}
				if seen[v.Key()] {
					continue
				}
				seen[v.Key()] = true
				verbs = append(verbs, v)
			}
		}
		if progress != nil {
			progress(i+1, len(names))
		}
	}
	return verbs, failures, nil
}

// MergeVerbs puts the authoritative list first and lets the fallback fill in
// only the tools the first one says nothing about. A tool that answered for
// itself is complete by definition — a verb of its in the reference and not in
// its help is a stale reference, not a missing verb.
func MergeVerbs(authoritative, fallback []Verb) []Verb {
	answered := map[string]bool{}
	for _, v := range authoritative {
		answered[v.Tool] = true
	}
	out := append([]Verb(nil), authoritative...)
	seen := map[string]bool{}
	for _, v := range out {
		seen[v.Key()] = true
	}
	for _, v := range fallback {
		if answered[v.Tool] || seen[v.Key()] {
			continue
		}
		seen[v.Key()] = true
		out = append(out, v)
	}
	return out
}
