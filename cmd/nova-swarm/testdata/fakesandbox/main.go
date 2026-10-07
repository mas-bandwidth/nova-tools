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
//	hosts      like pass, but check also says hosts=enforceable: the wall can express a
//	           repo allow rule (network to github.com), so the native run builds --repo
//	           rules instead of refusing
//
// The exec verb's SANDBOX OK line writes the cwd the way the real wall does -- a readable
// cwd=<dir> field through oneline.Field and the machine-readable cwdb64=<base64url>
// receipt of the raw path bytes. NOVA_SWARM_FAKE_RECEIPT makes the exec verb corrupt that
// receipt on purpose, so the native refusal path is testable:
//
//	wrong      the receipt decodes to a path that is not the applied --cwd
//	malformed  the receipt is not valid base64url at all
//
// It writes the argv it was handed into `<--cwd>/sandbox-argv`, which is how a test reads
// the argv the dispatcher built for a worker. The file goes in the job directory rather
// than a path from the environment, because the supervisor BUILDS the child's environment
// rather than inheriting it -- no test variable reaches this far, and the job directory is
// where a job's own evidence belongs.
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
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
		hosts := "none"
		if mode == "none" {
			backend, net = "none", "unenforceable"
		} else if mode == "hosts" {
			// A wall that can express a repo allow rule: network to github.com for the
			// named repos is a host rule, and this stand-in claims it so the native run's
			// seam test can assert the --repo allow rules it builds.
			hosts = "enforceable"
		}
		fmt.Printf("CHECK OK backend=%s abi=- net=%s hosts=%s note=the fake sandbox of the swarm seam tests\n", backend, net, hosts)
		os.Exit(0)
	case "probe":
		if mode == "none" {
			fmt.Fprintln(os.Stderr, "PROBE REFUSED reason=no_sandbox: this machine has no backend")
			os.Exit(1)
		}
		if mode == "probefail" {
			// ALL FIVE CHECKS RUN EVEN WHEN ONE FAILS (SPEC-SANDBOX test 10), so the
			// refusal is NOT the last line: the checks after it pass and print. The
			// stand-in says so on purpose, because the dispatcher used to quote the last
			// line and told a reader that `read_root expect=allow got=allow` was the
			// reason no worker started.
			fmt.Println("PROBE STEP name=write_outside expect=deny got=allow path=-")
			fmt.Fprintln(os.Stderr, "PROBE REFUSED reason=check: write_outside expected deny and got allow")
			fmt.Println("PROBE STEP name=read_root expect=allow got=allow path=-")
			fmt.Println("PROBE STEP name=secret_outside expect=deny got=deny path=-")
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
	// cwd is rendered exactly as the real wall renders it (cmd/nova-sandbox main.go): a
	// readable cwd=<dir> field through oneline.Field and the cwdb64=<base64url> receipt of
	// the raw path bytes, which is the field a reader must decode -- the readable one, whose
	// escape is not injective, is only the fallback a producer that prints no receipt leaves.
	// The cmd= field is rendered through oneline.Field too (issue #572), so a command holding
	// a space reaches the consumer as one escaped token rather than splitting the line.
	// NOVA_SWARM_FAKE_RECEIPT corrupts the receipt for the refusal tests.
	receipt := base64.RawURLEncoding.EncodeToString([]byte(cwd))
	switch os.Getenv("NOVA_SWARM_FAKE_RECEIPT") {
	case "wrong":
		receipt = base64.RawURLEncoding.EncodeToString([]byte(filepath.Join(cwd, "elsewhere")))
	case "malformed":
		receipt = "not-a-receipt###"
	}
	fmt.Fprintf(os.Stderr, "SANDBOX OK backend=fake-wall abi=- read=%d write=%d net=nopromise cwd=%s cwdb64=%s cmd=%s\n",
		count(args[:i], "--read"), count(args[:i], "--write"), oneline.Field(cwd), receipt, oneline.Field(args[i+1]))
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
