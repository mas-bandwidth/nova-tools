package pulse

// StatusLine is G4 of the pit stop's class G (#828): STATE LIVES IN FILES, AND THE WINDOW
// RESTARTS AT BEATS.
//
// `status` already answers the all-day questions in eight lines, and eight lines is the
// right answer for a person opening a report. It is the wrong answer for a fresh
// coordinator window, which needs the whole day reconstructed before it decides anything
// and pays for every line it reads in a context it then carries for hours. So `status
// --oneline` is the same day in ONE line under StatusLineMax bytes: width per bench, the
// pool, whether work is stopped, the reds and the merges of the day, the cards done and
// failed, the day's spend, and the pit-stop note when one is open. A fresh window needs
// this line and POLICY, and never the transcript.
//
// Every number comes from a file under --queue or a usage.tsv under --roots. Nothing here
// calls gh, nothing here calls a model, and a count nobody wrote is a zero this line prints
// rather than a fact it invents.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// StatusLineMax is the ceiling on the line, in bytes. Four hundred is about a hundred
// tokens: the price of knowing the whole day, paid once per window.
const StatusLineMax = 400

// StatusLine prints the one line and returns 0, or 2 when it could not run.
func StatusLine(in StatusInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	for _, r := range []struct{ v, name, wants string }{
		{in.Queue, "queue", "the queue directory holding pending, launched, done, failed and the state files"},
		{in.Roots, "roots", "the benches to report, comma separated"},
	} {
		if strings.TrimSpace(r.v) == "" {
			return refusal(in.Stderr, "STATUS", fmt.Errorf("missing --%s; refusing to guess (%s)", r.name, r.wants))
		}
	}
	day := in.Now().Format("2006-01-02")
	if d := strings.TrimSpace(in.Day); d != "" {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			return refusal(in.Stderr, "STATUS", fmt.Errorf("--day wants YYYY-MM-DD, got %q (say the day the window starts at)", in.Day))
		}
		day = d
	}
	fmt.Fprintln(in.Stdout, statusOneLine(in, day))
	return 0
}

// statusOneLine builds the line and holds it under the ceiling.
func statusOneLine(in StatusInput, day string) string {
	roots := splitList(in.Roots)

	stop := "no"
	if _, err := os.Stat(filepath.Join(in.Queue, "STOP")); err == nil {
		stop = "yes"
	}

	pitstop := "-"
	if s := firstLine(filepath.Join(in.Queue, "PITSTOP")); s != "" {
		pitstop = oneline.Field(s)
	}

	head := fmt.Sprintf("STATUS %s stop=%s pool=%d cards=%d/%d reds=%d merges=%d spend=%s",
		day, stop,
		countCards(in.Queue, "pending"),
		countCards(in.Queue, "done"), countCards(in.Queue, "failed"),
		countDay(in.Queue, "REDS", day, "MAIN-RED"),
		countDay(in.Queue, "MERGED", day, ""),
		usd(spendOfDay(roots, day)))

	// The benches, widest information first: a bench that does not fit is counted, never
	// silently dropped -- the same law as every listing here (internal/bounded).
	var shown []string
	dropped := 0
	for _, r := range roots {
		running, slots := slotCounts(r)
		cell := fmt.Sprintf("%s:%d/%d", oneline.Field(filepath.Base(r)), running, slots)
		trial := head + " width=" + strings.Join(append(append([]string{}, shown...), cell), ",")
		if len(trial)+len(pitstopField(pitstop, dropped+1)) > StatusLineMax {
			dropped++
			continue
		}
		shown = append(shown, cell)
	}
	width := strings.Join(shown, ",")
	if width == "" {
		width = "-"
	}
	line := head + " width=" + width + pitstopField(pitstop, dropped)
	if len(line) > StatusLineMax {
		// Only the pit-stop note can still be over: it is a person's sentence, and the
		// remedy is the file, which the note names by being in the queue.
		room := StatusLineMax - (len(line) - len(pitstop))
		if room < 8 {
			room = 8
		}
		line = head + " width=" + width + pitstopField(oneline.Cap(pitstop, room), dropped)
	}
	return line
}

// pitstopField is the tail of the line: the open pit stop and the benches that did not fit.
func pitstopField(pitstop string, dropped int) string {
	s := " pitstop=" + pitstop
	if dropped > 0 {
		s += fmt.Sprintf(" benches_more=%d", dropped)
	}
	return s
}

// slotCounts reads one bench's slot lock files (SPEC-PULSE rule 8: the swarm's own lock
// files, never a log age).
func slotCounts(root string) (running, slots int) {
	matches, _ := filepath.Glob(filepath.Join(root, "pool", "slots", "*.json"))
	for _, m := range matches {
		slots++
		raw, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var sf struct {
			State string `json:"state"`
		}
		if json.Unmarshal(raw, &sf) != nil {
			continue
		}
		if sf.State != "free" {
			running++
		}
	}
	return running, slots
}

// countDay counts the lines of a state file stamped with the day, optionally carrying a
// word. The files are the loop's own records -- REDS is written by run.go's gate, MERGED by
// the sweep step -- and a file nobody wrote is a zero.
func countDay(queue, name, day, word string) int {
	n := 0
	for _, l := range readLines(filepath.Join(queue, name)) {
		if !strings.HasPrefix(strings.TrimSpace(l), day) {
			continue
		}
		if word != "" && !strings.Contains(l, word) {
			continue
		}
		n++
	}
	return n
}

// spendOfDay sums the usd column of EVERY usage.tsv row under the benches that started on
// the day. A row whose usd cell is a dash is not a zero and is not summed: a cost nobody
// measured is unknown (SPEC-PULSE, the RATE line's law). It walks the rows itself rather
// than through status.go's collectUsage, which takes the first row of each file because
// its arithmetic is per card; a day's spend is per row.
func spendOfDay(roots []string, day string) float64 {
	total := 0.0
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() != "usage.tsv" {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for _, l := range strings.Split(string(raw), "\n") {
				if l == "" || strings.HasPrefix(l, "job") {
					continue
				}
				f := strings.Split(l, "\t")
				if len(f) < 13 || f[12] == "-" {
					continue
				}
				started, err := time.Parse(time.RFC3339, f[2])
				if err != nil || started.UTC().Format("2006-01-02") != day {
					continue
				}
				if v, err := strconv.ParseFloat(f[12], 64); err == nil {
					total += v
				}
			}
			return nil
		})
	}
	return total
}

func usd(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
