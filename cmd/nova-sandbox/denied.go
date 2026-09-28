// What the wall denied, said out loud.
//
// The failure this file is about was measured on 2026-09-18, dogfooding `nova-sandbox run`
// on a real card step: a `go build` inside the wall died and the ONLY symptom was Go's own
// sentence — `go: cannot find GOROOT directory: 'go' binary is trimmed and GOROOT is not
// set`. Nothing in that sentence says "sandbox", and nothing the tool printed said which
// path had been refused. A wall that denies silently costs the reader the whole
// investigation.
//
// The root cause of that particular one is fixed where it belonged, in the optional roots'
// ancestors (internal/sandbox/policy.go). This file is the class: when a contained command
// exits non-zero, ASK THE OPERATING SYSTEM what it refused and print one line per path,
// with the flag that would have allowed it.
//
// THE HONEST LIMIT, measured on this Studio (macOS 26, arm64, 2026-09-18) and stated here
// because a reader will otherwise think this is broken:
//
//   - macOS does report seatbelt violations to the unified log, under the subsystem
//     `com.apple.sandbox.reporting`, category `violation`, and the parser below reads that
//     exact shape.
//   - It does NOT report them for a profile applied with `sandbox-exec -p`. A denial
//     produced by this tool is absent from `log show` at every level, `--info` and
//     `--debug` included, while other processes' violations are in the same window.
//   - The two ways to ask for them do not exist on this OS either: `(deny default (with
//     report))` is refused by the compiler — "report modifier does not apply to deny
//     action" — and `(trace "<file>")` aborts sandbox-exec with SIGABRT, exit 134.
//
// So on this macOS these lines are usually silent, and the NOTE that goes with a non-zero
// exit is what speaks instead. The reader is built and tested anyway, because it costs one
// process on a failing run, it is right where the OS does report, and the alternative —
// leaving the tool with no way at all to say what it denied — is the thing being fixed.
package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// EVERY PATH IN THIS FILE IS A POSIX PATH, and that is a contract rather than an accident.
// These lines come out of macOS's unified log, so their separator is `/` whatever machine
// reads them — and this file is compiled on all three platforms, so the suite reads the
// same fixture on windows, where `path/filepath` means `\`.
//
// Measured on the windows CI leg (run 35367602664, job test-windows-pr):
// `filepath.Dir("/Users/me/notes/out.txt")` answered `\Users\me\notes` and the remedy line
// printed `--write \\Users\\me\\notes`; and `sandbox.Inside`, which joins with
// `os.PathSeparator`, called a denial at `/Volumes/nova-j1/work/inside.txt` OUTSIDE the
// write set `/Volumes/nova-j1` — so a run was told to go and fix a path it had already
// named. Both are `path` now, and the containment test is this file's own rather than
// `sandbox.Inside`: that function is about the paths of the machine it runs on, and these
// are not those.
//
// denialStat is the one question here that IS about the local machine — whether a denied
// path is a directory — and it is a seam so the tests answer it the same way everywhere.
// Without it they were asserting that the machine reading them has an `/opt`.
var denialStat = os.Stat

// maxDenied is how many SANDBOX DENIED lines a run prints before one line stands for the
// rest — rule 16's shape for a list. A command that died on its first syscall can trip
// hundreds, and a wall of them is not a remedy.
const maxDenied = 10

// deniedPath is one thing the wall refused: the path, the operation in the two words a
// flag answers, and the process that asked.
type deniedPath struct {
	Path string
	Op   string // read or write; nothing else has a --read or a --write that answers it
	PID  int
}

// denialReader is the seam this file reaches the operating system through. sinceSeconds is
// how far back to look and pidFloor is this run's leader, so a shared machine's other
// violations are not handed to this card.
type denialReader func(sinceSeconds int, pidFloor int) []deniedPath

var runDenials denialReader = readOSDenials

