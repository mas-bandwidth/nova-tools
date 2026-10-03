package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

// run --bench: a record refresh as one command from a working machine. The
// command stays where it is; the cases run on a TLC record machine (a machine
// row with tla=true, its jar placed and checked by the tools play's tla play),
// each after the bench's load falls under a limit, and the records come back
// and are merged into the checkout's tla/RUNS.tsv (tla/README.md, "The record
// machines"). The logic is here and the transport is remote: the tests drive
// it with a bench that is a temp dir and this package's own run.

// pinnedSumFile is the file under tla/ that pins the TLC jar: its first word
// is the jar's SHA-256, the one the records name in jar_sha256.
const pinnedSumFile = "tla2tools.sha256"

// defaultBenchJar is where the tools play's tla play holds the jar on a
// record machine (fleet/group_vars/all.yml, nova_tla_jar).
const defaultBenchJar = "/opt/tla/tla2tools.jar"

// benchProbe is what one look at a bench says: its platform, its logical
// CPUs, its 1-minute load and the SHA-256 of the jar at the path asked about
// ("" when there is no jar there).
type benchProbe struct {
	GOOS, GOARCH string
	CPUs         int
	Load         float64
	JarSHA256    string
}

// benchRow is a machine row as the fleet's list prints it: its name and
// whether it is a TLC record machine.
type benchRow struct {
	Name string
	TLA  bool
}

// remote is one bench machine, as operations. sshRemote sends each as one
// shell line over ssh.
type remote interface {
	Probe(ctx context.Context, jar string) (benchProbe, error)
	Load(ctx context.Context) (float64, error)
	// Stage unpacks the archive into a new directory of its own on the
	// bench and returns that directory.
	Stage(ctx context.Context, archive io.Reader) (string, error)
	// Run runs the staged tlacheck in dir with args, niced, its output
	// streamed, and returns its exit status.
	Run(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) (int, error)
	// Fetch returns a tar of a run directory's records and logs.
	Fetch(ctx context.Context, dir string) ([]byte, error)
	// Remove removes a directory Stage made.
	Remove(ctx context.Context, dir string) error
}

// benchDeps is everything run --bench reaches outside this process.
type benchDeps struct {
	dial     func(machine string) remote
	machines func(ctx context.Context) ([]benchRow, error)                   // the fleet's machine rows
	build    func(ctx context.Context, root, goos, goarch, out string) error // tlacheck of root, for a platform
	sleep    func(time.Duration)
}

func realBench() benchDeps {
	return benchDeps{
		dial:     func(m string) remote { return sshRemote{machine: m} },
		machines: novaConfigMachines,
		build:    goBuild,
		sleep:    time.Sleep,
	}
}

// benchOpts is one run --bench.
type benchOpts struct {
	root, dir, machine, jar, java, group string
	timeout                              time.Duration
	workers                              int
	manual                               bool
	loadBelow                            float64
	troughWait, troughPoll               time.Duration
	cases                                []tlc.Case
}

// loadLimit is the load a case waits to fall under: the flag's when given,
// else four fifths of the bench's logical CPUs.
func loadLimit(flag float64, cpus int) float64 {
	if flag > 0 {
		return flag
	}
	return float64(cpus) * 0.8
}

// trough waits until the bench's 1-minute load is under limit: it reads the
// load and, while it is at or over the limit, sleeps one poll and reads again,
// for at most max in all. It returns the load it went on at and the time it
// waited, counted in polls.
func trough(ctx context.Context, load func(context.Context) (float64, error), limit float64, poll, max time.Duration, sleep func(time.Duration)) (float64, time.Duration, error) {
	var waited time.Duration
	for {
		l, err := load(ctx)
		if err != nil {
			return 0, waited, err
		}
		if l < limit {
			return l, waited, nil
		}
		if waited >= max {
			return l, waited, fmt.Errorf("the 1-minute load stayed at or over %.2f for %s (last %.2f)", limit, max, l)
		}
		sleep(poll)
		waited += poll
	}
}

