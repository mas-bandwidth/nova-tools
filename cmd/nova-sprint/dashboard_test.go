package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprintdash"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dashboard reads the sprint in this process exactly as where --json prints it, and
// serves that object as /api/sprint's data.
func TestDashboardReadsTheSprintAsWhereJSONDoes(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1,m2")
	ta.ok("add --stream s1 --count 3")
	want := ta.ok("where --json")
	got, err := ta.a.whereJSON("", false)
	require.NoError(t, err)
	assert.JSONEq(t, want, string(got))

	srv := &sprintdash.Server{Read: func() ([]byte, error) { return ta.a.whereJSON("", false) }, Now: ta.a.now, Every: time.Second}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sprint", nil))
	var v struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	assert.True(t, v.OK)
	assert.JSONEq(t, want, string(v.Data))
}

// A read where refuses is the dashboard's failed read, with where's own line.
func TestDashboardReadFailureIsWheresRefusal(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	_, err := ta.a.whereJSON("", false) // no init: no sprint here yet
	require.Error(t, err)
	assert.Contains(t, err.Error(), "where exited 1: nova-sprint where: this store: no sprint here yet")
}

// The page listens on loopback and the fleet's private network only, each address once.
func TestDashboardListensOnPrivateAddressesOnly(t *testing.T) {
	t.Parallel()
	for list, want := range map[string][]string{
		"127.0.0.1:7390":                {"127.0.0.1:7390"},
		"localhost:7390,localhost:7390": {"localhost:7390"},
		"[::1]:7390,,127.0.0.1:7390,":   {"[::1]:7390", "127.0.0.1:7390"},
	} {
		got, err := dashboardAddrs(list)
		require.NoError(t, err, list)
		assert.Equal(t, want, got, list)
	}
	for list, why := range map[string]string{
		"bench-a:7390": "never a name",
		"127.0.0.1":    "wants address:port",
		"":             "names no address",
	} {
		_, err := dashboardAddrs(list)
		if assert.Error(t, err, list) {
			assert.Contains(t, err.Error(), why, list)
		}
	}
	// the ranges, as addresses: none of them is dialled
	for _, ip := range []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback, net.IPv4(100, 64, 1, 2), net.IPv4(100, 127, 255, 9),
		net.IPv4(10, 0, 0, 2), net.IPv4(192, 168, 1, 2), net.IPv4(172, 16, 0, 2), {0xfd, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}} {
		assert.Empty(t, listenable(ip), "%s", ip)
	}
	for _, tc := range []struct {
		ip  net.IP
		why string
	}{
		{net.IPv4zero, "every network"},
		{net.IPv6unspecified, "every network"},
		{net.IPv4(100, 128, 0, 1), "a public address"},
		{net.IPv4(8, 8, 8, 8), "a public address"},
		{net.IPv4(203, 0, 113, 7), "a public address"},
		{net.IPv4(169, 254, 1, 1), "a link-local address"},
		{net.IP{0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, "a link-local address"},
	} {
		assert.Contains(t, listenable(tc.ip), tc.why, "%s", tc.ip)
	}
}

// Every refusal comes before any listener: exit 2, one line, nothing served.
func TestDashboardRefusesBadUse(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	for line, why := range map[string]string{
		"dashboard now":                       "takes no words",
		"dashboard --every 0s":                "--every wants a duration above 0",
		"dashboard --listen 0.0.0.0:7390":     "does not listen on every network",
		"dashboard --listen 1.1.1.1:7390":     "a public address",
		"dashboard --logo " + t.TempDir():     "is not a file",
		"dashboard --logo /no/such/logo.webp": "is not a file",
	} {
		code, out, errs := ta.do(line)
		assert.Equal(t, 2, code, "%s: %s%s", line, out, errs)
		assert.Contains(t, errs, why, line)
		assert.Contains(t, errs, "nova-sprint dashboard REFUSED", line)
		assert.Equal(t, 1, strings.Count(errs, "\n"), line)
		assert.Empty(t, out, line)
	}
}

// The sprint's server never runs the dashboard: it is run where it is typed and reads
// through the server.
func TestDashboardIsNotServed(t *testing.T) {
	t.Parallel()
	assert.Contains(t, readVerb([]string{"dashboard"}).unserved(), "dashboard is not run by the server")
	assert.Equal(t, classRead, verbClasses["dashboard"])
}
