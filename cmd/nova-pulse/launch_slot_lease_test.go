package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE PULSE LAUNCH CLI BENCH SLOT LEASE (nova-tools#1903).
//
// The verb function refuses without --slots-store/--owner; so does the flag
// parser, so a caller who never reaches Launch still sees native's one line.
// No paid native: the fake nova-swarm on PATH records argv.

func TestLaunchCmdWithoutSlotsStoreRefuses(t *testing.T) {
	const want = swarm.NoSlotsStoreRefusal
	dir := t.TempDir()
	specs := fakePATH(t)
	argvLog := filepath.Join(dir, "argv.log")
	fakeTool(t, specs, "nova-swarm", fakeSpec{Log: argvLog, Rules: []fakeRule{swarmVersionRule()}})

	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	card := writeMainFile(t, dir, "card-a.md", "RESULT card-a sha=000000000000\nbody card-a\n")
	cards := writeMainFile(t, dir, "cards.tsv", "card-a\t-\tpro\t"+card+"\n")

	var out, errb bytes.Buffer
	code := run([]string{"launch", "--cards", cards, "--root", root, "--slots", "2", "--deadline", "60"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("launch without --slots-store is refused with exit 2, got %d:\n%s%s", code, out.String(), errb.String())
	}
	if got := strings.TrimSuffix(errb.String(), "\n"); got != want {
		t.Errorf("the refusal is exactly\n  %s\nand it printed\n  %s", want, got)
	}
	if n := len(strings.Split(strings.TrimSuffix(errb.String(), "\n"), "\n")); n != 1 {
		t.Errorf("the refusal is ONE line, got %d:\n%s", n, errb.String())
	}
	if out.String() != "" {
		t.Errorf("a refusal writes nothing to stdout, got: %q", out.String())
	}
	if raw, err := os.ReadFile(argvLog); err == nil && strings.TrimSpace(string(raw)) != "" {
		t.Fatalf("zero batch runs, got %q", raw)
	}
}

func TestLaunchCmdWithSlotsStoreStillWorks(t *testing.T) {
	dir := t.TempDir()
	specs := fakePATH(t)
	argvLog := filepath.Join(dir, "argv.log")
	fakeTool(t, specs, "nova-swarm", fakeSpec{Log: argvLog, Rules: []fakeRule{swarmVersionRule()}})

	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	card := writeMainFile(t, dir, "card-a.md", "RESULT card-a sha=000000000000\nbody card-a\n")
	cards := writeMainFile(t, dir, "cards.tsv", "card-a\t-\tpro\t"+card+"\n")
	store := filepath.Join(dir, "slots-store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "shares.tsv"), []byte("capacity\t8\nreserve\t0\nfake-1\t8\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := run([]string{
		"launch", "--cards", cards, "--root", root, "--slots", "2", "--deadline", "60",
		"--slots-store", store, "--owner", "fake-1",
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("launch with a store still works, got exit %d:\n%s%s", code, out.String(), errb.String())
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	log := string(raw)
	if !strings.Contains(log, "--slots-store "+store) {
		t.Fatalf("batch argv lacks --slots-store: %q", log)
	}
	if !strings.Contains(log, "--owner fake-1") {
		t.Fatalf("batch argv lacks --owner: %q", log)
	}
}
