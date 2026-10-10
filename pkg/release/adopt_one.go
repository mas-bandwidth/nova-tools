package release

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// OneMachine adopts one release onto one machine at a time, through adopt
// itself: the sprint's tick hands it a fleet member back from down
// (docs/SPEC-SPRINT.md section 5, "Back from down: adopt the latest";
// docs/SPEC-RELEASE.md, "Adopting one machine"). An adoption is three runs of
// adopt with a machine list of that machine alone: --dry-run, which reads the
// version it has installed (installed= of RELEASE WOULD ADOPT); the adopt; and
// --dry-run again, which reads the version back. The tick calls Start and
// Adoption from inside a plan, so the runs go on beside it, and one machine
// has at most one adoption in flight.
type OneMachine struct {
	// Version is the release to adopt: the one the coordinator's machine runs.
	Version string
	// Flags are adopt's flags but --machines, --version and --dry-run, as the
	// coordinator would type them (--ssh, --from, --bin, --dest, the stage's
	// digest, --no-certify or the certification's three).
	Flags []string
	// Dir is where the one-machine lists are written; "" is os.TempDir.
	Dir string
	// Deps are adopt's (Run); the zero value is the real ssh and clock.
	Deps Deps

	mu   sync.Mutex
	runs map[string]*OneRun
}

// OneRun is a machine's adoption of one episode: running until it ends, the
// version installed before and read back after, and why it failed.
type OneRun struct {
	Episode string
	Running bool
	From    string
	To      string
	Err     string
}

// wouldLine reads the installed version of the machine off a dry run's line.
var wouldLine = regexp.MustCompile(`RELEASE WOULD ADOPT machine=(\S+) version=\S+ installed=(\S+)`)

// Start begins the adoption of version on machine for the episode, beside the
// caller. It is false, and starts nothing, while an adoption of that machine is
// in flight, or when the machine already has a run of the episode.
func (o *OneMachine) Start(machine, version, episode string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.runs == nil {
		o.runs = map[string]*OneRun{}
	}
	if r := o.runs[machine]; r != nil && (r.Running || r.Episode == episode) {
		return false
	}
	o.runs[machine] = &OneRun{Episode: episode, Running: true}
	go func() {
		from, to, err := o.adopt(machine, version)
		o.mu.Lock()
		defer o.mu.Unlock()
		r := o.runs[machine]
		r.Running, r.From, r.To = false, from, to
		if err != nil {
			r.Err = err.Error()
		}
	}()
	return true
}

// Run is the machine's adoption of the episode, false when there is none.
func (o *OneMachine) Run(machine, episode string) (OneRun, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	r := o.runs[machine]
	if r == nil || r.Episode != episode {
		return OneRun{}, false
	}
	return *r, true
}

// adopt is the three runs: the version installed before, the adopt, and the
// version read back.
func (o *OneMachine) adopt(machine, version string) (from, to string, err error) {
	if ValidVersion(version) != nil {
		return "", "", fmt.Errorf("no release to adopt: the coordinator's machine runs %q", version)
	}
	if !machineName.MatchString(machine) {
		return "", "", fmt.Errorf("%q is not a machine name adopt takes", machine)
	}
	f, err := os.CreateTemp(o.Dir, "adopt-one-*.machines")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = os.Remove(f.Name()) }() // ignored: a list left behind names one machine and nothing else
	_, werr := fmt.Fprintln(f, machine)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", "", werr
	}
	list := filepath.Clean(f.Name())
	from, err = o.installed(machine, version, list)
	if err != nil {
		return "", "", err
	}
	out, errs, code := o.run(o.args(version, list))
	if code != 0 || !strings.Contains(out, "RELEASE ADOPTED machine="+machine+" ") {
		return from, "", fmt.Errorf("adopt exited %d: %s", code, lastLine(errs, out))
	}
	to, err = o.installed(machine, version, list)
	return from, to, err
}

// installed is the version the machine has installed, by a dry run.
func (o *OneMachine) installed(machine, version, list string) (string, error) {
	out, errs, code := o.run(append(o.args(version, list), "--dry-run"))
	for _, m := range wouldLine.FindAllStringSubmatch(out, -1) {
		if m[1] == machine {
			if m[2] == "-" {
				return "", nil // nothing installed there yet
			}
			return m[2], nil
		}
	}
	return "", fmt.Errorf("adopt --dry-run exited %d and read no installed version: %s", code, lastLine(errs, out))
}

func (o *OneMachine) args(version, list string) []string {
	return append(append([]string{"adopt"}, o.Flags...), "--machines", list, "--version", version)
}

func (o *OneMachine) run(args []string) (out, errs string, code int) {
	var ob, eb bytes.Buffer
	code = Run("release", args, &ob, &eb, o.Deps)
	return ob.String(), eb.String(), code
}

// lastLine is the last line of the first of the outputs that says anything.
func lastLine(outs ...string) string {
	for _, s := range outs {
		if s = strings.TrimSpace(s); s != "" {
			lines := strings.Split(s, "\n")
			return strings.TrimSpace(lines[len(lines)-1])
		}
	}
	return "it said nothing"
}
