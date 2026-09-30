package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The tests drive the witness against a fake bench: a real directory tree under
// t.TempDir() for the files the standard reads, and a host whose processes
// (ps, systemctl, df, du, every tool the witness runs) answer from a script. No
// real tool is started and nothing outside the temp directory is touched.

// answer is one scripted process: the first whose match holds is the reply.
type answer struct {
	match func(s runSpec) bool
	res   runResult
}

// fakeHost is the machine under test.
type fakeHost struct {
	osHost // the file probes, against the real temp directory

	mu         sync.Mutex
	osName     string
	environ    []string
	sourced    map[string]string
	sourceSaid string // what sourcing the sdk env file printed
	sourceEr   error
	answers    []answer
	cgroups    map[string]string
	ran        []string // every process started: "base arg arg"
	ranSpecs   []runSpec
	killed     []int
	made       []string // MkdirTemp results
	removed    []string // RemoveUnder paths
}

func (f *fakeHost) OS() string        { return f.osName }
func (f *fakeHost) Environ() []string { return f.environ }

func (f *fakeHost) SourceEnv(file string, environ []string) (map[string]string, string, error) {
	return f.sourced, f.sourceSaid, f.sourceEr
}

func (f *fakeHost) ProcCgroup(pid string) (string, bool) {
	cg, ok := f.cgroups[pid]
	return cg, ok
}

func (f *fakeHost) Kill(pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, pid)
	return nil
}

func (f *fakeHost) MkdirTemp(dir, pattern string) (string, error) {
	d, err := f.osHost.MkdirTemp(dir, pattern)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		f.made = append(f.made, d)
	}
	return d, err
}

func (f *fakeHost) RemoveUnder(root, p string) error {
	f.mu.Lock()
	f.removed = append(f.removed, p)
	f.mu.Unlock()
	return f.osHost.RemoveUnder(root, p)
}

var errNoAnswer = errors.New("the test scripted no answer for this process")

func (f *fakeHost) Run(s runSpec) runResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, strings.TrimSpace(filepath.Base(s.name)+" "+strings.Join(s.args, " ")))
	f.ranSpecs = append(f.ranSpecs, s)
	for _, a := range f.answers {
		if a.match(s) {
			return a.res
		}
	}
	return runResult{err: fmt.Errorf("%s %v: %w", s.name, s.args, errNoAnswer)}
}

// calls is how many processes whose base name is name the witness started.
func (f *fakeHost) calls(name string) []runSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []runSpec
	for _, s := range f.ranSpecs {
		if filepath.Base(s.name) == name {
			out = append(out, s)
		}
	}
	return out
}

// on scripts a reply for the program `base` started with args beginning with
// prefix; later scripts do not shadow earlier ones, so a test that changes an
// answer uses reply, which goes first.
func (f *fakeHost) on(base string, prefix []string, res runResult) {
	f.answers = append(f.answers, answer{match: matchBase(base, prefix), res: res})
}

// reply is on, but ahead of every answer already scripted.
func (f *fakeHost) reply(base string, prefix []string, res runResult) {
	f.answers = append([]answer{{match: matchBase(base, prefix), res: res}}, f.answers...)
}

// first scripts a reply for any process the function matches, ahead of every
// answer already scripted.
func (f *fakeHost) first(match func(runSpec) bool, res runResult) {
	f.answers = append([]answer{{match: match, res: res}}, f.answers...)
}

func matchBase(base string, prefix []string) func(runSpec) bool {
	return func(s runSpec) bool {
		if filepath.Base(s.name) != base || len(s.args) < len(prefix) {
			return false
		}
		for i, p := range prefix {
			if s.args[i] != p {
				return false
			}
		}
		return true
	}
}

func out(s string) runResult { return runResult{stdout: s} }

var failed = runResult{code: 1}

// bench is one fake bench: its home, the directory its PATH is, and its host.
type bench struct {
	t    *testing.T
	home string
	bin  string
	h    *fakeHost
}

const benchWant = "v9.9.9-bench"

// dfTable is what `df -Pk` prints for a filesystem with gb free.
func dfTable(gb int) string {
	return fmt.Sprintf("Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/fake 1048576000 1 %d 99%% /\n", gb*1048576)
}

func (b *bench) mkdir(rel ...string) string {
	b.t.Helper()
	p := filepath.Join(append([]string{b.home}, rel...)...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		b.t.Fatal(err)
	}
	return p
}

// write lays down a file of the bench; an executable when exec.
func (b *bench) write(rel string, body string, exec bool) string {
	b.t.Helper()
	p := filepath.Join(b.home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		b.t.Fatal(err)
	}
	mode := os.FileMode(0o644)
	if exec {
		mode = 0o755
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		b.t.Fatal(err)
	}
	return p
}

// stub puts an executable called name in the bench's PATH directory. What it
// contains does not matter: the host answers for it.
func (b *bench) stub(name string) string {
	b.t.Helper()
	p := filepath.Join(b.bin, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		b.t.Fatal(err)
	}
	return p
}

