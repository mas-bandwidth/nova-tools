package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// refusal is one reason the wall will not start. Every independent problem is
// reported at once, one line each, and the exit status is 125.
type refusal struct{ reason, text string }

func refuse(reason, format string, a ...any) refusal {
	return refusal{reason, fmt.Sprintf(format, a...)}
}

// input is everything a wall is built from. Nothing is defaulted from the
// environment except the first --write (cwd, tmp) and the PATH the command is
// looked up on.
type input struct {
	reads, readsNoExec, writes []string
	cwd, tmp, home             string
	netDeny                    bool
	netAllow                   []string
	gpu                        string
	argv                       []string
	pathEnv                    string   // PATH, for resolving the command
	homes                      []string // the caller's own home directories
}

// policy is the checked, resolved form of an input.
type policy struct {
	reads, readsNoExec, writes []string // resolved
	linkSpellings              []string // the caller's spelling where it differs from the resolved path
	optRoots, pathDirs         []string
	cwd, tmp, home             string
	netDeny                    bool
	netAllow                   []string
	gpu                        bool
	command                    string
	argv                       []string
}

const tmpDirName = ".nova-sandbox-tmp"

// build checks in and returns the policy, or every refusal it holds. It makes
// one directory, the default temp directory, and only when nothing was refused.
func build(in input) (*policy, []refusal) {
	var bad []refusal
	p := &policy{netDeny: in.netDeny}
	switch in.gpu {
	case "", "none":
	case "metal":
		p.gpu = true
	default:
		bad = append(bad, refuse("bad_gpu", "--gpu wants none or metal and got %s", in.gpu))
	}
	for _, hp := range in.netAllow {
		host, port, err := net.SplitHostPort(hp)
		ip := net.ParseIP(host)
		switch {
		case err != nil || host == "" || port == "" || badPath(runtime.GOOS, hp) != "":
			bad = append(bad, refuse("bad_net", "--net-allow wants host:port and got %s", hp))
		case !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()):
			bad = append(bad, refuse("bad_net", "--net-allow %s is not this machine's loopback; it opens a local port back up, never a remote address", hp))
		default:
			p.netAllow = append(p.netAllow, hp)
		}
	}
	if len(in.argv) == 0 {
		bad = append(bad, refuse("no_command", "nothing after --; usage: nova-sandbox --read <dir>... --write <dir>... -- <command> <args...>"))
	}
	if len(in.writes) == 0 {
		bad = append(bad, refuse("bad_write", "--write is required and has no default; name the directory this job may write"))
	}
	resolveAll := func(flag, reason string, raw []string, dst *[]string) {
		for _, r := range raw {
			got, rf := resolveDir(reason, flag, r)
			if rf != nil {
				bad = append(bad, *rf)
				continue
			}
			*dst = append(*dst, got)
			if c := filepath.Clean(r); c != got && !slices.Contains(p.linkSpellings, c) {
				p.linkSpellings = append(p.linkSpellings, c)
			}
		}
	}
	resolveAll("--read", "bad_read", in.reads, &p.reads)
	resolveAll("--read-noexec", "bad_read", in.readsNoExec, &p.readsNoExec)
	resolveAll("--write", "bad_write", in.writes, &p.writes)
	// One directory in two lists is a contradiction, not something to merge.
	for _, w := range p.writes {
		if slices.Contains(p.reads, w) {
			bad = append(bad, refuse("bad_read", "%s is in both --read and --write; name it once, --write already carries read", w))
		}
		if slices.Contains(p.readsNoExec, w) {
			bad = append(bad, refuse("bad_read", "%s is in both --read-noexec and --write; --write carries execute", w))
		}
	}
	for _, n := range p.readsNoExec {
		if slices.Contains(p.reads, n) {
			bad = append(bad, refuse("bad_read", "%s is in both --read and --read-noexec; one gives execute and the other takes it away", n))
		}
	}
	if len(p.writes) == 0 {
		return nil, bad
	}
	first := p.writes[0]

	p.cwd = first
	if in.cwd != "" {
		got, rf := resolveDir("bad_cwd", "--cwd", in.cwd)
		switch {
		case rf != nil:
			bad = append(bad, *rf)
		case !insideAny(got, p.writes):
			bad = append(bad, refuse("bad_cwd", "--cwd %s is outside every --write; its default is the first --write (%s)", got, first))
		default:
			p.cwd = got
		}
	}
	if in.tmp != "" {
		got, rf := resolveDir("bad_write", "--tmp", in.tmp)
		switch {
		case rf != nil:
			bad = append(bad, *rf)
		case !insideAny(got, p.writes):
			bad = append(bad, refuse("bad_write", "--tmp %s is outside every --write", got))
		default:
			p.tmp = got
		}
	}

	// HOME is never defaulted: almost every tool derives a path from it, and the
	// caller's own HOME is denied by the wall. It must be inside a --write.
	if strings.TrimSpace(in.home) == "" {
		bad = append(bad, refuse("home_outside", "HOME is unset; set it to a data home inside a --write"))
	} else if got, err := filepath.EvalSymlinks(in.home); err != nil {
		bad = append(bad, refuse("home_outside", "HOME %s does not resolve: %v", in.home, err))
	} else if !insideAny(got, p.writes) {
		bad = append(bad, refuse("home_outside", "HOME %s is outside every --write; set it to a data home inside one (a --read is not enough)", got))
	} else if m := badPath(runtime.GOOS, got); m != "" {
		bad = append(bad, refuse("home_outside", "HOME %s %s", got, m))
	} else {
		p.home = got
	}

	if len(in.argv) > 0 {
		cmd, rf := resolveCommand(in.argv[0])
		if rf != nil {
			bad = append(bad, *rf)
		} else {
			p.command, p.argv = cmd, append([]string{cmd}, in.argv[1:]...)
			named := append(slices.Clone(p.reads), p.writes...)
			if rf := commandDirRefusal(cmd, named, in.homes); rf != nil {
				bad = append(bad, *rf)
			}
		}
	}
	if len(bad) > 0 {
		return nil, bad
	}
	if in.tmp == "" {
		tmp := filepath.Join(first, tmpDirName)
		if err := os.MkdirAll(tmp, 0o700); err != nil {
			return nil, []refusal{refuse("bad_write", "could not create %s: %v", tmp, err)}
		}
		p.tmp = tmp
		if got, err := filepath.EvalSymlinks(tmp); err == nil {
			p.tmp = got
		}
	}
	p.optRoots = optionalRoots(p.command)
	p.pathDirs = pathDirs(in.pathEnv, in.homes, p.optRoots, p.reads, p.writes)
	return p, nil
}

