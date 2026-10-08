package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// cmdAdopt is the adoption pipeline (internal/sprint/adopt.go;
// docs/SPRINT-COORDINATOR.md, "Adoption is a pipeline"). One run is one pass:
// it starts an adoption when the base tip is not the live build's commit and
// moves it as far as it can go. --answer is the coordinator's answer to the
// one judgment it raises.
//
// The verb `adopt` is the seat's adoption through the fleet play
// (adopt_play.go, cmdAdoptPlay); this pipeline, cmdAdopt, is in no verb
// table and the tick does not call its pass. run.go's tick and the store
// tick call no adoption hook; the tick's end parts are planners, and the
// shadow tick calls the same functions, so a pass there would write during a
// read-only plan and hold the tick.
func init() {
	// it builds over ssh, switches binaries on disk and pushes to the fleet:
	// it runs where it is typed or scheduled, never on the server
	notServed = append(notServed, "adopt")
	// the window reads this host's process table for the seat play: the seat
	// runs it where the agents ran, and the server serves it to nobody
	verbClasses["window"] = classRead
	notServed = append(notServed, "window")
	verbExit["window"] = "exit codes: 0 every agent the adopt stopped exited within --window (a process of the seat's binary the adopt did not stop is printed as OTHER and ignored), 1 an agent the adopt stopped still runs at the bound (the refusal names it by pid and label; nothing was migrated), 2 usage"
	verbEffect["window"] = "reads only: the seat's process table (ps) until each agent the adopt stopped has exited, or --window passes; the other processes of the seat's nova-sprint are printed as OTHER and ignored"
}

// adoptRunner runs one command and returns its combined output: exec in
// production, a fake in a test.
type adoptRunner func(ctx context.Context, name string, args ...string) (string, error)

func execAdoptRunner(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	s := strings.TrimSpace(out.String())
	if err != nil {
		return s, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, adoptLastLine(s))
	}
	return s, nil
}

func adoptLastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return oneline.Escape(s)
}

// adoptSteps is the production AdoptSteps: every stage the 2026-10-04 hand
// adoption did, as the commands it ran.
type adoptSteps struct {
	a   *app
	c   common
	run adoptRunner
	out io.Writer

	repoDir, base   string   // the clone the base tip is read from, and the branch
	serverBin       string   // the live server binary
	daemons         []string // the friends' daemon binaries
	bench           string   // the bench the build runs on
	benchSrc        string   // its clone
	benchOut        string   // its release output directory
	local           string   // where the build is fetched to here
	release         string   // the release number the build carries a suffix of
	machinesFile    string   // the fleet's machines file
	adoptArgs       []string // the rest of nova-update release adopt's flags
	stream          string   // the cold-read card's stream
	judgmentTo      string   // the file the judgment is appended to, "" for none
	shadowDeadline  time.Duration
	platform        string
	versionMachines map[string]string // the remote bin directory of each machine row
}

func (s *adoptSteps) BaseTip(ctx context.Context) (string, error) {
	out, err := s.run(ctx, "git", "-C", s.repoDir, "ls-remote", "origin", "refs/heads/"+s.base)
	if err != nil {
		return "", err
	}
	tip, _, _ := strings.Cut(out, "\t")
	if len(strings.TrimSpace(tip)) < 40 {
		return "", fmt.Errorf("origin has no branch %s", s.base)
	}
	return strings.TrimSpace(tip), nil
}

// revisionOf is the revision a version line names.
func revisionOf(line string) (string, error) {
	f, ok := buildinfo.Parse(line)
	if !ok {
		return "", fmt.Errorf("not a version line: %q", adoptLastLine(line))
	}
	src, ok := f.FindSource()
	if !ok {
		return "", fmt.Errorf("the version line names no source revision: %q", adoptLastLine(line))
	}
	return src.Revision, nil
}

func (s *adoptSteps) LiveBuild(ctx context.Context) (string, error) {
	out, err := s.run(ctx, s.serverBin, "version")
	if err != nil {
		return "", err
	}
	return revisionOf(out)
}

func (s *adoptSteps) version(tip string) string {
	return s.release + "-adopt." + tip[:12]
}

