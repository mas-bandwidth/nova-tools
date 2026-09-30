// Command benchstandard is the acceptance WITNESS for a Linux bench: it checks
// the bench it runs on against the standard and prints one DRIFT line per
// finding.
//
// It is a witness and never a provisioner. It installs no package, creates no
// user, writes no unit file, arms no timer, mounts nothing and authorizes no
// seat key; all of that is the fleet declaration's job, and a DRIFT line is the
// witness that the declaration and the host disagree, not a ticket for this tool
// to repair. Its one mutation is --apply, which sends SIGTERM to stray runner
// listeners: a process holding the runner's listener that is not under the
// runner's systemd unit. No other action is taken on any path, with or without
// --apply.
//
// The checks, in the order they print: each self-hosted runner has one listener
// under its unit and a unit file with the standard stanzas (Linux only); the
// toolchain roots the sandbox wall grants exist; go is the version go.mod names;
// go and sbcl can be EXECUTED inside the wall, not merely found on PATH; sbcl is
// the pinned version under $HOME/sdk; the pro rung and sqlite3 under $HOME/sdk
// are present; NOVA_SLOT_SHARE is declared; the agent harness exists and starts
// inside the wall, and the network is reachable from inside it (Linux only);
// each nova binary reports the wanted version; exactly one seat key exists per
// owner and the secrets tool accepts it; no plaintext provider key is on the
// bench; the disk has headroom.
//
// Exit 0 prints "STANDARD OK ..."; exit 1 prints "STANDARD DRIFT (see lines
// above)" after the DRIFT lines.
//
// The witness runs ON the bench, so it must not need the toolchain it judges: a
// bench whose go is the thing in doubt cannot `go run` it. Build it as a static
// binary on another machine, copy it over, and run it there:
//
//	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/benchstandard ./tools/benchstandard
//	scp bin/benchstandard bench:
//	ssh bench NOVA_WANT=v1.2.3 NOVA_GO=go1.26.5 NOVA_SLOT_SHARE=64 ./benchstandard
//
// On a bench whose go is good, `go run ./tools/benchstandard` from a checkout
// does the same, and reads the wanted go from that checkout's go.mod.
//
// Environment:
//
//	NOVA_GO            the wanted go, such as go1.26.5; default the `go` line of go.mod
//	                   in the directory above the binary, or above the working directory
//	NOVA_WANT          the wanted nova version each binary reports
//	NOVA_SBCL          the pinned sbcl version (default 2.5.8)
//	NOVA_SLOT_SHARE    the bench's declared slot share, a positive whole number
//	NOVA_PRO_RUNG      an executable that is the pro rung
//	NOVA_HARNESS       the harness binary, instead of $HOME/nova-bench/harness-*/opencode
//	NOVA_PROBE_URL     the URL the sandbox's network probe fetches
//	NOVA_SECRETS_STORE the seat store for the seat check
//	NOVA_MIN_FREE_G    the disk floor in gigabytes (default 25)
//	NOVA_RESOLV_CONF   the resolver file the wall's resolver directory is read from
//
// The card's environment is built before any tool is resolved: if
// $HOME/sdk/env.sh exists it is sourced, so the verdict does not depend on the
// caller's PATH.
//
//	example:
//	  NOVA_WANT=v1.2.3 NOVA_SLOT_SHARE=64 benchstandard
//	  DRIFT sbcl version [SBCL 2.6.0] want 2.5.8 (NOVA_SBCL)
//	  STANDARD DRIFT (see lines above)
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const tool = "benchstandard"

const usage = `usage: benchstandard [--apply]
  --apply   kills stray runner listeners (the tool's only mutation); nothing more
`

// env is everything a run reads from outside itself.
type env struct {
	stdout io.Writer
	// stderr is where the sdk env file's errors and output are shown, as a shell
	// sourcing it would show them.
	stderr io.Writer
	h      host
	exeDir string // the directory of the running binary
	cwd    string // the working directory
	// systemDir is where the system-wide systemd units are: /etc/systemd/system on a
	// bench, a directory of the test's own in a test, so no test reads the host's.
	systemDir string
}

func main() {
	exe, _ := os.Executable()
	cwd, _ := os.Getwd()
	os.Exit(run(os.Args[1:], env{stdout: os.Stdout, stderr: os.Stderr, h: osHost{}, exeDir: filepath.Dir(exe), cwd: cwd, systemDir: "/etc/systemd/system"}))
}

// run is the whole tool. An unknown argument is refused as drift, loudly: a
// witness that quietly accepts `--apply --reboot-the-host` is a witness that lies.
func run(args []string, e env) int {
	apply := false
	for _, arg := range args {
		switch arg {
		case "--apply":
			apply = true
		case "-h", "--help":
			fmt.Fprint(e.stdout, usage)
			return 0
		default:
			fmt.Fprintf(e.stdout, "DRIFT unknown argument %s\n", arg)
			fmt.Fprintln(e.stdout, "STANDARD DRIFT (see lines above)")
			return 1
		}
	}

	process := parseEnvList(e.h.Environ())
	w := &witness{
		h:         e.h,
		out:       e.stdout,
		home:      process["HOME"],
		apply:     apply,
		goWant:    process["NOVA_GO"],
		want:      process["NOVA_WANT"],
		harness:   process["NOVA_HARNESS"],
		systemDir: e.systemDir,
	}
	if w.goWant == "" {
		w.goWant = wantedGo(e)
	}

	// The card's environment, before any tool is resolved.
	w.env = process
	if sdkEnv := filepath.Join(w.home, "sdk", "env.sh"); w.home != "" && w.isRegular(sdkEnv) {
		set, said, err := e.h.SourceEnv(sdkEnv, e.h.Environ())
		if said != "" && e.stderr != nil {
			fmt.Fprint(e.stderr, said)
		}
		if err != nil && e.stderr != nil {
			fmt.Fprintf(e.stderr, "benchstandard: sourcing %s: %v\n", sdkEnv, err)
		}
		if err == nil {
			merged := map[string]string{}
			for k, v := range process {
				merged[k] = v
			}
			for k, v := range set {
				merged[k] = v
			}
			w.env = merged
		}
	}
	return w.standard()
}

func parseEnvList(environ []string) map[string]string { return parseEnv(environ) }

// wantedGo is the tree's go.mod `go` line as a go version name, or "" when no
// go.mod is found: the one beside the directory the binary is in, then the
// nearest one at or above the working directory. The wanted go is a fact about
// the tree, never a patch version copied into this tool.
func wantedGo(e env) string {
	var candidates []string
	if e.exeDir != "" {
		candidates = append(candidates, filepath.Join(e.exeDir, "..", "go.mod"))
	}
	for dir := e.cwd; dir != "" && dir != "."; {
		candidates = append(candidates, filepath.Join(dir, "go.mod"))
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	for _, c := range candidates {
		raw, err := e.h.ReadFile(c)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if f := strings.Fields(line); len(f) >= 2 && f[0] == "go" {
				return "go" + f[1]
			}
		}
	}
	return ""
}
