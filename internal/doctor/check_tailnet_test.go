package doctor

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// tailnetJSON builds a `tailscale status --json` body: a backend in state, this
// machine named self, and peers named by peers. Each node's DNS name is
// <n>.test.ts.net. so its first label is the nova machine name (docs/SPEC-CONFIG.md,
// "The machine kind").
func tailnetJSON(state, self string, peers ...string) string {
	node := func(n string) string {
		return fmt.Sprintf(`{"DNSName":"%s.test.ts.net.","HostName":"%s"}`, n, n)
	}
	peerMap := ""
	for i, p := range peers {
		if i > 0 {
			peerMap += ","
		}
		peerMap += fmt.Sprintf(`"100.0.0.%d":%s`, i+1, node(p))
	}
	return fmt.Sprintf(`{"BackendState":%q,"Self":%s,"Peer":{%s}}`, state, node(self), peerMap)
}

// novaConfigList builds `nova-config machine list` output for the given machines:
// one `MACHINE name=<n> ...` line each, then the `CONFIG LIST` tally.
func novaConfigList(names ...string) string {
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "MACHINE name=%s user=u seat=coordinator slots=1 runners=0 width=- tla=false note=-\n", n)
	}
	fmt.Fprintf(&b, "CONFIG LIST kind=machine rows=%d\n", len(names))
	return b.String()
}

// tailnetExecs returns a fake exec that answers the tailscale status call with ts
// and the nova-config list call with inv.
func tailnetExecs(ts, inv string) func(string, ...string) (string, error) {
	return func(name string, args ...string) (string, error) {
		switch {
		case name == "tailscale" && len(args) == 2 && args[0] == "status" && args[1] == "--json":
			return ts, nil
		case name == "nova-config" && len(args) == 2 && args[0] == "machine" && args[1] == "list":
			return inv, nil
		}
		return "", fmt.Errorf("unexpected exec: %s %v", name, args)
	}
}

// TestDoctorTailnetCheckNamesTheMissingPeer pins the tailnet check (docs/SPEC-DOCTOR.md):
// tailscale is installed and running, this machine is named on it, and every machine of
// the nova-config inventory is named on the tailnet. A machine that is missing is named in
// the evidence, and --local skips the check as a fleet-only dependency. No test opens a
// socket or a service: every program is faked behind Env.
func TestDoctorTailnetCheckNamesTheMissingPeer(t *testing.T) {
	t.Parallel()
	t.Run("tailscale not installed is a fail with the install fix", func(t *testing.T) {
		t.Parallel()
		env := fakeEnv{exec: func(name string, args ...string) (string, error) {
			return "", fmt.Errorf("executable file not found in $PATH")
		}}
		r := checkTailnet(context.Background(), env)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Fix, "tailscale")
	})
	t.Run("a tailnet that is not running is a fail naming the state", func(t *testing.T) {
		t.Parallel()
		env := fakeEnv{exec: tailnetExecs(`{"BackendState":"NeedsLogin"}`, "")}
		r := checkTailnet(context.Background(), env)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "NeedsLogin")
		assert.Contains(t, r.Fix, "tailscale up")
	})
	t.Run("this machine has no tailnet name is a fail", func(t *testing.T) {
		t.Parallel()
		env := fakeEnv{exec: tailnetExecs(tailnetJSON("Running", ""), novaConfigList())}
		r := checkTailnet(context.Background(), env)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Fix, "tailscale up")
	})
	t.Run("a peer missing from the tailnet is named and given its fix", func(t *testing.T) {
		t.Parallel()
		// the inventory has m1, m2, m3; the tailnet knows only m1 (self) and m2
		env := fakeEnv{exec: tailnetExecs(
			tailnetJSON("Running", "m1", "m2"),
			novaConfigList("m1", "m2", "m3"),
		)}
		r := checkTailnet(context.Background(), env)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "m3")
		assert.NotContains(t, r.Evidence, "m1")
		assert.NotContains(t, r.Evidence, "m2")
		assert.Contains(t, r.Fix, "m3")
	})
	t.Run("two missing peers are both named", func(t *testing.T) {
		t.Parallel()
		env := fakeEnv{exec: tailnetExecs(
			tailnetJSON("Running", "m1"),
			novaConfigList("m1", "m2", "m3"),
		)}
		r := checkTailnet(context.Background(), env)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "m2")
		assert.Contains(t, r.Evidence, "m3")
	})
	t.Run("every inventory machine on the tailnet is ok", func(t *testing.T) {
		t.Parallel()
		env := fakeEnv{exec: tailnetExecs(
			tailnetJSON("Running", "m1", "m2", "m3"),
			novaConfigList("m1", "m2", "m3"),
		)}
		r := checkTailnet(context.Background(), env)
		assert.Equal(t, OK, r.Status, r)
		assert.Empty(t, r.Fix)
	})
	t.Run("an inventory that cannot be read is a fail", func(t *testing.T) {
		t.Parallel()
		env := fakeEnv{exec: func(name string, args ...string) (string, error) {
			if name == "tailscale" && len(args) == 2 && args[0] == "status" && args[1] == "--json" {
				return tailnetJSON("Running", "m1", "m2"), nil
			}
			return "", fmt.Errorf("connection refused")
		}}
		r := checkTailnet(context.Background(), env)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "inventory")
	})
	t.Run("under --local the check is skipped and says why", func(t *testing.T) {
		t.Parallel()
		r := NewRegistry()
		r.Register(Default.checks["tailnet"])
		var out, errs bytes.Buffer
		code := Main(r, fakeEnv{}, "", []string{"--local"}, strings.NewReader(""), &out, &errs)
		assert.Equal(t, 0, code)
		assert.Contains(t, out.String(), "DOCTOR local skipped=tailnet")
	})
}
