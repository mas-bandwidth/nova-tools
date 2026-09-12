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

// absFixture builds a fixture path that is absolute ON EVERY PLATFORM. A path typed
// `/w/home-1` is absolute on darwin and linux and is NOT on windows -- `filepath.IsAbs` is
// false without a volume -- so `absPath`, which is the seam obeying rule 5 ("paths are
// resolved, absolute and existing"), correctly turned the fixture into `D:\w\home-1` and
// the test compared it against what it had typed (windows CI of #88 at d8a5824). The seam
// was right and the FIXTURE was wrong: the promise is that every path the seam hands the
// wall is absolute, and a test of that promise must start from a path that is absolute
// where it runs. The volume is the one the machine's own temp directory is on; nothing is
// created, so no directory of that volume is touched.
func absFixture(parts ...string) string {
	root := filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	return filepath.Join(append([]string{root}, parts...)...)
}

// DEMANDED (SPEC-SANDBOX.md, the dispatcher caller). The write set is the job directory
// FIRST and the per-job data home; the read set is the worker home and the toolchain roots
// the description named; the cwd is the job directory; and --net-deny is nowhere, because
// the provider's API is the work.
func TestTheWrapArgvIsTheTwoListsAndNothingElse(t *testing.T) {
	slotDir := absFixture("w", "home-1")
	jobDir := filepath.Join(slotDir, "jobs", "j1")
	dataHome := filepath.Join(jobDir, "data")
	tools, gotools := absFixture("Users", "x", "toolchains"), absFixture("Users", "x", "go")
	// The harness's own argv is typed POSIX-style ON EVERY PLATFORM on purpose: the seam
	// passes everything after `--` verbatim (rule 12), so a path shape this tool would
	// never build is the sharpest proof that it rewrote nothing.
	job := SandboxJob{
		Sandbox: "/usr/local/bin/nova-sandbox", PoolName: "pool-7",
		SlotDir: slotDir, JobDir: jobDir, DataHome: dataHome,
		ReadRoots: []string{tools, gotools},
		Command:   "/opt/homebrew/bin/opencode", Args: []string{"run", "--model", "m", "--", "/w/home-1/jobs/j1/PROMPT.md"},
	}
	argv := strings.Join(job.SandboxArgv(), " ")
	for _, want := range []string{
		"--read " + slotDir,
		"--read " + tools,
		"--read " + gotools,
		"--write " + jobDir + " --write " + dataHome,
		"--cwd " + jobDir,
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

// DEMANDED (SPEC-SANDBOX.md rule 5, "paths are resolved, absolute and existing"). Every path
// this seam hands the wall is ABSOLUTE at the point the argv is built, whatever the caller
// typed. `--pool ./pool` is the README's own line and `Pool.Dir` keeps it as typed, so the
// probe was handed `--write pool/sandbox-probe`, the wall refused it as relative, and `run`
// refused the pass and started no worker (DeepSeek's read of #88 at d0c1841, HIGH 1). This
// says NO on the tool before the fix: the probe directory is relative, and so is the argv.
func TestEveryPathTheSeamHandsTheWallIsAbsolute(t *testing.T) {
	if got := SandboxProbeDir("pool"); !filepath.IsAbs(got) {
		t.Errorf("a relative --pool makes a relative probe directory %q, and rule 5 refuses a relative path", got)
	}
	pool := absFixture("w", "pool")
	if got, want := SandboxProbeDir(pool), filepath.Join(pool, "sandbox-probe"); got != want {
		t.Errorf("an absolute pool is left alone: got %q, want %q", got, want)
	}
	job := SandboxJob{
		Sandbox: "nova-sandbox",
		SlotDir: "home-1", JobDir: "home-1/jobs/j1", DataHome: "home-1/jobs/j1/data",
		ReadRoots: []string{"toolchains"},
		Command:   "/opt/homebrew/bin/opencode", Args: []string{"run", "--", "home-1/jobs/j1/PROMPT.md"},
	}
	argv := job.SandboxArgv()
	for i, a := range argv {
		if i == 0 || argv[i-1] != "--read" && argv[i-1] != "--write" && argv[i-1] != "--cwd" {
			continue
		}
		if !filepath.IsAbs(a) {
			t.Errorf("%s %s is relative, and the wall resolves every path before it grants anything:\n%s",
				argv[i-1], a, strings.Join(argv, " "))
		}
	}
	// The harness's own argv after -- is the harness's, verbatim: this seam does not
	// rewrite a path a harness was given (rule 12).
	if got := argv[len(argv)-1]; got != "home-1/jobs/j1/PROMPT.md" {
		t.Errorf("the harness's own argument was rewritten: %q", got)
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
	// The key file lives OUTSIDE the worker directory, because a key inside it is copied
	// into every slot and is refused at load in its own right (the test below).
	home := filepath.Join(dir, "worker")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "key")
	if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	desc := map[string]any{
		"name": "w", "provider": "p", "model": "m", "env_var": "K", "key_file": key,
		"usage": "none", "harness": "h", "worker_dir": home, "deadline": "1m",
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

// DEMANDED (SPEC-SANDBOX.md rule 6 and the dispatcher caller: "the key FILE is in neither
// list, so the job cannot read it even if it is told to"). The tool must not put it in one.
// `RefreshSlot` copies every regular file of `worker_dir` into the slot directory, and the
// slot directory is the job's `--read`, so a `key_file` under `worker_dir` is a copy of the
// key INSIDE the wall, under a green `SANDBOX OK` and a probe that passed -- the probe's own
// `secret_inside_allow` is checked against the probe's lists, never against a job's (Rowan's
// Fable read of #88 at d0c1841, M1). A key under a `read_roots` entry is the same hole
// without the copy. Both are refused at LOAD, where a person can still move the file.
func TestAKeyFileInsideTheReadSetIsRefusedAtLoad(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "worker")
	tools := filepath.Join(dir, "toolchains")
	for _, d := range []string{home, tools} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	descFor := func(workerDir, key string) string {
		raw, _ := json.MarshalIndent(map[string]any{
			"name": "w", "provider": "p", "model": "m", "env_var": "K", "key_file": key,
			"usage": "none", "harness": "h", "worker_dir": workerDir, "deadline": "1m",
			"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
			"read_roots":   []string{tools},
		}, "", "  ")
		path := filepath.Join(dir, "worker.json")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	desc := func(key string) string { return descFor(home, key) }
	// The key file a description may NOT name: one inside the worker directory, which is
	// copied into the slot before every job.
	inside := filepath.Join(home, ".key")
	if err := os.WriteFile(inside, []byte("sk-not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, problems := LoadWorker(desc(inside))
	if len(problems) != 1 {
		t.Fatalf("a key file inside worker_dir reported %d problems, want 1: %v", len(problems), problems)
	}
	said := problems[0].Error()
	for _, want := range []string{inside, home, "copied into the slot", "--read", "Keep the key file outside worker_dir"} {
		if !strings.Contains(said, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, said)
		}
	}
	// And one inside a read root, which every job of this worker may read directly.
	inRoot := filepath.Join(tools, "key")
	if err := os.WriteFile(inRoot, []byte("sk-not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, problems = LoadWorker(desc(inRoot))
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), "read_roots[0]") {
		t.Fatalf("a key file inside a read root is not refused: %v", problems)
	}
	// And one inside a SLOT directory, which is a sibling of worker_dir and not under it:
	// the slot IS the job's `--read`, so the key is read inside the wall under a probe that
	// passed, and neither check above sees it (Rowan's Fable read 2 of #88 at fc400ce, L1).
	// The refusal is at LOAD, so `nova-swarm run` returns before Run prints any RUN line --
	// the task stays pending and no worker starts.
	slotJobs := filepath.Join(dir, "worker-1", "jobs", "t1")
	if err := os.MkdirAll(slotJobs, 0o755); err != nil {
		t.Fatal(err)
	}
	inSlot := filepath.Join(dir, "worker-1", ".key")
	if err := os.WriteFile(inSlot, []byte("sk-not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, problems = LoadWorker(desc(inSlot))
	if len(problems) != 1 {
		t.Fatalf("a key file inside a slot directory reported %d problems, want 1: %v", len(problems), problems)
	}
	said = problems[0].Error()
	for _, want := range []string{inSlot, filepath.Join(dir, "worker-1"), "--read", "~/.keys/<provider>"} {
		if !strings.Contains(said, want) {
			t.Errorf("the slot refusal does not say %q:\n%s", want, said)
		}
	}
	// The same key one level deeper, under the slot's own jobs/, is the same hole.
	underJobs := filepath.Join(slotJobs, ".key")
	if err := os.WriteFile(underJobs, []byte("sk-not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, problems := LoadWorker(desc(underJobs)); len(problems) != 1 {
		t.Fatalf("a key file under a slot's jobs/ is not refused: %v", problems)
	}
	// A sibling that merely starts the same way is NOT a slot and is sound: the tool
	// refuses the placements it creates, not every neighbour of worker_dir.
	neighbour := filepath.Join(dir, "worker-keys")
	if err := os.MkdirAll(neighbour, 0o700); err != nil {
		t.Fatal(err)
	}
	sound := filepath.Join(neighbour, "provider")
	if err := os.WriteFile(sound, []byte("sk-not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, problems := LoadWorker(desc(sound)); len(problems) != 0 {
		t.Errorf("a key file in a sibling that is not a slot is refused: %v", problems)
	}
	// The key file the README teaches -- outside both lists -- is sound, and a symlink
	// into the worker directory does not walk around the check (rule 5 resolves paths).
	outside := filepath.Join(dir, "keys", "provider")
	if err := os.MkdirAll(filepath.Dir(outside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("sk-not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, problems := LoadWorker(desc(outside)); len(problems) != 0 {
		t.Errorf("a key file outside both lists is refused: %v", problems)
	}
	link := filepath.Join(dir, "linked-key")
	if err := os.Symlink(inside, link); err != nil {
		t.Fatal(err)
	}
	if _, problems := LoadWorker(desc(link)); len(problems) != 1 {
		t.Errorf("a symlink walks around the check: %v", problems)
	}
	// AND A SYMLINKED worker_dir DOES NOT WALK AROUND THE SLOT CHECK. `SlotDir` builds the
	// slot from the TYPED spelling, so with `worker_dir` a link the slot the tool creates and
	// hands to `--read` is a sibling of the LINK, not of its target: asking only the resolved
	// spelling left a key inside the wall under a green line (Rowan's Fable read 3 of #88 at
	// 56c7dcc, L1). Exactly one refusal, naming the key and the slot the tool would build.
	realDir := filepath.Join(dir, "real", "worker")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	typedDir := filepath.Join(dir, "typed-worker")
	if err := os.Symlink(realDir, typedDir); err != nil {
		t.Fatal(err)
	}
	linkedSlot := Worker{WorkerDir: typedDir}.SlotDir(1)
	if err := os.MkdirAll(linkedSlot, 0o755); err != nil {
		t.Fatal(err)
	}
	inLinkedSlot := filepath.Join(linkedSlot, ".key")
	if err := os.WriteFile(inLinkedSlot, []byte("sk-not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, problems = LoadWorker(descFor(typedDir, inLinkedSlot))
	if len(problems) != 1 {
		t.Fatalf("a key file inside the slot of a SYMLINKED worker_dir reported %d problems, want 1: %v", len(problems), problems)
	}
	said = problems[0].Error()
	for _, want := range []string{inLinkedSlot, linkedSlot, "--read", "~/.keys/<provider>"} {
		if !strings.Contains(said, want) {
			t.Errorf("the symlinked-slot refusal does not say %q:\n%s", want, said)
		}
	}
}

// DEMANDED (SPEC-SANDBOX.md test 10, "all five checks run even when the first fails"). The
// line RUN REFUSED quotes is the line that said NO. The probe keeps going after a failed
// check, so the refusal is not last, and the tool used to quote a PASSING step as the reason
// no worker started (DeepSeek's read of #88 at d0c1841, MEDIUM 2).
func TestTheRefusalQuotesTheLineThatSaidNo(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"the refusal is followed by checks that passed",
			"PROBE STEP name=write_outside expect=deny got=allow path=-\n" +
				"PROBE REFUSED reason=check: write_outside expected deny and got allow\n" +
				"PROBE STEP name=read_root expect=allow got=allow path=-\n",
			"PROBE REFUSED reason=check: write_outside expected deny and got allow"},
		{"no refusal line, so the first step whose got is not its expect",
			"PROBE STEP name=read_root expect=allow got=allow path=-\n" +
				"PROBE STEP name=write_outside expect=deny got=allow path=-\n" +
				"PROBE STEP name=secret_outside expect=deny got=deny path=-\n",
			"PROBE STEP name=write_outside expect=deny got=allow path=-"},
		{"neither, so the last line is all there is",
			"the wall said something this tool does not parse\nand then a last word\n",
			"and then a last word"},
		{"nothing at all", "", "the probe said nothing"},
	} {
		if got := refusalLine(c.body); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
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
