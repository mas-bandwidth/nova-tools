package sandbox

import (
	"strings"
	"testing"
	"time"
)

// The path logic of the two platforms this build does not run on is tested HERE, with the
// platform injected, because a Mac cannot run the windows body and a test that only ever
// walks a unix path calls a windows bug green. Both tests below are the two windows CI
// failures of run 34663812025, reproduced on darwin.

// winDir is filepath.Dir's answer on windows: the separator is a backslash, a path may
// carry a volume name, and the VOLUME ROOT IS ITS OWN PARENT — which is the whole of the
// second bug. It is the model this package's loop has to terminate against.
func winDir(p string) string {
	vol := ""
	if len(p) >= 2 && p[1] == ':' {
		vol, p = p[:2], p[2:]
	}
	p = strings.ReplaceAll(p, "/", `\`)
	i := strings.LastIndex(p, `\`)
	switch {
	case i < 0:
		return vol + "."
	case i == 0:
		return vol + `\`
	default:
		return vol + p[:i]
	}
}

func TestWinDirModelsTheVolumeRoot(t *testing.T) {
	for path, want := range map[string]string{
		`C:\Users\runneradmin\AppData`: `C:\Users\runneradmin`,
		`C:\Users`:                     `C:\`,
		`C:\`:                          `C:\`, // its own parent: there is nothing above it
		`\a`:                           `\`,
		`\`:                            `\`,
	} {
		if got := winDir(path); got != want {
			t.Fatalf("winDir(%q) = %q, want %q", path, got, want)
		}
	}
}

// internal/sandbox timed out after 600s on windows with the goroutine dump ending in the
// call to Ancestors: the walk up the tree stopped at the literal "/" and a windows path
// never reaches one, so it spun on the volume root forever. A test must never wait without
// a deadline, and this one has five seconds.
func TestAncestorsTerminatesOnAWindowsPath(t *testing.T) {
	done := make(chan []string, 1)
	go func() { done <- ancestors(winDir, `C:\Users\runneradmin\AppData\Local\Temp\job\w`) }()
	select {
	case got := <-done:
		want := []string{
			`C:\Users`,
			`C:\Users\runneradmin`,
			`C:\Users\runneradmin\AppData`,
			`C:\Users\runneradmin\AppData\Local`,
			`C:\Users\runneradmin\AppData\Local\Temp`,
			`C:\Users\runneradmin\AppData\Local\Temp\job`,
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("ancestors = %v, want %v", got, want)
		}
		for _, d := range got {
			if winDir(d) == d {
				t.Fatalf("the volume root %q is in the ancestor list; the root is granted above, not as an ancestor", d)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ancestors did not return in 5s on a windows path: the walk up the tree has no stop above the volume root")
	}
}

// TestRefusalsThroughTheArgv/home_outside and TestUnbuiltPlatformsRefuse failed on windows
// with SANDBOX REFUSED reason=bad_write naming the test's own temp directory: the SBPL
// metacharacter set holds a backslash, and every absolute windows path is backslashes. The
// refusal is about the DARWIN profile's ancestor literals, which put a path into the policy
// text; windows has no such text, and `C:\Program Files (x86)` is an ordinary directory
// there. A control character is refused on every platform.
func TestPathMetacharactersAreRefusedPerPlatform(t *testing.T) {
	for _, tc := range []struct {
		goos, path string
		refused    bool
	}{
		{"darwin", "/Users/me/pool/jobs/j1", false},
		{"darwin", "/Users/me/a (paren)", true},
		{"darwin", `/Users/me/a\b`, true},
		{"darwin", "/Users/me/a\x01b", true},
		{"windows", `C:\Users\runneradmin\AppData\Local\Temp\TestX001\w`, false},
		{"windows", `C:\Program Files (x86)\Go\bin`, false},
		{"windows", "C:\\Users\\me\x01b", true},
		{"linux", "/home/me/jobs/j1", false},
		{"linux", "/home/me/a (paren)", false},
	} {
		got := badPathTextFor(tc.goos, tc.path)
		if refused := got != ""; refused != tc.refused {
			t.Errorf("badPathTextFor(%q, %q) = %q; refused=%v, want refused=%v", tc.goos, tc.path, got, refused, tc.refused)
		}
	}
}