func runOnBench(e env, o benchOpts) int {
	ctx := context.Background()
	refuseB := func(cause, next string) int { return refuse(e, "run", cause, next) }
	again := tool + " run --bench " + o.machine + " ..."
	want, err := pinnedSum(o.root)
	if err != nil {
		return refuseB(err.Error(), tool+" run -h")
	}
	groups, err := benchGroups(o)
	if err != nil {
		return refuseB(err.Error(), tool+" groups --root "+o.root+" --stale")
	}
	if len(groups) == 0 {
		event(e.stdout, "BENCH", "OK", "bench", o.machine, "stale", "0", "records", filepath.Join(o.root, "tla", tlc.RunsFile))
		return 0
	}
	var rows []benchRow
	if o.machine == "any" {
		if rows, err = e.bench.machines(ctx); err != nil {
			return refuseB("the fleet's machine rows cannot be read: "+err.Error(), "nova-config machine list (with NOVA_PG_DSN set), or name the record machine: "+tool+" run --bench <machine> ...")
		}
	}
	machine, probe, err := pickBench(ctx, e, o, rows, want)
	if err != nil {
		var r *benchRefusal
		if errors.As(err, &r) {
			return refuseB(r.cause, r.next)
		}
		return refuseB(err.Error(), again)
	}
	platform, err := tlc.Platform(probe.GOOS, probe.GOARCH)
	if err != nil {
		return refuseB(machine+" cannot be a record machine: "+err.Error(), "nova-config machine set "+machine+" --tla false --as <actor>")
	}
	limit := loadLimit(o.loadBelow, probe.CPUs)
	event(e.stdout, "BENCH", "OK", "bench", machine, "platform", platform, "cpus", strconv.Itoa(probe.CPUs),
		"load", fmt.Sprintf("%.2f", probe.Load), "below", fmt.Sprintf("%.2f", limit), "jar", o.jar, "sha256", want)

	bin := filepath.Join(o.dir, "bin", tool+"-"+platform)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return refuseB("cannot create "+filepath.Dir(bin)+": "+err.Error(), tool+" run --dir <a directory you can write> ...")
	}
	if err := e.bench.build(ctx, o.root, probe.GOOS, probe.GOARCH, bin); err != nil {
		return refuseB("tlacheck cannot be built for "+platform+": "+err.Error(), "go build ./tools/tlacheck")
	}
	archive, files, err := stageArchive(o.root, bin)
	if err != nil {
		return refuseB(err.Error(), tool+" run -h")
	}
	r := e.bench.dial(machine)
	rdir, err := r.Stage(ctx, archive)
	if err != nil {
		return refuseB("cannot stage the models on "+machine+": "+err.Error(), again)
	}
	event(e.stdout, "STAGE", "OK", "bench", machine, "dir", rdir, "files", strconv.Itoa(files))
	defer func() {
		if err := r.Remove(ctx, rdir); err != nil {
			eventWhy(e.stderr, "CLEAN", "FAIL", err.Error()+"; the next run --bench on it removes it after a day", "bench", machine, "dir", rdir)
		}
	}()

	var fetched []string
	failed, cases := false, 0
	for _, g := range groups {
		chosen, _ := tlc.Select(o.cases, g, 1, 0) // a group benchGroups found in the plan
		for i := range chosen {
			load, waited, err := trough(ctx, r.Load, limit, o.troughPoll, o.troughWait, e.bench.sleep)
			if err != nil {
				eventWhy(e.stderr, "BENCH", "FAIL", err.Error()+"; tla/RUNS.tsv is unchanged; run the same command again when the bench is quieter, or name another record machine",
					"bench", machine, "group", g, "case", fmt.Sprintf("%d/%d", i+1, len(chosen)))
				return 1
			}
			event(e.stdout, "TROUGH", "OK", "bench", machine, "group", g, "case", fmt.Sprintf("%d/%d", i+1, len(chosen)),
				"load", fmt.Sprintf("%.2f", load), "below", fmt.Sprintf("%.2f", limit), "waited", waited.String())
			sub := path.Join("runs", g, strconv.Itoa(i))
			args := []string{"run", "--root", rdir, "--jar", o.jar, "--dir", path.Join(rdir, sub), "--group", g,
				"--shards", strconv.Itoa(len(chosen)), "--shard", strconv.Itoa(i), "--workers", strconv.Itoa(o.workers), "--timeout", o.timeout.String()}
			if o.java != "" {
				args = append(args, "--java", o.java)
			}
			if o.manual {
				args = append(args, "--manual")
			}
			runCtx, cancel := context.WithTimeout(ctx, o.timeout+5*time.Minute)
			code, err := r.Run(runCtx, rdir, args, e.stdout, e.stderr)
			cancel()
			if err != nil || (code != 0 && code != 1) {
				why := fmt.Sprintf("the bench's tlacheck exited %d", code)
				if err != nil {
					why = err.Error()
				}
				eventWhy(e.stderr, "BENCH", "FAIL", why+"; tla/RUNS.tsv is unchanged", "bench", machine, "group", g, "case", fmt.Sprintf("%d/%d", i+1, len(chosen)))
				return 2
			}
			cases++
			local := filepath.Join(o.dir, g, strconv.Itoa(i))
			if err := fetchRun(ctx, r, path.Join(rdir, sub), local); err != nil {
				eventWhy(e.stderr, "BENCH", "FAIL", "the records cannot be fetched: "+err.Error()+"; tla/RUNS.tsv is unchanged", "bench", machine, "group", g)
				return 2
			}
			if code == 1 {
				failed = true
				continue
			}
			fetched = append(fetched, filepath.Join(local, tlc.RunsFile))
		}
	}
	if failed {
		eventWhy(e.stderr, "BENCH", "FAIL", "a case did not reach the result CASES.tsv declares; nothing was merged and tla/RUNS.tsv is unchanged; each run's records and logs are under "+o.dir,
			"bench", machine, "groups", strconv.Itoa(len(groups)), "cases", strconv.Itoa(cases))
		return 1
	}
	event(e.stdout, "BENCH", "OK", "bench", machine, "groups", strconv.Itoa(len(groups)), "cases", strconv.Itoa(cases), "runs", o.dir)
	runs := filepath.Join(o.root, "tla", tlc.RunsFile)
	return cmdMerge(e, append([]string{"--root", o.root, "--keep", runs, "--out", runs}, fetched...))
}