// resolveDir checks one caller directory: absolute, existing, a directory, and
// carrying nothing the generated policy cannot. It returns the symlink-free path.
func resolveDir(reason, flag, raw string) (string, *refusal) {
	fail := func(format string, a ...any) (string, *refusal) {
		r := refuse(reason, "%s "+format, append([]any{flag}, a...)...)
		return "", &r
	}
	switch {
	case strings.TrimSpace(raw) == "":
		return fail("wants an absolute directory path")
	case !filepath.IsAbs(raw):
		return fail("%s is relative; it wants an absolute path", raw)
	}
	fi, err := os.Stat(raw)
	if err != nil {
		return fail("%s does not exist; every path is the caller's and none is created", raw)
	}
	if !fi.IsDir() {
		return fail("%s is not a directory", raw)
	}
	resolved, err := filepath.EvalSymlinks(raw)
	if err != nil {
		return fail("%s could not be resolved: %v", raw, err)
	}
	if bad := badPath(runtime.GOOS, resolved); bad != "" {
		return fail("%s %s", resolved, bad)
	}
	return resolved, nil
}

// badPath says why a path cannot go into the generated policy, or "". On darwin a
// path is written into the profile text (the ancestor literals), so a quote,
// backslash or paren could rewrite the policy: such a path is refused, not quoted.
// A control character is refused everywhere.
func badPath(goos, p string) string {
	if i := strings.IndexAny(p, "\"\\()"); goos == "darwin" && i >= 0 {
		return fmt.Sprintf("holds %q, which the generated policy cannot carry", string(p[i]))
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "holds a control character, which the generated policy cannot carry"
		}
	}
	return ""
}

