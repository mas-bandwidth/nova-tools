package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprintdash"
)

// DashboardListen is the dashboard's address when --listen names none: this machine only.
const DashboardListen = "127.0.0.1:7390"

// tailnetRange is the addresses a tailnet hands out (100.64.0.0/10, the shared range):
// with the private and loopback ranges, the only ones the dashboard listens on.
var tailnetRange = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// cmdDashboard serves the sprint dashboard (docs/SPEC-SPRINT-DASHBOARD.md) until it is
// interrupted: the page from the files embedded in the binary, and /api/sprint, the
// sprint as where --json prints it, read in this process at most once per --every and
// only while a page asks.
func (a *app) cmdDashboard(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("dashboard")
	listen := fs.String("listen", DashboardListen, "serve the page on each `address:port` of a comma-separated list, one listener each and one cached copy of the sprint: loopback, or this machine's address on the fleet's private network (the tailnet); 0.0.0.0, :: and public addresses are refused")
	logo := fs.String("logo", "", "an image `file` served as the page's logo and favicon; none: the logo slot renders nothing")
	every := fs.Duration("every", time.Second, "read the sprint at most once per this `duration`, above 0")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "dashboard", argErr("takes no words ", err, pos...))
	}
	if *every <= 0 {
		return refuse(stderr, "dashboard", "--every wants a duration above 0, got "+every.String())
	}
	addrs, err := dashboardAddrs(*listen)
	if err != nil {
		return refuse(stderr, "dashboard", err.Error())
	}
	if *logo != "" {
		if fi, err := os.Stat(*logo); err != nil || fi.IsDir() {
			return refuse(stderr, "dashboard", "--logo "+*logo+" is not a file this process can read")
		}
	}
	redis := (verbArgs{fs: fs}).given("redis")
	stdout = &lockedWriter{w: stdout} // the listeners and the reads write lines from their own goroutines
	srv := &sprintdash.Server{
		Read:    func(context.Context) ([]byte, error) { return a.whereJSON(c.redis, redis) },
		Now:     a.now,
		Every:   *every,
		Logo:    *logo,
		Version: version,
		Log:     stdout,
	}
	ctx, stop := a.notify(context.Background())
	defer stop()
	servers, err := a.serveDashboard(addrs, srv, stdout)
	if err != nil {
		return refuse(stderr, "dashboard", err.Error())
	}
	defer func() {
		for _, s := range servers {
			// ignored: the dashboard is ending; a page asking now asks again
			_ = s.Close()
		}
	}()
	began := a.binaryStamp()
	for a.pause(ctx, *every) {
		if now := a.binaryStamp(); began != "" && now != began {
			fmt.Fprintln(stdout, "DASHBOARD STOP the binary this dashboard runs was replaced; its supervisor starts the new one")
			return exitReplaced
		}
	}
	fmt.Fprintln(stdout, "DASHBOARD STOP interrupted")
	return 0
}

// whereJSON reads the sprint as `nova-sprint where --json` does, in this process: through
// the sprint's server when NOVA_SPRINT_SERVER names one, else on the store; --redis is
// handed on when the dashboard was given it, as where would have been.
func (a *app) whereJSON(addr string, given bool) ([]byte, error) {
	argv := []string{"where", "--json"}
	if given {
		argv = append(argv, "--redis", addr)
	}
	var out, errb bytes.Buffer
	if code := a.run(argv, &out, &errb); code != 0 {
		why := fmt.Sprintf("where exited %d", code)
		if line, _, _ := strings.Cut(strings.TrimSpace(errb.String()), "\n"); line != "" {
			why += ": " + line
		}
		return nil, errors.New(why)
	}
	return out.Bytes(), nil
}

// dashboardAddrs is the --listen list, each a host:port whose host is an IP address of
// loopback, a private range or the tailnet's (or localhost), each once.
func dashboardAddrs(list string) ([]string, error) {
	var out []string
	for _, addr := range strings.Split(list, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("--listen wants address:port, found %s", addr)
		}
		if host != "localhost" {
			if why := listenable(net.ParseIP(host)); why != "" {
				return nil, fmt.Errorf("--listen %s: %s", addr, why)
			}
		}
		if !slices.Contains(out, addr) {
			out = append(out, addr)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("--listen names no address:port")
	}
	return out, nil
}

// listenable is why the dashboard does not listen on ip, "" when it does: loopback, a
// private range or the tailnet's, never every network and never a public address.
func listenable(ip net.IP) string {
	switch {
	case ip == nil:
		return "the address is an IP address of this machine (loopback, or its address on the fleet's private network), never a name"
	case ip.IsUnspecified():
		return "the page shows the sprint and checks no credential, so it does not listen on every network; name loopback or this machine's tailnet address"
	case !ip.IsLoopback() && !ip.IsPrivate() && !tailnetRange.Contains(ip):
		return "a public address; the page shows the sprint and checks no credential, so it listens on loopback or the fleet's private network (the tailnet) only"
	}
	return ""
}

// serveDashboard starts one listener per address, every one the same server, and says
// each address it listens on; a listener that cannot start closes the others.
func (a *app) serveDashboard(addrs []string, h http.Handler, stdout io.Writer) ([]*http.Server, error) {
	var lns []net.Listener
	for _, addr := range addrs {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			for _, l := range lns {
				// ignored: closing the listeners already open on the failure path; err is the one returned
				_ = l.Close()
			}
			return nil, fmt.Errorf("--listen %s: %w", addr, err)
		}
		lns = append(lns, ln)
	}
	var servers []*http.Server
	for _, ln := range lns {
		s := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, WriteTimeout: 2 * time.Minute}
		servers = append(servers, s)
		at := ln.Addr().String()
		go func() {
			if err := s.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintf(stdout, "DASHBOARD STOPPED %s: %s\n", at, oneline.Escape(err.Error()))
			}
		}()
		fmt.Fprintf(stdout, "DASHBOARD listening on http://%s/\n", at)
	}
	return servers, nil
}

// lockedWriter writes each line whole, one writer at a time.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
