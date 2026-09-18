package dogfood

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fakeHelp = `nova-fake: a thin client (see docs/SPEC-FAKE.md)

usage:
  nova-fake session start --session <path> --as <name>
  nova-fake ask           delivers ONE unit to the FRIEND who owns it
  nova-fake asks          the open asks, oldest first
  nova-fake help

example:
  nova-check links --dir ./self
`

// binName is a built binary's file name on this platform: `nova-fake` on Unix,
// `nova-fake.exe` on Windows. Windows has no executable mode bit -- every file
// in a directory reads back as 0666 and the EXTENSION is what makes one
// runnable -- so a fixture that spells the name one way and chmods it is a
// fixture that exists on one platform only.
func binName(tool string) string {
	if runtime.GOOS == "windows" {
		return tool + ".exe"
	}
	return tool
}

func toolsDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, binName(name)), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A file that is not a tool, and one that is not executable: neither is run.
	// "Not executable" is the platform's own answer -- no mode bit on Unix, no
	// runnable extension on Windows -- and `nova-notexec` is written without
	// either, so the same fixture means the same thing on both.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("not a tool\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nova-notexec"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestVerbsFromToolsAsksEachBinaryForItsOwnVerbs(t *testing.T) {
	dir := toolsDir(t, "nova-fake")
	var asked []string
	run := func(ctx context.Context, bin string) (string, error) {
		asked = append(asked, filepath.Base(bin))
		return fakeHelp, nil
	}
	verbs, failures, err := VerbsFromTools(context.Background(), dir, run, nil)
	if err != nil {
		t.Fatalf("VerbsFromTools: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("failures %+v", failures)
	}
	if strings.Join(asked, ",") != binName("nova-fake") {
		t.Fatalf("ran %v; only executable nova-* files are run", asked)
	}
	var got []string
	for _, v := range verbs {
		got = append(got, v.Key())
	}
	want := "nova-fake session start|nova-fake ask|nova-fake asks|nova-fake help"
	if strings.Join(got, "|") != want {
		t.Fatalf("verbs %q, want %q", strings.Join(got, "|"), want)
	}
}