// benchGroups is the group named, or else every group the records call
// stale.
func benchGroups(o benchOpts) ([]string, error) {
	if o.group != "" {
		if _, err := tlc.Select(o.cases, o.group, 1, 0); err != nil {
			return nil, err
		}
		return []string{o.group}, nil
	}
	src, err := tlc.SourceAt(o.root)
	if err != nil {
		return nil, err
	}
	records, err := tlc.ReadRecordsFile(filepath.Join(o.root, "tla", tlc.RunsFile))
	if err != nil {
		return nil, fmt.Errorf("cannot read the records: %v%s", err, layoutHint(err))
	}
	return tlc.StaleGroups(src, o.cases, records)
}

// pinnedSum reads the SHA-256 tla/tla2tools.sha256 pins the jar to.
func pinnedSum(root string) (string, error) {
	p := filepath.Join(root, "tla", pinnedSumFile)
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("the pinned jar's sum cannot be read: %v", err)
	}
	f := strings.Fields(string(raw))
	if len(f) == 0 || !sha256RE.MatchString(f[0]) {
		return "", fmt.Errorf("%s holds no SHA-256 first", p)
	}
	return f[0], nil
}

var sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// benchRefusal is a refusal of the machine named, with its next command.
type benchRefusal struct{ cause, next string }

func (r *benchRefusal) Error() string { return r.cause }

// pickBench is the record machine the run goes to and what a look at it
// says: the one named, which must hold the pinned jar (whoever names it, an
// operator or a card builder, chose it by its row's tla fact, so its rows are
// not read again here), or with "any" the record machine (a row with
// tla=true) holding it with the lowest load per CPU.
func pickBench(ctx context.Context, e env, o benchOpts, rows []benchRow, want string) (string, benchProbe, error) {
	look := func(m string) (benchProbe, error) {
		p, err := e.bench.dial(m).Probe(ctx, o.jar)
		switch {
		case err != nil:
			return p, &benchRefusal{"cannot reach " + m + ": " + err.Error(), "ssh " + m + " true"}
		case p.JarSHA256 == "":
			return p, &benchRefusal{m + " holds no jar at " + o.jar, playLine(m)}
		case p.JarSHA256 != want:
			return p, &benchRefusal{"the jar at " + o.jar + " on " + m + " is not the pinned TLC jar (sha256 " + p.JarSHA256 + "; tla/" + pinnedSumFile + " pins " + want + ")", playLine(m)}
		}
		return p, nil
	}
	if o.machine != "any" {
		p, err := look(o.machine)
		return o.machine, p, err
	}
	best, bestP, why := "", benchProbe{}, []string{}
	for _, r := range rows {
		if !r.TLA {
			continue
		}
		p, err := look(r.Name)
		if err != nil {
			why = append(why, err.Error())
			continue
		}
		if best == "" || p.Load/float64(max(p.CPUs, 1)) < bestP.Load/float64(max(bestP.CPUs, 1)) {
			best, bestP = r.Name, p
		}
	}
	if best == "" {
		cause := "no record machine holds the pinned jar"
		if len(why) > 0 {
			cause += ": " + strings.Join(why, "; ")
		}
		return "", benchProbe{}, &benchRefusal{cause, "nova-config machine list (a record machine says tla=true; nova-config machine set <machine> --tla true --as <actor> makes one), then " + playLine("<machine>")}
	}
	return best, bestP, nil
}

