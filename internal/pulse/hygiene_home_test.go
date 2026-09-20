package pulse

import (
	"bytes"
	"strings"
	"testing"
)

// #1282. Every path the hygiene verb may remove is built from --home: the two
// roots, the action log and the build cache. The coordinator's hostile-input
// run measured 36 of 40 cases passing and exactly the four HOME cases failing,
// because a --home that is empty, is not absolute, or has fewer than two
// components resolved to nonsense roots the verb then worked under.
//
// TestHygieneRefusesAHomeWithoutTwoComponents feeds the four values #1282 names
// -- "", "/", "relative/home" and "/onlyone" -- to the log verb, which reads
// and deletes nothing, and requires the same refusal the coordinator's copy
// prints: exit 2 with a line naming the home.
//
// The control is not optional. A guard that refused every home would pass the
// hostile half and break the verb, so a real t.TempDir() home (never this
// machine's) must NOT print the refusal.
func TestHygieneRefusesAHomeWithoutTwoComponents(t *testing.T) {
	hostile := []struct {
		name string
		home string
		why  string
	}{
		{"empty", "", "empty"},
		{"whole-disk", "/", "the whole disk"},
		{"relative", "relative/home", "a relative path"},
		{"one-component", "/onlyone", "only one component"},
	}
	for _, tc := range hostile {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := Hygiene(HygieneInput{Verb: "log", Home: tc.home, Stdout: &out, Stderr: &errb})
			if code != 2 {
				t.Errorf("--home %q (%s) exited %d; the verb must refuse with exit 2 before it builds a path", tc.home, tc.why, code)
			}
			if !strings.Contains(errb.String(), "REFUSED") {
				t.Errorf("--home %q (%s) exited %d with stderr %q; the refusal must say what --home wants", tc.home, tc.why, code, errb.String())
			}
		})
	}

	// THE CONTROL: a good home is not refused. The log verb finds no log and
	// exits 0; what matters is that the refusal line is absent.
	home := t.TempDir()
	var out, errb bytes.Buffer
	Hygiene(HygieneInput{Verb: "log", Home: home, Stdout: &out, Stderr: &errb})
	if strings.Contains(errb.String(), "REFUSED") {
		t.Errorf("a good home %q was refused: %s", home, errb.String())
	}
}
