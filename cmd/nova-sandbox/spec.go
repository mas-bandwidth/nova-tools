package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// refusal is one reason the wall will not start. Every independent problem is
// reported at once, one line each.
type refusal struct{ reason, text string }

func refuse(reason, format string, a ...any) refusal {
	return refusal{reason, fmt.Sprintf(format, a...)}
}

// spec is the command line as given.
type spec struct {
	reads, writes, netAllow, passEnv []string
	netDeny, gpu, unix               bool
	argv                             []string
}

// wall is a checked spec: every path absolute, existing and symlink-free.
type wall struct {
	reads, writes []string
	linkSpellings []string // the caller's spelling where it differs from the resolved path
	optRoots      []string // darwin: the active developer directory the /usr/bin shims read
	netDeny, gpu  bool
	unix          bool
	netPorts      []uint16 // --net-allow, loopback only
	passEnv       []string // --env names, beyond PATH, HOME, LANG and TERM
	command       string
	argv          []string
}

// build checks s for goos and returns the wall, or every refusal it holds. It creates
// nothing.
func build(s spec, goos string) (*wall, []refusal) {
	var bad []refusal
	w := &wall{netDeny: s.netDeny, gpu: s.gpu, unix: s.unix}
	if s.gpu && goos != "darwin" {
		bad = append(bad, refuse("bad_gpu", "--gpu is macOS only; on Linux grant the device directory itself, e.g. --write /dev/dri"))
	}
	if s.unix && goos == "darwin" {
		bad = append(bad, refuse("bad_unix", "--unix is Linux only; on macOS a socket inside a --write grant is reachable without it, e.g. --write <socket dir>"))
	}
	if len(s.netAllow) > 0 && !s.netDeny {
		bad = append(bad, refuse("bad_net", "--net-allow reopens a port under --net-deny and means nothing without it"))
	}
	for _, hp := range s.netAllow {
		host, port, err := net.SplitHostPort(hp)
		ip := net.ParseIP(host)
		n, perr := strconv.ParseUint(port, 10, 16)
		switch {
		case err != nil || perr != nil || n == 0:
			bad = append(bad, refuse("bad_net", "--net-allow wants host:port and got %s", hp))
		case !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()):
			bad = append(bad, refuse("bad_net", "--net-allow %s is not loopback; the kernel filters by port only, so a remote host cannot be named honestly", hp))
		default:
			w.netPorts = append(w.netPorts, uint16(n))
		}
	}
	resolveAll := func(flag string, raw []string, dst *[]string) {
		for _, r := range raw {
			got, rf := resolveDir(goos, flag, r)
			if rf != nil {
				bad = append(bad, *rf)
				continue
			}
			c := filepath.Clean(r)
			if m := badPath(goos, c); m != "" { // the spelling is a literal in the profile too
				bad = append(bad, refuse("bad_"+strings.TrimPrefix(flag, "--"), "%s %s %s", flag, c, m))
				continue
			}
			*dst = append(*dst, got)
			if c != got && !slices.Contains(w.linkSpellings, c) {
				w.linkSpellings = append(w.linkSpellings, c)
			}
		}
	}
	resolveAll("--read", s.reads, &w.reads)
	resolveAll("--write", s.writes, &w.writes)
	if goos == "darwin" {
		w.optRoots = developerDir("/var/db/xcode_select_link")
	}
	for _, n := range s.passEnv {
		if !envNameOK(n) {
			bad = append(bad, refuse("bad_env", "--env wants a name of letters, digits and underscores, got %q", n))
		} else if !slices.Contains(w.passEnv, n) {
			w.passEnv = append(w.passEnv, n)
		}
	}
	if len(s.argv) > 0 {
		spelled, target, rf := resolveCommand(s.argv[0])
		switch {
		case rf != nil:
			bad = append(bad, *rf)
		case !underAny(target, slices.Concat(systemRoots(goos), w.optRoots, w.reads, w.writes)):
			bad = append(bad, refuse("bad_read", "%s is under no system root, --read or --write; name its directory with --read", target))
		case badPath(goos, spelled) != "":
			bad = append(bad, refuse("bad_read", "the command %s %s", spelled, badPath(goos, spelled)))
		default:
			// The exec target is the caller's spelling made absolute, symlinks kept, and
			// argv is the caller's verbatim: a multi-call binary, a venv python, a
			// cc->clang shim or a #! script reached through a link sees the name it was
			// invoked as. Only the checks above use the resolved target.
			w.command, w.argv = spelled, s.argv
			if spelled != target && !slices.Contains(w.linkSpellings, spelled) {
				w.linkSpellings = append(w.linkSpellings, spelled) // macOS reads a link it follows
			}
		}
	}
	if len(bad) > 0 {
		return nil, bad
	}
	return w, nil
}

func systemRoots(goos string) []string {
	if goos == "darwin" {
		return darwinRoots
	}
	return linuxReadRoots
}

// resolveDir checks one caller directory: absolute, existing, a directory, and
// carrying nothing the generated rules cannot. It returns the symlink-free path.
func resolveDir(goos, flag, raw string) (string, *refusal) {
	reason := "bad_" + strings.TrimPrefix(flag, "--")
	fail := func(format string, a ...any) (string, *refusal) {
		r := refuse(reason, "%s "+format, append([]any{flag}, a...)...)
		return "", &r
	}
	if !filepath.IsAbs(raw) {
		return fail("%q is not an absolute path", raw)
	}
	fi, err := os.Stat(raw)
	if err != nil || !fi.IsDir() {
		if flag == "--write" && os.IsNotExist(err) {
			return fail("%s is not an existing directory; mkdir -p %s", raw, raw)
		}
		return fail("%s is not an existing directory; none is created", raw)
	}
	resolved, err := filepath.EvalSymlinks(raw)
	if err != nil {
		return fail("%s could not be resolved: %v", raw, err)
	}
	if m := badPath(goos, resolved); m != "" {
		return fail("%s %s", resolved, m)
	}
	return resolved, nil
}

