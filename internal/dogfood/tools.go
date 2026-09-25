package dogfood

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// exeSuffix is the extension a built binary carries on Windows. It IS the
// executable bit there: Windows has no mode bits to speak of — every file in a
// directory reads back as 0666 — and what makes a file runnable is its
// extension. A discovery written for one platform's answer finds nothing on the
// other, which is exactly what happened: the Windows legs read a directory of
// real binaries as empty, because `nova-check.exe` is neither spelled
// `nova-check` nor executable by a bit that does not exist there.
const exeSuffix = ".exe"

// toolName maps one directory entry's name to the tool it is, accepting both
// spellings a built binary has: `nova-check` and `nova-check.exe`.
func toolName(name string) (string, bool) {
	if trimmed, ok := strings.CutSuffix(name, exeSuffix); ok {
		name = trimmed
	}
	if !isToolName(name) {
		return "", false
	}
	return name, true
}

// runnable decides "executable" the way this platform decides it.
func runnable(name string, info os.FileInfo) bool {
	return runnableOn(runtime.GOOS, name, info.Mode())
}

// runnableOn is runnable with the platform named rather than assumed, so both
// answers are tested on one bench. The bug this exists to stop is one nobody
// can see from the platform they are on: the discovery was written for mode
// bits, every bench that ran it had them, and the Windows legs read a directory
// of real binaries as empty until CI said so.
func runnableOn(goos, name string, mode os.FileMode) bool {
	if !mode.IsRegular() {
		return false
	}
	if goos == "windows" {
		return strings.HasSuffix(strings.ToLower(name), exeSuffix)
	}
	return mode.Perm()&0o111 != 0
}

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
	// The file's name and the tool's name are two things: on Windows the file is
	// `nova-check.exe` and the tool it speaks for is `nova-check`.
	type binary struct{ file, tool string }
	var bins []binary
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		tool, ok := toolName(name)
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil || !runnable(name, info) {
			continue
		}
		bins = append(bins, binary{file: name, tool: tool})
	}
	// two runs read the same
	sort.Slice(bins, func(i, j int) bool { return bins[i].file < bins[j].file })

	var (
		verbs    []Verb
		failures []Failure
		seen     = map[string]bool{}
	)
	for i, b := range bins {
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("tools: reading %s: %w", b.file, err)
		}
		bin := filepath.Join(dir, b.file)
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
				// this binary's verb list. The comparison is against the TOOL's
				// name, not the file's: `nova-check.exe` speaks for nova-check.
				if v.Tool != b.tool {
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
			progress(i+1, len(bins))
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
