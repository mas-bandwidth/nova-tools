package pulse

// status --html is the fleet status page as a verb: it folds the old status-page.sh into
// nova-pulse and fixes the correctness bugs while doing it. The script counted a live slot
// from log age -- a harness-output.log under fifteen minutes old with no RESULT.md -- which
// the bench-hygiene incident showed is wrong: a card can be silently dead with a
// fresh-looking log, and a long card's log is older than fifteen minutes while it is still
// alive. Live here is the count of running card processes per bench, the authoritative
// liveness bench-hygiene.sh's live_slot uses, and the readers are injected so a test never
// starts ssh. Both outputs are counts and numbers only: no card id, branch name or label
// ever reaches a public page.
//
// The second pass over this file came from an ADOPTION ATTEMPT: a non-author put the verb
// in front of the script on the Studio and refused to install it over twelve gaps. Four
// bench rows matching the script byte for byte is not the page, and the gaps were the whole
// difference between a verb that renders and a verb that replaces something. Every one of
// them is closed here, and the rule they share is the DOWN rule: a number nobody measured
// is a DASH, never a zero.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// BenchReading is one bench's numbers for the page: the live cards counted from running
// card processes, the capacity the allowed arithmetic folds, and the hygiene actions the
// same round trip counted.
type BenchReading struct {
	Name    string
	Live    int
	Cores   int
	Load    int
	FreeGB  int
	MemGB   int
	Allowed int
	Hygiene int // reaps and deletions on this bench since the window opened
	// Down is the bench that did not answer. It is NOT a bench with nothing to do, and the
	// difference is what the 2026-09-17 pit stop cost: a row of zeros reads as an idle
	// bench, so a fleet nobody could see looked like a fleet with nothing to do. The page
	// says DOWN, the metrics row writes a dash for its disk rather than a number the bench
	// never gave, and the STATUS HTML line counts it.
	Down bool
	// Note says WHY, when the bench answered well enough to tell us. A bench whose home is
	// not there answers every other question perfectly and then reports no disk, which read
	// as "out of disk, allowed 0" on every row of a page that was otherwise right.
	Note string
}

// SelfReading is the host running the verb, which is a bench too. The Studio drowned at
// load 147 on 2026-09-17 and no page showed it, because the page only ever read the
// machines it ssh'd to.
type SelfReading struct {
	Name      string
	CIRunners int
	Cores     int
	Load      int
	FreeGB    int
	Orphans   int
	Loops     []LoopCount
	Unknown   bool // the host could not be read; it renders like a DOWN bench
}

// LoopCount is one long-running loop the caller named and how many of it are up. The
// patterns come from --loop and never from a name baked into this tool: a verb carrying
// `harvest-loop.sh` in its source would freeze the scripts it exists to retire.
type LoopCount struct {
	Label string
	N     int
}

// PublishFile is one file shipped to where the page is served.
type PublishFile struct {
	Name string
	Body []byte
}

// FleetReader reads every bench's numbers in file order. The production reader reads them
// over ssh with ps/pgrep liveness; a test injects a fake returning per-bench process counts
// and disk/mem, so no test starts ssh.
type FleetReader func(benches []FleetBench, now time.Time) []BenchReading

// SelfReader reads the host the verb runs on. Injected for the same reason.
type SelfReader func(name string, loops []string, now time.Time) SelfReading

// Publisher ships the page and its series to where they are served. Injected: a test that
// published for real would need a host.
type Publisher func(dest string, files []PublishFile, timeout time.Duration) error

// StatusHTMLInput is everything `status --html` needs, apart from flag parsing, so a test
// can drive it against fake directories and injected readers.
type StatusHTMLInput struct {
	HTML     string   // the local file to write
	Benches  string   // the fleet file: name, ssh target, home, mac
	Queue    string   // the queue whose depth, fill log and REPO the page reads
	SSH      string   // the ssh program; empty is "ssh"
	Publish  string   // host:dir to ship the page and the series to; empty ships nothing
	GhConfig string   // GH_CONFIG_DIR for the gh children; empty leaves the caller's own
	DayStart string   // when the merged counter resets, as HH:MMZ; empty is 02:00Z
	Branch   string   // the branch whose merge queue and tip the page shows; empty is dev
	Self     string   // the name of the host running the verb; empty omits the row
	Loops    []string // label=pattern pairs counted on the self host
	// Certs is the certificates file `fleet certify` appends to. The page gains one
	// certification cell per bench read from it and from NOTHING else: no ssh, no forge, no
	// registry, because a page that guesses what it did not measure is the DOWN row again.
	// Empty leaves every cell a dash, which is "nobody asked" and not "nothing is certified".
	Certs      string
	CertMaxAge time.Duration // how long a certificate stands; 0 is fleet.DefaultMaxAge
	Timeout    time.Duration
	Reader     FleetReader
	SelfRead   SelfReader
	Ship       Publisher
	Now        func() time.Time
	Stdout     io.Writer
	Stderr     io.Writer
}

