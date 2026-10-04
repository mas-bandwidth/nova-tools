package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcheck"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
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
// launchdLoaded, queue and versions: the dashboard, the bus, the agents, the
// merge queue and the versions say not measured.
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
	// queue reads the merge queue of the branch QueueEnv names, versions the
	// branch tip's version and each machine's installed one, and mark keeps
	// the previous check's time, from which a pull request thrown out of the
	// queue is new. A served check runs none of them.
	queue    queueReader
	versions versionReader
	mark     queueMark
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
		queue:    ghQueue{query: workgh.GhQuery(workgh.DefaultProgram())},
		versions: fleetVersions{query: workgh.GhQuery(workgh.DefaultProgram()), run: runVersionCommand},
		mark:     cacheMark{dir: seatCheckDir},
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

	// 10. the merge queue, 11. the installed versions, where QueueEnv names
	// the repository and branch the sprint lands into
	if spec := a.getenv(QueueEnv); spec != "" {
		a.measureQueue(ctx, o, &m, spec, self, now)
	}
	return seatcheck.Judge(m, now)
}

// measureQueue measures the merge queue of the branch spec names and each
// fleet machine's installed nova-tools against that branch's tip
// (docs/SPEC-SPRINT.md, "The seat check", rows 10 and 11). A pull request
// thrown out of the queue is new when GitHub records its removal after the
// previous check (the mark; QueueFirstWindow before any), and the mark moves
// to now only once the queue was read. A served check reads neither: its
// rows say not measured.
func (a *app) measureQueue(ctx context.Context, o outside, m *seatcheck.Measures, spec string, self bool, now time.Time) {
	repo, branch, ok := splitQueueSpec(spec)
	m.Queue = seatcheck.QueueM{Repo: spec, Queued: []int{}, Green: []int{}, Thrown: []seatcheck.ThrownM{}}
	m.Versions = seatcheck.VersionsM{Machines: []seatcheck.MachineVersionM{}}
	if !ok {
		m.Queue.Err = QueueEnv + " wants <owner>/<repo>:<branch>, got " + spec
		m.Versions.Err = m.Queue.Err
		return
	}
	m.Queue.Repo, m.Queue.Branch = repo, branch
	if self {
		return
	}
	since, seen := o.mark.Last(repo, branch)
	if !seen {
		since = now.Add(-QueueFirstWindow)
	}
	read, err := o.queue.MergeQueue(ctx, repo, branch, since)
	if err != nil {
		m.Queue.Err = err.Error()
	} else {
		m.Queue.Queued, m.Queue.Green = read.Queued, read.Green
		for _, r := range read.Removed {
			if r.At.After(since) {
				m.Queue.Thrown = append(m.Queue.Thrown, seatcheck.ThrownM{PR: r.PR, Age: now.Sub(r.At), Reason: r.Reason})
			}
		}
		_ = o.mark.Set(repo, branch, now) // ignored: a mark not kept shows the same removals again at the next check, never hides one
	}

	dev, sha, err := o.versions.Tip(ctx, repo, branch)
	if err != nil {
		m.Versions.Err = err.Error()
		return
	}
	m.Versions.Dev, m.Versions.DevSHA = dev, sha
	var machines []string
	for _, mm := range m.Fleet {
		if mm.Status != sprint.Down && !strings.HasPrefix(mm.Name, "friend.") {
			machines = append(machines, mm.Name) // a machine down is the fleet line's; it answers nothing
		}
	}
	answers := make([]seatcheck.MachineVersionM, len(machines))
	var wg sync.WaitGroup
	for i, name := range machines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := o.versions.Installed(ctx, name, name == m.Host)
			answers[i] = seatcheck.MachineVersionM{Name: name, Version: v}
			if err != nil {
				answers[i] = seatcheck.MachineVersionM{Name: name, Err: err.Error()}
			}
		}()
	}
	wg.Wait()
	m.Versions.Machines = answers
}

// cmdMachinery is `nova-sprint machinery`: the seat check on demand, writing
// nothing but the merge queue's mark; exit 1 when anything is DOWN.
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

// QueueEnv names the repository and the branch the sprint lands into,
// <owner>/<repo>:<branch> (nova-tools' own repository and dev): the
// seat check reads that branch's merge queue and judges each fleet machine's
// installed nova-tools against its tip. Unset, neither row is printed: a
// sprint whose branch has no queue has none to judge.
const QueueEnv = "NOVA_SPRINT_MERGE_QUEUE"

// QueueFirstWindow is how far back a removal from the queue counts as new at
// a check with no previous one.
const QueueFirstWindow = 24 * time.Hour

