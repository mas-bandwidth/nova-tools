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
