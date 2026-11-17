package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type endpoint struct {
	network  string
	address  string
	password string
	local    bool
	dir      string
}

func resolveEndpoint(o options, w world) (endpoint, error) {
	addr := o.addr
	if addr == "" && !o.dirSet {
		addr = strings.TrimSpace(w.env("NOVA_RESERVE_ADDR"))
	}
	if addr != "" {
		if !strings.Contains(addr, ":") {
			addr += ":" + defaultReservePort
		}
		return endpoint{
			network:  "tcp",
			address:  addr,
			password: w.env("NOVA_RESERVE_PASSWORD"),
			local:    false,
		}, nil
	}
	absDir := o.dir
	if a, err := filepath.Abs(o.dir); err == nil {
		absDir = a
	}
	sock := resolveSocketPath(absDir, o.socket)
	return endpoint{
		network: "unix",
		address: sock,
		local:   true,
		dir:     absDir,
	}, nil
}

func dialEndpoint(ep endpoint, o options, w world, timeout time.Duration) (net.Conn, *bufio.Reader, error) {
	if w.dial != nil {
		c, err := w.dial(ep, o)
		if err != nil {
			return nil, nil, err
		}
		if ep.network == "tcp" {
			if err := sendAuth(c, ep.password, w); err != nil {
				// ignored: close failed auth connection
				_ = c.Close()
				return nil, nil, err
			}
		}
		return c, bufio.NewReader(c), nil
	}
	if ep.network == "tcp" {
		c, err := net.DialTimeout("tcp", ep.address, timeout)
		if err != nil {
			return nil, nil, err
		}
		if err := sendAuth(c, ep.password, w); err != nil {
			// ignored: close failed auth connection
			_ = c.Close()
			return nil, nil, err
		}
		return c, bufio.NewReader(c), nil
	}

	c, err := net.DialTimeout("unix", ep.address, min(timeout, 500*time.Millisecond))
	if err == nil {
		return c, bufio.NewReader(c), nil
	}
	if o.verb == "status" || o.verb == "release" || o.verb == "subscribe" {
		return nil, nil, err
	}
	if err := spawnLocalDaemon(ep, o); err != nil {
		return nil, nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		c, err = net.DialTimeout("unix", ep.address, 250*time.Millisecond)
		if err == nil {
			return c, bufio.NewReader(c), nil
		}
		if !time.Now().Before(deadline) {
			return nil, nil, fmt.Errorf("local nova-reserve daemon at %s did not answer: %w", ep.address, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func sendAuth(c net.Conn, password string, w world) error {
	clientName := cmp.Or(w.env("USER"), "peer") + "@" + shortHost()
	return writeClientFrame(c, ClientFrame{
		Op:       "auth",
		Version:  protocolVersion,
		Password: password,
		Client:   clientName,
	}, 3*time.Second)
}

func spawnLocalDaemon(ep endpoint, o options) error {
	lockFile := filepath.Join(ep.dir, "serve.lock")
	lf, err := openIn(lockFile, os.O_RDWR|os.O_CREATE)
	if err != nil {
		return err
	}
	defer func() {
		// ignored: closing startup lock descriptor releases flock
		_ = lf.Close()
	}()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	if c, derr := net.DialTimeout("unix", ep.address, 150*time.Millisecond); derr == nil {
		// ignored: daemon already up
		_ = c.Close()
		return nil
	}
	// ignored: remove stale socket file before spawning daemon
	_ = os.Remove(ep.address)

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() {
		// ignored: child inherited duplicated descriptor
		_ = devNull.Close()
	}()

	args := []string{"serve", "--local", "--dir", ep.dir, "--socket", ep.address, "--reconnect-grace", "0s"}
	if filepath.Clean(ep.dir) != systemDir {
		args = append(args, "--idle-exit", "10s")
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = "/"
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap detached daemon starter asynchronously so no zombie remains in long-lived callers.
	go func() {
		// ignored: background daemon lifecycle managed via socket
		_ = cmd.Wait()
	}()

	waitUntil := time.Now().Add(2 * time.Second)
	for time.Now().Before(waitUntil) {
		if c, derr := net.DialTimeout("unix", ep.address, 100*time.Millisecond); derr == nil {
			// ignored: readiness probe succeeded
			_ = c.Close()
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

func writeClientFrame(c net.Conn, f ClientFrame, timeout time.Duration) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if timeout > 0 {
		// ignored: best-effort write deadline
		_ = c.SetWriteDeadline(time.Now().Add(timeout))
	}
	_, err = c.Write(append(b, '\n'))
	return err
}

func readServerFrame(r *bufio.Reader, c net.Conn, deadline time.Time) (ServerFrame, error) {
	if !deadline.IsZero() {
		// ignored: read deadline bounds wait
		_ = c.SetReadDeadline(deadline)
	} else {
		// ignored: clear read deadline for blocking wait
		_ = c.SetReadDeadline(time.Time{})
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return ServerFrame{}, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var sf ServerFrame
		if err := json.Unmarshal([]byte(line), &sf); err != nil {
			return ServerFrame{}, err
		}
		return sf, nil
	}
}

type remoteHold struct {
	ep       endpoint
	w        world
	req      ClientFrame
	granted  ServerFrame
	failOpen bool

	mu        sync.Mutex
	writeMu   sync.Mutex
	conn      net.Conn
	paused    bool
	closed    bool
	stopCh    chan struct{}
	closeOnce sync.Once
}

func (h *remoteHold) sendFrame(c net.Conn, f ClientFrame, timeout time.Duration) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	return writeClientFrame(c, f, timeout)
}

func (h *remoteHold) Close() {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closed = true
		c := h.conn
		h.conn = nil
		lid := h.granted.LeaseID
		h.mu.Unlock()
		close(h.stopCh)
		if c != nil {
			if lid != "" && !h.failOpen {
				// ignored: best-effort explicit release before closing socket
				_ = h.sendFrame(c, ClientFrame{Op: "release", LeaseID: lid}, 500*time.Millisecond)
			}
			// ignored: closing socket unconditionally drops connection-bound lease
			_ = c.Close()
		}
	})
}

func (h *remoteHold) keepaliveAndReconnectLoop(initialReader *bufio.Reader) {
	r := initialReader
	for {
		h.mu.Lock()
		c := h.conn
		closed := h.closed
		h.mu.Unlock()
		if closed {
			return
		}
		if c != nil && r != nil {
			h.serveSession(c, r)
		}
		select {
		case <-h.stopCh:
			return
		case <-time.After(150 * time.Millisecond):
		}
		// Server dropped or restarted: reconnect and re-present active lease!
		nc, nr, err := dialEndpoint(h.ep, options{verb: "hold", dir: h.ep.dir}, h.w, 2*time.Second)
		if err != nil {
			continue
		}
		var reconnectErr error
		if h.failOpen {
			req := h.req
			req.Nowait = false
			reconnectErr = writeClientFrame(nc, req, 2*time.Second)
		} else {
			rl := ReconnectLease{
				LeaseID: h.granted.LeaseID, Host: h.granted.Host, Needs: h.granted.Needs,
				Exclusive: h.req.Exclusive, ParentLease: h.req.ParentLease,
				Holder: h.req.Holder, Source: h.req.Source, Consumer: h.req.Consumer,
				Label: h.req.Label, Priority: h.req.Priority, Why: h.req.Why,
				Cores: h.granted.Cores, CPUs: h.granted.CPUs, PID: h.req.PID, Ancestors: h.req.Ancestors,
			}
			reconnectErr = writeClientFrame(nc, ClientFrame{Op: "reconnect", Leases: []ReconnectLease{rl}}, 2*time.Second)
		}
		if reconnectErr != nil {
			// ignored: close failed reconnect attempt
			_ = nc.Close()
			continue
		}
		sf, rerr := readServerFrame(nr, nc, time.Now().Add(3*time.Second))
		if rerr != nil || sf.Op == "refused" {
			// ignored: close rejected reconnect attempt
			_ = nc.Close()
			continue
		}
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			// ignored: hold ended while reconnecting
			_ = nc.Close()
			return
		}
		if sf.Op == "granted" && sf.LeaseID != "" {
			h.granted.LeaseID = sf.LeaseID
			h.failOpen = false
		}
		h.conn = nc
		h.mu.Unlock()
		r = nr
	}
}

func (h *remoteHold) serveSession(c net.Conn, r *bufio.Reader) {
	sessionDone := make(chan struct{})
	defer close(sessionDone)

	go func() {
		tick := time.NewTicker(defaultKeepaliveMs * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-h.stopCh:
				return
			case <-sessionDone:
				return
			case now := <-tick.C:
				if err := h.sendFrame(c, ClientFrame{Op: "ping", TS: now.UnixMilli()}, 3*time.Second); err != nil {
					// ignored: force read loop to observe broken connection and reconnect
					_ = c.Close()
					return
				}
			}
		}
	}()

	for {
		sf, err := readServerFrame(r, c, time.Time{})
		if err != nil {
			return
		}
		if sf.Op == "state" && sf.LeaseID == h.granted.LeaseID {
			h.mu.Lock()
			h.paused = sf.State == "paused"
			h.mu.Unlock()
		}
	}
}

func prepareLocalDir(ep endpoint, o options, w world, autoCalibrate bool) (*capRecord, error) {
	if !ep.local {
		return nil, nil
	}
	if err := ensureDir(ep.dir, o.dirFrom, "nova-reserve "+quoteAll(o.args)); err != nil {
		return nil, err
	}
	_, have, cerr := readCapacity(ep.dir)
	if bad := corrupt(""); errors.As(cerr, &bad) {
		return nil, refuse(2, "%s; run: %s", bad, o.remedy("bench", "--force"))
	} else if cerr != nil {
		return nil, cerr
	}
	if !have {
		if !autoCalibrate || filepath.Clean(ep.dir) != systemDir {
			return nil, refuse(2, "%s %s has no capacity.json; measure or set capacity first; run: %s",
				o.dirFrom, o.dir, o.remedy("bench", "--force"))
		}
		mc, merr := calibrateLocal(ep.dir, false, w)
		if merr != nil {
			return nil, merr
		}
		return &mc, nil
	}
	return nil, nil
}

func acquireHold(o options, w world) (*remoteHold, *capRecord, error) {
	ep, err := resolveEndpoint(o, w)
	if err != nil {
		return nil, nil, err
	}
	autoMeasured, err := prepareLocalDir(ep, o, w, true)
	if err != nil {
		return nil, nil, err
	}

	mode := "any"
	host := o.host
	if ep.local {
		mode = "host"
		if host == "" {
			host = "local"
		}
	} else if o.verb == "run" && host == "" {
		mode = "host"
		host = shortHost()
	} else if host != "" {
		mode = "host"
	}

	parentLease := strings.TrimSpace(w.env("NOVA_RESERVE_LEASE"))
	parentHost := strings.TrimSpace(w.env("NOVA_RESERVE_HOST"))
	if parentLease != "" && mode == "host" && parentHost != "" && parentHost != host && !(ep.local && parentHost == "local") {
		// Cross-host nested request still passes parentLease for deadlock cycle detection!
	}

	var localUsable []int
	var localGroups [][]int
	if o.verb == "run" || ep.local || (host != "" && strings.EqualFold(host, shortHost())) {
		if u, uerr := w.usable(); uerr == nil && len(u) > 0 {
			localUsable = u
			localGroups = w.groups(u)
		}
	}

	req := ClientFrame{
		Op:          "request",
		ReqID:       fmt.Sprintf("req-%d-%d", w.pid, w.now().UnixNano()),
		Mode:        mode,
		Host:        host,
		Needs:       NeedsSpec{Raw: o.needsRaw},
		Exclusive:   o.exclusive,
		Nowait:      o.nowait,
		WaitMs:      o.wait.Milliseconds(),
		ParentLease: parentLease,
		Holder:      o.holder,
		Source:      cmp.Or(o.source, o.holder),
		Consumer:    o.consumer,
		Label:       o.label,
		Priority:    o.priority,
		Why:         o.why,
		PID:         w.pid,
		Ancestors:   readPPIDChain(w.pid),
		UsableCPUs:  localUsable,
		SMTGroups:   localGroups,
	}

	dialBudget := 5 * time.Second
	if o.holder == "ci" {
		dialBudget = defaultCIFailOpenMs * time.Millisecond
	}
	ciDeadline := time.Now().Add(defaultCIFailOpenMs * time.Millisecond)

	c, r, derr := dialEndpoint(ep, o, w, dialBudget)
	if derr != nil {
		if o.holder == "ci" {
			return newCIFailOpenHold(ep, w, req, derr), autoMeasured, nil
		}
		return nil, nil, refuse(2, "cannot reach arbitrator at %s: %v; run: %s", ep.address, cmp.Or(errors.Unwrap(derr), derr), o.remedy("status"))
	}

	if err := writeClientFrame(c, req, 3*time.Second); err != nil {
		// ignored: close failed socket
		_ = c.Close()
		if o.holder == "ci" {
			return newCIFailOpenHold(ep, w, req, err), autoMeasured, nil
		}
		return nil, nil, refuse(2, "write request to %s: %v; run: %s", ep.address, err, o.remedy("status"))
	}

	// Keep ping alive while waiting in queue so long waits (>15s) never expire!
	waitDone := make(chan struct{})
	var waitPingWg sync.WaitGroup
	var closeWaitOnce sync.Once
	stopWaitPing := func() {
		closeWaitOnce.Do(func() { close(waitDone) })
		waitPingWg.Wait()
	}
	defer stopWaitPing()
	waitPingWg.Add(1)
	go func() {
		defer waitPingWg.Done()
		tick := time.NewTicker(defaultKeepaliveMs * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-waitDone:
				return
			case now := <-tick.C:
				if writeClientFrame(c, ClientFrame{Op: "ping", TS: now.UnixMilli()}, 3*time.Second) != nil {
					return
				}
			}
		}
	}()

	for {
		var readLimit time.Time
		if o.holder == "ci" {
			readLimit = ciDeadline
		}
		sf, rerr := readServerFrame(r, c, readLimit)
		if rerr != nil {
			stopWaitPing()
			// ignored: close broken socket
			_ = c.Close()
			if o.holder == "ci" {
				return newCIFailOpenHold(ep, w, req, rerr), autoMeasured, nil
			}
			return nil, nil, refuse(2, "arbitrator %s closed connection: %v; run: %s", ep.address, rerr, o.remedy("status"))
		}
		switch sf.Op {
		case "pong", "queued":
			continue
		case "refused":
			stopWaitPing()
			// ignored: close socket after refusal
			_ = c.Close()
			return nil, nil, mapRefusedError(sf, o)
		case "granted":
			stopWaitPing()
			h := &remoteHold{
				ep:      ep,
				w:       w,
				req:     req,
				granted: sf,
				conn:    c,
				stopCh:  make(chan struct{}),
			}
			go h.keepaliveAndReconnectLoop(r)
			return h, autoMeasured, nil
		}
	}
}

func newCIFailOpenHold(ep endpoint, w world, req ClientFrame, cause error) *remoteHold {
	fmt.Fprintf(w.stderr, "nova-reserve: warning: arbitrator %s unreachable within 3s (%v); running CI hold in fail-open mode\n", ep.address, cause)
	cores := max(1, w.cpus)
	lid := fmt.Sprintf("L-failopen-%d", w.pid)
	host := cmp.Or(req.Host, shortHost())
	h := &remoteHold{
		ep:       ep,
		w:        w,
		req:      req,
		failOpen: true,
		granted: ServerFrame{
			Op:      "granted",
			ReqID:   req.ReqID,
			LeaseID: lid,
			Host:    host,
			Needs:   map[string]int{"cpu": 1, "memory": 1},
			Cores:   cores,
			Env:     buildLeaseEnv(lid, host, cores),
		},
		stopCh: make(chan struct{}),
	}
	go h.keepaliveAndReconnectLoop(nil)
	return h
}

func mapRefusedError(sf ServerFrame, o options) error {
	switch sf.Code {
	case "unauthorized", "bad_request":
		return refuse(2, "%s; run: %s", sf.Message, helpFor(o.verb))
	case "deadlock":
		return refuse(1, "%s; run: %s", sf.Message, o.remedy("status"))
	default:
		return refuse(1, "%s; run: %s", sf.Message, o.remedy("status"))
	}
}

func claimRemote(o options, w world) error {
	ep, err := resolveEndpoint(o, w)
	if err != nil {
		return err
	}
	if _, err := prepareLocalDir(ep, o, w, true); err != nil {
		return err
	}
	c, r, err := dialEndpoint(ep, o, w, 5*time.Second)
	if err != nil {
		return refuse(2, "cannot reach arbitrator at %s: %v; run: %s", ep.address, cmp.Or(errors.Unwrap(err), err), o.remedy("status"))
	}
	defer func() {
		// ignored: close claim RPC connection after reply
		_ = c.Close()
	}()
	host := o.host
	if ep.local && host == "" {
		host = "local"
	} else if !ep.local && host == "" {
		host = shortHost()
	}
	ttl := o.ttl
	if ttl <= 0 {
		ttl = defaultClaimTTLMs * time.Millisecond
	}
	req := ClientFrame{
		Op:        "claim",
		ReqID:     fmt.Sprintf("claim-%d-%d", w.pid, w.now().UnixNano()),
		Host:      host,
		Needs:     NeedsSpec{Raw: o.needsRaw},
		Exclusive: o.exclusive,
		TTLMs:     ttl.Milliseconds(),
		Source:    cmp.Or(o.source, "person"),
		Why:       o.why,
	}
	if err := writeClientFrame(c, req, 3*time.Second); err != nil {
		return refuse(2, "write claim to %s: %v; run: %s", ep.address, err, o.remedy("status"))
	}
	for {
		sf, rerr := readServerFrame(r, c, time.Now().Add(5*time.Second))
		if rerr != nil {
			return refuse(2, "read claim reply from %s: %v; run: %s", ep.address, rerr, o.remedy("status"))
		}
		switch sf.Op {
		case "pong":
			continue
		case "refused":
			return mapRefusedError(sf, o)
		case "granted":
			if o.jsonOut {
				b, merr := MarshalServerFrame(sf)
				if merr != nil {
					return merr
				}
				fmt.Fprintln(w.stdout, string(b))
				return nil
			}
			fmt.Fprintf(w.stdout, "CLAIMED lease_id=%s host=%s %s expires_at=%s\n",
				sf.LeaseID, sf.Host, formatNeedsKV(sf.Needs), sf.ExpiresAt)
			return nil
		}
	}
}

func releaseRemote(o options, w world) error {
	ep, err := resolveEndpoint(o, w)
	if err != nil {
		return err
	}
	c, r, err := dialEndpoint(ep, o, w, 3*time.Second)
	if err != nil {
		if ep.local {
			return refuse(1, "unknown lease or claim %q; run: %s", o.leaseID, o.remedy("status"))
		}
		return refuse(2, "cannot reach arbitrator at %s: %v; run: %s", ep.address, cmp.Or(errors.Unwrap(err), err), o.remedy("status"))
	}
	defer func() {
		// ignored: close release RPC connection
		_ = c.Close()
	}()
	if err := writeClientFrame(c, ClientFrame{Op: "release", LeaseID: o.leaseID}, 3*time.Second); err != nil {
		return refuse(2, "write release to %s: %v; run: %s", ep.address, err, o.remedy("status"))
	}
	for {
		sf, rerr := readServerFrame(r, c, time.Now().Add(5*time.Second))
		if rerr != nil {
			return refuse(2, "read release reply from %s: %v; run: %s", ep.address, rerr, o.remedy("status"))
		}
		switch sf.Op {
		case "pong":
			continue
		case "refused":
			return refuse(1, "%s; run: %s", sf.Message, o.remedy("status"))
		case "state":
			fmt.Fprintf(w.stdout, "RELEASED lease_id=%s\n", o.leaseID)
			return nil
		}
	}
}

func querySnapshot(o options, w world) (ServerFrame, error) {
	ep, err := resolveEndpoint(o, w)
	if err != nil {
		return ServerFrame{}, err
	}
	if ep.local {
		fi, lerr := os.Lstat(o.dir)
		if lerr != nil {
			return ServerFrame{}, refuse(2, "%s %s is not a reservation directory (no reservation was taken there); run: %s",
				o.dirFrom, shellQuote(o.dir), helpFor("status"))
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return ServerFrame{}, refuse(2, "%s %s is a symbolic link; nova-reserve uses no link as its directory: give the directory it names, or remove the link; run: ls -ld %s",
				o.dirFrom, o.dir, shellQuote(o.dir))
		}
		if !fi.IsDir() {
			return ServerFrame{}, refuse(2, "%s %s is a file, not a directory; run: ls -ld %s", o.dirFrom, o.dir, shellQuote(o.dir))
		}
	}
	c, r, derr := dialEndpoint(ep, o, w, 3*time.Second)
	if derr != nil {
		if !ep.local {
			return ServerFrame{}, refuse(2, "cannot reach arbitrator at %s: %v; run: %s", ep.address, cmp.Or(errors.Unwrap(derr), derr), helpFor("status"))
		}
		// Local directory exists with no active daemon: synthesize snapshot directly from capacity.json.
		capRec, have, cerr := readCapacity(ep.dir)
		if bad := corrupt(""); errors.As(cerr, &bad) {
			return ServerFrame{}, refuse(2, "%s; run: %s", bad, o.remedy("bench", "--force"))
		} else if cerr != nil {
			return ServerFrame{}, cerr
		}
		if !have {
			return ServerFrame{Op: "snapshot", Machines: []SnapshotMachine{}}, nil
		}
		m := MachineSpec{
			Host: "local", Online: true, Tags: effectiveTags(capRec.Tags, w.goos, w.goarch),
			UsableCores: capRec.UsableCores, Resources: capRec.Resources,
		}
		eng := NewEngine([]MachineSpec{m}, w.now().UnixMilli(), 0)
		return eng.Snapshot("", w.now().UnixMilli()), nil
	}
	defer func() {
		// ignored: close status connection after snapshot
		_ = c.Close()
	}()
	if err := writeClientFrame(c, ClientFrame{Op: "subscribe", Host: o.host}, 3*time.Second); err != nil {
		if sf, rerr := readServerFrame(r, c, time.Now().Add(250*time.Millisecond)); rerr == nil && sf.Op == "refused" {
			return ServerFrame{}, mapRefusedError(sf, o)
		}
		return ServerFrame{}, refuse(2, "write status request to %s: %v; run: %s", ep.address, err, helpFor("status"))
	}
	for {
		sf, rerr := readServerFrame(r, c, time.Now().Add(5*time.Second))
		if rerr != nil {
			return ServerFrame{}, refuse(2, "read status from %s: %v; run: %s", ep.address, rerr, helpFor("status"))
		}
		if sf.Op == "pong" {
			continue
		}
		if sf.Op == "refused" {
			return ServerFrame{}, mapRefusedError(sf, o)
		}
		if sf.Op == "snapshot" {
			return sf, nil
		}
	}
}

func subscribeRemote(o options, w world) error {
	ep, err := resolveEndpoint(o, w)
	if err != nil {
		return err
	}
	if ep.local {
		if err := ensureDir(ep.dir, o.dirFrom, "nova-reserve "+quoteAll(o.args)); err != nil {
			return err
		}
		if _, have, cerr := readCapacity(ep.dir); cerr == nil && have {
			// ignored: best-effort daemon start before subscribe
			_ = spawnLocalDaemon(ep, o)
		}
	}
	c, r, derr := dialEndpoint(ep, o, w, 5*time.Second)
	if derr != nil {
		return refuse(2, "cannot reach arbitrator at %s: %v; run: %s", ep.address, cmp.Or(errors.Unwrap(derr), derr), helpFor("subscribe"))
	}
	defer func() {
		// ignored: close subscribe stream on exit
		_ = c.Close()
	}()
	if err := writeClientFrame(c, ClientFrame{Op: "subscribe", Host: o.host}, 3*time.Second); err != nil {
		return refuse(2, "write subscribe to %s: %v; run: %s", ep.address, err, helpFor("subscribe"))
	}
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		tick := time.NewTicker(defaultKeepaliveMs * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stopPing:
				return
			case now := <-tick.C:
				if writeClientFrame(c, ClientFrame{Op: "ping", TS: now.UnixMilli()}, 3*time.Second) != nil {
					return
				}
			}
		}
	}()
	for {
		sf, rerr := readServerFrame(r, c, time.Time{})
		if rerr != nil {
			return nil
		}
		if sf.Op == "pong" {
			continue
		}
		if sf.Op == "refused" {
			return mapRefusedError(sf, o)
		}
		b, merr := MarshalServerFrame(sf)
		if merr != nil {
			return merr
		}
		fmt.Fprintln(w.stdout, string(b))
		flushOut(w.stdout)
	}
}
