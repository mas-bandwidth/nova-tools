package pulse

// status --html is the fleet status page as a verb: it folds the old status-page.sh into
// nova-pulse and fixes the one correctness bug while doing it. The script counted a live
// slot from log age -- a harness-output.log under fifteen minutes old with no RESULT.md --
// which the bench-hygiene incident showed is wrong: a card can be silently dead with a
// fresh-looking log, and a long card's log is older than fifteen minutes while it is still
// alive. Live here is the count of running card processes per bench, the authoritative
// liveness bench-hygiene.sh's live_slot uses (a process whose command line names the job
// dir, or whose cwd is under the slot), and the reader is injected so a test never starts
// ssh. Both outputs are counts and numbers only: no card id, branch name or label ever
// reaches a public page.

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

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// BenchReading is one bench's numbers for the page: the live cards counted from running
// card processes, and the capacity the allowed arithmetic folds.
type BenchReading struct {
	Name    string
	Live    int
	Cores   int
	Load    int
	FreeGB  int
	MemGB   int
	Allowed int
	// Down is the bench that did not answer. It is NOT a bench with nothing to do, and the
	// difference is what the 2026-09-17 pit stop cost: a row of zeros reads as an idle
	// bench, so a fleet nobody could see looked like a fleet with nothing to do. The page
	// says DOWN, the metrics row writes a dash for its disk rather than a number the bench
	// never gave, and the STATUS HTML line counts it.
	Down bool
}

// FleetReader reads every bench's numbers in file order. The production reader reads them
// over ssh with ps/pgrep liveness; a test injects a fake returning per-bench process counts
// and disk/mem, so no test starts ssh.
type FleetReader func(benches []FleetBench, now time.Time) []BenchReading

// StatusHTMLInput is everything `status --html` needs, apart from flag parsing, so a test
// can drive it against fake directories and an injected fleet reader.
type StatusHTMLInput struct {
	HTML    string // the local file to write; the verb never ships it
	Benches string // the fleet file: name, ssh target, home, mac
	Queue   string // the queue whose pending depth and fill log the metrics row counts
	SSH     string // the ssh program; empty is "ssh"
	Timeout time.Duration
	Reader  FleetReader
	Now     func() time.Time
	Stdout  io.Writer
	Stderr  io.Writer
}

