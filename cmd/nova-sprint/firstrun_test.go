package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The first sitting is the three store-free help commands in docs/CLI.md.
// Their whole output is stable: no clock, store, credential or path belongs to
// this run, so CompareTranscript receives no volatile-field exclusions.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	const transcriptTool = "nova-sprint"
	if prog != transcriptTool {
		t.Fatalf("program name %q does not match transcript section %q", prog, transcriptTool)
	}

	examplesWant := []string{
		"nova-sprint help",
		"nova-sprint help add",
		"nova-sprint help inbox",
	}
	a := newApp(func(string) string { return "" })
	var banner, bannerErr bytes.Buffer
	if code := a.run([]string{"help"}, &banner, &bannerErr); code != 0 || bannerErr.Len() != 0 {
		t.Fatalf("help: exit %d, stderr %q", code, bannerErr.String())
	}
	examples, err := onboarding.ExampleLines(banner.String(), transcriptTool)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(examples, "\n") != strings.Join(examplesWant, "\n") {
		t.Fatalf("banner examples = %q, want %q", examples, examplesWant)
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), transcriptTool)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps(transcriptTool, lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != len(examplesWant) {
		t.Fatalf("transcript has %d commands, want %d: %q", len(steps), len(examplesWant), examplesWant)
	}
	got := make([]onboarding.Result, 0, len(steps))
	for i, s := range steps {
		if line := strings.TrimPrefix(s.Line, "$ "); line != examplesWant[i] || s.Stdin != "" {
			t.Fatalf("transcript command %d = %q with stdin %q, want %q", i+1, s.Line, s.Stdin, examplesWant[i])
		}
		var out, errb bytes.Buffer
		code := a.run(s.Args, &out, &errb)
		if code != 0 {
			t.Fatalf("%s: exit %d, stderr %q", s.Line, code, errb.String())
		}
		got = append(got, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()})
	}
	if len(a.conns) != 0 || len(a.cached) != 0 {
		t.Fatalf("offline first run opened a store: connections=%d backends=%d", len(a.conns), len(a.cached))
	}
	for _, p := range onboarding.CompareTranscript(steps, got, nil) {
		t.Error(p)
	}
}

func TestFirstRunRefusalNamesTheHelpDoor(t *testing.T) {
	t.Parallel()

	a := newApp(func(string) string { return "" })
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "run: nova-sprint help"},
		{[]string{"add"}, "run: nova-sprint add -h"},
	} {
		var out, errb bytes.Buffer
		code := a.run(tc.args, &out, &errb)
		if code != 2 || out.Len() != 0 || strings.Count(errb.String(), "\n") != 1 ||
			!strings.Contains(errb.String(), tc.want) {
			t.Errorf("%q: exit %d, stdout %q, stderr %q; want %q", tc.args, code, out.String(), errb.String(), tc.want)
		}
	}
}
