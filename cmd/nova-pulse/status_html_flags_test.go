package main

// The flags the adoption attempt asked for have to reach the verb, not merely exist. Each
// one here is driven through the real command line, with the three seams replaced so no
// test starts ssh, reads this machine's process table or ships a file anywhere.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func withSelfReader(t *testing.T, r pulse.SelfReader) {
	t.Helper()
	old := statusHTMLSelfReader
	statusHTMLSelfReader = r
	t.Cleanup(func() { statusHTMLSelfReader = old })
}

func withPublisher(t *testing.T, p pulse.Publisher) {
	t.Helper()
	old := statusHTMLPublisher
	statusHTMLPublisher = p
	t.Cleanup(func() { statusHTMLPublisher = old })
}

// oneBench is the smallest fleet file plus a reader that answers for it.
func oneBench(t *testing.T) string {
	t.Helper()
	withFleetReader(t, testFleetReader(t,
		pulse.BenchReading{Name: "alpha", Live: 1, Cores: 4, Load: 1, FreeGB: 90, MemGB: 30, Allowed: 2}))
	benches := filepath.Join(t.TempDir(), "benches.tsv")
	write(t, benches, "alpha\tfake-alpha\t/tmp/alpha\t-\n")
	return benches
}

// --self and --loop reach the verb and land on the page.
func TestStatusHTMLSelfAndLoopFlagsReachThePage(t *testing.T) {
	benches := oneBench(t)
	var gotName string
	var gotLoops []string
	withSelfReader(t, func(name string, loops []string, _ time.Time) pulse.SelfReading {
		gotName, gotLoops = name, loops
		return pulse.SelfReading{Name: name, CIRunners: 4, Cores: 20, Load: 147, FreeGB: 300, Orphans: 19,
			Loops: []pulse.LoopCount{{Label: "harvest", N: 1}}}
	})
	dir := t.TempDir()
	out := filepath.Join(dir, "index.html")
	exit, _, stderr := invokePulse(t, "status", "--queue", t.TempDir(), "--benches", benches, "--html", out,
		"--self", "studio", "--loop", "harvest=harvest-loop")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr)
	}
	if gotName != "studio" {
		t.Errorf("--self reached the reader as %q", gotName)
	}
	if len(gotLoops) != 1 || gotLoops[0] != "harvest=harvest-loop" {
		t.Errorf("--loop reached the reader as %v", gotLoops)
	}
	html := readFile(t, out)
	for _, want := range []string{"<td>studio</td>", "ci=4", "orphans=19", "loops on studio", "harvest=1"} {
		if !strings.Contains(html, want) {
			t.Errorf("the page is missing %q:\n%s", want, html)
		}
	}
}

// --publish reaches the verb and ships both files.
func TestStatusHTMLPublishFlagShipsBothFiles(t *testing.T) {
	benches := oneBench(t)
	var dest string
	var names []string
	withPublisher(t, func(d string, files []pulse.PublishFile, _ time.Duration) error {
		dest = d
		for _, f := range files {
			names = append(names, f.Name)
		}
		return nil
	})
	dir := t.TempDir()
	exit, stdout, stderr := invokePulse(t, "status", "--queue", t.TempDir(), "--benches", benches,
		"--html", filepath.Join(dir, "index.html"), "--publish", "space:status")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr)
	}
	if dest != "space:status" {
		t.Errorf("--publish reached the publisher as %q", dest)
	}
	if strings.Join(names, ",") != "index.html,metrics.tsv" {
		t.Errorf("published %v, want the page and the series", names)
	}
	if !strings.Contains(stdout, "published=space:status") {
		t.Errorf("the line does not say where it published:\n%s", stdout)
	}
}

// --timeout takes what the tool prints. A flag that refuses the duration on its own
// progress line is the trap the adoption attempt walked into.
func TestStatusHTMLTimeoutTakesADuration(t *testing.T) {
	benches := oneBench(t)
	for _, arg := range []string{"120", "90s", "2m"} {
		dir := t.TempDir()
		exit, _, stderr := invokePulse(t, "status", "--queue", t.TempDir(), "--benches", benches,
			"--html", filepath.Join(dir, "index.html"), "--timeout", arg)
		if exit != 0 {
			t.Errorf("--timeout %s exit = %d, want 0; stderr=%s", arg, exit, stderr)
		}
	}
	dir := t.TempDir()
	exit, _, stderr := invokePulse(t, "status", "--queue", t.TempDir(), "--benches", benches,
		"--html", filepath.Join(dir, "index.html"), "--timeout", "soon")
	if exit != 2 {
		t.Fatalf("--timeout soon exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr, "--timeout") {
		t.Errorf("the refusal does not name --timeout:\n%s", stderr)
	}
}

// The help names every one of them: a flag only the source knows about is a flag nobody
// finds, and the next reader will write the shell script again.
func TestHelpNamesTheStatusHTMLFlags(t *testing.T) {
	exit, stdout, stderr := invokePulse(t, "help")
	if exit != 0 {
		t.Fatalf("help exit = %d; stderr=%s", exit, stderr)
	}
	for _, want := range []string{"--publish", "--gh-config", "--day-start", "--self", "--loop", "GH_CONFIG_DIR"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the help does not name %q", want)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
