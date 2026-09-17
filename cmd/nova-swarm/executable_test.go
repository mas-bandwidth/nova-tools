package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// THE EXECUTE QUESTION IS ASKED OF THE PLATFORM, NOT OF THE UNIX BIT (windows leg, 2026-09-15).
// `native` refused every harness on windows-latest -- "the harness binary C:\...\fake-harness.exe
// is not executable" on every card -- because the check read `Mode().Perm()&0o111`, and NTFS
// has no execute bit: os.Stat reports 0666 there for every readable file, so the condition was
// true of a real .exe. The helper keeps the unix rule and asks PATHEXT on windows.
//
// This test runs on every platform and asserts the answer the platform itself gives, so the
// two bodies are held to one contract: a thing the loader will run is admitted, a thing it
// will not run is refused, and a directory or a missing path is never executable.
func TestIsExecutableAsksThePlatformsOwnRule(t *testing.T) {
	dir := t.TempDir()

	// (1) A FILE THE PLATFORM WILL RUN. On unix that is mode 0755; on windows it is the
	// extension, so the same bytes are written under a name the loader recognises.
	runnable := filepath.Join(dir, "runnable")
	if runtime.GOOS == "windows" {
		runnable += ".exe"
	}
	if err := os.WriteFile(runnable, []byte("binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isExecutable(runnable) {
		t.Fatalf("a file this platform runs is executable: %s was refused", runnable)
	}

	// (2) A FILE THE PLATFORM WILL NOT RUN. On unix, a regular file with no execute bit.
	// On windows the bit does not exist, so the refusable file is one whose extension is
	// not in PATHEXT -- and it is written 0755 on purpose, to prove the windows body does
	// not fall back to reading a bit that is always the same there.
	notRunnable := filepath.Join(dir, "not-runnable.txt")
	mode := os.FileMode(0o644)
	if runtime.GOOS == "windows" {
		mode = 0o755
	}
	if err := os.WriteFile(notRunnable, []byte("data\n"), mode); err != nil {
		t.Fatal(err)
	}
	if isExecutable(notRunnable) {
		t.Fatalf("a file this platform will not run is not executable: %s was admitted", notRunnable)
	}

	// (3) A DIRECTORY IS NEVER EXECUTABLE, whatever its mode -- 0755 on every platform.
	if isExecutable(dir) {
		t.Fatalf("a directory is never executable: %s was admitted", dir)
	}

	// (4) A PATH THAT IS NOT THERE IS NOT EXECUTABLE. `native` names that case "missing"
	// before it asks this question; the helper must not claim otherwise if anyone else does.
	gone := filepath.Join(dir, "gone")
	if runtime.GOOS == "windows" {
		gone += ".exe"
	}
	if isExecutable(gone) {
		t.Fatalf("a path with no file behind it is not executable: %s was admitted", gone)
	}
}

// THE WINDOWS RULE, HELD TO ITS CONTRACT ON EVERY PLATFORM. executableByExtension is written
// where darwin and linux compile it precisely because the bug it replaces could not be seen
// from here: a check only windows runs is a check nobody reads. The cases are the ones the
// loader itself answers -- the extension decides, case does not matter, a missing PATHEXT
// falls back to the shipped list, and the absence of an extension is a refusal.
func TestExecutableByExtensionIsTheWindowsRule(t *testing.T) {
	const pathext = `.COM;.EXE;.BAT;.CMD;.VBS;.JS`
	for _, tc := range []struct {
		path    string
		pathext string
		want    bool
	}{
		// The fault: a real .exe was refused because it carried no unix execute bit.
		{`C:\tmp\fake-harness.exe`, pathext, true},
		{`C:\tmp\FAKE-HARNESS.EXE`, pathext, true},
		{`C:\tmp\runner.bat`, pathext, true},
		{`C:\tmp\runner.cmd`, pathext, true},
		{`C:\tmp\legacy.com`, pathext, true},
		// Not a program the loader will start.
		{`C:\tmp\notes.txt`, pathext, false},
		{`C:\tmp\runner.sh`, pathext, false},
		{`C:\tmp\harness`, pathext, false},
		// A machine that sets no PATHEXT gets the shipped list, which holds .exe and not .sh.
		{`C:\tmp\fake-harness.exe`, "", true},
		{`C:\tmp\runner.sh`, "", false},
		{`C:\tmp\fake-harness.exe`, "   ", true},
		// The loader tolerates a PATHEXT spelled without dots, and a lower-cased one.
		{`C:\tmp\fake-harness.exe`, "COM;EXE;BAT", true},
		{`C:\tmp\fake-harness.exe`, ".com;.exe", true},
		// A PATHEXT that does not name the suffix refuses it, however common the suffix is.
		{`C:\tmp\fake-harness.exe`, ".BAT;.CMD", false},
	} {
		if got := executableByExtension(tc.path, tc.pathext); got != tc.want {
			t.Fatalf("executableByExtension(%q, PATHEXT=%q) = %v, want %v", tc.path, tc.pathext, got, tc.want)
		}
	}
}

// The unix body is the one darwin and linux can exercise directly: the three execute bits,
// each on its own, admit; none of them refuses. It is skipped on windows, where the bits are
// not the rule and os.Chmod cannot set them.
func TestIsExecutableUnixBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows carries no execute bit: os.Stat reports 0666 for every readable file, so there is no bit to set or clear (see executable.go)")
	}
	dir := t.TempDir()
	for _, tc := range []struct {
		mode os.FileMode
		want bool
	}{
		{0o644, false},
		{0o600, false},
		{0o000, false},
		{0o755, true},
		{0o700, true},
		{0o100, true}, // owner execute alone
		{0o010, true}, // group execute alone
		{0o001, true}, // other execute alone
	} {
		path := filepath.Join(dir, "bin")
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, tc.mode); err != nil {
			t.Fatal(err)
		}
		if got := isExecutable(path); got != tc.want {
			t.Fatalf("mode %04o: isExecutable = %v, want %v", tc.mode, got, tc.want)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}
