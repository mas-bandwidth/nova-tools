package tokens

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultStalenessThreshold is the default threshold (36 hours) before day files
// are considered stale when declared sources indicate activity.
const DefaultStalenessThreshold = 36 * time.Hour

// CollateDayResult holds the result of collating a single day's token rows.
type CollateDayResult struct {
	Day        string
	Written    bool
	Sources    []string
	Turns      string
	Rows       int
	Shrank     bool
	Shrinks    []Shrink
	Partial    bool
	Partials   []Partial
	Unreadable []Unreadable
	File       *DayFile
}

// CheckStaleness evaluates whether the newest day file is older than maxStaleness.
// If hasActivity is true and the last recorded day is older than the horizon (or absent),
// it returns stale=true, the expected day (UTC date string), and the threshold in hours.
func CheckStaleness(lastDay string, now time.Time, maxStaleness time.Duration, hasActivity bool) (bool, string, int) {
	thresholdHours := int(maxStaleness / time.Hour)
	if thresholdHours <= 0 {
		return false, "", 0
	}
	expectedDay := now.UTC().Format("2006-01-02")
	if !hasActivity {
		return false, expectedDay, thresholdHours
	}
	if lastDay == "" {
		return true, expectedDay, thresholdHours
	}
	lastDate, err := time.Parse("2006-01-02", lastDay)
	if err != nil {
		return true, expectedDay, thresholdHours
	}
	// The day covers up to 24 hours from its midnight UTC start.
	endOfLastDay := lastDate.Add(24 * time.Hour)
	if now.UTC().Sub(endOfLastDay) > maxStaleness {
		return true, expectedDay, thresholdHours
	}
	return false, expectedDay, thresholdHours
}

// BuildDayFile constructs a DayFile from folded rows, turns, build ID, and timestamp.
func BuildDayFile(day, build, turns string, rows []*Row, now time.Time) *DayFile {
	f := &DayFile{
		Day:   day,
		At:    now.UTC().Format(time.RFC3339),
		Build: build,
		Turns: turns,
	}
	labels := map[string]bool{}
	for _, r := range rows {
		for _, l := range r.Sources() {
			labels[l] = true
		}
		f.Rows = append(f.Rows, DayRow{
			Date:    day,
			Model:   r.Model,
			Repo:    r.Repo,
			Unit:    r.Unit,
			Counts:  r.Counts,
			Rough:   r.Rough,
			Basis:   r.Basis(),
			Sources: r.Sources(),
		})
	}
	sort.Slice(f.Rows, func(i, j int) bool {
		ki := f.Rows[i].Model + "\t" + f.Rows[i].Repo + "\t" + f.Rows[i].Unit
		kj := f.Rows[j].Model + "\t" + f.Rows[j].Repo + "\t" + f.Rows[j].Unit
		return ki < kj
	})
	for l := range labels {
		f.Sources = append(f.Sources, l)
	}
	sort.Strings(f.Sources)
	return f
}

// CollateDay processes and writes a single day's folded rows into the output directory,
// enforcing Rule 10 (refuses shrinkage unless allowShrink is set) and Card 268 (source-aware row merging).
// It writes atomically via DayFile.Save (which uses atomicfile.WriteFile).
func CollateDay(out string, day string, rows []*Row, turns string, declared []string, allowShrink bool, build string, now time.Time) (*CollateDayResult, error) {
	res := &CollateDayResult{
		Day:   day,
		Turns: turns,
	}
	outPath := Path(out, day)
	old, findings, readErr := ReadDayFile(outPath)

	file := BuildDayFile(day, build, turns, rows, now)
	res.File = file
	res.Sources = file.Sources
	res.Rows = len(file.Rows)

	if readErr != nil && !os.IsNotExist(readErr) {
		res.Unreadable = append(res.Unreadable, Unreadable{
			Label: "out",
			Path:  outPath,
			Why:   readErr.Error(),
		})
	} else if readErr == nil && len(findings) > 0 {
		for _, f := range findings {
			why := oneline.Escape(f.Reason)
			if f.Line > 0 {
				why = fmt.Sprintf("line %d: %s", f.Line, oneline.Escape(f.Reason))
			}
			res.Unreadable = append(res.Unreadable, Unreadable{
				Label: "out",
				Path:  outPath,
				Why:   why,
			})
		}
	} else if readErr == nil && len(findings) == 0 {
		merged, retained, partials := MergeDay(old.Rows, file.Rows, declared)
		if len(partials) > 0 {
			res.Partial = true
			res.Partials = partials
		} else {
			file.Rows = merged
			sort.Slice(file.Rows, func(i, j int) bool {
				ki := file.Rows[i].Model + "\t" + file.Rows[i].Repo + "\t" + file.Rows[i].Unit
				kj := file.Rows[j].Model + "\t" + file.Rows[j].Repo + "\t" + file.Rows[j].Unit
				return ki < kj
			})
			file.Sources = SourcesOf(merged)
			res.Sources = file.Sources
			res.Rows = len(file.Rows)
			if retained > 0 {
				file.Turns = Dash
				res.Turns = Dash
			}
			res.Shrinks = Shrinks(old.Totals(), file.Totals(), day)
			if len(res.Shrinks) > 0 {
				res.Shrank = true
			}
		}
	}

	canWrite := !res.Partial && (!res.Shrank || allowShrink) &&
		((readErr == nil && len(findings) == 0) || (readErr != nil && os.IsNotExist(readErr)))
	if canWrite {
		if len(file.Rows) > 0 {
			if err := file.Save(out); err != nil {
				res.Unreadable = append(res.Unreadable, Unreadable{
					Label: "out",
					Path:  outPath,
					Why:   err.Error(),
				})
			} else {
				res.Written = true
			}
		}
	}
	return res, nil
}
