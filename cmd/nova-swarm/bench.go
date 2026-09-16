// bench is the verb that proves one bench before a batch is pointed at it:
// `nova-swarm bench probe --benches <file> --bench <name>` (docs/SPEC-SWARM.md
// "Benches"). It runs one check line per fact, every check runs even when one
// fails, and it ends with one BENCH OK or BENCH REFUSED line.
package main

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// benchCheck is one probe check's result.
type benchCheck struct {
	name   string
	ok     bool
	val    string // one bounded value on the BENCH CHECK line
	reason string // the clause a BENCH REFUSED line quotes
}

// cmdBench dispatches the bench verb's subcommands. probe is the only one.
func cmdBench(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, " bench", "wants a subcommand: probe (bench probe --benches <file> --bench <name>)")
	}
	switch args[0] {
	case "probe":
		return cmdBenchProbe(args[1:], stdout, stderr)
	}
	return refuse(stderr, " bench", fmt.Sprintf("unknown subcommand %q", args[0]))
}

// cmdBenchProbe proves one bench: ssh reachable, root writable, harness --version,
// remote nova-swarm version equals this binary's, cores valid against nproc, pin,
// auth present with mode 0600, and the wall as nova-sandbox check reports.
func cmdBenchProbe(args []string, stdout, stderr io.Writer) int {
	f := newFlags("bench probe")
	benches := f.fs.String("benches", "", "")
	bench := f.fs.String("bench", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*benches, "benches", "the TSV file naming each bench (name host root cores harness auth wall)")
	f.want(*bench, "bench", "the name of one row to prove, as its name column")
	if f.refused(stderr) {
		return 2
	}
	table, err := swarm.LoadBenchTable(*benches)
	if err != nil {
		fmt.Fprintf(stderr, "BENCH REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	var row *swarm.Bench
	for i := range table {
		if table[i].Name == *bench {
			row = &table[i]
			break
		}
	}
	if row == nil {
		fmt.Fprintf(stderr, "BENCH REFUSED: no bench %s in %s\n", oneline.Field(*bench), oneline.Field(*benches))
		return 2
	}
	p := &prober{row: row}
	checks := p.run()
	firstFail := -1
	fails := 0
	for i, c := range checks {
		fmt.Fprintf(stdout, "BENCH CHECK name=%s check=%s ok=%t %s\n",
			oneline.Field(row.Name), oneline.Field(c.name), c.ok, oneline.Field(c.val))
		if !c.ok {
			fails++
			if firstFail < 0 {
				firstFail = i
			}
		}
	}
	if firstFail < 0 {
		fmt.Fprintf(stdout, "BENCH OK name=%s cores=%s pin=%s wall=%s\n",
			oneline.Field(row.Name), oneline.Field(p.coresWord()), oneline.Field(p.pin), oneline.Field(p.wall))
		return 0
	}
	c := checks[firstFail]
	fmt.Fprintf(stdout, "BENCH REFUSED name=%s check=%s: %s (more %d)\n",
		oneline.Field(row.Name), oneline.Field(c.name), oneline.Escape(c.reason), fails-1)
	return 1
}

// prober holds the bench being proved and the answers the pin and wall checks
// settle for the BENCH OK line.
type prober struct {
	row  *swarm.Bench
	pin  string
	wall string
}

// remote runs args after host through ssh, or locally for the local row. The fake
// ssh on a test's PATH records its argv and execs the rest.
func (p *prober) remote(host string, args ...string) (string, error) {
	var argv []string
	if host != "" && host != "local" {
		argv = append([]string{"ssh", host}, args...)
	} else {
		argv = args
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// run performs every check in order and returns their results. Every check runs
// even when an earlier one failed, so one probe reports the whole bench.
func (p *prober) run() []benchCheck {
	b := p.row
	var out []benchCheck

	// ssh reachable: one no-op round trip; the local row needs no ssh.
	if b.Host == "" || b.Host == "local" {
		out = append(out, benchCheck{name: "ssh", ok: true, val: "local"})
	} else if _, err := p.remote(b.Host, "true"); err != nil {
		out = append(out, benchCheck{name: "ssh", ok: false, reason: "ssh " + b.Host + " refused"})
	} else {
		out = append(out, benchCheck{name: "ssh", ok: true, val: "ok"})
	}

	// root writable: one probe file written and removed.
	probeFile := b.Root + "/.nova-probe"
	if _, err := p.remote(b.Host, "sh", "-c", "touch \""+probeFile+"\" && rm -f \""+probeFile+"\""); err != nil {
		out = append(out, benchCheck{name: "root", ok: false, reason: b.Root + " not writable"})
	} else {
		out = append(out, benchCheck{name: "root", ok: true, val: "written"})
	}

	// harness --version runs.
	if _, err := p.remote(b.Host, b.Harness, "--version"); err != nil {
		out = append(out, benchCheck{name: "harness", ok: false, reason: b.Harness + " --version would not run"})
	} else {
		out = append(out, benchCheck{name: "harness", ok: true, val: "runs"})
	}

	// remote nova-swarm version equals this binary's own version line.
	local := buildinfo.Line("nova-swarm", version)
	remoteVer, err := p.remote(b.Host, b.Root+"/bin/nova-swarm", "version")
	if err != nil || remoteVer != local {
		out = append(out, benchCheck{name: "version", ok: false, reason: "local=" + local + " remote=" + remoteVer})
	} else {
		out = append(out, benchCheck{name: "version", ok: true, val: "match"})
	}

	// cores valid against nproc --all, or "-".
	if !b.Pinned() {
		out = append(out, benchCheck{name: "cores", ok: true, val: "-"})
	} else {
		cores, _ := swarm.CoresList(b.Cores)
		nprocOut, nerr := p.remote(b.Host, "nproc", "--all")
		n, aerr := strconv.Atoi(strings.TrimSpace(nprocOut))
		bad := ""
		for _, c := range cores {
			if c < 0 || c >= n {
				bad = fmt.Sprintf("core %d not in nproc=%d", c, n)
				break
			}
		}
		if nerr != nil || aerr != nil {
			out = append(out, benchCheck{name: "cores", ok: false, reason: "nproc --all would not run"})
		} else if bad != "" {
			out = append(out, benchCheck{name: "cores", ok: false, reason: bad})
		} else {
			out = append(out, benchCheck{name: "cores", ok: true, val: nprocOut})
		}
	}

	// pin: taskset when it is on the bench's PATH, none when not; a list without
	// taskset is a refusal.
	if _, err := p.remote(b.Host, "sh", "-c", "command -v taskset"); err == nil {
		p.pin = "taskset"
	} else {
		p.pin = "none"
	}
	if b.Pinned() && p.pin == "none" {
		out = append(out, benchCheck{name: "pin", ok: false, reason: "cores list " + b.Cores + " but no taskset on PATH"})
	} else {
		out = append(out, benchCheck{name: "pin", ok: true, val: p.pin})
	}

	// auth present with mode 0600, never read: a stat of the mode only.
	authOut, aerr := p.remote(b.Host, "stat", "-c", "%a", b.Auth)
	if aerr != nil || strings.TrimSpace(authOut) != "600" {
		out = append(out, benchCheck{name: "auth", ok: false, reason: "auth not mode 0600 (stat " + strings.TrimSpace(authOut) + ")"})
	} else {
		out = append(out, benchCheck{name: "auth", ok: true, val: "0600"})
	}

	// wall: whatever nova-sandbox check reports there.
	checkOut := p.wallCheck()
	if strings.Contains(checkOut, "backend=none") {
		p.wall = "none"
	} else {
		p.wall = "sandbox"
	}
	out = append(out, benchCheck{name: "wall", ok: true, val: p.wall})

	return out
}

// wallCheck runs nova-sandbox check on the bench and returns its output line.
func (p *prober) wallCheck() string {
	out, _ := p.remote(p.row.Host, "nova-sandbox", "check")
	return out
}

// coresWord is the cores= token of the BENCH OK line: a count for a list, "-" when
// the row pins nothing.
func (p *prober) coresWord() string {
	if !p.row.Pinned() {
		return "-"
	}
	cores, _ := swarm.CoresList(p.row.Cores)
	return strconv.Itoa(len(cores))
}
