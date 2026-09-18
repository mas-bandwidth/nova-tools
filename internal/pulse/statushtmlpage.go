package pulse

// The page itself. Counts and numbers only: no card id, branch name or label ever reaches
// it, because it is served to whoever can reach the host. A number nobody measured is a
// dash -- the page never prints a zero it did not count.

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// htmlText is prose on the page: one line, no control characters, and no markup, so a
// bench name or a home path out of the fleet file can say what went wrong without being
// able to say anything else.
func htmlText(s string) string { return html.EscapeString(oneline.Escape(s)) }

// mergeQueueCount is the merge queue's depth broken into the three states a reader acts
// on: one running the checks, the ones waiting behind it, and the ones that cannot merge
// at all and are holding the queue up.
type mergeQueueCount struct {
	total       int
	running     int
	waiting     int
	unmergeable int
}

// pageData is everything the page renders, gathered before a byte is written.
type pageData struct {
	now        time.Time
	dayStart   string // the boundary the merged count is from, as HH:MMZ
	branch     string
	tipSHA     string
	tipRun     string
	tipKnown   bool
	merged     count
	opened     count
	mq         mergeQueueCount
	mqKnown    bool
	queueDepth int
	ready      int
	launched   int
	refused    int
	hygiene    int
	readings   []BenchReading
	self       *SelfReading
	// certs is the certification cell per machine name, read from the certificates file.
	// nil is "no --certs was given"; a name missing from a non-nil map is a machine with no
	// row, and both print a dash. A zero here would read as "nothing is certified".
	certs map[string]string
}

// certCell is the certification cell for one machine: what the record says, or a dash.
func (p pageData) certCell(name string) string {
	if p.certs == nil {
		return "-"
	}
	if cell, ok := p.certs[name]; ok && cell != "" {
		return cell
	}
	return "-"
}

