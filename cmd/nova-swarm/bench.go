// bench is the verb that proves one bench before a batch is pointed at it:
// `nova-swarm bench probe --benches <file> --bench <name>` (docs/SPEC-SWARM.md
// "Benches"). It runs one check line per fact, every check runs even when one
// fails, and it ends with one BENCH OK or BENCH REFUSED line.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// benchCheck is one probe check's result.
type benchCheck struct {
	name   string
	ok     bool
	val    string // one bounded value on the BENCH CHECK line
	reason string // the clause a BENCH REFUSED line quotes
}

// cmdBench dispatches the bench verb's subcommands. probe proves one bench;
// size measures its width.
func cmdBench(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, " bench", "wants a subcommand: probe (bench probe --benches <file> --bench <name>), size (bench size --benches <file> --bench <name> [--max <n>])")
	}
	switch args[0] {
	case "probe":
		return cmdBenchProbe(args[1:], stdout, stderr)
	case "size":
		return cmdBenchSize(args[1:], stdout, stderr)
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
		testguard.RefuseHosts(argv[0], argv[1:]...)
	} else {
		argv = args
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// authMode reports the auth file's permission bits as octal digits, and never one
// byte of its contents. A remote bench answers through the one `stat -c %a` round
// trip its ssh log is checked for: benches are Linux, and their stat is GNU's. The
// local row answers through os.Stat instead of shelling out, because `-c` is GNU's
// spelling alone -- darwin's stat refuses the option outright -- and a tool of ours
// may not require GNU coreutils on the operator's own Mac.
func (p *prober) authMode(b *swarm.Bench) (string, error) {
	if b.Host == "" || b.Host == "local" {
		fi, err := os.Stat(b.Auth)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%o", fi.Mode().Perm()), nil
	}
	out, err := p.remote(b.Host, "stat", "-c", "%a", b.Auth)
	return strings.TrimSpace(out), err
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
	authOut, aerr := p.authMode(b)
	if aerr != nil || authOut != "600" {
		out = append(out, benchCheck{name: "auth", ok: false, reason: "auth not mode 0600 (stat " + authOut + ")"})
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

// benchSizeRound runs one doubling round: the known-answer card W times
// concurrently, and reports the round's measurements. It is a variable so
// tests run with a fake and no network: the default runs W concurrent no-op
// round trips (the exit-0 card) and reads the bench's own one-minute load.
var benchSizeRound = func(row *swarm.Bench, w int, prevCPM float64) (swarm.SizeRound, error) {
	p := &prober{row: row}
	cores := 0
	if list, err := swarm.CoresList(row.Cores); err == nil && row.Pinned() {
		cores = len(list)
	} else if out, err := p.remote(row.Host, "nproc", "--all"); err == nil {
		if n, aerr := strconv.Atoi(strings.TrimSpace(out)); aerr == nil {
			cores = n
		}
	}
	before, _ := benchLoad(p, row.Host)
	start := benchNow()
	done := make(chan bool, w)
	for i := 0; i < w; i++ {
		go func() {
			_, _ = p.remote(row.Host, "true")
			done <- true
		}()
	}
	abstains := 0
	for i := 0; i < w; i++ {
		if !<-done {
			abstains++
		}
	}
	minutes := benchNow().Sub(start).Minutes()
	if minutes <= 0 {
		minutes = 1.0 / 60.0
	}
	after, _ := benchLoad(p, row.Host)
	load := before
	if after > load {
		load = after
	}
	return swarm.SizeRound{W: w, Cores: cores, Load: load,
		CardsPerMin: float64(w) / minutes, PrevCardsPerMin: prevCPM, Abstains: abstains}, nil
}

// benchLoad reads the bench's one-minute load average: /proc/loadavg first,
// uptime where there is no proc.
var benchLoad = func(p *prober, host string) (float64, error) {
	if out, err := p.remote(host, "cat", "/proc/loadavg"); err == nil {
		var load float64
		if _, serr := fmt.Sscanf(strings.TrimSpace(out), "%f", &load); serr == nil {
			return load, nil
		}
	}
	out, err := p.remote(host, "uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(out)
	for i, f := range fields {
		if strings.HasPrefix(f, "average") && i+2 < len(fields) {
			var load float64
			if _, serr := fmt.Sscanf(strings.TrimSuffix(fields[i+2], ","), "%f", &load); serr == nil {
				return load, nil
			}
		}
	}
	return 0, fmt.Errorf("no load average in %q", out)
}

// benchNow is the clock the throughput division reads, so tests hold it still.
var benchNow = func() (t time.Time) { return time.Now() }

// benchSizeVersion is the tool identity the row's version is written with.
var benchSizeVersion = func() string { return buildinfo.Version(version) }

// cmdBenchSize measures one bench's width: W = 1, 2, 4, ... while the three
// size rules hold, then records the last W that held on the bench row with
// the measured table beside it, and prints one BENCH WIDTH line.
func cmdBenchSize(args []string, stdout, stderr io.Writer) int {
	f := newFlags("bench size")
	benches := f.fs.String("benches", "", "")
	bench := f.fs.String("bench", "", "")
	max := f.fs.Int("max", 256, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*benches, "benches", "the TSV file naming each bench (name host root cores harness auth wall)")
	f.want(*bench, "bench", "the name of one row to size, as its name column")
	if *max < 1 {
		f.add(fmt.Sprintf("--max is at least 1, got %d; it caps the doubling", *max))
	}
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
	var rounds []swarm.SizeRound
	prevCPM := 0.0
	width := 0
	for w := 1; w <= *max; w *= 2 {
		r, err := benchSizeRound(row, w, prevCPM)
		if err != nil {
			fmt.Fprintf(stderr, "BENCH REFUSED: %s\n", oneline.Err(err))
			return 2
		}
		rounds = append(rounds, r)
		if !swarm.SizeRoundHolds(r) {
			break
		}
		width = w
		prevCPM = r.CardsPerMin
	}
	if width == 0 {
		fmt.Fprintf(stderr, "BENCH REFUSED: bench %s held no width: the first round broke a size rule\n", oneline.Field(row.Name))
		return 1
	}
	stamp := benchNow().UTC().Format(time.RFC3339)
	ver := swarm.Version8(benchSizeVersion())
	cores, rows, err := swarm.RecordBenchWidth(*benches, row.Name, width, stamp, ver)
	if err != nil {
		fmt.Fprintf(stderr, "BENCH REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	_ = swarm.WriteMeasuredTable(*benches+".measured", row.Name, rounds, benchNow())
	coresWord := strconv.Itoa(cores)
	if cores < 0 {
		coresWord = "-"
	}
	fmt.Fprintf(stdout, "BENCH WIDTH bench=%s width=%d cores=%s rows=%d\n",
		oneline.Field(row.Name), width, oneline.Field(coresWord), rows)
	return 0
}
