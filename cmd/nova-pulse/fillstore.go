package main

// The capacity probe the fleet runs on: one line of shell on the bench that asks the
// bench's OWN slot store how many more cards its owner may take (#1914), with the legacy
// load formula kept as the answer for a bench that has no store yet -- so adopting the
// store is not a flag day.
//
// A bench named by --local-bench is read by this machine's own shell. There is no ssh from
// the Studio to the Studio (`ssh studio` fails host key verification there), and there
// should not be: a bench that is this machine is read locally or not at all.

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// defaultSlotsStore is where a bench keeps its slot store unless --slots-store says
// otherwise. It is a PATH and not a machine name -- expanded on the bench, by the bench's
// own shell, because every bench has a different home.
const defaultSlotsStore = "$HOME/nova-bench/slots"

// defaultSlotsBin is the nova-swarm that reads the store's leases. A non-login ssh does not
// always carry ~/.local/bin on PATH, and a probe that silently found no nova-swarm would
// read zero leases and call a full bench empty -- which is the one mistake a capacity
// number may not make. So the path is named, and it is a flag.
const defaultSlotsBin = "$HOME/.local/bin/nova-swarm"

// defaultMaxLoadPerCore is the brake: a bench over this many load units per core is dealt
// nothing this tick. It is the core term of the old formula read as a guard rather than as
// a size (`cores*3/2` is 1.5 per core), and 0 turns it off.
const defaultMaxLoadPerCore = 1.5

// storeProbeConfig is everything the probe needs that is not the bench's name.
type storeProbeConfig struct {
	SSH      string          // the ssh binary; empty is `ssh`
	Store    string          // the slot store path ON the bench
	Owner    string          // the owner row in shares.tsv; empty is swarm-<bench>
	SlotsBin string          // the nova-swarm that lists the store's leases
	Root     string          // the swarm root, for the legacy formula's disk term
	Local    map[string]bool // benches read by this machine's own shell, never over ssh
	MaxLoad  float64         // the load brake, per core; 0 is no brake
	Stderr   io.Writer       // where a braked bench says so
}

// storeProbeCapacity is the pulse.Capacity the verb uses when a slot store is named: one
// probe per bench per tick, parsed and braked by internal/pulse.
func storeProbeCapacity(c storeProbeConfig) pulse.Capacity {
	return pulse.StoreCapacity{
		Probe:          c.probe,
		MaxLoadPerCore: c.MaxLoad,
		Stderr:         c.Stderr,
	}
}

// probe runs the one line on the bench and hands back what it printed.
func (c storeProbeConfig) probe(bench string) (string, error) {
	owner := strings.TrimSpace(c.Owner)
	if owner == "" {
		// The seat the launcher already hands the bench. It is a convention this tool
		// already writes down (flashLauncher's `swarm-<bench>`), not a new guess.
		owner = "swarm-" + bench
	}
	script := storeScript(c.Store, owner, c.SlotsBin, c.Root)
	if c.Local[bench] {
		return runProbeLocally(script)
	}
	return c.runProbeOverSSH(bench, script)
}

// runProbeOverSSH is the raw ssh seam: one probe, one bench, the line it printed.
func (c storeProbeConfig) runProbeOverSSH(bench, script string) (string, error) {
	ssh := c.SSH
	if ssh == "" {
		ssh = "ssh"
	}
	testguard.RefuseHosts(ssh, "-n", "-o", "BatchMode=yes", bench, script)
	return runProbe(exec.Command(ssh, "-n", "-o", "BatchMode=yes", bench, script))
}

// runProbeLocally reads a bench that IS this machine with this machine's own shell. No
// host is reached, so there is no host to guard.
func runProbeLocally(script string) (string, error) {
	return runProbe(exec.Command("/bin/sh", "-c", script))
}

// runProbe runs the probe and keeps the child's last line, so a probe that failed says why
// rather than answering a bare exit status.
func runProbe(cmd *exec.Cmd) (string, error) {
	var out bytes.Buffer
	said := &tail{}
	cmd.Stdout, cmd.Stderr = &out, said
	if err := cmd.Run(); err != nil {
		return "", said.wrap(err)
	}
	return out.String(), nil
}

// storeScript is the one line the bench runs. It prints exactly one of
//
//	store share=<n> held=<n> cores=<n> load1=<f>
//	formula capacity=<n> cores=<n> load1=<f>
//
// and nothing else, so the caller parses an answer rather than a shell transcript. `nproc`
// and /proc are Linux; the fallbacks are darwin's `sysctl` (`vm.loadavg` prints
// `{ 3.20 3.40 3.60 }`), because a bench is whatever the registry says is a bench.
func storeScript(store, owner, slotsBin, root string) string {
	if strings.TrimSpace(slotsBin) == "" {
		slotsBin = defaultSlotsBin
	}
	return strings.Join([]string{
		`S="` + shellDoubleQuoted(store) + `"`,
		`O="` + shellDoubleQuoted(owner) + `"`,
		`B="` + shellDoubleQuoted(slotsBin) + `"`,
		`c=$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null)`,
		`[ -n "$c" ] || c=0`,
		`l=$(cut -d" " -f1 /proc/loadavg 2>/dev/null || sysctl -n vm.loadavg 2>/dev/null | awk '{print $2}')`,
		`[ -n "$l" ] || l=0`,
		`sh=$(awk -F'\t' -v o="$O" '$1==o{print $2}' "$S/shares.tsv" 2>/dev/null)`,
		`if [ -n "$sh" ]; then ` +
			`h=$("$B" slots list --store "$S" 2>/dev/null | grep -c "owner=$O .*state=live"); ` +
			`[ -n "$h" ] || h=0; ` +
			`echo "store share=$sh held=$h cores=$c load1=$l"; ` +
			`else ` + legacyFormula(root) + `; ` +
			`echo "formula capacity=$a cores=$c load1=$l"; fi`,
	}, "; ")
}

// legacyFormula is capacityBody -- the number `fill` answered before the store -- with the
// whole-number load it wants, clamped at zero. It is kept for a bench that has no slot
// store yet, and for nothing else.
func legacyFormula(root string) string {
	return `li=$(printf '%.0f' "$l" 2>/dev/null || echo 0); ` +
		capacityBody(root) +
		`; [ -n "$a" ] || a=0; [ "$a" -lt 0 ] 2>/dev/null && a=0; true`
}

// localBenchSet reads the repeatable --local-bench into a set, and refuses a name that is
// not one of the benches this run fills: a local bench nobody fills is a typo, and a typo
// here means a bench read over an ssh that cannot work.
func localBenchSet(names, benches []string) (map[string]bool, error) {
	if len(names) == 0 {
		return nil, nil
	}
	known := map[string]bool{}
	for _, b := range benches {
		known[b] = true
	}
	out := map[string]bool{}
	for _, name := range names {
		if len(benches) > 0 && !known[name] {
			return nil, fmt.Errorf(
				"--local-bench names %q, which is not one of the benches this fill fills (%s)",
				name, strings.Join(benches, ", "))
		}
		out[name] = true
	}
	return out, nil
}
