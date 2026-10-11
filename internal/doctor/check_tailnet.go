package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// tailnetTimeout bounds the tailscale and nova-config calls one run makes.
const tailnetTimeout = 10 * time.Second

// tailnetDoc is the documented step a fix line names for the tailnet
// (docs/SETUP.md, dep-tailnet-b.w4).
const tailnetDoc = "docs/SETUP.md, dep-tailnet-b.w4"

func init() {
	Default.Register(Check{
		Name:       "tailnet",
		Dependency: "the tailnet",
		Fleet:      true,
		Run:        checkTailnet,
	})
}

// tailnetStatus is the part of `tailscale status --json` the check reads: the
// backend state, this machine (Self) and the other machines (Peer), keyed by
// address. A node's DNS name -- the first label of it -- is the nova machine
// name (docs/SPEC-CONFIG.md, "The machine kind"; pkg/config/self.go).
type tailnetStatus struct {
	BackendState string                 `json:"BackendState"`
	Self         tailnetNode            `json:"Self"`
	Peer         map[string]tailnetNode `json:"Peer"`
}

// tailnetNode is one node's name fields from `tailscale status --json`.
type tailnetNode struct {
	DNSName  string `json:"DNSName"`
	HostName string `json:"HostName"`
}

// checkTailnet covers the tailnet a fleet leans on: tailscale is installed and
// the backend is running, this machine names itself on it, and every machine of
// the nova-config inventory is named on the tailnet so it can be reached by name
// (docs/SPEC-DOCTOR.md, the checks). Fleet marks it: --local skips it, because a
// single-machine setup has no fleet to reach.
func checkTailnet(ctx context.Context, env Env) Result {
	ctx, cancel := context.WithTimeout(ctx, tailnetTimeout)
	defer cancel()

	out, err := env.Exec(ctx, "tailscale", "status", "--json")
	if err != nil {
		return Result{Status: Fail,
			Evidence: "tailscale is not installed or did not answer",
			Fix:      "install tailscale (https://tailscale.com/download) and run `tailscale up` (" + tailnetDoc + ")"}
	}
	var st tailnetStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		return Result{Status: Fail,
			Evidence: "tailscale did not answer JSON: " + oneLine(out),
			Fix:      "run `tailscale up` and confirm `tailscale status --json` prints a state (" + tailnetDoc + ")"}
	}
	if st.BackendState != "Running" {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("the tailnet is not up: BackendState=%s", st.BackendState),
			Fix:      "run `tailscale up` and follow its login prompt (" + tailnetDoc + ")"}
	}
	self := tailnetName(st.Self)
	if self == "" {
		return Result{Status: Fail,
			Evidence: "the tailnet is up but this machine has no name on it",
			Fix:      "run `tailscale up` and confirm this machine is logged in (its DNS name is set) (" + tailnetDoc + ")"}
	}

	// A machine answers by name when it is a self or peer on the tailnet: its
	// first label resolves over Magic DNS and the node is in the status.
	names := map[string]bool{self: true}
	for _, p := range st.Peer {
		if n := tailnetName(p); n != "" {
			names[n] = true
		}
	}

	// The fleet's inventory is what nova-config has applied; a machine in it but
	// absent from the tailnet is the peer the check names.
	inv, err := inventoryNames(ctx, env)
	if err != nil {
		return Result{Status: Fail,
			Evidence: "the machine inventory could not be read: " + err.Error(),
			Fix:      "source the seat file (`set -a; . ~/nova/seat.env; set +a`), then run nova-doctor run again (" + tailnetDoc + ")"}
	}
	var missing []string
	for _, m := range inv {
		if !names[m] {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		word := "machines"
		if len(missing) == 1 {
			word = "machine"
		}
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("%d %s not on the tailnet: %s", len(missing), word, strings.Join(missing, ", ")),
			Fix:      "run `tailscale up` on " + missing[0] + " and confirm it is in the same tailnet as this machine (" + tailnetDoc + ")"}
	}
	return Result{Status: OK,
		Evidence: fmt.Sprintf("tailscale is up as %s; all %d inventory machines answer on the tailnet", self, len(inv))}
}

// tailnetName is the nova machine name of a tailnet node: the first label of its
// DNS name, else its host name, lower-cased; "" when the node carries neither.
func tailnetName(n tailnetNode) string {
	if n := firstLabel(n.DNSName); n != "" {
		return n
	}
	return firstLabel(n.HostName)
}

// firstLabel is the lower-cased first label of a dotted name; "" when there is none.
func firstLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

// inventoryNames is the machine rows nova-config has applied, read from
// `nova-config machine list` (docs/SPEC-CONFIG.md, "Lines"): one
// `MACHINE name=<n> ...` line per machine, plus a `CONFIG LIST` tally.
func inventoryNames(ctx context.Context, env Env) ([]string, error) {
	out, err := env.Exec(ctx, "nova-config", "machine", "list")
	if err != nil {
		return nil, fmt.Errorf("nova-config machine list: %w", err)
	}
	var names []string
	const prefix = "MACHINE name="
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, prefix) {
			continue
		}
		rest := strings.TrimPrefix(l, prefix)
		name := rest
		if i := strings.IndexByte(rest, ' '); i >= 0 {
			name = rest[:i]
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}
