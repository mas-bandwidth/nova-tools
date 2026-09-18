package main

// The install verb's flags (nova-tools #1142). The work itself is
// internal/pulse/install.go; the runner below is the one real transport, so a
// test drives a fake runner and no test reaches a machine.

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdInstall(args []string, stdout, stderr io.Writer) int {
	f := newFlags("install")
	sha := f.fs.String("sha", "", "")
	buildBench := f.fs.String("build-bench", "", "")
	benches := f.fs.String("benches", "", "")
	ssh := f.fs.String("ssh", "ssh", "")
	timeout := f.fs.Duration("timeout", 15*time.Minute, "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*sha, "sha", "the commit to build once and install everywhere, 7 to 40 hex")
	f.want(*buildBench, "build-bench", "the one bench that builds and caches the tools")
	f.want(*benches, "benches", "the benches to install on, comma separated")
	if *sha != "" && !pulse.ValidInstallSHA(*sha) {
		f.add(fmt.Sprintf("--sha wants 7 to 40 hex characters, got %q", *sha))
	}
	if *buildBench != "" && !pulse.ValidInstallBench(*buildBench) {
		f.add(fmt.Sprintf("--build-bench wants letters, digits, dot, underscore and dash, got %q", *buildBench))
	}
	list := fleetNames(*benches)
	if *benches != "" && len(list) == 0 {
		f.add("--benches names no bench; give the names comma separated")
	}
	for _, b := range list {
		if !pulse.ValidInstallBench(b) {
			f.add(fmt.Sprintf("--benches wants letters, digits, dot, underscore and dash, got %q", b))
		}
	}
	if *timeout <= 0 {
		f.add(fmt.Sprintf("--timeout wants a positive duration such as 15m, got %s", *timeout))
	}
	if f.refused(stderr) {
		return 2
	}

	home, _ := os.UserHomeDir()
	return pulse.Install(pulse.InstallInput{
		SHA:        *sha,
		BuildBench: *buildBench,
		Benches:    list,
		Home:       home,
		Timeout:    *timeout,
		Runner:     pulse.SSHInstallRunner{Program: *ssh, Timeout: *timeout},
		Stdout:     stdout,
		Stderr:     stderr,
	})
}
