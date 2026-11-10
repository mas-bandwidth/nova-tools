package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func darwinWall() *wall {
	return &wall{reads: []string{"/r/one"}, writes: []string{"/w/job", "/w/data"}, optRoots: []string{"/Applications/Xcode.app/Contents"}}
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" && !strings.HasPrefix(l, ";;") {
			out = append(out, l)
		}
	}
	return out
}

func TestDarwinProfileGrants(t *testing.T) {
	t.Parallel()
	text, params, err := darwinProfile(darwinWall())
	require.NoError(t, err)
	got := lines(text)
	assert.Contains(t, got, `(allow file-read* (subpath (param "READ0")))`)
	assert.Contains(t, got, `(allow file-read* file-write* (subpath (param "WRITE1")))`)
	assert.Contains(t, got, `(allow network-outbound (subpath (param "WRITE0")))`, "a socket inside a --write is reachable")
	assert.Contains(t, got, `(allow file-read* (subpath "/Applications/Xcode.app/Contents"))`)
	assert.Contains(t, got, `(allow file-read-metadata (literal "/w"))`)
	assert.Contains(t, got, `(allow file-read-metadata (literal "/Applications"))`)
	assert.Contains(t, got, `(allow network-outbound (remote ip) (literal "/private/var/run/mDNSResponder"))`)
	assert.NotContains(t, got, "(allow network*)", "network* would grant every unix socket on the machine")
	assert.Equal(t, []string{"READ0=/r/one", "WRITE0=/w/job", "WRITE1=/w/data"}, params)
	for _, l := range got {
		assert.NotContains(t, l, "@@", "every marker is filled")
		assert.NotContains(t, l, "/w/job", "a caller path that is not an ancestor enters only as a param")
	}
	assert.NotContains(t, text, "AGXDeviceUserClient", "the GPU is closed by default")
}

func TestDarwinNetDeny(t *testing.T) {
	t.Parallel()
	w := darwinWall()
	w.netDeny = true
	text, _, err := darwinProfile(w)
	require.NoError(t, err)
	assert.NotContains(t, text, "mDNSResponder", "the resolver goes with the network")
	assert.NotContains(t, text, "(remote ip)")
	w.netPorts = []uint16{11434}
	text, _, err = darwinProfile(w)
	require.NoError(t, err)
	assert.Contains(t, lines(text), `(allow network-outbound (remote ip "localhost:11434"))`)
}

func TestDarwinGPUAndLinks(t *testing.T) {
	t.Parallel()
	w := darwinWall()
	w.gpu = true
	w.linkSpellings = []string{"/w/alias"}
	text, _, err := darwinProfile(w)
	require.NoError(t, err)
	assert.Contains(t, text, `(iokit-user-client-class "AGXDeviceUserClient" "IOSurfaceRootUserClient")`)
	assert.Contains(t, text, `"com.apple.MTLCompilerService"`)
	assert.Contains(t, lines(text), `(allow file-read* (literal "/w/alias"))`)
}

// A literal carries the path's own UTF-8: the profile language has no \u escape,
// so a no-break space must reach it as the character, as it does in a -D param.
func TestDarwinLiteralsCarryThePathAsWritten(t *testing.T) {
	t.Parallel()
	w := &wall{reads: []string{"/r/nb sp/a"}, optRoots: []string{"/o/x y"}, linkSpellings: []string{"/l/nb link"}}
	text, params, err := darwinProfile(w)
	require.NoError(t, err)
	got := lines(text)
	assert.Contains(t, got, "(allow file-read-metadata (literal \"/r/nb sp\"))")
	assert.Contains(t, got, "(allow file-read* (subpath \"/o/x y\"))")
	assert.Contains(t, got, "(allow file-read* (literal \"/l/nb link\"))")
	assert.NotContains(t, text, `\u`, "no Go escape reaches the profile")
	assert.Equal(t, []string{"READ0=/r/nb sp/a"}, params)
}

func TestDarwinRootsMatchTheTemplate(t *testing.T) {
	t.Parallel()
	for _, r := range darwinRoots {
		assert.Contains(t, darwinTemplate, `(allow file-read* (subpath "`+r+`"))`, "a command under a root needs no --read only if the template grants it")
	}
	for _, m := range []string{"@@OPTROOTS@@", "@@ANCESTORS@@", "@@READS@@", "@@WRITES@@", "@@NET@@"} {
		assert.Contains(t, strings.Split(darwinTemplate, "\n"), m)
	}
	assert.Contains(t, darwinTemplate, "(deny default (with no-report))")
}

func TestAncestors(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"/a", "/a/b"}, ancestors("/a/b/c", "/a/b/"))
	assert.Empty(t, ancestors("/a"), `"/" is granted elsewhere`)
}