// StatusHTML writes the fleet page to --html and appends one seven-column metrics.tsv row
// beside it, then prints the one STATUS HTML line. Counts only, live from running card
// processes.
func StatusHTML(in StatusHTMLInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.Timeout <= 0 {
		in.Timeout = fleetDefaultTimeout
	}
	for _, r := range []struct{ v, name, wants string }{
		{in.HTML, "html", "the local file path to write the fleet page to; the caller ships it"},
		{in.Benches, "benches", "the fleet file: name, ssh target, home, mac one per line"},
	} {
		if strings.TrimSpace(r.v) == "" {
			return refusal(in.Stderr, "STATUS", fmt.Errorf("--%s is required; it wants %s; refusing to guess", r.name, r.wants))
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

	reader := in.Reader
	if reader == nil {
		reader = func(list []FleetBench, now time.Time) []BenchReading {
			return readFleetOverSSH(list, in.SSH, in.Timeout)
		}
	}
	now := in.Now()
	// Reading the fleet is the slow step: every bench over ssh, bounded by --timeout. A
	// verb that takes seconds says what it is doing while it takes them.
	fmt.Fprintf(in.Stderr, "STATUS reading %d benches, up to %s each\n", len(benches), in.Timeout)
	readings := reader(benches, now)

	liveTotal, downTotal := 0, 0
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
		freeDisk = append(freeDisk, strconv.Itoa(r.FreeGB))
	}

	queueDepth := countCards(in.Queue, "pending")
	ready := countCards(in.Queue, "ready")
	launched := countFillLog(in.Queue, "attempt=")
	refused := countFillLog(in.Queue, "REFUSED")
	merged, opened := 0, 0
	if repo := firstLine(filepath.Join(in.Queue, "REPO")); repo != "" {
		prs, _ := readGh(repo, in.Timeout)
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		opened, _ = prsOpenedMerged(prs, now.Add(-time.Hour), now)
		merged, _ = prsOpenedMerged(prs, dayStart, now)
	}

	page := fleetPage(now, merged, opened, queueDepth, ready, launched, refused, readings)
	if err := os.WriteFile(in.HTML, []byte(page), 0o644); err != nil {
		return refusal(in.Stderr, "STATUS", fmt.Errorf("could not write %s: %s", oneline.Field(in.HTML), oneline.Err(err)))
	}
	row := fmt.Sprintf("%s\t%d\t%d\t%d\t%d\t%d\t%s\n",
		now.UTC().Format(time.RFC3339), liveTotal, queueDepth, merged, opened, launched, strings.Join(freeDisk, ","))
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

	fmt.Fprintf(in.Stdout, "STATUS HTML wrote=%s live=%d queue=%d down=%d\n", in.HTML, liveTotal, queueDepth, downTotal)
	return 0
}

// fleetPage renders the page: counts and bench numbers only, never a card id or branch name.
func fleetPage(now time.Time, merged, opened, queueDepth, ready, launched, refused int, readings []BenchReading) string {
	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><meta http-equiv=\"refresh\" content=\"60\"><title>nova fleet</title><style>")
	b.WriteString("body{font-family:-apple-system,Helvetica,sans-serif;margin:24px;color:#222}")
	b.WriteString("table{border-collapse:collapse}td,th{border:1px solid #ccc;padding:4px 10px;text-align:right}")
	b.WriteString("th:first-child,td:first-child{text-align:left}</style></head><body>\n")
	fmt.Fprintf(&b, "<h2>nova fleet at %s</h2>\n", now.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "<p>Merged since 00:00Z: <b>%d</b>. PRs opened last hour: <b>%d</b>.</p>\n", merged, opened)
	b.WriteString("<h3>queues</h3><table><tr><th>queue</th><th>depth</th><th>of which</th></tr>\n")
	fmt.Fprintf(&b, "<tr><td>pending cards</td><td>%d</td><td>ready %d</td></tr>\n", queueDepth, ready)
	fmt.Fprintf(&b, "<tr><td>launched by the fill loop, total</td><td>%d</td><td>refused by capacity: %d</td></tr></table>\n", launched, refused)
	b.WriteString("<h3>slots</h3>\n")
	b.WriteString("<table><tr><th>bench</th><th>live cards</th><th>cores</th><th>load</th><th>free disk</th><th>free mem</th><th>allowed</th></tr>\n")
	for _, r := range readings {
		if r.Down {
			fmt.Fprintf(&b, "<tr><td>%s</td><td colspan=\"6\"><b>DOWN</b> (no answer over ssh)</td></tr>\n",
				oneline.Field(r.Name))
			continue
		}
		fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td><td>%d</td><td>%d</td><td>%d GB</td><td>%d GB</td><td>%d</td></tr>\n",
			oneline.Field(r.Name), r.Live, r.Cores, r.Load, r.FreeGB, r.MemGB, r.Allowed)
	}
	b.WriteString("</table>\n")
	b.WriteString("<p>live cards are running card processes per bench. allowed = min(cores*1.5 - load, (free_gb - 25)/2, memfree_gb/2). A bench that does not answer says DOWN: a row of zeros would read as a bench with nothing to do. Page rewritten every minute; refreshes itself every minute.</p>\n")
	b.WriteString(timeSeries)
	b.WriteString("</body></html>\n")
	return b.String()
}

// timeSeries draws metrics.tsv, the file the verb writes beside the page, as the two charts
// bin/status-page.sh drew. The page that writes a series and draws nothing loses the one
// view that shows the fleet widening or stalling rather than its state at this instant.
// The columns are the seven the metrics row writes: stamp, live, queue depth, merged,
// opened, launched, free disk per bench. A row whose live column is a dash (a bench that
// did not answer) still plots the rest of its numbers.
const timeSeries = `<h3>time series (one row per page write)</h3>
<canvas id="c1" height="90"></canvas><canvas id="c2" height="90"></canvas>
<script src="https://cdnjs.cloudflare.com/ajax/libs/Chart.js/4.4.1/chart.umd.min.js"></script>
<script>fetch('metrics.tsv').then(r=>r.text()).then(t=>{
const rows=t.trim().split('\n').map(l=>l.split('\t')).filter(r=>r.length>=6);
const L=rows.map(r=>r[0].slice(11,16));
const n=(v)=>{const x=parseFloat(v);return isNaN(x)?null:x;};
const mk=(id,ds)=>new Chart(document.getElementById(id),{type:'line',data:{labels:L,datasets:ds},options:{animation:false,scales:{y:{beginAtZero:true}}}});
mk('c1',[{label:'live cards',data:rows.map(r=>n(r[1]))},{label:'queue depth',data:rows.map(r=>n(r[2]))},{label:'PRs opened, last hour',data:rows.map(r=>n(r[4]))}]);
mk('c2',[{label:'merged today',data:rows.map(r=>n(r[3]))},{label:'cards launched by the fill loop (cumulative)',data:rows.map(r=>n(r[5]))}]);
});</script>
`

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
// order. A bench that cannot be reached reads as all zeros rather than failing the page.
func readFleetOverSSH(benches []FleetBench, ssh string, timeout time.Duration) []BenchReading {
	out := make([]BenchReading, len(benches))
	var wg sync.WaitGroup
	for i, b := range benches {
		wg.Add(1)
		go func(i int, b FleetBench) {
			defer wg.Done()
			out[i] = readOneBench(ssh, b, timeout)
		}(i, b)
	}
	wg.Wait()
	return out
}

