package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// A machine's name is its tailnet host (docs/SPEC-CONFIG.md, "The machine
// kind"): `ssh <name>` reaches it, and the machine row, the fleet table's
// member and the beat all carry that one name. SelfName is how a process
// learns its own, so no name is ever typed on the machine it names.

// EnvMachine names the machine row a process runs on, an explicit override
// (an empty value counts as unset).
const EnvMachine = "NOVA_MACHINE"

// How SelfName learnt the name.
const (
	SelfEnv      = "env"      // NOVA_MACHINE
	SelfTailnet  = "tailnet"  // the tailnet's own name for this host
	SelfHostname = "hostname" // the first label of the hostname
)

// SelfSource is what SelfName reads, each a seam a test replaces.
type SelfSource struct {
	Getenv   func(string) string
	Hostname func() (string, error)
	// Tailscale returns `tailscale status --json --peers=false`, or
	// ErrNoTailnet when the program is not installed. nil is the same as
	// ErrNoTailnet.
	Tailscale func(ctx context.Context) ([]byte, error)
}

// ErrNoTailnet says no tailnet is present on this host.
var ErrNoTailnet = errors.New("no tailnet on this host")

// SelfName is this machine's name as the config keys it, and how it was
// learnt: NOVA_MACHINE when set (lower-cased, and refused when it is no valid
// machine name), else the first label of the host's name on
// the tailnet when a tailnet is present and running, else the first label of
// the hostname. Names are lower-case. It refuses when none of the three
// yields a name. It never reads the inventory: whether the name is a machine
// row is the caller's check (machine self --check).
func SelfName(ctx context.Context, src SelfSource) (name, how string, err error) {
	if v := src.Getenv(EnvMachine); v != "" {
		n := strings.ToLower(v)
		if err := ValidateName(n); err != nil {
			return "", "", fmt.Errorf("%s=%q is no machine name: %v", EnvMachine, v, err)
		}
		return n, SelfEnv, nil
	}
	if src.Tailscale != nil {
		raw, terr := src.Tailscale(ctx)
		switch {
		case terr == nil:
			if n, ok := tailnetName(raw); ok {
				return n, SelfTailnet, nil
			}
		case !errors.Is(terr, ErrNoTailnet):
			// a tailnet that would not answer is no tailnet: the hostname
			// stands, and the caller's --check says whether it is a row
		}
	}
	if src.Hostname != nil {
		if h, herr := src.Hostname(); herr == nil {
			if n := firstLabel(h); n != "" {
				return n, SelfHostname, nil
			}
		}
	}
	return "", "", fmt.Errorf("this machine's name could not be resolved: no %s, no running tailnet and no hostname", EnvMachine)
}

// tailnetName is the machine's own name from `tailscale status --json`: the
// first label of Self.DNSName, else Self.HostName, when the backend is
// running. ok is false for anything else (a tailnet that is stopped, needs a
// login, or is not the JSON).
func tailnetName(raw []byte) (string, bool) {
	var st struct {
		BackendState string
		Self         struct {
			DNSName  string
			HostName string
		}
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return "", false
	}
	if st.BackendState != "Running" {
		return "", false
	}
	if n := firstLabel(st.Self.DNSName); n != "" {
		return n, true
	}
	if n := firstLabel(st.Self.HostName); n != "" {
		return n, true
	}
	return "", false
}

// firstLabel is the lower-cased first label of a dotted host name.
func firstLabel(host string) string {
	host = strings.TrimSpace(host)
	label, _, _ := strings.Cut(host, ".")
	return strings.ToLower(label)
}

// TailscaleStatus runs `tailscale status --json --peers=false` when the
// program is installed, bounded to 5 s, and ErrNoTailnet when it is not: it
// shells out only where there is a tailnet to ask.
func TailscaleStatus(ctx context.Context) ([]byte, error) {
	path, err := exec.LookPath("tailscale")
	if err != nil {
		return nil, ErrNoTailnet
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, "status", "--json", "--peers=false").Output()
}
