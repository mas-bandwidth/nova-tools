package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/seatcheck"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
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
// handover in its single-threaded step) runs none of httpGet, ping,
// launchdLoaded, devMergeQueue and machineVersions: the dashboard, the bus,
// the dev merge queue, machine versions and the agents say not measured.
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
	// devMergeQueue reads the dev merge queue: its entries, open green pull
	// requests, and any pull request thrown out since the previous check.
	devMergeQueue func(ctx context.Context) (seatcheck.QueueM, error)
	// machineVersions reads each machine's nova-update version and dev's tip
	// the same way (the version identity that verb prints).
	machineVersions func(ctx context.Context, machines []string) (seatcheck.VersionsM, error)
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
		devMergeQueue: func(ctx context.Context) (seatcheck.QueueM, error) {
			path, err := seatQueuePath()
			if err != nil {
				return seatcheck.QueueM{}, err
			}
			owner, name, err := moduleRepo()
			if err != nil {
				return seatcheck.QueueM{}, err
			}
			return readDevMergeQueue(ctx, a.ghQuery, owner, name, path, a.now())
		},
		machineVersions: func(ctx context.Context, machines []string) (seatcheck.VersionsM, error) {
			owner, name, err := moduleRepo()
			if err != nil {
				return seatcheck.VersionsM{}, err
			}
			return readMachineVersions(ctx, machines, owner, name, a.ghQuery, a.novaUpdateVersion)
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

	// 10. the dev merge queue, 11. each machine's installed version.
	// Neither runs in the server: both are outside probes.
	if !self {
		measureQueueAndVersions(ctx, &m, o)
	}
	return seatcheck.Judge(m, now)
}

// measureQueueAndVersions fills the two outside rows. A nil probe is a missed
// read, DOWN, never a silent green.
func measureQueueAndVersions(ctx context.Context, m *seatcheck.Measures, o outside) {
	switch {
	case o.devMergeQueue == nil:
		m.Errs[seatcheck.Queue] = "dev merge queue was not measured"
	default:
		q, err := o.devMergeQueue(ctx)
		if err != nil {
			m.Errs[seatcheck.Queue] = err.Error()
		} else {
			m.Queue = q
		}
	}
	names := make([]string, len(m.Fleet))
	for i, mm := range m.Fleet {
		names[i] = mm.Name
	}
	switch {
	case o.machineVersions == nil:
		m.Errs[seatcheck.Versions] = "machine versions were not measured"
	default:
		v, err := o.machineVersions(ctx, names)
		if err != nil {
			m.Errs[seatcheck.Versions] = err.Error()
		} else {
			m.Versions = v
		}
	}
}

// devQueueBranch is the merge queue the seat check reads: this module's dev.
// The owner and the repository name come from the module path (moduleRepo),
// so the check is of the tool's own repository.
const devQueueBranch = "dev"

// moduleRepo is the owner and name of this module, from the build info, so
// the queue read names the repository the binary belongs to.
func moduleRepo() (owner, name string, err error) {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil || info.Main.Path == "" {
		return "", "", fmt.Errorf("dev merge queue: no module path")
	}
	parts := strings.Split(info.Main.Path, "/")
	if len(parts) < 2 || parts[len(parts)-1] == "" || parts[len(parts)-2] == "" {
		return "", "", fmt.Errorf("dev merge queue: module path %q", info.Main.Path)
	}
	return parts[len(parts)-2], parts[len(parts)-1], nil
}

// devQueueQuery is one read of the queue, the open pull requests on dev, and
// each one's removals from the queue. devTipQuery is dev's tip commit, whose
// version identity is what nova-update version would print for a clean build
// of it (buildinfo.Resolve).
const devQueueQuery = `query($owner:String!,$name:String!,$branch:String!) {
  repository(owner:$owner, name:$name) {
    mergeQueue(branch:$branch) {
      entries(first:100) {
        pageInfo { hasNextPage }
        nodes { pullRequest { number } }
      }
    }
    pullRequests(states:OPEN, baseRefName:$branch, first:100, orderBy:{field:UPDATED_AT, direction:DESC}) {
      pageInfo { hasNextPage }
      nodes {
        number
        isDraft
        mergeStateStatus
        timelineItems(itemTypes:[REMOVED_FROM_MERGE_QUEUE_EVENT], first:10) {
          nodes { ... on RemovedFromMergeQueueEvent { createdAt } }
        }
      }
    }
  }
}`