func (s *adoptSteps) Build(ctx context.Context, tip string) (sprint.AdoptBuild, error) {
	v := s.version(tip)
	for _, argv := range [][]string{
		{"git", "-C", s.benchSrc, "fetch", "--quiet", "origin", s.base},
		{"git", "-C", s.benchSrc, "checkout", "--quiet", "--detach", tip},
		{"nova-update", "release", "build", "--version", v, "--out", s.benchOut, "--source", s.benchSrc},
	} {
		if _, err := s.run(ctx, "ssh", append([]string{"--", s.bench}, argv...)...); err != nil {
			return sprint.AdoptBuild{}, err
		}
	}
	dir := filepath.Join(s.local, v)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return sprint.AdoptBuild{}, err
	}
	if _, err := s.run(ctx, "rsync", "-a", s.bench+":"+s.benchOut+"/"+v+"/", dir+"/"); err != nil {
		return sprint.AdoptBuild{}, err
	}
	return sprint.AdoptBuild{Tip: tip, Version: v, Dir: dir}, nil
}

// candidate is the built nova-sprint for this host's platform.
func (s *adoptSteps) candidate(b sprint.AdoptBuild) string {
	return filepath.Join(b.Dir, s.platform, "nova-sprint")
}

// daemonCandidate is the nova-friend artifact from the adopted release. A
// daemon's target path is installation-specific, but the binary it runs is
// always nova-friend (docs/SPRINT-COORDINATOR.md, "Adoption is a pipeline").
func (s *adoptSteps) daemonCandidate(b sprint.AdoptBuild) string {
	return filepath.Join(b.Dir, s.platform, "nova-friend")
}

func (s *adoptSteps) Canary(ctx context.Context, b sprint.AdoptBuild) (string, error) {
	bin := s.candidate(b)
	arts, err := release.ReadSums(filepath.Dir(bin))
	if err != nil {
		return "", err
	}
	n, err := release.VerifyArtifacts(filepath.Dir(bin), arts)
	if err != nil {
		return "", err
	}
	out, err := s.run(ctx, bin, "version")
	if err != nil {
		return "", err
	}
	rev, err := revisionOf(out)
	if err != nil {
		return "", err
	}
	if !sprint.SameCommit(rev, b.Tip) {
		return "", fmt.Errorf("the built nova-sprint names revision %s, not the tip %s", rev, b.Tip)
	}
	return fmt.Sprintf("verified=%d revision=%s", n, rev), nil
}

func (s *adoptSteps) Shadow(ctx context.Context, b sprint.AdoptBuild) (string, error) {
	plan, wall, err := runShadow(ctx, s.candidate(b), s.c.redis, s.shadowDeadline)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("epoch=%d state=%s parts=%d size=%d wall=%s", plan.Epoch, plan.State, len(plan.Parts), plan.Size, wall.Round(time.Millisecond)), nil
}

func (s *adoptSteps) readCard(tip string) string { return "adopt-read-" + tip[:12] }

func (s *adoptSteps) DealColdRead(_ context.Context, b sprint.AdoptBuild) (string, error) {
	id := s.readCard(b.Tip)
	brief := fmt.Sprintf("Cold read of the build %s, made from %s of %s. Read the commits between the live build and this tip "+
		"(git log --oneline <live>..%s) and the canary and shadow evidence in the adoption record; say whether anything in them "+
		"should stop this build becoming the live server. End the report with one line: READ OK, or READ BROKEN: <the finding>.",
		b.Version, b.Tip, s.base, b.Tip)
	var out, errs bytes.Buffer
	args := []string{"add", "--stream", s.stream, "--one", id, "--brief", brief}
	if s.c.redis != "" {
		args = append(args, "--redis", s.c.redis)
	}
	if code := s.a.run(args, &out, &errs); code != 0 {
		return "", fmt.Errorf("add %s exited %d: %s", id, code, adoptLastLine(errs.String()))
	}
	return id, nil
}