// defaultDayStart is when the day's merged counter resets. It is 02:00Z because
// INSTALL-fleet.md's rate_counter resets there and the script counted from there: a page
// that disagrees with every other instrument for two hours a day is a page nobody trusts
// at 01:00Z.
const defaultDayStart = "02:00Z"

// defaultBranch is the branch whose merge queue and tip the page shows.
const defaultBranch = "dev"

// count is a number that may not have been measured. The whole class of bug this file has
// been through twice -- the DOWN row, the missing REPO -- is a zero standing in for "we
// did not ask", so an unmeasured number prints a dash and says so.
type count struct {
	n     int
	known bool
}

func known(n int) count { return count{n: n, known: true} }

func (c count) String() string {
	if !c.known {
		return "-"
	}
	return strconv.Itoa(c.n)
}

// StatusHTML writes the fleet page to --html, appends one seven-column metrics.tsv row
// beside it, ships both where --publish says, and prints the one STATUS HTML line.
func StatusHTML(in StatusHTMLInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.Timeout <= 0 {
		in.Timeout = fleetDefaultTimeout
	}
	if strings.TrimSpace(in.Branch) == "" {
		in.Branch = defaultBranch
	}
	for _, r := range []struct{ v, name, wants string }{
		{in.HTML, "html", "the local file path to write the fleet page to"},
		{in.Benches, "benches", "the fleet file: name, ssh target, home, mac one per line"},
	} {
		if strings.TrimSpace(r.v) == "" {
			return refusal(in.Stderr, "STATUS", fmt.Errorf("--%s is required; it wants %s; refusing to guess", r.name, r.wants))
		}
	}

	// Everything the flags can get wrong is refused BEFORE a bench is read: a minute of ssh
	// spent on a run that cannot finish is a minute the fleet did not have.
	dayStart := strings.TrimSpace(in.DayStart)
	if dayStart == "" {
		dayStart = defaultDayStart
	}
	resetHour, resetMin, derr := parseDayStart(dayStart)
	if derr != nil {
		return refusal(in.Stderr, "STATUS", fmt.Errorf(
			"--day-start %s: %s; it wants the UTC time the day's merged counter resets, as in --day-start 02:00Z (the rate_counter's own boundary)",
			oneline.Field(dayStart), oneline.Err(derr)))
	}
	loops, lerr := parseLoops(in.Loops)
	if lerr != nil {
		return refusal(in.Stderr, "STATUS", fmt.Errorf(
			"--loop %s; it wants <label>=<pattern>, as in --loop harvest=harvest-loop, and may be repeated", oneline.Err(lerr)))
	}
	if len(loops) > 0 && strings.TrimSpace(in.Self) == "" {
		return refusal(in.Stderr, "STATUS", fmt.Errorf(
			"--loop was given without --self; the loops are counted on the host running the verb, so name it: --self studio"))
	}
	if strings.TrimSpace(in.Publish) != "" {
		if _, _, perr := splitPublish(in.Publish); perr != nil {
			return refusal(in.Stderr, "STATUS", fmt.Errorf(
				"--publish %s: %s; it wants <host>:<dir>, as in --publish space:status", oneline.Field(in.Publish), oneline.Err(perr)))
		}
	}

	small, err := readFleetBenches(in.Benches)
	if err != nil {
		return refusal(in.Stderr, "STATUS", fmt.Errorf("--benches %s: %s", oneline.Field(in.Benches), oneline.Err(err)))
	}
	benches := make([]FleetBench, len(small))
	for i, b := range small {
		benches[i] = FleetBench{Name: b.Name, SSH: b.Target, Home: b.Home, MAC: b.Mac}
	}

	now := in.Now()
	hourStart := now.Add(-time.Hour)
	reader := in.Reader
	if reader == nil {
		reader = func(list []FleetBench, at time.Time) []BenchReading {
			return readFleetOverSSH(list, in.SSH, in.Timeout, at.Add(-time.Hour))
		}
	}
	// Reading the fleet is the slow step: every bench over ssh, bounded by --timeout. A
	// verb that takes seconds says what it is doing while it takes them, in the same units
	// the flag accepts.
	fmt.Fprintf(in.Stderr, "STATUS reading %d benches over ssh, up to %s each\n", len(benches), in.Timeout)
	readings := reader(benches, now)

	liveTotal, downTotal, hygiene := 0, 0, 0
	var freeDisk []string
	for _, r := range readings {
		if r.Down {
			// A bench that did not answer contributes no live cards and no disk figure:
			// the series must not carry a number nobody measured.
			downTotal++
			freeDisk = append(freeDisk, "-")
			continue
		}
		liveTotal += r.Live
		hygiene += r.Hygiene
		freeDisk = append(freeDisk, strconv.Itoa(r.FreeGB))
	}

	var self *SelfReading
	if name := strings.TrimSpace(in.Self); name != "" {
		selfRead := in.SelfRead
		if selfRead == nil {
			selfRead = readSelf
		}
		fmt.Fprintf(in.Stderr, "STATUS reading the host %s\n", oneline.Field(name))
		s := selfRead(name, in.Loops, now)
		s.Name = name
		self = &s
	}

	// The certification column. The file is read ONCE, here, and a file that will not read
	// is said out loud rather than becoming a page full of dashes nobody can explain.
	var certs []fleet.Certificate
	certsRead := false
	if path := strings.TrimSpace(in.Certs); path != "" {
		rows, cerr := fleet.ReadCertificates(path)
		if cerr != nil {
			fmt.Fprintf(in.Stderr, "STATUS NOTE certs=%s unread=%s\n", oneline.Field(path), oneline.Quote(oneline.Err(cerr)))
		} else {
			certs, certsRead = rows, true
		}
	}

	queueDepth := countCards(in.Queue, "pending")
	ready := countCards(in.Queue, "ready")
	launched := countFillLog(in.Queue, "attempt=")
	refusedByCapacity := countFillLog(in.Queue, "REFUSED")

	// The forge rows. With no REPO there is nobody to ask, and "nobody asked" is a dash --
	// the same rule as the DOWN row, in the one line a reader uses to decide whether the
	// day is moving.
	page := pageData{
		now: now, dayStart: dayStart, branch: in.Branch,
		queueDepth: queueDepth, ready: ready, launched: launched, refused: refusedByCapacity,
		hygiene: hygiene, readings: readings, self: self,
		merged: count{}, opened: count{},
		certs: certificationOf(certs, certsRead, benches, self, now, in.CertMaxAge),
	}
	if repo := firstLine(filepath.Join(in.Queue, "REPO")); repo != "" {
		env := ghEnv(os.Environ(), in.GhConfig)
		// PRs only: the page shows no issue count, so a second round trip for issues is
		// bought for nothing. --limit is the script's 500, because gh's own default is 30
		// and a count that caps at 30 flatlines the series for the rest of the day.
		prs := readGhPRs(repo, in.Timeout, env)
		dayStartAt := dayBoundary(now, resetHour, resetMin)
		o, _ := prsOpenedMerged(prs, hourStart, now)
		m, _ := prsOpenedMerged(prs, dayStartAt, now)
		page.opened, page.merged = known(o), known(m)
		page.mq, page.mqKnown = mergeQueueCounts(repo, in.Branch, in.Timeout, env)
		page.tipSHA, page.tipRun, page.tipKnown = branchTip(repo, in.Branch, in.Timeout, env)
	}

	body := fleetPage(page)
	if err := os.WriteFile(in.HTML, []byte(body), 0o644); err != nil {
		return refusal(in.Stderr, "STATUS", fmt.Errorf("could not write %s: %s", oneline.Field(in.HTML), oneline.Err(err)))
	}
	row := fmt.Sprintf("%s\t%d\t%d\t%s\t%s\t%d\t%s\n",
		now.UTC().Format(time.RFC3339), liveTotal, queueDepth, page.merged, page.opened, launched, strings.Join(freeDisk, ","))
	metrics := filepath.Join(filepath.Dir(in.HTML), "metrics.tsv")
	f, err := os.OpenFile(metrics, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return refusal(in.Stderr, "STATUS", fmt.Errorf("could not append %s: %s", oneline.Field(metrics), oneline.Err(err)))
	}
	if _, err := f.WriteString(row); err != nil {
		f.Close()
		return refusal(in.Stderr, "STATUS", fmt.Errorf("could not append %s: %s", oneline.Field(metrics), oneline.Err(err)))
	}
	f.Close()

	// Shipping is last and is loud when it fails: a page that quietly stopped shipping goes
	// stale while everybody keeps reading it, which is the worst failure this verb has.
	published := "-"
	if dest := strings.TrimSpace(in.Publish); dest != "" {
		ship := in.Ship
		if ship == nil {
			ship = func(d string, files []PublishFile, t time.Duration) error {
				return publishOverSSH(in.SSH, d, files, t)
			}
		}
		series, rerr := os.ReadFile(metrics)
		if rerr != nil {
			return refusal(in.Stderr, "STATUS", fmt.Errorf("could not read %s back to publish it: %s", oneline.Field(metrics), oneline.Err(rerr)))
		}
		fmt.Fprintf(in.Stderr, "STATUS publishing to %s\n", oneline.Field(dest))
		files := []PublishFile{
			{Name: "index.html", Body: []byte(body)},
			{Name: "metrics.tsv", Body: series},
		}
		if perr := ship(dest, files, in.Timeout); perr != nil {
			fmt.Fprintf(in.Stderr, "STATUS HTML publish failed to %s: %s; the page is written at %s and nothing was shipped\n",
				oneline.Field(dest), oneline.Err(perr), oneline.Field(in.HTML))
			return 3
		}
		published = dest
	}

	fmt.Fprintf(in.Stdout, "STATUS HTML wrote=%s live=%d queue=%d down=%d merged=%s published=%s\n",
		in.HTML, liveTotal, queueDepth, downTotal, page.merged, oneline.Field(published))
	return 0
}

