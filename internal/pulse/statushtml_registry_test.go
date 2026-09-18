package pulse

// red: `status --html` reads the fleet from a four-column benches.tsv a child had to invent
// on 2026-09-18, while the fleet's truth is the machines registry -- queue/control/machines.tsv,
// internal/fleet, seven columns and roles, the file every other refusal in the tool names. Two
// files that disagree about what the fleet IS is how a runner host becomes a bench.
//
// Here: --machines is the registry, benches are the rows with role `bench` and runners the rows
// with role `runner`; --benches still reads for one release and says so; and the repo the forge
// rows need is derived from the queue clone's origin when queue/REPO is absent, rather than the
// page printing "unknown (no repo to ask)" at a bench that has an origin to ask.
//
// No test here starts ssh or gh: the fleet reader is injected and gh and git are the package's
// fakes on PATH.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// registryFixture is the fleet as machines.tsv carries it: the studio that is never a bench,
// two shared bench/runner hosts, a runner too small to be a bench, and the Air, which is a
// bench WHILE UP and whose being unreachable is not a failure.
const registryFixture = "studio\tstudio\tdarwin/arm64\tcoordination,runner\tstudio\t32\tGlenn's Mac Studio: my window and the lisp CI leg. Never a card bench.\n" +
	"hulk\thulk\tlinux/x64\tbench,runner\tswarm-hulk\t64\tallow-shared=2026-09-18 eight CI runners beside the cards\n" +
	"vision\tvision\tlinux/x64\tbench,runner\tswarm-vision\t64\tallow-shared=2026-09-18 four CI runners beside the cards\n" +
	"mini\tmini\tlinux/x64\trunner\t-\t4\tone CI runner; 4 cores and 7 GB, too small for a card bench\n" +
	"air\tglenn@100.117.59.68\tdarwin/arm64\tbench,runner\tswarm-air\t8\tallow-shared=2026-09-18 Glenn's M2 MacBook Air: a fleet machine WHILE UP; unreachable is not a failure\n"

// regRun drives StatusHTML with the fleet reader injected, recording the benches it was handed.
type regRun struct {
	dir     string
	queue   string
	specs   string
	ghLog   string
	gitLog  string
	handed  []FleetBench
	reading map[string]BenchReading
	in      StatusHTMLInput
}

func newRegRun(t *testing.T) *regRun {
	t.Helper()
	r := &regRun{dir: t.TempDir(), queue: t.TempDir(), specs: fakePATH(t), reading: map[string]BenchReading{}}
	logs := t.TempDir()
	r.ghLog, r.gitLog = filepath.Join(logs, "gh.log"), filepath.Join(logs, "git.log")
	fakeTool(t, r.specs, "gh", fakeSpec{Log: r.ghLog, Default: fakeRule{Stdout: "[]"}})
	fakeTool(t, r.specs, "git", fakeSpec{Log: r.gitLog, Default: fakeRule{Exit: 1, Stderr: "fatal: not a git repository\n"}})
	now, _ := time.Parse(time.RFC3339, edgeStamp)
	r.in = StatusHTMLInput{
		HTML:  filepath.Join(r.dir, "index.html"),
		Queue: r.queue,
		Now:   func() time.Time { return now },
	}
	return r
}

// machines writes the registry and points the verb at it.
func (r *regRun) machines(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machines.tsv")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r.in.Machines = path
	return path
}

// benches writes the retired four-column file and points the verb at it.
func (r *regRun) benches(t *testing.T, names ...string) string {
	t.Helper()
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + "\tfake-" + n + "\t/tmp/" + n + "\t-\n")
	}
	path := filepath.Join(t.TempDir(), "benches.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	r.in.Benches = path
	return path
}

func (r *regRun) run(t *testing.T) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	in := r.in
	in.Stdout, in.Stderr = &out, &errs
	in.Reader = func(list []FleetBench, _ time.Time) []BenchReading {
		r.handed = list
		readings := make([]BenchReading, len(list))
		for i, b := range list {
			if fixed, ok := r.reading[b.Name]; ok {
				fixed.Name = b.Name
				readings[i] = fixed
				continue
			}
			readings[i] = BenchReading{Name: b.Name, Live: 1, Cores: 4, Load: 1, FreeGB: 90, MemGB: 30, Allowed: 2}
		}
		return readings
	}
	code := StatusHTML(in)
	return out.String(), errs.String(), code
}

