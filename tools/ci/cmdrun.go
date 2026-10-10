package ci

import (
	"os"
	"os/exec"
	"path/filepath"
)

// longRunning returns true if prog is a known long-running program.
func longRunning(prog string, args ...string) bool {
	base := filepath.Base(prog)
	// Check args first
	if len(args) > 0 {
		switch args[0] {
		case "test", "build", "run", "install", "vet":
			return true
		}
	}
	// Check base name
	switch base {
	case "make", "apt-get", "brew", "sbcl", "nova-secrets", "sudo":
		return true
	}
	// Check absolute path with known base
	if filepath.IsAbs(prog) {
		switch filepath.Base(prog) {
		case "make":
			return true
		}
	}
	return false
}

// CmdRunner is an interface for running commands.
type CmdRunner interface {
	Run(args ...string) (int, error)
}

// osCmdRunner runs commands on the local OS.
type osCmdRunner struct{}

// Run executes a command and returns its exit code.
func (o *osCmdRunner) Run(args ...string) (int, error) {
	if len(args) == 0 {
		return -1, os.ErrNotExist
	}
	prog := args[0]
	if prog == "" {
		return -1, os.ErrNotExist
	}
	if longRunning(prog, args[1:]...) {
		return runLongRunning(prog, args[1:])
	}
	return runNormal(prog, args[1:])
}

func runLongRunning(prog string, args []string) (int, error) {
	cmd := exec.Command(prog, args...)
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

func runNormal(prog string, args []string) (int, error) {
	cmd := exec.Command(prog, args...)
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

// LookPath finds the executable in PATH.
func LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

// PrependPath prepends dir to PATH.
func PrependPath(dir string) {
	old := os.Getenv("PATH")
	if old != "" {
		os.Setenv("PATH", dir+string(os.PathListSeparator)+old)
	} else {
		os.Setenv("PATH", dir)
	}
}
