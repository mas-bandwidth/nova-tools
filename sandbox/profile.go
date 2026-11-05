package main

import (
	_ "embed"
	"fmt"
	"net"
	"slices"
	"strings"
)

//go:embed darwin.sb.tmpl
var darwinTemplate string

// fixedDarwin are the roots the template grants as subpaths; an optional root or PATH
// directory under one is dropped rather than granted twice.
var fixedDarwin = []string{"/usr", "/bin", "/sbin", "/System", "/Library", "/private/etc", "/private/var/select", "/dev"}

// metalGrants is --gpu metal: named IOKit clients and XPC services only, never a
// blanket iokit-open or mach-lookup.
const metalGrants = `
(allow iokit-open (iokit-user-client-class "AGXDeviceUserClient" "IOSurfaceRootUserClient"))
(allow mach-lookup (xpc-service-name "com.apple.MTLCompilerService" "com.apple.coremedia.videoencoder" "com.apple.coremedia.videodecoder"))
`

// darwinProfile fills the template for one run and returns the text and the -D
// parameters it names. Every param the text names is passed.
func darwinProfile(p *policy) (text string, params []string, err error) {
	var optRoots, anc, reads, noExec, writes, netLines []string
	for _, r := range p.optRoots {
		optRoots = append(optRoots, fmt.Sprintf("(allow file-read* (subpath %q))", r))
	}
	paths := slices.Concat(p.reads, p.readsNoExec, p.writes, p.optRoots, p.pathDirs, []string{p.cwd, p.tmp})
	for _, d := range ancestors(paths...) {
		anc = append(anc, fmt.Sprintf("(allow file-read-metadata (literal %q))", d))
	}
	for _, d := range p.pathDirs {
		anc = append(anc, fmt.Sprintf("(allow file-read-metadata (subpath %q))", d))
	}
	for i, r := range p.reads {
		reads = append(reads, fmt.Sprintf(`(allow file-read* (subpath (param "READ%d")))`, i))
		params = append(params, fmt.Sprintf("READ%d=%s", i, r))
	}
	for i, r := range p.readsNoExec {
		noExec = append(noExec,
			fmt.Sprintf(`(allow file-read* (subpath (param "NOEXEC%d")))`, i),
			fmt.Sprintf(`(deny process-exec* (subpath (param "NOEXEC%d")) (with no-report))`, i))
		params = append(params, fmt.Sprintf("NOEXEC%d=%s", i, r))
	}
	for i, w := range p.writes {
		writes = append(writes,
			fmt.Sprintf(`(allow file-read* file-write* (subpath (param "WRITE%d")))`, i),
			fmt.Sprintf(`(allow network-outbound (subpath (param "WRITE%d")))`, i))
		params = append(params, fmt.Sprintf("WRITE%d=%s", i, w))
	}
	// The caller's own spelling of a granted path, and each symlink among its
	// ancestors, is read as a literal on the link: following a link needs read on it.
	for _, l := range linkLiterals(p.linkSpellings) {
		reads = append(reads, fmt.Sprintf("(allow file-read* (literal %q))", l))
	}
	params = append(params, "HOME="+p.home)
	if !p.netDeny {
		// DNS is the mDNSResponder socket, so it goes with the network and goes with --net-deny.
		netLines = append(netLines, `(allow network-outbound (remote ip) (literal "/private/var/run/mDNSResponder"))`)
	}
	// --net-allow: one loopback port back up. sandbox-exec takes only the literal
	// "localhost" as the host here, and it admits both 127.0.0.1 and ::1.
	for _, hp := range p.netAllow {
		if _, port, err := net.SplitHostPort(hp); err == nil {
			netLines = append(netLines, fmt.Sprintf(`(allow network-outbound (remote ip "localhost:%s"))`, port))
		}
	}
	fill := map[string]string{
		"@@OPTROOTS@@": strings.Join(optRoots, "\n"), "@@ANCESTORS@@": strings.Join(anc, "\n"),
		"@@READS@@": strings.Join(reads, "\n"), "@@READSNOEXEC@@": strings.Join(noExec, "\n"),
		"@@WRITES@@": strings.Join(writes, "\n"), "@@NET@@": strings.Join(netLines, "\n"),
	}
	var out strings.Builder
	used := 0
	for _, line := range strings.Split(darwinTemplate, "\n") {
		if body, ok := fill[line]; ok {
			used++
			if body != "" {
				out.WriteString(body + "\n")
			}
			continue
		}
		out.WriteString(line + "\n")
	}
	if used != len(fill) {
		return "", nil, fmt.Errorf("darwin.sb.tmpl and the generator have diverged: %d of %d markers found", used, len(fill))
	}
	text = out.String()
	if p.gpu {
		text += metalGrants
	}
	return text, params, nil
}

// linkLiterals is each spelling and every symlink among its ancestors, once, in order.
func linkLiterals(spellings []string) []string {
	var out []string
	for _, s := range spellings {
		for d := parentOf(s); d != s && parentOf(d) != d; d = parentOf(d) {
			if isLink(d) && !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