func (r *regRun) page(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.dir, "index.html"))
	if err != nil {
		t.Fatalf("no page: %v", err)
	}
	return string(raw)
}

func (r *regRun) handedNames() []string {
	var out []string
	for _, b := range r.handed {
		out = append(out, b.Name)
	}
	return out
}

func (r *regRun) calls(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// status-machines-reads-the-registry: the benches read over ssh are the rows carrying role
// `bench`, in file order -- never the coordination host and never a runner too small to be one.
func TestStatusHTMLMachinesReadsBenchRowsFromTheRegistry(t *testing.T) {
	r := newRegRun(t)
	r.machines(t, registryFixture)
	_, errs, code := r.run(t)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	if got := strings.Join(r.handedNames(), ","); got != "hulk,vision,air" {
		t.Errorf("benches read = %q, want \"hulk,vision,air\" (the rows with role bench, in file order)", got)
	}
	for _, b := range r.handed {
		if b.SSH == "" {
			t.Errorf("bench %s was handed no ssh target from the registry", b.Name)
		}
	}
	if r.handed[2].SSH != "glenn@100.117.59.68" {
		t.Errorf("the Air's ssh target = %q, want the registry's glenn@100.117.59.68", r.handed[2].SSH)
	}
}

// status-machines-shows-the-runners: a page made from the registry says which machines serve
// the merge group's shards, so nobody has to ask "can I fill batman?" by hand again.
func TestStatusHTMLMachinesPageShowsTheRunners(t *testing.T) {
	r := newRegRun(t)
	r.machines(t, registryFixture)
	if _, errs, code := r.run(t); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	page := r.page(t)
	for _, want := range []string{"mini", "studio", "runner"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page from a registry never names %q:\n%s", want, page)
		}
	}
}

// status-machines-shows-the-air-while-up-note: the Air is a bench WHILE UP, and a page that
// shows it DOWN with no note reads as a broken fleet every time the laptop is shut.
func TestStatusHTMLMachinesShowsTheAirWhileUpNote(t *testing.T) {
	r := newRegRun(t)
	r.machines(t, registryFixture)
	r.reading["air"] = BenchReading{Down: true, Note: "no answer over ssh"}
	if _, errs, code := r.run(t); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	page := r.page(t)
	if !strings.Contains(page, "WHILE UP") {
		t.Errorf("the page does not carry the Air's registry note:\n%s", page)
	}
	if !strings.Contains(page, "unreachable is not a failure") {
		t.Errorf("the DOWN Air reads as a failure; the registry says it is not:\n%s", page)
	}
}

// status-machines-refuses-a-registry-with-no-bench: a registry all of whose machines are
// CI-only is a fleet with nowhere to put a card, and a page of nothing is not the answer.
func TestStatusHTMLMachinesRefusesARegistryWithNoBench(t *testing.T) {
	r := newRegRun(t)
	r.machines(t, "mini\tmini\tlinux/x64\trunner\t-\t4\tone CI runner\n")
	_, errs, code := r.run(t)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %s", code, errs)
	}
	if !strings.Contains(errs, "bench") {
		t.Errorf("the refusal does not say no machine carries the bench role: %s", errs)
	}
}

// status-benches-is-deprecated-for-one-release: the retired file still reads, and every run of
// it says once, on stderr, what to pass instead. A flag removed without a word is a page that
// stops being written on a bench nobody was watching.
func TestStatusHTMLBenchesStillReadsAndSaysItIsRetired(t *testing.T) {
	r := newRegRun(t)
	r.benches(t, "hulk", "vision")
	_, errs, code := r.run(t)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	if got := strings.Join(r.handedNames(), ","); got != "hulk,vision" {
		t.Errorf("benches read = %q, want \"hulk,vision\" from the four-column file", got)
	}
	if !strings.Contains(errs, "NOTE") || !strings.Contains(errs, "--machines") {
		t.Errorf("--benches printed no NOTE naming --machines: %s", errs)
	}
}