const devTipQuery = `query($owner:String!,$name:String!) {
  repository(owner:$owner, name:$name) {
    ref(qualifiedName:"refs/heads/dev") {
      target { ... on Commit { oid committedDate } }
    }
  }
}`

// ghQuery is one read-only GitHub query. workgh refuses anything else.
type ghQuery func(ctx context.Context, doc string, vars map[string]any) ([]byte, error)

// queueSnap is the previous check's queue, so a later check can name what
// left it. It is this probe's memory, not a write to the sprint store.
type queueSnap struct {
	At      time.Time `json:"at"`
	Entries []string  `json:"entries"`
}

type ghMsg struct {
	Message string `json:"message"`
}

type ghPR struct {
	Number           int    `json:"number"`
	IsDraft          bool   `json:"isDraft"`
	MergeStateStatus string `json:"mergeStateStatus"`
	TimelineItems    struct {
		Nodes []struct {
			CreatedAt time.Time `json:"createdAt"`
		} `json:"nodes"`
	} `json:"timelineItems"`
}

func (a *app) ghQuery(ctx context.Context, doc string, vars map[string]any) ([]byte, error) {
	return workgh.GhQuery("gh")(ctx, doc, vars)
}

func devQueueVars(owner, name string) map[string]any {
	return map[string]any{"owner": owner, "name": name, "branch": devQueueBranch}
}

// readDevMergeQueue measures the queue. Thrown-out names only pull requests
// that left, or were removed, since the snapshot of the previous check; the
// first check has no previous one and names none. A merged pull request left
// the queue by landing, which is not a throw.
func readDevMergeQueue(ctx context.Context, gh ghQuery, owner, name, path string, now time.Time) (seatcheck.QueueM, error) {
	body, err := gh(ctx, devQueueQuery, devQueueVars(owner, name))
	if err != nil {
		return seatcheck.QueueM{}, err
	}
	entries, prs, err := parseDevQueue(body)
	if err != nil {
		return seatcheck.QueueM{}, err
	}
	prev, hasPrev, err := loadQueueSnap(path)
	if err != nil {
		return seatcheck.QueueM{}, err
	}
	thrown, err := thrownOut(ctx, gh, owner, name, entries, prs, prev, hasPrev)
	if err != nil {
		return seatcheck.QueueM{}, err
	}
	if err := saveQueueSnap(path, queueSnap{At: now, Entries: entries}); err != nil {
		return seatcheck.QueueM{}, err
	}
	return seatcheck.QueueM{Entries: entries, Green: greenCount(prs), ThrownOut: thrown}, nil
}