// sdkTool installs name under $HOME/sdk/<name>-<ver>/bin, the layout a
// standard bench keeps, and links it into the PATH directory.
func (b *bench) sdkTool(name, ver string) string {
	b.t.Helper()
	real := b.write(filepath.Join("sdk", name+"-"+ver, "bin", name), "#!/bin/sh\n", true)
	_ = os.Remove(filepath.Join(b.bin, name))
	if err := os.Symlink(real, filepath.Join(b.bin, name)); err != nil {
		b.t.Fatal(err)
	}
	return real
}

// setEnv sets one variable of the process environment, replacing an earlier one.
func (b *bench) setEnv(kv string) {
	k, _, _ := strings.Cut(kv, "=")
	kept := b.h.environ[:0:0]
	for _, e := range b.h.environ {
		if !strings.HasPrefix(e, k+"=") {
			kept = append(kept, e)
		}
	}
	b.h.environ = append(kept, kv)
}

func (b *bench) unsetEnv(k string) {
	kept := b.h.environ[:0:0]
	for _, e := range b.h.environ {
		if !strings.HasPrefix(e, k+"=") {
			kept = append(kept, e)
		}
	}
	b.h.environ = kept
}

// emptyBench is a home with nothing in it and a PATH that holds nothing: the
// bench of a machine that has never been provisioned.
func emptyBench(t *testing.T) *bench {
	t.Helper()
	root := t.TempDir()
	b := &bench{t: t, home: filepath.Join(root, "home"), bin: filepath.Join(root, "bin")}
	for _, d := range []string{b.home, b.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	b.h = &fakeHost{
		osName: "linux",
		environ: []string{
			"HOME=" + b.home,
			"PATH=" + b.bin,
			"NOVA_WANT=" + benchWant,
			"NOVA_GO=go1.26.5",
			"NOVA_PROBE_URL=https://probe.invalid/api.json",
			"NOVA_SLOT_SHARE=64",
		},
	}
	return b
}

// conformingBench is a bench that passes every check: the toolchain under
// $HOME/sdk and linked into PATH, every nova binary at the wanted version, a
// harness the wall starts, a network the wall reaches, one seat key the secrets
// tool accepts, and the disk free.
func conformingBench(t *testing.T) *bench {
	t.Helper()
	b := emptyBench(t)
	b.mkdir("go", "pkg", "mod")
	b.mkdir("nova-bench", "rungs", "pro")
	b.mkdir(".local", "bin")

	b.sdkTool("go", "go1.26.5")
	b.sdkTool("sbcl", defaultSBCL)
	b.sdkTool("sqlite3", "3.46.0")
	b.stub("curl")
	b.stub("df")
	b.stub("du")
	b.stub("nova-secrets")
	for _, n := range novaBins {
		b.write(filepath.Join(".local", "bin", n), "#!/bin/sh\n", true)
	}
	b.write("nova-bench/harness-v1/opencode", "#!/bin/sh\n", true)
	b.write(".config/nova-secrets/rows.key", "AGE-SECRET-KEY-FAKE\n", false)

	b.h.on("go", []string{"version"}, out("go version go1.26.5 linux/amd64\n"))
	b.h.on("sbcl", []string{"--version"}, out("SBCL "+defaultSBCL+"\n"))
	for _, n := range novaBins {
		b.h.on(n, []string{"version"}, out(benchWant+"\n"))
	}
	// The sandbox runs what follows `--`: the harness starts, and curl answers 200.
	b.h.on("nova-sandbox", []string{"--read"}, runResult{stdout: "200"})
	b.h.on("nova-secrets", []string{"check"}, runResult{})
	b.h.on("df", []string{"-Pk"}, out(dfTable(400)))
	return b
}

// standard runs the witness against the bench and returns its exit code and
// everything it printed.
func (b *bench) standard(args ...string) (int, string) {
	b.t.Helper()
	var buf bytes.Buffer
	code := run(args, env{stdout: &buf, h: b.h, exeDir: b.t.TempDir(), cwd: b.t.TempDir(), systemDir: b.t.TempDir()})
	return code, buf.String()
}

// drifts is the DRIFT lines of an output.
func drifts(output string) []string {
	var lines []string
	for _, l := range strings.Split(output, "\n") {
		if strings.HasPrefix(l, "DRIFT ") {
			lines = append(lines, l)
		}
	}
	return lines
}

// driftWith is the DRIFT lines that start with prefix after "DRIFT ".
func driftWith(output, prefix string) []string {
	var lines []string
	for _, l := range drifts(output) {
		if strings.HasPrefix(l, "DRIFT "+prefix) {
			lines = append(lines, l)
		}
	}
	return lines
}

// wantOnlyDrift fails the test unless the run drifted, and every DRIFT line
// starts with prefix (after "DRIFT ").
func wantOnlyDrift(t *testing.T, code int, output, prefix string) {
	t.Helper()
	if code != 1 {
		t.Errorf("exit %d, want 1:\n%s", code, output)
	}
	got := drifts(output)
	if len(got) == 0 {
		t.Fatalf("no DRIFT line, want one starting %q:\n%s", prefix, output)
	}
	for _, l := range got {
		if !strings.HasPrefix(l, "DRIFT "+prefix) {
			t.Errorf("a DRIFT line that is not %q:\n%s", prefix, l)
		}
	}
	if !strings.Contains(output, "STANDARD DRIFT (see lines above)") {
		t.Errorf("no STANDARD DRIFT verdict:\n%s", output)
	}
}
