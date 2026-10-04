package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/seatcheck"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// The seat check (docs/SPEC-SPRINT.md, "The seat check"; the owner, 2026-10-03
// 11:21 PM ET: "as coordinator, you need to check these things are up when you
// start."): `nova-sprint machinery` measures the machinery under the sprint and
// prints one line per thing, up or DOWN with the remedy; coordinator and
// handover print it before anything else. The judging is internal/seatcheck;
// this file is the transport: what is measured, and how. Every reach outside
// the store goes through outside, so a test runs the check with no socket, no
// process and no clock of its own.

// DashboardEnv names the local dashboard the check reads; DashboardListen is
// the default.
const DashboardEnv = "NOVA_SPRINT_DASHBOARD"

// BusEnv names nova-bus2's Redis; unset is "not configured", never DOWN. It
// is dialed as the store is (openConn): the one fleet Redis seat every tool
// dials with, internal/nsprint/redisauth, never a password variable of its own.
const BusEnv = "NOVA_BUS_REDIS"

// outside is every reach of the check past the store: each a function a test
// replaces. A served check (self: the server answering coordinator or
// handover in its single-threaded step) runs none of httpGet, ping and
// launchdLoaded: the dashboard, the bus and the agents say not measured.
type outside struct {
	// serverAddr is the sprint's server as this process knows it: the address
	// NOVA_SPRINT_SERVER names, or self when this process is the server.
	serverAddr func() (addr string, self bool)
	// roundTrip sends the server one verb and says how long it took.
	roundTrip func(ctx context.Context, addr string) (time.Duration, error)
	// listenerPID is the pid listening on a local port, 0 unknown.
	listenerPID func(port string) int
	// dbsize is the store's DBSIZE, -1 when the store keeps none (the twin).
	dbsize func(ctx context.Context, addr string) int64
	// httpGet is one GET: the status and the body.
	httpGet func(ctx context.Context, url string) (int, []byte, error)
	// ping pings a Redis by address.
	ping func(ctx context.Context, addr string) error
	// hostname is this machine's short name; uid its user id.
	hostname func() string
	uid      func() int
	// launchdLoaded says whether the launchd label is loaded in gui/<uid>;
	// measured false where there is no launchd (not darwin).
	launchdLoaded func(ctx context.Context, uid int, label string) (loaded, measured bool)
}

// realOutside is the check as it runs on a machine.
func (a *app) realOutside() outside {
	return outside{
		serverAddr: func() (string, bool) {
			if a.serveAddr != "" {
				return a.serveAddr, true
			}
			return a.getenv(ServerEnv), false
		},
		roundTrip: func(ctx context.Context, addr string) (time.Duration, error) {
			t := a.now()
			res, err := a.ask(ctx, addr, []string{"routes"}, nil)
			if err != nil {
				return 0, err
			}
			if res.Code != 0 {
				return 0, fmt.Errorf("routes answered exit %d: %s", res.Code, strings.TrimSpace(res.Stderr))
			}
			return a.now().Sub(t), nil
		},
		listenerPID: func(port string) int {
			cmd, cancel := subproc.Command(context.Background(), subproc.Tool, "lsof", "-nP", "-iTCP:"+port, "-sTCP:LISTEN", "-t")
			defer cancel()
			out, err := cmd.Output()
			if err != nil {
				return 0
			}
			pid, _ := strconv.Atoi(strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]))
			return pid
		},
		dbsize: func(ctx context.Context, addr string) int64 {
			conn, ok := a.conns[addr]
			if !ok {
				return -1
			}
			n, err := conn.Client().DBSize(ctx).Result()
			if err != nil {
				return -1
			}
			return n
		},
		httpGet: func(ctx context.Context, url string) (int, []byte, error) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return 0, nil, err
			}
			resp, err := (&http.Client{Transport: a.transport}).Do(req) // nil is http.DefaultTransport; a test gives a handler
			if err != nil {
				return 0, nil, err
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			return resp.StatusCode, body, err
		},
		ping: func(ctx context.Context, addr string) error {
			conn, err := a.openConn(ctx, addr)
			if err != nil {
				return err
			}
			return conn.Close()
		},
		hostname: func() string {
			h, _ := os.Hostname()
			return strings.SplitN(h, ".", 2)[0]
		},
		uid: os.Getuid,
		launchdLoaded: func(ctx context.Context, uid int, label string) (bool, bool) {
			if runtime.GOOS != "darwin" {
				return false, false
			}
			cmd, cancel := subproc.Command(ctx, subproc.Tool, "launchctl", "print", fmt.Sprintf("gui/%d/%s", uid, label))
			defer cancel()
			return cmd.Run() == nil, true
		},
	}
}