// fleetPage renders the page.
func fleetPage(p pageData) string {
	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><meta http-equiv=\"refresh\" content=\"60\"><title>nova fleet</title><style>")
	b.WriteString("body{font-family:-apple-system,Helvetica,sans-serif;margin:24px;color:#222}")
	b.WriteString("table{border-collapse:collapse}td,th{border:1px solid #ccc;padding:4px 10px;text-align:right}")
	b.WriteString("th:first-child,td:first-child{text-align:left}</style></head><body>\n")
	fmt.Fprintf(&b, "<h2>nova fleet at %s</h2>\n", p.now.UTC().Format(time.RFC3339))

	// The tip and its run: which commit the fleet is actually building on, and whether that
	// build is green. A red tip is the one fact that stops every other number mattering.
	tip := "unknown (no repo to ask)"
	if p.tipKnown {
		// The branch and the sha are tokens; the run's verdict is two words a person reads.
		tip = fmt.Sprintf("%s %s, its run: %s", oneline.Field(p.branch), oneline.Field(p.tipSHA), htmlText(p.tipRun))
	}
	fmt.Fprintf(&b, "<p>%s. Merged since %s: <b>%s</b>. PRs opened last hour: <b>%s</b>.</p>\n",
		tip, oneline.Field(p.dayStart), p.merged, p.opened)

	b.WriteString("<h3>queues</h3><table><tr><th>queue</th><th>depth</th><th>of which</th></tr>\n")
	if p.mqKnown {
		fmt.Fprintf(&b, "<tr><td>merge queue</td><td>%d</td><td>running %d, waiting %d, unmergeable %d</td></tr>\n",
			p.mq.total, p.mq.running, p.mq.waiting, p.mq.unmergeable)
	} else {
		b.WriteString("<tr><td>merge queue</td><td>-</td><td>nobody asked the forge</td></tr>\n")
	}
	fmt.Fprintf(&b, "<tr><td>pending cards</td><td>%d</td><td>ready %d</td></tr>\n", p.queueDepth, p.ready)
	fmt.Fprintf(&b, "<tr><td>launched by the fill loop, total</td><td>%d</td><td>refused by capacity: %d</td></tr>\n", p.launched, p.refused)
	fmt.Fprintf(&b, "<tr><td>hygiene actions, last hour</td><td>%d</td><td>reaps and deletions across the benches</td></tr></table>\n", p.hygiene)

	b.WriteString("<h3>slots</h3>\n")
	b.WriteString("<table><tr><th>bench</th><th>live cards</th><th>cores</th><th>load</th><th>free disk</th><th>free mem</th><th>allowed</th><th>certification</th></tr>\n")
	for _, r := range p.readings {
		if r.Down {
			note := r.Note
			if note == "" {
				note = "no answer over ssh"
			}
			// A bench that did not answer over ssh still has a RECORD, and the record is
			// read from a file: "DOWN, and it was certified 14/14 an hour ago" and "DOWN,
			// and it has never been certified" send a person to two different places.
			fmt.Fprintf(&b, "<tr><td>%s</td><td colspan=\"6\"><b>DOWN</b> (%s)</td><td>%s</td></tr>\n",
				oneline.Field(r.Name), htmlText(note), htmlText(p.certCell(r.Name)))
			continue
		}
		fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td><td>%d</td><td>%d</td><td>%d GB</td><td>%d GB</td><td>%d</td><td>%s</td></tr>\n",
			oneline.Field(r.Name), r.Live, r.Cores, r.Load, r.FreeGB, r.MemGB, r.Allowed, htmlText(p.certCell(r.Name)))
	}
	// The host running the verb is a bench too, and its columns are its own: CI runners
	// where a bench counts cards, orphans where a bench counts headroom.
	if p.self != nil {
		if p.self.Unknown {
			fmt.Fprintf(&b, "<tr><td>%s</td><td colspan=\"6\"><b>UNKNOWN</b> (the host could not be read)</td><td>%s</td></tr>\n",
				oneline.Field(p.self.Name), htmlText(p.certCell(p.self.Name)))
		} else {
			fmt.Fprintf(&b, "<tr><td>%s</td><td>ci=%d</td><td>%d</td><td>%d</td><td>%d GB</td><td>-</td><td>orphans=%d</td><td>%s</td></tr>\n",
				oneline.Field(p.self.Name), p.self.CIRunners, p.self.Cores, p.self.Load, p.self.FreeGB, p.self.Orphans,
				htmlText(p.certCell(p.self.Name)))
		}
	}
	b.WriteString("</table>\n")

	if p.self != nil && len(p.self.Loops) > 0 {
		parts := make([]string, 0, len(p.self.Loops))
		for _, l := range p.self.Loops {
			parts = append(parts, fmt.Sprintf("%s=%d", oneline.Field(l.Label), l.N))
		}
		fmt.Fprintf(&b, "<p>loops on %s: %s</p>\n", oneline.Field(p.self.Name), strings.Join(parts, " "))
	}

	b.WriteString("<p>live cards are running card processes per bench. allowed = min(cores*1.5 - load, (free_gb - 25)/2, memfree_gb/2). A bench that does not answer says DOWN and a number nobody measured is a dash: a row of zeros would read as a bench with nothing to do. certification is what the certificates file records for that machine -- classes current under the build and standard its own newest row was written with, out of the classes it has ever run -- and a dash there means nobody has certified it, never that it failed. Page rewritten every minute; refreshes itself every minute.</p>\n")
	b.WriteString(timeSeries)
	b.WriteString("</body></html>\n")
	return b.String()
}

// timeSeries draws metrics.tsv, the file the verb writes beside the page, as the two charts
// bin/status-page.sh drew. The page that writes a series and draws nothing loses the one
// view that shows the fleet widening or stalling rather than its state at this instant.
// The columns are the seven the metrics row writes: stamp, live, queue depth, merged,
// opened, launched, free disk per bench. A cell that is a dash -- a count nobody took --
// plots as a gap, never as a zero, because a flat line at zero is a claim.
const timeSeries = `<h3>time series (one row per page write)</h3>
<canvas id="c1" height="90"></canvas><canvas id="c2" height="90"></canvas>
<script src="https://cdnjs.cloudflare.com/ajax/libs/Chart.js/4.4.1/chart.umd.min.js"></script>
<script>fetch('metrics.tsv').then(r=>r.text()).then(t=>{
const rows=t.trim().split('\n').map(l=>l.split('\t')).filter(r=>r.length>=6);
const L=rows.map(r=>r[0].slice(11,16));
const n=(v)=>{const x=parseFloat(v);return isNaN(x)?null:x;};
const mk=(id,ds)=>new Chart(document.getElementById(id),{type:'line',data:{labels:L,datasets:ds},options:{animation:false,spanGaps:false,scales:{y:{beginAtZero:true}}}});
mk('c1',[{label:'live cards',data:rows.map(r=>n(r[1]))},{label:'queue depth',data:rows.map(r=>n(r[2]))},{label:'PRs opened, last hour',data:rows.map(r=>n(r[4]))}]);
mk('c2',[{label:'merged since the day start',data:rows.map(r=>n(r[3]))},{label:'cards launched by the fill loop (cumulative)',data:rows.map(r=>n(r[5]))}]);
});</script>
`
