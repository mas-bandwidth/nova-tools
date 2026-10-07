package sandbox

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/profiles"
)

// The six markers of profiles/darwin.sb.tmpl. A line whose WHOLE content is one of
// these is replaced; the template's own header says what each becomes, and this file is
// the only thing that fills them: the policy is generated, never hand-edited,
// and the tool never accepts a caller-supplied profile file.
const (
	markerOptRoots  = "@@OPTROOTS@@"
	markerAncestors = "@@ANCESTORS@@"
	markerReads     = "@@READS@@"
	markerNoExec    = "@@READSNOEXEC@@"
	markerWrites    = "@@WRITES@@"
	markerNet       = "@@NET@@"
)

// DarwinProfile fills the template for ONE run and returns the profile text and the -D
// parameters that must accompany it. Caller paths never enter the text as data: they
// arrive as parameters and are read back as (param "READn"), (param "WRITEn") and
// (param "HOME"), so a directory name cannot rewrite the policy. The one place a path
// does enter the text is the ancestor literals, which is why Build refuses a path
// carrying an SBPL metacharacter.
//
// Every param the filled profile names MUST be passed: an unfilled (param ...) is
// "invalid data type of path filter; expected pattern, got boolean" at exit 65, which is
// a broken run and not a weaker wall. The params returned here are exactly the params
// the returned text names.
func DarwinProfile(p *Policy) (text string, params []string, err error) {
	if p == nil || len(p.Writes) == 0 {
		return "", nil, fmt.Errorf("a policy with no --write has no profile")
	}

	var optRoots []string
	for _, r := range p.OptRoots {
		if bad := badPathText(r); bad != "" {
			return "", nil, fmt.Errorf("root %s %s", r, bad)
		}
		optRoots = append(optRoots, fmt.Sprintf("(allow file-read* (subpath %q))", r))
	}

	// Without these every absolute path into the write set fails at its leading
	// components: git init is "cannot mkdir: Operation not permitted" and mkdir -p dies
	// at /private. file-read-metadata is stat(2) only — listing an ancestor stays denied.
	var ancestors []string
	for _, d := range Ancestors(p.ancestorPaths()...) {
		ancestors = append(ancestors, fmt.Sprintf("(allow file-read-metadata (literal %q))", d))
	}
	for _, d := range p.PathDirs {
		if bad := badPathText(d); bad != "" {
			return "", nil, fmt.Errorf("path dir %s %s", d, bad)
		}
		ancestors = append(ancestors, fmt.Sprintf("(allow file-read-metadata (subpath %q))", d))
	}

	var reads, noExec, writes []string
	for i, r := range p.Reads {
		name := fmt.Sprintf("READ%d", i)
		reads = append(reads, fmt.Sprintf("(allow file-read* (subpath (param %q)))", name))
		params = append(params, name+"="+r)
	}
	// --read-noexec: READABLE AND NOT EXECUTABLE. This profile grants
	// (allow process-exec* process-fork) once and globally, so on darwin readable IS
	// executable unless the exec is taken back -- and SBPL's last matching rule wins, so
	// the deny has to be emitted, and has to be emitted after the global grant and after
	// the @@READS@@ block. The template puts the marker there and says why.
	for i, r := range p.ReadsNoExec {
		name := fmt.Sprintf("NOEXEC%d", i)
		noExec = append(noExec,
			fmt.Sprintf("(allow file-read* (subpath (param %q)))", name),
			fmt.Sprintf("(deny process-exec* (subpath (param %q)))", name))
		params = append(params, name+"="+r)
	}
	for i, w := range p.Writes {
		name := fmt.Sprintf("WRITE%d", i)
		writes = append(writes, fmt.Sprintf("(allow file-read* file-write* (subpath (param %q)))", name))
		// The ONLY unix-domain socket reach this policy grants: a socket is a file, and
		// a socket under the write set is the job's own. The SSH agent's socket under
		// /private/tmp, the launchd Listeners socket and a sibling job's socket are all
		// denied — which (allow network*) would not have been.
		writes = append(writes, fmt.Sprintf("(allow network-outbound (subpath (param %q)))", name))
		params = append(params, name+"="+w)
	}
	// The caller's own spelling of a granted path, and each symlink component of it, is
	// granted as a literal on the LINK, as the template does for /etc /tmp /var
	// (docs/SPEC-SANDBOX.md rule 5; security#67 finding 1). A literal reads the link and
	// lists nothing: the contents stay behind the READn/WRITEn grants.
	for _, l := range linkLiterals(p.LinkSpellings) {
		if bad := badPathText(l); bad != "" {
			return "", nil, fmt.Errorf("link spelling %s %s", l, bad)
		}
		reads = append(reads, fmt.Sprintf("(allow file-read* (literal %q))", l))
	}
	// The template names (param "HOME") unconditionally, so HOME is always passed. Build
	// has already refused a HOME outside every --write, so this grants nothing
	// the WRITEn grants did not already grant: it is the profile stating the requirement.
	params = append(params, "HOME="+p.Home)

	// Network access is limited to IP; (allow network*) would also grant every unix-domain socket on
	// the machine as well — including an inherited SSH agent's. Inbound is not granted
	// at all unless the caller asks with --net-listen: a job that does not listen cannot
	// be listened to. Under --net-deny the marker is emitted empty, apart from any
	// --net-allow grants, which the caller explicitly names.
	var lines []string
	if !p.NetDeny {
		// The mDNSResponder socket is needed for DNS because macOS resolves names over
		// that unix socket, so IP-only outbound without it is a wall with a network and
		// no name resolution — measured rc=6/000 without, 200 with. It sits inside this
		// branch so that --net-deny takes the resolver away with the network.
		lines = append(lines, `(allow network-outbound (remote ip) (literal "/private/var/run/mDNSResponder"))`)
		if p.NetListen {
			lines = append(lines, `(allow network-inbound (local ip))`)
		}
	}
	// --net-allow opens one loopback port back up by name for local-model providers
	// (ollama on 127.0.0.1) that bare (remote ip) does not reach. It is an
	// explicit exception the caller named, so it is emitted even under --net-deny.
	//
	// THE FORM IS (remote ip "localhost:PORT"), NEVER (local ip (host ..) (port ..))
	// (measured on darwin 27.2, sandbox-exec -f): the nested form Build's own comment used
	// to cite is not one sandbox-exec's SBPL compiler accepts at all -- it aborts the whole
	// profile with `unbound variable: host`, exit 65, before the child ever runs, which is
	// the wall failing SHUT in the wrong way: not a denial but a refusal to compile. The
	// `remote ip` form's own host slot additionally accepts only the literal "localhost" or
	// "*", never a numeric address, which is why Build (policy.go) confirms the caller's
	// host is loopback and this always emits the literal "localhost" for it: measured,
	// "localhost:PORT" admits both a 127.0.0.1 and a ::1 listener on that port, and a
	// numeric host here (`(remote ip "127.0.0.1:PORT")`) is the same compile abort under a
	// different message (`host must be * or localhost in network address`).
	// A lane's wall (LaneProfile): a denied network with its TCP ports opened outbound to
	// any host, and the name resolver with them, since a port reached by name needs it.
	// SBPL has no host filter but localhost and *, so the grant is the port, never the
	// host; measured on darwin 27.2 with sandbox-exec -p: curl to github.com:443 answered
	// 200 and to example.com:80 failed to connect (rc=7).
	if p.NetDeny && len(p.NetPorts) > 0 {
		lines = append(lines, `(allow network-outbound (literal "/private/var/run/mDNSResponder"))`)
		for _, port := range p.NetPorts {
			lines = append(lines, fmt.Sprintf(`(allow network-outbound (remote tcp "*:%d"))`, port))
		}
	}
	for _, hp := range p.NetAllow {
		_, port, err := net.SplitHostPort(hp)
		if err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf(`(allow network-outbound (remote ip "localhost:%s"))`, port))
	}
	net := strings.Join(lines, "\n")

	filled := map[string]string{
		markerOptRoots:  strings.Join(optRoots, "\n"),
		markerAncestors: strings.Join(ancestors, "\n"),
		markerReads:     strings.Join(reads, "\n"),
		markerNoExec:    strings.Join(noExec, "\n"),
		markerWrites:    strings.Join(writes, "\n"),
		markerNet:       net,
	}
	var out strings.Builder
	seen := map[string]bool{}
	for _, line := range strings.Split(profiles.DarwinTemplate, "\n") {
		if body, ok := filled[strings.TrimSpace(line)]; ok && strings.TrimSpace(line) == line {
			seen[strings.TrimSpace(line)] = true
			if body != "" {
				out.WriteString(body)
				out.WriteString("\n")
			}
			continue
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	for marker := range filled {
		if !seen[marker] {
			return "", nil, fmt.Errorf("profiles/darwin.sb.tmpl carries no %s line; the template and the generator have diverged", marker)
		}
	}
	// A marker is a LINE, never a substring: the template's own header documents each
	// marker by name, and those lines are comments that stay in the filled profile.
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, ";;") {
			continue
		}
		if _, isMarker := filled[strings.TrimSpace(line)]; isMarker {
			return "", nil, fmt.Errorf("the filled profile still carries the marker line %s", strings.TrimSpace(line))
		}
	}
	// The explicit local GPU capability is recorded, never widened
	// silently. A metal opt-in adds no mach-lookup service and no blanket
	// device grant here: the minimum Metal mechanisms are still unmeasured, so
	// the profile stays closed and the GPU probe classifies the outcome.
	text = out.String()
	// Deletes in every --write root ("deletes-in-every-write-root"): the template's
	// file-write* grant on each --write includes unlink, and no later rule takes it back.
	if p.GPUMode == GPUMetal {
		text += ";; gpu=metal requested: no mach-lookup or device grant added; Metal stays denied until measured\n"
	}
	return text, params, nil
}