// queueTimeout and versionTimeout bound one GitHub read and one machine's
// answer: the check is the first thing a seat reads, and never waits long.
const (
	queueTimeout   = 30 * time.Second
	versionTimeout = 15 * time.Second
)

func splitQueueSpec(spec string) (repo, branch string, ok bool) {
	repo, branch, ok = strings.Cut(spec, ":")
	owner, name, slash := strings.Cut(repo, "/")
	return repo, branch, ok && slash && owner != "" && name != "" && !strings.Contains(name, "/") && branch != ""
}

// queueReader reads a branch's merge queue on GitHub.
type queueReader interface {
	// MergeQueue reads the pull requests in the queue, the open pull
	// requests into the branch that are not drafts, have green checks and are
	// not in it, and the removals from the queue recorded on the pull
	// requests into the branch updated since since.
	MergeQueue(ctx context.Context, repo, branch string, since time.Time) (queueRead, error)
}

// queueRead is what MergeQueue read.
type queueRead struct {
	Queued, Green []int
	Removed       []queueRemoval
}

// queueRemoval is one pull request removed from the queue, when, and the
// reason GitHub records.
type queueRemoval struct {
	PR     int
	At     time.Time
	Reason string
}

// versionReader reads nova-tools versions as a build states them.
type versionReader interface {
	// Tip is the branch tip's version identity, as a build of it states it
	// (internal/buildinfo: <utc commit time>-<12 hex>), and its full sha.
	Tip(ctx context.Context, repo, branch string) (identity, sha string, err error)
	// Installed is the version identity nova-update's version verb answers
	// on the machine (here: this one).
	Installed(ctx context.Context, machine string, here bool) (string, error)
}

// queueMark keeps the time of the previous check that read the queue.
type queueMark interface {
	Last(repo, branch string) (time.Time, bool)
	Set(repo, branch string, at time.Time) error
}

// queueDoc is the one GitHub read of the queue: its entries, the open green
// pull requests, and the removals on the pull requests touched since the mark.
const queueDoc = `query($owner: String!, $name: String!, $branch: String!, $green: String!, $touched: String!) {
  repository(owner: $owner, name: $name) {
    mergeQueue(branch: $branch) { entries(first: 100) { nodes { pullRequest { number } } } }
  }
  green: search(query: $green, type: ISSUE, first: 100) { nodes { ... on PullRequest { number isInMergeQueue } } }
  touched: search(query: $touched, type: ISSUE, first: 100) {
    nodes { ... on PullRequest { number timelineItems(itemTypes: [REMOVED_FROM_MERGE_QUEUE_EVENT], last: 10) {
      nodes { ... on RemovedFromMergeQueueEvent { createdAt reason } } } } }
  }
}`

// tipDoc reads a branch tip's sha and commit time.
const tipDoc = `query($owner: String!, $name: String!, $ref: String!) {
  repository(owner: $owner, name: $name) { ref(qualifiedName: $ref) { target { ... on Commit { oid committedDate } } } }
}`

// ghQueue is queueReader over GitHub's GraphQL API (workgh.Query: gh api
// graphql, read-only; a test gives canned answers).
type ghQueue struct{ query workgh.Query }

