// Package sandbox is the wall of docs/SPEC-SANDBOX.md: one command run with its
// filesystem reach cut down by the operating system. This file is the
// platform-independent half — the Policy, the path resolution and refusal of rule 5,
// the per-platform root tables as data, and the temp directory of rule 8. The three
// bodies that apply a policy live behind build tags beside it, and on a platform whose
// backend is not built the body REFUSES (rule 1): there is no fallback, no degraded
// mode, and no partial wall.
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Exit codes. The exec path uses the env(1)/timeout(1) convention rather than SPEC.md's
// 0/1/2, because its status belongs to the wrapped command; the departure and its reason
// are in the spec's exit-codes section.
const (
	ExitRefused     = 125 // nova-sandbox itself said NO before the command ran
	ExitNotExecuted = 126 // the command could not be executed and the tool was still there
	ExitNotFound    = 127 // the command could not be resolved on the caller's PATH
	ExitProbeFailed = 1   // probe/check grammar: the verb ran and said NO
	ExitCannotRun   = 2   // probe/check grammar: the verb could not run
	tmpDirName      = ".nova-sandbox-tmp"
	// profileFilePrefx is the name NO file carries: the darwin body passes the profile
	// inline with -p, and wrap_darwin_test.go asserts that nothing with this prefix is
	// ever written. The companion profileFilePerm went with the file it was for.
	profileFilePrefx = ".nova-sandbox-"
)

// Refusal is one independent problem, named by the flag it is about. Rule 16: a refusal
// says what the input wants and every independent problem is reported at once, so the
// callers below collect these rather than returning the first.
type Refusal struct {
	Reason string // the reason= token of the SANDBOX REFUSED line
	Text   string // what was wrong and what the flag wants, never a file's contents
}

func (r Refusal) Error() string { return r.Reason + ": " + r.Text }

// Code is the exit status this refusal costs. Every refusal of the tool's own is 125
// except the one about a command that is not on the PATH at all.
func (r Refusal) Code() int {
	if r.Reason == "not_found" {
		return ExitNotFound
	}
	return ExitRefused
}

func refuse(reason, format string, a ...any) Refusal {
	return Refusal{Reason: reason, Text: fmt.Sprintf(format, a...)}
}

// Input is the argv as the caller typed it, before any resolution. Everything here is a
// claim about this job; Build turns it into a Policy or into refusals.
type Input struct {
	Reads     []string
	Writes    []string
	Cwd       string // empty: the first --write (rule 13)
	Tmp       string // empty: <first --write>/.nova-sandbox-tmp (rule 8)
	Name      string // windows container name; accepted and ignored elsewhere
	NetDeny   bool
	NetListen bool
	Argv      []string // the command and its arguments, everything after --
	Home      string   // the caller's HOME as the child will see it (rule 9)
	LookAt    string   // PATH to resolve the command on; empty means the process's own
}

// Policy is one run's wall: resolved, absolute, existing paths and nothing guessed. The
// two named exceptions to "never guessed" are rule 4's, and both are recorded here as
// the caller's own first --write.
type Policy struct {
	Reads     []string // resolved, read-only, recursive
	Writes    []string // resolved, read+write, recursive; the first is load-bearing
	OptRoots  []string // the platform's optional roots that EXIST on this machine
	Cwd       string
	Tmp       string
	Home      string
	Name      string
	NetDeny   bool
	NetListen bool
	Command   string   // the resolved absolute path of the executable
	Argv      []string // Command followed by its arguments, verbatim

	// Extra is the file descriptors the child gets ABOVE stdin/stdout/stderr, in order,
	// starting at fd 3. It is never built from caller input: Build leaves it nil and the
	// only writer is the probe, which hands its child one end of a pipe carrying the
	// one-time value that makes the child the probe's own (cmd/nova-sandbox/main.go).
	// A descriptor cannot be forged by a caller who merely knows an argument, which is
	// why the probe's guard stands on one.
	Extra []*os.File
}

// Net is the word rule 7 puts on the SANDBOX OK line. There is no net=unenforced: a
// denial that cannot be enforced is a refusal, not a word in a line.
func (p *Policy) Net() string {
	if p.NetDeny {
		return "denied"
	}
	return "nopromise"
}

