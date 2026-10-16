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

// KVMDevice is the device a linux kernel's KVM answers on, the "kvm" gate fact's (a VM tier boots its guests on it).
const KVMDevice = "/dev/kvm"

// KVMAPIVersion is what KVM_GET_API_VERSION answers on every Linux since 2.6.22: the "kvm" gate fact is a device that
// opens read-write and answers it with this, the check the fleet's kvm play makes (rowan-tools fleet/roles/kvm). A
// node alone is no hypervisor: a WSL2 bench with nested virtualisation off has /dev/kvm root:kvm 660 whose open fails
// ENODEV, and a VM there runs under QEMU's TCG at a tenth of the speed with no word said.
const KVMAPIVersion = 12

// kvmAnswers says the opener reached KVM at KVMDevice: it opened read-write and KVM_GET_API_VERSION answered
// KVMAPIVersion. open is the opener (kvmOpen on this machine; tests give their own).
func kvmAnswers(open func(path string) (apiVersion int, err error)) bool {
	v, err := open(KVMDevice)
	return err == nil && v == KVMAPIVersion
}

// factTools are the toolchains a gate fact names by the program on the bench's PATH.
var factTools = []string{"go", "dotnet", "java"}

// machineProbe is what the facts are read from: the OS and ISA, whether the kernel runs
// landlock (lsmList), whether KVM answers (kvmAnswers), whether a file is, and whether a program
// is on PATH. Tests give their own.
type machineProbe struct {
	goos, goarch string
	sandbox      func() bool
	kvm          func() bool
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
		kvm:    func() bool { return kvmAnswers(kvmOpen) },
		exists: func(p string) bool { _, err := os.Stat(p); return err == nil },
		onPath: func(n string) bool { _, err := exec.LookPath(n); return err == nil },
	}
}

// machineFacts are the member's gate facts (member.Config.Facts; nova-sprint FieldFacts,
// docs/SPEC-SPRINT.md, the gate-host rule), in a fixed order: the OS ("linux", "darwin"),
// the ISA ("amd64", "arm64"), "landlock" when a linux kernel has it, "kvm" when a linux kernel's
// KVM answers the member's user (kvmAnswers), "tla" when the TLA
// jar is at TLAJar, then each toolchain of factTools on PATH. The member measures them
// itself, at start: the sprint server cannot see another machine.
func machineFacts(p machineProbe) []string {
	facts := []string{p.goos, p.goarch}
	if p.goos == "linux" && p.sandbox() {
		facts = append(facts, "landlock")
	}
	if p.goos == "linux" && p.kvm != nil && p.kvm() {
		facts = append(facts, "kvm")
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