// playLine is the play that places and checks the jar on a record machine.
func playLine(m string) string {
	return "ansible-playbook -i ./nova-inventory fleet/tools.yml --tags tla --limit " + m + " | cat (it says how to place the jar)"
}

// stageArchive is the tar a bench run needs: every regular file at the top of
// the checkout's tla/ under tla/, and the bench's tlacheck as tlacheck.
func stageArchive(root, bin string) (io.Reader, int, error) {
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	add := func(name, src string, mode int64) error {
		raw, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(raw)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = tw.Write(raw)
		return err
	}
	entries, err := os.ReadDir(filepath.Join(root, "tla"))
	if err != nil {
		return nil, 0, fmt.Errorf("cannot read the models: %v", err)
	}
	files := 0
	for _, en := range entries {
		if !en.Type().IsRegular() {
			continue
		}
		if err := add("tla/"+en.Name(), filepath.Join(root, "tla", en.Name()), 0o644); err != nil {
			return nil, 0, err
		}
		files++
	}
	if err := add(tool, bin, 0o755); err != nil {
		return nil, 0, err
	}
	return &b, files + 1, tw.Close()
}

// fetchRun brings a bench run's records and logs into local, regular files
// at the top only.
func fetchRun(ctx context.Context, r remote, dir, local string) error {
	raw, err := r.Fetch(ctx, dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(local, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(h.Name)
		if h.Typeflag != tar.TypeReg || strings.Contains(name, "/") || name == "." || name == ".." {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, 64<<20))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(local, name), body, 0o644); err != nil {
			return err
		}
	}
}

// --- the transport: ssh, nova-config and go -------------------------------

// sshRemote is a bench reached as `ssh <machine>`: a fleet machine's name is
// its tailnet host. Each operation is one shell line.
type sshRemote struct{ machine string }

// stageLine makes a directory of its own under ~/tla-runs, unpacks the
// archive on stdin into it and prints it. A directory a run left behind (a
// crash between staging and removal) is removed by the next stage after a
// day, so nothing piles up.
const stageLine = `mkdir -p "$HOME/tla-runs" && { find "$HOME/tla-runs" -mindepth 1 -maxdepth 1 -type d -name 'tlacheck-*' -mmin +1440 -exec rm -r -- {} + 2>/dev/null; d=$(mktemp -d "$HOME/tla-runs/tlacheck-XXXXXX") && tar -x -C "$d" && printf '%s\n' "$d"; }`

func probeLine(jar string) string {
	return "uname -s -m && nproc && cat /proc/loadavg && { sha256sum -- " + shq(jar) + " 2>/dev/null || true; }"
}

const loadLine = "cat /proc/loadavg"

// runLine runs the staged tlacheck in dir, niced, as the bench's own work
// goes first.
func runLine(dir string, args []string) string {
	words := []string{"cd", shq(dir), "&&", "exec", "nice", "-n", "15", shq(path.Join(dir, tool))}
	for _, a := range args {
		words = append(words, shq(a))
	}
	return strings.Join(words, " ")
}

func fetchLine(dir string) string {
	return "cd " + shq(dir) + ` && find . -maxdepth 1 -type f \( -name ` + tlc.RunsFile + ` -o -name '*.log' \) -print0 | tar -c -f - --null -T -`
}

var stagedRE = regexp.MustCompile(`^/.+/tla-runs/tlacheck-[A-Za-z0-9]+$`)

// removeLine removes a directory stageLine made, and nothing else.
func removeLine(dir string) (string, error) {
	if path.Clean(dir) != dir || !stagedRE.MatchString(dir) {
		return "", fmt.Errorf("%s is not a directory run --bench staged (~/tla-runs/tlacheck-*); it is left", oneline.Quote(dir))
	}
	return "rm -r -- " + shq(dir), nil
}

// shq quotes a word for a POSIX shell.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// parseProbe reads probeLine's output: `uname -s -m`, nproc, /proc/loadavg
// and, when the jar is there, its sha256sum line.
func parseProbe(out string) (benchProbe, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		return benchProbe{}, fmt.Errorf("the probe printed %d lines, want at least 3: %s", len(lines), oneline.Quote(out))
	}
	u := strings.Fields(lines[0])
	if len(u) != 2 {
		return benchProbe{}, fmt.Errorf("uname printed %s", oneline.Quote(lines[0]))
	}
	arch := map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[u[1]]
	if arch == "" {
		arch = u[1]
	}
	cpus, err := strconv.Atoi(strings.TrimSpace(lines[1]))
	if err != nil || cpus < 1 {
		return benchProbe{}, fmt.Errorf("nproc printed %s", oneline.Quote(lines[1]))
	}
	load, err := parseLoad(lines[2])
	if err != nil {
		return benchProbe{}, err
	}
	p := benchProbe{GOOS: strings.ToLower(u[0]), GOARCH: arch, CPUs: cpus, Load: load}
	if len(lines) > 3 {
		if f := strings.Fields(lines[3]); len(f) > 0 && sha256RE.MatchString(f[0]) {
			p.JarSHA256 = f[0]
		}
	}
	return p, nil
}