// CmdName is the base name of the executable, and it is the ONLY thing about the argv
// that is ever printed: arguments carry task text and task text carries quoted rules.
func (p *Policy) CmdName() string { return filepath.Base(p.Command) }

// darwinOptRoots is the per-platform optional root table, as DATA and in one place
// (spec: "they are data, not code"). The fixed darwin roots — /, /etc, /tmp, /var as
// literals on the symlinks, /System, /usr, /bin, /sbin, /Library, /private/etc,
// /private/var/select, /dev, and write on /dev/null and /dev/tty — are in
// profiles/darwin.sb.tmpl verbatim, because two copies of a profile is one copy too
// many. What varies per machine is here. A root is SKIPPED if it is absent; only a
// caller's path is refused for absence (rule 5).
var darwinOptRoots = []string{"/opt/homebrew", "/opt/local"}

// fixedDarwinPrefixes are the roots the template already grants as subpaths. An optional
// root under one of them is dropped rather than emitted twice.
var fixedDarwinPrefixes = []string{"/usr", "/bin", "/sbin", "/System", "/Library", "/private/etc", "/private/var/select", "/dev"}

// OptionalRoots is the machine's answer to the table above plus the directory of the
// resolved command, which is a root for exactly this run (the spec's roots table names
// it on all three platforms).
func OptionalRoots(command string) []string {
	var out []string
	seen := map[string]bool{}
	candidates := append([]string{}, darwinOptRoots...)
	if command != "" {
		candidates = append(candidates, filepath.Dir(command))
	}
	for _, r := range candidates {
		if r == "" || r == "/" || seen[r] {
			continue
		}
		if underAny(r, fixedDarwinPrefixes) {
			continue
		}
		if fi, err := os.Stat(r); err != nil || !fi.IsDir() {
			continue // skip-if-absent: /opt/local exists on a Mac with MacPorts and no other
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// underAny reports whether path is one of the prefixes or lies beneath one.
func underAny(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// callerHomes is every directory that is a HOME of the person running the tool: the
// passwd home and $HOME as THIS PROCESS inherited it. It is a var so that a test can
// stand a temporary home in front of it without touching the machine's.
var callerHomes = defaultCallerHomes

func defaultCallerHomes() []string {
	var out []string
	add := func(path string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		if got, err := filepath.EvalSymlinks(path); err == nil {
			path = got
		}
		if got, err := filepath.Abs(path); err == nil {
			path = got
		}
		for _, h := range out {
			if h == path {
				return
			}
		}
		out = append(out, path)
	}
	if u, err := user.Current(); err == nil {
		add(u.HomeDir)
	}
	add(os.Getenv("HOME"))
	return out
}

// commandDirRefusal is the home guard on the roots table's "the directory of the resolved
// command". That entry is in the spec for all three platforms and it stays, because a
// command cannot be exec'd from a directory the wall denies — but it is a root the CALLER
// never typed, and OptionalRoots granted it with no guard at all. A command placed at
// ~/x.sh therefore handed the profile (allow file-read* (subpath "/Users/<user>")) — the
// whole of .ssh, .config/gh and the login keychain — while the SANDBOX OK line said
// read=0. Measured at 1922f9d with a key planted beside the command: the key printed.
//
// The roots section says "The home directory is never a root", so the guard is a refusal
// rather than a silent drop: dropping it would leave a command that cannot be read and a
// run that dies at exec with no reason given. Rule 3's "a caller that adds one back has
// done so in its own argv" is the one exemption, so a directory the caller already named
// in --read or --write is not refused: nothing new is granted there.
func commandDirRefusal(command string, named []string) *Refusal {
	dir := filepath.Dir(command)
	if got, err := filepath.EvalSymlinks(dir); err == nil {
		dir = got
	}
	if insideAny(dir, named) {
		return nil
	}
	for _, home := range callerHomes() {
		// A home the caller pointed INTO the job is not the home this guard is about:
		// rule 9 makes the tool's own $HOME the job's data home, which is inside a
		// --write by construction, so guarding it would refuse every command installed
		// anywhere above the job directory — the tool's own binary included. Measured
		// while writing this: `nova-sandbox probe --write <job>` refused itself.
		if insideAny(home, named) {
			continue
		}
		if dir != home && !Inside(home, dir) {
			continue
		}
		r := refuse("bad_read",
			"the directory of %s is %s, a home directory, and the home directory is never a root: the directory of the resolved command IS a read root, so wrapping a command that lives there would make the whole of %s readable inside the wall. Install the command in a directory of its own, or name the directory in the caller's own --read",
			command, dir, home)
		return &r
	}
	return nil
}

// Inside reports whether path is dir or lies beneath it. Both are expected resolved.
func Inside(path, dir string) bool {
	sep := string(os.PathSeparator)
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, sep)+sep)
}

// insideAny is Inside over a list, and it is what rules 9 and 13 ask of HOME and --cwd.
func insideAny(path string, dirs []string) bool {
	for _, d := range dirs {
		if Inside(path, d) {
			return true
		}
	}
	return false
}

// sbplMetacharacters are the characters a path may not carry ON DARWIN. The ancestor
// literals of the darwin profile put a path INTO the profile text (the -D parameters do
// not), so a path holding a quote, a backslash or a paren could rewrite the policy — and a
// measured run with a path holding a space and a paren aborted at exit 134 (spec, "to
// verify at build" item 2). The decision taken here is the first of the two the spec
// offered: the tool REFUSES such a path, naming the flag, rather than trying to quote it.
const sbplMetacharacters = "\"\\()"

func badPathText(path string) string { return badPathTextFor(runtime.GOOS, path) }

// badPathTextFor is badPathText with the platform named, so that a test on one machine can
// ask what the tool would say on another.
//
// The metacharacter set is DARWIN'S, and applying it everywhere was the windows failure of
// run 34663812025: a backslash is windows's path separator, so every absolute windows path
// carried one and every --write was SANDBOX REFUSED reason=bad_write before the run reached
// the refusal it was about. `%ProgramFiles(x86)%` is in the spec's own windows root table,
// parens and all. Neither the Landlock body nor the AppContainer one writes a path into a
// policy TEXT — they pass file descriptors and ACEs — so neither has this hazard. A control
// character is refused on every platform: no caller means one, and a path holding one
// corrupts any line that prints it.
func badPathTextFor(goos, path string) string {
	if i := strings.IndexAny(path, sbplMetacharacters); goos == "darwin" && i >= 0 {
		return fmt.Sprintf("holds %q, which the generated policy cannot carry", string(path[i]))
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return "holds a control character, which the generated policy cannot carry"
		}
	}
	return ""
}

// resolvePath is rule 5 for one caller path: absolute, existing, symlinks resolved. A
// relative path is refused with the absolute form it WOULD have taken, so the refusal is
// a line the caller can edit rather than a complaint.
func resolvePath(reason, flag, raw string) (string, *Refusal) {
	if strings.TrimSpace(raw) == "" {
		r := refuse(reason, "%s wants an absolute directory path: %s <dir>", flag, flag)
		return "", &r
	}
	if !filepath.IsAbs(raw) {
		abs, err := filepath.Abs(raw)
		if err != nil {
			abs = raw
		}
		r := refuse(reason, "%s %s is relative; %s wants an absolute path, which here would be %s", flag, raw, flag, abs)
		return "", &r
	}
	fi, err := os.Stat(raw)
	if err != nil {
		r := refuse(reason, "%s %s does not exist; every path is named by the caller and none is created", flag, raw)
		return "", &r
	}
	if !fi.IsDir() {
		r := refuse(reason, "%s %s is not a directory; %s wants a directory", flag, raw, flag)
		return "", &r
	}
	resolved, err := filepath.EvalSymlinks(raw)
	if err != nil {
		r := refuse(reason, "%s %s could not be resolved: %v", flag, raw, err)
		return "", &r
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		r := refuse(reason, "%s %s could not be made absolute: %v", flag, raw, err)
		return "", &r
	}
	if bad := badPathText(resolved); bad != "" {
		r := refuse(reason, "%s %s %s", flag, resolved, bad)
		return "", &r
	}
	return resolved, nil
}

// ResolveCallerFile is rule 5 for a caller path that names a FILE rather than a
// directory: --secret is the only one, and before this it was the one caller path the
// tool never resolved and never metacharacter-checked. It is absolute, it exists, it is
// not a directory, its symlinks are followed, and it carries nothing the generated policy
// cannot. A path that does not exist is a refusal, because a probe that "could not read"
// a file that was never there is a pass about nothing.
func ResolveCallerFile(flag, raw string) (string, *Refusal) {
	if strings.TrimSpace(raw) == "" {
		r := refuse("bad_read", "%s wants a path to the file this probe proves it cannot read: %s <path>", flag, flag)
		return "", &r
	}
	if !filepath.IsAbs(raw) {
		abs, err := filepath.Abs(raw)
		if err != nil {
			abs = raw
		}
		r := refuse("bad_read", "%s %s is relative; %s wants an absolute path, which here would be %s", flag, raw, flag, abs)
		return "", &r
	}
	fi, err := os.Stat(raw)
	if err != nil {
		r := refuse("bad_read", "%s %s does not exist; a probe against a file that is not there proves nothing", flag, raw)
		return "", &r
	}
	if fi.IsDir() {
		r := refuse("bad_read", "%s %s is a directory; %s wants the credential file itself", flag, raw, flag)
		return "", &r
	}
	resolved, err := filepath.EvalSymlinks(raw)
	if err != nil {
		r := refuse("bad_read", "%s %s could not be resolved: %v", flag, raw, err)
		return "", &r
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		r := refuse("bad_read", "%s %s could not be made absolute: %v", flag, raw, err)
		return "", &r
	}
	if bad := badPathText(resolved); bad != "" {
		r := refuse("bad_read", "%s %s %s", flag, resolved, bad)
		return "", &r
	}
	return resolved, nil
}

// Build turns an Input into a Policy, or into every independent refusal it holds. It
// creates exactly one directory, rule 8's, and only when the rest of the input is sound.
func Build(in Input) (*Policy, []Refusal) {
	var bad []Refusal
	p := &Policy{NetDeny: in.NetDeny, NetListen: in.NetListen, Name: in.Name}

	// --net-deny and --net-listen ask for opposite things, and a tool that picked one
	// would be deciding which of the two the caller meant.
	if in.NetDeny && in.NetListen {
		bad = append(bad, refuse("bad_net", "--net-deny and --net-listen together: one asks for an enforced denial and the other for an inbound grant; pass at most one"))
	}

	if len(in.Argv) == 0 {
		bad = append(bad, refuse("no_command", "nothing after --; usage: nova-sandbox --read <dir>... --write <dir>... -- <command> <args...>"))
	}
	if len(in.Writes) == 0 {
		bad = append(bad, refuse("bad_write", "--write is required and has no default: refusing to guess which directory this job may write. Name it: --write <dir>"))
	}

	for _, raw := range in.Reads {
		got, r := resolvePath("bad_read", "--read", raw)
		if r != nil {
			bad = append(bad, *r)
			continue
		}
		p.Reads = append(p.Reads, got)
	}
	for _, raw := range in.Writes {
		got, r := resolvePath("bad_write", "--write", raw)
		if r != nil {
			bad = append(bad, *r)
			continue
		}
		p.Writes = append(p.Writes, got)
	}
	// A path given to both lists is a refusal naming both flags, never a silent merge
	// (rule 4): the caller asked for two different things about one directory.
	for _, r := range p.Reads {
		for _, w := range p.Writes {
			if r == w {
				bad = append(bad, refuse("bad_read", "%s is in both --read and --write; name it once, and --write already carries read", r))
			}
		}
	}
	if len(p.Writes) == 0 {
		return nil, bad // everything below is about the first --write
	}
	first := p.Writes[0]

	// rule 13: the cwd defaults to the first --write and must resolve inside the write set.
	p.Cwd = first
	if in.Cwd != "" {
		got, r := resolvePath("bad_cwd", "--cwd", in.Cwd)
		switch {
		case r != nil:
			bad = append(bad, *r)
		case !insideAny(got, p.Writes):
			bad = append(bad, refuse("bad_cwd", "--cwd %s is outside every --write; the working directory is inside the wall, and its default is the first --write (%s)", got, first))
		default:
			p.Cwd = got
		}
	}

	// rule 8: temp is inside the wall, and this is the one directory the tool creates.
	if in.Tmp != "" {
		got, r := resolvePath("bad_write", "--tmp", in.Tmp)
		switch {
		case r != nil:
			bad = append(bad, *r)
		case !insideAny(got, p.Writes):
			bad = append(bad, refuse("bad_write", "--tmp %s is outside every --write; the temp directory is inside the wall", got))
		default:
			p.Tmp = got
		}
	}
	// The default temp directory is created at the BOTTOM of this function, after the
	// command has been resolved, because this comment's own promise — "only when the rest
	// of the input is sound" — was false for a run refused at not_found or not_executable:
	// a refused `nova-sandbox --write <fresh> -- no-such-cmd` left .nova-sandbox-tmp in a
	// directory it never ran in. A refusal makes nothing.
	makeTmp := in.Tmp == ""

	// rule 9: the caller points the child's HOME into the write set, and a HOME outside
	// every --write is a refusal BEFORE the command runs — a wall that lets the job start
	// and kills its first git command is the silent sandbox rule 1 exists to prevent.
	home := in.Home
	if strings.TrimSpace(home) == "" {
		bad = append(bad, refuse("home_outside", "HOME is unset; rule 9 wants HOME set to a data home inside a --write, because almost every tool a worker runs derives a path from it"))
	} else {
		got, err := filepath.EvalSymlinks(home)
		if err != nil {
			bad = append(bad, refuse("home_outside", "HOME %s does not resolve: %v; rule 9 wants HOME set to an existing directory inside a --write", home, err))
		} else if got, _ = filepath.Abs(got); !insideAny(got, p.Writes) {
			bad = append(bad, refuse("home_outside", "HOME %s is outside every --write; set HOME to a per-job data home inside one (a --read is not enough: the first config write dies there)", got))
		} else {
			p.Home = got
		}
	}

	// rule 5: the command is resolved on the CALLER's PATH, here, outside the wall.
	if len(in.Argv) > 0 {
		cmd, r := resolveCommand(in.Argv[0], in.LookAt)
		if r != nil {
			bad = append(bad, *r)
		} else {
			p.Command = cmd
			p.Argv = append([]string{cmd}, in.Argv[1:]...)
			if hr := commandDirRefusal(cmd, append(append([]string{}, p.Reads...), p.Writes...)); hr != nil {
				bad = append(bad, *hr)
			}
		}
	}
	if len(bad) > 0 {
		return nil, bad
	}
	// rule 8, and the one directory this tool creates: everything above passed.
	if makeTmp {
		tmp := filepath.Join(first, tmpDirName)
		if err := os.MkdirAll(tmp, 0o700); err != nil {
			return nil, []Refusal{refuse("bad_write", "could not create %s, the one directory this tool makes: %v", tmp, err)}
		}
		if got, err := filepath.EvalSymlinks(tmp); err == nil {
			p.Tmp = got
		} else {
			p.Tmp = tmp
		}
	}
	p.OptRoots = OptionalRoots(p.Command)
	return p, nil
}

// resolveCommand is the PATH lookup and the pre-flight of the exit-codes section. The
// executability check happens HERE, outside the wall and before any profile exists,
// because on darwin sandbox-exec's own exec failure is an exit 71 the tool cannot see.
func resolveCommand(name, path string) (string, *Refusal) {
	var (
		found string
		err   error
	)
	if strings.ContainsRune(name, os.PathSeparator) {
		found, err = filepath.Abs(name)
		if err != nil {
			r := refuse("not_found", "%s could not be made absolute: %v", name, err)
			return "", &r
		}
	} else {
		lookIn := path
		if lookIn == "" {
			lookIn = os.Getenv("PATH")
		}
		found, err = lookPathIn(name, lookIn)
		if err != nil {
			r := refuse("not_found", "%s is on no PATH entry; name the command or give its absolute path", name)
			return "", &r
		}
	}
	fi, statErr := os.Stat(found)
	switch {
	case statErr != nil:
		r := refuse("not_executable", "%s cannot be run: %v", found, statErr)
		return "", &r
	case fi.IsDir():
		r := refuse("not_executable", "%s is a directory, not a command", found)
		return "", &r
	case runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0:
		r := refuse("not_executable", "%s carries no executable bit for this user", found)
		return "", &r
	}
	if resolved, err := filepath.EvalSymlinks(found); err == nil {
		found = resolved
	}
	return found, nil
}

// lookPathIn is exec.LookPath against a NAMED path rather than the process's own, so a
// test can pin the lookup without writing the environment of a running binary.
func lookPathIn(name, path string) (string, error) {
	if path == os.Getenv("PATH") {
		return exec.LookPath(name)
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() && fi.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s: not found in %s", name, path)
}

// ChildEnv is rule 9's environment: the caller's, unchanged, minus the three temp
// variables the tool sets to rule 8's directory — and minus the agent sockets. It is not
// a secrets tool: the credential the caller deliberately passed by environment must
// arrive, and every other variable passes through untouched.
//
// The agent variables are the exception, and they are a fix this build made to the spec
// rather than something the spec asked for. SSH_AUTH_SOCK names a unix-domain socket
// that speaks for a private key without ever revealing it: a wall that denies ~/.ssh but
// leaves the agent reachable has not stopped the thing ~/.ssh was about. The profile
// denies unix sockets outside the write set, which is the wall; unsetting the variables
// is the fence beside it, so that an honest program does not try and a log does not have
// to be read to see that it could not.
func ChildEnv(env []string, tmp string) []string {
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case name == "TMPDIR", name == "TMP", name == "TEMP":
			continue
		case isAgentVar(name):
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
}

// isAgentVar is rule 9's scrub, and it is by EXCLUSION on the name. Revision 7 states the
// set exactly: SSH_AUTH_SOCK, SSH_AGENT_*, GPG_AGENT_INFO and any *_AGENT_PID/INFO/SOCK. The wall denies the agent's
// socket and the scrub removes its address; a command that would otherwise sign a push
// with a key it cannot read has neither half. Everything else passes through untouched,
// because rule 6's credential must still arrive.
//
// The previous "name contains AGENT" width dropped AI_AGENT and CLAUDE_AGENT_SDK_VERSION,
// which are names that say what is RUNNING the job and address nothing. Under the set
// above both pass through, and the SANDBOX NOTE line is true as it is written.
func isAgentVar(name string) bool {
	switch name {
	case "SSH_AUTH_SOCK", "GPG_AGENT_INFO":
		return true
	}
	if strings.HasPrefix(name, "SSH_AGENT_") {
		return true
	}
	// *_AGENT_PID / *_AGENT_INFO / *_AGENT_SOCK: an agent's pid, address or socket under
	// whatever prefix the next agent invents. AI_AGENT and CLAUDE_AGENT_SDK_VERSION end
	// in none of these and pass through, which is what makes the NOTE line true.
	for _, suffix := range []string{"_AGENT_PID", "_AGENT_INFO", "_AGENT_SOCK"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// DroppedEnv names the variables ChildEnv removes that are not the three temp ones, for
// the one NOTE line the tool prints before the command starts. A reader of a log should
// not have to diff two environments to learn that the agent was taken away.
func DroppedEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case isAgentVar(name):
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Ancestors is every proper ancestor of the given paths, "/" excluded, sorted and
// unique. It is what the darwin profile's file-read-metadata literals are built from,
// and it is here rather than in the darwin body so that a test on any platform can
// assert its shape.
func Ancestors(paths ...string) []string { return ancestors(filepath.Dir, paths...) }

// ancestors is Ancestors with the parent function named, so that a test on one platform can
// walk the other's paths: filepath.Dir's answer at the top of the tree differs per platform
// and the stop condition is the whole of this function's correctness.
func ancestors(dir func(string) string, paths ...string) []string {
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" {
			continue
		}
		// The stop is "d is its own parent", not the literal "/": on windows the top of
		// the tree is `C:\` (and filepath.Dir(`C:\`) is `C:\`), so a loop that waited for
		// "/" spun on the volume root forever — the 600s timeout of run 34663812025. Asking the
		// parent function where IT stops is the one form that is right on every platform.
		//
		// A path with a trailing separator is its own first "ancestor": Dir("/a/b/") is
		// "/a/b", so Ancestors("/a/b/") returned "/a /a/b" against this function's own
		// word "every PROPER ancestor" — and "/a/b" then got a file-read-metadata literal
		// it already holds by its own subpath rule. Skipping the cleaned path itself is
		// the whole repair; the loop still walks from there upwards.
		self := filepath.Clean(p)
		for d := dir(p); d != "." && d != "" && dir(d) != d; d = dir(d) {
			if d == self {
				continue
			}
			seen[d] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
