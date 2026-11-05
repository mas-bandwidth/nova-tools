package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func darwinPolicy() *policy {
	return &policy{
		reads: []string{"/r/one"}, readsNoExec: []string{"/n/cache"}, writes: []string{"/w/job", "/w/data"},
		optRoots: []string{"/opt/homebrew"}, cwd: "/w/job", tmp: "/w/job/.nova-sandbox-tmp", home: "/w/data",
	}
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
	text, params, err := darwinProfile(darwinPolicy())
	require.NoError(t, err)
	got := lines(text)
	assert.Contains(t, got, `(allow file-read* (subpath (param "READ0")))`)
	assert.Contains(t, got, `(allow file-read* file-write* (subpath (param "WRITE1")))`)
	assert.Contains(t, got, `(allow network-outbound (subpath (param "WRITE0")))`, "a socket under the write set is the job's own")
	assert.Contains(t, got, `(allow file-read* (subpath "/opt/homebrew"))`)
	assert.Contains(t, got, `(allow file-read-metadata (literal "/w"))`)
	assert.Contains(t, got, `(allow network-outbound (remote ip) (literal "/private/var/run/mDNSResponder"))`)
	assert.NotContains(t, got, "(allow network*)", "network* would grant every unix socket on the machine")
	assert.ElementsMatch(t, []string{"READ0=/r/one", "NOEXEC0=/n/cache", "WRITE0=/w/job", "WRITE1=/w/data", "HOME=/w/data"}, params)
	for _, l := range got {
		assert.NotContains(t, l, "@@", "every marker is filled")
	}
	assert.NotContains(t, text, "AGXDeviceUserClient", "the GPU is closed by default")
}

func TestDarwinNoExecDenyComesAfterTheGlobalExecGrant(t *testing.T) {
	t.Parallel()
	text, _, err := darwinProfile(darwinPolicy())
	require.NoError(t, err)
	// SBPL's last matching rule wins.
	assert.Greater(t, strings.Index(text, `(deny process-exec* (subpath (param "NOEXEC0"))`), strings.Index(text, "(allow process-exec* process-fork)"))
}

func TestDarwinNetDeny(t *testing.T) {
	t.Parallel()
	p := darwinPolicy()
	p.netDeny = true
	text, _, err := darwinProfile(p)
	require.NoError(t, err)
	assert.NotContains(t, strings.Join(lines(text), "\n"), "mDNSResponder", "the resolver goes with the network")
	assert.NotContains(t, lines(text), "(allow network-outbound (remote ip))")
	p.netAllow = []string{"127.0.0.1:11434"}
	text, _, err = darwinProfile(p)
	require.NoError(t, err)
	assert.Contains(t, lines(text), `(allow network-outbound (remote ip "localhost:11434"))`)
}

func TestDarwinGPUAndLinks(t *testing.T) {
	t.Parallel()
	p := darwinPolicy()
	p.gpu = true
	p.linkSpellings = []string{"/w/alias"}
	text, _, err := darwinProfile(p)
	require.NoError(t, err)
	assert.Contains(t, text, `"AGXDeviceUserClient"`)
	assert.Contains(t, lines(text), `(allow file-read* (literal "/w/alias"))`)
}

func TestTemplateMarkersAreWholeLines(t *testing.T) {
	t.Parallel()
	for _, m := range []string{"@@OPTROOTS@@", "@@ANCESTORS@@", "@@READS@@", "@@READSNOEXEC@@", "@@WRITES@@", "@@NET@@"} {
		assert.Contains(t, strings.Split(darwinTemplate, "\n"), m)
	}
	assert.Contains(t, darwinTemplate, "(deny default (with no-report))")
}
