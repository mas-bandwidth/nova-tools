package main

import (
	"bufio"
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/v2/modules/config"
)

type ServerConfig struct {
	Local            bool
	Dir              string
	SocketPath       string
	ListenAddr       string
	Password         string
	ReconnectGraceMs int64
	IdleExit         time.Duration
	LoadMachines     func(context.Context) ([]MachineSpec, error)
}

type peerConn struct {
	id        uint64
	netConn   net.Conn
	sendCh    chan []byte
	authed    bool
	subscribe bool
	subHost   string
	closeOnce sync.Once
	done      chan struct{}
}

func (p *peerConn) enqueue(line []byte) {
	select {
	case p.sendCh <- line:
	default:
		// Slow subscriber/peer exceeded 256 queued frames; drop connection so it reconnects fresh.
		p.shutdown()
	}
}

func (p *peerConn) shutdown() {
	p.closeOnce.Do(func() {
		close(p.done)
		// ignored: closing peer socket on disconnect or shutdown
		_ = p.netConn.Close()
	})
}

type Server struct {
	cfg       ServerConfig
	mu        sync.Mutex
	eng       *Engine
	peers     map[uint64]*peerConn
	nextConn  atomic.Uint64
	lastIdle  time.Time
	capMtime  int64
	listener  net.Listener
	closeOnce sync.Once
	closed    chan struct{}
}

func NewServer(cfg ServerConfig, initial []MachineSpec) *Server {
	nowMs := time.Now().UnixMilli()
	eng := NewEngine(initial, nowMs, cfg.ReconnectGraceMs)
	if cfg.ReconnectGraceMs > 0 && nowMs > 0 {
		eng.SetNextSeq(uint64(nowMs%100_000_000)*100 + 1)
	}
	if cfg.Local {
		if hn, err := os.Hostname(); err == nil {
			short, _, _ := strings.Cut(hn, ".")
			eng.SetLocalAlias(short)
		}
	}
	return &Server{
		cfg:      cfg,
		eng:      eng,
		peers:    map[uint64]*peerConn{},
		lastIdle: time.Now(),
		closed:   make(chan struct{}),
	}
}

func shortHost() string {
	hn, err := os.Hostname()
	if err != nil || hn == "" {
		return "local"
	}
	short, _, _ := strings.Cut(hn, ".")
	return cmp.Or(short, "local")
}