func (s *adoptSteps) ColdRead(ctx context.Context, card string) (sprint.AdoptRead, error) {
	st, err := s.a.store(s.c)
	if err != nil {
		return sprint.AdoptRead{}, err
	}
	v, err := st.CardOf(ctx, card)
	if err != nil {
		return sprint.AdoptRead{}, err
	}
	if v.Primary == nil {
		return sprint.AdoptRead{}, fmt.Errorf("no card %s on the table", card)
	}
	switch v.Primary.Col {
	case sprint.Review, sprint.Merging, sprint.Landed:
	default:
		return sprint.AdoptRead{}, nil
	}
	report := ""
	for _, w := range v.Work {
		if r := w.F("report"); r != "" {
			report = r
		}
	}
	return coldReadOf(report), nil
}

// coldReadOf reads the cold-read card's report: OK only on READ OK; a
// report that says neither is a broken read, naming what it said.
func coldReadOf(report string) sprint.AdoptRead {
	r := sprint.AdoptRead{Done: true}
	switch i := strings.LastIndex(report, "READ "); {
	case i < 0:
		r.Finding = "the report says neither READ OK nor READ BROKEN: " + adoptLastLine(report)
	case strings.HasPrefix(report[i:], "READ OK"):
		r.OK, r.Finding = true, strings.TrimSpace(report[:i])
	default:
		r.Finding = strings.TrimSpace(strings.TrimPrefix(report[i:], "READ BROKEN:"))
	}
	r.Finding = adoptLastLine(r.Finding)
	return r
}

func (s *adoptSteps) Ask(_ context.Context, j sprint.AdoptJudgment) error {
	if s.judgmentTo == "" {
		return nil
	}
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.judgmentTo, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close() // ignored: the write already failed, and that error is returned
		return err
	}
	return f.Close()
}

// keptName is where a binary's rollback copy is kept.
func keptName(path string) string { return path + ".adopt-prev" }

func (s *adoptSteps) binaries() []string { return append([]string{s.serverBin}, s.daemons...) }

func (s *adoptSteps) KeepRollback(context.Context) ([]string, error) {
	var kept []string
	for _, p := range s.binaries() {
		if err := copyFile(p, keptName(p)); err != nil {
			return kept, fmt.Errorf("the rollback copy of %s: %w", p, err)
		}
		kept = append(kept, keptName(p))
	}
	return kept, nil
}

func (s *adoptSteps) Switch(ctx context.Context, b sprint.AdoptBuild) error {
	if err := sprint.ServerSwitch(ctx, sprint.ServerSwitchOptions{Binary: s.candidate(b), Target: s.serverBin, Now: s.a.now}); err != nil {
		return err
	}
	for _, daemon := range s.daemons {
		if err := sprint.ServerSwitch(ctx, sprint.ServerSwitchOptions{Binary: s.daemonCandidate(b), Target: daemon, Now: s.a.now}); err != nil {
			return err
		}
	}
	return nil
}

