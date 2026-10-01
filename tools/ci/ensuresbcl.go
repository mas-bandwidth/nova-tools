package main

import (
	"fmt"
	"path/filepath"
)

func init() {
	register(verb{
		name:    "ensure-sbcl",
		summary: "put sbcl on PATH for the lisp job, installing it when absent",
		help: `usage: go run ./tools/ci ensure-sbcl

Finds sbcl for the steps after this one. A runner user keeps sbcl in its own
$HOME/.local/bin, which a listener started before it existed does not have on
PATH, so that directory is put on PATH and appended to the file GITHUB_PATH
names when it holds an executable sbcl. When no sbcl is on PATH after that, it
is installed with apt-get (the self-hosted runners need no sudo). The version
is printed.

exit 0  sbcl is on PATH
exit 1  it could not be installed
`,
		do: func(e env, args []string) int {
			if len(args) != 0 {
				fmt.Fprintln(e.stderr, "ensure-sbcl: takes no arguments")
				return 2
			}
			return ensureSbcl(osInstallHost(e))
		},
	})
}

// ensureSbcl is the verb over a host: see the help.
func ensureSbcl(h installHost) int {
	if home := h.getenv("HOME"); home != "" {
		dir := filepath.Join(home, ".local", "bin")
		if h.isExec(filepath.Join(dir, "sbcl")) {
			h.publish(dir)
			h.run.PrependPath(dir)
		}
	}
	if !lookOK(h.run, "sbcl") {
		if code := h.runInstallStep(nil, "apt-get", "update", "-qq"); code != 0 {
			return code
		}
		if code := h.runInstallStep(nil, "apt-get", "install", "-y", "--no-install-recommends", "sbcl"); code != 0 {
			return code
		}
	}
	return h.runInstallStep(nil, "sbcl", "--version")
}
