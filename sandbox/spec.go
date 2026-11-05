package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
	reads, writes, netAllow []string
	netDeny, gpu            bool
	argv                    []string
}

// wall is a checked spec: every path absolute, existing and symlink-free.
type wall struct {
	reads, writes []string
	linkSpellings []string // the caller's spelling where it differs from the resolved path
	optRoots      []string // darwin: the active developer directory the /usr/bin shims read
	netDeny, gpu  bool
	netPorts      []uint16 // --net-allow, loopback only
	command       string
	argv          []string
}

// build checks s for goos and returns the wall, or every refusal it holds. It creates
// nothing.
func build(s spec, goos string) (*wall, []refusal) {
	var bad []refusal
	w := &wall{netDeny: s.netDeny, gpu: s.gpu}
	if s.gpu && goos != "darwin" {
		bad = append(bad, refuse("bad_gpu", "--gpu is macOS only; on Linux grant the device directory itself, e.g. --write /dev/dri"))
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
			*dst = append(*dst, got)
			if c := filepath.Clean(r); c != got && !slices.Contains(w.linkSpellings, c) {
				w.linkSpellings = append(w.linkSpellings, c)
			}
		}
	}
	resolveAll("--read", s.reads, &w.reads)
	resolveAll("--write", s.writes, &w.writes)
	if goos == "darwin" {
		w.optRoots = developerDir()
	}
	if len(s.argv) > 0 {
		cmd, rf := resolveCommand(s.argv[0])
		switch {
		case rf != nil:
			bad = append(bad, *rf)
		case !underAny(cmd, slices.Concat(systemRoots(goos), w.optRoots, w.reads, w.writes)):
			bad = append(bad, refuse("bad_read", "%s is under no system root, --read or --write; name its directory with --read", cmd))
		default:
			w.command, w.argv = cmd, append([]string{cmd}, s.argv[1:]...)
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

// resolveCommand finds the command on PATH and checks it can run, outside the wall:
// sandbox-exec's own exec failure is an exit 71 that cannot be told from the
// command's. A name on no PATH entry is reason not_found (exit 127).
func resolveCommand(name string) (string, *refusal) {
	found, err := exec.LookPath(name)
	if strings.Contains(name, "/") {
		found, err = filepath.Abs(name)
	}
	if err != nil {
		r := refuse("not_found", "%s is on no PATH entry", name)
		return "", &r
	}
	if fi, err := os.Stat(found); err != nil || fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
		r := refuse("not_executable", "%s is not an executable file", found)
		return "", &r
	}
	if resolved, err := filepath.EvalSymlinks(found); err == nil {
		found = resolved
	}
	return found, nil
}

// developerDir is the directory the xcode-select link points at, when it is not under
// a fixed root: the /usr/bin compiler and git shims read it to find the tools. The
// shims also read Info.plist and SharedFrameworks beside Developer, so the grant is
// its parent.
func developerDir() []string {
	t, err := os.Readlink("/var/db/xcode_select_link")
	if err != nil || t == "" {
		return nil
	}
	if !filepath.IsAbs(t) {
		t = filepath.Join("/var/db", t)
	}
	if got, err := filepath.EvalSymlinks(t); err == nil {
		t = got
	}
	t = strings.TrimSuffix(t, "/Developer")
	if fi, err := os.Stat(t); err != nil || !fi.IsDir() || t == "/" || underAny(t, darwinRoots) {
		return nil
	}
	return []string{t}
}
