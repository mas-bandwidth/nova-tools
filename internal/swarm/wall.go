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

// A DENIAL IN THE CAPTURE IS NEVER AN OK, AND IT IS NEVER A DIAGNOSIS EITHER
// (issue #1465, and Stella's HOLD on PR #1478, comment 5737662335).
//
// THE RUN THIS CLOSES. A `native` Go card was handed GOMODCACHE, GOCACHE and
// GOTOOLCHAIN=local for a toolchain under a user directory that the wall admitted no root
// for. It wrote its test, could not compile it, said so in its own RESULT.md, and the tool
// reported `NATIVE OK ... rc=0 sandbox=landlock harness=ok`. A commit nobody had compiled
// read as green, because the only record of the failure was one line in a log:
//
//	/usr/bin/bash: line 1: /home/glenn/go/bin/go: Permission denied
//
// WHAT THAT LINE PROVES, AND WHAT IT DOES NOT. The first version of this reader called the
// line an EXEC refusal, named the path a program, concluded that the gate never ran and that
// nothing had been compiled, and prescribed `read_roots`. Stella measured that none of it
// follows. An owned bash running `: > "$1"; printf "RECOVERED\n"` against a non-writable
// directory prints
//
//	/bin/bash: <absolute output path>: Permission denied
//
// -- the identical shape -- attempts no program at all, and exits 0 having RECOVERED. A `cd`
// into an unreadable directory is a third shape with the same words. The shell names a PATH
// and a refusal; it does not name the OPERATION. And this reader runs on `--no-wall` runs
// too, where there is no wall to attribute anything to.
//
// SO THE VERDICT IS A REFUSAL AND THE CAUSE IS UNVERIFIED. What is established is enough on
// its own: the card's shell was denied something, nobody read it, and a disposition of OK
// over an unread denial is what cost the card of #1465. The run is refused. The line is
// quoted verbatim, the operation is labelled `unverified`, and the remedy is to re-run the
// gate and read its stderr -- not a cause invented to fill the field. On a WALLED run the
// read set is offered as ONE CANDIDATE, said to be a candidate.
//
// WHAT THIS STILL CANNOT DO. It cannot bind the verdict to the card's own declared gate,
// because `native` is handed a card as free text and no machine-readable declaration of what
// the gate is or what it returned. Until a card declares its gate in a form the tool can
// read, the honest signal is this one: a denial was seen, its operation is unknown, and the
// disposition is refused rather than OK.

// execShells are the shells whose denial this reads, by the base name of whatever ran them.
// A line whose first field is anything else is some other program's complaint about a path.
var execShells = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true, "ash": true,
	"csh": true, "tcsh": true, "fish": true,
}

// SandboxNoneByFlag is the name the OK line carries for a run the caller unwalled with
// --no-wall: a run that had no sandbox, told apart here from one that had a real one, so no
// refusal attributes a denial to a wall that was not there.
const SandboxNoneByFlag = "none-by-flag"

// permissionDeniedMark is the refusal itself, matched case-insensitively: bash capitalises
// it, Go's os/exec does not.
const permissionDeniedMark = "permission denied"

// ShellDenial is one denial a card's own shell reported: the path it named, the step the card
// had reached, and the line itself, which is the only thing a person can act on. There is no
// field for the operation, because the text does not carry one.
type ShellDenial struct {
	Path string
	Step string
	Line string
}

// ShellDenied reports the FIRST denial a card's shell printed in one capture, and whether it
// printed any. The first is the one that matters: every later line is downstream of the same
// closed path.
func ShellDenied(log []byte) (ShellDenial, bool) {
	for _, raw := range strings.Split(string(log), "\n") {
		line := strings.TrimSpace(stripPaint(raw))
		if line == "" {
			continue
		}
		if p, ok := deniedPath(line); ok {
			return ShellDenial{Path: p, Step: WallStep(log), Line: line}, true
		}
	}
	return ShellDenial{}, false
}

