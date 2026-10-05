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
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// adopt is the adoption pipeline's verb (internal/sprint/adopt.go;
// docs/SPRINT-COORDINATOR.md, "Adoption is a pipeline"). One run is one pass:
// it starts an adoption when the base tip is not the live build's commit and
// moves it as far as it can go. --answer is the coordinator's answer to the
// one judgment it raises.
//
// This file does not register the verb and the tick does not call the pass.
// verbs.go's init assigns the verb table and runs after this file (adopt.go
// sorts first), so an append here is replaced. run.go's tick and the store
// tick call no adoption hook; the tick's end parts are planners, and the
// shadow tick calls the same functions, so a pass there would write during a
// read-only plan and hold the tick. verbClasses is not set: a class with no
// verb fails TestEveryVerbHasAClass.
func init() {
	// it builds over ssh, switches binaries on disk and pushes to the fleet:
	// it runs where it is typed or scheduled, never on the server
	notServed = append(notServed, "adopt")
	verbExit["adopt"] = "exit codes: 0 the pass ran (whatever stage it left: CURRENT, WAIT, JUDGMENT, BLOCKED, ROLLED BACK are all lines, not failures) or the answer was recorded, 1 a step it could not read (the base, the live build, the record) or an answer refused, 2 usage"
	verbEffect["adopt"] = "local and remote writes: on a base past the live build, builds on --bench, runs the candidate's canary and shadow tick, adds the cold-read card, and on a yes copies the server and daemon binaries aside, switches them, and runs nova-update release adopt to every --machines row; --answer writes only the record"
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