// A binary speaks for itself: the example line in its own help naming another
// tool is not a verb of that tool's, and counting it would let one worked
// example invent verbs across the whole family.
func TestVerbsFromToolsIgnoresAnotherToolsLineInAHelpBlock(t *testing.T) {
	dir := toolsDir(t, "nova-fake")
	run := func(ctx context.Context, bin string) (string, error) { return fakeHelp, nil }
	verbs, _, err := VerbsFromTools(context.Background(), dir, run, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range verbs {
		if v.Tool != "nova-fake" {
			t.Fatalf("nova-fake's help declared %q for another tool", v.Key())
		}
	}
}

func TestVerbsFromToolsNamesABinaryThatCannotAnswer(t *testing.T) {
	dir := toolsDir(t, "nova-fake", "nova-broken")
	run := func(ctx context.Context, bin string) (string, error) {
		if strings.Contains(filepath.Base(bin), "nova-broken") {
			return "", os.ErrPermission
		}
		return fakeHelp, nil
	}
	verbs, failures, err := VerbsFromTools(context.Background(), dir, run, nil)
	if err != nil {
		t.Fatalf("one unreadable binary failed the whole run: %v", err)
	}
	if len(failures) != 1 || !strings.Contains(failures[0].Subject, "nova-broken") {
		t.Fatalf("failures %+v, want the one binary named", failures)
	}
	if len(verbs) == 0 {
		t.Fatal("a half-built directory cost every tool in it, not just the broken one")
	}
}

func TestVerbsFromToolsRefusesWithNoDirectoryAndStopsOnADeadline(t *testing.T) {
	if _, _, err := VerbsFromTools(context.Background(), "  ", nil, nil); err == nil {
		t.Fatal("an empty --tools was accepted; every path comes from a flag")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := toolsDir(t, "nova-fake")
	run := func(ctx context.Context, bin string) (string, error) { return fakeHelp, nil }
	if _, _, err := VerbsFromTools(ctx, dir, run, nil); err == nil {
		t.Fatal("a cancelled read ran on")
	}
}

func TestMergeVerbsLetsTheBinariesWinAndTheReferenceFillIn(t *testing.T) {
	fromTools := verbs("nova-work ask", "nova-work asks")
	fromCLI := verbs("nova-work session start", "nova-check links")
	got := MergeVerbs(fromTools, fromCLI)
	var keys []string
	for _, v := range got {
		keys = append(keys, v.Key())
	}
	want := "nova-work ask|nova-work asks|nova-check links"
	if strings.Join(keys, "|") != want {
		t.Fatalf("merged %q, want %q: a tool that answered for itself is complete", strings.Join(keys, "|"), want)
	}
}

// One end-to-end read against a real executable written here, so the way this
// invokes a binary is the way a binary is actually invoked. Local only.
func TestVerbsFromToolsAgainstARealExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a shell script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\n" + fakeHelp + "EOF\n"
	if err := os.WriteFile(filepath.Join(dir, binName("nova-fake")), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	verbs, failures, err := VerbsFromTools(ctx, dir, nil, nil)
	if err != nil {
		t.Fatalf("VerbsFromTools: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("failures %+v", failures)
	}
	if len(verbs) != 4 {
		t.Fatalf("read %d verbs out of a real binary's help, want 4: %+v", len(verbs), verbs)
	}
}

// The Windows PR legs read a directory of real binaries as EMPTY. A built
// binary there is `nova-check.exe`, and it has no executable mode bit at all --
// every file in a Windows directory reads back as 0666 -- so a discovery that
// wants the bare spelling and a 0o111 bit finds nothing on the one platform
// nobody develops on. Both spellings name the same tool, everywhere.
func TestToolNameAcceptsBothSpellingsOfABuiltBinary(t *testing.T) {
	for name, want := range map[string]string{
		"nova-check":     "nova-check",
		"nova-check.exe": "nova-check",
		"nova-self-talk": "nova-self-talk",
		"README.md":      "",
		"nova-":          "",
		"check":          "",
		"nova-check.sh":  "",
		"NOVA-CHECK.EXE": "",
	} {
		got, ok := toolName(name)
		if want == "" {
			if ok {
				t.Errorf("%q was read as the tool %q; it is not one", name, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("%q = %q (%v), want %q", name, got, ok, want)
		}
	}
}

// "Executable" is the platform's own answer, and both answers are checked here
// rather than on whichever bench happens to run the suite.
func TestRunnableIsThePlatformsOwnAnswer(t *testing.T) {
	for name, tc := range map[string]struct {
		goos string
		file string
		mode os.FileMode
		want bool
	}{
		"unix, mode bit set":              {goos: "linux", file: "nova-check", mode: 0o755, want: true},
		"unix, no mode bit":               {goos: "linux", file: "nova-check", mode: 0o644},
		"unix, a directory":               {goos: "linux", file: "nova-check", mode: os.ModeDir | 0o755},
		"windows, the extension is it":    {goos: "windows", file: "nova-check.exe", mode: 0o666, want: true},
		"windows, no extension":           {goos: "windows", file: "nova-check", mode: 0o666},
		"windows, no mode bit needed":     {goos: "windows", file: "nova-check.exe", mode: 0o444, want: true},
		"windows, mode bits mean nothing": {goos: "windows", file: "nova-notexec", mode: 0o755},
	} {
		if got := runnableOn(tc.goos, tc.file, tc.mode); got != tc.want {
			t.Errorf("%s: runnable = %v, want %v", name, got, tc.want)
		}
	}
}

// And the discovery itself reads a Windows-shaped directory: the `.exe` files
// are the tools, and the verbs come back under the tool's own name.
func TestVerbsFromToolsReadsAWindowsShapedDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nova-fake.exe"), []byte("binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(ctx context.Context, bin string) (string, error) { return fakeHelp, nil }
	verbs, failures, err := VerbsFromTools(context.Background(), dir, run, nil)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		// Off Windows a `.exe` with no mode bit is not runnable, and the
		// discovery says so rather than pretending: the name is accepted, the
		// permission is not.
		if len(verbs) != 0 || len(failures) != 0 {
			t.Fatalf("a non-executable file was run: verbs=%d failures=%d", len(verbs), len(failures))
		}
		return
	}
	if len(failures) != 0 {
		t.Fatalf("failures %+v", failures)
	}
	if len(verbs) == 0 {
		t.Fatal("a directory of .exe binaries read as empty; that is the Windows leg's own red")
	}
	for _, v := range verbs {
		if v.Tool != "nova-fake" {
			t.Errorf("the verb is under %q; nova-fake.exe speaks for nova-fake", v.Tool)
		}
	}
}
