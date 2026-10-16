package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// lsmList is where a linux kernel lists the security modules it runs, landlock among them
// when the wall can use it (the same kernel the wall's landlock probe asks).
const lsmList = "/sys/kernel/security/lsm"

// TLAJar is where a bench keeps the pinned TLA+ tools (the provisioning standard; the
// model gate's TLC_JAR), the jar whose presence is the "tla" gate fact.
const TLAJar = "/opt/tla/tla2tools.jar"

// factTools are the toolchains a gate fact names by the program on the bench's PATH.
var factTools = []string{"go", "dotnet", "java"}

// machineProbe is what the facts are read from: the OS and ISA, whether the kernel runs
// landlock (lsmList), whether a file is, and whether a program is on PATH. Tests give their own.
type machineProbe struct {
	goos, goarch string
	sandbox      func() bool
	exists       func(path string) bool
	onPath       func(name string) bool
}

// localProbe reads this machine.
func localProbe() machineProbe {
	return machineProbe{
		goos: runtime.GOOS, goarch: runtime.GOARCH,
		sandbox: func() bool {
			b, err := os.ReadFile(lsmList)
			return err == nil && strings.Contains(","+strings.TrimSpace(string(b))+",", ",landlock,")
		},
		exists: func(p string) bool { _, err := os.Stat(p); return err == nil },
		onPath: func(n string) bool { _, err := exec.LookPath(n); return err == nil },
	}
}

// machineFacts are the member's gate facts (member.Config.Facts; nova-sprint FieldFacts,
// docs/SPEC-SPRINT.md, the gate-host rule), in a fixed order: the OS ("linux", "darwin"),
// the ISA ("amd64", "arm64"), "landlock" when a linux kernel has it, "tla" when the TLA
// jar is at TLAJar, then each toolchain of factTools on PATH. The member measures them
// itself, at start: the sprint server cannot see another machine.
func machineFacts(p machineProbe) []string {
	facts := []string{p.goos, p.goarch}
	if p.goos == "linux" && p.sandbox() {
		facts = append(facts, "landlock")
	}
	if p.exists(TLAJar) {
		facts = append(facts, "tla")
	}
	for _, t := range factTools {
		if p.onPath(t) {
			facts = append(facts, t)
		}
	}
	return facts
}
