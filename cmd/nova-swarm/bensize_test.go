package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// sizeFixture is one bench's fake world for bench size: a local row whose harness
// exits clean after a fixed pause (so the round's throughput scales with W
// deterministically) and whose fake cat answers a quiet /proc/loadavg, on a PATH the
// size verb's exec reads. Nothing reaches a network.
type sizeFixture struct {
	t       *testing.T
	dir     string
	fakeBin string
	root    string
	harness string
	auth    string
}

func newSizeFixture(t *testing.T) *sizeFixture {
	t.Helper()
	f := &sizeFixture{t: t, dir: t.TempDir()}
	f.fakeBin = filepath.Join(f.dir, "bin")
	f.root = filepath.Join(f.dir, "swarm-root")
	f.harness = filepath.Join(f.dir, "harness")
	f.auth = filepath.Join(f.dir, "auth")

	// A quiet bench: the one-minute load average reads 0.00.
	writeScript(t, filepath.Join(f.fakeBin, "cat"), "printf '0.00 0.00 0.00\\n'\n")
	// The known-answer card always completes: a fixed pause, then a clean exit.
	writeScript(t, f.harness, "sleep 0.05\nexit 0\n")
	if err := os.WriteFile(f.auth, []byte("key not read\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *sizeFixture) writeTable(t *testing.T, cores string) string {
	t.Helper()
	p := filepath.Join(f.dir, "benches.tsv")
	body := "name\thost\troot\tcores\tharness\tauth\twall\n" +
		"b2\tlocal\t" + f.root + "\t" + cores + "\t" + f.harness + "\t" + f.auth + "\tsandbox\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *sizeFixture) size(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	tool, _ := builtBinaries(t)
	cmd := exec.Command(tool, append([]string{"bench", "size"}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+f.fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running bench size: %v", err)
	}
	return exit, out.String(), errb.String()
}

// The row gains its width, measured stamp and the running tool's sha8, and the BENCH
// WIDTH line names them all (SPEC-SWARM, "Benches", replay 20).
func TestSizeRecordsWidthWithVersion(t *testing.T) {
	f := newSizeFixture(t)
	table := f.writeTable(t, "1-15")
	exit, stdout, stderr := f.size(t, "--benches", table, "--bench", "b2", "--max", "4")
	if exit != 0 {
		t.Fatalf("a quiet bench measures clean, got exit %d:\n%s%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "BENCH WIDTH bench=b2 width=4 cores=15 rows=1") {
		t.Fatalf("BENCH WIDTH line wrong:\n%s", stdout)
	}
	benches, err := swarm.LoadBenchTable(table)
	if err != nil {
		t.Fatal(err)
	}
	b := benches[0]
	if b.Width != 4 {
		t.Errorf("the row records width=4, got %d", b.Width)
	}
	if b.Measured == "" {
		t.Errorf("the row records a measured stamp, got empty")
	}
	if len(b.Version) != 8 {
		t.Errorf("the row records the running tool's sha8 (8 characters), got %q", b.Version)
	}
}
