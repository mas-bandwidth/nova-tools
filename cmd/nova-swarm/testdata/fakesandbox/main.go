// The FAKE SANDBOX: a stand-in for nova-sandbox on PATH, so the LAUNCH SEAM is tested on
// every platform -- the argv the dispatcher builds, the probe it runs before the first
// worker, and the two refusals a machine with no wall produces -- with no dependence on
// whether this machine has a backend at all.
//
// It is NOT a wall and never claims to be one. The wall itself is proved against the real
// binary, on the platform whose body is built, by the tests that name their platform and
// skip elsewhere (SPEC-SANDBOX.md, "tests this spec demands": every test names the platform
// it runs on and is skipped with a reason, never silently).
//
// Its behaviour is the one environment variable NOVA_FAKE_SANDBOX:
//
//	pass       check says a backend is here, probe passes, and the exec verb runs the
//	           command after -- verbatim, so a whole pass runs through the seam
//	none       check says backend=none: the machine has no wall (rule 1)
//	probefail  check says a backend is here and the probe FAILS a check (rule 10)
//
// It writes the argv it was handed into `<--cwd>/sandbox-argv`, which is how a test reads
// the argv the dispatcher built for a worker. The file goes in the job directory rather
// than a path from the environment, because the supervisor BUILDS the child's environment
// rather than inheriting it -- no test variable reaches this far, and the job directory is
// where a job's own evidence belongs.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	args := os.Args[1:]
	mode := os.Getenv("NOVA_FAKE_SANDBOX")
	if mode == "" {
		mode = "pass"
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "fake sandbox: no arguments")
		os.Exit(2)
	}
	switch args[0] {
	case "check":
		backend := "fake-wall"
		net := "enforceable"
		if mode == "none" {
			backend, net = "none", "unenforceable"
		}
		fmt.Printf("CHECK OK backend=%s abi=- net=%s note=the fake sandbox of the swarm seam tests\n", backend, net)
		os.Exit(0)
	case "probe":
		if mode == "none" {
			fmt.Fprintln(os.Stderr, "PROBE REFUSED reason=no_sandbox: this machine has no backend")
			os.Exit(1)
		}
		if mode == "probefail" {
			fmt.Println("PROBE STEP name=write_outside expect=deny got=allow path=-")
			fmt.Fprintln(os.Stderr, "PROBE REFUSED reason=check: write_outside expected deny and got allow")
			os.Exit(1)
		}
		fmt.Println("PROBE STEP name=write_outside expect=deny got=deny path=-")
		fmt.Println("PROBE OK backend=fake-wall abi=- steps=5 passed=5 net=nopromise")
		os.Exit(0)
	}
	// The exec verb: everything after -- is run verbatim, and everything before it is the
	// two lists, which this stand-in records and does not enforce.
	i := indexOf(args, "--")
	if i < 0 || i+1 >= len(args) {
		fmt.Fprintln(os.Stderr, "SANDBOX REFUSED reason=no_command: nothing after --")
		os.Exit(125)
	}
	cwd := flagValue(args[:i], "--cwd")
	record(cwd, args)
	cmd := exec.Command(args[i+1], args[i+2:]...)
	cmd.Dir = cwd
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	fmt.Fprintf(os.Stderr, "SANDBOX OK backend=fake-wall abi=- read=%d write=%d net=nopromise cwd=%s cmd=%s\n",
		count(args[:i], "--read"), count(args[:i], "--write"), cwd, args[i+1])
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "SANDBOX REFUSED reason=sandbox_failed:", err)
		os.Exit(125)
	}
}

func record(cwd string, args []string) {
	if cwd == "" {
		return
	}
	path := filepath.Join(cwd, "sandbox-argv")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, strings.Join(args, " "))
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func count(args []string, name string) int {
	n := 0
	for _, a := range args {
		if a == name {
			n++
		}
	}
	return n
}
