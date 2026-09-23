package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The coding lineup's fixtures: one bench's FACT transcript with every precondition
// present, and one launcher that stages and runs a card on the bench it is on.
const lineupWant = "dev.0123456789ab"

var lineupGreenFacts = map[string]string{
	"stage_receipt":      "/home/nova/nova-bench/STAGE-RECEIPT",
	"stage":              "STAGE OK 64/64 elapsed=42s",
	"stage_age_s":        "300",
	"push_credential":    "yes:gh",
	"results_root":       "ok /home/nova/nova-bench/results",
	"finished_jobs":      "0 ",
	"version:nova-swarm": "nova-swarm v0.16.0-" + lineupWant + " linux/amd64",
	"version:nova-pulse": "nova-pulse v0.16.0-" + lineupWant + " linux/amd64",
	"end":                "1",
}

const lineupGreenLauncher = "#!/usr/bin/env bash\n# one card, on this bench; the deal pass holds the one ssh session (#2743)\nset -euo pipefail\nnova-swarm native --card \"$1\"\n"

func writeLineupFacts(t *testing.T, dir, name string, facts map[string]string) string {
	t.Helper()
	var b strings.Builder
	for k, v := range facts {
		b.WriteString("FACT\t" + k + "\t" + v + "\n")
	}
	path := filepath.Join(dir, name+".facts")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runLineup(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"lineup"}, args...), &out, &errb, time.Now().UTC())
	return code, out.String(), errb.String()
}

func redLines(out string) []string {
	var reds []string
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 4 && f[0] == "LINEUP" && f[3] == "RED" {
			reds = append(reds, l)
		}
	}
	return reds
}

// TestLineupCodingRedOnEachMissingPrecondition is #2562's DONE-WHEN: against fixtures,
// each missing precondition prints ONE RED line naming its check and the verb exits 1;
// all present exits 0.
func TestLineupCodingRedOnEachMissingPrecondition(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(f map[string]string)
		launcher string // replaces the green launcher when set
		check    string // the one check that must be RED
	}{
		{name: "stage W/W over 60 s", check: "stage", mutate: func(f map[string]string) { f["stage"] = "STAGE OK 64/64 elapsed=61s" }},
		{name: "stage short of W/W", check: "stage", mutate: func(f map[string]string) { f["stage"] = "STAGE OK 60/64 elapsed=30s" }},
		{name: "stage never run", check: "stage", mutate: func(f map[string]string) { f["stage"] = ""; delete(f, "stage_age_s") }},
		{name: "no push credential", check: "push-credential", mutate: func(f map[string]string) { f["push_credential"] = "no" }},
		{name: "no results root", check: "results-root", mutate: func(f map[string]string) { f["results_root"] = "missing /home/nova/nova-bench/results" }},
		{name: "unwritable results root", check: "results-root", mutate: func(f map[string]string) { f["results_root"] = "unwritable /home/nova/nova-bench/results" }},
		{name: "finished job dirs", check: "finished-jobs", mutate: func(f map[string]string) {
			f["finished_jobs"] = "3 /home/nova/rowan-swarm-root/slot-01/jobs/card-nx-1"
		}},
		{name: "stale tool version", check: "version-nova-swarm", mutate: func(f map[string]string) {
			f["version:nova-swarm"] = "nova-swarm v0.15.9-dev.fedcba987654 linux/amd64"
		}},
		{name: "tool missing", check: "version-nova-pulse", mutate: func(f map[string]string) { delete(f, "version:nova-pulse") }},
		{name: "a per-card ssh launcher", check: "launcher",
			launcher: "#!/usr/bin/env bash\nset -euo pipefail\nbench=$1 card=$2\nssh -o BatchMode=yes \"$bench\" bash -s < \"$card\"\n"},
	}

	// All present: exit 0, no RED.
	dir := t.TempDir()
	launcher := filepath.Join(dir, "green-launcher.sh")
	if err := os.WriteFile(launcher, []byte(lineupGreenLauncher), 0o644); err != nil {
		t.Fatal(err)
	}
	green := writeLineupFacts(t, dir, "hulk", lineupGreenFacts)
	code, out, errs := runLineup(t, "--profile", "coding", "--want", lineupWant, "--facts", "hulk="+green, "--launcher", launcher)
	if code != 0 || len(redLines(out)) != 0 || !strings.Contains(out, "LINEUP coding GREEN") {
		t.Fatalf("all present: exit %d, want 0 and no RED\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			facts := map[string]string{}
			for k, v := range lineupGreenFacts {
				facts[k] = v
			}
			if c.mutate != nil {
				c.mutate(facts)
			}
			body := lineupGreenLauncher
			if c.launcher != "" {
				body = c.launcher
			}
			launcher := filepath.Join(dir, "launcher.sh")
			if err := os.WriteFile(launcher, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			path := writeLineupFacts(t, dir, "hulk", facts)
			code, out, errs := runLineup(t, "--profile", "coding", "--want", lineupWant, "--facts", "hulk="+path, "--launcher", launcher)
			if code != 1 {
				t.Fatalf("exit %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out, errs)
			}
			reds := redLines(out)
			if len(reds) != 1 {
				t.Fatalf("%d RED lines, want exactly 1 (%s)\nstdout:\n%s", len(reds), c.check, out)
			}
			if f := strings.Fields(reds[0]); f[2] != c.check {
				t.Fatalf("the RED line names check %q, want %q: %s", f[2], c.check, reds[0])
			}
			if !strings.Contains(out, "LINEUP coding RED red=1/") {
				t.Fatalf("no closing RED line:\n%s", out)
			}
		})
	}
}

