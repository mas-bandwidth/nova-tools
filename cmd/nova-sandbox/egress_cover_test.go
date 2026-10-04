package main

import (
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file is the per-function cover for the egress verb's three machine seams in
// egress.go: the privileged command's Look, the bench resolver constructor and the
// resolver's LookupHost. It reaches nothing the production code does not: no packet,
// no child process, no store. Run is NOT covered here: it executes `sudo -n nft ...`
// through subproc.Command, which is a subprocess, and the card forbids one. It is
// named in the report as the function left uncovered.

// TestEgressCoverSudoLook pins the privileged-command seam's Look: the production body
// is exec.LookPath and nothing else, so the main path finds a program on PATH and the
// refusal is a name no PATH carries. Look starts no child and opens no socket.
func TestEgressCoverSudoLook(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		binary  string
		wantErr bool
	}{
		{name: "a program on PATH", binary: "sh"},
		{name: "a name no PATH carries", binary: "nova-egress-cover-no-such-binary", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sudoCommand{}.Look(c.binary)
			if c.wantErr {
				require.Error(t, err, "Look(%q) answered a path for a program that is not on this bench", c.binary)
				assert.Empty(t, got, "Look(%q) refused and still returned %q", c.binary, got)
				return
			}
			require.NoError(t, err, "Look(%q) could not find a program the unit tier runs with", c.binary)
			assert.True(t, filepath.IsAbs(got), "Look(%q) returned %q, which is not the absolute path a caller runs", c.binary, got)
			assert.Equal(t, c.binary, filepath.Base(got), "Look(%q) returned the path of another program: %q", c.binary, got)
		})
	}
}

// TestEgressCoverBenchResolver pins the resolver seam's constructor: it wraps the
// address it is handed and nothing else, so the resolver the plan is allowed to reach
// is the one the plan pinned.
func TestEgressCoverBenchResolver(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		at   netip.Addr
	}{
		{name: "the bench resolver's IPv4", at: netip.MustParseAddr("10.9.0.53")},
		{name: "the bench resolver's IPv6", at: netip.MustParseAddr("2001:db8::53")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := benchResolver(c.at)
			require.NotNil(t, got, "benchResolver(%s) returned no resolver, so a plan cannot pin its lookups", c.at)
			d, ok := got.(dnsResolver)
			require.True(t, ok, "benchResolver(%s) returned %T, not the dnsResolver whose address a plan pins", c.at, got)
			assert.Equal(t, c.at, d.at, "benchResolver(%s) wrapped %s, so a plan would pin the wrong resolver", c.at, d.at)
		})
	}
}

// TestEgressCoverLookupHost pins the resolver body: the main path resolves a name the
// host file answers (localhost, so no packet leaves the process) and the refusal is an
// empty name, which the standard resolver rejects before it dials. The two together
// reach LookupHost without a socket; an address that is not on the wire never dials.
func TestEgressCoverLookupHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{name: "localhost comes from the host file, not the wire", host: "localhost"},
		{name: "an empty name is refused before any dial", host: "", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := dnsResolver{at: netip.MustParseAddr("127.0.0.1")}
			got, err := d.LookupHost(c.host)
			if c.wantErr {
				require.Error(t, err, "LookupHost(%q) answered addresses for a name that is not one", c.host)
				assert.Empty(t, got, "LookupHost(%q) refused and still returned %v", c.host, got)
				return
			}
			require.NoError(t, err, "LookupHost(%q) could not read the host file every bench carries", c.host)
			assert.NotEmpty(t, got, "LookupHost(%q) resolved to no address", c.host)
		})
	}
}