// inside reports whether p is dir or lies beneath it. Both are resolved and exist,
// so the answer is the filesystem's (same device and inode, which holds for a
// case-folding APFS), with the string prefix as the cheap first answer.
func inside(p, dir string) bool {
	if p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/") {
		return true
	}
	target, err := os.Stat(dir)
	if err != nil {
		return false
	}
	for at := p; ; {
		if fi, err := os.Stat(at); err == nil && os.SameFile(fi, target) {
			return true
		}
		up := filepath.Dir(at)
		if up == at {
			return false
		}
		at = up
	}
}

func insideAny(p string, dirs []string) bool {
	return slices.ContainsFunc(dirs, func(d string) bool { return inside(p, d) })
}

func underAny(p string, prefixes []string) bool {
	return slices.ContainsFunc(prefixes, func(d string) bool { return p == d || strings.HasPrefix(p, d+"/") })
}

// resolveCommand finds the command on PATH and checks it can run, outside the wall:
// sandbox-exec's own exec failure is an exit 71 the tool could not tell from the
// command's. A name that is on no PATH entry is reason not_found (exit 127).
func resolveCommand(name string) (string, *refusal) {
	found := name
	if strings.Contains(name, "/") {
		abs, err := filepath.Abs(name)
		if err != nil {
			r := refuse("not_found", "%s could not be made absolute: %v", name, err)
			return "", &r
		}
		found = abs
	} else {
		var err error
		if found, err = exec.LookPath(name); err != nil {
			r := refuse("not_found", "%s is on no PATH entry; name the command or give its absolute path", name)
			return "", &r
		}
	}
	fi, err := os.Stat(found)
	switch {
	case err != nil:
		r := refuse("not_executable", "%s cannot be run: %v", found, err)
		return "", &r
	case fi.IsDir():
		r := refuse("not_executable", "%s is a directory, not a command", found)
		return "", &r
	case fi.Mode().Perm()&0o111 == 0:
		r := refuse("not_executable", "%s carries no executable bit for this user", found)
		return "", &r
	}
	if resolved, err := filepath.EvalSymlinks(found); err == nil {
		found = resolved
	}
	return found, nil
}

// commandDirRefusal stops the command's own directory, which the wall must make
// readable for the command to start, from handing over a home directory: a command
// at ~/x.sh would make the whole of ~/.ssh readable. A directory the caller already
// named in --read or --write is the caller's grant and passes.
func commandDirRefusal(command string, named, homes []string) *refusal {
	dir := filepath.Dir(command)
	if got, err := filepath.EvalSymlinks(dir); err == nil {
		dir = got
	}
	if insideAny(dir, named) {
		return nil
	}
	for _, home := range homes {
		if insideAny(home, named) || (dir != home && !inside(home, dir)) {
			continue
		}
		r := refuse("bad_read", "the directory of %s is %s, a home directory, and the directory of the command is a read root: that would make the whole of %s readable. Install the command in a directory of its own, or name its directory with --read", command, dir, home)
		return &r
	}
	return nil
}

// callerHomes is the passwd home and $HOME of the person running the tool.
func callerHomes() []string {
	var out []string
	add := func(p string) {
		if strings.TrimSpace(p) == "" {
			return
		}
		if got, err := filepath.EvalSymlinks(p); err == nil {
			p = got
		}
		if abs, err := filepath.Abs(p); err == nil && !slices.Contains(out, abs) {
			out = append(out, abs)
		}
	}
	if u, err := user.Current(); err == nil {
		add(u.HomeDir)
	}
	add(os.Getenv("HOME"))
	return out
}