// countFillLog counts the fill log's lines carrying a token; a missing log is zero.
func countFillLog(queue, token string) int {
	raw, err := os.ReadFile(filepath.Join(queue, "fill.log"))
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.Contains(l, token) {
			n++
		}
	}
	return n
}

// readFleetOverSSH reads the benches in parallel, each bounded by timeout, preserving file
// order. since is when the hygiene window opened.
func readFleetOverSSH(benches []FleetBench, ssh string, timeout time.Duration, since time.Time) []BenchReading {
	out := make([]BenchReading, len(benches))
	var wg sync.WaitGroup
	for i, b := range benches {
		wg.Add(1)
		go func(i int, b FleetBench) {
			defer wg.Done()
			out[i] = readOneBench(ssh, b, timeout, since)
		}(i, b)
	}
	wg.Wait()
	return out
}

// readOneBench runs the liveness script on one bench and folds its answer.
func readOneBench(ssh string, b FleetBench, timeout time.Duration, since time.Time) BenchReading {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := fleetSSH(ctx, ssh, b.SSH, fleetStatusScript(b.Home, since.UTC().Format(time.RFC3339)))
	if err != nil {
		return BenchReading{Name: b.Name, Down: true, Note: "no answer over ssh"}
	}
	if strings.Contains(out, "STATUSFLEET\tNOHOME") {
		return BenchReading{Name: b.Name, Down: true,
			Note: "home " + b.Home + " is not a directory on the bench; check the --benches home column"}
	}
	vals, ok := parseFleetStatus(out)
	if !ok {
		// An answer nobody can parse is no answer: it says DOWN, never a quiet zero.
		return BenchReading{Name: b.Name, Down: true, Note: "an answer that could not be read"}
	}
	return BenchReading{
		Name: b.Name, Live: vals[0], Cores: vals[1], Load: vals[2],
		FreeGB: vals[3], MemGB: vals[4], Allowed: vals[5], Hygiene: vals[6],
	}
}

