//go:build functional && !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
)

// Execute the actual printed remedy through a POSIX shell, capturing argv with
// an owned stand-in, then give that argv to the real CLI and verify the state.
func TestCheckLiftRemedyRoundTripsThroughShell(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, box, surface string }{
		{"plain", "box.json", "a-forum"},
		{"quotes", "a box's $HOME `literal`.json", "it's a $(printf substituted) forum"},
		{"flag", "box.json", "--box=other.json"},
		{"separator", "box.json", "--"},
		{"stored-spelling", "box.json", "Stored\x00NAME\n"},
		{"controls", "box\t\n.json\n", "stored\nname\u2028end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			box := filepath.Join(dir, tc.box)
			if err := fuse.WriteBox(box, fuse.Box{Quarantine: map[string]fuse.Fuse{tc.surface: {At: "t", Reason: "r"}}}); err != nil {
				t.Fatal(err)
			}
			code, _, refusal := capture(t, []string{"check", "--box", box, "--", fuse.Surface(tc.surface)}, nowish())
			if code != 1 || strings.Count(refusal, "\n") != 1 {
				t.Fatalf("check exit=%d stderr=%q", code, refusal)
			}
			_, remedy, ok := strings.Cut(refusal, "when the surface is safe again: ")
			if !ok || !strings.HasSuffix(remedy, ")\n") {
				t.Fatalf("no remedy: %q", refusal)
			}
			remedy = strings.TrimSuffix(remedy, ")\n")
			stub := filepath.Join(dir, "nova-fuse")
			if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\\000' \"$@\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("/bin/sh", "-c", remedy)
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + dir}
			raw, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("remedy %q: %v: %s", remedy, err, raw)
			}
			args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
			want := []string{"lift", "quarantine", "--box", box, "--", fuse.Surface(tc.surface)}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("argv=%q want=%q", args, want)
			}
			if code, out, errOut := capture(t, args, nowish()); code != 0 {
				t.Fatalf("lift exit=%d out=%q err=%q", code, out, errOut)
			}
			if code, _, errOut := capture(t, []string{"check", "--box", box, "--", fuse.Surface(tc.surface)}, nowish()); code != 0 {
				t.Fatalf("still quarantined: exit=%d stderr=%q", code, errOut)
			}
		})
	}
}
