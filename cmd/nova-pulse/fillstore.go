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
		Owner:          c.Owner,
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

// probeAnswerLimit bounds what a bench may say. A bench is the least trusted thing on this
// wire, and the answer used to land in an unbounded buffer: a bench that printed for ever
// cost the coordinator its memory. A real store's whole listing is a few hundred lines.
const probeAnswerLimit = 64 << 10

// runProbe runs the probe, reads a BOUNDED answer, and keeps the child's last line, so a
// probe that failed says why rather than answering a bare exit status.
func runProbe(cmd *exec.Cmd) (string, error) {
	out := &capped{limit: probeAnswerLimit}
	said := &tail{}
	cmd.Stdout, cmd.Stderr = out, said
	if err := cmd.Run(); err != nil {
		return "", said.wrap(err)
	}
	if out.over {
		return "", fmt.Errorf(
			"the bench answered more than %d bytes; refusing to read a capacity off an answer with no end", probeAnswerLimit)
	}
	return out.buf.String(), nil
}

// capped keeps at most limit bytes and remembers that there were more. It never fails the
// child's write, so the probe is not killed by a broken pipe halfway through.
type capped struct {
	buf   bytes.Buffer
	limit int
	over  bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room > 0 {
		if len(p) <= room {
			c.buf.Write(p)
		} else {
			c.buf.Write(p[:room])
			c.over = true
		}
	} else if len(p) > 0 {
		c.over = true
	}
	return len(p), nil
}

// storeScript is what the bench runs. It prints exactly one of
//
//	store share=<n> cores=<n> load1=<f>
//	leases
//	<nova-swarm slots list's own output, verbatim, for Go to count>
//
//	formula capacity=<n> cores=<n> load1=<f> why=<no-shares-file|no-row-for-owner>
//	unreadable reason=<why> ...
//
// and nothing else, so the caller parses an answer rather than a shell transcript. `nproc`
// and /proc are Linux; the fallbacks are darwin's `sysctl` (`vm.loadavg` prints
// `{ 3.20 3.40 3.60 }`), because a bench is whatever the registry says is a bench.
//
// THE LEASE READ'S STATUS IS KEPT (Stella, #1945). It used to be
// `h=$("$B" slots list ... | grep -c ...)`, whose status is grep's: a missing or failing
// nova-swarm printed nothing, grep counted 0, the script succeeded, and a bench with every
// slot leased answered `held=0` -- its whole share dealt to a machine with nothing free.
// The output is captured first -- a plain command substitution, never a pipeline -- its
// exit status checked, and then handed back VERBATIM so Go counts it (pulse.countLeases):
// a shell that counts is a shell whose status belongs to grep. An EMPTY list, which is a
// bench with its whole share free, stays an empty list.
//
// The ONE fallthrough to the formula is the documented no-store-row case: no shares.tsv at
// all, or a readable shares.tsv with no row for this owner AND a lease read that SUCCEEDED.
// It carries `why=` so it is never silent. A shares.tsv that is there and cannot be read, a
// share that is not a whole number and a lease read that failed are refusals -- not a quiet
// reversion to the number this change exists to stop using.
func storeScript(store, owner, slotsBin, root string) string {
	if strings.TrimSpace(slotsBin) == "" {
		slotsBin = defaultSlotsBin
	}
	formula := legacyFormula(root) + `; echo "formula capacity=$a cores=$c load1=$l why=$w"; exit 0`
	return strings.Join([]string{
		`S="` + shellDoubleQuoted(store) + `"`,
		`O="` + shellDoubleQuoted(owner) + `"`,
		`B="` + shellDoubleQuoted(slotsBin) + `"`,
		// A MEASUREMENT NOBODY TOOK IS NOT A ZERO (Stella, R2). Both of these used to
		// fall back to `0` when every reader failed, and `load1=0` passes any brake:
		// a bench whose load nothing could read was dealt cards with the brake on.
		// Unreadable is its own token now, and it travels to the caller as itself.
		`c=$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null)`,
		`case "$c" in ''|*[!0-9]*) c=unreadable;; esac`,
		`l=$(cut -d" " -f1 /proc/loadavg 2>/dev/null || sysctl -n vm.loadavg 2>/dev/null | awk '{print $2}')`,
		`case "$l" in ''|*[!0-9.]*) l=unreadable;; esac`,
		// No store on this bench at all: the documented fallback, positively detected,
		// and it says which case it is.
		`if [ ! -e "$S/shares.tsv" ]; then w=no-shares-file; ` + formula + `; fi`,
		`if [ ! -r "$S/shares.tsv" ]; then echo "unreadable reason=shares-unreadable"; exit 0; fi`,
		`sh=$(awk -F'\t' -v o="$O" '$1==o{print $2}' "$S/shares.tsv" 2>/dev/null)`,
		// THE LEASE READ, AND ITS OWN EXIT STATUS. It is a plain command substitution,
		// never a pipeline, so the status is the reader's and not grep's -- and it runs
		// BEFORE the no-row fallback, so the one path back to the formula is only ever
		// taken off a listing that succeeded.
		`o=$("$B" slots list --store "$S" 2>/dev/null); rc=$?`,
		`if [ "$rc" -ne 0 ]; then echo "unreadable reason=slots-list-exit rc=$rc"; exit 0; fi`,
		`if [ -z "$sh" ]; then w=no-row-for-owner; ` + formula + `; fi`,
		`case "$sh" in ''|*[!0-9]*) echo "unreadable reason=share-not-a-whole-number"; exit 0;; esac`,
		// The listing goes back VERBATIM; the counting is Go's.
		`echo "store share=$sh cores=$c load1=$l"`,
		`echo "leases"`,
		`printf '%s\n' "$o"`,
	}, "; ")
}

// legacyFormula is capacityBody -- the number `fill` answered before the store -- with the
// whole-number load it wants, clamped at zero. It is kept for a bench that has no slot
// store yet, and for nothing else.
// legacyFormula is capacityBody -- the number `fill` answered before the store -- with
// every measurement it stands on CHECKED first. It is the one path back to that number, so
// it may not be reached on a reading nobody took: each unread measurement is its own
// refusal, named, rather than an empty string that shell arithmetic reads as zero.
//
// The one number it still floors is $a itself, which is computed and not measured: a
// formula that comes out negative is a bench with no room, and that was always its meaning.
func legacyFormula(root string) string {
	return `if [ "$c" = unreadable ]; then echo "unreadable reason=cores-unreadable"; exit 0; fi` +
		`; if [ "$l" = unreadable ]; then echo "unreadable reason=load-unreadable"; exit 0; fi` +
		`; li=$(printf '%.0f' "$l" 2>/dev/null)` +
		`; case "$li" in ''|*[!0-9]*) echo "unreadable reason=load-unreadable"; exit 0;; esac` +
		`; ` + capacityReadings(root) +
		`; case "$f" in ''|*[!0-9]*) echo "unreadable reason=formula-disk-unreadable"; exit 0;; esac` +
		`; case "$m" in ''|*[!0-9]*) echo "unreadable reason=formula-memory-unreadable"; exit 0;; esac` +
		`; ` + capacityArithmetic() +
		`; case "$a" in ''|*[!0-9-]*) echo "unreadable reason=formula-unreadable"; exit 0;; esac` +
		`; [ "$a" -lt 0 ] 2>/dev/null && a=0; true`
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