// resolveSocketPath returns <dir>/sock when short enough for Unix domain
// sockets (SUN_LEN <= 104 on darwin), falling back to a deterministic /tmp
// socket path when <dir> is a deep temporary path.
func resolveSocketPath(dir, explicit string) string {
	if explicit != "" {
		return explicit
	}
	abs := dir
	if a, err := filepath.Abs(dir); err == nil {
		abs = a
	}
	cand := filepath.Join(abs, "sock")
	if len(cand) <= 96 {
		return cand
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join("/tmp", "nr-"+hex.EncodeToString(sum[:8])+".sock")
}

func localMachineFromDir(dir string) (MachineSpec, int64, error) {
	p := capPath(dir)
	var mtime int64
	if fi, err := os.Stat(p); err == nil {
		mtime = fi.ModTime().UnixNano()
	}
	c, ok, err := readCapacity(dir)
	if err != nil {
		return MachineSpec{}, 0, err
	}
	if !ok {
		return MachineSpec{}, 0, fmt.Errorf("%s has no capacity.json", dir)
	}
	// ignored: non-Linux platforms return nil usable CPUs without pinning
	usable, _ := usableCPUs()
	var groups [][]int
	if len(usable) > 0 {
		groups = smtGroups(usable)
	}
	return MachineSpec{
		Host:        "local",
		Online:      true,
		Tags:        effectiveTags(c.Tags, runtime.GOOS, runtime.GOARCH),
		UsableCores: c.UsableCores,
		Resources:   c.Resources,
		UsableCPUs:  usable,
		SMTGroups:   groups,
	}, mtime, nil
}

func loadMachinesFromStore(ctx context.Context, dsn, pgPassword string) ([]MachineSpec, error) {
	st, err := config.Open(ctx, dsn, pgPassword)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	rows, err := st.Machines(ctx)
	if err != nil {
		return nil, err
	}
	var out []MachineSpec
	for _, m := range rows {
		res := m.EffectiveResources()
		cores := 0
		if m.Cores != nil && *m.Cores > 0 {
			cores = *m.Cores
		}
		if res["cpu"] <= 0 && cores > 0 {
			res["cpu"] = cores
		}
		if cores <= 0 && res["cpu"] > 0 {
			cores = res["cpu"]
		}
		if res["cpu"] <= 0 || res["memory"] <= 0 {
			roles := strings.Split(m.Roles, ",")
			hasWorkRole := false
			for _, r := range roles {
				if r == "bench" || r == "runner" {
					hasWorkRole = true
				}
			}
			if !hasWorkRole {
				continue
			}
			res["cpu"] = max(1, res["cpu"])
			res["memory"] = max(1, res["memory"])
			cores = max(1, cores)
		}
		out = append(out, MachineSpec{
			Host:        m.Name,
			Address:     m.Address,
			User:        m.User,
			Online:      true,
			Tags:        m.EffectiveTags(),
			UsableCores: max(1, cores),
			Resources:   res,
		})
	}
	return out, nil
}

func (s *Server) Reload(ctx context.Context) error {
	if s.cfg.Local {
		m, mt, err := localMachineFromDir(s.cfg.Dir)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.capMtime = mt
		msgs := s.eng.UpdateMachines([]MachineSpec{m}, time.Now().UnixMilli())
		s.dispatchLocked(msgs)
		s.mu.Unlock()
		return nil
	}
	if s.cfg.LoadMachines == nil {
		return nil
	}
	ms, err := s.cfg.LoadMachines(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	msgs := s.eng.UpdateMachines(ms, time.Now().UnixMilli())
	s.dispatchLocked(msgs)
	s.mu.Unlock()
	return nil
}

func (s *Server) maybeRefreshLocalLocked(nowMs int64) {
	if !s.cfg.Local || s.cfg.Dir == "" {
		return
	}
	fi, err := os.Stat(capPath(s.cfg.Dir))
	if err != nil || fi.ModTime().UnixNano() == s.capMtime {
		return
	}
	if m, mt, err := localMachineFromDir(s.cfg.Dir); err == nil {
		s.capMtime = mt
		s.dispatchLocked(s.eng.UpdateMachines([]MachineSpec{m}, nowMs))
	}
}

func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.mu.Lock()
	s.listener = ln
	if s.cfg.Local && s.cfg.Dir != "" {
		if fi, err := os.Stat(capPath(s.cfg.Dir)); err == nil {
			s.capMtime = fi.ModTime().UnixNano()
		}
	}
	s.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.closed:
		}
	}()

	go s.tickLoop(ctx)

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.closed:
				wg.Wait()
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				wg.Wait()
				return nil
			}
			continue
		}
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			s.handleConn(c)
		}(conn)
	}
}

func (s *Server) Close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.mu.Lock()
		ln := s.listener
		peers := make([]*peerConn, 0, len(s.peers))
		for _, p := range s.peers {
			peers = append(peers, p)
		}
		s.mu.Unlock()
		if ln != nil {
			// ignored: closing listener on server shutdown
			_ = ln.Close()
		}
		for _, p := range peers {
			p.shutdown()
		}
	})
}

func (s *Server) tickLoop(ctx context.Context) {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	var lastReload time.Time
	for {
		select {
		case <-s.closed:
			return
		case now := <-tick.C:
			if !s.cfg.Local && s.cfg.LoadMachines != nil && now.Sub(lastReload) >= 5*time.Second {
				lastReload = now
				// ignored: transient store blips preserve existing in-memory inventory
				_ = s.Reload(ctx)
			}
			s.mu.Lock()
			nowMs := now.UnixMilli()
			if s.cfg.Local && s.cfg.Dir != "" && filepath.Clean(s.cfg.Dir) != systemDir && len(s.peers) == 0 {
				if _, err := os.Stat(s.cfg.Dir); errors.Is(err, os.ErrNotExist) {
					s.mu.Unlock()
					s.Close()
					return
				}
			}
			s.maybeRefreshLocalLocked(nowMs)
			msgs := s.eng.Tick(nowMs)
			s.dispatchLocked(msgs)
			idle := s.eng.IsIdle() && len(s.peers) == 0
			if !idle {
				s.lastIdle = now
			} else if s.cfg.IdleExit > 0 && now.Sub(s.lastIdle) >= s.cfg.IdleExit {
				s.mu.Unlock()
				s.Close()
				return
			}
			s.mu.Unlock()
		}
	}
}

