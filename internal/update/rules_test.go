package update

// The rules SPEC-UPDATE demands evidence for that the rest of this package's
// tests carried only in passing: 9 (check writes nothing), 16 (bounded output at
// the largest plausible state), 18 (every refusal names a remedy), 26 (no clock,
// no install, no send nobody asked for), and rule 25's injected clock and a
// reporter really killed while it writes its snapshot.
//
// Each test below asserts the property, not the name. The kill helpers are the
// join's own (setGroup, killGroup, buildTreeBinaries); nothing here is a second
// harness.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// treeOf records every file under a directory the way a person checking "did
// anything change" would: name, size and modification time.
func treeOf(t *testing.T, root string) []string {
	t.Helper()
	var seen []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		seen = append(seen, fmt.Sprintf("%s %d %s", path, info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano)))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return seen
}

// Rule 9: a verdict is not an action. check reads, prints and exits; it writes no
// file, and the apply command of a STALE entry is never the thing it runs.
func TestRule9CheckWritesNothingAndNeverRunsAnApply(t *testing.T) {
	dir := t.TempDir()
	installed := printer(t, "1.0.0")
	latest := printer(t, "2.0.0")
	witness := filepath.Join(dir, "the-apply-ran")
	calls := filepath.Join(dir, "calls")
	t.Setenv("NOVA_UPDATE_CALLS", calls)
	// The apply column is a command that would leave a file behind if it ran.
	p := manifest(t, row("x", "tool", installed, "local:"+latest, command(t, "write", witness, "installed")))
	before := treeOf(t, filepath.Dir(p))
	c, out, errs := run(t, Environment{}, "check", "--file", p)
	if c != 1 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	need(t, out, "UPDATE STALE name=x")
	if _, err := os.Stat(witness); err == nil {
		t.Fatal("check ran the apply command")
	}
	if after := treeOf(t, filepath.Dir(p)); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("check wrote to the manifest's directory:\nbefore %v\nafter  %v", before, after)
	}
	log, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		if line != "" && !strings.HasPrefix(line, "print ") {
			t.Fatalf("check ran something that is not a version read: %q", line)
		}
	}
	if n := strings.Count(string(log), "\n"); n != 2 {
		t.Fatalf("want exactly the installed and latest reads, got %d calls", n)
	}
}

// Rule 16: the output is bounded at the largest state a person will plausibly
// have, --max 0 is the explicit way to ask for all of it, and a negative cap is a
// refusal rather than a silent interpretation.
func TestRule16OutputIsBoundedAtTheLargestPlausibleState(t *testing.T) {
	const many = 500
	rows := make([]string, 0, many)
	for i := 0; i < many; i++ {
		// Neither side resolves, so this reads 500 entries with no process and
		// no network: the size of the state, not the cost of it, is the point.
		rows = append(rows, row(fmt.Sprint(i), "tool", "nova-no-such-tool-exists", "local:nova-no-such-tool-exists", "none"))
	}
	p := manifest(t, rows...)
	c, out, errs := run(t, Environment{}, "check", "--file", p, "--max", "3")
	if c != 1 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	lines := strings.Count(strings.TrimSpace(out), "\n") + 1
	if lines > 12 {
		t.Fatalf("a 500-entry run printed %d lines to stdout", lines)
	}
	need(t, out, "entries=500", "UPDATE MORE kind=unknown shown=3 total=500")
	c, all, errs := run(t, Environment{}, "check", "--file", p, "--max", "0")
	if c != 1 {
		t.Fatalf("%d %s %s", c, all, errs)
	}
	if shown := strings.Count(all, "UPDATE UNKNOWN "); shown != many {
		t.Fatalf("--max 0 showed %d of %d", shown, many)
	}
	if strings.Contains(all, "UPDATE MORE") {
		t.Fatal("--max 0 still elided something")
	}
	if c, _, errs = run(t, Environment{}, "check", "--file", p, "--max", "-1"); c != 2 {
		t.Fatalf("--max -1 was not refused: %d %s", c, errs)
	}
}

