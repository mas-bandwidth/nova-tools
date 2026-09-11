package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// FINALIZE IS THE RUNNER'S STEP AFTER EVERY END -- exit, reap, budget, violation -- once the
// process group is dead, and its ORDER is the rule (rule 12):
//
//	1. the usage file, <pool>/usage/<job>.tsv, outside everything reclaim removes
//	2. the published report, copied byte for byte to <pool>/reports/<job>/RESULT.md, or a
//	   MALFORMED or NO-RESULT marker in its place, and a REV beside it holding the attempt
//	   and the SHA-256 of what was copied
//	3. ONLY THEN the job's files move to done/ or failed/
//	4. ONLY THEN the job's RUN line is printed
//
// A completed job reclaimed before the first triage lost its only RESULT.md once. So triage
// and `result --id` read <pool>/reports/<job>/ for a finalized job and <job>/RESULT.md only
// for a running one, and the first triage after a reclaim reads the same bytes it would
// have read before it.

// The three names under <pool>/reports/<job>/.
const (
	MarkerMalformed = "MALFORMED"
	MarkerNoResult  = "NO-RESULT"
	MarkerRev       = "REV"
	CopiedResult    = "RESULT.md"
)

// Ending is everything finalize needs to know about a job that has ended.
type Ending struct {
	Sidecar  Sidecar
	JobDir   string
	Provider string
	Model    string
	End      string
	RC       int // -1 is no exit code: unknown, or a launch that never happened
	Started  time.Time
	Ended    time.Time
	Usage    ProviderUsage
	Repo     string
}

// Finalized is what finalize did, so the caller can print one line about it.
type Finalized struct {
	UsagePath     string
	UsageExisted  bool
	Class         string
	MalformedLine int
	Hash          string
	Report        Report
	Published     bool
}

// ReportsDir is <pool>/reports/<job>/, the retained record of one job.
func (p *Pool) ReportsDir(id string) string { return p.Path(Reports, id) }

// Finalize writes the usage file and the report copy, in that order, before anything moves.
func (p *Pool) Finalize(e Ending) (Finalized, error) {
	var out Finalized
	attempt := 1
	if e.Sidecar.Requeued > 0 {
		attempt = 2
	}
	row := UsageRow{
		"job": e.Sidecar.ID, "attempt": strconv.Itoa(attempt), "from": dashOr(e.Sidecar.From),
		"started": stampOr(e.Started), "ended": stampOr(e.Ended), "end": e.End,
		"rc": rcColumn(e.End, e.RC), "provider": dashOr(e.Provider), "model": dashOr(e.Model),
		"repo": dashOr(e.Repo),
	}
	for _, c := range append(append([]string{}, TokenColumns...), "usd") {
		row[c] = dashOr(strings.TrimSpace(e.Usage.Values[c]))
	}
	if row["repo"] == Dash {
		row["repo"] = dashOr(strings.TrimSpace(e.Usage.Values["repo"]))
	}
	if row["model"] == Dash {
		row["model"] = dashOr(strings.TrimSpace(e.Usage.Values["model"]))
	}
	path, existed, err := p.WriteUsage(e.Sidecar.ID, row)
	out.UsagePath, out.UsageExisted = path, existed
	if err != nil {
		return out, err
	}
	return p.copyReport(e, out)
}

// copyReport is step 2: the published report, or the marker that says why there is none.
func (p *Pool) copyReport(e Ending, out Finalized) (Finalized, error) {
	dir := p.ReportsDir(e.Sidecar.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return out, err
	}
	attempt := 1
	if e.Sidecar.Requeued > 0 {
		attempt = 2
	}
	raw, err := os.ReadFile(ResultPath(e.JobDir))
	if err != nil {
		out.Class, out.Hash = ClassNoResult, HashBytes(nil)
		if err := writeAtomic(filepath.Join(dir, MarkerNoResult), []byte("no RESULT.md was published\n"), 0o644); err != nil {
			return out, err
		}
		return out, p.writeRev(dir, attempt, out.Hash)
	}
	out.Published = true
	report := ParseReport(raw)
	out.Report, out.Class, out.Hash, out.MalformedLine = report, report.Class, report.Hash, report.MalformedLine
	if err := writeAtomic(filepath.Join(dir, CopiedResult), raw, 0o644); err != nil {
		return out, err
	}
	if report.Class == ClassMalformed {
		marker := fmt.Sprintf("line=%d\n", report.MalformedLine)
		if err := writeAtomic(filepath.Join(dir, MarkerMalformed), []byte(marker), 0o644); err != nil {
			return out, err
		}
	}
	return out, p.writeRev(dir, attempt, out.Hash)
}