// deniedPath is the grammar, and nothing outside it is this class. Five spellings are read,
// each measured off a real log:
//
//	/usr/bin/bash: line 1: /home/glenn/go/bin/go: Permission denied   bash, by path
//	bash: /home/glenn/go/bin/go: Permission denied                    bash, by name
//	sh: 1: /opt/sdk/go1.26.5/bin/go: Permission denied                dash, which numbers
//	zsh: permission denied: /opt/sdk/go1.26.5/bin/go                  zsh, path last
//	fork/exec /opt/sdk/go1.26.5/bin/go: permission denied             Go's own os/exec
//
// THE PATH IS THE WHOLE SEGMENT BETWEEN THE DELIMITERS (Stella's P1). A shell delimits its
// fields with `: `, and everything between two delimiters is the path -- spaces, parentheses
// and all, because those are ordinary pathname characters. The first version required a
// token with no spaces in it, and `/opt/sdk tool/bin/go` therefore recreated the whole silent
// green of #1465 through an ordinary absolute path. A whitespace-bearing path is not prose.
//
// WHAT KEEPS IT FROM EATING PROSE is the shell at the head of the line, not the shape of the
// path: `cat: /etc/shadow: Permission denied` is some other program's complaint, and a
// sentence a model wrote is not a shell speaking at all.
func deniedPath(line string) (string, bool) {
	if !strings.Contains(strings.ToLower(line), permissionDeniedMark) {
		return "", false
	}
	parts := strings.Split(line, ": ")
	if len(parts) < 2 {
		return "", false
	}
	// Go's own os/exec, which a harness that launched a program itself prints. The words are
	// a whole segment, never a substring of a longer one.
	for _, p := range parts {
		rest, ok := strings.CutPrefix(strings.TrimSpace(p), "fork/exec ")
		if !ok {
			continue
		}
		if cand := absSegment(rest); cand != "" {
			return cand, true
		}
	}
	if !isShellWord(parts[0]) {
		return "", false
	}
	// zsh puts its refusal before the path: `zsh: permission denied: <path>`.
	if len(parts) >= 3 && strings.EqualFold(strings.TrimSpace(parts[1]), permissionDeniedMark) {
		if cand := absSegment(strings.Join(parts[2:], ": ")); cand != "" {
			return cand, true
		}
	}
	// Every other shell puts the path in the segment before the refusal, with an optional
	// `line <n>` or bare `<n>` segment in between which this never has to read.
	if !strings.EqualFold(strings.TrimSpace(parts[len(parts)-1]), permissionDeniedMark) {
		return "", false
	}
	cand := absSegment(parts[len(parts)-2])
	return cand, cand != ""
}

// absSegment is one delimited segment read as a path: absolute, and taken whole. Surrounding
// quotes are stripped because a shell that quotes a path is naming the same path. A segment
// that does not begin with `/` is not one.
func absSegment(s string) string {
	t := strings.TrimSpace(s)
	t = strings.Trim(t, `"'`)
	t = strings.TrimSpace(t)
	if !strings.HasPrefix(t, "/") {
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

// DeniedPathRoots is the read set a coordinator would have to open IF the denied path was one
// the child had to read or execute -- a candidate, never a diagnosis. A path reached through
// a symlink needs TWO roots, because the kernel checks the grant against the resolved target
// and a coordinator who names one and not the other loses the card again. A launcher in a
// `bin` directory belongs to the tree above it; anything else is named by its own directory.
//
// Nothing here is specific to Go, to a bench or to a user: every path is read off the denied
// path itself.
func DeniedPathRoots(denied string) []string {
	if !filepath.IsAbs(denied) {
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
	add(filepath.Dir(denied))
	resolved, err := filepath.EvalSymlinks(denied)
	if err != nil || resolved == denied {
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

// ShellDenialReason is the ONE line an unread denial owes its caller, and the one place its
// words live. It separates what was measured from what was not, by construction:
//
//   - MEASURED: the step, the child's exit code, the wall this ran under, the path named, and
//     the shell's own line, quoted.
//   - NOT MEASURED: which operation was denied. The field says `operation=unverified` and the
//     sentence says why, so nobody reads a cause into it.
//   - THE REMEDY IS A MEASUREMENT, not a guess: re-run the card's own gate against this
//     commit and read its stderr. On a walled run the read set is offered beside it as one
//     candidate among the others.
//
// `wall` is the wall's own name, or empty for a run that had none; a run with no wall is told
// so and nothing is attributed to a sandbox that was not there.
func ShellDenialReason(label, jobDir, wall string, rc int, d ShellDenial) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s a denial the card's shell reported went unread, so this run's disposition is refused rather than OK: step=%s rc=%d wall=%s denied_path=%s operation=unverified job=%s line=%q",
		oneline.Field(label), oneline.Field(dashOr(d.Step)), rc, oneline.Field(dashOr(wall)),
		oneline.Field(dashOr(d.Path)), oneline.Field(jobDir), d.Line)
	b.WriteString(". The shell names a path and a refusal and NOT an operation: a denied exec, a redirection to a path the card may not write, and a cd into a directory it may not read all print these words, and a card can carry on from any of them, so what failed here is not established by this line")
	b.WriteString(". Remedy: re-run the card's own gate against the commit under ")
	b.WriteString(oneline.Field(jobDir))
	b.WriteString(" and read its stderr -- that is the measurement this refusal is standing in for")
	if wall == "" || wall == SandboxNoneByFlag {
		b.WriteString("; this run had no sandbox, so nothing here is attributable to one")
		return b.String()
	}
	roots := DeniedPathRoots(d.Path)
	if len(roots) == 0 {
		return b.String()
	}
	b.WriteString(". One candidate among the others, if that path was one the child had to read or execute: it is under no root this wall was handed, and ")
	for i, r := range roots {
		if i > 0 {
			b.WriteString(" and ")
		}
		b.WriteString(`"` + r + `"`)
	}
	b.WriteString(" would be the read_roots entries for it")
	if len(roots) > 1 {
		b.WriteString(" -- both, because the kernel checks a grant against the RESOLVED target and that path is a symlink into another tree")
	}
	b.WriteString(". That is a candidate and not the diagnosis")
	return b.String()
}