// Rule 18: a refusal a person cannot act on is not a refusal. Every one of them
// names a remedy, and the shape is the grammar's: REFUSED: <reason> (<remedy>).
func TestRule18EveryRefusalNamesARemedy(t *testing.T) {
	good := row("x", "tool", printer(t, "1.0.0"), "npm:unused", "none")
	p := manifest(t, good)
	bad := manifest(t, strings.Replace(good, "tool", "weights", 1))
	for name, args := range map[string][]string{
		"no verb":                {},
		"no file":                {"check"},
		"a kind nobody has":      {"check", "--file", bad},
		"apply with no name":     {"apply", "--file", p},
		"apply two names":        {"apply", "--file", p, "x", "y"},
		"apply an unknown":       {"apply", "--file", p, "nobody"},
		"a negative cap":         {"check", "--file", p, "--max", "-1"},
		"send with no flags":     {"report", "--file", p, "--send"},
		"help with an argument":  {"help", "please"},
		"an unknown kind filter": {"check", "--file", p, "--kind", "weights"},
	} {
		var out, errs bytes.Buffer
		if c := Run("nova-update", args, "", &out, &errs, Environment{}); c != 2 {
			t.Errorf("%s: exit %d, not a refusal: %s%s", name, c, out.String(), errs.String())
			continue
		}
		said := strings.TrimSpace(errs.String())
		if said == "" {
			t.Errorf("%s: refused in silence", name)
			continue
		}
		first := firstLine(said)
		if !strings.Contains(first, "REFUSED") {
			t.Errorf("%s: not a REFUSED line: %q", name, first)
		}
		open := strings.Index(first, "(")
		if open < 0 || !strings.HasSuffix(first, ")") || open+2 >= len(first) {
			t.Errorf("%s: no remedy in parentheses: %q", name, first)
		}
	}
}

// Rule 26: the tool has no clock and no appetite. Every stamp it prints comes
// from the clock it was handed, a report with recipients named but no --send
// starts no bus, and a run whose children hang still ends inside its budget.
func TestRule26NoClockOfItsOwnNoInstallNoSendNobodyAsked(t *testing.T) {
	fixed := time.Date(2026, 9, 9, 12, 34, 56, 0, time.UTC)
	env := Environment{Now: func() time.Time { return fixed }}
	snapshot := filepath.Join(t.TempDir(), "s.json")
	log := fakeBusPath(t)
	p := manifest(t, row("x", "tool", printer(t, "v1.2.3"), "npm:unused", "none"))
	c, out, errs := run(t, env, "report", "--file", p, "--as", "fixture", "--to", "integrator", "--snapshot", snapshot)
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	if !strings.Contains(out, "REPORT at="+fixed.Format(time.RFC3339)) {
		t.Fatalf("the printed stamp is not the injected clock's: %s", firstLine(out))
	}
	if strings.Contains(out, "REPORT SENT") {
		t.Fatal("a report with recipients sent without --send")
	}
	// A missing call log is the strongest form of the answer: the fake bus never
	// ran at all, so it never even opened the file it logs to.
	if _, err := os.Stat(log); err == nil {
		if nprep, nsend := calls(t, log); nprep != 0 || nsend != 0 {
			t.Fatalf("a plain report invoked the bus: %d prepares, %d sends", nprep, nsend)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	// Rule 25's injected clock: the snapshot's own stamps are that clock too, so
	// two runs of one fixture are byte-identical and a diff means a change.
	state, err := readSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for name, o := range state.Observed {
		if o.At != fixed.Format(time.RFC3339) {
			t.Fatalf("%s was stamped %q, not by the injected clock", name, o.At)
		}
	}
	// Ends inside its budget: the children hang, and the run still returns. The
	// bound is the budget plus one killGrace, which is the time a killed child's
	// pipe may stay open, and it is why that grace is two seconds and not more.
	hanging := manifest(t, row("h", "tool", command(t, "hang"), "npm:unused", "none"))
	started := time.Now()
	if c, _, _ = run(t, env, "check", "--file", hanging, "--budget", "300ms", "--timeout", "200ms"); c != 1 {
		t.Fatalf("a hanging read was not a finding: %d", c)
	}
	if took := time.Since(started); took > 300*time.Millisecond+killGrace+2*time.Second {
		t.Fatalf("a 300ms budget took %s", took)
	}
}

// Rule 25, killed mid-write: the snapshot is the thing that carries an identity
// across processes, so a reporter killed while it writes one must leave either
// the old snapshot or the new one and never half of either. This kills a real
// nova-update process, repeatedly, at the moment it is writing.
func TestRule25SnapshotSurvivesAReporterKilledWhileWriting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this kills a process group with SIGKILL; the owed Windows termination validation is named in the pull request")
	}
	bin := filepath.Join(buildTreeBinaries(t), exeName("nova-update"))
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "s.json")
	t.Setenv("NOVA_UPDATE_HELPER", "1")
	first := manifest(t, row("x", "tool", printer(t, "v1.0.0"), "npm:unused", "none"))
	// One good run, so there is a previous snapshot a later kill could corrupt.
	good := exec.Command(bin, "report", "--file", first, "--snapshot", snapshot)
	if out, err := good.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	settled, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	killed := 0
	for attempt := 0; attempt < 12; attempt++ {
		p := manifest(t, row("x", "tool", printer(t, fmt.Sprintf("v2.%d.0", attempt)), "npm:unused", "none"))
		c := exec.Command(bin, "report", "--file", p, "--snapshot", snapshot)
		setGroup(c)
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		// Kill as soon as a replacement is in flight: writeSnapshot creates its
		// temporary beside the snapshot and renames it, so the temporary's
		// existence is the write happening.
		done := make(chan struct{})
		go func() { _ = c.Wait(); close(done) }()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			temps, _ := filepath.Glob(filepath.Join(dir, ".nova-version-snapshot-*"))
			if len(temps) > 0 {
				if killGroup(c) == nil {
					killed++
				}
				break
			}
			select {
			case <-done:
				deadline = time.Now()
			default:
			}
			time.Sleep(time.Millisecond)
		}
		<-done
		// Whatever the kill interrupted, what is on disk must still read.
		state, err := readSnapshot(snapshot)
		if err != nil {
			raw, _ := os.ReadFile(snapshot)
			t.Fatalf("attempt %d left an unreadable snapshot: %v\n%q", attempt, err, clip(string(raw), 400))
		}
		if len(state.Observed) != 1 {
			t.Fatalf("attempt %d left %d observations", attempt, len(state.Observed))
		}
		var check map[string]json.RawMessage
		raw, err := os.ReadFile(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if json.Unmarshal(raw, &check) != nil {
			t.Fatalf("attempt %d left JSON that does not parse", attempt)
		}
	}
	if killed == 0 {
		t.Skipf("no reporter was caught mid-write in 12 attempts; the snapshot stayed readable in all of them (settled bytes %d)", len(settled))
	}
	t.Logf("killed %d of 12 reporters while they wrote the snapshot; every snapshot read afterwards", killed)
	if _, err := readSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	// And a fresh reporter still works against whatever survived.
	final := exec.Command(bin, "report", "--file", first, "--snapshot", snapshot)
	if out, err := final.CombinedOutput(); err != nil {
		t.Fatalf("a later reporter could not use the surviving snapshot: %v\n%s", err, out)
	}
	// A SIGKILL runs no deferred cleanup, so every killed writer left its
	// half-written temporary beside the snapshot. The next writer sweeps them.
	if temps, _ := filepath.Glob(filepath.Join(dir, snapshotTempPrefix+"*")); len(temps) != 0 {
		t.Fatalf("%d killed writers left %d temporaries behind: %v", killed, len(temps), temps)
	}
}