// parseFleetStatus reads the one STATUSFLEET line the remote script prints. It accepts the
// seven-number line and the older six-number one, whose hygiene count is simply zero: a
// bench still running last week's script is a bench, not a DOWN row.
func parseFleetStatus(out string) ([7]int, bool) {
	var vals [7]int
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) == 2 && f[0] == "STATUSFLEET" && f[1] == "NOHOME" {
			return vals, false
		}
		if len(f) < 7 || len(f) > 8 || f[0] != "STATUSFLEET" {
			continue
		}
		var got [7]int
		good := true
		for i := 1; i < len(f); i++ {
			n, err := strconv.Atoi(f[i])
			if err != nil {
				good = false
				break
			}
			got[i-1] = n
		}
		if !good {
			continue
		}
		return got, true
	}
	return vals, false
}

// fleetStatusScript is the remote liveness, capacity and hygiene script, run through
// `ssh <target> bash -s` with HOME set to the bench's home column. A slot is live when a
// process's command line names one of its job directories (pgrep -f) or a process's cwd is
// under the slot -- the authoritative shape bench-hygiene.sh's live_slot uses.
//
// The pid walk happens ONCE, before the slot loop. It used to sit inside it: forty slots
// against a few thousand processes is a hundred thousand readlinks, which is why a live
// bench read DOWN at --timeout 10 and the whole page took 24.6 s against the script's 13.8.
// The list is capped for the same reason -- an unbounded pid walk on a busy bench is that
// stall by another name.
//
// Hygiene rides the same round trip. A second ssh per bench per minute for one integer is
// exactly the dumb waste the fleet audits for.
func fleetStatusScript(home, since string) string {
	return strings.Join([]string{
		"HOME=" + fleetQuote(home),
		"export HOME",
		"since=" + fleetQuote(since),
		// The home is checked FIRST. Without it df answers nothing, and a bench that
		// answered every other question reads as a bench out of disk with no capacity --
		// which is what a wrong home column looked like on every row of a real page.
		`if [ ! -d "$HOME" ]; then printf 'STATUSFLEET\tNOHOME\n'; exit 0; fi`,
		// One pass over /proc, before any slot is looked at.
		`cwds=""`,
		`if [ -d /proc ]; then`,
		`  for pid in $(ls /proc 2>/dev/null | grep -E '^[0-9]+$' | head -4000); do`,
		`    c=$(readlink /proc/$pid/cwd 2>/dev/null)`,
		`    [ -n "$c" ] && cwds="$cwds:$c/"`,
		`  done`,
		`fi`,
		"live=0",
		`for s in "$HOME"/rowan-working/tmp/*/ "$HOME"/rowan-swarm-root/*/; do`,
		`  s=${s%/}`,
		`  [ -d "$s" ] || continue`,
		`  found=0`,
		`  for j in "$s"/jobs/*/; do`,
		`    j=${j%/}`,
		`    [ -d "$j" ] || continue`,
		`    if command -v pgrep >/dev/null 2>&1 && pgrep -f -- "$j" >/dev/null 2>&1; then found=1; break; fi`,
		`  done`,
		`  if [ "$found" = 0 ]; then`,
		`    case "$cwds" in *":$s/"*) found=1;; esac`,
		`  fi`,
		`  [ "$found" = 1 ] && live=$((live+1))`,
		`done`,
		`cores=$(nproc 2>/dev/null || echo 1)`,
		`load=$(cut -d. -f1 /proc/loadavg 2>/dev/null || echo 0)`,
		`free=$(df -BG "$HOME" 2>/dev/null | awk 'NR==2{gsub("G","",$4); print $4}')`,
		`mem=$(awk '/MemAvailable/{printf "%d", $2/1048576}' /proc/meminfo 2>/dev/null)`,
		`hyg=$(awk -v s="$since" '$1 >= s && ($2=="delete-job" || $2=="delete-slot" || $2=="reap")' "$HOME/hygiene.log" 2>/dev/null | wc -l | tr -d ' ')`,
		`cores=${cores:-1}; load=${load:-0}; free=${free:-0}; mem=${mem:-0}; hyg=${hyg:-0}`,
		`a1=$(( cores*3/2 - load )); a2=$(( (free-25)/2 )); a3=$(( mem/2 )); a=0`,
		`[ $a1 -gt $a ] && a=$a1; [ $a2 -lt $a ] && a=$a2; [ $a3 -lt $a ] && a=$a3; [ $a -lt 0 ] && a=0`,
		`printf 'STATUSFLEET\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$live" "$cores" "$load" "$free" "$mem" "$a" "$hyg"`,
	}, "\n")
}

// certificationOf is one cell per row of the slots table, keyed by the name that row prints.
// The self row is a machine too -- the coordinator is in the registry and is certified like
// every other -- so it gets a cell as well.
func certificationOf(certs []fleet.Certificate, read bool, benches []FleetBench, self *SelfReading, now time.Time, maxAge time.Duration) map[string]string {
	if !read {
		return nil
	}
	names := make([]string, 0, len(benches)+1)
	for _, b := range benches {
		names = append(names, b.Name)
	}
	if self != nil && strings.TrimSpace(self.Name) != "" {
		names = append(names, self.Name)
	}
	out := map[string]string{}
	for _, c := range fleet.SummarizeAll(certs, names, now, maxAge) {
		out[c.Machine] = c.Column()
	}
	return out
}
