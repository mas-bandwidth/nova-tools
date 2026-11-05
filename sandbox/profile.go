package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed darwin.sb.tmpl
var darwinTemplate string

// darwinRoots are the roots the template grants read as subpaths: a command under one
// needs no --read, and an optional root under one is not granted twice.
var darwinRoots = []string{"/usr", "/bin", "/sbin", "/opt", "/System", "/Library", "/private/etc", "/private/var/select", "/dev"}

// metalGrants is --gpu: the Metal IOKit clients and XPC services by name, never a
// blanket iokit-open or mach-lookup.
const metalGrants = `
(allow iokit-open (iokit-user-client-class "AGXDeviceUserClient" "IOSurfaceRootUserClient"))
(allow mach-lookup (xpc-service-name "com.apple.MTLCompilerService" "com.apple.coremedia.videoencoder" "com.apple.coremedia.videodecoder"))
`

// darwinProfile fills the template for one run and returns the text and the -D
// parameters it names. Caller paths enter as parameters, except the ancestor and
// link literals, which is why badPath refuses profile metacharacters.
func darwinProfile(w *wall) (text string, params []string, err error) {
	var optRoots, anc, reads, writes, netLines []string
	for _, r := range w.optRoots {
		optRoots = append(optRoots, fmt.Sprintf("(allow file-read* (subpath %q))", r))
	}
	for _, d := range ancestors(slices.Concat(w.reads, w.writes, w.optRoots)...) {
		anc = append(anc, fmt.Sprintf("(allow file-read-metadata (literal %q))", d))
	}
	for i, r := range w.reads {
		reads = append(reads, fmt.Sprintf(`(allow file-read* (subpath (param "READ%d")))`, i))
		params = append(params, fmt.Sprintf("READ%d=%s", i, r))
	}
	// Following a symlink needs read on the link: the caller's own spelling of a granted
	// path, and each link among its ancestors, is a literal.
	for _, l := range linkLiterals(w.linkSpellings) {
		reads = append(reads, fmt.Sprintf("(allow file-read* (literal %q))", l))
	}
	for i, d := range w.writes {
		writes = append(writes,
			fmt.Sprintf(`(allow file-read* file-write* (subpath (param "WRITE%d")))`, i),
			fmt.Sprintf(`(allow network-outbound (subpath (param "WRITE%d")))`, i))
		params = append(params, fmt.Sprintf("WRITE%d=%s", i, d))
	}
	if !w.netDeny {
		// DNS is the mDNSResponder socket, so it goes with the network.
		netLines = append(netLines, `(allow network-outbound (remote ip) (literal "/private/var/run/mDNSResponder"))`)
	}
	// sandbox-exec takes only the literal "localhost" as a host here, and it admits
	// both 127.0.0.1 and ::1.
	for _, port := range w.netPorts {
		netLines = append(netLines, fmt.Sprintf(`(allow network-outbound (remote ip "localhost:%d"))`, port))
	}
	fill := map[string]string{
		"@@OPTROOTS@@": strings.Join(optRoots, "\n"), "@@ANCESTORS@@": strings.Join(anc, "\n"),
		"@@READS@@": strings.Join(reads, "\n"), "@@WRITES@@": strings.Join(writes, "\n"),
		"@@NET@@": strings.Join(netLines, "\n"),
	}
	var out strings.Builder
	used := 0
	for _, line := range strings.Split(darwinTemplate, "\n") {
		body, ok := fill[line]
		if !ok {
			out.WriteString(line + "\n")
			continue
		}
		used++
		if body != "" {
			out.WriteString(body + "\n")
		}
	}
	if used != len(fill) {
		return "", nil, fmt.Errorf("darwin.sb.tmpl and the generator have diverged: %d of %d markers found", used, len(fill))
	}
	text = out.String()
	if w.gpu {
		text += metalGrants
	}
	return text, params, nil
}

// ancestors is every proper ancestor of paths, "/" excluded, sorted, once each: stat
// on them is what lets mkdir and stat by absolute path reach a granted directory.
func ancestors(paths ...string) []string {
	var out []string
	for _, p := range paths {
		for d := filepath.Dir(filepath.Clean(p)); filepath.Dir(d) != d; d = filepath.Dir(d) {
			if !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	slices.Sort(out)
	return out
}

// linkLiterals is each spelling and every symlink among its ancestors, once, in order.
func linkLiterals(spellings []string) []string {
	var out []string
	add := func(p string) {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	for _, s := range spellings {
		for d := filepath.Dir(s); filepath.Dir(d) != d; d = filepath.Dir(d) {
			if fi, err := os.Lstat(d); err == nil && fi.Mode()&os.ModeSymlink != 0 {
				add(d)
			}
		}
		add(s)
	}
	return out
}