// SPEC-UPDATE: "Those three usage lines are the string `nova-update help` prints,
// byte for byte: one string in the binary, so the spec and the help cannot drift
// apart." Nothing made that true until this test read the spec and compared.
func TestHelpIsTheSpecsVerbsBlock(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-UPDATE.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The block is the first fenced code block after the "## The verbs" heading.
	_, after, ok := strings.Cut(string(doc), "\n## The verbs\n")
	if !ok {
		t.Fatal("the spec has no verbs section")
	}
	_, after, ok = strings.Cut(after, "```\n")
	if !ok {
		t.Fatal("the verbs section has no block")
	}
	block, _, ok := strings.Cut(after, "```")
	if !ok {
		t.Fatal("the verbs block does not close")
	}
	if want, got := strings.TrimRight(block, "\n"), updateVerbs; want != got {
		t.Fatalf("help has drifted from the spec's verbs block:\nspec:\n%s\nhelp:\n%s", want, got)
	}
	var printed bytes.Buffer
	help("nova-update", &printed)
	if !strings.HasPrefix(printed.String(), updateVerbs+"\n") {
		t.Fatalf("help does not open with the verbs block:\n%s", printed.String())
	}
	printed.Reset()
	help("nova-version", &printed)
	if !strings.HasPrefix(printed.String(), versionVerbs+"\n") {
		t.Fatalf("nova-version's help does not open with its two lines:\n%s", printed.String())
	}
	// The spec says nova-version's lines are the report line's flags under that
	// name, so every flag the report line offers a plain report must appear.
	for _, flag := range []string{"--file <path>", "--host <label>", "--snapshot <path>", "--max <n>", "--timeout <d>", "--budget <d>", "--kind <k>"} {
		if !strings.Contains(versionVerbs, flag) {
			t.Errorf("nova-version's report line does not carry %s", flag)
		}
	}
	for _, flag := range []string{"--bus <path>", "--remote <r>", "--branch <b>", "--as <friend>", "--to <who,who>"} {
		if !strings.Contains(versionVerbs, flag) {
			t.Errorf("nova-version's send line does not carry %s", flag)
		}
	}
}

