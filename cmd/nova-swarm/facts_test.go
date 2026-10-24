package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMachineFactsNameTheOSTheISATheSandboxTheJarAndTheToolchains: the facts a member's beat
// carries, in their fixed order, from a probe of the machine (no real machine is read).
func TestMachineFactsNameTheOSTheISATheSandboxTheJarAndTheToolchains(t *testing.T) {
	t.Parallel()
	linux := machineProbe{goos: "linux", goarch: "amd64", home: "/home/b", sandbox: func() bool { return true }, kvm: func() bool { return true },
		exists: func(p string) bool { return p == TLAJar || p == "/home/b/.local/bin/tlacheck" }, onPath: func(n string) bool { return n == "go" || n == "java" }}
	assert.Equal(t, []string{"linux", "amd64", "landlock", "kvm", "tla", "go", "java"}, machineFacts(linux))
	noKVM := linux
	noKVM.kvm = func() bool { return false }
	assert.Equal(t, []string{"linux", "amd64", "landlock", "tla", "go", "java"}, machineFacts(noKVM), "no KVM, no kvm fact")
	onPath := noKVM
	onPath.exists = func(p string) bool { return p == TLAJar }
	onPath.onPath = func(n string) bool { return n == "go" || n == "java" || n == TLACheck }
	assert.Equal(t, []string{"linux", "amd64", "landlock", "tla", "go", "java"}, machineFacts(onPath), "a runner on PATH is a runner")
	jarOnly := noKVM
	jarOnly.exists = func(p string) bool { return p == TLAJar }
	assert.Equal(t, []string{"linux", "amd64", "landlock", "go", "java"}, machineFacts(jarOnly),
		"the jar without tlacheck is no TLA bench: the 2026-10-24 fleet, whose TLA gates failed on their cards")
	mac := machineProbe{goos: "darwin", goarch: "arm64", sandbox: func() bool { return true }, kvm: func() bool { return true },
		exists: func(string) bool { return false }, onPath: func(n string) bool { return n == "go" }}
	assert.Equal(t, []string{"darwin", "arm64", "go"}, machineFacts(mac), "landlock and kvm are a linux kernel's alone")
}

// TestKVMAnswersOnlyWhenTheDeviceOpensAndSaysTwelve: the "kvm" fact is the kvm play's check, on a
// fake opener (no real /dev/kvm is opened): the device opened read-write and KVM_GET_API_VERSION
// answered 12. A node whose open fails (WSL2 with nested virtualisation off: ENODEV) and a device
// that answers another version carry no fact.
func TestKVMAnswersOnlyWhenTheDeviceOpensAndSaysTwelve(t *testing.T) {
	t.Parallel()
	var asked []string
	opener := func(v int, err error) func(string) (int, error) {
		return func(p string) (int, error) { asked = append(asked, p); return v, err }
	}
	assert.True(t, kvmAnswers(opener(12, nil)))
	assert.False(t, kvmAnswers(opener(0, errors.New("no such device"))), "a node with no hypervisor behind it")
	assert.False(t, kvmAnswers(opener(0, errors.New("permission denied"))), "a member user outside the kvm group")
	assert.False(t, kvmAnswers(opener(11, nil)), "an API version other than 12")
	assert.Equal(t, []string{KVMDevice, KVMDevice, KVMDevice, KVMDevice}, asked)
}