// measure runs every probe against the store and the outside and judges the
// measures: the check as coordinator, handover and machinery print it.
func (a *app) machineryCheck(ctx context.Context, st *store.Store, redisAddr string) seatcheck.Report {
	o := a.outside
	if o.serverAddr == nil {
		o = a.realOutside()
	}
	now := a.now()
	m := seatcheck.Measures{Fleet: []seatcheck.MemberM{}, Friends: []seatcheck.FriendM{}, Errs: map[string]string{}, Host: o.hostname()}

	// 1. the server
	addr, self := o.serverAddr()
	m.Server = seatcheck.ServerM{Addr: addr, Self: self}
	switch {
	case self:
		m.Server.PID = os.Getpid()
	case addr != "":
		d, err := o.roundTrip(ctx, addr)
		if err != nil {
			m.Server.Err = err.Error()
		} else {
			m.Server.Millis = d.Milliseconds()
			if host, port, err := net.SplitHostPort(addr); err == nil && (host == "127.0.0.1" || host == "localhost" || host == "::1") {
				m.Server.PID = o.listenerPID(port)
			}
		}
	}

	// 2. the store, 3. the loop: the records the run loop writes
	m.Store = seatcheck.StoreM{Addr: redisAddr, DBSize: -1}
	mach, hb, err := st.Machine(ctx)
	if err != nil {
		m.Store.Err = err.Error()
	} else {
		m.Store.DBSize = o.dbsize(ctx, redisAddr)
		m.Store.Machine = strings.ToLower(mach.StateWord())
		if ep, err := st.EpochNow(ctx); err == nil {
			m.Store.Epoch = ep.N
		}
		if seen := hb.Alive(); !seen.IsZero() {
			m.Store.Tick = seatcheck.TickM{Ticked: true, Age: now.Sub(seen), Ticks: hb.Ticks, Failures: hb.Failures, Error: hb.Error}
		}
	}

	// 4. the fleet, 6. the readers: the tables as where shows them
	v, _, err := a.where(ctx, st, defaultStale, false)
	if err != nil {
		m.Errs[seatcheck.Fleet], m.Errs[seatcheck.Readers] = err.Error(), err.Error()
	} else {
		members := sortedKeys(v.Tables[sprint.Fleet])
		beats, err := st.Beats(ctx, members)
		if err != nil {
			m.Errs[seatcheck.Fleet] = err.Error()
		}
		for _, name := range members {
			b := beats[name]
			m.Fleet = append(m.Fleet, seatcheck.MemberM{Name: name, Status: v.Tables[sprint.Fleet][name][sprint.Status], Beaten: b.Beaten(), Age: now.Sub(b.At)})
		}
		readers := sortedKeys(v.Tables[sprint.Readers])
		states, err := st.ReaderStates(ctx, readers, now)
		if err != nil {
			m.Errs[seatcheck.Readers] = err.Error()
		}
		m.Readers.Total = len(readers)
		for _, r := range readers {
			if states[r] == sprint.ReaderUp {
				m.Readers.Up++
			}
			n, _ := strconv.Atoi(v.Tables[sprint.Readers][r][sprint.Reading]) // ignored: a cell that is no number counts 0
			m.Readers.Reading += n
		}
	}

	// 5. the friends: the roster's statuses and beats, and their agents here
	rows, err := st.FriendRows(ctx, now)
	if err != nil {
		m.Errs[seatcheck.Friends] = err.Error()
	}
	beats, err := st.FriendBeats(ctx)
	if err != nil {
		m.Errs[seatcheck.Friends] = err.Error()
	}
	for _, r := range rows {
		b := beats[r.Name]
		f := seatcheck.FriendM{Name: r.Name, Status: r.Status, Beaten: b.Beaten(), Age: now.Sub(b.At)}
		if r.Status == sprint.Down && !self {
			// her agent on this host: launchd's word where there is a launchd
			switch loaded, measured := o.launchdLoaded(ctx, o.uid(), seatcheck.Label(r.Name)); {
			case !measured:
			case loaded:
				f.Loaded = "loaded"
			default:
				f.Loaded = "not loaded"
			}
		}
		m.Friends = append(m.Friends, f)
	}

	// 7. the dashboard
	dash := a.getenv(DashboardEnv)
	if dash == "" {
		dash = DashboardListen
	}
	m.Dashboard = seatcheck.DashM{Addr: dash}
	if !self {
		status, body, err := o.httpGet(ctx, "http://"+dash+"/api/sprint")
		m.Dashboard.Status = status
		if err != nil {
			m.Dashboard.Err = err.Error()
		} else {
			var snap struct {
				Build string `json:"build"`
			}
			_ = json.Unmarshal(body, &snap) // ignored: a body that is no snapshot carries no build, which the judge says
			m.Dashboard.Build = snap.Build
		}
	}

	// 8. the bus
	m.Bus.Addr = a.getenv(BusEnv)
	if !self && m.Bus.Addr != "" {
		if err := o.ping(ctx, m.Bus.Addr); err != nil {
			m.Bus.Err = err.Error()
		}
	}

	// 9. the inbox
	in, err := st.Inbox(ctx, defaultDeadline, defaultStale, 10000)
	if err != nil {
		m.Errs[seatcheck.Inbox] = err.Error()
	}
	for _, g := range in.Groups {
		if g.Kind != sprint.Judgment {
			continue
		}
		m.Inbox.Open++
		if w := now.Sub(g.Oldest); w > m.Inbox.Oldest {
			m.Inbox.Oldest = w
		}
	}
	return seatcheck.Judge(m, now)
}

// cmdMachinery is `nova-sprint machinery`: the seat check on demand, read-only;
// exit 1 when anything is DOWN.
func (a *app) cmdMachinery(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("machinery")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, "machinery", argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "machinery", err.Error())
	}
	r := a.machineryCheck(context.Background(), st, c.redis)
	if c.json {
		fmt.Fprintln(stdout, r.JSON())
	} else {
		fmt.Fprint(stdout, r.Text())
	}
	if r.Down > 0 {
		return 1
	}
	return 0
}