// SPEC-UPDATE's first-run block names ./cmd/nova-update/testdata/versions.tsv and
// apply.tsv, and its "The versions file" section prints the five entries the
// first of those holds. Both files now exist; this reads them the way the tool
// does, so a fixture the spec points a stranger at cannot rot unnoticed. It
// executes nothing: no version command runs, no installer runs, no GET is made.
func TestTheSpecsNamedFixturesLoad(t *testing.T) {
	for _, dir := range []string{"nova-update", "nova-version"} {
		for _, name := range []string{"versions.tsv", "apply.tsv", "example.tsv", "nova.tsv"} {
			path := filepath.Join("..", "..", "cmd", dir, "testdata", name)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("%s: %v", path, err)
				continue
			}
			entries, err := Load(strings.NewReader(string(b)))
			if err != nil {
				t.Errorf("%s does not load: %v", path, err)
				continue
			}
			if len(entries) == 0 {
				t.Errorf("%s carries no entry", path)
			}
			for _, e := range entries {
				if e.Name == "" || e.Kind == "" || e.Owner == "" || len(e.Installed) == 0 {
					t.Errorf("%s: an entry is missing a field: %+v", path, e)
				}
			}
		}
	}
	// The versions fixture is the spec's own five entries, in its order.
	entries, err := Load(strings.NewReader(readFixture(t, "versions.tsv")))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ name, kind, owner string }{
		{"gh", "tool", "rowan"},
		{"sops", "tool", "rowan"},
		{"opencode", "harness", "freddy"},
		{"qwen3-coder:30b", "model", "stella"},
		{"nova-wake-pin-nova-bus", "pin", "rowan"},
	}
	if len(entries) != len(want) {
		t.Fatalf("the spec prints %d entries, the fixture carries %d", len(want), len(entries))
	}
	for i, w := range want {
		if entries[i].Name != w.name || entries[i].Kind != w.kind || entries[i].Owner != w.owner {
			t.Errorf("entry %d is %s/%s/%s, the spec says %s/%s/%s", i, entries[i].Name, entries[i].Kind, entries[i].Owner, w.name, w.kind, w.owner)
		}
	}
}
func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "cmd", "nova-update", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The tripwires SPEC-UPDATE's rule list ends on, asserted rather than grepped by
// hand: this tool has no cwd, no home directory, no shell, no hostname of its
// own, no hard-coded model port, and it reads exactly one environment variable.
// A tripwire measured by a person is a tripwire that goes quiet the first busy
// week; this one fails a build.
func TestTripwiresStayOutOfShippingCode(t *testing.T) {
	shipping := map[string]string{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		shipping[name] = string(b)
	}
	if len(shipping) < 8 {
		t.Fatalf("only %d shipping files were read; the sweep is not sweeping", len(shipping))
	}
	for _, forbidden := range []string{"os.Getwd", "os.UserHomeDir", "os.Hostname", "11434", `"sh"`, `"bash"`, `"cmd.exe"`, "os/user", "time.Ticker", "time.Tick(", "http.DefaultClient"} {
		for name, body := range shipping {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s carries %s, which rule 26 and the tripwire list keep out of this tool", name, forbidden)
			}
		}
	}
	// The two hosts this tool may name live in one file, so a third one added
	// anywhere else is a diff somebody reads.
	for name, body := range shipping {
		for _, host := range []string{"api.github.com", "ollama.com", "registry.npmjs.org"} {
			if strings.Contains(body, host) && name != "latest.go" {
				t.Errorf("%s names the host %s; the sources belong in latest.go alone", name, host)
			}
		}
	}
	// One environment read, and it is PATH inside a remedy, never a setting.
	reads := 0
	for name, body := range shipping {
		n := strings.Count(body, "os.Getenv")
		reads += n
		if n > 0 && name != "read.go" {
			t.Errorf("%s reads the environment; a path comes from a flag (rule 1)", name)
		}
		if n > 0 && !strings.Contains(body, `os.Getenv("PATH")`) {
			t.Errorf("%s reads an environment variable that is not PATH", name)
		}
	}
	if reads != 1 {
		t.Errorf("shipping code reads the environment %d times, want exactly the one PATH in a remedy", reads)
	}
	if strings.Count(strings.Join(valuesOf(shipping), "\n"), "NOVA_UPDATE_") != 0 {
		t.Error("shipping code reads a NOVA_UPDATE_ variable; the test seams are not settings")
	}
}
func valuesOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
