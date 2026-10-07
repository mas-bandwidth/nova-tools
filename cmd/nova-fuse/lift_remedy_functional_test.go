//go:build functional && !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/stretchr/testify/require"
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
			require.NoError(t, fuse.WriteBox(box, fuse.Box{Quarantine: map[string]fuse.Fuse{tc.surface: {At: "t", Reason: "r"}}}))
			code, _, refusal := capture(t, []string{"check", "--box", box, "--", fuse.Surface(tc.surface)}, nowish())
			require.Equal(t, 1, code, "check exit=%d stderr=%q", code, refusal)
			require.Equal(t, 1, strings.Count(refusal, "\n"), "check exit=%d stderr=%q", code, refusal)
			_, remedy, ok := strings.Cut(refusal, "when the surface is safe again: ")
			require.True(t, ok, "no remedy: %q", refusal)
			require.True(t, strings.HasSuffix(remedy, ")\n"), "no remedy: %q", refusal)
			remedy = strings.TrimSuffix(remedy, ")\n")
			stub := filepath.Join(dir, "nova-fuse")
			require.NoError(t, os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\\000' \"$@\"\n"), 0700))
			cmd := exec.Command("/bin/sh", "-c", remedy)
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + dir}
			raw, err := cmd.CombinedOutput()
			require.NoError(t, err, "remedy %q: %v: %s", remedy, err, raw)
			args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
			want := []string{"lift", "quarantine", "--box", box, "--", fuse.Surface(tc.surface)}
			require.Equal(t, want, args, "argv=%q want=%q", args, want)
			code, out, errOut := capture(t, args, nowish())
			require.Equal(t, 0, code, "lift exit=%d out=%q err=%q", code, out, errOut)
			code, _, errOut = capture(t, []string{"check", "--box", box, "--", fuse.Surface(tc.surface)}, nowish())
			require.Equal(t, 0, code, "still quarantined: exit=%d stderr=%q", code, errOut)
		})
	}
}

// The missing-box init and existing-box status remedies must preserve the
// caller's exact path through a real shell, just like the lift remedy above.
func TestInitAndStatusRemediesRoundTripThroughShell(t *testing.T) {
	t.Parallel()
	for _, spelling := range []struct{ name, path string }{
		{"plain", "box.json"},
		{"quotes", "a box's $HOME `printf expanded`.json"},
		{"operators", "box;$(printf expanded).json"},
		{"controls", "box\t\n\u2028.json\n"},
	} {
		for _, verb := range []string{"check", "status", "quarantine", "lift", "init"} {
			t.Run(spelling.name+"/"+verb, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				box := filepath.Join(dir, spelling.path)
				args := []string{verb, "--box", box}
				prefix, suffix := "make it: ", "; run: nova-fuse help\n"
				wantVerb := "init"
				wantCode := 2
				if verb == "quarantine" {
					args = append(args, "surface", "reason")
					prefix, suffix = "make the box first (", "), or blow lockdown; run: nova-fuse help\n"
				}
				if verb == "lift" {
					args = []string{"lift", "quarantine", "--box", box, "surface"}
				}
				var before []byte
				if verb == "init" {
					require.NoError(t, fuse.WriteBox(box, fuse.Box{Lockdown: &fuse.Fuse{At: "t", Reason: "r"}}))
					var err error
					before, err = os.ReadFile(box)
					require.NoError(t, err)
					prefix, suffix = "read it with ", "\n"
					wantVerb, wantCode = "status", 1
				}
				code, out, refusal := capture(t, args, nowish())
				require.Equal(t, wantCode, code, "refusal exit=%d out=%q err=%q", code, out, refusal)
				require.Empty(t, out, "refusal exit=%d out=%q err=%q", code, out, refusal)
				require.Equal(t, 1, strings.Count(refusal, "\n"), "refusal exit=%d out=%q err=%q", code, out, refusal)
				_, remedy, ok := strings.Cut(refusal, prefix)
				require.True(t, ok, "no remedy: %q", refusal)
				require.True(t, strings.HasSuffix(remedy, suffix), "no remedy: %q", refusal)
				remedy = strings.TrimSuffix(remedy, suffix)
				stub := filepath.Join(dir, "nova-fuse")
				require.NoError(t, os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\\000' \"$@\"\n"), 0700))
				cmd := exec.Command("/bin/sh", "-c", remedy)
				cmd.Dir = dir
				cmd.Env = []string{"PATH=" + dir}
				raw, err := cmd.CombinedOutput()
				require.NoError(t, err, "remedy %q: %v: %s", remedy, err, raw)
				actual := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
				want := []string{wantVerb, "--box", box}
				require.Equal(t, want, actual, "argv=%q want=%q", actual, want)
				code, out, errOut := capture(t, actual, nowish())
				require.Equal(t, 0, code, "remedy exit=%d out=%q err=%q", code, out, errOut)
				if verb == "init" {
					after, err := os.ReadFile(box)
					require.NoError(t, err)
					require.Equal(t, string(before), string(after), "status remedy changed blown box")
					code, _, _ = capture(t, []string{"check", "--box", box}, nowish())
					require.Equal(t, 1, code, "status remedy cleared lockdown, check exit=%d", code)
				} else {
					code, _, errOut := capture(t, []string{"check", "--box", box}, nowish())
					require.Equal(t, 0, code, "init remedy did not initialize exact path: exit=%d err=%q", code, errOut)
				}
			})
		}
	}
}
