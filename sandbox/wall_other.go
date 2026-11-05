//go:build !linux && !darwin

package main

import "fmt"

const backendName = "none"

func wallCheck() (string, bool, string, error) {
	return "-", false, "", fmt.Errorf("no wall on this platform: macOS sandbox-exec and Linux Landlock only")
}

func execWall(*policy, []string) *refusal {
	r := refuse("no_sandbox", "no wall on this platform; this tool does not run a command it cannot contain")
	return &r
}
