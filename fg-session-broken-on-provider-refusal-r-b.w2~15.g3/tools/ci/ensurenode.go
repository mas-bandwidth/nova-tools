package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// nodeVersion is the one node release the unit legs install when a runner has
// none: the release the benches carry under ~/sdk (v26.7.0 on the Linux bench,
// read 2026-10-06). Only the download is pinned; a node already on the machine
// is used as it is.
const nodeVersion = "v26.7.0"

func init() {
	register(verb{
		name:    "ensure-node",
		summary: "put node on PATH for the dashboard tests, installing the pinned release when absent",
		help: `usage: go run ./tools/ci ensure-node

Finds node for the steps after this one. internal/sprintdash's page tests render
the dashboard with node and, under NOVA_CI=1, fail rather than skip when it is
missing. A runner user keeps node in its own $HOME/sdk/bin, which a listener
started before it existed does not have on PATH, so that directory is looked at
after PATH (the functional image carries no node: infra/functional-image/
binaries.txt names no directory for it). When neither holds an executable node,
the pinned release (` + nodeVersion + `) is downloaded from nodejs.org and
unpacked under $RUNNER_TEMP. The directory holding node is put on PATH, appended
to the file GITHUB_PATH names, and one line is printed:

  CI ENSURE-NODE OK node=<path> version=<v>

Only a directory that holds no redis-server is ever published: the unit legs put
a refusing redis-server first on PATH, and GITHUB_PATH prepends, so publishing a
system bin directory here could put a real one in front of it.

exit 0  node is on PATH
exit 1  no node was found or installed; stderr names every place looked at
`,
		do: func(e env, args []string) int {
			if len(args) != 0 {
				fmt.Fprintln(e.stderr, "ensure-node: takes no arguments")
				return 2
			}
			return ensureNode(osInstallHost(e), runtime.GOOS, runtime.GOARCH)
		},
	})
}

// nodeArtifact is the name of the nodejs.org tarball's directory for a
// platform (node-<version>-<os>-<arch>), ok false where nodejs.org ships no
// tarball this verb can unpack.
func nodeArtifact(goos, goarch string) (string, bool) {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if arch == "" || (goos != "linux" && goos != "darwin") {
		return "", false
	}
	return "node-" + nodeVersion + "-" + goos + "-" + arch, true
}

// ensureNode is the verb over a host: see the help.
func ensureNode(h installHost, goos, goarch string) int {
	var looked []string
	if bin, err := h.run.LookPath("node"); err == nil {
		return nodeFound(h, bin, false)
	}
	looked = append(looked, "PATH")
	if home := h.getenv("HOME"); home != "" {
		bin := filepath.Join(home, "sdk", "bin", "node")
		if h.isExec(bin) {
			return nodeFound(h, bin, true)
		}
		looked = append(looked, bin)
	} else {
		looked = append(looked, "$HOME/sdk/bin/node (HOME is not set)")
	}
	tmp := h.getenv("RUNNER_TEMP")
	if tmp == "" {
		return nodeRefused(h, looked, "RUNNER_TEMP is not set, so there is nowhere to install node")
	}
	name, ok := nodeArtifact(goos, goarch)
	if !ok {
		return nodeRefused(h, looked, "nodejs.org ships no tarball for "+goos+"/"+goarch+" that this verb unpacks")
	}
	tarball := filepath.Join(tmp, name+".tar.gz")
	url := "https://nodejs.org/dist/" + nodeVersion + "/" + name + ".tar.gz"
	if code := h.runInstallStep(nil, "curl", "-fsSL", url, "-o", tarball); code != 0 {
		return nodeRefused(h, looked, fmt.Sprintf("curl %s exited %d", url, code))
	}
	if code := h.runInstallStep(nil, "tar", "-xzf", tarball, "-C", tmp); code != 0 {
		return nodeRefused(h, looked, fmt.Sprintf("tar -xzf %s exited %d", tarball, code))
	}
	bin := filepath.Join(tmp, name, "bin", "node")
	if !h.isExec(bin) {
		return nodeRefused(h, looked, "the download unpacked no executable "+bin)
	}
	return nodeFound(h, bin, true)
}

// nodeFound publishes the directory holding bin (and puts it on this process's
// PATH when bin was found off it), reads the version and prints the OK line.
func nodeFound(h installHost, bin string, offPath bool) int {
	dir := filepath.Dir(bin)
	if offPath {
		h.run.PrependPath(dir)
	}
	h.publish(dir)
	v, code, err := capture(h.run, cmdSpec{Name: bin, Args: []string{"--version"}, Stderr: h.stderr})
	if err != nil || code != 0 {
		fmt.Fprintf(h.stderr, "ensure-node: %s --version exited %d (%v)\n", bin, code, err)
		return 1
	}
	fmt.Fprintf(h.stdout, "CI ENSURE-NODE OK node=%s version=%s\n", bin, strings.TrimSpace(v))
	return 0
}

// nodeRefused is the exit 1: the reason, and every place looked at.
func nodeRefused(h installHost, looked []string, why string) int {
	fmt.Fprintf(h.stderr, "ensure-node: no node: %s; looked at %s\n", why, strings.Join(looked, ", "))
	return 1
}