// status-machines-wins-over-benches: two files that disagree about the fleet is the bug this
// card is about, so the registry decides and the run says out loud which file it did not read.
func TestStatusHTMLMachinesWinsOverBenches(t *testing.T) {
	r := newRegRun(t)
	r.machines(t, registryFixture)
	path := r.benches(t, "batman", "superman")
	_, errs, code := r.run(t)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	if got := strings.Join(r.handedNames(), ","); got != "hulk,vision,air" {
		t.Errorf("benches read = %q, want the registry's; --benches must not decide when --machines is given", got)
	}
	if !strings.Contains(errs, path) {
		t.Errorf("the run never says it ignored %s: %s", path, errs)
	}
}

// status-wants-one-fleet-file: neither flag is a refusal that names the registry first.
func TestStatusHTMLWithoutMachinesOrBenchesRefuses(t *testing.T) {
	r := newRegRun(t)
	_, errs, code := r.run(t)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %s", code, errs)
	}
	if !strings.Contains(errs, "--machines") {
		t.Errorf("the refusal does not name --machines: %s", errs)
	}
}

// status-derives-the-repo-from-the-queue-origin: with no queue/REPO the page used to print
// "unknown (no repo to ask)" on a bench whose queue clone has an origin to ask.
func TestStatusHTMLDerivesRepoFromTheQueueOrigin(t *testing.T) {
	r := newRegRun(t)
	r.machines(t, registryFixture)
	fakeTool(t, r.specs, "git", fakeSpec{Log: r.gitLog, Default: fakeRule{Stdout: "git@github-rowan:mas-bandwidth/nova-tools.git\n"}})
	if _, errs, code := r.run(t); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	asked := strings.Join(r.calls(t, r.ghLog), "\n")
	if !strings.Contains(asked, "mas-bandwidth/nova-tools") {
		t.Errorf("the forge was never asked about the origin's repo; gh calls:\n%s", asked)
	}
	if strings.Contains(r.page(t), "no repo to ask") {
		t.Errorf("the page still says there is no repo to ask:\n%s", r.page(t))
	}
}

// status-repo-file-wins-over-the-origin: the REPO file is the declared answer, and a declared
// answer is never second-guessed by a remote a clone happens to carry.
func TestStatusHTMLRepoFileWinsOverTheOrigin(t *testing.T) {
	r := newRegRun(t)
	r.machines(t, registryFixture)
	if err := os.WriteFile(filepath.Join(r.queue, "REPO"), []byte("mas-bandwidth/nova\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeTool(t, r.specs, "git", fakeSpec{Log: r.gitLog, Default: fakeRule{Stdout: "git@github-rowan:mas-bandwidth/nova-tools.git\n"}})
	if _, errs, code := r.run(t); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	if asked := strings.Join(r.calls(t, r.ghLog), "\n"); !strings.Contains(asked, "mas-bandwidth/nova ") && !strings.Contains(asked, "mas-bandwidth/nova\n") {
		t.Errorf("the REPO file did not decide the repo; gh calls:\n%s", asked)
	}
	if calls := r.calls(t, r.gitLog); len(calls) != 0 {
		t.Errorf("git was asked for an origin although queue/REPO answers: %v", calls)
	}
}

// status-no-repo-anywhere-is-still-a-dash: no REPO file and no origin is "nobody asked", which
// is the dash rule this file has been through twice. A guess here is a number nobody measured.
func TestStatusHTMLWithNoRepoAnywhereStillSaysNobodyAsked(t *testing.T) {
	r := newRegRun(t)
	r.machines(t, registryFixture)
	if _, errs, code := r.run(t); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	if !strings.Contains(r.page(t), "no repo to ask") {
		t.Errorf("a queue with no REPO and no origin must still say nobody asked:\n%s", r.page(t))
	}
}

// status-registry-needs-no-home-column: the registry carries no home, and it needs none -- with
// no home the liveness script uses the login home, which is the home ssh lands in anyway.
func TestFleetStatusScriptWithNoHomeUsesTheLoginHome(t *testing.T) {
	script := fleetStatusScript("", "2026-09-18T11:00:00Z")
	if strings.Contains(script, "HOME=") || strings.Contains(script, "export HOME") {
		t.Errorf("the script sets HOME although the registry gave none:\n%s", script)
	}
	if !strings.Contains(script, `if [ ! -d "$HOME" ]`) {
		t.Errorf("the script no longer checks the home first:\n%s", script)
	}
	if named := fleetStatusScript("/home/gaffer", "2026-09-18T11:00:00Z"); !strings.Contains(named, "HOME='/home/gaffer'") {
		t.Errorf("a named home is no longer exported:\n%s", named)
	}
}
