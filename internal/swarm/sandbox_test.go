package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The LAUNCH SEAM's own unit: the argv the dispatcher builds for a worker, and the one
// field the worker description gained. The wall itself is asked about the operating system
// in cmd/nova-swarm's tests, on the platform whose body is built; what is here is the shape
// of the two lists, which is the same shape on every platform.

// DEMANDED (SPEC-SANDBOX.md, the dispatcher caller). The write set is the job directory
// FIRST and the per-job data home; the read set is the worker home and the toolchain roots
// the description named; the cwd is the job directory; and --net-deny is nowhere, because
// the provider's API is the work.
func TestTheWrapArgvIsTheTwoListsAndNothingElse(t *testing.T) {
	job := SandboxJob{
		Sandbox: "/usr/local/bin/nova-sandbox", PoolName: "pool-7",
		SlotDir: "/w/home-1", JobDir: "/w/home-1/jobs/j1", DataHome: "/w/home-1/jobs/j1/data",
		ReadRoots: []string{"/Users/x/toolchains", "/Users/x/go"},
		Command:   "/opt/homebrew/bin/opencode", Args: []string{"run", "--model", "m", "--", "/w/home-1/jobs/j1/PROMPT.md"},
	}
	argv := strings.Join(job.SandboxArgv(), " ")
	for _, want := range []string{
		"--read /w/home-1",
		"--read /Users/x/toolchains",
		"--read /Users/x/go",
		"--write /w/home-1/jobs/j1 --write /w/home-1/jobs/j1/data",
		"--cwd /w/home-1/jobs/j1",
		"--name pool-7",
		"-- /opt/homebrew/bin/opencode run --model m -- /w/home-1/jobs/j1/PROMPT.md",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("the wrap argv does not carry %q:\n%s", want, argv)
		}
	}
	if strings.Contains(argv, "--net-deny") {
		t.Errorf("--net-deny is in the argv and the provider's API is the work:\n%s", argv)
	}
	if strings.Contains(argv, "--no-sandbox") {
		t.Errorf("the one loud workaround is in a built argv:\n%s", argv)
	}
	// The whole command starts with the binary, and everything after -- is the harness's
	// own argv, verbatim and in order (rule 12).
	whole := job.SandboxCommand()
	if whole[0] != job.Sandbox {
		t.Errorf("the command is %q, want the wall's binary", whole[0])
	}
	if got := whole[len(whole)-1]; got != "/w/home-1/jobs/j1/PROMPT.md" {
		t.Errorf("the prompt file is not last in the child's argv, got %q", got)
	}
}

// DEMANDED (SPEC-SANDBOX.md rule 5, "there is no --root flag"). read_roots is the one field
// the wall added to the worker description, and a root that is relative, absent or a file
// is refused at LOAD -- once, where a person can fix it -- rather than by the wall at every
// launch. Every independent problem is reported in one run.
func TestReadRootsAreRefusedBeforeTheyReachTheWall(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "toolchains")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "key")
	if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	desc := map[string]any{
		"name": "w", "provider": "p", "model": "m", "env_var": "K", "key_file": key,
		"usage": "none", "harness": "h", "worker_dir": dir, "deadline": "1m",
		"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
		"read_roots":   []string{good, "relative/toolchain", filepath.Join(dir, "absent"), file},
	}
	raw, _ := json.MarshalIndent(desc, "", "  ")
	path := filepath.Join(dir, "worker.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	w, problems := LoadWorker(path)
	if len(problems) != 3 {
		t.Fatalf("three read roots are wrong and the load reported %d problems: %v", len(problems), problems)
	}
	all := ""
	for _, p := range problems {
		all += p.Error() + "\n"
	}
	for _, want := range []string{"is relative", "does not exist", "is not a directory"} {
		if !strings.Contains(all, want) {
			t.Errorf("no problem says %q:\n%s", want, all)
		}
	}
	if len(w.ReadRoots) != 4 || w.ReadRoots[0] != good {
		t.Errorf("the roots are read as written, in order: %v", w.ReadRoots)
	}
	// A description that names none is sound: the system roots are the floor.
	delete(desc, "read_roots")
	raw, _ = json.MarshalIndent(desc, "", "  ")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, problems := LoadWorker(path); len(problems) != 0 {
		t.Errorf("a description naming no read root is refused: %v", problems)
	}
}

// DEMANDED (SPEC-SANDBOX.md rule 11). The one loud line names the job and says what is
// missing, in the words the rule gives it.
func TestTheLoudLineSaysWhatIsMissing(t *testing.T) {
	line := UnsandboxedLine("20260912T0000Z-task-1", 3)
	for _, want := range []string{"RUN UNSANDBOXED ", "id=20260912T0000Z-task-1", "slot=3", "no OS containment"} {
		if !strings.Contains(line, want) {
			t.Errorf("the loud line does not carry %q: %s", want, line)
		}
	}
}
