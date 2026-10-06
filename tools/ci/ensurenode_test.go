package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nodeTestHost is the install host with a RUNNER_TEMP of its own and a fake
// node that answers --version wherever it is run from.
func nodeTestHost(t *testing.T) (testInstallHost, string) {
	t.Helper()
	h := newTestInstallHost(t)
	tmp := filepath.Join(t.TempDir(), "runner-temp")
	inner := h.installHost.getenv
	h.installHost.getenv = func(k string) string {
		if k == "RUNNER_TEMP" {
			return tmp
		}
		return inner(k)
	}
	h.runner.answer = func(c cmdSpec) (string, int, error) {
		if strings.HasSuffix(c.Name, "node") && len(c.Args) == 1 && c.Args[0] == "--version" {
			return nodeVersion + "\n", 0, nil
		}
		return "", 0, nil
	}
	return h, tmp
}

// TestEnsureNodeFindsOrInstallsNodeAndPublishesItsDirectory: a node on PATH is
// used where it is, one in the runner user's $HOME/sdk/bin is put on PATH, and
// a machine with neither gets the pinned release unpacked under $RUNNER_TEMP; in
// every case the directory is published to GITHUB_PATH and the one OK line is
// printed. With nowhere to install, the refusal names every place looked at.
// Nothing here downloads or runs a real node.
func TestEnsureNodeFindsOrInstallsNodeAndPublishesItsDirectory(t *testing.T) {
	t.Parallel()
	const goos, goarch = "linux", "amd64"

	t.Run("on PATH", func(t *testing.T) {
		t.Parallel()
		h, _ := nodeTestHost(t)
		h.runner.onPath["node"] = "/usr/bin/node"
		require.Equal(t, 0, ensureNode(h.installHost, goos, goarch), h.errb.String())
		assert.Equal(t, "CI ENSURE-NODE OK node=/usr/bin/node version="+nodeVersion+"\n", h.out.String())
		assert.Equal(t, "/usr/bin\n", h.published(t))
		assert.Empty(t, h.runner.prepends, "a node already on PATH is not prepended again")
		assert.Equal(t, []string{"/usr/bin/node --version"}, h.runner.lines(), "nothing is downloaded")
	})

	t.Run("in the user's sdk bin", func(t *testing.T) {
		t.Parallel()
		h, _ := nodeTestHost(t)
		bin := filepath.Join(h.home, "sdk", "bin", "node")
		h.installHost.isExec = func(p string) bool { return p == bin }
		require.Equal(t, 0, ensureNode(h.installHost, goos, goarch), h.errb.String())
		dir := filepath.Dir(bin)
		assert.Equal(t, "CI ENSURE-NODE OK node="+bin+" version="+nodeVersion+"\n", h.out.String())
		assert.Equal(t, dir+"\n", h.published(t))
		assert.Equal(t, []string{dir}, h.runner.prepends)
		assert.Equal(t, []string{bin + " --version"}, h.runner.lines(), "nothing is downloaded")
	})

	t.Run("installed under RUNNER_TEMP", func(t *testing.T) {
		t.Parallel()
		h, tmp := nodeTestHost(t)
		name, ok := nodeArtifact(goos, goarch)
		require.True(t, ok)
		bin := filepath.Join(tmp, name, "bin", "node")
		unpacked := false
		h.installHost.isExec = func(p string) bool { return unpacked && p == bin }
		inner := h.runner.answer
		h.runner.answer = func(c cmdSpec) (string, int, error) {
			if c.Name == "tar" {
				unpacked = true
			}
			return inner(c)
		}
		require.Equal(t, 0, ensureNode(h.installHost, goos, goarch), h.errb.String())
		tarball := filepath.Join(tmp, name+".tar.gz")
		assert.Equal(t, []string{
			"curl -fsSL https://nodejs.org/dist/" + nodeVersion + "/" + name + ".tar.gz -o " + tarball,
			"tar -xzf " + tarball + " -C " + tmp,
			bin + " --version",
		}, h.runner.lines())
		assert.Equal(t, "CI ENSURE-NODE OK node="+bin+" version="+nodeVersion+"\n", h.out.String())
		assert.Equal(t, filepath.Dir(bin)+"\n", h.published(t))
		assert.Equal(t, []string{filepath.Dir(bin)}, h.runner.prepends)
	})

	t.Run("nowhere to install", func(t *testing.T) {
		t.Parallel()
		h := newTestInstallHost(t) // no RUNNER_TEMP
		assert.Equal(t, 1, ensureNode(h.installHost, goos, goarch))
		assert.Equal(t, "ensure-node: no node: RUNNER_TEMP is not set, so there is nowhere to install node; looked at PATH, "+filepath.Join(h.home, "sdk", "bin", "node")+"\n", h.errb.String())
		assert.Empty(t, h.out.String())
		assert.Empty(t, h.published(t), "a refusal publishes nothing")
		assert.Empty(t, h.runner.lines(), "a refusal with nowhere to install downloads nothing")
	})

	t.Run("the download unpacks no node", func(t *testing.T) {
		t.Parallel()
		h, tmp := nodeTestHost(t)
		name, _ := nodeArtifact(goos, goarch)
		assert.Equal(t, 1, ensureNode(h.installHost, goos, goarch))
		assert.Contains(t, h.errb.String(), "the download unpacked no executable "+filepath.Join(tmp, name, "bin", "node"))
		assert.Contains(t, h.errb.String(), "looked at PATH, "+filepath.Join(h.home, "sdk", "bin", "node"))
		assert.Empty(t, h.published(t))
	})

	t.Run("a platform nodejs.org does not ship", func(t *testing.T) {
		t.Parallel()
		h, _ := nodeTestHost(t)
		assert.Equal(t, 1, ensureNode(h.installHost, "windows", "amd64"))
		assert.Contains(t, h.errb.String(), "nodejs.org ships no tarball for windows/amd64")
		assert.Empty(t, h.runner.lines())
	})
}

// The pinned release is a node version string, and the artifact names are the
// ones nodejs.org publishes.
func TestEnsureNodePinAndArtifactNames(t *testing.T) {
	t.Parallel()
	parts := strings.Split(strings.TrimPrefix(nodeVersion, "v"), ".")
	assert.True(t, strings.HasPrefix(nodeVersion, "v") && len(parts) == 3, "nodeVersion %q is not vMAJOR.MINOR.PATCH", nodeVersion)
	for _, tc := range []struct{ goos, goarch, want string }{
		{"linux", "amd64", "node-" + nodeVersion + "-linux-x64"},
		{"linux", "arm64", "node-" + nodeVersion + "-linux-arm64"},
		{"darwin", "arm64", "node-" + nodeVersion + "-darwin-arm64"},
		{"darwin", "amd64", "node-" + nodeVersion + "-darwin-x64"},
	} {
		got, ok := nodeArtifact(tc.goos, tc.goarch)
		assert.True(t, ok && got == tc.want, "%s/%s: %q %v, want %q", tc.goos, tc.goarch, got, ok, tc.want)
	}
	_, ok := nodeArtifact("linux", "386")
	assert.False(t, ok)
}
