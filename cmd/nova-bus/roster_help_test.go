package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// A cold reader gets from nothing to a first send with the binary alone: the banner and
// `send -h` state the roster file, what a lane is, and one example roster, and the example
// the banner prints is a roster the tool loads.
func TestBannerStatesTheRosterAndLanes(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{
		"help":    invoke(t, "", "help").stdout,
		"send -h": invoke(t, "", "send", "-h").stdout,
	} {
		for _, want := range []string{"participants.json", `"lane":"from-ada"`, "git_name", "git_email", "no lane"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not say %q", name, want)
			}
		}
	}
	help := invoke(t, "", "help").stdout
	for _, want := range []string{"ROSTER AND LANES", "a lane", "git init -b main bus", "nova-bus send --bus . --file ../d.md --as Ada --remote origin --branch main --no-push"} {
		if !strings.Contains(help, want) {
			t.Errorf("the banner's roster paragraph does not say %q", want)
		}
	}
}

// Every roster the banner prints is a roster the tool loads, with Ada able to send.
func TestTheRostersTheBannerPrintsLoad(t *testing.T) {
	t.Parallel()
	lines := strings.Split(invoke(t, "", "help").stdout, "\n")
	var rosters []string
	for i := 0; i < len(lines); i++ {
		text := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(text, `{"participants":[`) {
			continue
		}
		// a roster is its first line through the line that closes the object
		cur := text
		for !strings.HasSuffix(cur, "}") || strings.Count(cur, "{") != strings.Count(cur, "}") {
			i++
			if i >= len(lines) {
				t.Fatalf("a roster in the banner never closes: %q", cur)
			}
			cur += strings.TrimSpace(lines[i])
		}
		rosters = append(rosters, cur)
	}
	if len(rosters) < 2 {
		t.Fatalf("the banner prints %d rosters, want its synopsis and its paragraph", len(rosters))
	}
	for _, roster := range rosters {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, bus.ConfigName), []byte(roster), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := bus.LoadConfig(dir)
		if err != nil {
			t.Errorf("the banner's roster does not load: %v\n%s", err, roster)
			continue
		}
		ada, ok := cfg.Lookup("Ada")
		if !ok || ada.Lane != "from-ada" {
			t.Errorf("the banner's roster gives Ada no lane: %+v", ada)
		}
	}
}

// A first run with nothing given names every problem it has: the missing flags and the
// receipt word count, whose refusal names the file and the variable that supply it.
func TestInboxAndWaitNameEveryMissingThingInOneRun(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		verb string
		want []string
	}{
		{"inbox", []string{"--as is required", "--bus is required", "--receipt-max-words must be given"}},
		{"wait", []string{"--as is required", "--branch is required", "--bus is required", "--remote is required", "--receipt-max-words must be given"}},
	} {
		r := invoke(t, "", c.verb).mustCode(t, 2)
		for _, w := range c.want {
			if !strings.Contains(r.stderr, w) {
				t.Errorf("%s with nothing: stderr has no %q:\n%s", c.verb, w, r.stderr)
			}
		}
		if !strings.Contains(r.stderr, "<bus>/.nova-bus/defaults") || !strings.Contains(r.stderr, "NOVA_BUS_RECEIPT_MAX_WORDS") {
			t.Errorf("%s: the refusal does not name the file and the variable that supply the count:\n%s", c.verb, r.stderr)
		}
	}
	// the flag given: the count is no longer a problem, the rest still are
	r := invoke(t, "", "inbox", "--receipt-max-words", "40").mustCode(t, 2)
	if strings.Contains(r.stderr, "receipt-max-words") || !strings.Contains(r.stderr, "--as is required") {
		t.Errorf("inbox with the count given: %s", r.stderr)
	}
}

// The banner says where the receipt word count comes from, and what it names is real: the
// file line is read from <bus>/.nova-bus/defaults, so a bus that carries it needs no flag.
func TestReceiptWordCountSourcesAreTheOnesTheBannerNames(t *testing.T) {
	t.Parallel()
	help := invoke(t, "", "help").stdout
	if strings.Contains(help, "no default receipt word count") {
		t.Error("the banner says there is no default receipt word count while two sources supply one")
	}
	for _, want := range []string{"receipt-max-words=<n>", "<bus>/.nova-bus/defaults", "NOVA_BUS_RECEIPT_MAX_WORDS"} {
		if !strings.Contains(help, want) {
			t.Errorf("the banner does not name %q", want)
		}
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".nova-bus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".nova-bus", "defaults"), []byte("# defaults\nreceipt-max-words=25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := invoke(t, "", "inbox", "--bus", dir, "--as", "Ada")
	if strings.Contains(r.stderr, "receipt-max-words must be given") {
		t.Errorf("a bus whose defaults file carries the count is still asked for the flag:\n%s", r.stderr)
	}
}
