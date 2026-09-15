package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The probe runs every check through a FAKE ssh on PATH that records its argv and
// execs the rest locally, with fake stat, nproc, taskset and nova-sandbox standing
// in for the ones a bench would have. Nothing reaches the network.

// probeFixture is one bench's fake world: a root with a fake remote nova-swarm, a
// harness, an auth file, and a bin dir of fake ssh and friends.
type probeFixture struct {
	t       *testing.T
	dir     string
	fakeBin string
	root    string
	harness string
	auth    string
	sshLog  string
	env     []string
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// newProbeFixture builds the world and returns it with the remote nova-swarm
// answering with the real built binary's own version.
func newProbeFixture(t *testing.T) *probeFixture {
	t.Helper()
	tool, _ := builtBinaries(t)
	f := &probeFixture{t: t, dir: t.TempDir()}
	f.fakeBin = filepath.Join(f.dir, "bin")
	f.root = filepath.Join(f.dir, "swarm-root")
	f.harness = filepath.Join(f.dir, "harness")
	f.auth = filepath.Join(f.dir, "auth")
	f.sshLog = filepath.Join(f.dir, "ssh.log")

	writeScript(t, filepath.Join(f.fakeBin, "ssh"), "printf '%s\\n' \"$*\" >> \"$SSH_LOG\"\nshift\nexec \"$@\"")
	writeScript(t, filepath.Join(f.fakeBin, "stat"), "printf '%s\\n' \"${STAT_MODE:-600}\"")
	writeScript(t, filepath.Join(f.fakeBin, "nproc"), "printf '%s\\n' \"${NPROC:-16}\"")
	writeScript(t, filepath.Join(f.fakeBin, "nova-sandbox"), "printf 'CHECK OK backend=%s abi=x net=nopromise hosts=none note=\\n' \"${SANDBOX_BACKEND:-none}\"")
	writeScript(t, f.harness, "exit 0")
	writeScript(t, filepath.Join(f.root, "bin", "nova-swarm"), "exec \"$REAL_NOVA_SWARM\" version")

	if err := os.WriteFile(f.auth, []byte("key not read\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.env = []string{
		"PATH=" + f.fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"SSH_LOG=" + f.sshLog,
		"REAL_NOVA_SWARM=" + tool,
		"SANDBOX_BACKEND=sandbox-exec",
	}
	return f
}

func (f *probeFixture) writeTable(t *testing.T, cores, wall string) string {
	t.Helper()
	p := filepath.Join(f.dir, "benches.tsv")
	body := "name\thost\troot\tcores\tharness\tauth\twall\n" +
		"b2\tb2\t" + f.root + "\t" + cores + "\t" + f.harness + "\t" + f.auth + "\t" + wall + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// probe runs the probe verb and returns exit, stdout, stderr and the ssh log.
func (f *probeFixture) probe(t *testing.T, args ...string) (int, string, string, string) {
	t.Helper()
	tool, _ := builtBinaries(t)
	cmd := exec.Command(tool, append([]string{"bench", "probe"}, args...)...)
	cmd.Env = f.env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running probe: %v", err)
	}
	log, _ := os.ReadFile(f.sshLog)
	return exit, out.String(), errb.String(), string(log)
}

// A matching bench probes green: BENCH OK with cores, pin and wall read from the row.
func TestBenchProbeOK(t *testing.T) {
	f := newProbeFixture(t)
	table := f.writeTable(t, "1-15", "sandbox")
	writeScript(t, filepath.Join(f.fakeBin, "taskset"), "exit 0")
	exit, stdout, _, _ := f.probe(t, "--benches", table, "--bench", "b2")
	if exit != 0 {
		t.Fatalf("a matching bench probes green, got %d:\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, "BENCH OK name=b2 cores=15 pin=taskset wall=sandbox") {
		t.Errorf("BENCH OK line wrong:\n%s", stdout)
	}
}

// Different identities are refused, naming both.
func TestBenchProbeRefusesVersionMismatch(t *testing.T) {
	f := newProbeFixture(t)
	writeScript(t, filepath.Join(f.root, "bin", "nova-swarm"), "printf 'nova-swarm WRONG linux/amd64 go1.20\\n'")
	writeScript(t, filepath.Join(f.fakeBin, "taskset"), "exit 0")
	table := f.writeTable(t, "1-15", "sandbox")
	exit, stdout, _, _ := f.probe(t, "--benches", table, "--bench", "b2")
	if exit != 1 {
		t.Fatalf("a version mismatch refuses at exit 1, got %d:\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, "check=version") || !strings.Contains(stdout, "WRONG") || !strings.Contains(stdout, "local=") {
		t.Errorf("the refusal names both identities:\n%s", stdout)
	}
}

// The auth file is only ever stat'd (never read), and mode 0644 is a refusal.
func TestBenchProbeNeverReadsAuth(t *testing.T) {
	f := newProbeFixture(t)
	if err := os.Chmod(f.auth, 0o644); err != nil {
		t.Fatal(err)
	}
	f.env = append(f.env, "STAT_MODE=644")
	writeScript(t, filepath.Join(f.fakeBin, "taskset"), "exit 0")
	table := f.writeTable(t, "1-15", "sandbox")
	exit, stdout, _, log := f.probe(t, "--benches", table, "--bench", "b2")
	_ = stdout
	if exit != 1 {
		t.Fatalf("mode 0644 refuses at exit 1, got %d:\n%s", exit, stdout)
	}
	// The fake ssh saw a stat of the auth path and never a read of it.
	if !strings.Contains(log, "stat -c %a "+f.auth) {
		t.Errorf("the probe stats the auth path:\n%s", log)
	}
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, f.auth) && (strings.Contains(line, "cat") || strings.Contains(line, "head") || strings.Contains(line, "cp")) {
			t.Errorf("the probe read the auth file: %s", line)
		}
	}
}

// cores=- on a bench without taskset probes pin=none and admits; a list on the same
// bench is refused check=pin.
func TestPinNoneRowAdmitsWithoutTaskset(t *testing.T) {
	f := newProbeFixture(t)
	table := f.writeTable(t, "-", "sandbox")
	exit, stdout, _, _ := f.probe(t, "--benches", table, "--bench", "b2")
	if exit != 0 {
		t.Fatalf("a - cores row without taskset admits, got %d:\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, "BENCH OK name=b2 cores=- pin=none wall=sandbox") {
		t.Errorf("BENCH OK line wrong:\n%s", stdout)
	}

	table2 := f.writeTable(t, "1-15", "sandbox")
	exit, stdout, _, _ = f.probe(t, "--benches", table2, "--bench", "b2")
	if exit != 1 {
		t.Fatalf("a cores list without taskset refuses, got %d:\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, "BENCH REFUSED name=b2 check=pin") {
		t.Errorf("the pin check refuses:\n%s", stdout)
	}
}
