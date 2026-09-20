package main

// #2014 at the argv: the second argument the launcher is given is the bench's seat from the
// machines registry. It was `swarm-`+bench, a name this package made up, and on the night of
// the 2026-09-20 load test every card dealt to the Studio -- whose seat is `studio` -- died
// at `SECRETS EXEC FAIL store file .../swarm-studio.yaml is absent` and bounced.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seatRegistry writes a machines registry whose rows carry the seats the test names, as
// `<name>=<seat>`.
func seatRegistry(t *testing.T, dir string, rows ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("# name\tssh\tos/arch\troles\tseat\tcores\tnotes\n")
	for _, row := range rows {
		name, seat, ok := strings.Cut(row, "=")
		if !ok {
			seat = "-"
		}
		b.WriteString(name + "\t" + name + "\tlinux/x64\tbench\t" + seat + "\t64\t-\n")
	}
	path := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFillGivesTheLauncherTheRegistrySeat(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	fakeTool(t, specs, "nova-swarm", fakeSpec{Log: log, Default: fakeRule{Exit: 0}})
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeMainFile(t, ready, "card-001.md", "a card\n")

	var out, errb bytes.Buffer
	code := run([]string{"fill", "--ready", ready, "--launched", launched,
		"--machines", seatRegistry(t, dir, "studio=studio"),
		"--bench", "studio", "--capacity", "1", "--once", "--launch-grace", "0",
		"--launcher", filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "swarm-studio") {
		t.Fatalf("the launcher was given the invented seat swarm-studio: %q", raw)
	}
	if !strings.Contains(string(raw), "nova-swarm studio studio ") {
		t.Fatalf("the launcher was not given the registry's seat `studio` as its second argument: %q", raw)
	}
}

// TestFillUsesTheRegistrySeatForStoreCapacityAndLaunch drives the real command boundary.
// Studio's share is two and one studio lease is live, so exactly one of two cards may run.
// Before #2029 the probe asked for swarm-studio, missed both facts, and used the formula.
func TestFillUsesTheRegistrySeatForStoreCapacityAndLaunch(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	fakeTool(t, specs, "nova-swarm", fakeSpec{
		Log:     log,
		Rules:   []fakeRule{{Arg: 1, Equals: "slots", Stdout: "SLOT 1 owner=studio pid=101 label=x until=2026-09-20T23:00:00Z state=live\n"}},
		Default: fakeRule{Exit: 0},
	})
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeMainFile(t, ready, "card-001.md", "a card\n")
	writeMainFile(t, ready, "card-002.md", "a card\n")
	store := fillStore(t, dir, "studio", 2)
	bin := filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix())

	var out, errb bytes.Buffer
	code := run([]string{"fill", "--ready", ready, "--launched", launched,
		"--machines", seatRegistry(t, dir, "studio=studio"), "--bench", "studio",
		"--local-bench", "studio", "--slots-store", store, "--slots-bin", bin,
		"--max-load-per-core", "0", "--launcher", bin, "--launch-grace", "0", "--once",
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stdout=%q stderr=%q", code, out.String(), errb.String())
	}
	if got := fillCount(t, launched); got != 1 {
		t.Fatalf("launched holds %d cards, want 1; stdout=%q stderr=%q", got, out.String(), errb.String())
	}
	if got := fillCount(t, ready); got != 1 {
		t.Fatalf("ready holds %d cards, want 1", got)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	argv := string(raw)
	if !strings.Contains(argv, "slots list --store ") || !strings.Contains(argv, "nova-swarm studio studio ") {
		t.Fatalf("probe and launcher did not both use Studio's seat: %q", argv)
	}
	if strings.Contains(argv, "swarm-studio") {
		t.Fatalf("command invented swarm-studio instead of using the registry seat: %q", argv)
	}
}