func (s *Server) handleConn(c net.Conn) {
	cid := s.nextConn.Add(1)
	requireAuth := !s.cfg.Local
	peer := &peerConn{
		id:      cid,
		netConn: c,
		sendCh:  make(chan []byte, 256),
		authed:  !requireAuth,
		done:    make(chan struct{}),
	}

	s.mu.Lock()
	s.peers[cid] = peer
	s.eng.RegisterConn(cid, time.Now().UnixMilli())
	s.lastIdle = time.Now()
	s.mu.Unlock()

	go func() {
		for {
			select {
			case <-peer.done:
				return
			case line := <-peer.sendCh:
				// ignored: write deadline guards against wedged peer sockets
				_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err := c.Write(line); err != nil {
					peer.shutdown()
					return
				}
			}
		}
	}()

	defer func() {
		peer.shutdown()
		s.mu.Lock()
		delete(s.peers, cid)
		msgs := s.eng.CloseConn(cid, time.Now().UnixMilli())
		s.dispatchLocked(msgs)
		s.lastIdle = time.Now()
		s.mu.Unlock()
	}()

	if requireAuth {
		// ignored: initial auth frame deadline
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	}

	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var f ClientFrame
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			s.sendDirect(peer, ServerFrame{Op: "refused", Code: "bad_request", Message: "invalid JSON frame"})
			if !peer.authed {
				return
			}
			continue
		}
		if !peer.authed {
			gotSum := sha256.Sum256([]byte(f.Password))
			wantSum := sha256.Sum256([]byte(s.cfg.Password))
			passOK := subtle.ConstantTimeCompare(gotSum[:], wantSum[:]) == 1 && (s.cfg.Password == "" || f.Password != "")
			if f.Op != "auth" || !passOK {
				// ignored: static refusal struct marshals without error
				b, _ := MarshalServerFrame(ServerFrame{Op: "refused", ReqID: f.ReqID, Code: "unauthorized", Message: "authentication failed"})
				go func() {
					// Drain pipelined client bytes briefly so synchronous pipes never deadlock while writing refusal.
					var sink [512]byte
					for {
						if _, rerr := c.Read(sink[:]); rerr != nil {
							return
						}
					}
				}()
				// ignored: best-effort unauthorized refusal before closing socket
				_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
				// ignored: best-effort write before disconnect
				_, _ = c.Write(append(b, '\n'))
				return
			}
			peer.authed = true
			// ignored: clear initial auth deadline; engine keepalive governs idle lifetime
			_ = c.SetReadDeadline(time.Time{})
			s.mu.Lock()
			s.eng.TouchConn(cid, time.Now().UnixMilli())
			s.mu.Unlock()
			continue
		}
		s.handleFrame(peer, f)
	}
}

func (s *Server) handleFrame(peer *peerConn, f ClientFrame) {
	nowMs := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maybeRefreshLocalLocked(nowMs)

	switch f.Op {
	case "auth":
		s.eng.TouchConn(peer.id, nowMs)
	case "ping":
		s.eng.TouchConn(peer.id, nowMs)
		s.sendDirect(peer, ServerFrame{Op: "pong", TS: f.TS})
	case "request":
		s.dispatchLocked(s.eng.Request(peer.id, f, nowMs))
	case "claim":
		s.dispatchLocked(s.eng.PlaceClaim(peer.id, f, nowMs))
	case "release":
		target := cmp.Or(f.LeaseID, f.ReqID)
		msgs, ok := s.eng.Release(peer.id, target, nowMs)
		if !ok {
			s.sendDirect(peer, ServerFrame{
				Op: "refused", ReqID: f.ReqID, Code: "bad_request",
				Message: fmt.Sprintf("unknown lease or claim %q", target),
			})
			return
		}
		s.dispatchLocked(msgs)
	case "reconnect":
		s.dispatchLocked(s.eng.Reconnect(peer.id, f, nowMs))
	case "subscribe":
		s.eng.TouchConn(peer.id, nowMs)
		peer.subscribe = true
		peer.subHost = s.eng.normHost(f.Host)
		s.sendDirect(peer, s.eng.Snapshot(peer.subHost, nowMs))
	default:
		s.eng.TouchConn(peer.id, nowMs)
		s.sendDirect(peer, ServerFrame{
			Op: "refused", ReqID: f.ReqID, Code: "bad_request",
			Message: fmt.Sprintf("unknown op %q", f.Op),
		})
	}
}

func (s *Server) sendDirect(peer *peerConn, frame ServerFrame) {
	b, err := MarshalServerFrame(frame)
	if err != nil {
		return
	}
	peer.enqueue(append(b, '\n'))
}

func (s *Server) dispatchLocked(msgs []OutMsg) {
	for _, m := range msgs {
		b, err := MarshalServerFrame(m.Frame)
		if err != nil {
			continue
		}
		line := append(b, '\n')
		if m.Broadcast {
			for _, p := range s.peers {
				if !p.subscribe {
					continue
				}
				if p.subHost != "" && m.EventHost != "" && m.EventHost != "*" && p.subHost != m.EventHost {
					continue
				}
				p.enqueue(line)
			}
			continue
		}
		if p := s.peers[m.ConnID]; p != nil {
			p.enqueue(line)
		}
	}
}
