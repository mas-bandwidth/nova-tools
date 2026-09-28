package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// #3880 DONE-WHEN: two fake benches beating different nova-sprint stamps make
// `nova-update report --store` print exactly one DRIFT line, naming the stale
// bench, with no ssh and no bus note. The beats are what ns_bench_beat writes
// (bench:<b>:beat, field build = the nova-sprint version line) and the
// registry is the benches set; the store is a throwaway miniredis. ssh and git
// on PATH are traps that leave a mark if anything runs them.
func TestReportStorePrintsOneDriftLineForTheStaleBench(t *testing.T) {
	t.Setenv("NOVA_TEST_NO_HOST", "1")
	t.Setenv("NOVA_SPRINT_REDIS_USER", "")
	trap := t.TempDir()
	mark := filepath.Join(trap, "ran")
	for _, tool := range []string{"ssh", "git", "gh"} {
		script := "#!/bin/sh\necho " + tool + " >> " + mark + "\nexit 1\n"
		if err := testbin.WriteExecutable(filepath.Join(trap, tool), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", trap)

	mr := miniredis.RunT(t)
	mr.SAdd("benches", "fresh", "stale", "quiet")
	beat := func(bench, build string) {
		mr.HSet("bench:"+bench+":beat", "host", bench, "at", "1790186398000", "build", build)
		mr.SetTTL("bench:"+bench+":beat", 3*time.Second)
	}
	beat("fresh", "nova-sprint 20260925120000-aaaaaaaaaaaa darwin/arm64 go1.26.1")
	beat("stale", "nova-sprint 20260924090000-bbbbbbbbbbbb darwin/arm64 go1.26.1")
	// quiet is registered but not beating: it says nothing, so it is not drift.

	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"report", "--store", mr.Addr()}, "test", &out, &errs, Environment{})
	all := out.String() + errs.String()
	if code != 1 {
		t.Fatalf("exit %d, want 1 (drift found)\n%s", code, all)
	}
	var drift []string
	for _, l := range strings.Split(all, "\n") {
		if strings.Contains(l, "DRIFT") {
			drift = append(drift, l)
		}
	}
	if len(drift) != 1 {
		t.Fatalf("want exactly one DRIFT line, got %d:\n%s", len(drift), all)
	}
	if !strings.Contains(drift[0], "bench=stale") || !strings.Contains(drift[0], "build=20260924090000-bbbbbbbbbbbb") || !strings.Contains(drift[0], "want=20260925120000-aaaaaaaaaaaa") {
		t.Fatalf("the DRIFT line does not name the stale bench and both stamps: %s", drift[0])
	}
	if !strings.Contains(errs.String(), "REPORT FAIL benches=3 beating=2 current=1 drift=1 unknown=0") {
		t.Fatalf("receipt line missing or wrong:\n%s", all)
	}
	if strings.Contains(all, "From:") || strings.Contains(all, "NOTE") || strings.Contains(all, "sent=") {
		t.Fatalf("a bus note was written:\n%s", all)
	}
	if b, err := os.ReadFile(mark); err == nil {
		t.Fatalf("report --store ran %s", strings.TrimSpace(string(b)))
	}

	// The stale bench updates: one beat later the fleet is current and the
	// verb exits 0 with no DRIFT line.
	beat("stale", "nova-sprint 20260925120000-aaaaaaaaaaaa linux/amd64 go1.26.1")
	out.Reset()
	errs.Reset()
	if code := Run("nova-update", []string{"report", "--store", mr.Addr()}, "test", &out, &errs, Environment{}); code != 0 {
		t.Fatalf("exit %d after the stale bench caught up\n%s%s", code, out.String(), errs.String())
	}
	if strings.Contains(out.String()+errs.String(), "DRIFT") || !strings.Contains(out.String(), "REPORT OK benches=3 beating=2 current=2 drift=0 unknown=0") {
		t.Fatalf("a current fleet printed:\n%s%s", out.String(), errs.String())
	}
}

// --store is the fleet read: it takes no manifest, no snapshot and no note, so
// a combination that would write a bus note is refused with exit 2.
func TestReportStoreRefusesANote(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"report", "--store", "127.0.0.1:1", "--send", "--as", "a", "--to", "b", "--bus", "x", "--remote", "r", "--branch", "b"},
		{"report", "--store", "127.0.0.1:1", "--draft", "--as", "a", "--to", "b"},
		{"report", "--store", "127.0.0.1:1", "--file", "m.tsv"},
	} {
		var out, errs bytes.Buffer
		if code := Run("nova-update", args, "test", &out, &errs, Environment{}); code != 2 || !strings.Contains(errs.String(), "REPORT REFUSED") {
			t.Fatalf("%v: exit %d, want 2 with a refusal\n%s%s", args, code, out.String(), errs.String())
		}
	}
}
