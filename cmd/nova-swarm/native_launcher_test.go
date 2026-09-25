package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// TestNativeLaunchGoesThroughTheOneLauncher is the call site #2646 left open (stella's
// hold): a native launch builds its harness argv with swarm.LaunchArgvFor, the providers
// table's one launcher, and the argv the child is handed is exactly the one it returned.
// A provider the table names launches with its own row; one it does not launches with the
// table's declared default row, never a literal argv in the caller. RED WITHOUT THE WIRING:
// native built `run --model <m> --title <l> -- <card>` inline and never asked the table.
func TestNativeLaunchGoesThroughTheOneLauncher(t *testing.T) {
	bin := nativeHarness(t)
	for _, tc := range []struct{ model, row string }{
		{"fake/fake-model", swarm.DefaultLaunchRow},
		{"opencode/deepseek-v4-flash", "opencode"},
	} {
		t.Run(tc.row, func(t *testing.T) {
			root, slot := aSlot(t)
			type call struct {
				provider string
				req      swarm.LaunchRequest
				argv     []string
			}
			var calls []call
			orig := launchArgvFor
			t.Cleanup(func() { launchArgvFor = orig })
			launchArgvFor = func(provider, goos string, req swarm.LaunchRequest) ([]string, error) {
				argv, err := orig(provider, goos, req)
				calls = append(calls, call{provider, req, argv})
				return argv, err
			}

			card := "a card\n"
			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: bin, model: tc.model, label: "launcher-lbl",
				card: []byte(card), slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
			}, &errOut)
			if code != 0 {
				t.Fatalf("the run exits 0, got %d:\n%s", code, errOut.String())
			}
			if len(calls) != 1 {
				t.Fatalf("the launch did not go through swarm.LaunchArgvFor: %d calls, want 1", len(calls))
			}
			c := calls[0]
			if c.provider != tc.row {
				t.Errorf("the launch asked the table for row %q, want %q", c.provider, tc.row)
			}
			if c.req.Harness != bin || c.req.Model != tc.model || c.req.Title != "launcher-lbl" || c.req.Prompt != card {
				t.Errorf("the launch request is %+v, want harness %s, model %s, title launcher-lbl and the card as prompt", c.req, bin, tc.model)
			}
			raw, err := os.ReadFile(filepath.Join(slot, "native-argv.log"))
			if err != nil {
				t.Fatalf("the run recorded no native-argv.log: %v", err)
			}
			want := "argv: " + oneline.Escape(strings.Join(c.argv, " "))
			if !strings.Contains(string(raw), want+"\n") {
				t.Errorf("the child was not handed the one launcher's argv; want line %q in:\n%s", want, raw)
			}
		})
	}
}

// TestNativeLaunchCarriesTheResultFormat is nova-tools#3651: a typed card's
// launch prompt is the card text followed by the RESULT-FORMAT paragraph, so the
// worker is told the grammar card end validates. RED WITHOUT swarm.CardPrompt:
// the prompt was the card text alone.
func TestNativeLaunchCarriesTheResultFormat(t *testing.T) {
	card := "RESULT: c1 sha=0123456789ab nova-tools fix: a card\nKIND: fix\n"
	argv, err := nativeLaunchArgv("/bin/true", nativeRunConfig{model: "fake/fake-model", label: "fmt-lbl", card: []byte(card)}, "fake")
	if err != nil {
		t.Fatal(err)
	}
	prompt := argv[len(argv)-1]
	if !strings.HasPrefix(prompt, card) || !strings.Contains(prompt, "RESULT-FORMAT") || !strings.Contains(prompt, "## Left owed") {
		t.Fatalf("the launch prompt does not carry the RESULT-FORMAT paragraph after the card:\n%s", prompt)
	}
}
