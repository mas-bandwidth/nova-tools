package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fleet-verb-dispatches-and-refuses: the sub-verbs a person types, the positional argument
// each one takes, and a refusal for everything else. Nothing here reaches a machine: the
// only paths exercised are the ones that refuse before the shell is used, and the one
// --dry-run that is defined to touch nothing.
func TestFleetVerbDispatchesAndRefuses(t *testing.T) {
	queue := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
		exit int
		want string
	}{
		{"no sub-verb", []string{"fleet"}, 2, "add, restart, probe, phantoms"},
		{"a sub-verb nobody has", []string{"fleet", "sprinkle"}, 2, "unknown sub-verb"},
		{"add with no name", []string{"fleet", "add", "--host", "studio.local", "--runners", "2", "--queue", queue}, 2, "<name> is required"},
		{"add with no runners", []string{"fleet", "add", "studio", "--host", "studio.local", "--queue", queue}, 2, "--runners is required"},
		{"restart with no runner", []string{"fleet", "restart", "--queue", queue}, 2, "<runner> is required"},
		{"restart of a runner with no row", []string{"fleet", "restart", "space-nova-9", "--queue", queue}, 2, "runner-services.tsv"},
		{"probe of a machine with no row", []string{"fleet", "probe", "ghost", "--queue", queue}, 2, "fleet.tsv"},
		{"phantoms with no repo", []string{"fleet", "phantoms"}, 2, "--repo is required"},
		{"phantoms with a stray argument", []string{"fleet", "phantoms", "mas-bandwidth/nova-tools"}, 2, "takes no positional arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(tc.args, &out, &errb, time.Now().UTC()); code != tc.exit {
				t.Fatalf("exit = %d, want %d; out=%q err=%q", code, tc.exit, out.String(), errb.String())
			}
			if !strings.Contains(errb.String(), tc.want) {
				t.Fatalf("stderr = %q, want it to name %q", errb.String(), tc.want)
			}
		})
	}
}

// fleet-add-dry-run-is-one-line: the plan of a real invocation, read before it is trusted,
// costing one ssh of nothing and one line of output.
func TestFleetAddDryRunPrintsOneLine(t *testing.T) {
	queue := t.TempDir()
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "add", "studio", "--host", "studio.local", "--runners", "8",
		"--labels", "self-hosted,macos", "--queue", queue, "--dry-run"}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%q err=%q", code, out.String(), errb.String())
	}
	line := strings.TrimSpace(out.String())
	if strings.Contains(line, "\n") || errb.Len() != 0 {
		t.Fatalf("fleet add --dry-run printed more than one line: %q %q", out.String(), errb.String())
	}
	for _, want := range []string{"FLEET add", "name=studio", "runners=8", "dry-run=true"} {
		if !strings.Contains(line, want) {
			t.Errorf("FLEET line = %q, want %s", line, want)
		}
	}
	if _, err := os.Stat(filepath.Join(queue, "fleet.tsv")); err == nil {
		t.Errorf("--dry-run wrote fleet.tsv")
	}
}

// fleet-is-in-the-help: a verb the usage does not print is a verb nobody finds.
func TestFleetIsInTheHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	for _, want := range []string{"nova-pulse fleet   add", "fleet   restart", "fleet   probe", "fleet   phantoms"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the usage does not offer %q", want)
		}
	}
}