func (s *adoptSteps) Rollback(_ context.Context, kept []string) error {
	var errs []error
	for _, k := range kept {
		if err := copyFile(k, strings.TrimSuffix(k, ".adopt-prev")); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *adoptSteps) machines() ([]release.Machine, error) {
	f, err := os.Open(s.machinesFile)
	if err != nil {
		return nil, err
	}
	defer f.Close() // ignored: a read-only file
	return release.Machines(f)
}

func (s *adoptSteps) Machines(context.Context) ([]string, error) {
	ms, err := s.machines()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, m := range ms {
		names = append(names, m.Name)
	}
	return names, nil
}

// Push runs the build's nova-update release adopt for the one machine's row, so one
// machine that does not answer is named and the rest are still reached.
func (s *adoptSteps) Push(ctx context.Context, machine string, b sprint.AdoptBuild) error {
	ms, err := s.machines()
	if err != nil {
		return err
	}
	row := ""
	for _, m := range ms {
		if m.Name == machine {
			row = strings.Join([]string{m.Name, m.Bin, m.Dest}, "\t")
			row = strings.TrimRight(row, "\t")
			if s.versionMachines != nil {
				s.versionMachines[m.Name] = m.Bin
			}
		}
	}
	if row == "" {
		return fmt.Errorf("no row %s in %s", machine, s.machinesFile)
	}
	one, err := os.CreateTemp("", "nova-sprint-adopt-machine-*")
	if err != nil {
		return err
	}
	defer os.Remove(one.Name()) // ignored: best-effort cleanup of the one-row file
	if _, err := one.WriteString(row + "\n"); err != nil {
		_ = one.Close() // ignored: the write already failed, and that error is returned
		return err
	}
	if err := one.Close(); err != nil {
		return err
	}
	args := append([]string{"release", "adopt", "--version", b.Version, "--machines", one.Name(), "--from", s.local}, s.adoptArgs...)
	// the build's own nova-update fans out, so the tool running the install is
	// never older than the release (docs/SPEC-RELEASE.md section 8)
	_, err = s.run(ctx, filepath.Join(b.Dir, s.platform, "nova-update"), args...)
	return err
}

// Version reads the machine's nova-sprint back over ssh: its version line's
// release field, the one release adopt installed.
func (s *adoptSteps) Version(ctx context.Context, machine string) (string, error) {
	bin := "nova-sprint"
	if d := s.versionMachines[machine]; d != "" {
		bin = release.RemotePath(d) + "/nova-sprint"
	}
	out, err := s.run(ctx, "ssh", "--", machine, bin, "version")
	if err != nil {
		return "", err
	}
	f, ok := buildinfo.Parse(out)
	if !ok {
		return "", fmt.Errorf("not a version line: %q", adoptLastLine(out))
	}
	return f.Version, nil
}

func (s *adoptSteps) LastTick(ctx context.Context) (time.Time, error) {
	st, err := s.a.store(s.c)
	if err != nil {
		return time.Time{}, err
	}
	_, hb, err := st.Machine(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return hb.Alive(), nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, fi.Mode()|0o100); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// adoptRecordPath is the record's default place.
func (a *app) adoptRecordPath() string {
	home := a.getenv("HOME")
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".local", "state", "nova-sprint", "adopt.json")
}

// adoptStepsOf is a test's steps for one app (*app to sprint.AdoptSteps): an
// app it does not hold builds the production steps. Keyed by the app, so
// tests that each give their own run in parallel.
var adoptStepsOf sync.Map

func (a *app) cmdAdopt(args []string, stdout, stderr io.Writer) int {
	const name = "adopt"
	fs, c := a.verbSetup(name)
	state := fs.String("state", "", "the adoption record, a JSON file kept between passes (default: ~/.local/state/nova-sprint/adopt.json)")
	answer := fs.String("answer", "", "answer the open judgment, yes or no, and run nothing else (with --judgment and --reason)")
	judgment := fs.String("judgment", "", "the judgment --answer answers: the tip it names")
	reason := fs.String("reason", "", "why, with --answer")
	show := fs.Bool("show", false, "print the record and run nothing")
	repoDir := fs.String("repo-dir", "", "a clone of the tools repository, whose origin's base branch is read")
	base := fs.String("base", "", "the sprint base branch")
	serverBin := fs.String("server-bin", a.getenv("NOVA_SPRINT_SERVER_BIN"), "the live server binary (else NOVA_SPRINT_SERVER_BIN)")
	var daemons stringList
	fs.Var(&daemons, "daemon", "a friend's daemon binary switched and rolled back with the server (repeatable)")
	bench := fs.String("bench", "", "the bench the build runs on, over ssh; never this host")
	benchSrc := fs.String("bench-src", "", "the bench's clone of the tools repository")
	benchOut := fs.String("bench-out", "", "the bench's release output directory")
	local := fs.String("out", "", "where the build is fetched to on this host")
	rel := fs.String("release", "", "the release number the build's version is a suffix of, vX.Y.Z: the build is <release>-adopt.<tip12>")
	machinesFile := fs.String("machines", "", "the machines file, every row of it pushed to, funded or not ("+release.MachinesShape+")")
	adoptArgsFlag := fs.String("adopt-args", "", "the rest of nova-update release adopt's flags, split on blanks (--ssh, --bin, --dest, --stage and the digest, --certify or --no-certify)")
	stream := fs.String("stream", "adopt", "the stream the cold-read card is added to")
	judgmentTo := fs.String("judgment-to", "", "a file the judgment is appended to as one JSON line, where the coordinator is woken from")
	every := fs.Duration("tick-every", sprint.DefaultAdoptTickEvery, "the server's tick interval")
	missed := fs.Int("missed", sprint.DefaultAdoptMissed, "tick intervals with no tick after a switch that roll it back")
	watch := fs.Int("watch", sprint.DefaultAdoptWatch, "tick intervals after a switch, every one ticked, that adopt it")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	path := *state
	if path == "" {
		path = a.adoptRecordPath()
	}
	rec := sprint.FileAdoptStore{Path: path}
	ctx := context.Background()
	if *show {
		r, err := rec.Load(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "%s adopt: %s\n", prog, oneline.Err(err))
			return 1
		}
		b, _ := json.Marshal(r)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if *answer != "" {
		if *judgment == "" || strings.TrimSpace(*reason) == "" {
			return refuse(stderr, name, "--answer wants --judgment <tip> and --reason <text>; run: nova-sprint adopt --show")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintf(stderr, "%s adopt: %s\n", prog, oneline.Err(err))
			return 1
		}
		r, err := sprint.AnswerAdoption(ctx, rec, *judgment, *answer, *reason)
		if err != nil {
			fmt.Fprintf(stderr, "%s adopt: the answer was not recorded: %s\n", prog, oneline.Err(err))
			return 1
		}
		fmt.Fprintf(stdout, "ADOPT ANSWERED judgment=%s answer=%s: the next pass acts on it\n", r.Judgment.ID, r.Answer)
		return 0
	}
	var steps sprint.AdoptSteps
	if fake, ok := adoptStepsOf.Load(a); ok {
		steps = fake.(sprint.AdoptSteps)
	} else {
		var missing []string
		for flag, v := range map[string]string{"repo-dir": *repoDir, "base": *base, "server-bin": *serverBin, "bench": *bench, "bench-src": *benchSrc, "bench-out": *benchOut, "out": *local, "release": *rel, "machines": *machinesFile} {
			if strings.TrimSpace(v) == "" {
				missing = append(missing, "--"+flag)
			}
		}
		if len(missing) > 0 {
			return refuse(stderr, name, "a pass wants "+strings.Join(slices.Sorted(slices.Values(missing)), ", ")+"; nothing was run")
		}
		if err := release.ValidVersion(*rel); err != nil {
			return refuse(stderr, name, "--release: "+oneline.Err(err))
		}
		steps = &adoptSteps{a: a, c: *c, run: execAdoptRunner, out: stdout, repoDir: *repoDir, base: *base, serverBin: *serverBin,
			daemons: []string(daemons), bench: *bench, benchSrc: *benchSrc, benchOut: *benchOut, local: *local, release: *rel,
			machinesFile: *machinesFile, adoptArgs: strings.Fields(*adoptArgsFlag), stream: *stream, judgmentTo: *judgmentTo,
			shadowDeadline: TickDeadline, platform: runtime.GOOS + "-" + runtime.GOARCH, versionMachines: map[string]string{}}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(stderr, "%s adopt: %s\n", prog, oneline.Err(err))
		return 1
	}
	p, err := (&sprint.Adoption{Steps: steps, Store: rec, Now: a.now, TickEvery: *every, Missed: *missed, Watch: *watch}).Pass(ctx)
	for _, l := range p.Lines {
		fmt.Fprintln(stdout, oneline.Escape(l))
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s adopt: %s\n", prog, oneline.Err(err))
		return 1
	}
	return 0
}

// cmdWindow is the adoption window's wait (docs/SPEC-SPRINT.md, "Adopting a
// build"; internal/sprint/adopt_window.go): the seat play boots out the old
// server, the member and every other loaded nova agent but the friends, then
// runs this verb with each stopped agent as --stopped <pid>:<label>. It reads
// the seat's process table and waits for those pids alone, until each has
// exited or --window passes. A process of the seat's own nova-sprint that the
// adopt did not stop is somebody else's work (the dashboard's poll, a
// person's verb): it is printed as OTHER <pid> <argv> and ignored, because
// the binary is replaced by rename and a running process keeps its inode.
// Only an agent the adopt itself stopped refuses, named by pid and label.
func (a *app) cmdWindow(args []string, stdout, stderr io.Writer) int {
	const name = "window"
	fs, c := a.verbSetup(name)
	binDir := fs.String("bin-dir", "", "the bin directory whose nova-sprint is the seat's binary (default: ~/.local/bin)")
	var stopped stringList
	fs.Var(&stopped, "stopped", "an agent the adopt stopped, <pid>:<label> (repeatable)")
	bound := fs.Duration("window", sprint.DefaultAdoptWindow, "how long each stopped agent gets to exit before the window refuses")
	every := fs.Duration("every", sprint.DefaultAdoptWindowEvery, "how often the process table is read while waiting")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	agents, err := windowAgents(stopped)
	if err != nil {
		return refuse(stderr, name, oneline.Err(err))
	}
	if strings.TrimSpace(*binDir) == "" {
		*binDir = filepath.Join(a.getenv("HOME"), ".local", "bin")
	}
	sweeper := windowSweeper{binDir: *binDir, stopped: map[int]bool{}, run: execAdoptRunner}
	for _, ag := range agents {
		sweeper.stopped[ag.PID] = true
	}
	now := a.now
	if now == nil {
		now = time.Now
	}
	ctx := context.Background()
	res, err := sprint.WaitAdoptWindow(sprint.AdoptWindowOptions{
		Stopped: agents, Bound: *bound, Every: *every, Now: now,
		Sweep: func() ([]sprint.AdoptWindowProcess, error) { return sweeper.sweep(ctx) },
	})
	if err != nil {
		fmt.Fprintf(stderr, "%s window: %s; run: nova-sprint live\n", prog, oneline.Err(err))
		return 1
	}
	if c.json {
		b, _ := json.Marshal(res) // ignored: a struct of numbers, strings and slices always encodes
		fmt.Fprintln(stdout, string(b))
		if res.OK() {
			return 0
		}
		return 1
	}
	if !res.OK() {
		fmt.Fprintf(stderr, "%s window REFUSED: %s; run: nova-sprint live\n", prog, oneline.Escape(res.Refusal()))
		return 1
	}
	for _, l := range res.OtherLines() {
		fmt.Fprintln(stdout, oneline.Escape(l))
	}
	fmt.Fprintln(stdout, res.Line())
	return 0
}

// windowAgents parses the repeated --stopped <pid>:<label> flags: the agents
// the adopt stopped, as the pre-window manifest read their pids.
func windowAgents(specs []string) ([]sprint.AdoptWindowAgent, error) {
	var out []sprint.AdoptWindowAgent
	for _, s := range specs {
		pid, label, ok := strings.Cut(s, ":")
		if !ok {
			return nil, fmt.Errorf("--stopped wants <pid>:<label>, not %q", s)
		}
		n, err := strconv.Atoi(strings.TrimSpace(pid))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("--stopped %q: the pid is not a positive number", s)
		}
		label = strings.TrimSpace(label)
		if label == "" {
			return nil, fmt.Errorf("--stopped %d: the label is empty", n)
		}
		out = append(out, sprint.AdoptWindowAgent{PID: n, Label: label})
	}
	return out, nil
}

// windowSweeper reads the seat's process table for the window: every process
// of this bin directory's nova-sprint (the binary, a bare name found on PATH
// and a link followed, as live reads it), and every process whose pid is an
// agent the adopt stopped, whatever it runs.
type windowSweeper struct {
	binDir  string
	stopped map[int]bool
	run     adoptRunner
}

func (s windowSweeper) sweep(ctx context.Context) ([]sprint.AdoptWindowProcess, error) {
	out, err := s.run(ctx, "ps", "-A", "-o", "pid=,args=")
	if err != nil {
		return nil, err
	}
	bin := s.binDir
	if b, err := filepath.EvalSymlinks(bin); err == nil {
		bin = b
	}
	self := os.Getpid()
	procs := []sprint.AdoptWindowProcess{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil || pid == self {
			continue
		}
		path := f[1]
		if !strings.Contains(path, "/") {
			if found, err := exec.LookPath(path); err == nil {
				path = found
			}
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		mine := filepath.Dir(path) == bin && filepath.Base(path) == "nova-sprint"
		if !mine && !s.stopped[pid] {
			continue
		}
		procs = append(procs, sprint.AdoptWindowProcess{PID: pid, Args: strings.Join(f[1:], " "), Mine: mine})
	}
	return procs, nil
}
