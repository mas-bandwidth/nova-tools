package main

import (
	"strings"
	"testing"
)

// TestHelpVerbExitsZeroWithUsage verifies Property P6/P8:
// `nova-fuse help <verb>` prints that verb's usage and synopsis at exit 0.
func TestHelpVerbExitsZeroWithUsage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args     []string
		wantVerb string
		wantText string
	}{
		{
			args:     []string{"help", "init"},
			wantVerb: "init",
			wantText: "create an empty box where none is",
		},
		{
			args:     []string{"help", "status"},
			wantVerb: "status",
			wantText: "what is blown, and since when",
		},
		{
			args:     []string{"help", "check"},
			wantVerb: "check",
			wantText: "may I read? -- the gate",
		},
		{
			args:     []string{"help", "lockdown"},
			wantVerb: "lockdown",
			wantText: "blow the one hard fuse",
		},
		{
			args:     []string{"help", "quarantine"},
			wantVerb: "quarantine",
			wantText: "stop reading one surface",
		},
		{
			args:     []string{"help", "lift"},
			wantVerb: "lift",
			wantText: "rescind a fuse",
		},
		{
			args:     []string{"help", "lift", "quarantine"},
			wantVerb: "lift quarantine",
			wantText: "rescind your own quarantine",
		},
		{
			args:     []string{"help", "lift", "lockdown"},
			wantVerb: "lift lockdown",
			wantText: "REFUSED by design",
		},
		{
			args:     []string{"help", "path"},
			wantVerb: "path",
			wantText: "echo the box path",
		},
		{
			args:     []string{"help", "version"},
			wantVerb: "version",
			wantText: "print this build identity",
		},
		{
			args:     []string{"help", "help"},
			wantVerb: "help",
			wantText: "print usage and help",
		},
	}

	for _, tc := range cases {
		tc := tc
		name := strings.Join(tc.args, "_")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := capture(t, tc.args, nowish())
			if code != 0 {
				t.Fatalf("%v exited %d, want 0; stderr: %q", tc.args, code, stderr)
			}
			if stderr != "" {
				t.Errorf("%v wrote to stderr: %q; help is not a refusal", tc.args, stderr)
			}
			if !strings.Contains(stdout, "usage:\n  nova-fuse "+tc.wantVerb) {
				t.Errorf("%v stdout lacks usage synopsis for %q:\n%s", tc.args, tc.wantVerb, stdout)
			}
			if !strings.Contains(stdout, tc.wantText) {
				t.Errorf("%v stdout lacks %q:\n%s", tc.args, tc.wantText, stdout)
			}
		})
	}
}

// TestVerbDashHRefusesAtExitTwo verifies Property P6/P8:
// `<verb> -h` must continue to refuse at exit 2 (fail closed by design),
// because exit 0 is this tool's CLEAR and an untrusted surface named -h
// cannot be allowed to reach exit 0.
func TestVerbDashHRefusesAtExitTwo(t *testing.T) {
	t.Parallel()

	verbs := []string{
		"init",
		"status",
		"check",
		"lockdown",
		"quarantine",
		"lift",
		"path",
		"version",
	}

	for _, verb := range verbs {
		for _, flag := range []string{"-h", "--help"} {
			verb, flag := verb, flag
			t.Run(verb+"_"+flag, func(t *testing.T) {
				t.Parallel()
				code, stdout, stderr := capture(t, []string{verb, flag}, nowish())
				if code != 2 {
					t.Fatalf("%s %s exited %d, want 2 (fail closed by design); stdout=%q stderr=%q",
						verb, flag, code, stdout, stderr)
				}
				if stdout != "" {
					t.Errorf("%s %s wrote to stdout: %q; refusals go to stderr", verb, flag, stdout)
				}
				if stderr == "" {
					t.Errorf("%s %s wrote nothing to stderr on refusal", verb, flag)
				}
			})
		}
	}
}

// TestHelpUnknownVerbRefuses verifies that asking for help on an unknown verb refuses at exit 2.
func TestHelpUnknownVerbRefuses(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := capture(t, []string{"help", "nonexistent-subcommand"}, nowish())
	if code != 2 {
		t.Fatalf("help unknown exited %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("help unknown wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "unknown verb \"nonexistent-subcommand\"") {
		t.Errorf("stderr does not name unknown verb: %q", stderr)
	}
	if !strings.HasSuffix(strings.TrimSpace(stderr), "; run: nova-fuse help") {
		t.Errorf("stderr does not end in door: %q", stderr)
	}
}

// TestHelpUnexpectedArgumentsRefuse verifies that passing extra arguments to help <verb> refuses at exit 2.
func TestHelpUnexpectedArgumentsRefuse(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := capture(t, []string{"help", "check", "extra"}, nowish())
	if code != 2 {
		t.Fatalf("help check extra exited %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("help check extra wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "unexpected argument \"extra\"") {
		t.Errorf("stderr does not name unexpected argument: %q", stderr)
	}
}