// ProfileFriend is the wall profile a nova-friend lane takes when its friend row names
// none (docs/SPEC-SANDBOX.md, buds-in-the-wall-r.w5).
const ProfileFriend = "friend"

// LaneProfiles is every wall profile a lane may name; any other name is refused.
var LaneProfiles = []string{ProfileFriend}

// The deny list of a lane's wall is the coordinator's self: the paths no lane's write
// reaches whatever else its profile grants (her repository at home and on a shared
// volume, and with them their memory/, identity/ and MEMORY-*.md). It is configuration,
// never a name in this code: the friend's `run --deny-self` (LaneProfile.Deny), and a
// lane profile with none is refused, never run with nothing denied. A path starting ~/
// is under the HOME the profile is built with.

// LaneNetPorts is the network a lane's wall opens: TCP 443 (github.com, and the
// harness's own provider) and 22 (the bench hosts over ssh), to any host on darwin,
// where SBPL has no host filter, and to none on linux, where Landlock here has no port
// rule (Input.NetPorts).
var LaneNetPorts = []int{443, 22}

// LaneProfile is the wall a nova-friend lane's children run inside: writes only to the
// friend's working directory, her job directories and her config directory
// (CLAUDE_CONFIG_DIR), never to a Deny path, and the network LaneNetPorts.
type LaneProfile struct {
	Name      string   // one of LaneProfiles; "" is ProfileFriend
	Work      string   // the friend's working directory
	Jobs      []string // her job directories outside Work
	ConfigDir string   // her CLAUDE_CONFIG_DIR, and the HOME of the wall; "" is Work
	Reads     []string // what the harness reads beyond the system roots and its own directory
	Deny      []string // the coordinator's self, never written; at least one
	Home      string   // the HOME the Deny paths starting ~/ are under
}

