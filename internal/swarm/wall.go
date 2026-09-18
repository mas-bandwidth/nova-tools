package swarm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE WALL DEATH (issue #918). A harness that DENIES a tool call returns a tool error the
// model routes around and keeps working -- that is the configured fence (fence.go). But a
// harness build that still auto-rejects stops the model there, and the run ends with no
// RESULT.md while the card's commits sit in ./repo: eight of thirty cards died this way on
// 2026-09-16 and the batch scored them `no-result`, a model that chose to publish nothing,
// with the work stranded and unpushed.
//
// WallDeath is what such a run is: the harness's own fence named a path it would not touch,
// the job published nothing, and ./repo is left with commits. The report NAMES the path and
// the commits so the harvester pushes the work -- `WALL task=<id> path=<p>` and, when there
// are commits past the repo's base, `commits=<n> branch=<name>`.
//
// A FENCE REJECTION BESIDE A RESULT IS NOT A DEATH. A run that emitted the line and then
// published is a model that took the tool error and finished, and it is done. The result is
// asked first, wherever the gather looks for one (FindCardResult), so the two shapes never
// trade places.
func WallDeath(jobDir, task string) (string, bool) {
	if _, found := FindCardResult(jobDir); found {
		return "", false
	}
	raw, err := fenceCapture(jobDir)
	if err != nil {
		return "", false
	}
	path, rejected := FenceRejection(raw)
	if !rejected {
		return "", false
	}
	report := "WALL task=" + oneline.Field(task) + " path=" + oneline.Field(path)
	if branch, n, ok := repoCommits(jobDir); ok {
		report += fmt.Sprintf(" commits=%d branch=%s", n, oneline.Field(branch))
	}
	return report, true
}

// fenceCapture is the file the harness's own words land in: the native path's own capture
// (<job>/harness-output.log), or the supervisor's and a runner's <job>/harness.log. Both
// names are asked because the two execution paths write different ones, and the fence line
// is in whichever path ran.
func fenceCapture(jobDir string) ([]byte, error) {
	for _, name := range []string{"harness-output.log", "harness.log"} {
		if raw, err := readRegular(filepath.Join(jobDir, name)); err == nil {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("no harness capture under %s", jobDir)
}

// repoCommits is the work the card already did: the branch it is on and how many commits are
// past the repo's base, or ok=false when ./repo is not a repository, is on no branch, or has
// no base to count against. The base is the branch's upstream when it has one, else the
// remote's own default branch -- never a guess at a commit.
func repoCommits(jobDir string) (string, int, bool) {
	dir := filepath.Join(jobDir, "repo")
	if fi, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !fi.IsDir() {
		return "", 0, false
	}
	branch := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" || branch == "HEAD" {
		return "", 0, false
	}
	base := repoBase(dir)
	if base == "" {
		return "", 0, false
	}
	n, err := strconv.Atoi(gitOut(dir, "rev-list", "--count", base+"..HEAD"))
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return branch, n, true
}

// repoBase is the ref ./repo was cloned from: the branch's own upstream, and failing that
// the remote's default branch under the names a clone writes.
func repoBase(dir string) string {
	for _, ref := range []string{"@{upstream}", "origin/HEAD", "origin/main", "origin/master", "origin/dev"} {
		if gitOut(dir, "rev-parse", "--verify", "--quiet", ref) != "" {
			return ref
		}
	}
	return ""
}

// A WALL DEATH IS A NAMED END (issue #644 and its follow-up).
//
// Card 8311 committed its fix and then died on
//
//	permission requested: external_directory (/.../jobs/scratch/*); auto-rejecting
//
// with no RESULT.md, and the pool reported `no-result` -- the token for a MODEL that chose to
// publish nothing. The model never got the chance: the harness's own fence, or the OS wall,
// stopped it at a path, and the absence of a report is the machinery's doing. The fence half
// of this landed first (fence.go, the `fence` token); this names the END itself, with the
// refused path, the step the card reached, and the commits it left on its branch so the
// harvester can still push the work the dead card had already committed.
//
// THREE THINGS ARE READ OUT OF THE CARD'S OWN LOG, and nothing is guessed: the harness's
// permission auto-reject line (FenceRejection), the sandbox's own `SANDBOX REFUSED` line, and
// `Operation not permitted` ON A PATH -- a bare `kill: Operation not permitted` is not this
// class. The path and the last `STEP <n>` the card printed are what the one report line
// carries.

// The wall's own words, kept here beside the classifier that reads them.
const (
	// SandboxRefusedMark is the sandbox's own refusal prefix (cmd/nova-sandbox/main.go).
	SandboxRefusedMark = "SANDBOX REFUSED"
	// OperationNotPermittedMark is the kernel's refusal, which on a path outside the write
	// set is the wall talking (internal/sandbox/gpu.go, the Darwin probe).
	OperationNotPermittedMark = "Operation not permitted"
	// WallStepMark is how a card numbers its steps; the LAST one in the log is where the
	// wall stopped it.
	WallStepMark = "STEP "
)

// WallRefusal is one wall death: the path the wall refused and the last step the card
// reached. An empty Path or Step is the dash the report line writes.
type WallRefusal struct {
	Path string
	Step string
}

// WallRefused reports the first path the harness's own fence or the OS wall refused in one
// log, and whether either refused anything. The first is the one that matters: the model
// stops at it, and every later line is a consequence of the same closed path.
func WallRefused(log []byte) (WallRefusal, bool) {
	if p, ok := FenceRejection(log); ok {
		return WallRefusal{Path: p, Step: WallStep(log)}, true
	}
	for _, raw := range strings.Split(string(log), "\n") {
		line := strings.TrimSpace(stripPaint(raw))
		if line == "" {
			continue
		}
		switch {
		case strings.Contains(line, SandboxRefusedMark):
			// The sandbox's refusal names the path it would not open in its own text; a
			// refusal that names none is still this class, and its path is the dash.
			return WallRefusal{Path: wallPathToken(line), Step: WallStep(log)}, true
		case strings.Contains(line, OperationNotPermittedMark):
			// ON A PATH. A bare `Operation not permitted` is a permission failure about
			// something that is not a path -- a signal, a socket -- and is not the wall
			// refusing a read or a write outside the write set.
			if p := wallPathToken(line); p != "" {
				return WallRefusal{Path: p, Step: WallStep(log)}, true
			}
		}
	}
	return WallRefusal{}, false
}

// wallRefusedInLog reads one log file and reports the wall refusal in it, or false when the
// file cannot be read or holds no refusal.
func wallRefusedInLog(path string) (WallRefusal, bool) {
	raw, err := readRegular(path)
	if err != nil {
		return WallRefusal{}, false
	}
	return WallRefused(raw)
}

// WallStep returns the number on the LAST `STEP <n>` line the card printed, or "" when it
// printed none. A card that died at its second step says so, so the remedy can name where.
func WallStep(log []byte) string {
	step := ""
	for _, raw := range strings.Split(string(log), "\n") {
		line := strings.TrimSpace(stripPaint(raw))
		rest, ok := strings.CutPrefix(line, WallStepMark)
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		n := strings.TrimRight(fields[0], ".:)(")
		if n != "" && isAllDigits(n) {
			step = n
		}
	}
	return step
}

// isAllDigits reports whether every byte is a decimal digit and there is at least one.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// wallPathToken is the path a refusal line names: the first quoted string holding a `/`, else
// the first absolute token. A line with no path at all returns "".
func wallPathToken(line string) string {
	for _, q := range []string{"'", `"`} {
		rest := line
		for {
			i := strings.Index(rest, q)
			if i < 0 {
				break
			}
			j := strings.Index(rest[i+1:], q)
			if j < 0 {
				break
			}
			cand := rest[i+1 : i+1+j]
			if strings.Contains(cand, "/") {
				return cand
			}
			rest = rest[i+1+j+1:]
		}
	}
	for _, f := range strings.Fields(line) {
		t := strings.Trim(f, "'\"`.,;:()[]{}<>")
		if strings.HasPrefix(t, "/") {
			return t
		}
	}
	return ""
}

// wallTail is the bounded field the batch's ABSTAIN line carries after log=<n>: the path the
// wall refused and the step the card reached. The full report line is WallLine, on the notes.
func wallTail(w WallRefusal) string {
	return "path=" + dashOr(w.Path) + " step=" + dashOr(w.Step)
}

// WallLine is the ONE line a wall death is reported on, to the coordinator and to the
// harvester. It names the task, the refused path and the last step the card reached, and --
// when the clone holds commits past its base -- the count and the branch, so work a dead card
// had already committed is not lost with it:
//
//	WALL task=<id> path=<the refused path> step=<the last STEP number seen in the log> [commits=<n> branch=<name>]
func WallLine(task string, w WallRefusal, branch string, commits int) string {
	line := fmt.Sprintf("WALL task=%s path=%s step=%s",
		oneline.Field(task), oneline.Field(dashOr(w.Path)), oneline.Field(dashOr(w.Step)))
	if commits > 0 && strings.TrimSpace(branch) != "" {
		line += fmt.Sprintf(" commits=%d branch=%s", commits, oneline.Field(branch))
	}
	return line
}

// WallCommits reports the branch a clone is on and the commits it holds past its base -- the
// commits reachable from HEAD and not from the base it cloned, which is exactly the work a
// card added before the wall stopped it. The base is the first remote-tracking ref named
// `dev`, `main` or `master`, else the first `refs/remotes/*`; a clone with no remote refs
// counts every commit it has, because there is no base to subtract. It reports false when the
// directory is not a clone or git cannot name the branch, so a report line is only decorated
// with numbers that are real.
func WallCommits(repoDir string) (branch string, commits int, ok bool) {
	if strings.TrimSpace(repoDir) == "" {
		return "", 0, false
	}
	branch = gitOut(repoDir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" || branch == "HEAD" {
		return "", 0, false
	}
	base := wallBaseRef(repoDir)
	count := "HEAD"
	if base != "" {
		count = base + "..HEAD"
	}
	n, err := strconv.Atoi(gitOut(repoDir, "rev-list", "--count", count))
	if err != nil || n < 0 {
		return "", 0, false
	}
	return branch, n, true
}

// wallBaseRef is the remote-tracking ref a clone's work sits on top of: `dev`, `main` or
// `master` if one is present, else the first remote-tracking ref, else "" when there is none.
func wallBaseRef(repoDir string) string {
	refs := strings.Fields(gitOut(repoDir, "for-each-ref", "--format=%(refname)", "refs/remotes"))
	fallback := ""
	for _, r := range refs {
		if strings.HasPrefix(r, "refs/remotes/") {
			fallback = r
		}
		switch r {
		case "refs/remotes/origin/dev", "refs/remotes/origin/main", "refs/remotes/origin/master":
			return r
		}
	}
	return fallback
}

// gitOut runs one local git command in dir and returns its trimmed stdout; an error is the
// empty string, because every caller here treats a missing answer as "no answer" and never
// as zero.
func gitOut(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// THE GATE THAT NEVER RAN (issue #1465).
//
// A `native` Go card is handed GOMODCACHE, GOCACHE and GOTOOLCHAIN=local, and a writable
// cache directory beside its job, all for a toolchain that lives under a USER directory --
// `~/go/bin/go`, itself a symlink into an SDK tree -- which is under no root the wall
// admits. The card wrote its test, tried to compile it, and got one line back:
//
//	/usr/bin/bash: line 1: /home/glenn/go/bin/go: Permission denied
//
// There is no `SANDBOX REFUSED` in that line, no `Operation not permitted`, and no fence
// rejection, so WallRefused above saw nothing. The card published an honest RESULT.md
// saying the gate could not be built or run, the child exited 0, and the run reported
// `NATIVE OK ... rc=0 sandbox=landlock harness=ok`. A commit nobody had compiled read as
// green, and the only thing that said otherwise was prose inside the card's own report.
//
// THE CLASS, NOT THE INSTANCE. The remedy for one bench is to name the toolchain in
// `read_roots`; the remedy for the CLASS is that a gate which could not execute its command
// cannot return OK. This is the reader that makes it impossible: the words a SHELL uses when
// the wall denies it a path it was told to RUN.
//
// IT FIRES WITH A RESULT BESIDE IT, and that is the whole difference from WallDeath and
// WallRefused. Those two ask for the result first, because a card that published despite a
// refusal is a card that routed around it and finished. Here the published result IS the
// lie: it is a report about work that was never compiled.
//
// IT IS DELIBERATELY NARROW. `cat: /etc/shadow: Permission denied` is a READ a card was
// refused and worked around, and a card's own prose about permissions is prose. The line's
// own first word must be a SHELL -- by name or by the path it was launched from -- or the
// words must be Go's own `fork/exec`, and the path it names must be absolute.

// execShells are the shells whose refusal this reads, by the base name of whatever ran them.
// A line whose first field is anything else is some other program's complaint about a path.
var execShells = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true, "ash": true,
	"csh": true, "tcsh": true, "fish": true,
}

// permissionDeniedMark is the refusal itself, matched case-insensitively: bash capitalises
// it, Go's os/exec does not.
const permissionDeniedMark = "permission denied"

// ExecRefused reports the FIRST path the wall refused to a card's own shell in one capture,
// and whether it refused any. The first is the one that matters: every later line is a
// consequence of the same closed root. The step the card had reached rides with it, exactly
// as it does for a wall death, so the refusal can say where the gate died.
func ExecRefused(log []byte) (WallRefusal, bool) {
	for _, raw := range strings.Split(string(log), "\n") {
		line := strings.TrimSpace(stripPaint(raw))
		if line == "" {
			continue
		}
		if p, ok := execRefusedPath(line); ok {
			return WallRefusal{Path: p, Step: WallStep(log)}, true
		}
	}
	return WallRefusal{}, false
}

// execRefusedPath is the grammar, and nothing outside it is this class. Four spellings are
// read, each one measured off a real log:
//
//	/usr/bin/bash: line 1: /home/glenn/go/bin/go: Permission denied   bash, by path
//	bash: /home/glenn/go/bin/go: Permission denied                    bash, by name
//	sh: 1: /opt/sdk/go1.26.5/bin/go: Permission denied                dash, which numbers
//	zsh: permission denied: /opt/sdk/go1.26.5/bin/go                  zsh, path last
//	fork/exec /opt/sdk/go1.26.5/bin/go: permission denied             Go's own os/exec
func execRefusedPath(line string) (string, bool) {
	if !strings.Contains(strings.ToLower(line), permissionDeniedMark) {
		return "", false
	}
	parts := strings.Split(line, ": ")
	if len(parts) < 2 {
		return "", false
	}
	// Go's own os/exec, which a harness that launched the toolchain itself prints. The
	// words are a whole segment, never a substring of a longer one.
	for _, p := range parts {
		rest, ok := strings.CutPrefix(strings.TrimSpace(p), "fork/exec ")
		if !ok {
			continue
		}
		if cand := absToken(rest); cand != "" {
			return cand, true
		}
	}
	if !isShellWord(parts[0]) {
		return "", false
	}
	// zsh puts its refusal before the path: `zsh: permission denied: <path>`.
	if len(parts) >= 3 && strings.EqualFold(strings.TrimSpace(parts[1]), permissionDeniedMark) {
		if cand := absToken(parts[2]); cand != "" {
			return cand, true
		}
	}
	// Every other shell puts the program in the segment before the refusal, with an optional
	// `line <n>` or bare `<n>` segment in between which this never has to read.
	if !strings.EqualFold(strings.TrimSpace(parts[len(parts)-1]), permissionDeniedMark) {
		return "", false
	}
	cand := absToken(parts[len(parts)-2])
	return cand, cand != ""
}

// absToken is one segment read as a path: an absolute path and nothing else. A segment
// holding a space, a quote or a parenthesis is a sentence about a path, not the path.
func absToken(s string) string {
	t := strings.TrimSpace(s)
	t = strings.Trim(t, `"'`)
	if !strings.HasPrefix(t, "/") || strings.ContainsAny(t, " \t'\"()") {
		return ""
	}
	return t
}

// isShellWord says whether a line's first field is a shell, by the base name of whatever
// path ran it: `/usr/bin/bash` and `bash` are the same shell talking.
func isShellWord(field string) bool {
	base := filepath.Base(strings.TrimSpace(field))
	base = strings.TrimSuffix(base, ".exe")
	return execShells[base]
}

// ExecRefusalRoots is the remedy, worked out rather than guessed at: the read roots that
// would have let this program run. A toolchain reached through a symlink needs TWO of them
// -- the directory holding the launcher, and the tree the launcher resolves into -- because
// the kernel checks the grant against the RESOLVED target, and a coordinator who names one
// and not the other loses the card again. An SDK's launcher sits in `<root>/bin`, so the
// root is that directory's parent; anything else is named by its own directory.
//
// Nothing here is specific to Go, to a bench or to a user: every path is read off the
// refused program itself.
func ExecRefusalRoots(refused string) []string {
	if !filepath.IsAbs(refused) {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(dir string) {
		if dir == "" || dir == "/" || seen[dir] {
			return
		}
		seen[dir] = true
		out = append(out, dir)
	}
	add(filepath.Dir(refused))
	resolved, err := filepath.EvalSymlinks(refused)
	if err != nil || resolved == refused {
		return out
	}
	dir := filepath.Dir(resolved)
	if filepath.Base(dir) == "bin" {
		add(filepath.Dir(dir))
		return out
	}
	add(dir)
	return out
}

// ExecRefusalReason is the ONE line a gate that never ran owes its caller, and the one place
// its words live. It names what was refused, where the card had got to, where the spend it
// already made is sitting, and the roots to open so the next run works -- `nova-swarm help`
// says of every listing that it carries one more line naming the remedy, and a refusal a
// coordinator cannot act on costs the same card twice.
func ExecRefusalReason(label, jobDir string, w WallRefusal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s the wall refused the card's own shell the program %s at step=%s, so the gate never executed its command and this run is not OK: the report under %s is about work that nothing compiled",
		oneline.Field(label), oneline.Field(dashOr(w.Path)), oneline.Field(dashOr(w.Step)), oneline.Field(jobDir))
	roots := ExecRefusalRoots(w.Path)
	if len(roots) == 0 {
		b.WriteString(". Remedy: name the directory holding that program in the worker description's read_roots -- the wall names every path and grants nothing it was not handed -- or run with --no-wall and own every read the child makes")
		return b.String()
	}
	b.WriteString(". Remedy: name ")
	for i, r := range roots {
		if i > 0 {
			b.WriteString(" and ")
		}
		b.WriteString(oneline.Field(r))
	}
	b.WriteString(" in the worker description's read_roots")
	if len(roots) > 1 {
		b.WriteString(" -- both, because the kernel checks the grant against the RESOLVED target and that program is a symlink into another tree")
	}
	b.WriteString(", or run with --no-wall and own every read the child makes")
	return b.String()
}
