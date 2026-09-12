package sandbox

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/profiles"
)

// The five markers of profiles/darwin.sb.tmpl. A line whose WHOLE content is one of
// these is replaced; the template's own header says what each becomes, and this file is
// the only thing that fills them. Rule 15: the policy is generated, never hand-edited,
// and the tool never accepts a caller-supplied profile file.
const (
	markerOptRoots  = "@@OPTROOTS@@"
	markerAncestors = "@@ANCESTORS@@"
	markerReads     = "@@READS@@"
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
	ancestorOf := append(append([]string{}, p.Reads...), p.Writes...)
	ancestorOf = append(ancestorOf, p.Cwd, p.Tmp)
	var ancestors []string
	for _, d := range Ancestors(ancestorOf...) {
		ancestors = append(ancestors, fmt.Sprintf("(allow file-read-metadata (literal %q))", d))
	}

	var reads, writes []string
	for i, r := range p.Reads {
		name := fmt.Sprintf("READ%d", i)
		reads = append(reads, fmt.Sprintf("(allow file-read* (subpath (param %q)))", name))
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
	// The template names (param "HOME") unconditionally, so HOME is always passed. Build
	// has already refused a HOME outside every --write (rule 9), so this grants nothing
	// the WRITEn grants did not already grant: it is the profile stating the requirement.
	params = append(params, "HOME="+p.Home)

	// Rule 7: IP only, never (allow network*), which grants every unix-domain socket on
	// the machine as well — including an inherited SSH agent's. Inbound is not granted
	// at all unless the caller asks with --net-listen: a job that does not listen cannot
	// be listened to. Under --net-deny the marker is emitted empty.
	var net string
	if !p.NetDeny {
		// The mDNSResponder socket is the DNS grant (rule 7): macOS resolves names over
		// that unix socket, so IP-only outbound without it is a wall with a network and
		// no name resolution — measured rc=6/000 without, 200 with. It sits inside this
		// branch so that --net-deny takes the resolver away with the network.
		lines := []string{`(allow network-outbound (remote ip) (literal "/private/var/run/mDNSResponder"))`}
		if p.NetListen {
			lines = append(lines, `(allow network-inbound (local ip))`)
		}
		net = strings.Join(lines, "\n")
	}

	filled := map[string]string{
		markerOptRoots:  strings.Join(optRoots, "\n"),
		markerAncestors: strings.Join(ancestors, "\n"),
		markerReads:     strings.Join(reads, "\n"),
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
	return out.String(), params, nil
}