// DeniedWrites is deny with ~/ made home.
func DeniedWrites(home string, deny []string) []string {
	var out []string
	for _, d := range deny {
		if rest, ok := strings.CutPrefix(d, "~/"); ok {
			if home == "" {
				continue // no home, so no path under it: the absolute ones still stand
			}
			d = filepath.Join(home, rest)
		}
		out = append(out, d)
	}
	return out
}

// Input is the wall's input for one command (argv) run in cwd: the profile's writes, its
// deny list, its network and HOME. A profile that is not a LaneProfiles name, that has
// no working directory, or that denies nothing, is refused.
func (lp LaneProfile) Input(cwd string, argv []string) (Input, error) {
	name := lp.Name
	if name == "" {
		name = ProfileFriend
	}
	if !slices.Contains(LaneProfiles, name) {
		return Input{}, fmt.Errorf("no wall profile %q; the profiles are %s", name, strings.Join(LaneProfiles, ", "))
	}
	if lp.Work == "" {
		return Input{}, fmt.Errorf("the %s profile wants the friend's working directory", name)
	}
	deny := DeniedWrites(lp.Home, lp.Deny)
	if len(deny) == 0 {
		return Input{}, fmt.Errorf("the %s profile denies nothing: name the coordinator's self (nova-friend run --deny-self), a lane never runs with nothing denied", name)
	}
	home := lp.ConfigDir
	if home == "" {
		home = lp.Work
	}
	writes := []string{lp.Work}
	for _, w := range append(append([]string{}, lp.Jobs...), lp.ConfigDir) {
		if w != "" && !slices.Contains(writes, w) {
			writes = append(writes, w)
		}
	}
	in := Input{
		Reads:    lp.Reads,
		Writes:   writes,
		NetDeny:  true,
		NetPorts: LaneNetPorts,
		Deny:     deny,
		Home:     home,
		Argv:     argv,
	}
	// the command runs where it was started when that is inside the wall, else in Work
	if cwd != "" && slices.ContainsFunc(writes, func(w string) bool { return Inside(resolved(cwd), resolved(w)) }) {
		in.Cwd = cwd
	}
	return in, nil
}

// resolved is path through its symlinks, or as it is when they do not resolve.
func resolved(path string) string {
	if got, err := filepath.EvalSymlinks(path); err == nil {
		return got
	}
	return path
}

// linkLiterals is each spelling and every symlink among its proper ancestors, once and in
// order (docs/SPEC-SANDBOX.md rule 5). A spelling that differs from its resolved path is a
// link or goes through one, so the spelling itself is always granted.
func linkLiterals(spellings []string) []string {
	var out []string
	add := func(l string) {
		if !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	for _, s := range spellings {
		for d := filepath.Dir(s); d != s && filepath.Dir(d) != d; d = filepath.Dir(d) {
			if fi, err := os.Lstat(d); err == nil && fi.Mode()&os.ModeSymlink != 0 {
				add(d)
			}
		}
		add(s)
	}
	return out
}