func (g ghQueue) MergeQueue(ctx context.Context, repo, branch string, since time.Time) (queueRead, error) {
	ctx, cancel := context.WithTimeout(ctx, queueTimeout)
	defer cancel()
	owner, name, _ := strings.Cut(repo, "/")
	pr := "repo:" + repo + " is:pr base:" + branch
	body, err := g.query(ctx, queueDoc, map[string]any{"owner": owner, "name": name, "branch": branch,
		"green":   pr + " is:open draft:false status:success",
		"touched": pr + " updated:>=" + since.UTC().Format(time.RFC3339)})
	if err != nil {
		return queueRead{}, err
	}
	var v struct {
		Data struct {
			Repository struct {
				MergeQueue *struct {
					Entries struct {
						Nodes []struct {
							PullRequest struct{ Number int } `json:"pullRequest"`
						}
					}
				} `json:"mergeQueue"`
			}
			Green struct {
				Nodes []struct {
					Number         int
					IsInMergeQueue bool `json:"isInMergeQueue"`
				}
			}
			Touched struct {
				Nodes []struct {
					Number        int
					TimelineItems struct {
						Nodes []struct {
							CreatedAt time.Time `json:"createdAt"`
							Reason    string
						}
					} `json:"timelineItems"`
				}
			}
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return queueRead{}, fmt.Errorf("the merge queue's answer is not GitHub's: %v", err)
	}
	if len(v.Errors) > 0 {
		return queueRead{}, fmt.Errorf("GitHub: %s", v.Errors[0].Message)
	}
	if v.Data.Repository.MergeQueue == nil {
		return queueRead{}, fmt.Errorf("%s has no merge queue on %s", repo, branch)
	}
	r := queueRead{Queued: []int{}, Green: []int{}}
	for _, e := range v.Data.Repository.MergeQueue.Entries.Nodes {
		r.Queued = append(r.Queued, e.PullRequest.Number)
	}
	for _, p := range v.Data.Green.Nodes {
		if p.Number > 0 && !p.IsInMergeQueue {
			r.Green = append(r.Green, p.Number)
		}
	}
	sort.Ints(r.Green)
	for _, p := range v.Data.Touched.Nodes {
		for _, e := range p.TimelineItems.Nodes {
			r.Removed = append(r.Removed, queueRemoval{PR: p.Number, At: e.CreatedAt, Reason: e.Reason})
		}
	}
	sort.Slice(r.Removed, func(i, j int) bool { return r.Removed[i].At.Before(r.Removed[j].At) })
	return r, nil
}

// fleetVersions is versionReader: the tip from GitHub (workgh.Query), a
// machine's answer from nova-update's version verb, run here or over ssh
// (run; a test gives its answers).
type fleetVersions struct {
	query workgh.Query
	run   func(ctx context.Context, argv []string) (string, error)
}

func (f fleetVersions) Tip(ctx context.Context, repo, branch string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, queueTimeout)
	defer cancel()
	owner, name, _ := strings.Cut(repo, "/")
	body, err := f.query(ctx, tipDoc, map[string]any{"owner": owner, "name": name, "ref": "refs/heads/" + branch})
	if err != nil {
		return "", "", err
	}
	var v struct {
		Data struct {
			Repository struct {
				Ref *struct {
					Target struct {
						OID           string    `json:"oid"`
						CommittedDate time.Time `json:"committedDate"`
					}
				}
			}
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", "", fmt.Errorf("the tip's answer is not GitHub's: %v", err)
	}
	if len(v.Errors) > 0 {
		return "", "", fmt.Errorf("GitHub: %s", v.Errors[0].Message)
	}
	t := v.Data.Repository.Ref
	if t == nil || len(t.Target.OID) < 12 || t.Target.CommittedDate.IsZero() {
		return "", "", fmt.Errorf("%s has no branch %s", repo, branch)
	}
	// the identity a vcs build of the tip states (buildinfo.Resolve)
	return t.Target.CommittedDate.UTC().Format("20060102150405") + "-" + t.Target.OID[:12], t.Target.OID, nil
}

func (f fleetVersions) Installed(ctx context.Context, machine string, here bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	argv := []string{"nova-update", "version"}
	if !here {
		argv = remoteVersionArgv(machine)
	}
	out, err := f.run(ctx, argv)
	if err != nil {
		return "", err
	}
	v, ok := buildinfo.Parse(out)
	if !ok {
		return "", fmt.Errorf("nova-update version answered %q, not a version line", oneline.Cap(strings.TrimSpace(out), 120))
	}
	return v.Version, nil
}

// remoteVersionArgv asks a fleet machine for its installed version, never
// interactively.
func remoteVersionArgv(machine string) []string {
	return []string{"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", machine, seatcheck.InstalledVersionCommand}
}

// runVersionCommand runs one version command and returns its output; a
// command reaching another machine is refused under the test guard.
func runVersionCommand(ctx context.Context, argv []string) (string, error) {
	kind := subproc.Tool
	if argv[0] == "ssh" {
		testguard.RefuseHosts(argv[0], argv[1:]...)
		kind = subproc.SSH
	}
	cmd, cancel := subproc.Command(ctx, kind, argv[0], argv[1:]...)
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%s: %s", err, oneline.Cap(strings.TrimSpace(string(ee.Stderr)), 200))
		}
		return "", err
	}
	return string(out), nil
}

// cacheMark is queueMark as a file per repository and branch under dir (the
// user's cache directory's nova-sprint/seat-check), holding one RFC 3339 time.
type cacheMark struct{ dir func() (string, error) }

func (c cacheMark) path(repo, branch string) (string, error) {
	dir, err := c.dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "queue-"+strings.ReplaceAll(repo, "/", "-")+"-"+strings.ReplaceAll(branch, "/", "-")), nil
}

func (c cacheMark) Last(repo, branch string) (time.Time, bool) {
	p, err := c.path(repo, branch)
	if err != nil {
		return time.Time{}, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	return t, err == nil
}

func (c cacheMark) Set(repo, branch string, at time.Time) error {
	p, err := c.path(repo, branch)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(at.UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
}

// seatCheckDir is where the check keeps its marks: nova-sprint/seat-check in
// the user's cache directory, beside land's clones (defaultLandRoot).
func seatCheckDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "nova-sprint", "seat-check"), nil
}