// parseDenials reads the OS's own violation lines. Two shapes appear in the log and both
// are handled: the plain one, and the deduplicated `N duplicate reports for Sandbox: ...`.
//
//	Sandbox: go(4210) deny(1) file-read-metadata /opt
//
// Only file operations are kept. A `mach-lookup` or `network-outbound` denial is real but
// no `--read` answers it, and a line whose remedy is empty is a line that wastes a reader.
func parseDenials(logText string, pidFloor int) []deniedPath {
	var out []deniedPath
	seen := map[string]bool{}
	for _, line := range strings.Split(logText, "\n") {
		_, after, ok := strings.Cut(line, "Sandbox: ")
		if !ok {
			continue
		}
		name, rest, ok := strings.Cut(after, " deny(")
		if !ok {
			continue
		}
		pid := pidIn(name)
		if pid < pidFloor {
			continue
		}
		// deny(N) — step over the count to the operation.
		_, rest, ok = strings.Cut(rest, ") ")
		if !ok {
			continue
		}
		op, path, ok := strings.Cut(rest, " ")
		if !ok {
			continue
		}
		path = strings.TrimSpace(path)
		if !strings.HasPrefix(path, "/") {
			continue // a mach service name, a sysctl, a host: no path and no flag for it
		}
		var kind string
		switch {
		case strings.HasPrefix(op, "file-read"):
			kind = "read"
		case strings.HasPrefix(op, "file-write"):
			kind = "write"
		default:
			continue
		}
		key := kind + " " + path
		if seen[key] {
			continue // the same path refused twice is one thing to fix
		}
		seen[key] = true
		out = append(out, deniedPath{Path: path, Op: kind, PID: pid})
	}
	return out
}

// pidIn reads the pid out of the `name(pid)` the violation line carries. A line whose pid
// cannot be read answers 0, which no floor above zero keeps.
func pidIn(name string) int {
	open := strings.LastIndex(name, "(")
	if open < 0 || !strings.HasSuffix(name, ")") {
		return 0
	}
	n, err := strconv.Atoi(name[open+1 : len(name)-1])
	if err != nil {
		return 0
	}
	return n
}

// outsideTheWall drops the denials on paths the caller ALREADY named. A denial inside the
// allowed set is some other operation on a path that is granted, and a remedy naming a
// flag that is already in the argv sends a reader to fix what is not broken.
func outsideTheWall(denied []deniedPath, allowed []string) []deniedPath {
	var out []deniedPath
	for _, d := range denied {
		inside := false
		for _, dir := range allowed {
			if insidePosix(d.Path, dir) {
				inside = true
				break
			}
		}
		if !inside {
			out = append(out, d)
		}
	}
	return out
}

// printDenied is the line, and the line is the contract:
//
//	SANDBOX DENIED path=<p> op=<read|write> remedy="--read <dir>"
//
// The remedy names a DIRECTORY, because that is what the flags take: the path itself when
// it is one, and its parent when it is a file.
func printDenied(stderr io.Writer, denied []deniedPath, max int) {
	if len(denied) == 0 {
		return
	}
	sort.SliceStable(denied, func(i, j int) bool {
		if denied[i].Path != denied[j].Path {
			return denied[i].Path < denied[j].Path
		}
		return denied[i].Op < denied[j].Op
	})
	shown := denied
	if max > 0 && len(shown) > max {
		shown = shown[:max]
	}
	for _, d := range shown {
		flag := "--read"
		if d.Op == "write" {
			flag = "--write"
		}
		fmt.Fprintf(stderr, "SANDBOX DENIED path=%s op=%s remedy=%s\n",
			oneline.Field(d.Path), oneline.Field(d.Op),
			oneline.Quote(flag+" "+remedyDir(d.Path)))
	}
	if rest := len(denied) - len(shown); rest > 0 {
		fmt.Fprintf(stderr, "SANDBOX NOTE and %d more denied paths; the lines above are the first %d, and one --read of a shared parent usually answers several\n", rest, len(shown))
	}
}

// insidePosix is Inside for the slash paths of a seatbelt log: the same path, or a path
// under it, with `/` as the separator on every platform. It is a prefix test and nothing
// more — there is no disk here to resolve a symlink against, because the denial was
// recorded on a machine this reader may not be.
func insidePosix(p, dir string) bool {
	p, dir = path.Clean(p), path.Clean(dir)
	if p == dir {
		return true
	}
	return strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

// remedyDir is the directory a flag would name for this path: the path when it is a
// directory, its parent when it is a file, and its parent when it is neither — a path that
// was denied may not exist, and the parent is the flag a caller can actually pass.
func remedyDir(p string) string {
	if fi, err := denialStat(p); err == nil && fi.IsDir() {
		return p
	}
	dir := path.Dir(p)
	if dir == "" || dir == "." {
		return p
	}
	return dir
}