// readOneBench runs the liveness script on one bench and folds its answer.
func readOneBench(ssh string, b FleetBench, timeout time.Duration) BenchReading {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := fleetSSH(ctx, ssh, b.SSH, fleetStatusScript(b.Home))
	if err != nil {
		return BenchReading{Name: b.Name, Down: true}
	}
	live, cores, load, free, mem, allowed, ok := parseFleetStatus(out)
	if !ok {
		// An answer nobody can parse is no answer: it says DOWN, never a quiet zero.
		return BenchReading{Name: b.Name, Down: true}
	}
	return BenchReading{Name: b.Name, Live: live, Cores: cores, Load: load, FreeGB: free, MemGB: mem, Allowed: allowed}
}

// parseFleetStatus reads the one STATUSFLEET line the remote script prints.
func parseFleetStatus(out string) (live, cores, load, free, mem, allowed int, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) != 7 || f[0] != "STATUSFLEET" {
			continue
		}
		vals := make([]int, 6)
		good := true
		for i := range vals {
			n, err := strconv.Atoi(f[i+1])
			if err != nil {
				good = false
				break
			}
			vals[i] = n
		}
		if !good {
			continue
		}
		return vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], true
	}
	return 0, 0, 0, 0, 0, 0, false
}

// fleetStatusScript is the remote liveness and capacity script, run through `ssh <target>
// bash -s` with HOME set to the bench's home column. A slot is live when a process's command
// line names one of its job directories (pgrep -f) or a process's cwd is under the slot --
// the authoritative shape bench-hygiene.sh's live_slot uses. It prints exactly one
// tab-separated STATUSFLEET line; the Go side is the only place a number is formatted.
func fleetStatusScript(home string) string {
	return strings.Join([]string{
		"HOME=" + fleetQuote(home),
		"export HOME",
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
		`    for pid in $(ls /proc 2>/dev/null | grep -E '^[0-9]+$'); do`,
		`      case "$(readlink /proc/$pid/cwd 2>/dev/null)" in "$s"/*) found=1; break;; esac`,
		`    done`,
		`  fi`,
		`  [ "$found" = 1 ] && live=$((live+1))`,
		`done`,
		`cores=$(nproc 2>/dev/null || echo 1)`,
		`load=$(cut -d. -f1 /proc/loadavg 2>/dev/null || echo 0)`,
		`free=$(df -BG "$HOME" 2>/dev/null | awk 'NR==2{gsub("G","",$4); print $4}')`,
		`mem=$(awk '/MemAvailable/{printf "%d", $2/1048576}' /proc/meminfo 2>/dev/null)`,
		`cores=${cores:-1}; load=${load:-0}; free=${free:-0}; mem=${mem:-0}`,
		`a1=$(( cores*3/2 - load )); a2=$(( (free-25)/2 )); a3=$(( mem/2 )); a=0`,
		`[ $a1 -gt $a ] && a=$a1; [ $a2 -lt $a ] && a=$a2; [ $a3 -lt $a ] && a=$a3; [ $a -lt 0 ] && a=0`,
		`printf 'STATUSFLEET\t%s\t%s\t%s\t%s\t%s\t%s\n' "$live" "$cores" "$load" "$free" "$mem" "$a"`,
	}, "\n")
}