type ghQueueBody struct {
	Data struct {
		Repository struct {
			MergeQueue *struct {
				Entries struct {
					PageInfo ghPage         `json:"pageInfo"`
					Nodes    []ghQueueEntry `json:"nodes"`
				} `json:"entries"`
			} `json:"mergeQueue"`
			PullRequests struct {
				PageInfo ghPage `json:"pageInfo"`
				Nodes    []ghPR `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	} `json:"data"`
	Errors []ghMsg `json:"errors"`
}

type ghPage struct {
	HasNextPage bool `json:"hasNextPage"`
}

func parseDevQueue(body []byte) ([]string, []ghPR, error) {
	var wrap ghQueueBody
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, nil, err
	}
	if err := ghErrs(wrap.Errors); err != nil {
		return nil, nil, err
	}
	repo := wrap.Data.Repository
	if repo.MergeQueue == nil {
		return nil, nil, fmt.Errorf("dev merge queue: dev has no merge queue")
	}
	if repo.MergeQueue.Entries.PageInfo.HasNextPage {
		return nil, nil, fmt.Errorf("dev merge queue: more than 100 entries")
	}
	if repo.PullRequests.PageInfo.HasNextPage {
		return nil, nil, fmt.Errorf("dev merge queue: more than 100 open pull requests")
	}
	return queueNumbers(repo.MergeQueue.Entries.Nodes), repo.PullRequests.Nodes, nil
}

type ghQueueEntry struct {
	PullRequest struct {
		Number int `json:"number"`
	} `json:"pullRequest"`
}

func queueNumbers(nodes []ghQueueEntry) []string {
	var entries []string
	seen := map[string]bool{}
	for _, n := range nodes {
		s := strconv.Itoa(n.PullRequest.Number)
		if n.PullRequest.Number == 0 || seen[s] {
			continue
		}
		seen[s] = true
		entries = append(entries, s)
	}
	return entries
}

func ghErrs(errs []ghMsg) error {
	if len(errs) == 0 || errs[0].Message == "" {
		return nil
	}
	return fmt.Errorf("github: %s", errs[0].Message)
}

func greenCount(prs []ghPR) int {
	n := 0
	for _, pr := range prs {
		if !pr.IsDraft && pr.MergeStateStatus == "CLEAN" {
			n++
		}
	}
	return n
}

func thrownOut(ctx context.Context, gh ghQuery, owner, name string, entries []string, prs []ghPR, prev queueSnap, hasPrev bool) ([]string, error) {
	if !hasPrev {
		return nil, nil
	}
	var set throwSet
	gone := goneFrom(prev.Entries, entries)
	if len(gone) > 0 {
		states, err := prStates(ctx, gh, owner, name, gone)
		if err != nil {
			return nil, err
		}
		for _, n := range gone {
			if states[n] == "OPEN" || states[n] == "CLOSED" {
				set.add(n)
			}
		}
	}
	for _, pr := range prs {
		if removedAfter(pr, prev.At) {
			set.add(strconv.Itoa(pr.Number))
		}
	}
	return set.list, nil
}

func removedAfter(pr ghPR, at time.Time) bool {
	for _, ev := range pr.TimelineItems.Nodes {
		if ev.CreatedAt.After(at) {
			return true
		}
	}
	return false
}

type throwSet struct {
	list []string
	seen map[string]bool
}

func (s *throwSet) add(n string) {
	if n == "" || n == "0" || s.seen[n] {
		return
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	s.seen[n] = true
	s.list = append(s.list, n)
}

func goneFrom(prev, entries []string) []string {
	in := map[string]bool{}
	for _, e := range entries {
		in[e] = true
	}
	var gone []string
	for _, e := range prev {
		if e != "" && !in[e] {
			gone = append(gone, e)
		}
	}
	return gone
}

func prStates(ctx context.Context, gh ghQuery, owner, name string, nums []string) (map[string]string, error) {
	doc, err := prStateQuery(owner, name, nums)
	if err != nil {
		return nil, err
	}
	body, err := gh(ctx, doc, nil)
	if err != nil {
		return nil, err
	}
	return parsePRStates(body)
}

func prStateQuery(owner, name string, nums []string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, `query { repository(owner:%q, name:%q) {`, owner, name)
	for _, n := range nums {
		if _, err := strconv.Atoi(n); err != nil {
			return "", fmt.Errorf("pull request %q is not a number", n)
		}
		fmt.Fprintf(&b, " n%s: pullRequest(number:%s) { number state }", n, n)
	}
	b.WriteString(" } }")
	return b.String(), nil
}

func parsePRStates(body []byte) (map[string]string, error) {
	var wrap struct {
		Data struct {
			Repository map[string]*struct {
				Number int    `json:"number"`
				State  string `json:"state"`
			} `json:"repository"`
		} `json:"data"`
		Errors []ghMsg `json:"errors"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, err
	}
	if err := ghErrs(wrap.Errors); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, pr := range wrap.Data.Repository {
		if pr == nil || pr.Number == 0 {
			continue
		}
		out[strconv.Itoa(pr.Number)] = pr.State
	}
	return out, nil
}

func seatQueuePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nova-sprint", "seatcheck-dev-queue.json"), nil
}

func loadQueueSnap(path string) (queueSnap, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return queueSnap{}, false, nil
	}
	if err != nil {
		return queueSnap{}, false, err
	}
	var s queueSnap
	if err := json.Unmarshal(b, &s); err != nil {
		return queueSnap{}, false, fmt.Errorf("remembering the dev merge queue: %w", err)
	}
	return s, true, nil
}

func saveQueueSnap(path string, s queueSnap) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("remembering the dev merge queue: %w", err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("remembering the dev merge queue: %w", err)
	}
	return nil
}

// readMachineVersions asks nova-update version on each machine and reads dev's
// tip as the same version identity (buildinfo.Resolve's vcs stamp of that commit).
func readMachineVersions(ctx context.Context, machines []string, owner, name string, gh ghQuery, ssh func(context.Context, string) (string, error)) (seatcheck.VersionsM, error) {
	body, err := gh(ctx, devTipQuery, map[string]any{"owner": owner, "name": name})
	if err != nil {
		return seatcheck.VersionsM{}, err
	}
	dev, err := parseDevTip(body)
	if err != nil {
		return seatcheck.VersionsM{}, err
	}
	out := make([]seatcheck.MachineVersionM, 0, len(machines))
	for _, m := range machines {
		out = append(out, seatcheck.MachineVersionM{Machine: m, Version: askedVersion(ctx, ssh, m)})
	}
	return seatcheck.VersionsM{Dev: dev, Machines: out}, nil
}

func parseDevTip(body []byte) (string, error) {
	var wrap struct {
		Data struct {
			Repository struct {
				Ref *struct {
					Target struct {
						Oid           string    `json:"oid"`
						CommittedDate time.Time `json:"committedDate"`
					} `json:"target"`
				} `json:"ref"`
			} `json:"repository"`
		} `json:"data"`
		Errors []ghMsg `json:"errors"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return "", err
	}
	if err := ghErrs(wrap.Errors); err != nil {
		return "", err
	}
	ref := wrap.Data.Repository.Ref
	if ref == nil || ref.Target.Oid == "" || ref.Target.CommittedDate.IsZero() {
		return "", fmt.Errorf("dev tip: no commit")
	}
	return devStamp(ref.Target.CommittedDate, ref.Target.Oid), nil
}

// devStamp is the version identity nova-update version prints for a clean
// build of that commit: buildinfo.Resolve's vcs stamp, UTC time then 12 hex.
func devStamp(at time.Time, sha string) string {
	if len(sha) > 12 {
		sha = sha[:12]
	}
	return at.UTC().Format("20060102150405") + "-" + sha
}

func askedVersion(ctx context.Context, ssh func(context.Context, string) (string, error), machine string) string {
	if !okMachine(machine) {
		return "unread"
	}
	out, err := ssh(ctx, machine)
	if err != nil {
		return "unread"
	}
	id, err := versionIdentity(out)
	if err != nil {
		return "unread"
	}
	return id
}

// versionIdentity is field two of a nova-update version line, the field a
// comparison reads (buildinfo.Parse).
func versionIdentity(out string) (string, error) {
	f, ok := buildinfo.Parse(out)
	if !ok || f.Version == "" {
		return "", fmt.Errorf("nova-update version: not a version line")
	}
	return f.Version, nil
}

// okMachine is a name safe to pass as an ssh destination: not empty, not a
// flag, and only the characters a host name here uses.
func okMachine(machine string) bool {
	if machine == "" || machine[0] == '-' {
		return false
	}
	for _, r := range machine {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '@':
		default:
			return false
		}
	}
	return true
}

// novaUpdateVersion is `nova-update version` on the machine, through the one
// remote this tree already runs (release.ExecSSH). The answer is that verb's
// own line.
func (a *app) novaUpdateVersion(ctx context.Context, machine string) (string, error) {
	if !okMachine(machine) {
		return "", fmt.Errorf("machine %q is not a destination", machine)
	}
	out, err := (release.ExecSSH{Path: "ssh"}).Run(ctx, machine, []string{"nova-update", "version"})
	if err != nil {
		return "", fmt.Errorf("nova-update version on %s: %w", machine, err)
	}
	return out, nil
}

// cmdMachinery is `nova-sprint machinery`: the seat check on demand, read-only
// of the sprint; exit 1 when anything is DOWN. The queue snapshot is the
// probe's own memory of the previous check, not a store write.
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