// optionalRoots are the per-machine read roots a command needs to run: Homebrew and
// MacPorts, the directory the Xcode shims point at, and the command's own directory.
// An absent root is skipped, never refused. Roots the darwin template already grants
// are dropped.
func optionalRoots(command string) []string {
	cands := []string{"/opt/homebrew", "/opt/local"}
	for _, link := range []string{"/var/db/xcode_select_link", "/private/var/db/xcode_select_link"} {
		t, err := os.Readlink(link)
		if err != nil || t == "" {
			continue
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(link), t)
		}
		if got, err := filepath.EvalSymlinks(t); err == nil {
			t = got
		}
		// The shims read Info.plist and SharedFrameworks next to Developer.
		cands = append(cands, strings.TrimSuffix(t, "/Developer"))
	}
	if command != "" {
		cands = append(cands, filepath.Dir(command))
	}
	var out []string
	for _, r := range cands {
		if fi, err := os.Stat(r); r == "/" || underAny(r, fixedDarwin) || slices.Contains(out, r) || err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, r)
	}
	return out
}

// pathDirs are the existing PATH directories no other grant covers. On darwin they get
// stat only, so a command installed in ~/.local/bin resolves by name while its files
// stay unreadable. A caller's home itself is skipped.
func pathDirs(pathEnv string, homes, optRoots, reads, writes []string) []string {
	var out []string
	for _, raw := range filepath.SplitList(pathEnv) {
		abs, err := filepath.Abs(strings.TrimSpace(raw))
		if fi, serr := os.Stat(abs); raw == "" || err != nil || serr != nil || !fi.IsDir() {
			continue
		}
		res, err := filepath.EvalSymlinks(abs)
		if err != nil {
			res = abs
		}
		if slices.Contains(homes, res) || slices.Contains(homes, abs) {
			continue
		}
		for _, d := range []string{abs, res} {
			if d != "/" && !slices.Contains(out, d) && !underAny(d, fixedDarwin) &&
				!underAny(d, optRoots) && !underAny(d, reads) && !underAny(d, writes) {
				out = append(out, d)
			}
		}
	}
	return out
}

// ancestors is every proper ancestor of paths, "/" excluded, sorted, once each.
func ancestors(paths ...string) []string {
	seen := map[string]bool{}
	for _, p := range paths {
		for d := filepath.Dir(p); d != "." && d != "" && filepath.Dir(d) != d; d = filepath.Dir(d) {
			if d != filepath.Clean(p) {
				seen[d] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	slices.Sort(out)
	return out
}

// childEnv is the environment the command gets: the caller's, with the temp
// variables pointed into the wall and the agent sockets removed. An agent socket
// speaks for a key the wall denies; unsetting the variables is the fence beside the
// wall itself. JAVA_TOOL_OPTIONS gains the wall's temp dir because a JVM defaults
// its temp dir to /tmp, which the wall denies.
func childEnv(env []string, tmp string) []string {
	out := make([]string, 0, len(env)+5)
	java := "-Djava.io.tmpdir=" + tmp
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		switch {
		case name == "TMPDIR", name == "TMP", name == "TEMP", name == "TMPPREFIX", isAgentVar(name):
		case name == "JAVA_TOOL_OPTIONS":
			if v := strings.TrimSpace(value); v != "" {
				java = v + " " + java
			}
		default:
			out = append(out, kv)
		}
	}
	return append(out, "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp, "TMPPREFIX="+path.Join(tmp, "zsh"), "JAVA_TOOL_OPTIONS="+java)
}

// isAgentVar names the agent address variables exactly: SSH_AUTH_SOCK, SSH_AGENT_*,
// GPG_AGENT_INFO and any *_AGENT_PID/INFO/SOCK. AI_AGENT and the like address nothing
// and pass.
func isAgentVar(name string) bool {
	if name == "SSH_AUTH_SOCK" || name == "GPG_AGENT_INFO" || strings.HasPrefix(name, "SSH_AGENT_") {
		return true
	}
	for _, s := range []string{"_AGENT_PID", "_AGENT_INFO", "_AGENT_SOCK"} {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// homeOf is HOME in env.
func homeOf(env []string) string {
	for _, kv := range env {
		if name, v, _ := strings.Cut(kv, "="); name == "HOME" {
			return v
		}
	}
	return ""
}

func parentOf(p string) string { return filepath.Dir(p) }

func isLink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}