// A bench whose probe was cut short is RED, never GREEN by omission.
func TestLineupCodingProbeCutShortIsRed(t *testing.T) {
	dir := t.TempDir()
	facts := map[string]string{}
	for k, v := range lineupGreenFacts {
		facts[k] = v
	}
	delete(facts, "end")
	path := writeLineupFacts(t, dir, "vision", facts)
	code, out, _ := runLineup(t, "--profile", "coding", "--want", lineupWant, "--facts", "vision="+path)
	if code != 1 || !strings.Contains(out, "LINEUP vision reach RED") {
		t.Fatalf("exit %d, want 1 with a reach RED:\n%s", code, out)
	}
}

func TestLineupRefusesWithoutProfileWantOrBenches(t *testing.T) {
	for _, args := range [][]string{
		{"--want", lineupWant, "--facts", "a=b"},
		{"--profile", "coding", "--facts", "a=b"},
		{"--profile", "coding", "--want", lineupWant},
		{"--profile", "reading", "--want", lineupWant, "--facts", "a=b"},
	} {
		if code, out, errs := runLineup(t, args...); code != 2 {
			t.Fatalf("%v: exit %d, want 2\nstdout:%s\nstderr:%s", args, code, out, errs)
		}
	}
}

// The remote half: the probe script, run through a fake ssh that is `bash -s` on this
// machine, against a fixture home, reads each fact the rules decide on -- one session for
// the whole bench.
func TestLineupProbeScriptReadsAFixtureHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the probe is a bash script run over ssh on a linux or darwin bench")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	mk := func(rel, body string, mode os.FileMode) {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	mk("nova-bench/STAGE-RECEIPT", "STAGE OK 8/8 elapsed=12s\n", 0o644)
	mk("nova-bench/results/.keep", "", 0o644)
	mk(".local/bin/nova-swarm", "#!/bin/sh\necho 'nova-swarm v0.16.0-"+lineupWant+"'\n", 0o755)
	mk(".local/bin/nova-pulse", "#!/bin/sh\necho 'nova-pulse v0.16.0-"+lineupWant+"'\n", 0o755)
	mk("rowan-swarm-root/slot-01/jobs/card-done/RESULT.md", "DONE\n", 0o644)
	mk("rowan-swarm-root/slot-01/jobs/card-running/.lease", "", 0o644)
	old := time.Now().Add(-time.Hour)
	for _, p := range []string{"rowan-swarm-root/slot-01/jobs/card-done", "rowan-swarm-root/slot-01/jobs/card-done/RESULT.md"} {
		if err := os.Chtimes(filepath.Join(home, p), old, old); err != nil {
			t.Fatal(err)
		}
	}
	fakeSSH := filepath.Join(dir, "ssh")
	// The fake drops every argument (the options, the target, `bash -s`) and runs the
	// script on stdin with a known credential in the environment.
	if err := os.WriteFile(fakeSSH, []byte("#!/bin/sh\nGH_TOKEN=fixture exec bash -s\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	benches := filepath.Join(dir, "benches.tsv")
	if err := os.WriteFile(benches, []byte("fixture\tfixture.invalid\t"+home+"\t-\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runLineup(t, "--profile", "coding", "--want", lineupWant, "--benches", benches, "--ssh", fakeSSH, "--timeout", "30s")
	if code != 1 {
		t.Fatalf("exit %d, want 1 (the finished job)\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}
	reds := redLines(out)
	if len(reds) != 1 || !strings.Contains(reds[0], "finished-jobs") || !strings.Contains(reds[0], "card-done") {
		t.Fatalf("want one RED, finished-jobs naming card-done:\n%s", out)
	}
	for _, want := range []string{
		"LINEUP fixture stage GREEN got=8/8",
		"LINEUP fixture push-credential GREEN source=env",
		"LINEUP fixture results-root GREEN",
		"LINEUP fixture version-nova-swarm GREEN",
		"LINEUP fixture version-nova-pulse GREEN",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