// parseLoad reads the 1-minute load, /proc/loadavg's first field.
func parseLoad(line string) (float64, error) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return 0, errors.New("/proc/loadavg printed nothing")
	}
	l, err := strconv.ParseFloat(f[0], 64)
	if err != nil || l < 0 {
		return 0, fmt.Errorf("/proc/loadavg printed %s", oneline.Quote(line))
	}
	return l, nil
}

// do runs one line on the machine and returns its stdout; a non-zero exit is
// an error naming the line's first stderr line.
func (s sshRemote) do(ctx context.Context, limit time.Duration, line string, stdin io.Reader) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	var out, errs bytes.Buffer
	cmd := s.command(ctx, line)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &out, &errs
	if err := cmd.Run(); err != nil {
		first, _, _ := strings.Cut(strings.TrimSpace(errs.String()), "\n")
		return nil, fmt.Errorf("ssh %s: %v: %s", s.machine, err, first)
	}
	return out.Bytes(), nil
}

func (s sshRemote) command(ctx context.Context, line string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", s.machine, line)
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (s sshRemote) Probe(ctx context.Context, jar string) (benchProbe, error) {
	out, err := s.do(ctx, time.Minute, probeLine(jar), nil)
	if err != nil {
		return benchProbe{}, err
	}
	return parseProbe(string(out))
}

func (s sshRemote) Load(ctx context.Context) (float64, error) {
	out, err := s.do(ctx, time.Minute, loadLine, nil)
	if err != nil {
		return 0, err
	}
	return parseLoad(string(out))
}

func (s sshRemote) Stage(ctx context.Context, archive io.Reader) (string, error) {
	out, err := s.do(ctx, 5*time.Minute, stageLine, archive)
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if !stagedRE.MatchString(dir) {
		return "", fmt.Errorf("the stage printed %s, not a directory under ~/tla-runs", oneline.Quote(dir))
	}
	return dir, nil
}

func (s sshRemote) Run(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) (int, error) {
	cmd := s.command(ctx, runLine(dir, args))
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit) && exit.ExitCode() != 255 && ctx.Err() == nil:
		return exit.ExitCode(), nil
	}
	return 0, fmt.Errorf("ssh %s: %v", s.machine, err)
}

func (s sshRemote) Fetch(ctx context.Context, dir string) ([]byte, error) {
	return s.do(ctx, 5*time.Minute, fetchLine(dir), nil)
}

func (s sshRemote) Remove(ctx context.Context, dir string) error {
	line, err := removeLine(dir)
	if err != nil {
		return err
	}
	_, err = s.do(ctx, 5*time.Minute, line, nil)
	return err
}

// novaConfigMachines reads the fleet's machine rows from `nova-config machine
// list`, with this process's environment (NOVA_PG_DSN names the store).
func novaConfigMachines(ctx context.Context) ([]benchRow, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	var out, errs bytes.Buffer
	cmd := exec.CommandContext(ctx, "nova-config", "machine", "list")
	cmd.Stdout, cmd.Stderr = &out, &errs
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		first, _, _ := strings.Cut(strings.TrimSpace(errs.String()), "\n")
		if first == "" {
			first = err.Error()
		}
		return nil, errors.New(first)
	}
	return parseMachineList(out.String()), nil
}

// parseMachineList reads the MACHINE lines of `nova-config machine list`: the
// name, and tla=true for a record machine (a row with no tla field is none).
func parseMachineList(out string) []benchRow {
	var rows []benchRow
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || f[0] != "MACHINE" {
			continue
		}
		var r benchRow
		for _, kv := range f[1:] {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "name":
				r.Name = v
			case "tla":
				r.TLA = v == "true"
			}
		}
		if r.Name != "" {
			rows = append(rows, r)
		}
	}
	return rows
}

// goBuild builds the checkout's tlacheck for a platform, with no cgo, so it
// runs on the bench as it is.
func goBuild(ctx context.Context, root, goos, goarch, out string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", out, "./tools/tlacheck")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	cmd.WaitDelay = 5 * time.Second
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, oneline.Escape(strings.TrimSpace(string(b))))
	}
	return nil
}
