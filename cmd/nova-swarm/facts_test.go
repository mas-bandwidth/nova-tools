package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMachineFactsNameTheOSTheISATheSandboxTheJarAndTheToolchains: the facts a member's beat
// carries, in their fixed order, from a probe of the machine (no real machine is read).
func TestMachineFactsNameTheOSTheISATheSandboxTheJarAndTheToolchains(t *testing.T) {
	t.Parallel()
	linux := machineProbe{goos: "linux", goarch: "amd64", sandbox: func() bool { return true },
		exists: func(p string) bool { return p == TLAJar }, onPath: func(n string) bool { return n == "go" || n == "java" }}
	assert.Equal(t, []string{"linux", "amd64", "landlock", "tla", "go", "java"}, machineFacts(linux))
	mac := machineProbe{goos: "darwin", goarch: "arm64", sandbox: func() bool { return true },
		exists: func(string) bool { return false }, onPath: func(n string) bool { return n == "go" }}
	assert.Equal(t, []string{"darwin", "arm64", "go"}, machineFacts(mac), "landlock is a linux kernel's alone")
}
