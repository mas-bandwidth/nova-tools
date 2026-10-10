package darwincheck

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// The hand filler. It fills profiles/darwin.sb.tmpl for a scratch write set
// WITHOUT asking the tool that generates the same profile (pkg/sandbox):
// the point of the check is one text filled two ways, so that a drift between
// the generator and the hand is a named FAIL here rather than a job that dies
// in its first second. The filler therefore reads no code of pkg/sandbox;
// it follows the template's own header, which says what each marker becomes.

// The markers of the template, each a whole line.
const (
	markerOptRoots    = "@@OPTROOTS@@"
	markerAncestors   = "@@ANCESTORS@@"
	markerReads       = "@@READS@@"
	markerReadsNoExec = "@@READSNOEXEC@@"
	markerWrites      = "@@WRITES@@"
	markerNet         = "@@NET@@"
)

// The grants the filled markers carry.
const (
	readsGrant = `(allow file-read* (subpath (param "READ0")))`
	// The second line is the ONLY unix-domain socket reach the profile grants: a
	// socket is a file, and one under the write set is the job's own.
	writesGrant = `(allow file-read* file-write* (subpath (param "WRITE0")))
(allow network-outbound (subpath (param "WRITE0")))`
	// IP only: (allow network*) would grant every unix-domain socket on the
	// machine, the inherited SSH agent's among them. The mDNSResponder literal is
	// the DNS grant: macOS resolves names over that unix socket, and IP-only
	// outbound without it is a wall with no DNS.
	netGrant = `(allow network-outbound (remote ip) (literal "` + mdnsSocket + `"))`
	// mdnsSocket is the resolver's socket; the control run removes exactly this
	// literal to prove the DNS check passes because of it.
	mdnsSocket = "/private/var/run/mDNSResponder"
)

// xcodeSelectLinks are the places xcode-select keeps its link to the selected
// developer directory.
var xcodeSelectLinks = []string{"/var/db/xcode_select_link", "/private/var/db/xcode_select_link"}

// fsProbe is what the filler asks the file system: the optional roots exist,
// and where xcode-select points.
type fsProbe interface {
	IsDir(path string) bool
	// Readlink is the target of the symlink at path; false when path is not a
	// symlink or cannot be read.
	Readlink(path string) (string, bool)
	// Real is the path with every link followed; path itself when it cannot be.
	Real(path string) string
}

// optionalRoots are the documented darwin optional roots and nothing else: a
// check that grants a root the spec's table does not name tests a broader policy
// than the document. A root is skipped when absent, and when the template
// already grants it as a subpath, matching pkg/sandbox.OptionalRoots
// (including /Library: CommandLineTools lives there). gitDir is the directory of
// the git the check runs, which is an optional root when it is under neither.
//
// It follows xcode_select_link the way the generator does: CommandLineTools sits
// under /Library; Xcode.app/Contents does not, and it is Contents, not
// Contents/Developer, that is granted, because the shims read Info.plist and
// SharedFrameworks.
func optionalRoots(fs fsProbe, gitDir string) []string {
	var roots []string
	seen := map[string]bool{}
	add := func(r string) {
		if !fs.IsDir(r) || templateGrants(r) || seen[r] {
			return
		}
		seen[r] = true
		roots = append(roots, r)
	}
	for _, r := range []string{"/opt/homebrew", "/opt/local", gitDir} {
		add(r)
	}
	for _, link := range xcodeSelectLinks {
		target, ok := fs.Readlink(link)
		if !ok || target == "" {
			continue
		}
		if !strings.HasPrefix(target, "/") {
			target = filepath.Join(filepath.Dir(link), target)
		}
		if fs.IsDir(target) {
			target = fs.Real(target)
		}
		if strings.HasSuffix(target, "/Contents/Developer") {
			target = strings.TrimSuffix(target, "/Developer")
		}
		add(target)
	}
	return roots
}

// templateGrants reports whether the template already grants r as a subpath, so
// that adding it would be a second grant of the same tree.
func templateGrants(r string) bool {
	for _, p := range []string{"/usr", "/System", "/Library", "/private/etc", "/private/var/select", "/dev"} {
		if r == p || strings.HasPrefix(r, p+"/") {
			return true
		}
	}
	return r == "/bin" || r == "/sbin"
}

// ancestorsOf is every proper ancestor directory of path, nearest first, "/"
// excluded.
func ancestorsOf(path string) []string {
	var out []string
	for d := filepath.Dir(path); d != "/" && d != "."; d = filepath.Dir(d) {
		out = append(out, d)
	}
	return out
}

// fillInput is what a filled profile is made for: the write set, the read set,
// and the optional roots found on this machine.
type fillInput struct {
	write, ref string
	optRoots   []string
}

// fillTemplate replaces the marker lines of the template. A line whose whole
// content is a marker is replaced; every other line passes through. The
// read-noexec marker is left empty, because this check names no --read-noexec.
//
// The template is read as a shell's `while read -r` reads it: a final line with
// no newline is not a line, so the template must end with one.
func fillTemplate(tmpl string, in fillInput) string {
	var optroots strings.Builder
	for _, r := range in.optRoots {
		optroots.WriteString(`(allow file-read* (subpath "` + r + `"))` + "\n")
	}
	anc := map[string]bool{}
	for _, p := range append([]string{in.write, in.ref}, in.optRoots...) {
		for _, a := range ancestorsOf(p) {
			anc[a] = true
		}
	}
	var ancestors strings.Builder
	for _, d := range slices.Sorted(maps.Keys(anc)) {
		ancestors.WriteString(`(allow file-read-metadata (literal "` + d + `"))` + "\n")
	}

	var out strings.Builder
	rest := tmpl
	for {
		line, after, found := strings.Cut(rest, "\n")
		if !found {
			break
		}
		rest = after
		switch line {
		case markerOptRoots:
			out.WriteString(optroots.String())
		case markerAncestors:
			out.WriteString(ancestors.String())
		case markerReads:
			out.WriteString(readsGrant + "\n")
		case markerReadsNoExec:
		case markerWrites:
			out.WriteString(writesGrant + "\n")
		case markerNet:
			out.WriteString(netGrant + "\n")
		default:
			out.WriteString(line + "\n")
		}
	}
	return out.String()
}

// agentVariable reports whether an environment variable is one of the exact
// scrub set, by name: SSH_AUTH_SOCK, SSH_AGENT_*, GPG_AGENT_INFO, *_AGENT_PID,
// *_AGENT_INFO, *_AGENT_SOCK. It is NOT "every name containing AGENT": that width
// was measured to drop AI_AGENT and CLAUDE_AGENT_SDK_VERSION, which say what is
// running the job and address nothing. The tool's own scrub is asserted in
// pkg/sandbox; this filter is the check's, so it can only agree with itself.
func agentVariable(name string) bool {
	switch {
	case name == "SSH_AUTH_SOCK", name == "GPG_AGENT_INFO", strings.HasPrefix(name, "SSH_AGENT_"):
		return true
	}
	return strings.HasSuffix(name, "_AGENT_PID") || strings.HasSuffix(name, "_AGENT_INFO") || strings.HasSuffix(name, "_AGENT_SOCK")
}

// childEnv is the caller's environment with the agent scrub set removed.
func childEnv(caller []string) []string {
	var out []string
	for _, kv := range caller {
		name, _, _ := strings.Cut(kv, "=")
		if !agentVariable(name) {
			out = append(out, kv)
		}
	}
	return out
}
