package swarm

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// THE FAKE RUNNER IS AN EXECUTABLE, NOT A SHELL SCRIPT (windows leg, 2026-09-15).
//
// These tests used to hand `nova-swarm batch --runner` the path of a POSIX shell script they
// had just written. The product does not run a shell: SPEC-SWARM's usage line is
// `nova-swarm batch --id <id> --cards <file> --deadline <seconds> --runner <cmd> --root <dir>`,
// batch.go refuses with "--runner is required; it wants the command one process per card
// runs", and it starts that command with
// `exec.Command(in.Runner, c.label, strconv.Itoa(c.slot), c.model, c.cardPath, in.Root)`.
// A `.sh` file is therefore a FIXTURE choice, and on windows it is a wrong one: every card
// died with `fork/exec C:\...\runner.sh: %1 is not a valid Win32 application`.
//
// So there is one fixture program (testdata/fakerunner), built ONCE for the whole package,
// and each fixture is a COPY of it under its own name with its behaviour in a JSON file
// beside it. One build, not one per test: a build per fixture would cost this package more
// than the tests themselves.

// runnerStep mirrors the step type in testdata/fakerunner/main.go; the JSON between them is
// the contract. See that file for what each op does.
type runnerStep struct {
	Op   string `json:"op"`
	Path string `json:"path,omitempty"`
	Body string `json:"body,omitempty"`
	N    int    `json:"n,omitempty"`
	Ms   int    `json:"ms,omitempty"`
	When string `json:"when,omitempty"`
}

var (
	fakeRunnerOnce sync.Once
	fakeRunnerBin  string
	fakeRunnerDir  string
	fakeRunnerErr  error
)

// builtFakeRunner builds testdata/fakerunner once per package run and returns its path.
func builtFakeRunner(t *testing.T) string {
	t.Helper()
	fakeRunnerOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nova-swarm-fakerunner")
		if err != nil {
			fakeRunnerErr = err
			return
		}
		fakeRunnerDir = dir
		bin := filepath.Join(dir, "fakerunner")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			fakeRunnerErr = err
			return
		}
		cmd := exec.Command("go", "build", "-o", bin, "./internal/swarm/testdata/fakerunner")
		cmd.Dir = root
		cmd.Env = goenv.Clean(os.Environ())
		if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
			fakeRunnerErr = &buildError{out: string(out), err: cmdErr}
			return
		}
		fakeRunnerBin = bin
	})
	if fakeRunnerErr != nil {
		t.Fatalf("building the fake runner these tests drive: %v", fakeRunnerErr)
	}
	return fakeRunnerBin
}

type buildError struct {
	out string
	err error
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.out }

// TestMain removes the one directory these tests keep outside a t.TempDir(): the fake runner
// every fixture is copied from, which cannot live in any single test's own directory because
// every test shares it.
func TestMain(m *testing.M) {
	code := m.Run()
	if fakeRunnerDir != "" {
		_ = os.RemoveAll(fakeRunnerDir)
	}
	os.Exit(code)
}

// runnerDoing returns the path of a fake runner, under `name` in `dir`, that does `steps`.
// The binary is a copy of the one built for this package and the steps are a JSON file
// beside it, so the fixture is a real executable on every platform -- which is what
// `--runner <cmd>` names.
func runnerDoing(t *testing.T, dir, name string, steps ...runnerStep) string {
	t.Helper()
	src := builtFakeRunner(t)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading the fake runner: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// A hard link, not a copy, wherever the filesystem allows it. On macOS every fresh
	// COPY of an executable is a never-seen binary that the system policy scanner assesses
	// on its first exec; one is quick, but a package run places dozens at once and they
	// queue behind the scanner for longer than a test will wait (batman and the Studio,
	// 2026-09-17: result-after-deadline timed out at 30 s only in the full package run).
	// A link shares the inode the scanner has already passed. The copy stays as the
	// fallback for a temp directory on another filesystem, and for Windows.
	_ = os.Remove(path)
	if runtime.GOOS == "windows" || os.Link(src, path) != nil {
		if err := os.WriteFile(path, raw, 0o755); err != nil {
			t.Fatalf("placing the fake runner at %s: %v", path, err)
		}
	}
	if steps == nil {
		steps = []runnerStep{}
	}
	body, err := json.Marshal(struct {
		Steps []runnerStep `json:"steps"`
	}{steps})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".json", body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// publishCard is the step every runner that "does the work" ends with: RESULT.md whose line 1
// is the card's contract line and line 2 its disposition, under the job directory. `into` is
// the directory it lands in -- "{job}" for a worker that published where it was told, or
// "{job}/repo" for one that published in its clone (issue #594).
func publishCard(into string) runnerStep {
	return runnerStep{Op: "write", Path: into + "/RESULT.md", Body: "{line1}\n{line2}\n"}
}

// TestFakeRunnerRecordsItsArgv: the `record` step writes the runner's whole argv, one element
// per line, which is how a fixture proves WHICH command the batch ran and with what arguments
// -- a `--runner`'s five, or the self's `native` verb (issue #636).
func TestFakeRunnerRecordsItsArgv(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	runner := runnerDoing(t, dir, "recorder", runnerStep{Op: "record", Path: out})
	card := filepath.Join(dir, "card.md")
	root := filepath.Join(dir, "root")
	cmd := exec.Command(runner, "card-f", "1", "m", card, root)
	if got, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the recording runner: %v\n%s", err, got)
	}
	want := strings.Join([]string{runner, "card-f", "1", "m", card, root}, "\n") + "\n"
	if got := string(readTestFile(t, out)); got != want {
		t.Fatalf("the recorded argv is %q, want %q", got, want)
	}
}
