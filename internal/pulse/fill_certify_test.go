package pulse

// The fill's half of Glenn's certification directive of 2026-09-18.
//
// The hurt: hulk was in the loop, met the provisioning standard, and the first real Go card
// dealt to it that morning died inside the swarm wall -- the wall could not read
// `$HOME/sdk/go1.26.5`, so the only go was /usr/bin/go 1.22 and go.mod refused it. The card
// was spent finding out something a mechanical check could have said in a second. The rule
// that retires it: a card may not be launched onto a machine that has not PROVED it can do
// that kind of work, and the proof is a current certificate.
//
// Every test here is fake-driven: a table of certificates on disk, a build reader that
// answers from a map, a recording launcher. Nothing opens an ssh connection.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// certBuilds answers the installed build per machine from a table, and records every read so
// a test can hold the once-per-tick promise.
type certBuilds struct {
	table map[string]string
	reads []string
}

func (b *certBuilds) Build(machine string) (string, error) {
	b.reads = append(b.reads, machine)
	return b.table[machine], nil
}

// certsFile writes a certificates file from rows.
func certsFile(t *testing.T, dir string, rows ...fleet.Certificate) string {
	t.Helper()
	p := filepath.Join(dir, "certs.tsv")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if err := fleet.AppendCertificate(p, r); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func certifiedRow(machine, class string) fleet.Certificate {
	return fleet.Certificate{
		Machine: machine, Build: "v0.17.0", Hash: "standard-hash", Class: class,
		Verdict: fleet.VerdictOK, Evidence: "GO OK go version go1.26.5",
		At: time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC),
	}
}

// TestFillRefusesACardWhoseWorkloadIsUncertifiedOnThatBench is the rule, as the line Glenn
// asked for. The card STAYS READY: the remedy is run and the same card is dealt again.
func TestFillRefusesACardWhoseWorkloadIsUncertifiedOnThatBench(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD-1\nLANG: go\n")
	writeCard(t, ready, "card-002.md", "RESULT: CARD-2\nLANG: go\n")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"hulk"}, nil),
		Benches:  []string{"hulk"},
		Once:     true, Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"hulk": 10}, Launcher: l,
		Certs: certsFile(t, dir), Hash: "standard-hash",
		Build: &certBuilds{table: map[string]string{"hulk": "v0.17.0"}},
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0", code)
	}
	if len(l.calls) != 0 {
		t.Fatalf("an uncertified bench was given %d cards: %q", len(l.calls), l.calls)
	}
	if got := len(readyCards(ready)); got != 2 {
		t.Fatalf("ready holds %d cards, want 2 (a refused card is never lost)", got)
	}
	want := `FILL REFUSED bench=hulk reason=uncertified workload=go-test remedy="nova-pulse fleet certify --machine hulk"`
	if !strings.Contains(errb.String(), want) {
		t.Fatalf("the refusal line is not the one the rule names:\nwant: %s\ngot:  %s", want, errb.String())
	}
	// One line for a queue of two: a queue of forty would otherwise be forty.
	if n := strings.Count(errb.String(), "reason=uncertified"); n != 1 {
		t.Errorf("printed %d uncertified lines for one bench and one class, want 1", n)
	}
}

// TestFillLaunchesOntoACertifiedBench: the same queue, with the certificate written.
func TestFillLaunchesOntoACertifiedBench(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD-1\nLANG: go\n")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	builds := &certBuilds{table: map[string]string{"hulk": "v0.17.0"}}
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"hulk"}, nil),
		Benches:  []string{"hulk"},
		Once:     true, Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"hulk": 10}, Launcher: l,
		Certs: certsFile(t, dir, certifiedRow("hulk", "go-test")), Hash: "standard-hash",
		Build: builds,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 1 {
		t.Fatalf("launcher calls = %d, want 1 (stderr=%q)", len(l.calls), errb.String())
	}
	if len(builds.reads) != 1 {
		t.Errorf("the build was read %d times for one machine in one tick, want 1", len(builds.reads))
	}
}

// TestACertificateForAnotherBuildIsNoCertificate: the machine adopted a new release, so the
// row it holds was written about a machine that no longer exists.
func TestACertificateForAnotherBuildIsNoCertificate(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD-1\nLANG: go\n")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"hulk"}, nil),
		Benches:  []string{"hulk"},
		Once:     true, Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"hulk": 10}, Launcher: l,
		Certs: certsFile(t, dir, certifiedRow("hulk", "go-test")), Hash: "standard-hash",
		Build: &certBuilds{table: map[string]string{"hulk": "v0.18.0"}},
	})
	if len(l.calls) != 0 {
		t.Fatalf("a card was launched on a bench certified under another build: %q", l.calls)
	}
	if !strings.Contains(errb.String(), "reason=uncertified") {
		t.Errorf("no refusal:\n%s", errb.String())
	}
}

// TestTheGateIsOffWithoutTheThreeInputs is the documented narrowing: a loop that has not
// adopted certification behaves exactly as it did before, and says nothing about it.
func TestTheGateIsOffWithoutTheThreeInputs(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD-1\nLANG: go\n")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"hulk"}, nil),
		Benches:  []string{"hulk"},
		Once:     true, Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"hulk": 10}, Launcher: l,
	})
	if len(l.calls) != 1 {
		t.Fatalf("launcher calls = %d, want 1 with no certification wired", len(l.calls))
	}
	if strings.Contains(errb.String(), "uncertified") {
		t.Errorf("the gate spoke with nothing wired:\n%s", errb.String())
	}
}

// TestTheCardsWorkloadClassIsItsOwnLineThenItsLanguage.
func TestTheCardsWorkloadClassIsItsOwnLineThenItsLanguage(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, body, want string }{
		{"its own line wins", "workload: sbcl\nLANG: go\n", "sbcl"},
		{"LANG go", "LANG: go\n", "go-test"},
		{"LANG c", "LANG: c\n", "c-build"},
		{"LANG c++", "LANG: c++\n", "cpp-build"},
		{"LEG lisp", "LEG: lisp\n", "sbcl"},
		{"no language at all", "RESULT: CARD-1\n", "go-test"},
		{"a language nobody mapped", "LANG: fortran\n", "go-test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".md")
			if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := cardWorkload(p); got != tc.want {
				t.Errorf("cardWorkload = %q, want %q", got, tc.want)
			}
		})
	}
}
