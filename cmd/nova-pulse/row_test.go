package main

// `nova-pulse row` and `nova-pulse status --store` at the command line.

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func runPulse(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, time.Now().UTC())
	return code, out.String(), errb.String()
}

// A bench pushes its row, and the table on the coordinator reads it back. One fake store,
// both verbs, no ssh anywhere.
func TestRowPushesAndStatusStoreRendersItBack(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	dir := t.TempDir()
	since := filepath.Join(dir, "SPRINT-START")
	if err := os.WriteFile(since, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errb := runPulse(t, "row", "--store", mr.Addr(), "--once",
		"--host", "test-bench", "--since", since, "--queue", filepath.Join(dir, "queue"),
		"--results", filepath.Join(dir, "results"), "--roots", dir, "--slots", filepath.Join(dir, "slots"),
	); code != 0 {
		t.Fatalf("row exit %d: %s", code, errb)
	}
	code, out, errb := runPulse(t, "status", "--store", mr.Addr())
	if code != 0 {
		t.Fatalf("status --store exit %d: %s", code, errb)
	}
	if !strings.HasPrefix(out, "SWARM TABLE  ") {
		t.Fatalf("the table's first line is the header: %s", out)
	}
	if !strings.Contains(out, "test-bench") || !strings.Contains(out, "total ") {
		t.Fatalf("the pushed row is not on the table:\n%s", out)
	}
}

// status --store is a mode of its own: it wants neither --queue nor --roots, which the
// queue report requires. A verb that demanded both to print a table read from the benches
// would be asking for two things it never reads.
func TestStatusStoreDoesNotWantTheQueueReportsFlags(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	if code, _, errb := runPulse(t, "status", "--store", mr.Addr()); code != 0 {
		t.Fatalf("status --store with no --queue exited %d: %s", code, errb)
	}
}

// --interval at or past the row's TTL is refused, not clamped: a bench pushing every five
// seconds with a five-second TTL flickers on and off the table.
func TestRowRefusesAnIntervalPastTheRowsTTL(t *testing.T) {
	t.Parallel()
	code, _, errb := runPulse(t, "row", "--store", "127.0.0.1:1", "--interval", "5s", "--once")
	if code != 2 {
		t.Fatalf("want 2, got %d", code)
	}
	if !strings.Contains(errb, "TTL") {
		t.Fatalf("the refusal must say why: %s", errb)
	}
}

func TestRowWantsAStoreUnlessItIsOnlyPrinting(t *testing.T) {
	t.Parallel()
	code, _, errb := runPulse(t, "row", "--once")
	if code != 2 || !strings.Contains(errb, "--store is required") {
		t.Fatalf("row with no store: exit %d, %s", code, errb)
	}
	dir := t.TempDir()
	since := filepath.Join(dir, "SPRINT-START")
	if err := os.WriteFile(since, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errb := runPulse(t, "row", "--print", "--host", "hulk", "--since", since,
		"--queue", filepath.Join(dir, "queue"), "--results", filepath.Join(dir, "results"),
		"--roots", dir, "--slots", filepath.Join(dir, "slots"))
	if code != 0 {
		t.Fatalf("row --print exit %d: %s", code, errb)
	}
	if !strings.Contains(out, "ROW bench=hulk") || !strings.Contains(out, "pushed=-") {
		t.Fatalf("print receipt: %s", out)
	}
}

// A store that cannot be reached is a refusal naming the address and the ACL user, and it
// never names the password or its value.
func TestRowRefusesAStoreItCannotReachWithoutNamingASecret(t *testing.T) {
	// No t.Parallel: t.Setenv and a parallel test cannot share a process.
	t.Setenv("NOVA_REDIS_BENCH_PASSWORD", "hunter2-not-a-real-password")
	code, _, errb := runPulse(t, "row", "--store", "127.0.0.1:1", "--once")
	if code != 2 {
		t.Fatalf("want 2, got %d", code)
	}
	if !strings.Contains(errb, "ROW REFUSED store") || !strings.Contains(errb, "127.0.0.1:1") {
		t.Fatalf("the refusal must name the address: %s", errb)
	}
	if strings.Contains(errb, "hunter2") {
		t.Fatalf("THE REFUSAL PRINTED THE PASSWORD: %s", errb)
	}
	if !strings.Contains(errb, "NOVA_REDIS_BENCH_PASSWORD") {
		t.Fatalf("the refusal must name the VARIABLE the password comes from: %s", errb)
	}
}

// The same rule on the reading side.
func TestStatusStoreRefusalNamesNoSecret(t *testing.T) {
	// No t.Parallel: t.Setenv and a parallel test cannot share a process.
	t.Setenv("NOVA_REDIS_BENCH_PASSWORD", "hunter2-not-a-real-password")
	code, _, errb := runPulse(t, "status", "--store", "127.0.0.1:1")
	if code != 2 {
		t.Fatalf("want 2, got %d", code)
	}
	if strings.Contains(errb, "hunter2") {
		t.Fatalf("THE REFUSAL PRINTED THE PASSWORD: %s", errb)
	}
}

// --out writes the table to a file atomically, which is what everybody watching it with
// `watch cat` reads; nothing goes to stdout in that mode.
func TestStatusStoreWritesTheTableToAFile(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	mr.HSet("bench:hulk", "host", "hulk", "queue", "1", "working", "0", "done", "2", "ok", "2", "fail", "0", "load1", "0.10", "ncpu", "16")
	path := filepath.Join(t.TempDir(), "SWARM-TABLE.txt")
	code, out, errb := runPulse(t, "status", "--store", mr.Addr(), "--out", path)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("with --out the table goes to the file, not to stdout: %q", out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "hulk") {
		t.Fatalf("the file does not hold the table:\n%s", raw)
	}
}

// --friends names the roster a friends: line must mention even when a friend's key is
// absent. No friend's name is baked into this tool.
func TestStatusStoreNamesAnAbsentFriendFromTheRoster(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	mr.HSet("bench:hulk", "host", "hulk", "load1", "0.10")
	code, out, errb := runPulse(t, "status", "--store", mr.Addr(), "--friends", "johnny,freddy")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(out, "friends: freddy none · johnny none") {
		t.Fatalf("a friend with no key at all reads as none:\n%s", out)
	}
}

// The help lists both verbs.
func TestRowAndSwarmTableAreInTheHelp(t *testing.T) {
	t.Parallel()
	_, out, _ := runPulse(t, "help")
	for _, want := range []string{"nova-pulse row ", "nova-pulse status  --store "} {
		if !strings.Contains(out, want) {
			t.Errorf("the help does not list %q", want)
		}
	}
}

// row --print over an unreadable queue prints `?` and says why, and exits non-zero: the
// operator asking "why does my bench say 0?" gets "I could not read it", not a zero
// (Stella HOLD 7 on #2622).
func TestRowPrintSaysAnUnreadableQueueIsUnknown(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a 000-mode directory that this user cannot read")
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "queue", "ready")
	if err := os.MkdirAll(ready, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ready, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ready, 0o755) })
	code, out, errb := runPulse(t, "row", "--print", "--host", "hulk", "--since", filepath.Join(dir, "no-stamp"),
		"--queue", filepath.Join(dir, "queue"), "--results", filepath.Join(dir, "results"),
		"--roots", dir, "--slots", filepath.Join(dir, "slots"))
	if code == 0 {
		t.Fatalf("an unreadable queue exited 0: %s", out)
	}
	if !strings.Contains(out, " queue=? ") || !strings.Contains(errb, "unavailable: queue: ") {
		t.Fatalf("want queue=? and the reason: out=%s err=%s", out, errb)
	}
}
