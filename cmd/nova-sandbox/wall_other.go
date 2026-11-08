//go:build !linux && !darwin

package main

import "fmt"

const backendName = "none"

func wallCheck() (string, bool, string, error) {
	return "-", false, "", fmt.Errorf("no wall on this platform: macOS sandbox-exec and Linux Landlock only")
}

func execWall(*wall, []string) []refusal {
	return []refusal{refuse("no_sandbox", "no wall on this platform; this tool does not run a command it cannot contain")}
}

func describeWall(w *wall) (string, []refusal) { return "", execWall(w, nil) }