// badPath says why a path cannot go into the generated rules, or "". On darwin a path
// is written into the profile text (the ancestor literals), so a quote, backslash or
// paren could rewrite the profile: such a path is refused, not quoted. A control
// character is refused everywhere.
func badPath(goos, p string) string {
	if i := strings.IndexAny(p, "\"\\()"); goos == "darwin" && i >= 0 {
		return fmt.Sprintf("holds %q, which the generated profile cannot carry", string(p[i]))
	}
	if strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "holds a control character, which the generated rules cannot carry"
	}
	return ""
}

func underAny(p string, roots []string) bool {
	return slices.ContainsFunc(roots, func(d string) bool { return p == d || strings.HasPrefix(p, strings.TrimSuffix(d, "/")+"/") })
}

// wallLine is the one stderr line a run prints once the wall has been built.
// System roots are not listed. net=denied only when --net-deny was set.
func wallLine(w *wall) string {
	net := "open"
	if w.netDeny {
		net = "denied"
	}
	if len(w.reads)+len(w.writes) == 0 {
		return "WALL grants=none net=" + net
	}
	s := "WALL"
	if len(w.reads) > 0 {
		s += " read=" + strings.Join(w.reads, ",")
	}
	if len(w.writes) > 0 {
		s += " write=" + strings.Join(w.writes, ",")
	}
	return s + " net=" + net
}

// outsideCwd reports a working directory outside the system roots and every grant.
// The text names a directory to cd into, or tells the caller to --read this one.
func outsideCwd(dir string, w *wall, goos string) (refusal, bool) {
	if underAny(dir, slices.Concat(systemRoots(goos), w.optRoots, w.reads, w.writes)) {
		return refusal{}, false
	}
	grant := "a granted directory"
	switch {
	case len(w.writes) > 0:
		grant = w.writes[0]
	case len(w.reads) > 0:
		grant = w.reads[0]
	}
	return refuse("cwd", "%s is outside the wall; cd %s or add --read %s", dir, grant, dir), true
}

// resolveCommand finds the command on PATH and checks it can run, outside the wall:
// sandbox-exec's own exec failure is an exit 71 that cannot be told from the command's.
// It returns the caller's spelling made absolute (symlinks kept) and the resolved
// target. A name on no PATH entry or a path that does not exist is not_found (127); an
// existing file that cannot be executed is not_executable (126).
func resolveCommand(name string) (spelled, target string, _ *refusal) {
	found, err := exec.LookPath(name)
	if strings.Contains(name, "/") {
		found, err = name, nil
	}
	if err == nil {
		found, err = filepath.Abs(found)
	}
	fi, serr := os.Stat(found)
	if err != nil || errors.Is(serr, fs.ErrNotExist) {
		r := refuse("not_found", "%s is on no PATH entry or does not exist", name)
		return "", "", &r
	}
	if serr != nil || fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
		r := refuse("not_executable", "%s is not an executable file", found)
		return "", "", &r
	}
	target, err = filepath.EvalSymlinks(found)
	if err != nil {
		target = found
	}
	return found, target, nil
}

// developerDir is the directory the xcode-select link points at, when it is not under
// a fixed root: the /usr/bin compiler and git shims read it to find the tools. The
// shims also read Info.plist and SharedFrameworks beside Developer, so the grant is
// its parent.
func developerDir(link string) []string {
	t, err := os.Readlink(link)
	if err != nil || t == "" {
		return nil
	}
	if !filepath.IsAbs(t) {
		t = filepath.Join(filepath.Dir(link), t)
	}
	if got, err := filepath.EvalSymlinks(t); err == nil {
		t = got
	}
	t = strings.TrimSuffix(t, "/Developer")
	if fi, err := os.Stat(t); err != nil || !fi.IsDir() || t == "/" || underAny(t, darwinRoots) || badPath("darwin", t) != "" {
		return nil
	}
	return []string{t}
}

// envNameOK is a letter or underscore, then letters, digits and underscores.
var envNameOK = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString

// passedEnv keeps PATH, HOME, LANG, TERM and extra. Never nil: syscall.Exec inherits a nil env.
func passedEnv(env, extra []string) []string {
	keep := map[string]bool{"PATH": true, "HOME": true, "LANG": true, "TERM": true}
	for _, n := range extra {
		keep[n] = true
	}
	out, seen := []string{}, map[string]bool{}
	for _, e := range env {
		if n, _, ok := strings.Cut(e, "="); ok && keep[n] && !seen[n] {
			seen[n], out = true, append(out, e)
		}
	}
	return out
}
func missingEnv(env, names []string) (bad []refusal) {
	have := map[string]bool{}
	for _, e := range env {
		if n, _, ok := strings.Cut(e, "="); ok {
			have[n] = true
		}
	}
	for _, n := range names {
		if !envNameOK(n) || have[n] {
			continue
		}
		have[n] = true
		bad = append(bad, refuse("bad_env", "--env %s is not set; set it in the environment, the value is never an argument", n))
	}
	return bad
}