func (p *Pool) writeRev(dir string, attempt int, hash string) error {
	return writeAtomic(filepath.Join(dir, MarkerRev), []byte(fmt.Sprintf("attempt=%d\nsha256=%s\n", attempt, hash)), 0o644)
}

// ReadRev reads the attempt and hash beside a retained report.
func (p *Pool) ReadRev(id string) (attempt int, hash string, err error) {
	raw, err := os.ReadFile(filepath.Join(p.ReportsDir(id), MarkerRev))
	if err != nil {
		return 0, "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "attempt":
			attempt, _ = strconv.Atoi(value)
		case "sha256":
			hash = value
		}
	}
	return attempt, hash, nil
}

// RetainedReport reads the retained copy of a finalized job's report, and says which of the
// three shapes the record takes.
func (p *Pool) RetainedReport(id string) (raw []byte, kind string, err error) {
	dir := p.ReportsDir(id)
	if _, err := os.Stat(filepath.Join(dir, MarkerNoResult)); err == nil {
		return nil, MarkerNoResult, nil
	}
	raw, err = os.ReadFile(filepath.Join(dir, CopiedResult))
	if err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(filepath.Join(dir, MarkerMalformed)); err == nil {
		return raw, MarkerMalformed, nil
	}
	return raw, CopiedResult, nil
}

// Reclaim removes a job directory -- the ONE thing this tool deletes -- and it requires BOTH
// the usage file and the report copy, because the evidence would otherwise be inside the
// thing about to be removed. A copy that does not hash to its REV is refused the same way:
// a persistence that cannot be verified is not a persistence.
func (p *Pool) Reclaim(id, jobDir string) (freed int64, usagePath string, err error) {
	usagePath = p.UsagePath(id)
	if _, statErr := os.Stat(usagePath); statErr != nil {
		return 0, usagePath, fmt.Errorf("no usage file at %s", usagePath)
	}
	raw, kind, readErr := p.RetainedReport(id)
	if readErr != nil {
		return 0, usagePath, fmt.Errorf("no report copy at %s", filepath.Join(p.ReportsDir(id), CopiedResult))
	}
	// A MALFORMED report reclaims ONLY with its marker (SPEC-SWARM.md:1298). The marker is
	// the record of WHY the copy beside it cannot be folded; without it the copy reads as an
	// ordinary report, and removing the job directory would leave a pool that has kept the
	// bytes and lost the reason. Demanded test 12 found this: the marker was deleted and
	// the reclaim printed OK.
	if kind == CopiedResult && ParseReport(raw).Class == ClassMalformed {
		return 0, usagePath, fmt.Errorf("no MALFORMED marker at %s beside a report that is malformed", filepath.Join(p.ReportsDir(id), MarkerMalformed))
	}
	_, want, revErr := p.ReadRev(id)
	if revErr != nil || want == "" {
		return 0, usagePath, fmt.Errorf("report copy does not match REV: no REV at %s", filepath.Join(p.ReportsDir(id), MarkerRev))
	}
	got := HashBytes(raw)
	if kind == MarkerNoResult {
		got = HashBytes(nil)
	}
	if got != want {
		return 0, usagePath, fmt.Errorf("report copy does not match REV at %s", filepath.Join(p.ReportsDir(id), MarkerRev))
	}
	freed = treeBytes(jobDir)
	if err := os.RemoveAll(jobDir); err != nil {
		return 0, usagePath, err
	}
	return freed, usagePath, nil
}

func treeBytes(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			n += info.Size()
		}
		return nil
	})
	return n
}

func dashOr(s string) string {
	if strings.TrimSpace(s) == "" {
		return Dash
	}
	return s
}

func stampOr(t time.Time) string {
	if t.IsZero() {
		return Dash
	}
	return Stamp(t)
}

// rcColumn is the worker's exit code, and a dash for the two ends that have none: an
// outcome with no completion evidence, and a launch that never happened.
func rcColumn(end string, rc int) string {
	if end == EndUnknown || end == EndLaunchFailed || rc < 0 {
		return Dash
	}
	return strconv.Itoa(rc)
}
