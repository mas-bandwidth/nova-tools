//go:build windows

package main

import "os"

// replace runs nova-worker and returns its exit code: windows has no exec that
// keeps the process image, so the streams are wired straight through instead.
// os.StartProcess is the standard library's own launcher, not the os/exec door
// the repository routes production children through.
func replace(bin string, argv []string, env []string) (int, error) {
	proc, err := os.StartProcess(bin, argv, &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		Env:   env,
	})
	if err != nil {
		return 0, err
	}
	state, err := proc.Wait()
	if err != nil {
		return 0, err
	}
	if code := state.ExitCode(); code >= 0 {
		return code, nil
	}
	return 1, nil
}
